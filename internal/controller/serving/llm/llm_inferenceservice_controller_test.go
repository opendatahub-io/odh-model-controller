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
	istioclientv1alpha3 "istio.io/client-go/pkg/apis/networking/v1alpha3"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/serving/llm/fixture"
	pkgtest "github.com/opendatahub-io/odh-model-controller/internal/controller/testing"
	testutils "github.com/opendatahub-io/odh-model-controller/test/utils"
)

const (
	LLMInferenceServiceName = "test-llmisvc"
	CustomGatewayName       = "ready-gateway"
	CustomHTTPRouteName     = "custom-httproute"
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
				customGatewayNamespace := testNs

				customGateway := fixture.Gateway(customGatewayName,
					fixture.InNamespace[*gatewayapiv1.Gateway](customGatewayNamespace),
					fixture.WithClassName(GatewayClassName),
					fixture.WithListener(gatewayapiv1.HTTPProtocolType),
				)
				Expect(envTest.Client.Create(ctx, customGateway)).Should(Succeed())

				llmisvc := fixture.LLMInferenceService(LLMInferenceServiceName,
					fixture.InNamespace[*kservev1alpha2.LLMInferenceService](testNs),
					fixture.WithGatewayRefs(kservev1alpha2.UntypedObjectReference{
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
					fixture.InNamespace[*kservev1alpha2.LLMInferenceService](testNs),
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

					gatewayAuthPolicy := fixture.WaitForResource(ctx, envTest.Client, constants.DefaultGatewayNamespace, constants.GetGatewayAuthPolicyName(constants.DefaultGatewayName), &kuadrantv1.AuthPolicy{})
					originalTargetRef := gatewayAuthPolicy.Spec.TargetRef

					gatewayAuthPolicy.Spec.TargetRef.Name = "modified-gateway"
					Expect(envTest.Client.Update(ctx, gatewayAuthPolicy)).Should(Succeed())

					fixture.VerifyGatewayAuthPolicyRestored(ctx, envTest.Client, constants.DefaultGatewayNamespace, constants.DefaultGatewayName, originalTargetRef.Name)
				})

				It("should recreate Gateway AuthPolicy when deleted", func(ctx SpecContext) {
					fixture.CreateBasicLLMInferenceService(ctx, envTest.Client, testNs, LLMInferenceServiceName, nil)

					gatewayAuthPolicy := fixture.WaitForResource(ctx, envTest.Client, constants.DefaultGatewayNamespace, constants.GetGatewayAuthPolicyName(constants.DefaultGatewayName), &kuadrantv1.AuthPolicy{})
					Expect(envTest.Client.Delete(ctx, gatewayAuthPolicy)).Should(Succeed())

					fixture.VerifyResourceExists(ctx, envTest.Client, constants.DefaultGatewayNamespace, constants.GetGatewayAuthPolicyName(constants.DefaultGatewayName), &kuadrantv1.AuthPolicy{})
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
						fixture.InNamespace[*kservev1alpha2.LLMInferenceService](testNs),
						fixture.WithGatewayRefs(kservev1alpha2.UntypedObjectReference{
							Name:      gatewayapiv1.ObjectName(customGatewayName),
							Namespace: gatewayapiv1.Namespace(customGatewayNamespace),
						}),
					)
					Expect(envTest.Client.Create(ctx, llmisvc)).Should(Succeed())

					gatewayAuthPolicy := fixture.WaitForResource(ctx, envTest.Client, customGatewayNamespace, constants.GetGatewayAuthPolicyName(customGatewayName), &kuadrantv1.AuthPolicy{})

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
						fixture.InNamespace[*kservev1alpha2.LLMInferenceService](testNs),
						fixture.WithGatewayRefs(kservev1alpha2.UntypedObjectReference{
							Name:      gatewayapiv1.ObjectName(customGatewayName),
							Namespace: gatewayapiv1.Namespace(customGatewayNamespace),
						}),
					)
					Expect(envTest.Client.Create(ctx, llmisvc)).Should(Succeed())

					gatewayAuthPolicy := fixture.WaitForResource(ctx, envTest.Client, customGatewayNamespace, constants.GetGatewayAuthPolicyName(customGatewayName), &kuadrantv1.AuthPolicy{})

					Expect(envTest.Client.Delete(ctx, gatewayAuthPolicy)).Should(Succeed())

					fixture.VerifyResourceExists(ctx, envTest.Client, customGatewayNamespace, constants.GetGatewayAuthPolicyName(customGatewayName), &kuadrantv1.AuthPolicy{})
				})
			})

			Context("when HTTPRoute AuthPolicy is modified or deleted", func() {
				It("should reconcile and restore HTTPRoute AuthPolicy when modified", func(ctx SpecContext) {
					enableAuth := false
					fixture.CreateBasicLLMInferenceService(ctx, envTest.Client, testNs, LLMInferenceServiceName, &enableAuth)

					fixture.CreateHTTPRouteForLLMService(ctx, envTest.Client, testNs, LLMInferenceServiceName)

					httpRouteName := constants.GetHTTPRouteName(LLMInferenceServiceName)
					httpRouteAuthPolicy := fixture.WaitForResource(ctx, envTest.Client, testNs, constants.GetHTTPRouteAuthPolicyName(httpRouteName), &kuadrantv1.AuthPolicy{})
					originalTargetRef := httpRouteAuthPolicy.Spec.TargetRef

					httpRouteAuthPolicy.Spec.TargetRef.Name = "modified-httproute"
					Expect(envTest.Client.Update(ctx, httpRouteAuthPolicy)).Should(Succeed())

					fixture.VerifyHTTPRouteAuthPolicyRestored(ctx, envTest.Client, testNs, LLMInferenceServiceName, originalTargetRef.Name)
				})

				It("should recreate HTTPRoute AuthPolicy when deleted", func(ctx SpecContext) {
					enableAuth := false
					fixture.CreateBasicLLMInferenceService(ctx, envTest.Client, testNs, LLMInferenceServiceName, &enableAuth)

					fixture.CreateHTTPRouteForLLMService(ctx, envTest.Client, testNs, LLMInferenceServiceName)

					httpRouteName := constants.GetHTTPRouteName(LLMInferenceServiceName)
					httpRouteAuthPolicy := fixture.WaitForResource(ctx, envTest.Client, testNs, constants.GetHTTPRouteAuthPolicyName(httpRouteName), &kuadrantv1.AuthPolicy{})

					Expect(envTest.Client.Delete(ctx, httpRouteAuthPolicy)).Should(Succeed())

					fixture.VerifyResourceExists(ctx, envTest.Client, testNs, constants.GetHTTPRouteAuthPolicyName(httpRouteName), &kuadrantv1.AuthPolicy{})
				})

				It("should restore custom HTTPRoute AuthPolicy when modified", func(ctx SpecContext) {
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
						fixture.InNamespace[*kservev1alpha2.LLMInferenceService](testNs),
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
					fixture.InNamespace[*kservev1alpha2.LLMInferenceService](testNs),
					fixture.WithGatewayRefs(kservev1alpha2.UntypedObjectReference{
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

				gatewayEnvoyFilter := fixture.WaitForResource(ctx, envTest.Client, constants.DefaultGatewayNamespace, constants.GetGatewayEnvoyFilterName(constants.DefaultGatewayName), &istioclientv1alpha3.EnvoyFilter{})

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

				gatewayEnvoyFilter := fixture.WaitForResource(ctx, envTest.Client, constants.DefaultGatewayNamespace, constants.GetGatewayEnvoyFilterName(constants.DefaultGatewayName), &istioclientv1alpha3.EnvoyFilter{})
				Expect(envTest.Client.Delete(ctx, gatewayEnvoyFilter)).Should(Succeed())

				fixture.VerifyResourceExists(ctx, envTest.Client, constants.DefaultGatewayNamespace, constants.GetGatewayEnvoyFilterName(constants.DefaultGatewayName), &istioclientv1alpha3.EnvoyFilter{})
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
					fixture.InNamespace[*kservev1alpha2.LLMInferenceService](testNs),
					fixture.WithGatewayRefs(kservev1alpha2.UntypedObjectReference{
						Name:      gatewayapiv1.ObjectName(customGatewayName),
						Namespace: gatewayapiv1.Namespace(customGatewayNamespace),
					}),
				)
				Expect(envTest.Client.Create(ctx, llmisvc)).Should(Succeed())

				gatewayEnvoyFilter := fixture.WaitForResource(ctx, envTest.Client, customGatewayNamespace, constants.GetGatewayEnvoyFilterName(customGatewayName), &istioclientv1alpha3.EnvoyFilter{})

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
					fixture.InNamespace[*kservev1alpha2.LLMInferenceService](testNs),
					fixture.WithGatewayRefs(kservev1alpha2.UntypedObjectReference{
						Name:      gatewayapiv1.ObjectName(customGatewayName),
						Namespace: gatewayapiv1.Namespace(constants.DefaultGatewayNamespace),
					}),
				)
				Expect(envTest.Client.Create(ctx, llmisvc)).Should(Succeed())

				gatewayEnvoyFilter := fixture.WaitForResource(ctx, envTest.Client, constants.DefaultGatewayNamespace, constants.GetGatewayEnvoyFilterName(customGatewayName), &istioclientv1alpha3.EnvoyFilter{})

				Expect(envTest.Client.Delete(ctx, gatewayEnvoyFilter)).Should(Succeed())

				fixture.VerifyResourceExists(ctx, envTest.Client, constants.DefaultGatewayNamespace, constants.GetGatewayEnvoyFilterName(customGatewayName), &istioclientv1alpha3.EnvoyFilter{})
			})
		})

		Context("when Gateway has opendatahub.io/managed=false label", func() {
			It("should NOT create EnvoyFilter for unmanaged gateway without opt-in annotation", func(ctx SpecContext) {
				unmanagedGatewayName := pkgtest.GenerateUniqueTestName("unmanaged-gateway")

				unmanagedGateway := fixture.Gateway(unmanagedGatewayName,
					fixture.InNamespace[*gatewayapiv1.Gateway](testNs),
					fixture.WithClassName(GatewayClassName),
					fixture.WithListener(gatewayapiv1.HTTPProtocolType),
					fixture.WithUnmanagedLabel(),
				)
				Expect(envTest.Client.Create(ctx, unmanagedGateway)).Should(Succeed())

				llmisvc := fixture.LLMInferenceService(LLMInferenceServiceName,
					fixture.InNamespace[*kservev1alpha2.LLMInferenceService](testNs),
					fixture.WithGatewayRefs(kservev1alpha2.UntypedObjectReference{
						Name:      gatewayapiv1.ObjectName(unmanagedGatewayName),
						Namespace: gatewayapiv1.Namespace(testNs),
					}),
				)
				Expect(envTest.Client.Create(ctx, llmisvc)).Should(Succeed())

				fixture.VerifyGatewayEnvoyFilterNotExist(ctx, envTest.Client, testNs, unmanagedGatewayName)
			})

			It("should create EnvoyFilter for unmanaged gateway WITH authorino-tls-bootstrap=true annotation", func(ctx SpecContext) {
				optInGatewayName := pkgtest.GenerateUniqueTestName("optin-gateway")

				optInGateway := fixture.Gateway(optInGatewayName,
					fixture.InNamespace[*gatewayapiv1.Gateway](testNs),
					fixture.WithClassName(GatewayClassName),
					fixture.WithListener(gatewayapiv1.HTTPProtocolType),
					fixture.WithUnmanagedLabel(),
					fixture.WithAuthorinoTLSBootstrapAnnotation("true"),
				)
				Expect(envTest.Client.Create(ctx, optInGateway)).Should(Succeed())

				llmisvc := fixture.LLMInferenceService(LLMInferenceServiceName,
					fixture.InNamespace[*kservev1alpha2.LLMInferenceService](testNs),
					fixture.WithGatewayRefs(kservev1alpha2.UntypedObjectReference{
						Name:      gatewayapiv1.ObjectName(optInGatewayName),
						Namespace: gatewayapiv1.Namespace(testNs),
					}),
				)
				Expect(envTest.Client.Create(ctx, llmisvc)).Should(Succeed())

				fixture.VerifyGatewayEnvoyFilterOwnerRef(ctx, envTest.Client, testNs, optInGatewayName)
			})

			It("should delete EnvoyFilter when authorino-tls-bootstrap annotation is removed from unmanaged gateway", func(ctx SpecContext) {
				optInGatewayName := pkgtest.GenerateUniqueTestName("optin-gateway")

				optInGateway := fixture.Gateway(optInGatewayName,
					fixture.InNamespace[*gatewayapiv1.Gateway](testNs),
					fixture.WithClassName(GatewayClassName),
					fixture.WithListener(gatewayapiv1.HTTPProtocolType),
					fixture.WithUnmanagedLabel(),
					fixture.WithAuthorinoTLSBootstrapAnnotation("true"),
				)
				Expect(envTest.Client.Create(ctx, optInGateway)).Should(Succeed())

				llmisvc := fixture.LLMInferenceService(LLMInferenceServiceName,
					fixture.InNamespace[*kservev1alpha2.LLMInferenceService](testNs),
					fixture.WithGatewayRefs(kservev1alpha2.UntypedObjectReference{
						Name:      gatewayapiv1.ObjectName(optInGatewayName),
						Namespace: gatewayapiv1.Namespace(testNs),
					}),
				)
				Expect(envTest.Client.Create(ctx, llmisvc)).Should(Succeed())
				fixture.VerifyGatewayEnvoyFilterOwnerRef(ctx, envTest.Client, testNs, optInGatewayName)

				Eventually(func() error {
					gw := &gatewayapiv1.Gateway{}
					if err := envTest.Client.Get(ctx, types.NamespacedName{Name: optInGatewayName, Namespace: testNs}, gw); err != nil {
						return err
					}
					delete(gw.Annotations, constants.AuthorinoTLSBootstrapAnnotation)
					return envTest.Client.Update(ctx, gw)
				}).WithContext(ctx).Should(Succeed())

				fixture.VerifyGatewayEnvoyFilterNotExist(ctx, envTest.Client, testNs, optInGatewayName)
			})

			It("should delete EnvoyFilter when authorino-tls-bootstrap annotation is changed to false", func(ctx SpecContext) {
				optInGatewayName := pkgtest.GenerateUniqueTestName("optin-gateway")

				optInGateway := fixture.Gateway(optInGatewayName,
					fixture.InNamespace[*gatewayapiv1.Gateway](testNs),
					fixture.WithClassName(GatewayClassName),
					fixture.WithListener(gatewayapiv1.HTTPProtocolType),
					fixture.WithUnmanagedLabel(),
					fixture.WithAuthorinoTLSBootstrapAnnotation("true"),
				)
				Expect(envTest.Client.Create(ctx, optInGateway)).Should(Succeed())

				llmisvc := fixture.LLMInferenceService(LLMInferenceServiceName,
					fixture.InNamespace[*kservev1alpha2.LLMInferenceService](testNs),
					fixture.WithGatewayRefs(kservev1alpha2.UntypedObjectReference{
						Name:      gatewayapiv1.ObjectName(optInGatewayName),
						Namespace: gatewayapiv1.Namespace(testNs),
					}),
				)
				Expect(envTest.Client.Create(ctx, llmisvc)).Should(Succeed())
				fixture.VerifyGatewayEnvoyFilterOwnerRef(ctx, envTest.Client, testNs, optInGatewayName)

				Eventually(func() error {
					gw := &gatewayapiv1.Gateway{}
					if err := envTest.Client.Get(ctx, types.NamespacedName{Name: optInGatewayName, Namespace: testNs}, gw); err != nil {
						return err
					}
					gw.Annotations[constants.AuthorinoTLSBootstrapAnnotation] = "false"
					return envTest.Client.Update(ctx, gw)
				}).WithContext(ctx).Should(Succeed())

				fixture.VerifyGatewayEnvoyFilterNotExist(ctx, envTest.Client, testNs, optInGatewayName)
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

	Context("LLMInferenceServiceConfig retrieval", func() {
		It("should retrieve LLMInferenceServiceConfig from service namespace and create AuthPolicy for referenced gateway", func(ctx SpecContext) {
			customGatewayName := pkgtest.GenerateUniqueTestName("config-gateway")
			customGatewayNamespace := testNs

			// Create a custom gateway referenced by the config
			customGateway := fixture.Gateway(customGatewayName,
				fixture.InNamespace[*gatewayapiv1.Gateway](customGatewayNamespace),
				fixture.WithClassName(GatewayClassName),
				fixture.WithListener(gatewayapiv1.HTTPProtocolType),
			)
			Expect(envTest.Client.Create(ctx, customGateway)).Should(Succeed())

			// Create LLMInferenceServiceConfig with a gateway reference
			config := fixture.LLMInferenceServiceConfig("test-config",
				fixture.InNamespace[*kservev1alpha2.LLMInferenceServiceConfig](testNs),
			)
			config.Spec.Router = &kservev1alpha2.RouterSpec{
				Gateway: &kservev1alpha2.GatewaySpec{
					Refs: []kservev1alpha2.UntypedObjectReference{
						{
							Name:      gatewayapiv1.ObjectName(customGatewayName),
							Namespace: gatewayapiv1.Namespace(customGatewayNamespace),
						},
					},
				},
			}
			Expect(envTest.Client.Create(ctx, config)).Should(Succeed())

			// Create LLMInferenceService with BaseRefs
			llmisvc := fixture.LLMInferenceService(LLMInferenceServiceName,
				fixture.InNamespace[*kservev1alpha2.LLMInferenceService](testNs),
				fixture.WithBaseRefs(corev1.LocalObjectReference{Name: "test-config"}),
			)
			Expect(envTest.Client.Create(ctx, llmisvc)).Should(Succeed())

			// Verify AuthPolicy is created for the gateway from the config
			fixture.VerifyGatewayAuthPolicyOwnerRef(ctx, envTest.Client, customGatewayNamespace, customGatewayName)
		})

		It("should retrieve LLMInferenceServiceConfig from system namespace when not found in service namespace", func(ctx SpecContext) {
			customGatewayName := pkgtest.GenerateUniqueTestName("system-gateway")

			// Create system namespace
			systemNs := testutils.Namespaces.Create(ctx, envTest.Client)
			systemNamespace := systemNs.Name

			// Set POD_NAMESPACE env var
			_ = os.Setenv("POD_NAMESPACE", systemNamespace)
			defer func() {
				_ = os.Unsetenv("POD_NAMESPACE")
			}()

			// Create a custom gateway in the system namespace
			customGateway := fixture.Gateway(customGatewayName,
				fixture.InNamespace[*gatewayapiv1.Gateway](systemNamespace),
				fixture.WithClassName(GatewayClassName),
				fixture.WithListener(gatewayapiv1.HTTPProtocolType),
			)
			Expect(envTest.Client.Create(ctx, customGateway)).Should(Succeed())

			// Create LLMInferenceServiceConfig in system namespace
			config := fixture.LLMInferenceServiceConfig("system-config",
				fixture.InNamespace[*kservev1alpha2.LLMInferenceServiceConfig](systemNamespace),
			)
			config.Spec.Router = &kservev1alpha2.RouterSpec{
				Gateway: &kservev1alpha2.GatewaySpec{
					Refs: []kservev1alpha2.UntypedObjectReference{
						{
							Name:      gatewayapiv1.ObjectName(customGatewayName),
							Namespace: gatewayapiv1.Namespace(systemNamespace),
						},
					},
				},
			}
			Expect(envTest.Client.Create(ctx, config)).Should(Succeed())

			// Create LLMInferenceService with BaseRefs in a different namespace
			llmisvc := fixture.LLMInferenceService(LLMInferenceServiceName,
				fixture.InNamespace[*kservev1alpha2.LLMInferenceService](testNs),
				fixture.WithBaseRefs(corev1.LocalObjectReference{Name: "system-config"}),
			)
			Expect(envTest.Client.Create(ctx, llmisvc)).Should(Succeed())

			// Verify AuthPolicy is created for the gateway from the system config
			fixture.VerifyGatewayAuthPolicyOwnerRef(ctx, envTest.Client, systemNamespace, customGatewayName)
		})

		It("should prioritize service namespace config over system namespace config", func(ctx SpecContext) {
			serviceGatewayName := pkgtest.GenerateUniqueTestName("service-gateway")
			systemGatewayName := pkgtest.GenerateUniqueTestName("system-gateway")

			// Create system namespace
			systemNs := testutils.Namespaces.Create(ctx, envTest.Client)
			systemNamespace := systemNs.Name

			// Set POD_NAMESPACE env var
			_ = os.Setenv("POD_NAMESPACE", systemNamespace)
			defer func() {
				_ = os.Unsetenv("POD_NAMESPACE")
			}()

			// Create gateways in both namespaces
			serviceGateway := fixture.Gateway(serviceGatewayName,
				fixture.InNamespace[*gatewayapiv1.Gateway](testNs),
				fixture.WithClassName(GatewayClassName),
				fixture.WithListener(gatewayapiv1.HTTPProtocolType),
			)
			Expect(envTest.Client.Create(ctx, serviceGateway)).Should(Succeed())

			systemGateway := fixture.Gateway(systemGatewayName,
				fixture.InNamespace[*gatewayapiv1.Gateway](systemNamespace),
				fixture.WithClassName(GatewayClassName),
				fixture.WithListener(gatewayapiv1.HTTPProtocolType),
			)
			Expect(envTest.Client.Create(ctx, systemGateway)).Should(Succeed())

			// Create configs in both namespaces with same name
			serviceConfig := fixture.LLMInferenceServiceConfig("shared-config",
				fixture.InNamespace[*kservev1alpha2.LLMInferenceServiceConfig](testNs),
			)
			serviceConfig.Spec.Router = &kservev1alpha2.RouterSpec{
				Gateway: &kservev1alpha2.GatewaySpec{
					Refs: []kservev1alpha2.UntypedObjectReference{
						{
							Name:      gatewayapiv1.ObjectName(serviceGatewayName),
							Namespace: gatewayapiv1.Namespace(testNs),
						},
					},
				},
			}
			Expect(envTest.Client.Create(ctx, serviceConfig)).Should(Succeed())

			systemConfig := fixture.LLMInferenceServiceConfig("shared-config",
				fixture.InNamespace[*kservev1alpha2.LLMInferenceServiceConfig](systemNamespace),
			)
			systemConfig.Spec.Router = &kservev1alpha2.RouterSpec{
				Gateway: &kservev1alpha2.GatewaySpec{
					Refs: []kservev1alpha2.UntypedObjectReference{
						{
							Name:      gatewayapiv1.ObjectName(systemGatewayName),
							Namespace: gatewayapiv1.Namespace(systemNamespace),
						},
					},
				},
			}
			Expect(envTest.Client.Create(ctx, systemConfig)).Should(Succeed())

			// Create LLMInferenceService with BaseRefs
			llmisvc := fixture.LLMInferenceService(LLMInferenceServiceName,
				fixture.InNamespace[*kservev1alpha2.LLMInferenceService](testNs),
				fixture.WithBaseRefs(corev1.LocalObjectReference{Name: "shared-config"}),
			)
			Expect(envTest.Client.Create(ctx, llmisvc)).Should(Succeed())

			// Verify AuthPolicy is created for the service namespace gateway, not the system one
			fixture.VerifyGatewayAuthPolicyOwnerRef(ctx, envTest.Client, testNs, serviceGatewayName)

			// Verify AuthPolicy is NOT created for the system namespace gateway
			Eventually(func() error {
				authPolicy := &kuadrantv1.AuthPolicy{}
				return envTest.Client.Get(ctx, types.NamespacedName{
					Name:      constants.GetGatewayAuthPolicyName(systemGatewayName),
					Namespace: systemNamespace,
				}, authPolicy)
			}).WithContext(ctx).Should(And(
				Not(Succeed()),
				WithTransform(errors.IsNotFound, BeTrue()),
			))
		})

		It("should handle config not found when POD_NAMESPACE is not set", func(ctx SpecContext) {
			// Do not set POD_NAMESPACE env var, so getConfig returns (nil, nil) when config is not found

			// Create LLMInferenceService with BaseRefs pointing to non-existent config
			llmisvc := fixture.LLMInferenceService(LLMInferenceServiceName,
				fixture.InNamespace[*kservev1alpha2.LLMInferenceService](testNs),
				fixture.WithBaseRefs(corev1.LocalObjectReference{Name: "missing-config"}),
			)
			Expect(envTest.Client.Create(ctx, llmisvc)).Should(Succeed())

			// When POD_NAMESPACE is not set and config is not found in service namespace,
			// getConfig returns (nil, nil) - no error, so reconciliation continues with just the service's own spec
			// This should create AuthPolicy for the default gateway
			fixture.VerifyGatewayAuthPolicyOwnerRef(ctx, envTest.Client, constants.DefaultGatewayNamespace, constants.DefaultGatewayName)
		})
	})

	Context("Multiple BaseRefs and Spec Merging", func() {
		It("should merge multiple config specs from BaseRefs and use the merged spec for AuthPolicy creation", func(ctx SpecContext) {
			gateway1Name := pkgtest.GenerateUniqueTestName("gateway1")

			// Create a gateway
			gateway1 := fixture.Gateway(gateway1Name,
				fixture.InNamespace[*gatewayapiv1.Gateway](testNs),
				fixture.WithClassName(GatewayClassName),
				fixture.WithListener(gatewayapiv1.HTTPProtocolType),
			)
			Expect(envTest.Client.Create(ctx, gateway1)).Should(Succeed())

			// Create first config with gateway reference
			config1 := fixture.LLMInferenceServiceConfig("config1",
				fixture.InNamespace[*kservev1alpha2.LLMInferenceServiceConfig](testNs),
			)
			config1.Spec.Router = &kservev1alpha2.RouterSpec{
				Gateway: &kservev1alpha2.GatewaySpec{
					Refs: []kservev1alpha2.UntypedObjectReference{
						{
							Name:      gatewayapiv1.ObjectName(gateway1Name),
							Namespace: gatewayapiv1.Namespace(testNs),
						},
					},
				},
			}
			Expect(envTest.Client.Create(ctx, config1)).Should(Succeed())

			// Create second config (will be merged but config1 gateway takes precedence)
			config2 := fixture.LLMInferenceServiceConfig("config2",
				fixture.InNamespace[*kservev1alpha2.LLMInferenceServiceConfig](testNs),
			)
			Expect(envTest.Client.Create(ctx, config2)).Should(Succeed())

			// Create LLMInferenceService with multiple BaseRefs
			llmisvc := fixture.LLMInferenceService(LLMInferenceServiceName,
				fixture.InNamespace[*kservev1alpha2.LLMInferenceService](testNs),
				fixture.WithBaseRefs(
					corev1.LocalObjectReference{Name: "config1"},
					corev1.LocalObjectReference{Name: "config2"},
				),
			)
			Expect(envTest.Client.Create(ctx, llmisvc)).Should(Succeed())

			// Verify AuthPolicy is created for gateway from config1
			fixture.VerifyGatewayAuthPolicyOwnerRef(ctx, envTest.Client, testNs, gateway1Name)
		})

		It("should allow service spec to override config specs", func(ctx SpecContext) {
			configGatewayName := pkgtest.GenerateUniqueTestName("config-gateway")
			serviceGatewayName := pkgtest.GenerateUniqueTestName("service-gateway")

			// Create gateways
			configGateway := fixture.Gateway(configGatewayName,
				fixture.InNamespace[*gatewayapiv1.Gateway](testNs),
				fixture.WithClassName(GatewayClassName),
				fixture.WithListener(gatewayapiv1.HTTPProtocolType),
			)
			Expect(envTest.Client.Create(ctx, configGateway)).Should(Succeed())

			serviceGateway := fixture.Gateway(serviceGatewayName,
				fixture.InNamespace[*gatewayapiv1.Gateway](testNs),
				fixture.WithClassName(GatewayClassName),
				fixture.WithListener(gatewayapiv1.HTTPProtocolType),
			)
			Expect(envTest.Client.Create(ctx, serviceGateway)).Should(Succeed())

			// Create config with gateway reference
			config := fixture.LLMInferenceServiceConfig("test-config",
				fixture.InNamespace[*kservev1alpha2.LLMInferenceServiceConfig](testNs),
			)
			config.Spec.Router = &kservev1alpha2.RouterSpec{
				Gateway: &kservev1alpha2.GatewaySpec{
					Refs: []kservev1alpha2.UntypedObjectReference{
						{
							Name:      gatewayapiv1.ObjectName(configGatewayName),
							Namespace: gatewayapiv1.Namespace(testNs),
						},
					},
				},
			}
			Expect(envTest.Client.Create(ctx, config)).Should(Succeed())

			// Create LLMInferenceService with BaseRefs but also with its own gateway spec
			llmisvc := fixture.LLMInferenceService(LLMInferenceServiceName,
				fixture.InNamespace[*kservev1alpha2.LLMInferenceService](testNs),
				fixture.WithBaseRefs(corev1.LocalObjectReference{Name: "test-config"}),
				fixture.WithGatewayRefs(kservev1alpha2.UntypedObjectReference{
					Name:      gatewayapiv1.ObjectName(serviceGatewayName),
					Namespace: gatewayapiv1.Namespace(testNs),
				}),
			)
			Expect(envTest.Client.Create(ctx, llmisvc)).Should(Succeed())

			// Verify AuthPolicy is created for the service's gateway, not the config's
			fixture.VerifyGatewayAuthPolicyOwnerRef(ctx, envTest.Client, testNs, serviceGatewayName)

			// Verify AuthPolicy is NOT created for the config gateway
			Eventually(func() error {
				authPolicy := &kuadrantv1.AuthPolicy{}
				return envTest.Client.Get(ctx, types.NamespacedName{
					Name:      constants.GetGatewayAuthPolicyName(configGatewayName),
					Namespace: testNs,
				}, authPolicy)
			}).WithContext(ctx).Should(And(
				Not(Succeed()),
				WithTransform(errors.IsNotFound, BeTrue()),
			))
		})

		It("should handle config fetch failures gracefully and continue with available configs", func(ctx SpecContext) {
			gatewayName := pkgtest.GenerateUniqueTestName("gateway")

			// Create a gateway
			gateway := fixture.Gateway(gatewayName,
				fixture.InNamespace[*gatewayapiv1.Gateway](testNs),
				fixture.WithClassName(GatewayClassName),
				fixture.WithListener(gatewayapiv1.HTTPProtocolType),
			)
			Expect(envTest.Client.Create(ctx, gateway)).Should(Succeed())

			// Create only config2, config1 will fail to fetch
			config2 := fixture.LLMInferenceServiceConfig("config2",
				fixture.InNamespace[*kservev1alpha2.LLMInferenceServiceConfig](testNs),
			)
			config2.Spec.Router = &kservev1alpha2.RouterSpec{
				Gateway: &kservev1alpha2.GatewaySpec{
					Refs: []kservev1alpha2.UntypedObjectReference{
						{
							Name:      gatewayapiv1.ObjectName(gatewayName),
							Namespace: gatewayapiv1.Namespace(testNs),
						},
					},
				},
			}
			Expect(envTest.Client.Create(ctx, config2)).Should(Succeed())

			// Create LLMInferenceService with BaseRefs including a non-existent config
			llmisvc := fixture.LLMInferenceService(LLMInferenceServiceName,
				fixture.InNamespace[*kservev1alpha2.LLMInferenceService](testNs),
				fixture.WithBaseRefs(
					corev1.LocalObjectReference{Name: "config1"}, // Will fail
					corev1.LocalObjectReference{Name: "config2"}, // Will succeed
				),
			)
			Expect(envTest.Client.Create(ctx, llmisvc)).Should(Succeed())

			// Verify AuthPolicy is still created for the gateway from config2
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

			role.Labels[constants.ODHManagedLabel] = "false"
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

			rb.Labels[constants.ODHManagedLabel] = "false"
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
	})

	Context("Gateway filtering with BaseRefs", func() {
		It("should exclude gateway with managed=false label when using BaseRefs", func(ctx SpecContext) {
			unmanagedGatewayName := pkgtest.GenerateUniqueTestName("unmanaged-gateway")

			// Create an unmanaged gateway
			unmanagedGateway := fixture.Gateway(unmanagedGatewayName,
				fixture.InNamespace[*gatewayapiv1.Gateway](testNs),
				fixture.WithClassName(GatewayClassName),
				fixture.WithListener(gatewayapiv1.HTTPProtocolType),
			)
			unmanagedGateway.Labels = map[string]string{
				constants.ODHManagedLabel: "false",
			}
			Expect(envTest.Client.Create(ctx, unmanagedGateway)).Should(Succeed())

			// Create config with reference to unmanaged gateway
			config := fixture.LLMInferenceServiceConfig("test-config",
				fixture.InNamespace[*kservev1alpha2.LLMInferenceServiceConfig](testNs),
			)
			config.Spec.Router = &kservev1alpha2.RouterSpec{
				Gateway: &kservev1alpha2.GatewaySpec{
					Refs: []kservev1alpha2.UntypedObjectReference{
						{
							Name:      gatewayapiv1.ObjectName(unmanagedGatewayName),
							Namespace: gatewayapiv1.Namespace(testNs),
						},
					},
				},
			}
			Expect(envTest.Client.Create(ctx, config)).Should(Succeed())

			// Create LLMInferenceService with BaseRefs
			llmisvc := fixture.LLMInferenceService(LLMInferenceServiceName,
				fixture.InNamespace[*kservev1alpha2.LLMInferenceService](testNs),
				fixture.WithBaseRefs(corev1.LocalObjectReference{Name: "test-config"}),
			)
			Expect(envTest.Client.Create(ctx, llmisvc)).Should(Succeed())

			// Verify NO AuthPolicy is created for the unmanaged gateway
			Consistently(func() error {
				authPolicy := &kuadrantv1.AuthPolicy{}
				return envTest.Client.Get(ctx, types.NamespacedName{
					Name:      constants.GetGatewayAuthPolicyName(unmanagedGatewayName),
					Namespace: testNs,
				}, authPolicy)
			}).WithContext(ctx).Should(And(
				Not(Succeed()),
				WithTransform(errors.IsNotFound, BeTrue()),
			))
		})
	})
})
