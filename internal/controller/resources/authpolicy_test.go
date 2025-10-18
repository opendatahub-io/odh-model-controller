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

package resources_test

import (
	"os"

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kuadrantv1 "github.com/kuadrant/kuadrant-operator/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ocpconfigv1 "github.com/openshift/api/config/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwapiv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/resources"
)

var _ = Describe("AuthPolicyDetector", func() {
	var detector resources.AuthPolicyDetector

	BeforeEach(func() {
		detector = resources.NewKServeAuthPolicyDetector(nil)
	})

	It("should return UserDefined when annotation is 'true'", func(ctx SpecContext) {
		annotations := map[string]string{
			constants.EnableAuthODHAnnotation: "true",
		}

		result := detector.Detect(ctx, annotations)

		Expect(result).To(Equal(constants.UserDefined))
	})

	It("should return Anonymous when annotation is 'false'", func(ctx SpecContext) {
		annotations := map[string]string{
			constants.EnableAuthODHAnnotation: "false",
		}

		result := detector.Detect(ctx, annotations)

		Expect(result).To(Equal(constants.Anonymous))
	})

	It("should return UserDefined when annotation is empty string", func(ctx SpecContext) {
		annotations := map[string]string{
			constants.EnableAuthODHAnnotation: "",
		}

		result := detector.Detect(ctx, annotations)

		Expect(result).To(Equal(constants.UserDefined))
	})

	It("should return UserDefined when annotation does not exist", func(ctx SpecContext) {
		annotations := map[string]string{}

		result := detector.Detect(ctx, annotations)

		Expect(result).To(Equal(constants.UserDefined))
	})

	It("should be case-insensitive for 'true' value", func(ctx SpecContext) {
		testCases := []string{"TRUE", "True", "tRuE"}

		for _, value := range testCases {
			annotations := map[string]string{
				constants.EnableAuthODHAnnotation: value,
			}

			result := detector.Detect(ctx, annotations)

			Expect(result).To(Equal(constants.UserDefined), "Expected UserDefined for case variation: %s", value)
		}
	})

	It("should be case-insensitive for 'false' value", func(ctx SpecContext) {
		testCases := []string{"FALSE", "False", "fAlSe"}

		for _, value := range testCases {
			annotations := map[string]string{
				constants.EnableAuthODHAnnotation: value,
			}

			result := detector.Detect(ctx, annotations)

			Expect(result).To(Equal(constants.Anonymous), "Expected Anonymous for case variation: %s", value)
		}
	})

	It("should return UserDefined for any other invalid values", func(ctx SpecContext) {
		testCases := []string{"yes", "1", "enabled", "on", "invalid", "123"}

		for _, value := range testCases {
			annotations := map[string]string{
				constants.EnableAuthODHAnnotation: value,
			}

			result := detector.Detect(ctx, annotations)

			Expect(result).To(Equal(constants.UserDefined), "Expected UserDefined for invalid value: %s", value)
		}
	})
})

