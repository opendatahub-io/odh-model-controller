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

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kuadrantv1 "github.com/kuadrant/kuadrant-operator/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/serving/llm/fixture"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/serving/reconcilers"
	pkgtest "github.com/opendatahub-io/odh-model-controller/internal/controller/testing"
	controllerutils "github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
	testutils "github.com/opendatahub-io/odh-model-controller/test/utils"
)

const (
	LLMInferenceServiceName = "test-llmisvc"
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

	AfterEach(func(ctx SpecContext) {
		// Clean up LLMInferenceServices to prevent cross-test interference
		// from watches (Gateway, ConfigMap) that list all services
		llmList := &kservev1alpha1.LLMInferenceServiceList{}
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
					fixture.InNamespace[*kservev1alpha1.LLMInferenceService](testNs),
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
					fixture.InNamespace[*kservev1alpha1.LLMInferenceService](testNs),
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
					fixture.InNamespace[*kservev1alpha1.LLMInferenceService](testNs),
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

	Describe("Model-as-a-Service Integration", func() {
		Describe("Role Reconciler", func() {
			When("creating an LLMInferenceService", func() {
				It("should create a Role with correct specifications and proper owner references", func(ctx SpecContext) {
					llmisvc := createLLMInferenceService(testNs, "test-llm-service", LLMServicePath1)
					Expect(envTest.Create(ctx, llmisvc)).Should(Succeed())

					role := waitForRole(testNs, controllerutils.GetMaaSRoleName(llmisvc))
					verifyRoleSpecification(role, llmisvc)
					verifyLLMISvcOwnerRef(llmisvc, role.GetOwnerReferences())
				})
			})

			When("Role is manually modified", func() {
				It("should reconcile back to desired state", func(ctx SpecContext) {
					llmisvc := createLLMInferenceService(testNs, "test-llm-service", LLMServicePath1)
					Expect(envTest.Create(ctx, llmisvc)).Should(Succeed())

					roleName := controllerutils.GetMaaSRoleName(llmisvc)
					role := waitForRole(testNs, roleName)

					role.Rules[0].Verbs = []string{"get"}
					Expect(envTest.Update(ctx, role)).Should(Succeed())

					Eventually(func() bool {
						updatedRole := &rbacv1.Role{}
						err := envTest.Get(ctx, types.NamespacedName{
							Name:      roleName,
							Namespace: testNs,
						}, updatedRole)
						if err != nil {
							return false
						}
						return len(updatedRole.Rules) > 0 &&
							len(updatedRole.Rules[0].Verbs) > 0 &&
							updatedRole.Rules[0].Verbs[0] == "post"
					}).WithContext(ctx).Should(BeTrue())
				})
			})

			When("multiple LLMInferenceServices exist", func() {
				It("should create individual Roles with correct specifications", func(ctx SpecContext) {
					llmisvc1 := createLLMInferenceService(testNs, "test-llm-service-1", LLMServicePath1)
					Expect(envTest.Create(ctx, llmisvc1)).Should(Succeed())

					llmisvc2 := createLLMInferenceService(testNs, "test-llm-service-2", LLMServicePath2)
					Expect(envTest.Create(ctx, llmisvc2)).Should(Succeed())

					verifyRoleSpecification(waitForRole(testNs, controllerutils.GetMaaSRoleName(llmisvc1)), llmisvc1)
					verifyRoleSpecification(waitForRole(testNs, controllerutils.GetMaaSRoleName(llmisvc2)), llmisvc2)
				})
			})

			When("tier annotation is removed", func() {
				It("should delete existing managed Role", func(ctx SpecContext) {
					llmisvc := createLLMInferenceService(testNs, "test-llm-service", LLMServicePath1)
					Expect(envTest.Create(ctx, llmisvc)).Should(Succeed())

					roleName := controllerutils.GetMaaSRoleName(llmisvc)
					waitForRole(testNs, roleName)

					delete(llmisvc.Annotations, reconcilers.TierAnnotationKey)
					Expect(envTest.Update(ctx, llmisvc)).Should(Succeed())

					verifyResourceDeleted(ctx, testNs, roleName, &rbacv1.Role{})
				})

				It("should not delete existing unmanaged Role", func(ctx SpecContext) {
					llmisvc := createLLMInferenceService(testNs, "test-llm-service", LLMServicePath1)

					unmanagedRole := &rbacv1.Role{
						ObjectMeta: metav1.ObjectMeta{
							Name:      controllerutils.GetMaaSRoleName(llmisvc),
							Namespace: testNs,
						},
						Rules: []rbacv1.PolicyRule{
							{
								APIGroups:     []string{"serving.kserve.io"},
								Resources:     []string{"llminferenceservices"},
								ResourceNames: []string{llmisvc.Name},
								Verbs:         []string{"post"},
							},
						},
					}

					Expect(envTest.Create(ctx, unmanagedRole)).Should(Succeed())
					Expect(envTest.Create(ctx, llmisvc)).Should(Succeed())

					delete(llmisvc.Annotations, reconcilers.TierAnnotationKey)
					Expect(envTest.Update(ctx, llmisvc)).Should(Succeed())

					verifyResourcePersists(ctx, testNs, unmanagedRole.Name, &rbacv1.Role{})
				})
			})
		})

		Describe("RoleBinding Reconciler", func() {
			When("creating an LLMInferenceService", func() {
				It("should create a RoleBinding with correct MaaS tier specifications and proper owner references", func(ctx SpecContext) {
					llmisvc := createLLMInferenceService(testNs, "test-llm-service", LLMServicePath1)
					Expect(envTest.Create(ctx, llmisvc)).Should(Succeed())

					roleBindingName := controllerutils.GetMaaSRoleBindingName(llmisvc)
					roleBinding := waitForRoleBinding(testNs, roleBindingName)
					verifyRoleBindingSpecification(Default, roleBinding, llmisvc)
					verifyLLMISvcOwnerRef(llmisvc, roleBinding.GetOwnerReferences())
				})
			})

			When("RoleBinding is manually modified", func() {
				It("should reconcile back to desired state and restore correct MaaS tier subjects when changed", func(ctx SpecContext) {
					llmisvc := createLLMInferenceService(testNs, "test-llm-service", LLMServicePath1)
					Expect(envTest.Create(ctx, llmisvc)).Should(Succeed())

					roleBindingName := controllerutils.GetMaaSRoleBindingName(llmisvc)
					roleBinding := waitForRoleBinding(testNs, roleBindingName)

					roleBinding.Subjects = []rbacv1.Subject{
						{
							Kind:     "Group",
							APIGroup: "rbac.authorization.k8s.io",
							Name:     "system:serviceaccounts:maas-default-gateway-tier-free",
						},
					}
					Expect(envTest.Update(ctx, roleBinding)).Should(Succeed())

					Eventually(func(g Gomega) {
						updatedRoleBinding := &rbacv1.RoleBinding{}
						err := envTest.Get(ctx, types.NamespacedName{
							Name:      roleBindingName,
							Namespace: testNs,
						}, updatedRoleBinding)
						g.Expect(err).ToNot(HaveOccurred())
						verifyRoleBindingSpecification(g, updatedRoleBinding, llmisvc)
					}).Should(Succeed())
				})
			})

			When("multiple LLMInferenceServices exist", func() {
				It("should create individual RoleBindings with correct Role references", func(ctx SpecContext) {
					llmisvc1 := createLLMInferenceService(testNs, "test-llm-service-1", LLMServicePath1)
					Expect(envTest.Create(ctx, llmisvc1)).Should(Succeed())

					llmisvc2 := createLLMInferenceService(testNs, "test-llm-service-2", LLMServicePath2)
					Expect(envTest.Create(ctx, llmisvc2)).Should(Succeed())

					roleBinding1 := waitForRoleBinding(testNs, controllerutils.GetMaaSRoleBindingName(llmisvc1))
					verifyRoleBindingSpecification(Default, roleBinding1, llmisvc1)

					roleBinding2 := waitForRoleBinding(testNs, controllerutils.GetMaaSRoleBindingName(llmisvc2))
					verifyRoleBinding2Specification(Default, roleBinding2, llmisvc2)
				})
			})

			When("tier annotation is removed", func() {
				It("should delete existing managed RoleBinding", func(ctx SpecContext) {
					llmisvc := createLLMInferenceService(testNs, "test-llm-service", LLMServicePath1)
					Expect(envTest.Create(ctx, llmisvc)).Should(Succeed())

					roleBindingName := controllerutils.GetMaaSRoleBindingName(llmisvc)
					waitForRoleBinding(testNs, roleBindingName)

					delete(llmisvc.Annotations, reconcilers.TierAnnotationKey)
					Expect(envTest.Update(ctx, llmisvc)).Should(Succeed())

					verifyResourceDeleted(ctx, testNs, roleBindingName, &rbacv1.RoleBinding{})
				})

				It("should not delete existing unmanaged RoleBinding", func(ctx SpecContext) {
					llmisvc := createLLMInferenceService(testNs, "test-llm-service", LLMServicePath1)

					unmanagedRoleBinding := &rbacv1.RoleBinding{
						ObjectMeta: metav1.ObjectMeta{
							Name:      controllerutils.GetMaaSRoleBindingName(llmisvc),
							Namespace: testNs,
						},
						Subjects: []rbacv1.Subject{
							{
								Kind:     "Group",
								APIGroup: "rbac.authorization.k8s.io",
								Name:     "system:serviceaccounts:maas-default-gateway-tier-free",
							},
						},
						RoleRef: rbacv1.RoleRef{
							Kind:     "Role",
							Name:     controllerutils.GetMaaSRoleName(llmisvc),
							APIGroup: "rbac.authorization.k8s.io",
						},
					}

					Expect(envTest.Create(ctx, unmanagedRoleBinding)).Should(Succeed())
					Expect(envTest.Create(ctx, llmisvc)).Should(Succeed())

					delete(llmisvc.Annotations, reconcilers.TierAnnotationKey)
					Expect(envTest.Update(ctx, llmisvc)).Should(Succeed())

					verifyResourcePersists(ctx, testNs, unmanagedRoleBinding.Name, &rbacv1.RoleBinding{})
				})
			})

			When("specific tiers are requested via annotation", func() {
				It("should create RoleBinding with only the requested tier subjects", func(ctx SpecContext) {
					llmisvc := fixture.LLMInferenceService("specific-tiers-test",
						fixture.InNamespace[*kservev1alpha1.LLMInferenceService](testNs),
						fixture.WithAnnotation(reconcilers.TierAnnotationKey, `["free", "premium"]`),
					)
					Expect(envTest.Create(ctx, llmisvc)).Should(Succeed())

					roleBinding := waitForRoleBinding(testNs, controllerutils.GetMaaSRoleBindingName(llmisvc))
					Expect(roleBinding.Subjects).To(HaveLen(2))
					Expect(roleBinding.Subjects).To(HaveExactElements([]rbacv1.Subject{
						{Kind: "Group", APIGroup: "rbac.authorization.k8s.io", Name: "system:serviceaccounts:maas-default-gateway-tier-free"},
						{Kind: "Group", APIGroup: "rbac.authorization.k8s.io", Name: "system:serviceaccounts:maas-default-gateway-tier-premium"},
					}))
				})
			})

			When("requested tier does not exist in ConfigMap", func() {
				It("should not create RoleBinding", func(ctx SpecContext) {
					llmisvc := fixture.LLMInferenceService("nonexistent-tier-test",
						fixture.InNamespace[*kservev1alpha1.LLMInferenceService](testNs),
						fixture.WithAnnotation(reconcilers.TierAnnotationKey, `["nonexistent-tier"]`),
					)
					Expect(envTest.Create(ctx, llmisvc)).Should(Succeed())

					verifyResourcePersistentlyAbsent(ctx, testNs, controllerutils.GetMaaSRoleBindingName(llmisvc), &rbacv1.RoleBinding{})
				})
			})
		})
	})
})

var _ = Describe("Tier ConfigMap Watch", func() {
	var testNs string

	BeforeEach(func(ctx SpecContext) {
		testNamespace := testutils.Namespaces.Create(ctx, envTest.Client)
		testNs = testNamespace.Name
	})

	Context("ConfigMap update triggers re-reconciliation", func() {
		It("should update RoleBinding subjects when ConfigMap tiers are modified", func(ctx SpecContext) {
			llmisvc := createLLMInferenceService(testNs, "tier-update-test", LLMServicePath1)
			Expect(envTest.Create(ctx, llmisvc)).Should(Succeed())

			roleBindingName := controllerutils.GetMaaSRoleBindingName(llmisvc)
			roleBinding := waitForRoleBinding(testNs, roleBindingName)
			Expect(roleBinding.Subjects).To(HaveLen(3))

			tierCM := &corev1.ConfigMap{}
			Expect(envTest.Get(ctx, types.NamespacedName{
				Name:      reconcilers.TierConfigMapName,
				Namespace: reconcilers.DefaultTenantNamespace,
			}, tierCM)).Should(Succeed())

			originalTiers := tierCM.Data["tiers"]
			DeferCleanup(func(ctx SpecContext) {
				tierCM.Data["tiers"] = originalTiers
				_ = envTest.Update(ctx, tierCM)
			})

			tierCM.Data["tiers"] = `
- name: free
  level: 1
- name: premium
  level: 10
- name: enterprise
  level: 20
- name: ultimate
  level: 30
`
			Expect(envTest.Update(ctx, tierCM)).Should(Succeed())

			Eventually(func(g Gomega, ctx context.Context) {
				updatedRB := &rbacv1.RoleBinding{}
				g.Expect(envTest.Get(ctx, types.NamespacedName{Name: roleBindingName, Namespace: testNs}, updatedRB)).To(Succeed())
				g.Expect(updatedRB.Subjects).To(HaveLen(4))
				subjectNames := make([]string, len(updatedRB.Subjects))
				for i, s := range updatedRB.Subjects {
					subjectNames[i] = s.Name
				}
				g.Expect(subjectNames).To(ContainElement("system:serviceaccounts:maas-default-gateway-tier-ultimate"))
			}).WithContext(ctx).Should(Succeed())
		})
	})

	Context("ConfigMap deletion behavior", func() {
		It("should delete RoleBinding when tier ConfigMap is deleted (fail-closed)", func(ctx SpecContext) {
			llmisvc := fixture.LLMInferenceService("delete-cm-test",
				fixture.InNamespace[*kservev1alpha1.LLMInferenceService](testNs),
				fixture.WithAnnotation(reconcilers.TierAnnotationKey, `["free"]`),
			)
			Expect(envTest.Create(ctx, llmisvc)).Should(Succeed())

			roleBindingName := controllerutils.GetMaaSRoleBindingName(llmisvc)
			roleBinding := waitForRoleBinding(testNs, roleBindingName)
			Expect(roleBinding.Subjects).To(HaveLen(1))

			tierCM := &corev1.ConfigMap{}
			Expect(envTest.Get(ctx, types.NamespacedName{
				Name:      reconcilers.TierConfigMapName,
				Namespace: reconcilers.DefaultTenantNamespace,
			}, tierCM)).Should(Succeed())

			originalData := tierCM.Data["tiers"]
			DeferCleanup(func(ctx SpecContext) {
				cm := &corev1.ConfigMap{}
				err := envTest.Get(ctx, types.NamespacedName{
					Name:      reconcilers.TierConfigMapName,
					Namespace: reconcilers.DefaultTenantNamespace,
				}, cm)
				if errors.IsNotFound(err) {
					cm = &corev1.ConfigMap{
						ObjectMeta: metav1.ObjectMeta{
							Name:      reconcilers.TierConfigMapName,
							Namespace: reconcilers.DefaultTenantNamespace,
						},
						Data: map[string]string{"tiers": originalData},
					}
					_ = envTest.Create(ctx, cm)
				}
			})

			Expect(envTest.Delete(ctx, tierCM)).Should(Succeed())

			verifyResourceDeleted(ctx, testNs, roleBindingName, &rbacv1.RoleBinding{})
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
			fixture.InNamespace[*kservev1alpha1.LLMInferenceServiceConfig](namespace),
		)
		config.Spec.Router = &kservev1alpha1.RouterSpec{
			Gateway: &kservev1alpha1.GatewaySpec{
				Refs: []kservev1alpha1.UntypedObjectReference{
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
				fixture.InNamespace[*kservev1alpha1.LLMInferenceService](testNs),
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
				fixture.InNamespace[*kservev1alpha1.LLMInferenceService](testNs),
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
				fixture.InNamespace[*kservev1alpha1.LLMInferenceService](testNs),
				fixture.WithBaseRefs(corev1.LocalObjectReference{Name: "shared-config"}),
			)
			Expect(envTest.Client.Create(ctx, llmisvc)).Should(Succeed())

			fixture.VerifyGatewayAuthPolicyOwnerRef(ctx, envTest.Client, testNs, serviceGatewayName)

			verifyResourcePersistentlyAbsent(ctx, systemNamespace, constants.GetAuthPolicyName(systemGatewayName), &kuadrantv1.AuthPolicy{})
		})

		It("should handle config not found when POD_NAMESPACE is not set", func(ctx SpecContext) {
			llmisvc := fixture.LLMInferenceService(LLMInferenceServiceName,
				fixture.InNamespace[*kservev1alpha1.LLMInferenceService](testNs),
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
				fixture.InNamespace[*kservev1alpha1.LLMInferenceServiceConfig](testNs),
			)
			Expect(envTest.Client.Create(ctx, config2)).Should(Succeed())

			llmisvc := fixture.LLMInferenceService(LLMInferenceServiceName,
				fixture.InNamespace[*kservev1alpha1.LLMInferenceService](testNs),
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
				fixture.InNamespace[*kservev1alpha1.LLMInferenceService](testNs),
				fixture.WithBaseRefs(corev1.LocalObjectReference{Name: "test-config"}),
				fixture.WithGatewayRefs(fixture.LLMGatewayRef(serviceGatewayName, testNs)),
			)
			Expect(envTest.Client.Create(ctx, llmisvc)).Should(Succeed())

			fixture.VerifyGatewayAuthPolicyOwnerRef(ctx, envTest.Client, testNs, serviceGatewayName)
			fixture.VerifyGatewayAuthPolicyOwnerRef(ctx, envTest.Client, testNs, configGatewayName)
		})

		It("should handle config fetch failures gracefully and continue with available configs", func(ctx SpecContext) {
			gatewayName := pkgtest.GenerateUniqueTestName("gateway")

			createTestGateway(ctx, gatewayName, testNs)
			createConfigWithGatewayRef(ctx, "config2", testNs, gatewayName, testNs)

			llmisvc := fixture.LLMInferenceService(LLMInferenceServiceName,
				fixture.InNamespace[*kservev1alpha1.LLMInferenceService](testNs),
				fixture.WithBaseRefs(
					corev1.LocalObjectReference{Name: "config1"}, // Will fail
					corev1.LocalObjectReference{Name: "config2"}, // Will succeed
				),
			)
			Expect(envTest.Client.Create(ctx, llmisvc)).Should(Succeed())

			fixture.VerifyGatewayAuthPolicyOwnerRef(ctx, envTest.Client, testNs, gatewayName)
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
				fixture.InNamespace[*kservev1alpha1.LLMInferenceService](testNs),
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

// createLLMInferenceService creates an LLMInferenceService from a testdata file.
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

// verifyLLMISvcOwnerRef validates that the owner references point to the given LLMInferenceService.
func verifyLLMISvcOwnerRef(llmisvc *kservev1alpha1.LLMInferenceService, ownerRefs []metav1.OwnerReference) {
	GinkgoHelper()
	Expect(ownerRefs).To(HaveLen(1))
	ownerRef := ownerRefs[0]
	Expect(ownerRef.UID).To(Equal(llmisvc.UID))
	Expect(ownerRef.Kind).To(Equal("LLMInferenceService"))
	Expect(ownerRef.APIVersion).To(Equal("serving.kserve.io/v1alpha1"))
	Expect(*ownerRef.Controller).To(BeTrue())
}

// verifyResourceDeleted waits for a resource to be deleted (Eventually + IsNotFound).
func verifyResourceDeleted[T client.Object](ctx context.Context, namespace, name string, obj T) {
	GinkgoHelper()
	Eventually(func() error {
		return envTest.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, obj)
	}).WithContext(ctx).Should(And(
		Not(Succeed()),
		WithTransform(errors.IsNotFound, BeTrue()),
	))
}

// verifyResourcePersists checks that a resource continues to exist (Consistently).
func verifyResourcePersists[T client.Object](ctx context.Context, namespace, name string, obj T) {
	GinkgoHelper()
	Consistently(func() error {
		return envTest.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, obj)
	}).WithContext(ctx).Should(Succeed())
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

func waitForRole(namespace, name string) *rbacv1.Role {
	GinkgoHelper()
	role := &rbacv1.Role{}
	Eventually(func() error {
		return envTest.Get(context.Background(), types.NamespacedName{Name: name, Namespace: namespace}, role)
	}).Should(Succeed())
	return role
}

func verifyRoleSpecification(role *rbacv1.Role, llmIsvc *kservev1alpha1.LLMInferenceService) {
	GinkgoHelper()
	Expect(role.GetName()).To(Equal(controllerutils.GetMaaSRoleName(llmIsvc)))
	Expect(role.GetLabels()).To(HaveKeyWithValue("app.kubernetes.io/managed-by", "odh-model-controller"))
	Expect(role.Rules).To(HaveLen(1))

	rule := role.Rules[0]
	Expect(rule.APIGroups).To(HaveExactElements("serving.kserve.io"))
	Expect(rule.Resources).To(HaveExactElements("llminferenceservices"))
	Expect(rule.ResourceNames).To(HaveExactElements(llmIsvc.Name))
	Expect(rule.Verbs).To(HaveExactElements("post"))
}

func waitForRoleBinding(namespace, name string) *rbacv1.RoleBinding {
	GinkgoHelper()
	roleBinding := &rbacv1.RoleBinding{}
	Eventually(func() error {
		return envTest.Get(context.Background(), types.NamespacedName{Name: name, Namespace: namespace}, roleBinding)
	}).Should(Succeed())
	return roleBinding
}

func verifyRoleBindingSpecification(g Gomega, roleBinding *rbacv1.RoleBinding, llmIsvc *kservev1alpha1.LLMInferenceService) {
	GinkgoHelper()
	verifyRoleBindingMetadata(g, roleBinding, llmIsvc)
	verifyMaaSTierSubjects(g, roleBinding.Subjects, "free", "premium", "enterprise")
	verifyRoleBindingRoleRef(g, roleBinding, llmIsvc)
}

func verifyRoleBinding2Specification(g Gomega, roleBinding *rbacv1.RoleBinding, llmIsvc *kservev1alpha1.LLMInferenceService) {
	GinkgoHelper()
	verifyRoleBindingMetadata(g, roleBinding, llmIsvc)
	verifyMaaSTierSubjects(g, roleBinding.Subjects, "free", "enterprise")
	verifyRoleBindingRoleRef(g, roleBinding, llmIsvc)
}

func verifyRoleBindingMetadata(g Gomega, roleBinding *rbacv1.RoleBinding, llmIsvc *kservev1alpha1.LLMInferenceService) {
	GinkgoHelper()
	g.Expect(roleBinding.GetName()).To(Equal(controllerutils.GetMaaSRoleBindingName(llmIsvc)))
	g.Expect(roleBinding.GetLabels()).To(HaveKeyWithValue("app.kubernetes.io/managed-by", "odh-model-controller"))
}

func verifyRoleBindingRoleRef(g Gomega, roleBinding *rbacv1.RoleBinding, llmIsvc *kservev1alpha1.LLMInferenceService) {
	GinkgoHelper()
	g.Expect(roleBinding.RoleRef.Name).To(Equal(controllerutils.GetMaaSRoleName(llmIsvc)))
	g.Expect(roleBinding.RoleRef.Kind).To(Equal("Role"))
	g.Expect(roleBinding.RoleRef.APIGroup).To(Equal("rbac.authorization.k8s.io"))
}

// verifyMaaSTierSubjects validates that the subjects match the expected MaaS tier groups.
func verifyMaaSTierSubjects(g Gomega, subjects []rbacv1.Subject, tiers ...string) {
	GinkgoHelper()
	expected := make([]rbacv1.Subject, len(tiers))
	for i, tier := range tiers {
		expected[i] = rbacv1.Subject{
			Kind:     "Group",
			APIGroup: "rbac.authorization.k8s.io",
			Name:     "system:serviceaccounts:maas-default-gateway-tier-" + tier,
		}
	}
	g.Expect(subjects).To(HaveExactElements(expected))
}
