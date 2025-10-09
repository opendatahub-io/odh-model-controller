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

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/types"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/serving/llm/fixture"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
	pkgtest "github.com/opendatahub-io/odh-model-controller/internal/controller/testing"
	testutils "github.com/opendatahub-io/odh-model-controller/test/utils"
)

const (
	LLMInferenceServiceName = "test-llmisvc"
	CustomGatewayName       = "ready-gateway"
	CustomHTTPRouteName     = "custom-httproute"
	LLMServicePath1         = "./testdata/deploy/test-llm-inference-service.yaml"
	LLMServicePath2         = "./testdata/deploy/test-llm-inference-service-2.yaml"
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
				customGatewayNamespace := testNs

				customGateway := fixture.Gateway(customGatewayName,
					fixture.InNamespace[*gatewayapiv1.Gateway](customGatewayNamespace),
					fixture.WithClassName(GatewayClassName),
					fixture.WithListener(gatewayapiv1.HTTPProtocolType),
				)
				Expect(envTest.Client.Create(ctx, customGateway)).Should(Succeed())

				llmisvc := fixture.LLMInferenceService(LLMInferenceServiceName,
					fixture.InNamespace[*kservev1alpha1.LLMInferenceService](testNs),
					fixture.WithGatewayRefs(kservev1alpha1.UntypedObjectReference{
						Name:      gatewayapiv1.ObjectName(customGatewayName),
						Namespace: gatewayapiv1.Namespace(customGatewayNamespace),
					}),
				)
				Expect(envTest.Client.Create(ctx, llmisvc)).Should(Succeed())

				fixture.VerifyGatewayAuthPolicyOwnerRef(ctx, envTest.Client, customGatewayNamespace, customGatewayName)
			})

			It("should create AuthPolicy for custom HTTPRoute when LLMInferenceService has HTTPRoute reference with enable-auth=false", func(ctx SpecContext) {
				customHTTPRoute := fixture.HTTPRoute(customHTTPRouteName,
					fixture.InNamespace[*gatewayapiv1.HTTPRoute](testNs),
				)
				Expect(envTest.Client.Create(ctx, customHTTPRoute)).Should(Succeed())

				llmisvc := fixture.LLMInferenceService(LLMInferenceServiceName,
					fixture.InNamespace[*kservev1alpha1.LLMInferenceService](testNs),
					fixture.WithEnableAuth(false),
					fixture.WithHTTPRouteRefs(fixture.HTTPRouteRef(customHTTPRouteName)),
				)
				Expect(envTest.Client.Create(ctx, llmisvc)).Should(Succeed())

				fixture.VerifyCustomHTTPRouteAuthPolicyExists(ctx, envTest.Client, testNs, LLMInferenceServiceName, customHTTPRouteName)
			})
		})

		Context("AuthPolicy Reconcile Tests", func() {
			Context("when Gateway AuthPolicy is modified or deleted", func() {
				It("should reconcile and restore Gateway AuthPolicy when modified", func(ctx SpecContext) {
					fixture.CreateBasicLLMInferenceService(ctx, envTest.Client, testNs, LLMInferenceServiceName, nil)

					gatewayAuthPolicy := fixture.WaitForGatewayAuthPolicy(ctx, envTest.Client, constants.DefaultGatewayNamespace, constants.DefaultGatewayName)
					originalTargetRef := gatewayAuthPolicy.Spec.TargetRef

					gatewayAuthPolicy.Spec.TargetRef.Name = "modified-gateway"
					Expect(envTest.Client.Update(ctx, gatewayAuthPolicy)).Should(Succeed())

					fixture.VerifyGatewayAuthPolicyRestored(ctx, envTest.Client, constants.DefaultGatewayNamespace, constants.DefaultGatewayName, originalTargetRef.Name)
				})

				It("should recreate Gateway AuthPolicy when deleted", func(ctx SpecContext) {
					fixture.CreateBasicLLMInferenceService(ctx, envTest.Client, testNs, LLMInferenceServiceName, nil)

					gatewayAuthPolicy := fixture.WaitForGatewayAuthPolicy(ctx, envTest.Client, constants.DefaultGatewayNamespace, constants.DefaultGatewayName)
					Expect(envTest.Client.Delete(ctx, gatewayAuthPolicy)).Should(Succeed())

					fixture.VerifyGatewayAuthPolicyRecreated(ctx, envTest.Client, constants.DefaultGatewayNamespace, constants.DefaultGatewayName)
				})

				It("should restore custom Gateway AuthPolicy when modified", func(ctx SpecContext) {
					customGatewayNamespace := testNs

					customGateway := fixture.Gateway(customGatewayName,
						fixture.InNamespace[*gatewayapiv1.Gateway](customGatewayNamespace),
						fixture.WithClassName("openshift-default"),
						fixture.WithListener(gatewayapiv1.HTTPProtocolType),
					)
					Expect(envTest.Client.Create(ctx, customGateway)).Should(Succeed())

					llmisvc := fixture.LLMInferenceService(LLMInferenceServiceName,
						fixture.InNamespace[*kservev1alpha1.LLMInferenceService](testNs),
						fixture.WithGatewayRefs(kservev1alpha1.UntypedObjectReference{
							Name:      gatewayapiv1.ObjectName(customGatewayName),
							Namespace: gatewayapiv1.Namespace(customGatewayNamespace),
						}),
					)
					Expect(envTest.Client.Create(ctx, llmisvc)).Should(Succeed())

					gatewayAuthPolicy := fixture.WaitForGatewayAuthPolicy(ctx, envTest.Client, customGatewayNamespace, customGatewayName)

					originalTargetRef := gatewayAuthPolicy.Spec.TargetRef

					gatewayAuthPolicy.Spec.TargetRef.Name = "modified-custom-gateway"
					Expect(envTest.Client.Update(ctx, gatewayAuthPolicy)).Should(Succeed())

					fixture.VerifyGatewayAuthPolicyRestored(ctx, envTest.Client, customGatewayNamespace, customGatewayName, originalTargetRef.Name)
				})

				It("should recreate custom Gateway AuthPolicy when deleted", func(ctx SpecContext) {
					customGatewayNamespace := testNs

					customGateway := fixture.Gateway(customGatewayName,
						fixture.InNamespace[*gatewayapiv1.Gateway](customGatewayNamespace),
						fixture.WithClassName("openshift-default"),
						fixture.WithListener(gatewayapiv1.HTTPProtocolType),
					)
					Expect(envTest.Client.Create(ctx, customGateway)).Should(Succeed())

					llmisvc := fixture.LLMInferenceService(LLMInferenceServiceName,
						fixture.InNamespace[*kservev1alpha1.LLMInferenceService](testNs),
						fixture.WithGatewayRefs(kservev1alpha1.UntypedObjectReference{
							Name:      gatewayapiv1.ObjectName(customGatewayName),
							Namespace: gatewayapiv1.Namespace(customGatewayNamespace),
						}),
					)
					Expect(envTest.Client.Create(ctx, llmisvc)).Should(Succeed())

					gatewayAuthPolicy := fixture.WaitForGatewayAuthPolicy(ctx, envTest.Client, customGatewayNamespace, customGatewayName)

					Expect(envTest.Client.Delete(ctx, gatewayAuthPolicy)).Should(Succeed())

					fixture.VerifyGatewayAuthPolicyRecreated(ctx, envTest.Client, customGatewayNamespace, customGatewayName)
				})
			})

			Context("when HTTPRoute AuthPolicy is modified or deleted", func() {
				It("should reconcile and restore HTTPRoute AuthPolicy when modified", func(ctx SpecContext) {
					enableAuth := false
					fixture.CreateBasicLLMInferenceService(ctx, envTest.Client, testNs, LLMInferenceServiceName, &enableAuth)

					fixture.CreateHTTPRouteForLLMService(ctx, envTest.Client, testNs, LLMInferenceServiceName)

					httpRouteAuthPolicy := fixture.WaitForHTTPRouteAuthPolicy(ctx, envTest.Client, testNs, LLMInferenceServiceName)
					originalTargetRef := httpRouteAuthPolicy.Spec.TargetRef

					httpRouteAuthPolicy.Spec.TargetRef.Name = "modified-httproute"
					Expect(envTest.Client.Update(ctx, httpRouteAuthPolicy)).Should(Succeed())

					fixture.VerifyHTTPRouteAuthPolicyRestored(ctx, envTest.Client, testNs, LLMInferenceServiceName, originalTargetRef.Name)
				})

				It("should recreate HTTPRoute AuthPolicy when deleted", func(ctx SpecContext) {
					enableAuth := false
					fixture.CreateBasicLLMInferenceService(ctx, envTest.Client, testNs, LLMInferenceServiceName, &enableAuth)

					fixture.CreateHTTPRouteForLLMService(ctx, envTest.Client, testNs, LLMInferenceServiceName)

					httpRouteAuthPolicy := fixture.WaitForHTTPRouteAuthPolicy(ctx, envTest.Client, testNs, LLMInferenceServiceName)

					Expect(envTest.Client.Delete(ctx, httpRouteAuthPolicy)).Should(Succeed())

					fixture.VerifyHTTPRouteAuthPolicyRecreated(ctx, envTest.Client, testNs, LLMInferenceServiceName)
				})

				It("should restore custom HTTPRoute AuthPolicy when modified", func(ctx SpecContext) {
					customHTTPRoute := fixture.HTTPRoute(customHTTPRouteName,
						fixture.InNamespace[*gatewayapiv1.HTTPRoute](testNs),
					)
					Expect(envTest.Client.Create(ctx, customHTTPRoute)).Should(Succeed())

					llmisvc := fixture.LLMInferenceService(LLMInferenceServiceName,
						fixture.InNamespace[*kservev1alpha1.LLMInferenceService](testNs),
						fixture.WithEnableAuth(false),
						fixture.WithHTTPRouteRefs(fixture.HTTPRouteRef(customHTTPRouteName)),
					)
					Expect(envTest.Client.Create(ctx, llmisvc)).Should(Succeed())

					// Wait for custom HTTPRoute AuthPolicy
					fixture.VerifyCustomHTTPRouteAuthPolicyExists(ctx, envTest.Client, testNs, LLMInferenceServiceName, customHTTPRouteName)

					httpRouteAuthPolicy := fixture.WaitForCustomHTTPRouteAuthPolicy(ctx, envTest.Client, testNs, customHTTPRouteName)

					originalTargetRef := httpRouteAuthPolicy.Spec.TargetRef

					httpRouteAuthPolicy.Spec.TargetRef.Name = "modified-custom-httproute"
					Expect(envTest.Client.Update(ctx, httpRouteAuthPolicy)).Should(Succeed())

					fixture.VerifyCustomHTTPRouteAuthPolicyRestored(ctx, envTest.Client, testNs, customHTTPRouteName, originalTargetRef.Name)
				})

				It("should recreate custom HTTPRoute AuthPolicy when deleted", func(ctx SpecContext) {
					customHTTPRoute := fixture.HTTPRoute(customHTTPRouteName,
						fixture.InNamespace[*gatewayapiv1.HTTPRoute](testNs),
					)
					Expect(envTest.Client.Create(ctx, customHTTPRoute)).Should(Succeed())

					llmisvc := fixture.LLMInferenceService(LLMInferenceServiceName,
						fixture.InNamespace[*kservev1alpha1.LLMInferenceService](testNs),
						fixture.WithEnableAuth(false),
						fixture.WithHTTPRouteRefs(fixture.HTTPRouteRef(customHTTPRouteName)),
					)
					Expect(envTest.Client.Create(ctx, llmisvc)).Should(Succeed())

					httpRouteAuthPolicy := fixture.WaitForCustomHTTPRouteAuthPolicy(ctx, envTest.Client, testNs, customHTTPRouteName)

					Expect(envTest.Client.Delete(ctx, httpRouteAuthPolicy)).Should(Succeed())

					fixture.VerifyCustomHTTPRouteAuthPolicyExists(ctx, envTest.Client, testNs, LLMInferenceServiceName, customHTTPRouteName)
				})
			})
		})
	})

	Context("EnvoyFilter Reconcile Tests", func() {
		Context("when Gateway EnvoyFilter is created", func() {
			It("should create default Gateway EnvoyFilter when LLMInferenceService is created", func(ctx SpecContext) {
				fixture.CreateBasicLLMInferenceService(ctx, envTest.Client, testNs, LLMInferenceServiceName, nil)
				fixture.VerifyGatewayEnvoyFilterOwnerRef(ctx, envTest.Client, constants.DefaultGatewayNamespace, constants.DefaultGatewayName)
			})

			It("should create custom Gateway EnvoyFilter when LLMInferenceService has gateway reference", func(ctx SpecContext) {
				customGateway := fixture.Gateway(customGatewayName,
					fixture.InNamespace[*gatewayapiv1.Gateway](constants.DefaultGatewayNamespace),
					fixture.WithClassName("openshift-default"),
					fixture.WithListener(gatewayapiv1.HTTPProtocolType),
				)
				Expect(envTest.Client.Create(ctx, customGateway)).Should(Succeed())

				llmisvc := fixture.LLMInferenceService(LLMInferenceServiceName,
					fixture.InNamespace[*kservev1alpha1.LLMInferenceService](testNs),
					fixture.WithGatewayRefs(kservev1alpha1.UntypedObjectReference{
						Name:      gatewayapiv1.ObjectName(customGatewayName),
						Namespace: gatewayapiv1.Namespace(constants.DefaultGatewayNamespace),
					}),
				)
				Expect(envTest.Client.Create(ctx, llmisvc)).Should(Succeed())

				fixture.VerifyGatewayEnvoyFilterOwnerRef(ctx, envTest.Client, constants.DefaultGatewayNamespace, customGatewayName)
			})
		})

		Context("when Gateway EnvoyFilter is modified or deleted", func() {
			It("should reconcile and restore default Gateway EnvoyFilter when modified", func(ctx SpecContext) {
				fixture.CreateBasicLLMInferenceService(ctx, envTest.Client, testNs, LLMInferenceServiceName, nil)

				gatewayEnvoyFilter := fixture.WaitForGatewayEnvoyFilter(ctx, envTest.Client, constants.DefaultGatewayNamespace, constants.DefaultGatewayName)

				var originalTargetRefName string
				if len(gatewayEnvoyFilter.Spec.TargetRefs) > 0 {
					originalTargetRefName = gatewayEnvoyFilter.Spec.TargetRefs[0].Name
				}

				gatewayEnvoyFilter.Spec.TargetRefs[0].Name = "modified-gateway"
				Expect(envTest.Client.Update(ctx, gatewayEnvoyFilter)).Should(Succeed())

				fixture.VerifyGatewayEnvoyFilterRestored(ctx, envTest.Client, constants.DefaultGatewayNamespace, constants.DefaultGatewayName, originalTargetRefName)
			})

			It("should recreate default Gateway EnvoyFilter when deleted", func(ctx SpecContext) {
				fixture.CreateBasicLLMInferenceService(ctx, envTest.Client, testNs, LLMInferenceServiceName, nil)

				gatewayEnvoyFilter := fixture.WaitForGatewayEnvoyFilter(ctx, envTest.Client, constants.DefaultGatewayNamespace, constants.DefaultGatewayName)
				Expect(envTest.Client.Delete(ctx, gatewayEnvoyFilter)).Should(Succeed())

				fixture.VerifyGatewayEnvoyFilterRecreated(ctx, envTest.Client, constants.DefaultGatewayNamespace, constants.DefaultGatewayName)
			})

			It("should restore custom Gateway EnvoyFilter when modified", func(ctx SpecContext) {
				customGatewayNamespace := testNs

				customGateway := fixture.Gateway(customGatewayName,
					fixture.InNamespace[*gatewayapiv1.Gateway](customGatewayNamespace),
					fixture.WithClassName(GatewayClassName),
					fixture.WithListener(gatewayapiv1.HTTPProtocolType),
				)
				Expect(envTest.Client.Create(ctx, customGateway)).Should(Succeed())

				llmisvc := fixture.LLMInferenceService(LLMInferenceServiceName,
					fixture.InNamespace[*kservev1alpha1.LLMInferenceService](testNs),
					fixture.WithGatewayRefs(kservev1alpha1.UntypedObjectReference{
						Name:      gatewayapiv1.ObjectName(customGatewayName),
						Namespace: gatewayapiv1.Namespace(customGatewayNamespace),
					}),
				)
				Expect(envTest.Client.Create(ctx, llmisvc)).Should(Succeed())

				gatewayEnvoyFilter := fixture.WaitForGatewayEnvoyFilter(ctx, envTest.Client, customGatewayNamespace, customGatewayName)

				var originalTargetRefName string
				if len(gatewayEnvoyFilter.Spec.TargetRefs) > 0 {
					originalTargetRefName = gatewayEnvoyFilter.Spec.TargetRefs[0].Name
				}

				gatewayEnvoyFilter.Spec.TargetRefs[0].Name = "modified-custom-gateway"
				Expect(envTest.Client.Update(ctx, gatewayEnvoyFilter)).Should(Succeed())

				fixture.VerifyGatewayEnvoyFilterRestored(ctx, envTest.Client, customGatewayNamespace, customGatewayName, originalTargetRefName)
			})

			It("should recreate custom Gateway EnvoyFilter when deleted", func(ctx SpecContext) {
				customGateway := fixture.Gateway(customGatewayName,
					fixture.InNamespace[*gatewayapiv1.Gateway](constants.DefaultGatewayNamespace),
					fixture.WithClassName("openshift-default"),
					fixture.WithListener(gatewayapiv1.HTTPProtocolType),
				)
				Expect(envTest.Client.Create(ctx, customGateway)).Should(Succeed())

				llmisvc := fixture.LLMInferenceService(LLMInferenceServiceName,
					fixture.InNamespace[*kservev1alpha1.LLMInferenceService](testNs),
					fixture.WithGatewayRefs(kservev1alpha1.UntypedObjectReference{
						Name:      gatewayapiv1.ObjectName(customGatewayName),
						Namespace: gatewayapiv1.Namespace(constants.DefaultGatewayNamespace),
					}),
				)
				Expect(envTest.Client.Create(ctx, llmisvc)).Should(Succeed())

				gatewayEnvoyFilter := fixture.WaitForGatewayEnvoyFilter(ctx, envTest.Client, constants.DefaultGatewayNamespace, customGatewayName)

				Expect(envTest.Client.Delete(ctx, gatewayEnvoyFilter)).Should(Succeed())

				fixture.VerifyGatewayEnvoyFilterRecreated(ctx, envTest.Client, constants.DefaultGatewayNamespace, customGatewayName)
			})
		})
	})
	
	Describe("Model-as-a-Service Integration", func() {
		Describe("Role Reconciler", func() {
			When("creating an LLMInferenceService", func() {
				It("should create a Role with correct specifications and proper owner references", func() {
					ctx := context.Background()

					// Create LLMInferenceService
					llmisvc := createLLMInferenceService(testNs, "test-llm-service", LLMServicePath1)
					Expect(envTest.Create(ctx, llmisvc)).Should(Succeed())

					// Wait for Role to be created and verify its specification
					role := waitForRole(testNs, utils.GetMaaSRoleName(llmisvc))
					verifyRoleSpecification(role, llmisvc)

					// Verify owner reference
					Expect(role.GetOwnerReferences()).To(HaveLen(1))
					ownerRef := role.GetOwnerReferences()[0]
					Expect(ownerRef.UID).To(Equal(llmisvc.UID))
					Expect(ownerRef.Kind).To(Equal("LLMInferenceService"))
					Expect(ownerRef.APIVersion).To(Equal("serving.kserve.io/v1alpha1"))
					Expect(*ownerRef.Controller).To(BeTrue())
				})
			})

			When("Role is manually modified", func() {
				It("should reconcile back to desired state", func() {
					ctx := context.Background()

					// Create LLMInferenceService with Role
					llmisvc := createLLMInferenceService(testNs, "test-llm-service", LLMServicePath1)
					Expect(envTest.Create(ctx, llmisvc)).Should(Succeed())

					roleName := utils.GetMaaSRoleName(llmisvc)
					role := waitForRole(testNs, roleName)

					// Manually modify Role (change verb from "post" to "get")
					role.Rules[0].Verbs = []string{"get"}
					Expect(envTest.Update(ctx, role)).Should(Succeed())

					// Verify the role is restored to the correct state
					Eventually(func() bool {
						updatedRole := &rbacv1.Role{}
						err := envTest.Get(ctx, types.NamespacedName{
							Name:      roleName,
							Namespace: testNs,
						}, updatedRole)
						if err != nil {
							return false
						}

						// Check if the role has been reconciled back to the correct state
						return len(updatedRole.Rules) > 0 &&
							len(updatedRole.Rules[0].Verbs) > 0 &&
							updatedRole.Rules[0].Verbs[0] == "post"
					}).Should(BeTrue())
				})
			})

			When("multiple LLMInferenceServices exist", func() {
				It("should create individual Roles with correct specifications", func() {
					ctx := context.Background()

					// Create multiple LLMInferenceServices in same namespace
					llmisvc1 := createLLMInferenceService(testNs, "test-llm-service-1", LLMServicePath1)
					llmisvc1.Name = "test-llm-service-1"
					Expect(envTest.Create(ctx, llmisvc1)).Should(Succeed())

					llmisvc2 := createLLMInferenceService(testNs, "test-llm-service-2", LLMServicePath2)
					llmisvc2.Name = "test-llm-service-2"
					Expect(envTest.Create(ctx, llmisvc2)).Should(Succeed())

					// Verify each has its own Role with correct resource names
					role1Name := utils.GetMaaSRoleName(llmisvc1)
					role2Name := utils.GetMaaSRoleName(llmisvc2)

					role1 := waitForRole(testNs, role1Name)
					verifyRoleSpecification(role1, llmisvc1)

					role2 := waitForRole(testNs, role2Name)
					verifyRoleSpecification(role2, llmisvc2)
				})
			})
		})

		Describe("RoleBinding Reconciler", func() {
			When("creating an LLMInferenceService", func() {
				It("should create a RoleBinding with correct MaaS tier specifications and proper owner references, and should reference the Role created by LLMRoleReconciler", func(ctx SpecContext) {
					// Create LLMInferenceService
					llmisvc := createLLMInferenceService(testNs, "test-llm-service", LLMServicePath1)
					Expect(envTest.Create(ctx, llmisvc)).Should(Succeed())

					// Wait for RoleBinding to be created and verify its specification
					roleBindingName := utils.GetMaaSRoleBindingName(llmisvc)
					roleBinding := waitForRoleBinding(testNs, roleBindingName)
					verifyRoleBindingSpecification(Default, roleBinding, llmisvc)

					// Verify owner reference
					Expect(roleBinding.GetOwnerReferences()).To(HaveLen(1))
					ownerRef := roleBinding.GetOwnerReferences()[0]
					Expect(ownerRef.UID).To(Equal(llmisvc.UID))
					Expect(ownerRef.Kind).To(Equal("LLMInferenceService"))
					Expect(ownerRef.APIVersion).To(Equal("serving.kserve.io/v1alpha1"))
					Expect(*ownerRef.Controller).To(BeTrue())
				})
			})

			When("RoleBinding is manually modified", func() {
				It("should reconcile back to desired state and restore correct MaaS tier subjects when changed", func(ctx SpecContext) {
					// Create LLMInferenceService with RoleBinding
					llmisvc := createLLMInferenceService(testNs, "test-llm-service", LLMServicePath1)
					Expect(envTest.Create(ctx, llmisvc)).Should(Succeed())

					roleBindingName := utils.GetMaaSRoleBindingName(llmisvc)
					roleBinding := waitForRoleBinding(testNs, roleBindingName)

					// Manually modify RoleBinding subjects (remove a MaaS tier)
					roleBinding.Subjects = []rbacv1.Subject{
						{
							Kind:      "Group",
							APIGroup:  "rbac.authorization.k8s.io",
							Name:      "system:serviceaccounts:maas-default-gateway-tier-free",
							Namespace: "",
						},
					}
					Expect(envTest.Update(ctx, roleBinding)).Should(Succeed())

					// Verify RoleBinding is restored to include all three tiers
					Eventually(func(g Gomega) {
						updatedRoleBinding := &rbacv1.RoleBinding{}
						err := envTest.Get(ctx, types.NamespacedName{
							Name:      roleBindingName,
							Namespace: testNs,
						}, updatedRoleBinding)
						g.Expect(err).ToNot(HaveOccurred())

						// Check RoleBinding is restored
						verifyRoleBindingSpecification(g, updatedRoleBinding, llmisvc)
					}).Should(Succeed())
				})
			})

			When("multiple LLMInferenceServices exist", func() {
				It("should create individual RoleBindings with correct Role references", func(ctx SpecContext) {
					// Create multiple LLMInferenceServices in the same namespace
					llmisvc1 := createLLMInferenceService(testNs, "test-llm-service-1", LLMServicePath1)
					llmisvc1.Name = "test-llm-service-1"
					Expect(envTest.Create(ctx, llmisvc1)).Should(Succeed())

					llmisvc2 := createLLMInferenceService(testNs, "test-llm-service-2", LLMServicePath2)
					llmisvc2.Name = "test-llm-service-2"
					Expect(envTest.Create(ctx, llmisvc2)).Should(Succeed())

					// Verify each has its own RoleBinding with correct Role reference
					roleBinding1Name := utils.GetMaaSRoleBindingName(llmisvc1)
					roleBinding2Name := utils.GetMaaSRoleBindingName(llmisvc2)

					roleBinding1 := waitForRoleBinding(testNs, roleBinding1Name)
					verifyRoleBindingSpecification(Default, roleBinding1, llmisvc1)

					roleBinding2 := waitForRoleBinding(testNs, roleBinding2Name)
					verifyRoleBinding2Specification(Default, roleBinding2, llmisvc2)
				})
			})
		})
	})
	
})