var _ = Describe("AuthPolicyTemplateLoader", func() {
	Context("Template loading", func() {
		var loader resources.AuthPolicyTemplateLoader
		var dummyLLMISvc kservev1alpha1.LLMInferenceService
		var fakeClient client.Client
		BeforeEach(func() {
			scheme := runtime.NewScheme()
			Expect(kservev1alpha1.AddToScheme(scheme)).To(Succeed())
			Expect(ocpconfigv1.AddToScheme(scheme)).To(Succeed())
			Expect(gwapiv1alpha2.Install(scheme)).To(Succeed())
			Expect(gatewayapiv1.Install(scheme)).To(Succeed())

			// Create default gateway that tests expect
			defaultGateway := &gatewayapiv1.Gateway{
				ObjectMeta: v1.ObjectMeta{
					Name:      "openshift-ai-inference",
					Namespace: "openshift-ingress",
				},
			}

			fakeClient = fake.NewClientBuilder().WithScheme(scheme).WithObjects(defaultGateway).Build()
			loader = resources.NewKServeAuthPolicyTemplateLoader(fakeClient)

			dummyLLMISvc = kservev1alpha1.LLMInferenceService{
				ObjectMeta: v1.ObjectMeta{
					Namespace: "test-ns",
					Name:      "test-llm",
				},
			}
		})

		It("should resolve UserDefined template for LLMInferenceService", func(ctx SpecContext) {
			authPolicies, err := loader.Load(
				ctx,
				constants.UserDefined,
				&dummyLLMISvc)

			Expect(err).ToNot(HaveOccurred())
			Expect(authPolicies).ToNot(BeNil())
			Expect(authPolicies).ToNot(BeEmpty())
			Expect(authPolicies[0].GetName()).To(Equal(constants.GetGatewayAuthPolicyName("openshift-ai-inference")))
			Expect(authPolicies[0].GetNamespace()).To(Equal("openshift-ingress"))
		})

		It("should resolve Anonymous template for LLMInferenceService", func(ctx SpecContext) {
			authPolicies, err := loader.Load(
				ctx,
				constants.Anonymous,
				&dummyLLMISvc)
			Expect(err).ToNot(HaveOccurred())
			Expect(authPolicies).ToNot(BeNil())
			Expect(authPolicies).To(HaveLen(1))
			httpRouteName := string(authPolicies[0].Spec.TargetRef.Name)
			Expect(authPolicies[0].GetName()).To(Equal(constants.GetHTTPRouteAuthPolicyName(httpRouteName)))
			Expect(authPolicies[0].GetNamespace()).To(Equal(dummyLLMISvc.Namespace))
		})

		It("should return error for unsupported auth type", func(ctx SpecContext) {
			authPolicies, err := loader.Load(
				ctx,
				constants.AuthType("unsupported-type"),
				&dummyLLMISvc)

			Expect(err).To(HaveOccurred())
			Expect(authPolicies).To(BeNil())
			Expect(err.Error()).To(ContainSubstring("unsupported AuthPolicy type"))
		})

		It("should read AUTH_AUDIENCE env var for Audience", func(ctx SpecContext) {
			_ = os.Setenv("AUTH_AUDIENCE", "http://test.com")
			defer func() {
				_ = os.Unsetenv("AUTH_AUDIENCE")
			}()

			authPolicies, err := loader.Load(
				ctx,
				constants.UserDefined,
				&dummyLLMISvc)

			Expect(err).ToNot(HaveOccurred())
			Expect(authPolicies).ToNot(BeNil())
			Expect(authPolicies).ToNot(BeEmpty())

			Expect(authPolicies[0].Name).To(ContainSubstring("authn"))
			Expect(string(authPolicies[0].Spec.TargetRef.Kind)).To(Equal("Gateway"))
			Expect(authPolicies[0].Spec.AuthScheme.Authentication["kubernetes-user"].KubernetesTokenReview.Audiences).To(ContainElement("http://test.com"))
		})

		It("should use serviceAccountIssuer from Authentication cluster object (ROSA) for Audience", func(ctx SpecContext) {
			testIssuer := "https://test.com/23c734st3pn7l167mq97d0ot8848lgrl"
			ocpAuthentication := &ocpconfigv1.Authentication{
				ObjectMeta: v1.ObjectMeta{
					Name: "cluster",
				},
				Spec: ocpconfigv1.AuthenticationSpec{
					ServiceAccountIssuer: testIssuer,
				},
			}

			err := fakeClient.Create(ctx, ocpAuthentication)
			Expect(err).ToNot(HaveOccurred())

			authPolicies, err := loader.Load(
				ctx,
				constants.UserDefined,
				&dummyLLMISvc)

			Expect(err).ToNot(HaveOccurred())
			Expect(authPolicies).ToNot(BeNil())
			Expect(authPolicies).ToNot(BeEmpty())

			Expect(authPolicies[0].Name).To(ContainSubstring("authn"))
			Expect(string(authPolicies[0].Spec.TargetRef.Kind)).To(Equal("Gateway"))
			Expect(authPolicies[0].Spec.AuthScheme.Authentication["kubernetes-user"].KubernetesTokenReview.Audiences).To(ConsistOf(testIssuer))
		})
	})

	Context("Gateway managed annotation filtering", func() {
		var loader resources.AuthPolicyTemplateLoader
		var fakeClient client.Client
		var scheme *runtime.Scheme

		BeforeEach(func() {
			scheme = runtime.NewScheme()
			Expect(kservev1alpha1.AddToScheme(scheme)).To(Succeed())
			Expect(ocpconfigv1.AddToScheme(scheme)).To(Succeed())
			Expect(gwapiv1alpha2.Install(scheme)).To(Succeed())
			Expect(gatewayapiv1.Install(scheme)).To(Succeed())
		})

		It("should exclude gateway with opendatahub.io/managed=false label", func(ctx SpecContext) {
			gateway := &gatewayapiv1.Gateway{
				ObjectMeta: v1.ObjectMeta{
					Name:      "test-gateway",
					Namespace: "test-gateway-ns",
					Labels: map[string]string{
						constants.ODHManagedLabel: "false",
					},
				},
			}

			fakeClient = fake.NewClientBuilder().WithScheme(scheme).WithObjects(gateway).Build()
			loader = resources.NewKServeAuthPolicyTemplateLoader(fakeClient)

			llmisvc := &kservev1alpha1.LLMInferenceService{
				ObjectMeta: v1.ObjectMeta{
					Namespace: "test-ns",
					Name:      "test-llm",
				},
				Spec: kservev1alpha1.LLMInferenceServiceSpec{
					Router: &kservev1alpha1.RouterSpec{
						Gateway: &kservev1alpha1.GatewaySpec{
							Refs: []kservev1alpha1.UntypedObjectReference{
								{
									Name:      "test-gateway",
									Namespace: "test-gateway-ns",
								},
							},
						},
					},
				},
			}

			authPolicies, err := loader.Load(ctx, constants.UserDefined, llmisvc)

			Expect(err).ToNot(HaveOccurred())
			Expect(authPolicies).To(BeEmpty(), "Expected no AuthPolicy for gateway with managed=false")
		})
	})
})

