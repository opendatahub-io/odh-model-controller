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
	"context"
	"os"

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/resources"
)

var _ = Describe("AuthPolicyDetector", func() {
	var detector resources.AuthPolicyDetector
	ctx := context.Background()

	BeforeEach(func() {
		detector = resources.NewKServeAuthPolicyDetector(nil)
	})

	It("should return UserDefined when annotation is 'true'", func() {
		annotations := map[string]string{
			constants.EnableAuthODHAnnotation: "true",
		}

		result := detector.Detect(ctx, annotations)

		Expect(result).To(Equal(constants.UserDefined))
	})

	It("should return Anonymous when annotation is 'false'", func() {
		annotations := map[string]string{
			constants.EnableAuthODHAnnotation: "false",
		}

		result := detector.Detect(ctx, annotations)

		Expect(result).To(Equal(constants.Anonymous))
	})

	It("should return UserDefined when annotation is empty string", func() {
		annotations := map[string]string{
			constants.EnableAuthODHAnnotation: "",
		}

		result := detector.Detect(ctx, annotations)

		Expect(result).To(Equal(constants.UserDefined))
	})

	It("should return UserDefined when annotation does not exist", func() {
		annotations := map[string]string{}

		result := detector.Detect(ctx, annotations)

		Expect(result).To(Equal(constants.UserDefined))
	})

	It("should be case-insensitive for 'true' value", func() {
		testCases := []string{"TRUE", "True", "tRuE"}

		for _, value := range testCases {
			annotations := map[string]string{
				constants.EnableAuthODHAnnotation: value,
			}

			result := detector.Detect(ctx, annotations)

			Expect(result).To(Equal(constants.UserDefined), "Expected UserDefined for case variation: %s", value)
		}
	})

	It("should be case-insensitive for 'false' value", func() {
		testCases := []string{"FALSE", "False", "fAlSe"}

		for _, value := range testCases {
			annotations := map[string]string{
				constants.EnableAuthODHAnnotation: value,
			}

			result := detector.Detect(ctx, annotations)

			Expect(result).To(Equal(constants.Anonymous), "Expected Anonymous for case variation: %s", value)
		}
	})

	It("should return UserDefined for any other invalid values", func() {
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
		dummyLLMISvc := kservev1alpha1.LLMInferenceService{
			ObjectMeta: v1.ObjectMeta{
				Namespace: "test-ns",
				Name:      "test-llm",
			},
		}

		It("should resolve UserDefined template for LLMInferenceService", func() {
			scheme := runtime.NewScheme()
			Expect(kservev1alpha1.AddToScheme(scheme)).To(Succeed())
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
			loader := resources.NewKServeAuthPolicyTemplateLoader(fakeClient, scheme)
			authPolicy, err := loader.Load(
				context.Background(),
				constants.UserDefined,
				&dummyLLMISvc)

			Expect(err).To(Succeed())
			Expect(authPolicy).ToNot(BeNil())
			Expect(authPolicy.GetName()).To(Equal(constants.GetAuthPolicyName(dummyLLMISvc.Name)))
			Expect(authPolicy.GetNamespace()).To(Equal(dummyLLMISvc.Namespace))
		})

		It("should resolve Anonymous template for LLMInferenceService", func() {
			scheme := runtime.NewScheme()
			Expect(kservev1alpha1.AddToScheme(scheme)).To(Succeed())
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
			loader := resources.NewKServeAuthPolicyTemplateLoader(fakeClient, scheme)
			authPolicy, err := loader.Load(
				context.Background(),
				constants.Anonymous,
				&dummyLLMISvc)

			Expect(err).To(Succeed())
			Expect(authPolicy).ToNot(BeNil())
			Expect(authPolicy.GetName()).To(Equal(constants.GetAuthPolicyName(dummyLLMISvc.Name)))
			Expect(authPolicy.GetNamespace()).To(Equal(dummyLLMISvc.Namespace))
		})

		It("should return error for unsupported auth type", func() {
			scheme := runtime.NewScheme()
			Expect(kservev1alpha1.AddToScheme(scheme)).To(Succeed())
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
			loader := resources.NewKServeAuthPolicyTemplateLoader(fakeClient, scheme)
			authPolicy, err := loader.Load(
				context.Background(),
				"unsupported-type",
				&dummyLLMISvc)

			Expect(err).To(HaveOccurred())
			Expect(authPolicy).To(BeNil())
			Expect(err.Error()).To(ContainSubstring("unsupported AuthPolicy type"))
		})

		It("should read AUTH_AUDIENCE env var for Audience", func() {
			_ = os.Setenv("AUTH_AUDIENCE", "http://test.com")
			defer func() {
				_ = os.Unsetenv("AUTH_AUDIENCE")
			}()

			scheme := runtime.NewScheme()
			Expect(kservev1alpha1.AddToScheme(scheme)).To(Succeed())
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
			loader := resources.NewKServeAuthPolicyTemplateLoader(fakeClient, scheme)
			authPolicy, err := loader.Load(
				context.Background(),
				constants.UserDefined,
				&dummyLLMISvc)

			Expect(err).To(Succeed())
			Expect(authPolicy).ToNot(BeNil())

			audiences, found, err := unstructured.NestedStringSlice(authPolicy.Object, "spec", "defaults", "rules", "authentication", "kubernetes-user", "kubernetesTokenReview", "audiences")
			Expect(err).To(Succeed())
			Expect(found).To(BeTrue())
			Expect(audiences).To(ContainElement("http://test.com"))
		})
	})
})
