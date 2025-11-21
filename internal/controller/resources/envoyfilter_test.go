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
	"encoding/json"
	"os"

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	authorinooperatorv1beta1 "github.com/kuadrant/authorino-operator/api/v1beta1"
	kuadrantv1beta1 "github.com/kuadrant/kuadrant-operator/api/v1beta1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	istiov1alpha3 "istio.io/api/networking/v1alpha3"
	istiotypev1beta1 "istio.io/api/type/v1beta1"
	istioclientv1alpha3 "istio.io/client-go/pkg/apis/networking/v1alpha3"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/resources"
)

// Test helper functions

// setupTestScheme creates a runtime scheme with all necessary types registered
func setupTestScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	Expect(kservev1alpha1.AddToScheme(scheme)).To(Succeed())
	Expect(gatewayapiv1.Install(scheme)).To(Succeed())
	Expect(kuadrantv1beta1.AddToScheme(scheme)).To(Succeed())
	Expect(authorinooperatorv1beta1.AddToScheme(scheme)).To(Succeed())
	Expect(istioclientv1alpha3.AddToScheme(scheme)).To(Succeed())
	return scheme
}

// createDefaultGateway creates a gateway object with default test values
func createDefaultGateway() *gatewayapiv1.Gateway {
	return &gatewayapiv1.Gateway{
		ObjectMeta: v1.ObjectMeta{
			Name:      "openshift-ai-inference",
			Namespace: "openshift-ingress",
		},
	}
}

// createTestLLMISvc creates a dummy LLMInferenceService for testing
func createTestLLMISvc() kservev1alpha1.LLMInferenceService {
	return kservev1alpha1.LLMInferenceService{
		ObjectMeta: v1.ObjectMeta{
			Namespace: "test-ns",
			Name:      "test-llm",
		},
	}
}

var _ = Describe("EnvoyFilterTemplateLoader", func() {
	Context("Template loading", func() {
		var loader resources.EnvoyFilterTemplateLoader
		var dummyLLMISvc kservev1alpha1.LLMInferenceService

		BeforeEach(func() {
			scheme := runtime.NewScheme()
			Expect(kservev1alpha1.AddToScheme(scheme)).To(Succeed())
			Expect(gatewayapiv1.Install(scheme)).To(Succeed())

			// Create default gateway that tests expect
			defaultGateway := &gatewayapiv1.Gateway{
				ObjectMeta: v1.ObjectMeta{
					Name:      "openshift-ai-inference",
					Namespace: "openshift-ingress",
				},
			}

			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(defaultGateway).Build()
			loader = resources.NewKServeEnvoyFilterTemplateLoader(fakeClient)

			dummyLLMISvc = kservev1alpha1.LLMInferenceService{
				ObjectMeta: v1.ObjectMeta{
					Namespace: "test-ns",
					Name:      "test-llm",
				},
			}
		})

		It("should resolve SSL template for LLMInferenceService", func(ctx SpecContext) {
			envoyFilters, err := loader.Load(ctx, &dummyLLMISvc)

			Expect(err).ToNot(HaveOccurred())
			Expect(envoyFilters).ToNot(BeNil())
			Expect(envoyFilters).ToNot(BeEmpty())
			Expect(envoyFilters[0].GetName()).To(Equal(constants.GetGatewayEnvoyFilterName("openshift-ai-inference")))
			Expect(envoyFilters[0].GetNamespace()).To(Equal("openshift-ingress"))
		})

		It("should have correct targetRefs in rendered template", func(ctx SpecContext) {
			envoyFilters, err := loader.Load(ctx, &dummyLLMISvc)

			Expect(err).ToNot(HaveOccurred())
			Expect(envoyFilters).ToNot(BeNil())
			Expect(envoyFilters).To(HaveLen(1))
			Expect(envoyFilters[0].Spec.TargetRefs).ToNot(BeEmpty())
			Expect(envoyFilters[0].Spec.TargetRefs[0].Kind).To(Equal("Gateway"))
			Expect(envoyFilters[0].Spec.TargetRefs[0].Name).To(Equal("openshift-ai-inference"))
		})

		It("should have configPatches in rendered template", func(ctx SpecContext) {
			envoyFilters, err := loader.Load(ctx, &dummyLLMISvc)

			Expect(err).ToNot(HaveOccurred())
			Expect(envoyFilters).ToNot(BeNil())
			Expect(envoyFilters).To(HaveLen(1))
			Expect(envoyFilters[0].Spec.ConfigPatches).ToNot(BeEmpty())
		})
	})
})

