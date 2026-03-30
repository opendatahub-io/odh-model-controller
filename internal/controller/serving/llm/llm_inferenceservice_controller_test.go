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

package llm_test

import (
	"context"
	"os"

	kservev1alpha2 "github.com/kserve/kserve/pkg/apis/serving/v1alpha2"
	kuadrantv1 "github.com/kuadrant/kuadrant-operator/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/serving/llm/fixture"
	pkgtest "github.com/opendatahub-io/odh-model-controller/internal/controller/testing"
	testutils "github.com/opendatahub-io/odh-model-controller/test/utils"
)

const (
	LLMInferenceServiceName = "test-llmisvc"
	GatewayClassName        = "openshift-default"
)

var _ = Describe("LLMInferenceService Controller", func() {
	var testNs string
	var customGatewayName string
	var customHTTPRouteName string
	BeforeEach(func() {
		ctx := context.Background()
		customGatewayName = pkgtest.GenerateUniqueTestName("custom-gateway")
		customHTTPRouteName = pkgtest.GenerateUniqueTestName("custom-httproute")
		testNamespace := testutils.Namespaces.Create(ctx, envTest.Client)
		testNs = testNamespace.Name
	})

	AfterEach(func(ctx SpecContext) {
		// Clean up LLMInferenceServices to prevent cross-test interference
		// from watches (Gateway, ConfigMap) that list all services
		llmList := &kservev1alpha2.LLMInferenceServiceList{}
		if err := envTest.Client.List(ctx, llmList, client.InNamespace(testNs)); err == nil {
			for i := range llmList.Items {
				_ = envTest.Client.Delete(ctx, &llmList.Items[i])
			}
		}
	})

	Context("LLMInferenceService with Authentication", func() {
		Context("enable-auth annotation behavior", func() {
			It("should create Gateway AuthPolicy only when LLMInferenceService is created", func(ctx SpecContext) {
				fixture.CreateBasicLLMInferenceService(ctx, envTest.Client, testNs, LLMInferenceServiceName, nil)
				fixture.VerifyGatewayAuthPolicyOwnerRef(ctx, envTest.Client, constants.DefaultGatewayNamespace, constants.DefaultGatewayName)
			})

			It("should create AuthPolicies for Gateway/HTTPRoute when enable-auth annotation is false", func(ctx SpecContext) {
				enableAuth := false
				fixture.CreateBasicLLMInferenceService(ctx, envTest.Client, testNs, LLMInferenceServiceName, &enableAuth)

				fixture.CreateHTTPRouteForLLMService(ctx, envTest.Client, testNs, LLMInferenceServiceName)

				fixture.VerifyGatewayAuthPolicyOwnerRef(ctx, envTest.Client, constants.DefaultGatewayNamespace, constants.DefaultGatewayName)
				fixture.VerifyHTTPRouteAuthPolicyOwnerRef(ctx, envTest.Client, testNs, LLMInferenceServiceName)
			})

			It("should delete HTTPRoute AuthPolicy when annotation changes from false to true", func(ctx SpecContext) {
				enableAuth := false
				llmisvc := fixture.CreateBasicLLMInferenceService(ctx, envTest.Client, testNs, LLMInferenceServiceName, &enableAuth)

				fixture.CreateHTTPRouteForLLMService(ctx, envTest.Client, testNs, LLMInferenceServiceName)
				fixture.VerifyHTTPRouteAuthPolicyExists(ctx, envTest.Client, testNs, LLMInferenceServiceName)

				llmisvc.Annotations[constants.EnableAuthODHAnnotation] = "true"
				Expect(envTest.Client.Update(ctx, llmisvc)).Should(Succeed())

				fixture.VerifyHTTPRouteAuthPolicyNotExist(ctx, envTest.Client, testNs, LLMInferenceServiceName)
			})

			It("should create HTTPRoute AuthPolicy when annotation changes from true to false", func(ctx SpecContext) {
				enableAuth := true
				llmisvc := fixture.CreateBasicLLMInferenceService(ctx, envTest.Client, testNs, LLMInferenceServiceName, &enableAuth)

				fixture.CreateHTTPRouteForLLMService(ctx, envTest.Client, testNs, LLMInferenceServiceName)
				fixture.VerifyHTTPRouteAuthPolicyNotExist(ctx, envTest.Client, testNs, LLMInferenceServiceName)

				llmisvc.Annotations[constants.EnableAuthODHAnnotation] = "false"
				Expect(envTest.Client.Update(ctx, llmisvc)).Should(Succeed())

				fixture.VerifyHTTPRouteAuthPolicyOwnerRef(ctx, envTest.Client, testNs, LLMInferenceServiceName)
			})

			It("should create AuthPolicy for custom Gateway when LLMInferenceService has gateway reference", func(ctx SpecContext) {
				createTestGateway(ctx, customGatewayName, testNs)

				llmisvc := fixture.LLMInferenceService(LLMInferenceServiceName,
					fixture.InNamespace[*kservev1alpha2.LLMInferenceService](testNs),
					fixture.WithGatewayRefs(fixture.LLMGatewayRef(customGatewayName, testNs)),
				)
				Expect(envTest.Client.Create(ctx, llmisvc)).Should(Succeed())

				fixture.VerifyGatewayAuthPolicyOwnerRef(ctx, envTest.Client, testNs, customGatewayName)
			})

			It("should create AuthPolicy for custom HTTPRoute when LLMInferenceService has HTTPRoute reference with enable-auth=false", func(ctx SpecContext) {
				customHTTPRoute := fixture.HTTPRoute(customHTTPRouteName,
					fixture.InNamespace[*gatewayapiv1.HTTPRoute](testNs),
				)
				Expect(envTest.Client.Create(ctx, customHTTPRoute)).Should(Succeed())

				llmisvc := fixture.LLMInferenceService(LLMInferenceServiceName,
					fixture.InNamespace[*kservev1alpha2.LLMInferenceService](testNs),
					fixture.WithEnableAuth(false),
					fixture.WithHTTPRouteRefs(fixture.HTTPRouteRef(customHTTPRouteName)),
				)
				Expect(envTest.Client.Create(ctx, llmisvc)).Should(Succeed())

				fixture.VerifyCustomHTTPRouteAuthPolicyExists(ctx, envTest.Client, testNs, LLMInferenceServiceName, customHTTPRouteName)
			})
		})

		Context("AuthPolicy Reconcile Tests", func() {
			// setupHTTPRouteAuthPolicy creates the common LLMInferenceService + HTTPRoute + AuthPolicy setup.
			setupHTTPRouteAuthPolicy := func(ctx context.Context) {
				enableAuth := false
				fixture.CreateBasicLLMInferenceService(ctx, envTest.Client, testNs, LLMInferenceServiceName, &enableAuth)
				fixture.CreateHTTPRouteForLLMService(ctx, envTest.Client, testNs, LLMInferenceServiceName)
			}

			// setupCustomHTTPRouteAuthPolicy creates the common custom HTTPRoute + LLMInferenceService setup.
			setupCustomHTTPRouteAuthPolicy := func(ctx context.Context) {
				customHTTPRoute := fixture.HTTPRoute(customHTTPRouteName,
					fixture.InNamespace[*gatewayapiv1.HTTPRoute](testNs),
				)
				Expect(envTest.Client.Create(ctx, customHTTPRoute)).Should(Succeed())

				llmisvc := fixture.LLMInferenceService(LLMInferenceServiceName,
					fixture.InNamespace[*kservev1alpha2.LLMInferenceService](testNs),
					fixture.WithEnableAuth(false),
					fixture.WithHTTPRouteRefs(fixture.HTTPRouteRef(customHTTPRouteName)),
				)
				Expect(envTest.Client.Create(ctx, llmisvc)).Should(Succeed())
			}

			Context("when HTTPRoute AuthPolicy is modified or deleted", func() {
				It("should reconcile and restore HTTPRoute AuthPolicy when modified", func(ctx SpecContext) {
					setupHTTPRouteAuthPolicy(ctx)

					httpRouteName := constants.GetHTTPRouteName(LLMInferenceServiceName)
					httpRouteAuthPolicy := fixture.WaitForResource(ctx, envTest.Client, testNs, constants.GetAuthPolicyName(httpRouteName), &kuadrantv1.AuthPolicy{})
					originalTargetRef := httpRouteAuthPolicy.Spec.TargetRef

					httpRouteAuthPolicy.Spec.TargetRef.Name = "modified-httproute"
					Expect(envTest.Client.Update(ctx, httpRouteAuthPolicy)).Should(Succeed())

					fixture.VerifyHTTPRouteAuthPolicyRestored(ctx, envTest.Client, testNs, LLMInferenceServiceName, originalTargetRef.Name)
				})

				It("should recreate HTTPRoute AuthPolicy when deleted", func(ctx SpecContext) {
					setupHTTPRouteAuthPolicy(ctx)

					httpRouteName := constants.GetHTTPRouteName(LLMInferenceServiceName)
					httpRouteAuthPolicy := fixture.WaitForResource(ctx, envTest.Client, testNs, constants.GetAuthPolicyName(httpRouteName), &kuadrantv1.AuthPolicy{})

					Expect(envTest.Client.Delete(ctx, httpRouteAuthPolicy)).Should(Succeed())

					fixture.VerifyResourceExists(ctx, envTest.Client, testNs, constants.GetAuthPolicyName(httpRouteName), &kuadrantv1.AuthPolicy{})
				})

				It("should restore custom HTTPRoute AuthPolicy when modified", func(ctx SpecContext) {
					setupCustomHTTPRouteAuthPolicy(ctx)

					fixture.VerifyCustomHTTPRouteAuthPolicyExists(ctx, envTest.Client, testNs, LLMInferenceServiceName, customHTTPRouteName)

					httpRouteAuthPolicy := fixture.WaitForCustomHTTPRouteAuthPolicy(ctx, envTest.Client, testNs, customHTTPRouteName)
					originalTargetRef := httpRouteAuthPolicy.Spec.TargetRef

					httpRouteAuthPolicy.Spec.TargetRef.Name = "modified-custom-httproute"
					Expect(envTest.Client.Update(ctx, httpRouteAuthPolicy)).Should(Succeed())

					fixture.VerifyCustomHTTPRouteAuthPolicyRestored(ctx, envTest.Client, testNs, customHTTPRouteName, originalTargetRef.Name)
				})

				It("should recreate custom HTTPRoute AuthPolicy when deleted", func(ctx SpecContext) {
					setupCustomHTTPRouteAuthPolicy(ctx)

					httpRouteAuthPolicy := fixture.WaitForCustomHTTPRouteAuthPolicy(ctx, envTest.Client, testNs, customHTTPRouteName)

					Expect(envTest.Client.Delete(ctx, httpRouteAuthPolicy)).Should(Succeed())

					fixture.VerifyCustomHTTPRouteAuthPolicyExists(ctx, envTest.Client, testNs, LLMInferenceServiceName, customHTTPRouteName)
				})
			})
		})
	})
})

