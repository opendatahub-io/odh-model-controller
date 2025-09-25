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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
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

		BeforeEach(func() {
			scheme := runtime.NewScheme()
			Expect(kservev1alpha1.AddToScheme(scheme)).To(Succeed())
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
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
			Expect(authPolicies[0].GetName()).To(Equal(constants.GetAuthPolicyName(dummyLLMISvc.Name)))
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
	})
})

func createTestAuthPolicy() *kuadrantv1.AuthPolicy {
	return &kuadrantv1.AuthPolicy{
		ObjectMeta: v1.ObjectMeta{
			Name:      constants.GetAuthPolicyName("test-llm"),
			Namespace: "test-namespace",
		},
		Spec: kuadrantv1.AuthPolicySpec{
			TargetRef: gwapiv1alpha2.LocalPolicyTargetReferenceWithSectionName{
				LocalPolicyTargetReference: gwapiv1alpha2.LocalPolicyTargetReference{
					Group: "gateway.networking.k8s.io",
					Kind:  "HTTPRoute",
					Name:  gwapiv1alpha2.ObjectName(constants.GetHTTPRouteName("test-llm")),
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
				Name:      constants.GetAuthPolicyName("non-existent"),
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
			nonExistentAuthPolicy.Name = constants.GetAuthPolicyName("non-existent")

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
				Name:      constants.GetAuthPolicyName("non-existent"),
				Namespace: "test-namespace",
			}
			err := store.Remove(ctx, key)
			Expect(err).ToNot(HaveOccurred())
		})

	})
})
