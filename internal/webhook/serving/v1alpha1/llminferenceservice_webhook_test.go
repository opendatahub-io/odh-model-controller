/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/serving/reconcilers"

	testutils "github.com/opendatahub-io/odh-model-controller/test/utils"
)

var _ = Describe("LLMInferenceService Webhook", func() {
	Context("When creating LLMInferenceService", func() {
		var testNs string

		BeforeEach(func() {
			testNamespace := testutils.Namespaces.Create(ctx, k8sClient)
			testNs = testNamespace.Name

			// Create the maas-api namespace required for tier ConfigMap lookups
			maasNs := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: reconcilers.DefaultTenantNamespace,
				},
			}
			Expect(k8sClient.Create(ctx, maasNs)).To(Or(Succeed(), WithTransform(k8sErrors.IsAlreadyExists, BeTrue())))
		})

		Context("Without tier annotation", func() {
			It("Should be created successfully", func() {
				llmisvc := &kservev1alpha1.LLMInferenceService{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-llm-no-annotation",
						Namespace: testNs,
					},
					Spec: kservev1alpha1.LLMInferenceServiceSpec{},
				}

				Expect(k8sClient.Create(ctx, llmisvc)).Should(Succeed())
			})
		})

		Context("With valid tier annotation", func() {
			var tierConfigMap *corev1.ConfigMap

			BeforeEach(func() {
				tierConfigMap = &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      TierConfigMapName,
						Namespace: reconcilers.DefaultTenantNamespace,
					},
					Data: map[string]string{
						"tiers": `
- name: free
  description: Free tier
  level: 1
  groups:
  - system:authenticated
- name: premium
  description: Premium tier
  level: 10
  groups:
  - premium-users
`,
					},
				}
				Expect(k8sClient.Create(ctx, tierConfigMap)).Should(Succeed())

				// Wait for ConfigMap to be available for reading to avoid race conditions
				Eventually(func() error {
					cm := &corev1.ConfigMap{}
					return k8sClient.Get(ctx, client.ObjectKeyFromObject(tierConfigMap), cm)
				}).Should(Succeed())
			})

			AfterEach(func() {
				_ = k8sClient.Delete(ctx, tierConfigMap)
			})

			It("Should be created successfully with valid tiers", func() {
				llmisvc := &kservev1alpha1.LLMInferenceService{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-llm-valid",
						Namespace: testNs,
						Annotations: map[string]string{
							TierAnnotationKey: `["free", "premium"]`,
						},
					},
					Spec: kservev1alpha1.LLMInferenceServiceSpec{},
				}

				Expect(k8sClient.Create(ctx, llmisvc)).Should(Succeed())
			})

			It("Should be created successfully with empty array (all tiers)", func() {
				llmisvc := &kservev1alpha1.LLMInferenceService{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-llm-all",
						Namespace: testNs,
						Annotations: map[string]string{
							TierAnnotationKey: `[]`,
						},
					},
					Spec: kservev1alpha1.LLMInferenceServiceSpec{},
				}

				Expect(k8sClient.Create(ctx, llmisvc)).Should(Succeed())
			})
		})

		Context("With invalid tier annotation", func() {
			It("Should reject invalid JSON with helpful message", func() {
				llmisvc := &kservev1alpha1.LLMInferenceService{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-llm-invalid-json",
						Namespace: testNs,
						Annotations: map[string]string{
							TierAnnotationKey: `{invalid json}`,
						},
					},
					Spec: kservev1alpha1.LLMInferenceServiceSpec{},
				}

				err := k8sClient.Create(ctx, llmisvc)
				Expect(err).Should(HaveOccurred())
				Expect(k8sErrors.IsInvalid(err)).Should(BeTrue())
				Expect(err.Error()).Should(ContainSubstring("Invalid JSON format"))
				Expect(err.Error()).Should(ContainSubstring("Expected format"))
			})

			It("Should reject missing tier ConfigMap with helpful message", func() {
				llmisvc := &kservev1alpha1.LLMInferenceService{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-llm-no-configmap",
						Namespace: testNs,
						Annotations: map[string]string{
							TierAnnotationKey: `["free"]`,
						},
					},
					Spec: kservev1alpha1.LLMInferenceServiceSpec{},
				}

				err := k8sClient.Create(ctx, llmisvc)
				Expect(err).Should(HaveOccurred())
				Expect(k8sErrors.IsInvalid(err)).Should(BeTrue())
				Expect(err.Error()).Should(ContainSubstring("ConfigMap"))
				Expect(err.Error()).Should(ContainSubstring("not found"))
			})

			It("Should reject invalid tier names with list of available tiers", func() {
				tierConfigMap := &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      TierConfigMapName,
						Namespace: reconcilers.DefaultTenantNamespace,
					},
					Data: map[string]string{
						"tiers": `
- name: free
  level: 1
- name: premium
  level: 10
`,
					},
				}
				Expect(k8sClient.Create(ctx, tierConfigMap)).Should(Succeed())
				defer func() { _ = k8sClient.Delete(ctx, tierConfigMap) }()

				// Wait for ConfigMap to be available for reading to avoid race conditions
				Eventually(func() error {
					cm := &corev1.ConfigMap{}
					return k8sClient.Get(ctx, client.ObjectKeyFromObject(tierConfigMap), cm)
				}).Should(Succeed())

				llmisvc := &kservev1alpha1.LLMInferenceService{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-llm-invalid-tier",
						Namespace: testNs,
						Annotations: map[string]string{
							TierAnnotationKey: `["invalid-tier"]`,
						},
					},
					Spec: kservev1alpha1.LLMInferenceServiceSpec{},
				}

				err := k8sClient.Create(ctx, llmisvc)
				Expect(err).Should(HaveOccurred())
				Expect(k8sErrors.IsInvalid(err)).Should(BeTrue())
				Expect(err.Error()).Should(ContainSubstring("not found in tier configuration"))
				Expect(err.Error()).Should(ContainSubstring("Available tiers"))
				Expect(err.Error()).Should(SatisfyAll(
					ContainSubstring("free"),
					ContainSubstring("premium"),
				))
			})
		})

		Context("When updating LLMInferenceService", func() {
			var tierConfigMap *corev1.ConfigMap

			BeforeEach(func() {
				tierConfigMap = &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      TierConfigMapName,
						Namespace: reconcilers.DefaultTenantNamespace,
					},
					Data: map[string]string{
						"tiers": `
- name: free
  level: 1
- name: premium
  level: 10
`,
					},
				}
				Expect(k8sClient.Create(ctx, tierConfigMap)).Should(Succeed())

				// Wait for ConfigMap to be available for reading to avoid race conditions
				Eventually(func() error {
					cm := &corev1.ConfigMap{}
					return k8sClient.Get(ctx, client.ObjectKeyFromObject(tierConfigMap), cm)
				}).Should(Succeed())
			})

			AfterEach(func() {
				_ = k8sClient.Delete(ctx, tierConfigMap)
			})

			It("Should allow adding valid tier annotation", func() {
				llmisvc := &kservev1alpha1.LLMInferenceService{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-llm-update-add",
						Namespace: testNs,
					},
					Spec: kservev1alpha1.LLMInferenceServiceSpec{},
				}
				Expect(k8sClient.Create(ctx, llmisvc)).Should(Succeed())

				if llmisvc.Annotations == nil {
					llmisvc.Annotations = make(map[string]string)
				}
				llmisvc.Annotations[TierAnnotationKey] = `["free"]`

				Expect(k8sClient.Update(ctx, llmisvc)).Should(Succeed())
			})

			It("Should reject invalid tier annotation on update", func() {
				llmisvc := &kservev1alpha1.LLMInferenceService{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-llm-update-invalid",
						Namespace: testNs,
					},
					Spec: kservev1alpha1.LLMInferenceServiceSpec{},
				}
				Expect(k8sClient.Create(ctx, llmisvc)).Should(Succeed())

				if llmisvc.Annotations == nil {
					llmisvc.Annotations = make(map[string]string)
				}
				llmisvc.Annotations[TierAnnotationKey] = `["invalid"]`

				err := k8sClient.Update(ctx, llmisvc)
				Expect(err).Should(HaveOccurred())
				Expect(k8sErrors.IsInvalid(err)).Should(BeTrue())
			})

			It("Should allow removing tier annotation", func() {
				llmisvc := &kservev1alpha1.LLMInferenceService{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-llm-update-remove",
						Namespace: testNs,
						Annotations: map[string]string{
							TierAnnotationKey: `["free"]`,
						},
					},
					Spec: kservev1alpha1.LLMInferenceServiceSpec{},
				}
				Expect(k8sClient.Create(ctx, llmisvc)).Should(Succeed())

				delete(llmisvc.Annotations, TierAnnotationKey)
				Expect(k8sClient.Update(ctx, llmisvc)).Should(Succeed())
			})

			It("Should allow updating tier annotation to different tiers", func() {
				llmisvc := &kservev1alpha1.LLMInferenceService{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-llm-update-change",
						Namespace: testNs,
						Annotations: map[string]string{
							TierAnnotationKey: `["free"]`,
						},
					},
					Spec: kservev1alpha1.LLMInferenceServiceSpec{},
				}
				Expect(k8sClient.Create(ctx, llmisvc)).Should(Succeed())

				llmisvc.Annotations[TierAnnotationKey] = `["premium"]`
				Expect(k8sClient.Update(ctx, llmisvc)).Should(Succeed())
			})

			It("Should allow updating from specific tiers to all tiers (empty array)", func() {
				llmisvc := &kservev1alpha1.LLMInferenceService{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-llm-update-to-all",
						Namespace: testNs,
						Annotations: map[string]string{
							TierAnnotationKey: `["free"]`,
						},
					},
					Spec: kservev1alpha1.LLMInferenceServiceSpec{},
				}
				Expect(k8sClient.Create(ctx, llmisvc)).Should(Succeed())

				llmisvc.Annotations[TierAnnotationKey] = `[]`
				Expect(k8sClient.Update(ctx, llmisvc)).Should(Succeed())
			})
		})
	})
})