// Helper Functions

// createLLMInferenceService creates an LLMInferenceService from a testdata file
func createLLMInferenceService(namespace, name, path string) *kservev1alpha1.LLMInferenceService {
	llmisvc := &kservev1alpha1.LLMInferenceService{}
	err := testutils.ConvertToStructuredResource(path, llmisvc)
	Expect(err).NotTo(HaveOccurred())
	llmisvc.SetNamespace(namespace)
	if name != "" {
		llmisvc.Name = name
	}
	return llmisvc
}

func waitForRole(namespace, name string) *rbacv1.Role {
	GinkgoHelper()

	role := &rbacv1.Role{}
	Eventually(func() error {
		return envTest.Get(context.Background(), types.NamespacedName{
			Name:      name,
			Namespace: namespace,
		}, role)
	}).Should(Succeed())

	return role
}

// verifyRoleSpecification validates Role matches expected template
func verifyRoleSpecification(role *rbacv1.Role, llmIsvc *kservev1alpha1.LLMInferenceService) {
	GinkgoHelper()

	// Verify Role name
	expectedName := utils.GetMaaSRoleName(llmIsvc)
	Expect(role.GetName()).To(Equal(expectedName))

	// Verify Role labels
	Expect(role.GetLabels()).To(HaveKeyWithValue("app.kubernetes.io/managed-by", "odh-model-controller"))

	// Verify Role rules
	Expect(role.Rules).To(HaveLen(1))

	rule := role.Rules[0]

	// Verify API Groups, Resources, Resource Names and Verbs
	Expect(rule.APIGroups).To(HaveExactElements("serving.kserve.io"))
	Expect(rule.Resources).To(HaveExactElements("llminferenceservices"))
	Expect(rule.ResourceNames).To(HaveExactElements(llmIsvc.Name))
	Expect(rule.Verbs).To(HaveExactElements("post"))
}

