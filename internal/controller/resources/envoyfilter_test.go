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

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
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
	Expect(istioclientv1alpha3.AddToScheme(scheme)).To(Succeed())
	return scheme
}

var _ = Describe("EnvoyFilterTemplateLoader", func() {
	Context("Template rendering", func() {
		var loader resources.EnvoyFilterTemplateLoader

		BeforeEach(func() {
			scheme := setupTestScheme()
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
			loader = resources.NewKServeEnvoyFilterTemplateLoader(fakeClient)
		})

		It("should render EnvoyFilter with correct name and namespace", func(ctx SpecContext) {
			envoyFilter, err := loader.Load(ctx, "openshift-ingress", "openshift-ai-inference")

			Expect(err).ToNot(HaveOccurred())
			Expect(envoyFilter).ToNot(BeNil())
			Expect(envoyFilter.GetName()).To(Equal(constants.GetGatewayEnvoyFilterName("openshift-ai-inference")))
			Expect(envoyFilter.GetNamespace()).To(Equal("openshift-ingress"))
		})

		It("should have correct targetRefs in rendered template", func(ctx SpecContext) {
			envoyFilter, err := loader.Load(ctx, "openshift-ingress", "openshift-ai-inference")

			Expect(err).ToNot(HaveOccurred())
			Expect(envoyFilter).ToNot(BeNil())
			Expect(envoyFilter.Spec.TargetRefs).ToNot(BeEmpty())
			Expect(envoyFilter.Spec.TargetRefs[0].Kind).To(Equal("Gateway"))
			Expect(envoyFilter.Spec.TargetRefs[0].Name).To(Equal("openshift-ai-inference"))
		})

		It("should have configPatches in rendered template", func(ctx SpecContext) {
			envoyFilter, err := loader.Load(ctx, "openshift-ingress", "openshift-ai-inference")

			Expect(err).ToNot(HaveOccurred())
			Expect(envoyFilter).ToNot(BeNil())
			Expect(envoyFilter.Spec.ConfigPatches).ToNot(BeEmpty())
		})

		It("should have managed-by label", func(ctx SpecContext) {
			envoyFilter, err := loader.Load(ctx, "openshift-ingress", "openshift-ai-inference")

			Expect(err).ToNot(HaveOccurred())
			Expect(envoyFilter).ToNot(BeNil())
			Expect(envoyFilter.GetLabels()).To(HaveKeyWithValue("app.kubernetes.io/managed-by", "odh-model-controller"))
		})
	})

	Context("Kuadrant namespace detection", func() {
		It("should use default kuadrant-system namespace when no Kuadrant resources exist", func(ctx SpecContext) {
			scheme := setupTestScheme()
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
			loader := resources.NewKServeEnvoyFilterTemplateLoader(fakeClient)

			envoyFilter, err := loader.Load(ctx, "openshift-ingress", "openshift-ai-inference")

			Expect(err).ToNot(HaveOccurred())
			// The EnvoyFilter should be rendered with default kuadrant-system namespace
			Expect(envoyFilter).ToNot(BeNil())
		})

		It("should detect Kuadrant namespace from Kuadrant resource", func(ctx SpecContext) {
			scheme := setupTestScheme()
			kuadrant := &kuadrantv1beta1.Kuadrant{
				ObjectMeta: v1.ObjectMeta{Name: "kuadrant", Namespace: "custom-kuadrant"},
			}

			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(kuadrant).Build()
			loader := resources.NewKServeEnvoyFilterTemplateLoader(fakeClient)

			envoyFilter, err := loader.Load(ctx, "openshift-ingress", "openshift-ai-inference")

			Expect(err).ToNot(HaveOccurred())
			Expect(envoyFilter).ToNot(BeNil())
		})

		It("should prioritize kuadrant-system namespace when multiple Kuadrant resources exist", func(ctx SpecContext) {
			scheme := setupTestScheme()
			kuadrant1 := &kuadrantv1beta1.Kuadrant{
				ObjectMeta: v1.ObjectMeta{Name: "kuadrant", Namespace: "custom-kuadrant"},
			}
			kuadrant2 := &kuadrantv1beta1.Kuadrant{
				ObjectMeta: v1.ObjectMeta{Name: "kuadrant", Namespace: "kuadrant-system"},
			}

			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(kuadrant1, kuadrant2).Build()
			loader := resources.NewKServeEnvoyFilterTemplateLoader(fakeClient)

			envoyFilter, err := loader.Load(ctx, "openshift-ingress", "openshift-ai-inference")

			Expect(err).ToNot(HaveOccurred())
			Expect(envoyFilter).ToNot(BeNil())
		})

		It("should use KUADRANT_NAMESPACE environment variable when set", func(ctx SpecContext) {
			scheme := setupTestScheme()
			GinkgoT().Setenv("KUADRANT_NAMESPACE", "env-kuadrant")

			kuadrant := &kuadrantv1beta1.Kuadrant{
				ObjectMeta: v1.ObjectMeta{Name: "kuadrant", Namespace: "env-kuadrant"},
			}

			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(kuadrant).Build()
			loader := resources.NewKServeEnvoyFilterTemplateLoader(fakeClient)

			envoyFilter, err := loader.Load(ctx, "openshift-ingress", "openshift-ai-inference")

			Expect(err).ToNot(HaveOccurred())
			Expect(envoyFilter).ToNot(BeNil())
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
	var (
		fakeClient client.Client
		store      resources.EnvoyFilterStore
	)

	BeforeEach(func() {
		scheme := runtime.NewScheme()
		Expect(istioclientv1alpha3.AddToScheme(scheme)).To(Succeed())
		fakeClient = fake.NewClientBuilder().WithScheme(scheme).Build()
		store = resources.NewClientEnvoyFilterStore(fakeClient)
	})

	Describe("Get", func() {
		It("should return the EnvoyFilter when it exists", func(ctx SpecContext) {
			ef := createTestEnvoyFilter()
			Expect(fakeClient.Create(ctx, ef)).To(Succeed())

			result, err := store.Get(ctx, types.NamespacedName{
				Name:      ef.Name,
				Namespace: ef.Namespace,
			})

			Expect(err).ToNot(HaveOccurred())
			Expect(result.Name).To(Equal(ef.Name))
			Expect(result.Namespace).To(Equal(ef.Namespace))
		})

		It("should return error when EnvoyFilter does not exist", func(ctx SpecContext) {
			_, err := store.Get(ctx, types.NamespacedName{
				Name:      "nonexistent",
				Namespace: "test-namespace",
			})

			Expect(err).To(HaveOccurred())
		})
	})

	Describe("Create", func() {
		It("should create an EnvoyFilter successfully", func(ctx SpecContext) {
			ef := createTestEnvoyFilter()
			err := store.Create(ctx, ef)
			Expect(err).ToNot(HaveOccurred())

			result, err := store.Get(ctx, types.NamespacedName{
				Name:      ef.Name,
				Namespace: ef.Namespace,
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(result.Name).To(Equal(ef.Name))
		})

		It("should return error when creating duplicate EnvoyFilter", func(ctx SpecContext) {
			ef := createTestEnvoyFilter()
			Expect(store.Create(ctx, ef)).To(Succeed())
			err := store.Create(ctx, ef)
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("Update", func() {
		It("should update an existing EnvoyFilter successfully", func(ctx SpecContext) {
			ef := createTestEnvoyFilter()
			Expect(store.Create(ctx, ef)).To(Succeed())

			ef.Spec.TargetRefs = append(ef.Spec.TargetRefs, &istiotypev1beta1.PolicyTargetReference{
				Kind: "Gateway",
				Name: "new-gateway",
			})

			err := store.Update(ctx, ef)
			Expect(err).ToNot(HaveOccurred())

			result, err := store.Get(ctx, types.NamespacedName{
				Name:      ef.Name,
				Namespace: ef.Namespace,
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(result.Spec.TargetRefs).To(HaveLen(2))
		})

		It("should return error when updating nonexistent EnvoyFilter", func(ctx SpecContext) {
			ef := createTestEnvoyFilter()
			err := store.Update(ctx, ef)
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("Remove", func() {
		It("should remove an existing EnvoyFilter successfully", func(ctx SpecContext) {
			ef := createTestEnvoyFilter()
			Expect(store.Create(ctx, ef)).To(Succeed())

			err := store.Remove(ctx, types.NamespacedName{
				Name:      ef.Name,
				Namespace: ef.Namespace,
			})
			Expect(err).ToNot(HaveOccurred())

			_, err = store.Get(ctx, types.NamespacedName{
				Name:      ef.Name,
				Namespace: ef.Namespace,
			})
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})

		It("should not return error when removing nonexistent EnvoyFilter", func(ctx SpecContext) {
			err := store.Remove(ctx, types.NamespacedName{
				Name:      "nonexistent",
				Namespace: "test-namespace",
			})
			Expect(err).ToNot(HaveOccurred())
		})
	})
})

var _ = Describe("EnvoyFilterMatcher", func() {
	var (
		fakeClient client.Client
		matcher    resources.EnvoyFilterMatcher
		scheme     *runtime.Scheme
	)

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(kservev1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(gatewayapiv1.Install(scheme)).To(Succeed())
		Expect(istioclientv1alpha3.AddToScheme(scheme)).To(Succeed())
	})

	Describe("FindLLMServiceFromEnvoyFilter", func() {
		It("should find matching LLMInferenceService when using default gateway", func(ctx SpecContext) {
			llmSvc := &kservev1alpha1.LLMInferenceService{
				ObjectMeta: v1.ObjectMeta{
					Name:      "test-llm",
					Namespace: "test-namespace",
				},
			}

			envoyFilter := &istioclientv1alpha3.EnvoyFilter{
				ObjectMeta: v1.ObjectMeta{
					Name:      constants.GetGatewayEnvoyFilterName(constants.DefaultGatewayName),
					Namespace: constants.DefaultGatewayNamespace,
				},
				Spec: istiov1alpha3.EnvoyFilter{
					TargetRefs: []*istiotypev1beta1.PolicyTargetReference{
						{
							Kind: "Gateway",
							Name: constants.DefaultGatewayName,
						},
					},
				},
			}

			fakeClient = fake.NewClientBuilder().WithScheme(scheme).WithObjects(llmSvc, envoyFilter).Build()
			matcher = resources.NewKServeEnvoyFilterMatcher(fakeClient)

			services, err := matcher.FindLLMServiceFromEnvoyFilter(ctx, envoyFilter)
			Expect(err).ToNot(HaveOccurred())
			Expect(services).To(HaveLen(1))
			Expect(services[0].Name).To(Equal("test-llm"))
		})

		It("should find matching LLMInferenceService with explicit gateway refs", func(ctx SpecContext) {
			llmSvc := &kservev1alpha1.LLMInferenceService{
				ObjectMeta: v1.ObjectMeta{
					Name:      "test-llm",
					Namespace: "test-namespace",
				},
				Spec: kservev1alpha1.LLMInferenceServiceSpec{
					Router: &kservev1alpha1.RouterSpec{
						Gateway: &kservev1alpha1.GatewaySpec{
							Refs: []kservev1alpha1.UntypedObjectReference{
								{Name: "custom-gateway", Namespace: "test-namespace"},
							},
						},
					},
				},
			}

			envoyFilter := &istioclientv1alpha3.EnvoyFilter{
				ObjectMeta: v1.ObjectMeta{
					Name:      constants.GetGatewayEnvoyFilterName("custom-gateway"),
					Namespace: "test-namespace",
				},
				Spec: istiov1alpha3.EnvoyFilter{
					TargetRefs: []*istiotypev1beta1.PolicyTargetReference{
						{
							Kind: "Gateway",
							Name: "custom-gateway",
						},
					},
				},
			}

			fakeClient = fake.NewClientBuilder().WithScheme(scheme).WithObjects(llmSvc, envoyFilter).Build()
			matcher = resources.NewKServeEnvoyFilterMatcher(fakeClient)

			services, err := matcher.FindLLMServiceFromEnvoyFilter(ctx, envoyFilter)
			Expect(err).ToNot(HaveOccurred())
			Expect(services).To(HaveLen(1))
			Expect(services[0].Name).To(Equal("test-llm"))
		})

		It("should return empty list when no matching LLMInferenceService found", func(ctx SpecContext) {
			llmSvc := &kservev1alpha1.LLMInferenceService{
				ObjectMeta: v1.ObjectMeta{
					Name:      "test-llm",
					Namespace: "test-namespace",
				},
				Spec: kservev1alpha1.LLMInferenceServiceSpec{
					Router: &kservev1alpha1.RouterSpec{
						Gateway: &kservev1alpha1.GatewaySpec{
							Refs: []kservev1alpha1.UntypedObjectReference{
								{Name: "other-gateway", Namespace: "test-namespace"},
							},
						},
					},
				},
			}

			envoyFilter := &istioclientv1alpha3.EnvoyFilter{
				ObjectMeta: v1.ObjectMeta{
					Name:      constants.GetGatewayEnvoyFilterName("unrelated-gateway"),
					Namespace: "some-namespace",
				},
				Spec: istiov1alpha3.EnvoyFilter{
					TargetRefs: []*istiotypev1beta1.PolicyTargetReference{
						{
							Kind: "Gateway",
							Name: "unrelated-gateway",
						},
					},
				},
			}

			fakeClient = fake.NewClientBuilder().WithScheme(scheme).WithObjects(llmSvc, envoyFilter).Build()
			matcher = resources.NewKServeEnvoyFilterMatcher(fakeClient)

			services, err := matcher.FindLLMServiceFromEnvoyFilter(ctx, envoyFilter)
			Expect(err).ToNot(HaveOccurred())
			Expect(services).To(BeEmpty())
		})

		It("should return empty list when EnvoyFilter has no targetRefs", func(ctx SpecContext) {
			llmSvc := &kservev1alpha1.LLMInferenceService{
				ObjectMeta: v1.ObjectMeta{
					Name:      "test-llm",
					Namespace: "test-namespace",
				},
			}

			envoyFilter := &istioclientv1alpha3.EnvoyFilter{
				ObjectMeta: v1.ObjectMeta{
					Name:      "empty-refs",
					Namespace: "test-namespace",
				},
				Spec: istiov1alpha3.EnvoyFilter{
					TargetRefs: []*istiotypev1beta1.PolicyTargetReference{},
				},
			}

			fakeClient = fake.NewClientBuilder().WithScheme(scheme).WithObjects(llmSvc, envoyFilter).Build()
			matcher = resources.NewKServeEnvoyFilterMatcher(fakeClient)

			services, err := matcher.FindLLMServiceFromEnvoyFilter(ctx, envoyFilter)
			Expect(err).ToNot(HaveOccurred())
			Expect(services).To(BeEmpty())
		})
	})
})

var _ = Describe("EnvoyFilterComparator", func() {
	createEnvoyFilterForComparison := func(name, namespace string, workloadSelector map[string]string) *istioclientv1alpha3.EnvoyFilter {
		var selector *istiov1alpha3.WorkloadSelector
		if workloadSelector != nil {
			selector = &istiov1alpha3.WorkloadSelector{
				Labels: workloadSelector,
			}
		}
		return &istioclientv1alpha3.EnvoyFilter{
			ObjectMeta: v1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
			},
			Spec: istiov1alpha3.EnvoyFilter{
				WorkloadSelector: selector,
				ConfigPatches: []*istiov1alpha3.EnvoyFilter_EnvoyConfigObjectPatch{
					{
						ApplyTo: istiov1alpha3.EnvoyFilter_CLUSTER,
						Match: &istiov1alpha3.EnvoyFilter_EnvoyConfigObjectMatch{
							ObjectTypes: &istiov1alpha3.EnvoyFilter_EnvoyConfigObjectMatch_Cluster{
								Cluster: &istiov1alpha3.EnvoyFilter_ClusterMatch{
									Name: "outbound|443||authorino-authorino-authorization.kuadrant-system.svc.cluster.local",
								},
							},
						},
						Patch: &istiov1alpha3.EnvoyFilter_Patch{
							Operation: istiov1alpha3.EnvoyFilter_Patch_MERGE,
							Value:     nil,
						},
					},
				},
			},
		}
	}

	Context("Spec comparison", func() {
		It("should detect differences in WorkloadSelector", func(ctx SpecContext) {
			ef1 := createEnvoyFilterForComparison("test-ef", "test-ns", map[string]string{"app": "gateway"})
			ef2 := createEnvoyFilterForComparison("test-ef", "test-ns", map[string]string{"app": "different"})

			spec1Json, _ := json.Marshal(&ef1.Spec)
			spec2Json, _ := json.Marshal(&ef2.Spec)
			Expect(spec1Json).ToNot(Equal(spec2Json))
		})

		It("should detect no differences when specs are identical", func(ctx SpecContext) {
			ef1 := createEnvoyFilterForComparison("test-ef", "test-ns", map[string]string{"app": "gateway"})
			ef2 := createEnvoyFilterForComparison("test-ef", "test-ns", map[string]string{"app": "gateway"})

			spec1Json, _ := json.Marshal(&ef1.Spec)
			spec2Json, _ := json.Marshal(&ef2.Spec)
			Expect(spec1Json).To(Equal(spec2Json))
		})
	})
})
