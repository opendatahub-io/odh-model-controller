/*
Copyright 2024.

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

package serving

import (
	"context"

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	"github.com/kuadrant/authorino/api/v1beta2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gcustom"
	corev1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"knative.dev/pkg/apis"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	testutils "github.com/opendatahub-io/odh-model-controller/test/utils"
)

var _ = Describe("InferenceGraph Controller", func() {
	Context("When reconciling a Serverless InferenceGraph", func() {
		var testNs string
		var exampleComUrl *apis.URL

		exampleComUrl, _ = apis.ParseURL("https://example.com")
		emptyUrl, _ := apis.ParseURL("")

		buildInferenceGraph := func(name string) kservev1alpha1.InferenceGraph {
			return kservev1alpha1.InferenceGraph{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Namespace:   testNs,
					Name:        name,
					Annotations: make(map[string]string),
				},
				Spec: kservev1alpha1.InferenceGraphSpec{
					Nodes: map[string]kservev1alpha1.InferenceRouter{
						"root": {
							RouterType: kservev1alpha1.Sequence,
							Steps: []kservev1alpha1.InferenceStep{
								{StepName: "one", InferenceTarget: kservev1alpha1.InferenceTarget{
									ServiceName: "isvc",
								}},
							},
						},
					},
				},
			}
		}

		buildInferenceGraphStatus := func(url *apis.URL) kservev1alpha1.InferenceGraphStatus {
			return kservev1alpha1.InferenceGraphStatus{
				URL: url,
			}
		}

		BeforeEach(func() {
			ctx := context.Background()
			testNamespace := testutils.Namespaces.Create(ctx, k8sClient)
			testNs = testNamespace.Name

			inferenceServiceConfig := &corev1.ConfigMap{}
			Expect(testutils.ConvertToStructuredResource(InferenceServiceConfigPath1, inferenceServiceConfig)).To(Succeed())
			if err := k8sClient.Create(ctx, inferenceServiceConfig); err != nil && !k8sErrors.IsAlreadyExists(err) {
				Fail(err.Error())
			}

			// TODO: See utils.VerifyIfMeshAuthorizationIsEnabled func
			if authPolicyErr := createAuthorizationPolicy(KServeAuthorizationPolicy); authPolicyErr != nil && !k8sErrors.IsAlreadyExists(authPolicyErr) {
				Fail(authPolicyErr.Error())
			}
		})

		AfterEach(func() {
			Expect(deleteAuthorizationPolicy(KServeAuthorizationPolicy)).To(Succeed())
		})

		createInferenceGraph := func(inferenceGraph *kservev1alpha1.InferenceGraph, inferenceGraphStatus kservev1alpha1.InferenceGraphStatus) {
			Expect(k8sClient.Create(ctx, inferenceGraph)).Should(Succeed())
			inferenceGraph.Status = inferenceGraphStatus
			Expect(k8sClient.Status().Update(ctx, inferenceGraph)).Should(Succeed())
		}

		createBaseInferenceGraph := func(igName string) *kservev1alpha1.InferenceGraph {
			ig := buildInferenceGraph(igName)
			igStatus := buildInferenceGraphStatus(exampleComUrl)
			createInferenceGraph(&ig, igStatus)

			return &ig
		}

		It("if no auth annotation is present, an anonymous AuthConfig should be created", func() {
			inferenceGraph := createBaseInferenceGraph("no-auth-annotation")
			defer func() { _ = k8sClient.Delete(ctx, inferenceGraph) }()

			Consistently(func(g Gomega) {
				authConfig := &v1beta2.AuthConfig{}
				key := types.NamespacedName{Name: inferenceGraph.Name + "-ig", Namespace: inferenceGraph.Namespace}
				g.Eventually(func() error { return k8sClient.Get(ctx, key, authConfig) }).Should(Succeed())

				g.Expect(authConfig.Spec.Authentication).To(SatisfyAll(
					HaveKey("anonymous-access"),
					Not(HaveKey("kubernetes-user")),
				))
			}).WithPolling(interval).WithTimeout(timeout).Should(Succeed())
		})

		It("if auth is explicitly disabled, an anonymous AuthConfig should be created", func() {
			inferenceGraph := buildInferenceGraph("auth-annotation-disabled")
			inferenceGraph.Annotations[constants.EnableAuthODHAnnotation] = "false"
			createInferenceGraph(&inferenceGraph, buildInferenceGraphStatus(exampleComUrl))
			defer func() { _ = k8sClient.Delete(ctx, &inferenceGraph) }()

			Consistently(func(g Gomega) {
				authConfig := &v1beta2.AuthConfig{}
				key := types.NamespacedName{Name: inferenceGraph.Name + "-ig", Namespace: inferenceGraph.Namespace}
				g.Eventually(func() error { return k8sClient.Get(ctx, key, authConfig) }).Should(Succeed())

				g.Expect(authConfig.Spec.Authentication).To(SatisfyAll(
					HaveKey("anonymous-access"),
					Not(HaveKey("kubernetes-user")),
				))
			}).WithPolling(interval).WithTimeout(timeout).Should(Succeed())
		})

		It("if auth is explicitly enabled, a user-defined AuthConfig should be created", func() {
			inferenceGraph := buildInferenceGraph("auth-annotation-enabled")
			inferenceGraph.Annotations[constants.EnableAuthODHAnnotation] = "true"
			createInferenceGraph(&inferenceGraph, buildInferenceGraphStatus(exampleComUrl))
			defer func() { _ = k8sClient.Delete(ctx, &inferenceGraph) }()

			Consistently(func(g Gomega) {
				authConfig := &v1beta2.AuthConfig{}
				key := types.NamespacedName{Name: inferenceGraph.Name + "-ig", Namespace: inferenceGraph.Namespace}
				g.Eventually(func() error { return k8sClient.Get(ctx, key, authConfig) }).Should(Succeed())

				g.Expect(authConfig.Spec.Authentication).To(SatisfyAll(
					Not(HaveKey("anonymous-access")),
					HaveKey("kubernetes-user"),
				))
			}).WithPolling(interval).WithTimeout(timeout).Should(Succeed())
		})

		It("if auth is explicitly enabled but InferenceGraph is not ready, no AuthConfig should be created", func() {
			inferenceGraph := buildInferenceGraph("auth-annotation-enabled-ig-not-ready")
			inferenceGraph.Annotations[constants.EnableAuthODHAnnotation] = "true"
			createInferenceGraph(&inferenceGraph, buildInferenceGraphStatus(emptyUrl))
			defer func() { _ = k8sClient.Delete(ctx, &inferenceGraph) }()

			Consistently(func() error {
				authConfig := &v1beta2.AuthConfig{}
				key := types.NamespacedName{Name: inferenceGraph.Name + "-ig", Namespace: inferenceGraph.Namespace}
				return k8sClient.Get(ctx, key, authConfig)
			}).WithPolling(interval).WithTimeout(timeout).Should(WithTransform(k8sErrors.IsNotFound, BeTrue()))
		})

		It("if auth is explicitly enabled but Authorino is not configured, no AuthConfig should be created", func() {
			inferenceGraph := buildInferenceGraph("auth-annotation-enabled-authorino-missing")
			inferenceGraph.Annotations[constants.EnableAuthODHAnnotation] = "true"
			createInferenceGraph(&inferenceGraph, buildInferenceGraphStatus(exampleComUrl))
			defer func() { _ = k8sClient.Delete(ctx, &inferenceGraph) }()

			// Deleting the authorization policy means Authorino is not available
			Expect(deleteAuthorizationPolicy(KServeAuthorizationPolicy)).To(Succeed())

			Consistently(func() error {
				authConfig := &v1beta2.AuthConfig{}
				key := types.NamespacedName{Name: inferenceGraph.Name + "-ig", Namespace: inferenceGraph.Namespace}
				return k8sClient.Get(ctx, key, authConfig)
			}).WithPolling(interval).WithTimeout(timeout).Should(WithTransform(k8sErrors.IsNotFound, BeTrue()))

			eventMatcher := gcustom.MakeMatcher(func(actual corev1.Event) (bool, error) {
				if actual.InvolvedObject.Kind != "InferenceGraph" ||
					actual.InvolvedObject.Namespace != inferenceGraph.GetNamespace() ||
					actual.InvolvedObject.Name != inferenceGraph.GetName() ||
					actual.Reason != constants.AuthUnavailable {
					return false, nil
				}
				return true, nil
			}).WithMessage("have InvolvedObject.Kind = InferenceGraph and Reason = " + constants.AuthUnavailable)

			Eventually(func(g Gomega) {
				events := corev1.EventList{}
				g.Expect(k8sClient.List(ctx, &events, client.InNamespace(inferenceGraph.GetNamespace()))).To(Succeed())
				g.Expect(events.Items).NotTo(BeEmpty())
				g.Expect(events.Items).To(ContainElement(eventMatcher))
			}).WithPolling(interval).WithTimeout(timeout).Should(Succeed())
		})
	})
})