// waitForRoleBinding waits for RoleBinding to be created and returns it
func waitForRoleBinding(namespace, name string) *rbacv1.RoleBinding {
	GinkgoHelper()

	roleBinding := &rbacv1.RoleBinding{}
	Eventually(func() error {
		return envTest.Get(context.Background(), types.NamespacedName{
			Name:      name,
			Namespace: namespace,
		}, roleBinding)
	}).Should(Succeed())

	return roleBinding
}

// verifyRoleBindingSpecification validates RoleBinding matches MaaS template
func verifyRoleBindingSpecification(g Gomega, roleBinding *rbacv1.RoleBinding, llmIsvc *kservev1alpha1.LLMInferenceService) {
	GinkgoHelper()

	verifyRoleBindingMetadata(g, roleBinding, llmIsvc)
	verifyMaaSTierSubjects(g, roleBinding.Subjects)
	verifyRoleBindingRoleRef(g, roleBinding, llmIsvc)
}

// verifyRoleBindingSpecification validates RoleBinding matches MaaS template for the fixture "2"
func verifyRoleBinding2Specification(g Gomega, roleBinding *rbacv1.RoleBinding, llmIsvc *kservev1alpha1.LLMInferenceService) {
	GinkgoHelper()

	verifyRoleBindingMetadata(g, roleBinding, llmIsvc)
	verifyMaaSTierSubjects2(g, roleBinding.Subjects)
	verifyRoleBindingRoleRef(g, roleBinding, llmIsvc)
}