func createTestAuthPolicy() *kuadrantv1.AuthPolicy {
	return &kuadrantv1.AuthPolicy{
		ObjectMeta: v1.ObjectMeta{
			Name:      constants.GetHTTPRouteAuthPolicyName("test-llm"),
			Namespace: "test-namespace",
		},
		Spec: kuadrantv1.AuthPolicySpec{
			TargetRef: gwapiv1alpha2.LocalPolicyTargetReferenceWithSectionName{
				LocalPolicyTargetReference: gwapiv1alpha2.LocalPolicyTargetReference{
					Group: "gateway.networking.k8s.io",
					Kind:  "HTTPRoute",
					Name:  "test-llm-kserve-route",
				},
			},
		},
	}
}

var _ = Describe("AuthPolicyStore", func() {
	var store resources.AuthPolicyStore
	var fakeClient client.Client

	BeforeEach(func() {
		scheme := runtime.NewScheme()
		Expect(kuadrantv1.AddToScheme(scheme)).ToNot(HaveOccurred())
		fakeClient = fake.NewClientBuilder().WithScheme(scheme).Build()
		store = resources.NewClientAuthPolicyStore(fakeClient)
	})

	Context("CRUD operations", func() {
		It("should create AuthPolicy successfully", func(ctx SpecContext) {
			testAuthPolicy := createTestAuthPolicy()
			err := store.Create(ctx, testAuthPolicy)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should return error when creating duplicate AuthPolicy", func(ctx SpecContext) {
			testAuthPolicy := createTestAuthPolicy()
			err := store.Create(ctx, testAuthPolicy)
			Expect(err).ToNot(HaveOccurred())

			err = store.Create(ctx, testAuthPolicy)
			Expect(err).To(HaveOccurred())
		})

		It("should get AuthPolicy successfully", func(ctx SpecContext) {
			testAuthPolicy := createTestAuthPolicy()
			err := store.Create(ctx, testAuthPolicy)
			Expect(err).ToNot(HaveOccurred())

			key := types.NamespacedName{
				Name:      testAuthPolicy.GetName(),
				Namespace: testAuthPolicy.GetNamespace(),
			}
			retrieved, err := store.Get(ctx, key)
			Expect(err).ToNot(HaveOccurred())
			Expect(retrieved).ToNot(BeNil())
			Expect(retrieved.GetName()).To(Equal(testAuthPolicy.GetName()))
			Expect(retrieved.GetNamespace()).To(Equal(testAuthPolicy.GetNamespace()))
		})

		It("should return error when getting non-existent AuthPolicy", func(ctx SpecContext) {
			key := types.NamespacedName{
				Name:      constants.GetHTTPRouteAuthPolicyName("non-existent"),
				Namespace: "test-namespace",
			}
			_, err := store.Get(ctx, key)
			Expect(err).To(HaveOccurred())
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})

		It("should update AuthPolicy successfully", func(ctx SpecContext) {
			testAuthPolicy := createTestAuthPolicy()
			err := store.Create(ctx, testAuthPolicy)
			Expect(err).ToNot(HaveOccurred())

			if testAuthPolicy.Annotations == nil {
				testAuthPolicy.Annotations = make(map[string]string)
			}
			testAuthPolicy.Annotations["test-updated"] = "true"
			err = store.Update(ctx, testAuthPolicy)
			Expect(err).ToNot(HaveOccurred())

			key := types.NamespacedName{
				Name:      testAuthPolicy.GetName(),
				Namespace: testAuthPolicy.GetNamespace(),
			}
			retrieved, err := store.Get(ctx, key)
			Expect(err).ToNot(HaveOccurred())
			Expect(retrieved.Annotations).To(HaveKeyWithValue("test-updated", "true"))
		})

		It("should return error when updating non-existent AuthPolicy", func(ctx SpecContext) {
			nonExistentAuthPolicy := createTestAuthPolicy()
			nonExistentAuthPolicy.Name = constants.GetHTTPRouteAuthPolicyName("non-existent")

			err := store.Update(ctx, nonExistentAuthPolicy)
			Expect(err).To(HaveOccurred())
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})

		It("should remove AuthPolicy successfully", func(ctx SpecContext) {
			testAuthPolicy := createTestAuthPolicy()
			err := store.Create(ctx, testAuthPolicy)
			Expect(err).ToNot(HaveOccurred())

			key := types.NamespacedName{
				Name:      testAuthPolicy.GetName(),
				Namespace: testAuthPolicy.GetNamespace(),
			}
			err = store.Remove(ctx, key)
			Expect(err).ToNot(HaveOccurred())

			_, err = store.Get(ctx, key)
			Expect(err).To(HaveOccurred())
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})

		It("should handle NotFound gracefully on remove", func(ctx SpecContext) {
			key := types.NamespacedName{
				Name:      constants.GetHTTPRouteAuthPolicyName("non-existent"),
				Namespace: "test-namespace",
			}
			err := store.Remove(ctx, key)
			Expect(err).ToNot(HaveOccurred())
		})

	})
})

