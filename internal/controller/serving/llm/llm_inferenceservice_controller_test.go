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
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/serving/llm/fixture"
	pkgtest "github.com/opendatahub-io/odh-model-controller/internal/controller/testing"
)

const (
	LLMInferenceServiceName = "test-llmisvc"
	CustomGatewayName       = "ready-gateway"
)

var _ = Describe("LLMInferenceService Controller", func() {

	var testNs string
	BeforeEach(func() {
		ctx := context.Background()
		// Generate unique namespace name
		testNs = pkgtest.GenerateUniqueTestNamespaceName("test-auth-ns")
		fixture.SetupTestNamespace(ctx, envTest.Client, testNs)
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
				customGatewayName := CustomGatewayName
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

				fixture.VerifyGatewayAuthPolicyOwnerRef(ctx, envTest.Client, customGatewayNamespace, customGatewayName)
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
					customGatewayName := CustomGatewayName
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
					customGatewayName := CustomGatewayName
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
			})
		})
	})
})