func createTestEnvoyFilter() *istioclientv1alpha3.EnvoyFilter {
	return &istioclientv1alpha3.EnvoyFilter{
		ObjectMeta: v1.ObjectMeta{
			Name:      constants.GetGatewayEnvoyFilterName("test-gateway"),
			Namespace: "test-namespace",
		},
		Spec: istiov1alpha3.EnvoyFilter{
			TargetRefs: []*istiotypev1beta1.PolicyTargetReference{
				{
					Group: "gateway.networking.k8s.io",
					Kind:  "Gateway",
					Name:  "test-gateway",
				},
			},
		},
	}
}

var _ = Describe("EnvoyFilterStore", func() {
	var store resources.EnvoyFilterStore
	var fakeClient client.Client

	BeforeEach(func() {
		scheme := runtime.NewScheme()
		Expect(istioclientv1alpha3.AddToScheme(scheme)).ToNot(HaveOccurred())
		fakeClient = fake.NewClientBuilder().WithScheme(scheme).Build()
		store = resources.NewClientEnvoyFilterStore(fakeClient)
	})

	Context("CRUD operations", func() {
		It("should create EnvoyFilter successfully", func(ctx SpecContext) {
			testEnvoyFilter := createTestEnvoyFilter()
			err := store.Create(ctx, testEnvoyFilter)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should return error when creating duplicate EnvoyFilter", func(ctx SpecContext) {
			testEnvoyFilter := createTestEnvoyFilter()
			err := store.Create(ctx, testEnvoyFilter)
			Expect(err).ToNot(HaveOccurred())

			err = store.Create(ctx, testEnvoyFilter)
			Expect(err).To(HaveOccurred())
		})

		It("should get EnvoyFilter successfully", func(ctx SpecContext) {
			testEnvoyFilter := createTestEnvoyFilter()
			err := store.Create(ctx, testEnvoyFilter)
			Expect(err).ToNot(HaveOccurred())

			key := types.NamespacedName{
				Name:      testEnvoyFilter.GetName(),
				Namespace: testEnvoyFilter.GetNamespace(),
			}
			retrieved, err := store.Get(ctx, key)
			Expect(err).ToNot(HaveOccurred())
			Expect(retrieved).ToNot(BeNil())
			Expect(retrieved.GetName()).To(Equal(testEnvoyFilter.GetName()))
			Expect(retrieved.GetNamespace()).To(Equal(testEnvoyFilter.GetNamespace()))
		})

		It("should return error when getting non-existent EnvoyFilter", func(ctx SpecContext) {
			key := types.NamespacedName{
				Name:      constants.GetGatewayEnvoyFilterName("non-existent"),
				Namespace: "test-namespace",
			}
			_, err := store.Get(ctx, key)
			Expect(err).To(HaveOccurred())
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})

		It("should update EnvoyFilter successfully", func(ctx SpecContext) {
			testEnvoyFilter := createTestEnvoyFilter()
			err := store.Create(ctx, testEnvoyFilter)
			Expect(err).ToNot(HaveOccurred())

			if testEnvoyFilter.Annotations == nil {
				testEnvoyFilter.Annotations = make(map[string]string)
			}
			testEnvoyFilter.Annotations["test-updated"] = "true"
			err = store.Update(ctx, testEnvoyFilter)
			Expect(err).ToNot(HaveOccurred())

			key := types.NamespacedName{
				Name:      testEnvoyFilter.GetName(),
				Namespace: testEnvoyFilter.GetNamespace(),
			}
			retrieved, err := store.Get(ctx, key)
			Expect(err).ToNot(HaveOccurred())
			Expect(retrieved.Annotations).To(HaveKeyWithValue("test-updated", "true"))
		})

		It("should return error when updating non-existent EnvoyFilter", func(ctx SpecContext) {
			nonExistentEnvoyFilter := createTestEnvoyFilter()
			nonExistentEnvoyFilter.Name = constants.GetGatewayEnvoyFilterName("non-existent")

			err := store.Update(ctx, nonExistentEnvoyFilter)
			Expect(err).To(HaveOccurred())
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})

		It("should remove EnvoyFilter successfully", func(ctx SpecContext) {
			testEnvoyFilter := createTestEnvoyFilter()
			err := store.Create(ctx, testEnvoyFilter)
			Expect(err).ToNot(HaveOccurred())

			key := types.NamespacedName{
				Name:      testEnvoyFilter.GetName(),
				Namespace: testEnvoyFilter.GetNamespace(),
			}
			err = store.Remove(ctx, key)
			Expect(err).ToNot(HaveOccurred())

			_, err = store.Get(ctx, key)
			Expect(err).To(HaveOccurred())
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})

		It("should handle NotFound gracefully on remove", func(ctx SpecContext) {
			key := types.NamespacedName{
				Name:      constants.GetGatewayEnvoyFilterName("non-existent"),
				Namespace: "test-namespace",
			}
			err := store.Remove(ctx, key)
			Expect(err).ToNot(HaveOccurred())
		})

	})
})