func verifyRoleBindingMetadata(g Gomega, roleBinding *rbacv1.RoleBinding, llmIsvc *kservev1alpha1.LLMInferenceService) {
	GinkgoHelper()

	expectedName := utils.GetMaaSRoleBindingName(llmIsvc)
	g.Expect(roleBinding.GetName()).To(Equal(expectedName))
	g.Expect(roleBinding.GetLabels()).To(HaveKeyWithValue("app.kubernetes.io/managed-by", "odh-model-controller"))
}

func verifyRoleBindingRoleRef(g Gomega, roleBinding *rbacv1.RoleBinding, llmIsvc *kservev1alpha1.LLMInferenceService) {
	GinkgoHelper()

	expectedRoleName := utils.GetMaaSRoleName(llmIsvc)
	g.Expect(roleBinding.RoleRef.Name).To(Equal(expectedRoleName))
	g.Expect(roleBinding.RoleRef.Kind).To(Equal("Role"))
	g.Expect(roleBinding.RoleRef.APIGroup).To(Equal("rbac.authorization.k8s.io"))
}

// verifyMaaSTierSubjects validates all three MaaS tier groups are present
func verifyMaaSTierSubjects(g Gomega, subjects []rbacv1.Subject) {
	GinkgoHelper()

	expectedSubjects := []rbacv1.Subject{
		{
			Kind:      "Group",
			APIGroup:  "rbac.authorization.k8s.io",
			Name:      "system:serviceaccounts:maas-default-gateway-tier-free",
			Namespace: "",
		},
		{
			Kind:      "Group",
			APIGroup:  "rbac.authorization.k8s.io",
			Name:      "system:serviceaccounts:maas-default-gateway-tier-premium",
			Namespace: "",
		},
		{
			Kind:      "Group",
			APIGroup:  "rbac.authorization.k8s.io",
			Name:      "system:serviceaccounts:maas-default-gateway-tier-enterprise",
			Namespace: "",
		},
	}

	g.Expect(subjects).To(HaveExactElements(expectedSubjects))
}

// verifyMaaSTierSubjects2 validates all three MaaS tier groups are present for the fixture "2"
func verifyMaaSTierSubjects2(g Gomega, subjects []rbacv1.Subject) {
	GinkgoHelper()

	expectedSubjects := []rbacv1.Subject{
		{
			Kind:      "Group",
			APIGroup:  "rbac.authorization.k8s.io",
			Name:      "system:serviceaccounts:maas-default-gateway-tier-free",
			Namespace: "",
		},
		{
			Kind:      "Group",
			APIGroup:  "rbac.authorization.k8s.io",
			Name:      "system:serviceaccounts:maas-default-gateway-tier-enterprise",
			Namespace: "",
		},
	}

	g.Expect(subjects).To(HaveExactElements(expectedSubjects))
}