var _ = Describe("AuthPolicyMatcher", func() {
	var matcher resources.AuthPolicyMatcher
	var fakeClient client.Client
	var scheme *runtime.Scheme

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(kservev1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(kuadrantv1.AddToScheme(scheme)).To(Succeed())

		fakeClient = fake.NewClientBuilder().WithScheme(scheme).Build()
		matcher = resources.NewKServeAuthPolicyMatcher(fakeClient)
	})

	Describe("FindLLMServiceFromHTTPRouteAuthPolicy", func() {
		It("should find LLMInferenceService from OwnerReference", func() {
			authPolicy := &kuadrantv1.AuthPolicy{
				ObjectMeta: v1.ObjectMeta{
					Name:      "test-auth-policy",
					Namespace: "test-namespace",
					OwnerReferences: []v1.OwnerReference{{
						APIVersion: "serving.kserve.io/v1alpha1",
						Kind:       "LLMInferenceService",
						Name:       "my-llm-service",
					}},
				},
				Spec: kuadrantv1.AuthPolicySpec{
					TargetRef: gwapiv1alpha2.LocalPolicyTargetReferenceWithSectionName{
						LocalPolicyTargetReference: gwapiv1alpha2.LocalPolicyTargetReference{
							Group: "gateway.networking.k8s.io",
							Kind:  "HTTPRoute",
							Name:  "some-llm-kserve-route",
						},
					},
				},
			}

			namespacedName, found := matcher.FindLLMServiceFromHTTPRouteAuthPolicy(authPolicy)

			Expect(found).To(BeTrue())
			Expect(namespacedName.Name).To(Equal("my-llm-service"))
			Expect(namespacedName.Namespace).To(Equal("test-namespace"))
		})

		It("should find LLMInferenceService from HTTPRoute name pattern", func() {
			authPolicy := &kuadrantv1.AuthPolicy{
				ObjectMeta: v1.ObjectMeta{
					Name:      "test-auth-policy",
					Namespace: "test-namespace",
				},
				Spec: kuadrantv1.AuthPolicySpec{
					TargetRef: gwapiv1alpha2.LocalPolicyTargetReferenceWithSectionName{
						LocalPolicyTargetReference: gwapiv1alpha2.LocalPolicyTargetReference{
							Group: "gateway.networking.k8s.io",
							Kind:  "HTTPRoute",
							Name:  "my-llm-service-kserve-route",
						},
					},
				},
			}

			namespacedName, found := matcher.FindLLMServiceFromHTTPRouteAuthPolicy(authPolicy)

			Expect(found).To(BeTrue())
			Expect(namespacedName.Name).To(Equal("my-llm-service"))
			Expect(namespacedName.Namespace).To(Equal("test-namespace"))
		})

		It("should prioritize OwnerReference over name pattern", func() {
			authPolicy := &kuadrantv1.AuthPolicy{
				ObjectMeta: v1.ObjectMeta{
					Name:      "test-auth-policy",
					Namespace: "test-namespace",
					OwnerReferences: []v1.OwnerReference{{
						APIVersion: "serving.kserve.io/v1alpha1",
						Kind:       "LLMInferenceService",
						Name:       "owner-ref-service",
					}},
				},
				Spec: kuadrantv1.AuthPolicySpec{
					TargetRef: gwapiv1alpha2.LocalPolicyTargetReferenceWithSectionName{
						LocalPolicyTargetReference: gwapiv1alpha2.LocalPolicyTargetReference{
							Group: "gateway.networking.k8s.io",
							Kind:  "HTTPRoute",
							Name:  "name-pattern-service-kserve-route",
						},
					},
				},
			}

			namespacedName, found := matcher.FindLLMServiceFromHTTPRouteAuthPolicy(authPolicy)

			Expect(found).To(BeTrue())
			Expect(namespacedName.Name).To(Equal("owner-ref-service"))
			Expect(namespacedName.Namespace).To(Equal("test-namespace"))
		})

		It("should return false when no match found", func() {
			authPolicy := &kuadrantv1.AuthPolicy{
				ObjectMeta: v1.ObjectMeta{
					Name:      "test-auth-policy",
					Namespace: "test-namespace",
				},
				Spec: kuadrantv1.AuthPolicySpec{
					TargetRef: gwapiv1alpha2.LocalPolicyTargetReferenceWithSectionName{
						LocalPolicyTargetReference: gwapiv1alpha2.LocalPolicyTargetReference{
							Group: "gateway.networking.k8s.io",
							Kind:  "HTTPRoute",
							Name:  "invalid-route-name",
						},
					},
				},
			}

			namespacedName, found := matcher.FindLLMServiceFromHTTPRouteAuthPolicy(authPolicy)

			Expect(found).To(BeFalse())
			Expect(namespacedName).To(Equal(types.NamespacedName{}))
		})

		It("should ignore non-LLMInferenceService OwnerReferences", func() {
			authPolicy := &kuadrantv1.AuthPolicy{
				ObjectMeta: v1.ObjectMeta{
					Name:      "test-auth-policy",
					Namespace: "test-namespace",
					OwnerReferences: []v1.OwnerReference{{
						APIVersion: "apps/v1",
						Kind:       "Deployment",
						Name:       "some-deployment",
					}},
				},
				Spec: kuadrantv1.AuthPolicySpec{
					TargetRef: gwapiv1alpha2.LocalPolicyTargetReferenceWithSectionName{
						LocalPolicyTargetReference: gwapiv1alpha2.LocalPolicyTargetReference{
							Group: "gateway.networking.k8s.io",
							Kind:  "HTTPRoute",
							Name:  "my-llm-service-kserve-route",
						},
					},
				},
			}

			namespacedName, found := matcher.FindLLMServiceFromHTTPRouteAuthPolicy(authPolicy)

			Expect(found).To(BeTrue())
			Expect(namespacedName.Name).To(Equal("my-llm-service"))
			Expect(namespacedName.Namespace).To(Equal("test-namespace"))
		})
	})

	Describe("FindLLMServiceFromGatewayAuthPolicy", func() {
		It("should return empty slice when no LLMInferenceService found", func(ctx SpecContext) {
			authPolicy := &kuadrantv1.AuthPolicy{
				ObjectMeta: v1.ObjectMeta{
					Name:      "gateway-auth-policy",
					Namespace: constants.DefaultGatewayNamespace,
				},
				Spec: kuadrantv1.AuthPolicySpec{
					TargetRef: gwapiv1alpha2.LocalPolicyTargetReferenceWithSectionName{
						LocalPolicyTargetReference: gwapiv1alpha2.LocalPolicyTargetReference{
							Group: "gateway.networking.k8s.io",
							Kind:  "Gateway",
							Name:  constants.DefaultGatewayName,
						},
					},
				},
			}

			namespacedNames, err := matcher.FindLLMServiceFromGatewayAuthPolicy(ctx, authPolicy)

			Expect(err).ToNot(HaveOccurred())
			Expect(namespacedNames).To(BeEmpty())
		})

		It("should find LLMInferenceService with default gateway", func(ctx SpecContext) {
			llmService := &kservev1alpha1.LLMInferenceService{
				ObjectMeta: v1.ObjectMeta{
					Name:      "test-llm-service",
					Namespace: "test-namespace",
				},
				Spec: kservev1alpha1.LLMInferenceServiceSpec{},
			}
			err := fakeClient.Create(ctx, llmService)
			Expect(err).ToNot(HaveOccurred())

			authPolicy := &kuadrantv1.AuthPolicy{
				ObjectMeta: v1.ObjectMeta{
					Name:      "gateway-auth-policy",
					Namespace: constants.DefaultGatewayNamespace,
				},
				Spec: kuadrantv1.AuthPolicySpec{
					TargetRef: gwapiv1alpha2.LocalPolicyTargetReferenceWithSectionName{
						LocalPolicyTargetReference: gwapiv1alpha2.LocalPolicyTargetReference{
							Group: "gateway.networking.k8s.io",
							Kind:  "Gateway",
							Name:  constants.DefaultGatewayName,
						},
					},
				},
			}

			namespacedNames, err := matcher.FindLLMServiceFromGatewayAuthPolicy(ctx, authPolicy)

			Expect(err).ToNot(HaveOccurred())
			Expect(namespacedNames).To(HaveLen(1))
			Expect(namespacedNames[0].Name).To(Equal("test-llm-service"))
			Expect(namespacedNames[0].Namespace).To(Equal("test-namespace"))
		})

		It("should find multiple LLMInferenceServices with default gateway", func(ctx SpecContext) {
			// Create multiple LLM services
			llmService1 := &kservev1alpha1.LLMInferenceService{
				ObjectMeta: v1.ObjectMeta{
					Name:      "test-llm-service-1",
					Namespace: "test-namespace",
				},
				Spec: kservev1alpha1.LLMInferenceServiceSpec{},
			}
			err := fakeClient.Create(ctx, llmService1)
			Expect(err).ToNot(HaveOccurred())

			llmService2 := &kservev1alpha1.LLMInferenceService{
				ObjectMeta: v1.ObjectMeta{
					Name:      "test-llm-service-2",
					Namespace: "test-namespace",
				},
				Spec: kservev1alpha1.LLMInferenceServiceSpec{},
			}
			err = fakeClient.Create(ctx, llmService2)
			Expect(err).ToNot(HaveOccurred())

			authPolicy := &kuadrantv1.AuthPolicy{
				ObjectMeta: v1.ObjectMeta{
					Name:      "gateway-auth-policy",
					Namespace: constants.DefaultGatewayNamespace,
				},
				Spec: kuadrantv1.AuthPolicySpec{
					TargetRef: gwapiv1alpha2.LocalPolicyTargetReferenceWithSectionName{
						LocalPolicyTargetReference: gwapiv1alpha2.LocalPolicyTargetReference{
							Group: "gateway.networking.k8s.io",
							Kind:  "Gateway",
							Name:  constants.DefaultGatewayName,
						},
					},
				},
			}

			namespacedNames, err := matcher.FindLLMServiceFromGatewayAuthPolicy(ctx, authPolicy)

			Expect(err).ToNot(HaveOccurred())
			Expect(namespacedNames).To(HaveLen(2))

			// Verify both services are found
			serviceNames := make([]string, len(namespacedNames))
			for i, ns := range namespacedNames {
				serviceNames[i] = ns.Name
			}
			Expect(serviceNames).To(ContainElement("test-llm-service-1"))
			Expect(serviceNames).To(ContainElement("test-llm-service-2"))
		})

		It("should handle API errors gracefully", func(ctx SpecContext) {
			authPolicy := &kuadrantv1.AuthPolicy{
				ObjectMeta: v1.ObjectMeta{
					Name:      "test-auth-policy",
					Namespace: constants.DefaultGatewayNamespace,
				},
				Spec: kuadrantv1.AuthPolicySpec{
					TargetRef: gwapiv1alpha2.LocalPolicyTargetReferenceWithSectionName{
						LocalPolicyTargetReference: gwapiv1alpha2.LocalPolicyTargetReference{
							Group: "gateway.networking.k8s.io",
							Kind:  "Gateway",
							Name:  constants.DefaultGatewayName,
						},
					},
				},
			}

			namespacedNames, err := matcher.FindLLMServiceFromGatewayAuthPolicy(ctx, authPolicy)

			Expect(err).ToNot(HaveOccurred())
			Expect(namespacedNames).To(BeEmpty())
		})
	})
})
