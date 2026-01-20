/*

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

package reconcilers

import (
	"errors"

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"knative.dev/pkg/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"

	controllerutils "github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
)

var _ = Describe("LLMRoleBindingReconciler", func() {
	var scheme *runtime.Scheme
	var fakeRecorder *record.FakeRecorder

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(kservev1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(v1.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		controllerutils.RegisterSchemes(scheme)
		// Create a fake event recorder for tests
		fakeRecorder = record.NewFakeRecorder(100)
	})

	Describe("createDesiredResource", func() {
		When("no annotation is present", func() {
			It("should not create a RoleBinding", func(ctx SpecContext) {
				llmisvc := &kservev1alpha1.LLMInferenceService{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-llm",
						Namespace: "test-namespace",
					},
				}

				client := fake.NewClientBuilder().WithScheme(scheme).Build()
				reconciler := NewLLMRoleBindingReconciler(client, fakeRecorder)

				roleBinding := reconciler.createDesiredResource(ctx, log.Log, llmisvc)
				Expect(roleBinding).To(BeNil())
			})
		})

		When("tier annotation is present", func() {
			It("should create a RoleBinding with subjects for specified tiers", func(ctx SpecContext) {
				tierConfigMap := createTierConfigMap(`
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
`)

				llmisvc := &kservev1alpha1.LLMInferenceService{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-llm",
						Namespace: "test-namespace",
						Annotations: map[string]string{
							TierAnnotationKey: `["free", "premium"]`,
						},
					},
				}

				client := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(tierConfigMap).
					Build()

				reconciler := NewLLMRoleBindingReconciler(client, fakeRecorder)
				roleBinding := reconciler.createDesiredResource(ctx, log.Log, llmisvc)

				expectedGroups := []string{
					"system:serviceaccounts:maas-default-gateway-tier-free",
					"system:serviceaccounts:maas-default-gateway-tier-premium",
				}

				Expect(roleBinding.Subjects).To(HaveLen(len(expectedGroups)))
				for i, subject := range roleBinding.Subjects {
					Expect(subject.Kind).To(Equal("Group"))
					Expect(subject.Name).To(Equal(expectedGroups[i]))
				}
			})
		})

		When("tier annotation contains empty array", func() {
			It("should create a RoleBinding with subjects for all tiers", func(ctx SpecContext) {
				tierConfigMap := createTierConfigMap(`
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
- name: enterprise
  description: Enterprise tier
  level: 20
  groups:
  - enterprise-users
`)

				llmisvc := &kservev1alpha1.LLMInferenceService{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-llm",
						Namespace: "test-namespace",
						Annotations: map[string]string{
							TierAnnotationKey: `[]`,
						},
					},
				}

				client := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(tierConfigMap).
					Build()

				reconciler := NewLLMRoleBindingReconciler(client, fakeRecorder)
				roleBinding := reconciler.createDesiredResource(ctx, log.Log, llmisvc)

				expectedGroups := []string{
					"system:serviceaccounts:maas-default-gateway-tier-free",
					"system:serviceaccounts:maas-default-gateway-tier-premium",
					"system:serviceaccounts:maas-default-gateway-tier-enterprise",
				}

				Expect(roleBinding.Subjects).To(HaveLen(len(expectedGroups)))
				for i, subject := range roleBinding.Subjects {
					Expect(subject.Kind).To(Equal("Group"))
					Expect(subject.Name).To(Equal(expectedGroups[i]))
				}
			})
		})

		When("tier annotation is present but ConfigMap is missing", func() {
			It("should not create a RoleBinding and emit TierConfigUnavailable event", func(ctx SpecContext) {
				llmisvc := &kservev1alpha1.LLMInferenceService{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-llm",
						Namespace: "test-namespace",
						Annotations: map[string]string{
							TierAnnotationKey: `["free"]`,
						},
					},
				}

				client := fake.NewClientBuilder().WithScheme(scheme).Build()
				reconciler := NewLLMRoleBindingReconciler(client, fakeRecorder)

				roleBinding := reconciler.createDesiredResource(ctx, log.Log, llmisvc)

				Expect(roleBinding).To(BeNil())
				Eventually(fakeRecorder.Events).Should(Receive(ContainSubstring(EventReasonTierConfigUnavail)))
			})
		})

		When("requested tier does not exist in ConfigMap", func() {
			It("should not create a RoleBinding and emit TierNotFound event", func(ctx SpecContext) {
				tierConfigMap := createTierConfigMap(`
- name: free
  level: 1
  groups:
  - system:authenticated
`)

				llmisvc := &kservev1alpha1.LLMInferenceService{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-llm",
						Namespace: "test-namespace",
						Annotations: map[string]string{
							TierAnnotationKey: `["nonexistent"]`,
						},
					},
				}

				client := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(tierConfigMap).
					Build()

				reconciler := NewLLMRoleBindingReconciler(client, fakeRecorder)

				roleBinding := reconciler.createDesiredResource(ctx, log.Log, llmisvc)

				Expect(roleBinding).To(BeNil())
				Eventually(fakeRecorder.Events).Should(Receive(ContainSubstring(EventReasonTierNotFound)))
			})
		})
	})

	Describe("Reconcile", func() {

		When("tier annotation is present", func() {

			It("should create a RoleBinding on first reconcile", func(ctx SpecContext) {
				tierConfigMap := createTierConfigMap(`
- name: free
  description: Free tier
  level: 1
  groups:
  - system:authenticated
`)

				llmisvc := &kservev1alpha1.LLMInferenceService{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-llm",
						Namespace: "test-namespace",
						Annotations: map[string]string{
							TierAnnotationKey: `["free"]`,
						},
					},
					Spec: kservev1alpha1.LLMInferenceServiceSpec{},
				}

				client := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(llmisvc, tierConfigMap).
					Build()

				reconciler := NewLLMRoleBindingReconciler(client, fakeRecorder)

				err := reconciler.Reconcile(ctx, log.Log, llmisvc)
				Expect(err).NotTo(HaveOccurred())

				roleBinding := &v1.RoleBinding{}
				err = client.Get(ctx, k8stypes.NamespacedName{
					Name:      controllerutils.GetMaaSRoleBindingName(llmisvc),
					Namespace: "test-namespace",
				}, roleBinding)
				Expect(err).NotTo(HaveOccurred())
			})

			It("should be idempotent on subsequent reconciles", func(ctx SpecContext) {
				tierConfigMap := createTierConfigMap(`
- name: free
  description: Free tier
  level: 1
  groups:
  - system:authenticated
`)

				llmisvc := &kservev1alpha1.LLMInferenceService{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-llm",
						Namespace: "test-namespace",
						Annotations: map[string]string{
							TierAnnotationKey: `["free"]`,
						},
					},
					Spec: kservev1alpha1.LLMInferenceServiceSpec{},
				}

				client := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(llmisvc, tierConfigMap).
					Build()

				reconciler := NewLLMRoleBindingReconciler(client, fakeRecorder)

				err := reconciler.Reconcile(ctx, log.Log, llmisvc)
				Expect(err).NotTo(HaveOccurred())

				err = reconciler.Reconcile(ctx, log.Log, llmisvc)
				Expect(err).NotTo(HaveOccurred())
			})
		})

		When("tier annotation is removed", func() {
			It("should delete existing RoleBinding", func(ctx SpecContext) {
				llmisvc := &kservev1alpha1.LLMInferenceService{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-llm",
						Namespace: "test-namespace",
						UID:       "test-uid",
					},
					Spec: kservev1alpha1.LLMInferenceServiceSpec{},
				}

				existingRoleBinding := &v1.RoleBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name:      controllerutils.GetMaaSRoleBindingName(llmisvc),
						Namespace: "test-namespace",
						Labels: map[string]string{
							"app.kubernetes.io/managed-by": "odh-model-controller",
						},
						OwnerReferences: []metav1.OwnerReference{
							{
								APIVersion: llmisvc.APIVersion,
								Kind:       llmisvc.Kind,
								Name:       llmisvc.Name,
								UID:        llmisvc.UID,
								Controller: ptr.Bool(true),
							},
						},
					},
					Subjects: []v1.Subject{
						{
							Kind:     "Group",
							Name:     "system:serviceaccounts:maas-default-gateway-tier-free",
							APIGroup: "rbac.authorization.k8s.io",
						},
					},
					RoleRef: v1.RoleRef{
						Kind:     "Role",
						Name:     controllerutils.GetMaaSRoleName(llmisvc),
						APIGroup: "rbac.authorization.k8s.io",
					},
				}

				client := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(llmisvc, existingRoleBinding).
					Build()

				reconciler := NewLLMRoleBindingReconciler(client, fakeRecorder)

				err := reconciler.Reconcile(ctx, log.Log, llmisvc)
				Expect(err).NotTo(HaveOccurred())

				roleBinding := &v1.RoleBinding{}
				err = client.Get(ctx, k8stypes.NamespacedName{
					Name:      controllerutils.GetMaaSRoleBindingName(llmisvc),
					Namespace: "test-namespace",
				}, roleBinding)
				Expect(err).To(HaveOccurred())
			})
		})
	})

	Describe("getTierSubjects", func() {
		When("no annotations are present", func() {
			It("should return nil subjects and no error", func(ctx SpecContext) {
				llmisvc := &kservev1alpha1.LLMInferenceService{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-llm",
						Namespace: "test-namespace",
					},
				}

				client := fake.NewClientBuilder().WithScheme(scheme).Build()
				reconciler := NewLLMRoleBindingReconciler(client, fakeRecorder)

				subjects, err := reconciler.getTierSubjects(ctx, log.Log, llmisvc)

				Expect(err).NotTo(HaveOccurred())
				Expect(subjects).To(BeNil())
			})
		})

		When("annotation is present but ConfigMap is missing", func() {
			It("should return error", func(ctx SpecContext) {
				llmisvc := &kservev1alpha1.LLMInferenceService{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-llm",
						Namespace: "test-namespace",
						Annotations: map[string]string{
							TierAnnotationKey: `["free"]`,
						},
					},
				}

				client := fake.NewClientBuilder().WithScheme(scheme).Build()
				reconciler := NewLLMRoleBindingReconciler(client, fakeRecorder)

				subjects, err := reconciler.getTierSubjects(ctx, log.Log, llmisvc)

				Expect(err).To(HaveOccurred())
				Expect(subjects).To(BeNil())
			})
		})

		When("annotation and ConfigMap are present", func() {
			It("should return subjects for specified tiers", func(ctx SpecContext) {
				tierConfigMap := createTierConfigMap(`
- name: free
  level: 1
- name: premium
  level: 10
`)

				llmisvc := &kservev1alpha1.LLMInferenceService{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-llm",
						Namespace: "test-namespace",
						Annotations: map[string]string{
							TierAnnotationKey: `["free", "premium"]`,
						},
					},
				}

				client := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(tierConfigMap).
					Build()

				reconciler := NewLLMRoleBindingReconciler(client, fakeRecorder)
				subjects, err := reconciler.getTierSubjects(ctx, log.Log, llmisvc)

				Expect(err).NotTo(HaveOccurred())
				Expect(subjects).To(HaveLen(2))
			})
		})

		When("requested tier does not exist in ConfigMap", func() {
			It("should return TierNotFoundError", func(ctx SpecContext) {
				tierConfigMap := createTierConfigMap(`
- name: free
  level: 1
`)

				llmisvc := &kservev1alpha1.LLMInferenceService{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-llm",
						Namespace: "test-namespace",
						Annotations: map[string]string{
							TierAnnotationKey: `["nonexistent"]`,
						},
					},
				}

				client := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(tierConfigMap).
					Build()

				reconciler := NewLLMRoleBindingReconciler(client, fakeRecorder)
				subjects, err := reconciler.getTierSubjects(ctx, log.Log, llmisvc)

				Expect(err).To(HaveOccurred())
				var tierNotFoundErr *TierNotFoundError
				Expect(errors.As(err, &tierNotFoundErr)).To(BeTrue())
				Expect(tierNotFoundErr.MissingTiers).To(ContainElement("nonexistent"))
				Expect(subjects).To(BeNil())
			})
		})

		When("existing RoleBinding has opendatahub.io/managed=false", func() {
			It("should skip reconciliation and not modify the RoleBinding", func(ctx SpecContext) {
				tierConfigMap := createTierConfigMap(`
- name: free
  level: 1
  groups:
  - system:authenticated
`)

				llmisvc := &kservev1alpha1.LLMInferenceService{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-llm",
						Namespace: "test-namespace",
						UID:       "test-uid",
						Annotations: map[string]string{
							TierAnnotationKey: `["free"]`,
						},
					},
				}

				existingRoleBinding := &v1.RoleBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name:      controllerutils.GetMaaSRoleBindingName(llmisvc),
						Namespace: "test-namespace",
						Labels: map[string]string{
							"opendatahub.io/managed": "false",
						},
					},
					Subjects: []v1.Subject{
						{Kind: "Group", Name: "custom-group", APIGroup: "rbac.authorization.k8s.io"},
					},
					RoleRef: v1.RoleRef{
						Kind:     "Role",
						Name:     "custom-role",
						APIGroup: "rbac.authorization.k8s.io",
					},
				}

				client := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(llmisvc, tierConfigMap, existingRoleBinding).
					Build()

				reconciler := NewLLMRoleBindingReconciler(client, fakeRecorder)
				err := reconciler.Reconcile(ctx, log.Log, llmisvc)
				Expect(err).NotTo(HaveOccurred())

				roleBinding := &v1.RoleBinding{}
				err = client.Get(ctx, k8stypes.NamespacedName{
					Name:      controllerutils.GetMaaSRoleBindingName(llmisvc),
					Namespace: "test-namespace",
				}, roleBinding)
				Expect(err).NotTo(HaveOccurred())
				Expect(roleBinding.Subjects).To(HaveLen(1))
				Expect(roleBinding.Subjects[0].Name).To(Equal("custom-group"))
				Expect(roleBinding.RoleRef.Name).To(Equal("custom-role"))
				Expect(fakeRecorder.Events).To(BeEmpty())
			})
		})
	})
})

func createTierConfigMap(tiersYAML string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      TierConfigMapName,
			Namespace: DefaultTenantNamespace,
		},
		Data: map[string]string{
			"tiers": tiersYAML,
		},
	}
}
