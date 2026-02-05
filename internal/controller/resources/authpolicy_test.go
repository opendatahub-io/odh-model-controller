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
		var fakeClient client.Client
		BeforeEach(func() {
			scheme := runtime.NewScheme()
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
		})

		It("should resolve authenticated template for Gateway", func(ctx SpecContext) {
			authPolicy, err := loader.Load(ctx, resources.AuthPolicyTarget{
				Kind:      "Gateway",
				Name:      "openshift-ai-inference",
				Namespace: "openshift-ingress",
				AuthType:  constants.UserDefined,
			})

			Expect(err).ToNot(HaveOccurred())
			Expect(authPolicy).ToNot(BeNil())
			Expect(authPolicy.GetName()).To(Equal(constants.GetAuthPolicyName("openshift-ai-inference")))
			Expect(authPolicy.GetNamespace()).To(Equal("openshift-ingress"))
			Expect(authPolicy.Spec.AuthScheme.Authentication).To(HaveKey("kubernetes-user"))
		})

		It("should resolve anonymous template for HTTPRoute", func(ctx SpecContext) {
			authPolicy, err := loader.Load(ctx, resources.AuthPolicyTarget{
				Kind:      "HTTPRoute",
				Name:      "test-route",
				Namespace: "test-ns",
				AuthType:  constants.Anonymous,
			})

			Expect(err).ToNot(HaveOccurred())
			Expect(authPolicy).ToNot(BeNil())
			Expect(string(authPolicy.Spec.TargetRef.Name)).To(Equal("test-route"))
			Expect(authPolicy.GetName()).To(Equal(constants.GetAuthPolicyName("test-route")))
			Expect(authPolicy.GetNamespace()).To(Equal("test-ns"))
			Expect(authPolicy.Spec.AuthScheme.Authentication).To(HaveKey("public"))
		})

		It("should apply labels with WithLabels option", func(ctx SpecContext) {
			authPolicy, err := loader.Load(ctx, resources.AuthPolicyTarget{
				Kind:      "HTTPRoute",
				Name:      "test-route",
				Namespace: "test-ns",
				AuthType:  constants.Anonymous,
			}, resources.WithLabels(map[string]string{
				"app.kubernetes.io/name": "my-llmisvc",
			}))

			Expect(err).ToNot(HaveOccurred())
			Expect(authPolicy).ToNot(BeNil())
			Expect(authPolicy.GetLabels()).To(HaveKeyWithValue("app.kubernetes.io/name", "my-llmisvc"))
		})

		It("should return error when AuthType is not set", func(ctx SpecContext) {
			_, err := loader.Load(ctx, resources.AuthPolicyTarget{
				Kind:      "HTTPRoute",
				Name:      "test-route",
				Namespace: "test-ns",
			})

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("unsupported auth type"))
		})

		It("should apply audiences with WithAudiences option", func(ctx SpecContext) {
			authPolicy, err := loader.Load(ctx, resources.AuthPolicyTarget{
				Kind:      "Gateway",
				Name:      "openshift-ai-inference",
				Namespace: "openshift-ingress",
				AuthType:  constants.UserDefined,
			}, resources.WithAudiences([]string{"http://test.com"}))

			Expect(err).ToNot(HaveOccurred())
			Expect(authPolicy).ToNot(BeNil())

			Expect(authPolicy.Name).To(ContainSubstring("authn"))
			Expect(string(authPolicy.Spec.TargetRef.Kind)).To(Equal("Gateway"))
			Expect(authPolicy.Spec.AuthScheme.Authentication["kubernetes-user"].KubernetesTokenReview.Audiences).To(ContainElement("http://test.com"))
		})

		It("should create Gateway AuthPolicy with default Kubernetes audience", func(ctx SpecContext) {
			authPolicy, err := loader.Load(ctx, resources.AuthPolicyTarget{
				Kind:      "Gateway",
				Name:      "openshift-ai-inference",
				Namespace: "openshift-ingress",
				AuthType:  constants.UserDefined,
			})

			Expect(err).ToNot(HaveOccurred())
			Expect(authPolicy).ToNot(BeNil())

			Expect(authPolicy.Name).To(ContainSubstring("authn"))
			Expect(string(authPolicy.Spec.TargetRef.Kind)).To(Equal("Gateway"))
			Expect(authPolicy.Spec.AuthScheme.Authentication["kubernetes-user"].KubernetesTokenReview.Audiences).To(ContainElement(constants.KubernetesAudience))
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

var _ = Describe("FindLLMServiceFromAuthPolicy", func() {
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
		}

		namespacedName, found := resources.FindLLMServiceFromAuthPolicy(authPolicy)

		Expect(found).To(BeTrue())
		Expect(namespacedName.Name).To(Equal("my-llm-service"))
		Expect(namespacedName.Namespace).To(Equal("test-namespace"))
	})

	It("should return false when no LLMInferenceService OwnerReference found", func() {
		authPolicy := &kuadrantv1.AuthPolicy{
			ObjectMeta: v1.ObjectMeta{
				Name:      "test-auth-policy",
				Namespace: "test-namespace",
			},
		}

		namespacedName, found := resources.FindLLMServiceFromAuthPolicy(authPolicy)

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
		}

		namespacedName, found := resources.FindLLMServiceFromAuthPolicy(authPolicy)

		Expect(found).To(BeFalse())
		Expect(namespacedName).To(Equal(types.NamespacedName{}))
	})
})