var _ = Describe("BaseRefs and Spec Merging", func() {
	var testNs string

	BeforeEach(func() {
		ctx := context.Background()
		testNamespace := testutils.Namespaces.Create(ctx, envTest.Client)
		testNs = testNamespace.Name
	})

	// createConfigWithGatewayRef creates an LLMInferenceServiceConfig that references a gateway.
	createConfigWithGatewayRef := func(ctx context.Context, configName, namespace, gatewayName, gatewayNamespace string) {
		config := fixture.LLMInferenceServiceConfig(configName,
			fixture.InNamespace[*kservev1alpha2.LLMInferenceServiceConfig](namespace),
		)
		config.Spec.Router = &kservev1alpha2.RouterSpec{
			Gateway: &kservev1alpha2.GatewaySpec{
				Refs: []kservev1alpha2.UntypedObjectReference{
					fixture.LLMGatewayRef(gatewayName, gatewayNamespace),
				},
			},
		}
		ExpectWithOffset(1, envTest.Client.Create(ctx, config)).Should(Succeed())
	}

	Context("LLMInferenceServiceConfig retrieval", func() {
		It("should retrieve config from service namespace and create AuthPolicy for referenced gateway", func(ctx SpecContext) {
			gatewayName := pkgtest.GenerateUniqueTestName("config-gateway")

			createTestGateway(ctx, gatewayName, testNs)
			createConfigWithGatewayRef(ctx, "test-config", testNs, gatewayName, testNs)

			llmisvc := fixture.LLMInferenceService(LLMInferenceServiceName,
				fixture.InNamespace[*kservev1alpha2.LLMInferenceService](testNs),
				fixture.WithBaseRefs(corev1.LocalObjectReference{Name: "test-config"}),
			)
			Expect(envTest.Client.Create(ctx, llmisvc)).Should(Succeed())

			fixture.VerifyGatewayAuthPolicyOwnerRef(ctx, envTest.Client, testNs, gatewayName)
		})

		It("should retrieve config from system namespace when not found in service namespace", func(ctx SpecContext) {
			gatewayName := pkgtest.GenerateUniqueTestName("system-gateway")
			systemNamespace := testutils.Namespaces.Create(ctx, envTest.Client).Name

			_ = os.Setenv("POD_NAMESPACE", systemNamespace)
			defer func() { _ = os.Unsetenv("POD_NAMESPACE") }()

			createTestGateway(ctx, gatewayName, systemNamespace)
			createConfigWithGatewayRef(ctx, "system-config", systemNamespace, gatewayName, systemNamespace)

			llmisvc := fixture.LLMInferenceService(LLMInferenceServiceName,
				fixture.InNamespace[*kservev1alpha2.LLMInferenceService](testNs),
				fixture.WithBaseRefs(corev1.LocalObjectReference{Name: "system-config"}),
			)
			Expect(envTest.Client.Create(ctx, llmisvc)).Should(Succeed())

			fixture.VerifyGatewayAuthPolicyOwnerRef(ctx, envTest.Client, systemNamespace, gatewayName)
		})

		It("should prioritize service namespace config over system namespace config", func(ctx SpecContext) {
			serviceGatewayName := pkgtest.GenerateUniqueTestName("service-gateway")
			systemGatewayName := pkgtest.GenerateUniqueTestName("system-gateway")
			systemNamespace := testutils.Namespaces.Create(ctx, envTest.Client).Name

			_ = os.Setenv("POD_NAMESPACE", systemNamespace)
			defer func() { _ = os.Unsetenv("POD_NAMESPACE") }()

			createTestGateway(ctx, serviceGatewayName, testNs)
			createTestGateway(ctx, systemGatewayName, systemNamespace)

			createConfigWithGatewayRef(ctx, "shared-config", testNs, serviceGatewayName, testNs)
			createConfigWithGatewayRef(ctx, "shared-config", systemNamespace, systemGatewayName, systemNamespace)

			llmisvc := fixture.LLMInferenceService(LLMInferenceServiceName,
				fixture.InNamespace[*kservev1alpha2.LLMInferenceService](testNs),
				fixture.WithBaseRefs(corev1.LocalObjectReference{Name: "shared-config"}),
			)
			Expect(envTest.Client.Create(ctx, llmisvc)).Should(Succeed())

			fixture.VerifyGatewayAuthPolicyOwnerRef(ctx, envTest.Client, testNs, serviceGatewayName)

			verifyResourcePersistentlyAbsent(ctx, systemNamespace, constants.GetAuthPolicyName(systemGatewayName), &kuadrantv1.AuthPolicy{})
		})

		It("should handle config not found when POD_NAMESPACE is not set", func(ctx SpecContext) {
			llmisvc := fixture.LLMInferenceService(LLMInferenceServiceName,
				fixture.InNamespace[*kservev1alpha2.LLMInferenceService](testNs),
				fixture.WithBaseRefs(corev1.LocalObjectReference{Name: "missing-config"}),
			)
			Expect(envTest.Client.Create(ctx, llmisvc)).Should(Succeed())

			fixture.VerifyGatewayAuthPolicyOwnerRef(ctx, envTest.Client, constants.DefaultGatewayNamespace, constants.DefaultGatewayName)
		})
	})

	Context("Multiple BaseRefs and Spec Merging", func() {
		It("should merge multiple config specs from BaseRefs", func(ctx SpecContext) {
			gatewayName := pkgtest.GenerateUniqueTestName("gateway1")

			createTestGateway(ctx, gatewayName, testNs)
			createConfigWithGatewayRef(ctx, "config1", testNs, gatewayName, testNs)

			config2 := fixture.LLMInferenceServiceConfig("config2",
				fixture.InNamespace[*kservev1alpha2.LLMInferenceServiceConfig](testNs),
			)
			Expect(envTest.Client.Create(ctx, config2)).Should(Succeed())

			llmisvc := fixture.LLMInferenceService(LLMInferenceServiceName,
				fixture.InNamespace[*kservev1alpha2.LLMInferenceService](testNs),
				fixture.WithBaseRefs(
					corev1.LocalObjectReference{Name: "config1"},
					corev1.LocalObjectReference{Name: "config2"},
				),
			)
			Expect(envTest.Client.Create(ctx, llmisvc)).Should(Succeed())

			fixture.VerifyGatewayAuthPolicyOwnerRef(ctx, envTest.Client, testNs, gatewayName)
		})

		It("should allow service spec to override config specs", func(ctx SpecContext) {
			configGatewayName := pkgtest.GenerateUniqueTestName("config-gateway")
			serviceGatewayName := pkgtest.GenerateUniqueTestName("service-gateway")

			createTestGateway(ctx, configGatewayName, testNs)
			createTestGateway(ctx, serviceGatewayName, testNs)
			createConfigWithGatewayRef(ctx, "test-config", testNs, configGatewayName, testNs)

			llmisvc := fixture.LLMInferenceService(LLMInferenceServiceName,
				fixture.InNamespace[*kservev1alpha2.LLMInferenceService](testNs),
				fixture.WithBaseRefs(corev1.LocalObjectReference{Name: "test-config"}),
				fixture.WithGatewayRefs(fixture.LLMGatewayRef(serviceGatewayName, testNs)),
			)
			Expect(envTest.Client.Create(ctx, llmisvc)).Should(Succeed())

			fixture.VerifyGatewayAuthPolicyOwnerRef(ctx, envTest.Client, testNs, serviceGatewayName)
			fixture.VerifyGatewayAuthPolicyNotExist(ctx, envTest.Client, testNs, configGatewayName)
		})

		It("should handle config fetch failures gracefully and continue with available configs", func(ctx SpecContext) {
			gatewayName := pkgtest.GenerateUniqueTestName("gateway")

			createTestGateway(ctx, gatewayName, testNs)
			createConfigWithGatewayRef(ctx, "config2", testNs, gatewayName, testNs)

			llmisvc := fixture.LLMInferenceService(LLMInferenceServiceName,
				fixture.InNamespace[*kservev1alpha2.LLMInferenceService](testNs),
				fixture.WithBaseRefs(
					corev1.LocalObjectReference{Name: "config1"}, // Will fail
					corev1.LocalObjectReference{Name: "config2"}, // Will succeed
				),
			)
			Expect(envTest.Client.Create(ctx, llmisvc)).Should(Succeed())

			fixture.VerifyGatewayAuthPolicyOwnerRef(ctx, envTest.Client, testNs, gatewayName)
		})
	})

	Context("LLMInferenceService with RBAC", func() {
		It("should create Role and RoleBinding when LLMInferenceService is created", func(ctx SpecContext) {
			fixture.CreateBasicLLMInferenceService(ctx, envTest.Client, testNs, LLMInferenceServiceName, nil)

			role := fixture.WaitForInferenceAccessRole(ctx, envTest.Client, testNs, LLMInferenceServiceName)
			Expect(role.Rules).To(HaveLen(1))
			Expect(role.Rules[0].APIGroups).To(Equal([]string{"serving.kserve.io"}))
			Expect(role.Rules[0].Resources).To(Equal([]string{"llminferenceservices"}))
			Expect(role.Rules[0].ResourceNames).To(Equal([]string{LLMInferenceServiceName}))
			Expect(role.Rules[0].Verbs).To(Equal([]string{"get"}))
			Expect(role.Labels["app.kubernetes.io/managed-by"]).To(Equal("odh-model-controller"))

			rb := fixture.WaitForInferenceAccessRoleBinding(ctx, envTest.Client, testNs, LLMInferenceServiceName)
			Expect(rb.RoleRef.Kind).To(Equal("Role"))
			Expect(rb.RoleRef.Name).To(Equal(fixture.GetInferenceAccessRoleName(LLMInferenceServiceName)))
			Expect(rb.Subjects).To(HaveLen(1))
			Expect(rb.Subjects[0].Kind).To(Equal("Group"))
			Expect(rb.Subjects[0].Name).To(Equal("system:authenticated"))
		})

		It("should restore Role rules on next reconcile after manual modification", func(ctx SpecContext) {
			llmisvc := fixture.CreateBasicLLMInferenceService(ctx, envTest.Client, testNs, LLMInferenceServiceName, nil)

			role := fixture.WaitForInferenceAccessRole(ctx, envTest.Client, testNs, LLMInferenceServiceName)
			originalVerbs := role.Rules[0].Verbs

			role.Rules[0].Verbs = []string{"get", "list", "delete"}
			Expect(envTest.Client.Update(ctx, role)).Should(Succeed())

			// Trigger reconcile by touching the LLMInferenceService
			if llmisvc.Annotations == nil {
				llmisvc.Annotations = make(map[string]string)
			}
			llmisvc.Annotations["test/trigger"] = "reconcile"
			Expect(envTest.Client.Update(ctx, llmisvc)).Should(Succeed())

			Eventually(func(g Gomega) {
				updated := fixture.WaitForInferenceAccessRole(ctx, envTest.Client, testNs, LLMInferenceServiceName)
				g.Expect(updated.Rules[0].Verbs).To(Equal(originalVerbs))
			}).WithContext(ctx).Should(Succeed())
		})

		It("should restore RoleBinding subjects on next reconcile after manual modification", func(ctx SpecContext) {
			llmisvc := fixture.CreateBasicLLMInferenceService(ctx, envTest.Client, testNs, LLMInferenceServiceName, nil)

			rb := fixture.WaitForInferenceAccessRoleBinding(ctx, envTest.Client, testNs, LLMInferenceServiceName)

			rb.Subjects = []rbacv1.Subject{
				{Kind: "User", APIGroup: rbacv1.GroupName, Name: "rogue-user"},
			}
			Expect(envTest.Client.Update(ctx, rb)).Should(Succeed())

			// Trigger reconcile by touching the LLMInferenceService
			if llmisvc.Annotations == nil {
				llmisvc.Annotations = make(map[string]string)
			}
			llmisvc.Annotations["test/trigger"] = "reconcile"
			Expect(envTest.Client.Update(ctx, llmisvc)).Should(Succeed())

			Eventually(func(g Gomega) {
				updated := fixture.WaitForInferenceAccessRoleBinding(ctx, envTest.Client, testNs, LLMInferenceServiceName)
				g.Expect(updated.Subjects).To(HaveLen(1))
				g.Expect(updated.Subjects[0].Kind).To(Equal("Group"))
				g.Expect(updated.Subjects[0].Name).To(Equal("system:authenticated"))
			}).WithContext(ctx).Should(Succeed())
		})

		It("should have OwnerReference pointing to LLMInferenceService", func(ctx SpecContext) {
			llmisvc := fixture.CreateBasicLLMInferenceService(ctx, envTest.Client, testNs, LLMInferenceServiceName, nil)

			role := fixture.WaitForInferenceAccessRole(ctx, envTest.Client, testNs, LLMInferenceServiceName)
			Expect(role.OwnerReferences).To(HaveLen(1))
			Expect(role.OwnerReferences[0].Name).To(Equal(llmisvc.Name))
			Expect(role.OwnerReferences[0].UID).To(Equal(llmisvc.UID))

			rb := fixture.WaitForInferenceAccessRoleBinding(ctx, envTest.Client, testNs, LLMInferenceServiceName)
			Expect(rb.OwnerReferences).To(HaveLen(1))
			Expect(rb.OwnerReferences[0].Name).To(Equal(llmisvc.Name))
			Expect(rb.OwnerReferences[0].UID).To(Equal(llmisvc.UID))
		})

		It("should skip reconciliation when Role has opendatahub.io/managed=false label", func(ctx SpecContext) {
			fixture.CreateBasicLLMInferenceService(ctx, envTest.Client, testNs, LLMInferenceServiceName, nil)

			role := fixture.WaitForInferenceAccessRole(ctx, envTest.Client, testNs, LLMInferenceServiceName)

			role.Labels[constants.ODHManaged] = "false"
			role.Rules[0].Verbs = []string{"get", "list"}
			Expect(envTest.Client.Update(ctx, role)).Should(Succeed())

			Consistently(func(g Gomega) {
				updated := fixture.WaitForInferenceAccessRole(ctx, envTest.Client, testNs, LLMInferenceServiceName)
				g.Expect(updated.Rules[0].Verbs).To(Equal([]string{"get", "list"}))
			}).WithContext(ctx).Should(Succeed())
		})

		It("should skip reconciliation when RoleBinding has opendatahub.io/managed=false label", func(ctx SpecContext) {
			fixture.CreateBasicLLMInferenceService(ctx, envTest.Client, testNs, LLMInferenceServiceName, nil)

			rb := fixture.WaitForInferenceAccessRoleBinding(ctx, envTest.Client, testNs, LLMInferenceServiceName)

			rb.Labels[constants.ODHManaged] = "false"
			rb.Subjects = []rbacv1.Subject{
				{Kind: "User", APIGroup: rbacv1.GroupName, Name: "custom-user"},
			}
			Expect(envTest.Client.Update(ctx, rb)).Should(Succeed())

			Consistently(func(g Gomega) {
				updated := fixture.WaitForInferenceAccessRoleBinding(ctx, envTest.Client, testNs, LLMInferenceServiceName)
				g.Expect(updated.Subjects).To(HaveLen(1))
				g.Expect(updated.Subjects[0].Kind).To(Equal("User"))
				g.Expect(updated.Subjects[0].Name).To(Equal("custom-user"))
			}).WithContext(ctx).Should(Succeed())
		})

		It("should recreate RoleBinding when RoleRef points at the wrong Role", func(ctx SpecContext) {
			llmisvc := fixture.CreateBasicLLMInferenceService(ctx, envTest.Client, testNs, LLMInferenceServiceName, nil)

			_ = fixture.WaitForInferenceAccessRole(ctx, envTest.Client, testNs, LLMInferenceServiceName)
			rb := fixture.WaitForInferenceAccessRoleBinding(ctx, envTest.Client, testNs, LLMInferenceServiceName)
			rbName := rb.Name

			wrongRoleName := "wrong-role-ref-target"
			wrongRole := &rbacv1.Role{
				ObjectMeta: metav1.ObjectMeta{Name: wrongRoleName, Namespace: testNs},
				Rules: []rbacv1.PolicyRule{{
					APIGroups: []string{""},
					Resources: []string{"configmaps"},
					Verbs:     []string{"get"},
				}},
			}
			Expect(envTest.Client.Create(ctx, wrongRole)).Should(Succeed())
			Expect(envTest.Client.Delete(ctx, rb)).Should(Succeed())

			broken := &rbacv1.RoleBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name:      rbName,
					Namespace: testNs,
					Labels:    rb.Labels,
				},
				Subjects: rb.Subjects,
				RoleRef: rbacv1.RoleRef{
					APIGroup: rbacv1.GroupName,
					Kind:     "Role",
					Name:     wrongRoleName,
				},
			}
			Expect(controllerutil.SetControllerReference(llmisvc, broken, envTest.Environment.Scheme)).Should(Succeed())
			Expect(envTest.Client.Create(ctx, broken)).Should(Succeed())

			if llmisvc.Annotations == nil {
				llmisvc.Annotations = make(map[string]string)
			}
			llmisvc.Annotations["test/trigger-roleref-recreate"] = "1"
			Expect(envTest.Client.Update(ctx, llmisvc)).Should(Succeed())

			Eventually(func(g Gomega) {
				fixed := fixture.WaitForInferenceAccessRoleBinding(ctx, envTest.Client, testNs, LLMInferenceServiceName)
				g.Expect(fixed.RoleRef.Name).To(Equal(fixture.GetInferenceAccessRoleName(LLMInferenceServiceName)))
				g.Expect(fixed.Subjects).To(HaveLen(1))
				g.Expect(fixed.Subjects[0].Name).To(Equal("system:authenticated"))
			}).WithContext(ctx).Should(Succeed())
		})
	})

	Context("Gateway filtering with BaseRefs", func() {
		It("should exclude gateway with managed=false label when using BaseRefs", func(ctx SpecContext) {
			unmanagedGatewayName := pkgtest.GenerateUniqueTestName("unmanaged-gateway")

			unmanagedGateway := fixture.Gateway(unmanagedGatewayName,
				fixture.InNamespace[*gatewayapiv1.Gateway](testNs),
				fixture.WithClassName(GatewayClassName),
				fixture.WithListener(gatewayapiv1.HTTPProtocolType),
				fixture.WithUnmanagedLabel(),
			)
			Expect(envTest.Client.Create(ctx, unmanagedGateway)).Should(Succeed())

			createConfigWithGatewayRef(ctx, "test-config", testNs, unmanagedGatewayName, testNs)

			llmisvc := fixture.LLMInferenceService(LLMInferenceServiceName,
				fixture.InNamespace[*kservev1alpha2.LLMInferenceService](testNs),
				fixture.WithBaseRefs(corev1.LocalObjectReference{Name: "test-config"}),
			)
			Expect(envTest.Client.Create(ctx, llmisvc)).Should(Succeed())

			verifyResourcePersistentlyAbsent(ctx, testNs, constants.GetAuthPolicyName(unmanagedGatewayName), &kuadrantv1.AuthPolicy{})
		})
	})
})

// Helper Functions

// createTestGateway creates a basic gateway for testing in the given namespace.
func createTestGateway(ctx context.Context, name, namespace string) {
	GinkgoHelper()
	gw := fixture.Gateway(name,
		fixture.InNamespace[*gatewayapiv1.Gateway](namespace),
		fixture.WithClassName(GatewayClassName),
		fixture.WithListener(gatewayapiv1.HTTPProtocolType),
	)
	Expect(envTest.Client.Create(ctx, gw)).Should(Succeed())
}

// verifyResourcePersistentlyAbsent checks that a resource remains absent (Consistently + IsNotFound).
func verifyResourcePersistentlyAbsent[T client.Object](ctx context.Context, namespace, name string, obj T) {
	GinkgoHelper()
	Consistently(func() error {
		return envTest.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, obj)
	}).WithContext(ctx).Should(And(
		Not(Succeed()),
		WithTransform(errors.IsNotFound, BeTrue()),
	))
}
