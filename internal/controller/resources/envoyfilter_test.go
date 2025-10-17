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
	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	istiov1alpha3 "istio.io/api/networking/v1alpha3"
	istiotypev1beta1 "istio.io/api/type/v1beta1"
	istioclientv1alpha3 "istio.io/client-go/pkg/apis/networking/v1alpha3"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/resources"
)

var _ = Describe("EnvoyFilterDetector", func() {
	var detector resources.EnvoyFilterDetector

	BeforeEach(func() {
		detector = resources.NewKServeEnvoyFilterDetector(nil)
	})

	It("should return true when annotation is 'true'", func(ctx SpecContext) {
		annotations := map[string]string{
			constants.EnableAuthODHAnnotation: "true",
		}

		result := detector.Detect(ctx, annotations)

		Expect(result).To(BeTrue())
	})

	It("should return false when annotation is 'false'", func(ctx SpecContext) {
		annotations := map[string]string{
			constants.EnableAuthODHAnnotation: "false",
		}

		result := detector.Detect(ctx, annotations)

		Expect(result).To(BeFalse())
	})

	It("should return true when annotation is empty string", func(ctx SpecContext) {
		annotations := map[string]string{
			constants.EnableAuthODHAnnotation: "",
		}

		result := detector.Detect(ctx, annotations)

		Expect(result).To(BeTrue())
	})

	It("should return true when annotation does not exist", func(ctx SpecContext) {
		annotations := map[string]string{}

		result := detector.Detect(ctx, annotations)

		Expect(result).To(BeTrue())
	})

	It("should be case-insensitive for 'true' value", func(ctx SpecContext) {
		testCases := []string{"TRUE", "True", "tRuE"}

		for _, value := range testCases {
			annotations := map[string]string{
				constants.EnableAuthODHAnnotation: value,
			}

			result := detector.Detect(ctx, annotations)

			Expect(result).To(BeTrue(), "Expected true for case variation: %s", value)
		}
	})

	It("should be case-insensitive for 'false' value", func(ctx SpecContext) {
		testCases := []string{"FALSE", "False", "fAlSe"}

		for _, value := range testCases {
			annotations := map[string]string{
				constants.EnableAuthODHAnnotation: value,
			}

			result := detector.Detect(ctx, annotations)

			Expect(result).To(BeFalse(), "Expected false for case variation: %s", value)
		}
	})

	It("should return true for any other invalid values", func(ctx SpecContext) {
		testCases := []string{"yes", "1", "enabled", "on", "invalid", "123"}

		for _, value := range testCases {
			annotations := map[string]string{
				constants.EnableAuthODHAnnotation: value,
			}

			result := detector.Detect(ctx, annotations)

			Expect(result).To(BeTrue(), "Expected true for invalid value: %s", value)
		}
	})
})

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