var _ = Describe("EnvoyFilterMatcher", func() {
	var matcher resources.EnvoyFilterMatcher
	var fakeClient client.Client
	var scheme *runtime.Scheme

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(kservev1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(istioclientv1alpha3.AddToScheme(scheme)).To(Succeed())

		fakeClient = fake.NewClientBuilder().WithScheme(scheme).Build()
		matcher = resources.NewKServeEnvoyFilterMatcher(fakeClient)
	})

	Describe("FindLLMServiceFromEnvoyFilter", func() {
		It("should return empty slice when no LLMInferenceService found", func(ctx SpecContext) {
			envoyFilter := &istioclientv1alpha3.EnvoyFilter{
				ObjectMeta: v1.ObjectMeta{
					Name:      "gateway-envoyfilter",
					Namespace: constants.DefaultGatewayNamespace,
				},
				Spec: istiov1alpha3.EnvoyFilter{
					TargetRefs: []*istiotypev1beta1.PolicyTargetReference{
						{
							Group: "gateway.networking.k8s.io",
							Kind:  "Gateway",
							Name:  constants.DefaultGatewayName,
						},
					},
				},
			}

			namespacedNames, err := matcher.FindLLMServiceFromEnvoyFilter(ctx, envoyFilter)

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

			envoyFilter := &istioclientv1alpha3.EnvoyFilter{
				ObjectMeta: v1.ObjectMeta{
					Name:      "gateway-envoyfilter",
					Namespace: constants.DefaultGatewayNamespace,
				},
				Spec: istiov1alpha3.EnvoyFilter{
					TargetRefs: []*istiotypev1beta1.PolicyTargetReference{
						{
							Group: "gateway.networking.k8s.io",
							Kind:  "Gateway",
							Name:  constants.DefaultGatewayName,
						},
					},
				},
			}

			namespacedNames, err := matcher.FindLLMServiceFromEnvoyFilter(ctx, envoyFilter)

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

			envoyFilter := &istioclientv1alpha3.EnvoyFilter{
				ObjectMeta: v1.ObjectMeta{
					Name:      "gateway-envoyfilter",
					Namespace: constants.DefaultGatewayNamespace,
				},
				Spec: istiov1alpha3.EnvoyFilter{
					TargetRefs: []*istiotypev1beta1.PolicyTargetReference{
						{
							Group: "gateway.networking.k8s.io",
							Kind:  "Gateway",
							Name:  constants.DefaultGatewayName,
						},
					},
				},
			}

			namespacedNames, err := matcher.FindLLMServiceFromEnvoyFilter(ctx, envoyFilter)

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

		It("should handle EnvoyFilter with no targetRefs", func(ctx SpecContext) {
			envoyFilter := &istioclientv1alpha3.EnvoyFilter{
				ObjectMeta: v1.ObjectMeta{
					Name:      "test-envoyfilter",
					Namespace: constants.DefaultGatewayNamespace,
				},
				Spec: istiov1alpha3.EnvoyFilter{
					TargetRefs: []*istiotypev1beta1.PolicyTargetReference{},
				},
			}

			namespacedNames, err := matcher.FindLLMServiceFromEnvoyFilter(ctx, envoyFilter)

			Expect(err).ToNot(HaveOccurred())
			Expect(namespacedNames).To(BeEmpty())
		})

		It("should handle API errors gracefully", func(ctx SpecContext) {
			envoyFilter := &istioclientv1alpha3.EnvoyFilter{
				ObjectMeta: v1.ObjectMeta{
					Name:      "test-envoyfilter",
					Namespace: constants.DefaultGatewayNamespace,
				},
				Spec: istiov1alpha3.EnvoyFilter{
					TargetRefs: []*istiotypev1beta1.PolicyTargetReference{
						{
							Group: "gateway.networking.k8s.io",
							Kind:  "Gateway",
							Name:  constants.DefaultGatewayName,
						},
					},
				},
			}

			namespacedNames, err := matcher.FindLLMServiceFromEnvoyFilter(ctx, envoyFilter)

			Expect(err).ToNot(HaveOccurred())
			Expect(namespacedNames).To(BeEmpty())
		})
	})
})

var _ = Describe("EnvoyFilterTemplateLoader Kuadrant Namespace Detection", func() {
	var loader resources.EnvoyFilterTemplateLoader
	var dummyLLMISvc kservev1alpha1.LLMInferenceService

	// Helper to verify Authorino service address in EnvoyFilter
	verifyAuthorinoNamespace := func(envoyFilter *istioclientv1alpha3.EnvoyFilter, expectedNamespace string) {
		envoyFilterJSON, err := json.Marshal(envoyFilter)
		Expect(err).ToNot(HaveOccurred())
		expectedAddress := "authorino-authorino-authorization." + expectedNamespace + ".svc.cluster.local"
		Expect(string(envoyFilterJSON)).To(ContainSubstring(expectedAddress))
	}

	Context("Kuadrant namespace detection", func() {
		It("should use default kuadrant-system namespace when no Kuadrant resources exist", func(ctx SpecContext) {
			scheme := setupTestScheme()
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(createDefaultGateway()).Build()
			loader = resources.NewKServeEnvoyFilterTemplateLoader(fakeClient)
			dummyLLMISvc = createTestLLMISvc()

			envoyFilters, err := loader.Load(ctx, &dummyLLMISvc)

			Expect(err).ToNot(HaveOccurred())
			Expect(envoyFilters).To(HaveLen(1))
			verifyAuthorinoNamespace(envoyFilters[0], "kuadrant-system")
		})

		It("should detect Kuadrant namespace from Kuadrant resource", func(ctx SpecContext) {
			scheme := setupTestScheme()
			kuadrant := &kuadrantv1beta1.Kuadrant{
				ObjectMeta: v1.ObjectMeta{Name: "kuadrant", Namespace: "custom-kuadrant-ns"},
			}

			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(createDefaultGateway(), kuadrant).Build()
			loader = resources.NewKServeEnvoyFilterTemplateLoader(fakeClient)
			dummyLLMISvc = createTestLLMISvc()

			envoyFilters, err := loader.Load(ctx, &dummyLLMISvc)

			Expect(err).ToNot(HaveOccurred())
			Expect(envoyFilters).To(HaveLen(1))
			verifyAuthorinoNamespace(envoyFilters[0], "custom-kuadrant-ns")
		})

		It("should prioritize kuadrant-system namespace when multiple Kuadrant resources exist", func(ctx SpecContext) {
			scheme := setupTestScheme()
			kuadrant1 := &kuadrantv1beta1.Kuadrant{
				ObjectMeta: v1.ObjectMeta{Name: "kuadrant", Namespace: "another-ns"},
			}
			kuadrant2 := &kuadrantv1beta1.Kuadrant{
				ObjectMeta: v1.ObjectMeta{Name: "kuadrant", Namespace: "kuadrant-system"},
			}

			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(createDefaultGateway(), kuadrant1, kuadrant2).Build()
			loader = resources.NewKServeEnvoyFilterTemplateLoader(fakeClient)
			dummyLLMISvc = createTestLLMISvc()

			envoyFilters, err := loader.Load(ctx, &dummyLLMISvc)

			Expect(err).ToNot(HaveOccurred())
			Expect(envoyFilters).To(HaveLen(1))
			verifyAuthorinoNamespace(envoyFilters[0], "kuadrant-system")
		})

		It("should prioritize KUADRANT_NAMESPACE environment variable when set", func(ctx SpecContext) {
			GinkgoT().Setenv("KUADRANT_NAMESPACE", "env-kuadrant-ns")

			scheme := setupTestScheme()
			kuadrant1 := &kuadrantv1beta1.Kuadrant{
				ObjectMeta: v1.ObjectMeta{Name: "kuadrant", Namespace: "another-kuadrant-ns"},
			}
			kuadrant2 := &kuadrantv1beta1.Kuadrant{
				ObjectMeta: v1.ObjectMeta{Name: "kuadrant", Namespace: "env-kuadrant-ns"},
			}

			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(createDefaultGateway(), kuadrant1, kuadrant2).Build()
			loader = resources.NewKServeEnvoyFilterTemplateLoader(fakeClient)
			dummyLLMISvc = createTestLLMISvc()

			envoyFilters, err := loader.Load(ctx, &dummyLLMISvc)

			Expect(err).ToNot(HaveOccurred())
			Expect(envoyFilters).To(HaveLen(1))
			verifyAuthorinoNamespace(envoyFilters[0], "env-kuadrant-ns")
		})

		It("should use KUADRANT_NAMESPACE environment variable when set", func(ctx SpecContext) {
			Expect(os.Setenv("KUADRANT_NAMESPACE", "env-kuadrant-ns")).To(Succeed())
			defer func() { _ = os.Unsetenv("KUADRANT_NAMESPACE") }()

			scheme := setupTestScheme()
			kuadrant := &kuadrantv1beta1.Kuadrant{
				ObjectMeta: v1.ObjectMeta{Name: "kuadrant", Namespace: "another-kuadrant-ns"},
			}

			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(createDefaultGateway(), kuadrant).Build()
			loader = resources.NewKServeEnvoyFilterTemplateLoader(fakeClient)
			dummyLLMISvc = createTestLLMISvc()

			envoyFilters, err := loader.Load(ctx, &dummyLLMISvc)

			Expect(err).ToNot(HaveOccurred())
			Expect(envoyFilters).To(HaveLen(1))
			verifyAuthorinoNamespace(envoyFilters[0], "another-kuadrant-ns")
		})
	})
})

var _ = Describe("EnvoyFilterTemplateLoader Authorino TLS Detection", func() {
	var loader resources.EnvoyFilterTemplateLoader
	var dummyLLMISvc kservev1alpha1.LLMInferenceService

	Context("Authorino TLS detection", func() {
		It("should skip EnvoyFilter creation when Authorino has TLS disabled", func(ctx SpecContext) {
			scheme := setupTestScheme()
			authorino := &authorinooperatorv1beta1.Authorino{
				ObjectMeta: v1.ObjectMeta{Name: "authorino", Namespace: "kuadrant-system"},
				Spec: authorinooperatorv1beta1.AuthorinoSpec{
					Listener: authorinooperatorv1beta1.Listener{
						Tls: authorinooperatorv1beta1.Tls{Enabled: ptr.To(false)},
					},
				},
			}

			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(createDefaultGateway(), authorino).Build()
			loader = resources.NewKServeEnvoyFilterTemplateLoader(fakeClient)
			dummyLLMISvc = createTestLLMISvc()

			envoyFilters, err := loader.Load(ctx, &dummyLLMISvc)

			Expect(err).ToNot(HaveOccurred())
			Expect(envoyFilters).To(BeEmpty(), "EnvoyFilter should not be created when Authorino has TLS disabled")
		})

		It("should create EnvoyFilter when Authorino has TLS enabled", func(ctx SpecContext) {
			scheme := setupTestScheme()
			authorino := &authorinooperatorv1beta1.Authorino{
				ObjectMeta: v1.ObjectMeta{Name: "authorino", Namespace: "kuadrant-system"},
				Spec: authorinooperatorv1beta1.AuthorinoSpec{
					Listener: authorinooperatorv1beta1.Listener{
						Tls: authorinooperatorv1beta1.Tls{Enabled: ptr.To(true)},
					},
				},
			}

			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(createDefaultGateway(), authorino).Build()
			loader = resources.NewKServeEnvoyFilterTemplateLoader(fakeClient)
			dummyLLMISvc = createTestLLMISvc()

			envoyFilters, err := loader.Load(ctx, &dummyLLMISvc)

			Expect(err).ToNot(HaveOccurred())
			Expect(envoyFilters).To(HaveLen(1), "EnvoyFilter should be created when Authorino has TLS enabled")
		})

		It("should skip EnvoyFilter creation when Authorino TLS is nil (defaults to disabled)", func(ctx SpecContext) {
			scheme := setupTestScheme()
			authorino := &authorinooperatorv1beta1.Authorino{
				ObjectMeta: v1.ObjectMeta{Name: "authorino", Namespace: "kuadrant-system"},
				Spec: authorinooperatorv1beta1.AuthorinoSpec{
					Listener: authorinooperatorv1beta1.Listener{
						Tls: authorinooperatorv1beta1.Tls{Enabled: nil},
					},
				},
			}

			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(createDefaultGateway(), authorino).Build()
			loader = resources.NewKServeEnvoyFilterTemplateLoader(fakeClient)
			dummyLLMISvc = createTestLLMISvc()

			envoyFilters, err := loader.Load(ctx, &dummyLLMISvc)

			Expect(err).ToNot(HaveOccurred())
			Expect(envoyFilters).To(BeEmpty(), "EnvoyFilter should not be created when Authorino TLS is nil (defaults to disabled)")
		})

		It("should create EnvoyFilter when no Authorino resources exist", func(ctx SpecContext) {
			scheme := setupTestScheme()
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(createDefaultGateway()).Build()
			loader = resources.NewKServeEnvoyFilterTemplateLoader(fakeClient)
			dummyLLMISvc = createTestLLMISvc()

			envoyFilters, err := loader.Load(ctx, &dummyLLMISvc)

			Expect(err).ToNot(HaveOccurred())
			Expect(envoyFilters).To(HaveLen(1), "EnvoyFilter should be created when no Authorino resources exist (assumes TLS enabled)")
		})

		It("should use first Authorino when multiple exist in namespace", func(ctx SpecContext) {
			scheme := setupTestScheme()
			authorino1 := &authorinooperatorv1beta1.Authorino{
				ObjectMeta: v1.ObjectMeta{Name: "authorino-a", Namespace: "kuadrant-system"},
				Spec: authorinooperatorv1beta1.AuthorinoSpec{
					Listener: authorinooperatorv1beta1.Listener{
						Tls: authorinooperatorv1beta1.Tls{Enabled: ptr.To(false)},
					},
				},
			}
			authorino2 := &authorinooperatorv1beta1.Authorino{
				ObjectMeta: v1.ObjectMeta{Name: "authorino-z", Namespace: "kuadrant-system"},
				Spec: authorinooperatorv1beta1.AuthorinoSpec{
					Listener: authorinooperatorv1beta1.Listener{
						Tls: authorinooperatorv1beta1.Tls{Enabled: ptr.To(true)},
					},
				},
			}

			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(createDefaultGateway(), authorino1, authorino2).Build()
			loader = resources.NewKServeEnvoyFilterTemplateLoader(fakeClient)
			dummyLLMISvc = createTestLLMISvc()

			envoyFilters, err := loader.Load(ctx, &dummyLLMISvc)

			Expect(err).ToNot(HaveOccurred())
			Expect(envoyFilters).To(BeEmpty(), "Should use first Authorino (authorino-a) which has TLS disabled")
		})

		It("should work with custom Kuadrant namespace and Authorino", func(ctx SpecContext) {
			scheme := setupTestScheme()
			customNamespace := "custom-kuadrant"
			kuadrant := &kuadrantv1beta1.Kuadrant{
				ObjectMeta: v1.ObjectMeta{Name: "kuadrant", Namespace: customNamespace},
			}
			authorino := &authorinooperatorv1beta1.Authorino{
				ObjectMeta: v1.ObjectMeta{Name: "authorino", Namespace: customNamespace},
				Spec: authorinooperatorv1beta1.AuthorinoSpec{
					Listener: authorinooperatorv1beta1.Listener{
						Tls: authorinooperatorv1beta1.Tls{Enabled: ptr.To(false)},
					},
				},
			}

			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(createDefaultGateway(), kuadrant, authorino).Build()
			loader = resources.NewKServeEnvoyFilterTemplateLoader(fakeClient)
			dummyLLMISvc = createTestLLMISvc()

			envoyFilters, err := loader.Load(ctx, &dummyLLMISvc)

			Expect(err).ToNot(HaveOccurred())
			Expect(envoyFilters).To(BeEmpty(), "Should detect Kuadrant namespace and check Authorino TLS in that namespace")
		})
	})
})
