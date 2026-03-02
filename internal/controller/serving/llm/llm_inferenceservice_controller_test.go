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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	istioclientv1alpha3 "istio.io/client-go/pkg/apis/networking/v1alpha3"
	corev1 "k8s.io/api/core/v1"
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
	BeforeEach(func() {
		ctx := context.Background()
		customGatewayName = pkgtest.GenerateUniqueTestName("custom-gateway")
		testNamespace := testutils.Namespaces.Create(ctx, envTest.Client)
		testNs = testNamespace.Name
	})

	AfterEach(func(ctx SpecContext) {
		llmList := &kservev1alpha1.LLMInferenceServiceList{}
		if err := envTest.Client.List(ctx, llmList, client.InNamespace(testNs)); err == nil {
			for i := range llmList.Items {
				_ = envTest.Client.Delete(ctx, &llmList.Items[i])
			}
		}
	})

	Context("EnvoyFilter Reconcile Tests", func() {
		Context("when Gateway EnvoyFilter is created", func() {
			It("should create default Gateway EnvoyFilter when LLMInferenceService is created", func(ctx SpecContext) {
				fixture.CreateBasicLLMInferenceService(ctx, envTest.Client, testNs, LLMInferenceServiceName)
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
				fixture.CreateBasicLLMInferenceService(ctx, envTest.Client, testNs, LLMInferenceServiceName)

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
				fixture.CreateBasicLLMInferenceService(ctx, envTest.Client, testNs, LLMInferenceServiceName)

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
					fixture.InNamespace[*kservev1alpha1.LLMInferenceService](testNs),
					fixture.WithGatewayRefs(kservev1alpha1.UntypedObjectReference{
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
					fixture.InNamespace[*kservev1alpha1.LLMInferenceService](testNs),
					fixture.WithGatewayRefs(kservev1alpha1.UntypedObjectReference{
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
					fixture.InNamespace[*kservev1alpha1.LLMInferenceService](testNs),
					fixture.WithGatewayRefs(kservev1alpha1.UntypedObjectReference{
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
					fixture.InNamespace[*kservev1alpha1.LLMInferenceService](testNs),
					fixture.WithGatewayRefs(kservev1alpha1.UntypedObjectReference{
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
					fixture.InNamespace[*kservev1alpha1.LLMInferenceService](testNs),
					fixture.WithGatewayRefs(kservev1alpha1.UntypedObjectReference{
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
					fixture.InNamespace[*kservev1alpha1.LLMInferenceService](testNs),
					fixture.WithGatewayRefs(kservev1alpha1.UntypedObjectReference{
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
		It("should retrieve LLMInferenceServiceConfig from service namespace and create EnvoyFilter for referenced gateway", func(ctx SpecContext) {
			customGatewayName := pkgtest.GenerateUniqueTestName("config-gateway")
			customGatewayNamespace := testNs

			customGateway := fixture.Gateway(customGatewayName,
				fixture.InNamespace[*gatewayapiv1.Gateway](customGatewayNamespace),
				fixture.WithClassName(GatewayClassName),
				fixture.WithListener(gatewayapiv1.HTTPProtocolType),
			)
			Expect(envTest.Client.Create(ctx, customGateway)).Should(Succeed())

			config := fixture.LLMInferenceServiceConfig("test-config",
				fixture.InNamespace[*kservev1alpha1.LLMInferenceServiceConfig](testNs),
			)
			config.Spec.Router = &kservev1alpha1.RouterSpec{
				Gateway: &kservev1alpha1.GatewaySpec{
					Refs: []kservev1alpha1.UntypedObjectReference{
						{
							Name:      gatewayapiv1.ObjectName(customGatewayName),
							Namespace: gatewayapiv1.Namespace(customGatewayNamespace),
						},
					},
				},
			}
			Expect(envTest.Client.Create(ctx, config)).Should(Succeed())

			llmisvc := fixture.LLMInferenceService(LLMInferenceServiceName,
				fixture.InNamespace[*kservev1alpha1.LLMInferenceService](testNs),
				fixture.WithBaseRefs(corev1.LocalObjectReference{Name: "test-config"}),
			)
			Expect(envTest.Client.Create(ctx, llmisvc)).Should(Succeed())

			fixture.VerifyGatewayEnvoyFilterOwnerRef(ctx, envTest.Client, customGatewayNamespace, customGatewayName)
		})

		It("should retrieve LLMInferenceServiceConfig from system namespace when not found in service namespace", func(ctx SpecContext) {
			customGatewayName := pkgtest.GenerateUniqueTestName("system-gateway")

			systemNs := testutils.Namespaces.Create(ctx, envTest.Client)
			systemNamespace := systemNs.Name

			_ = os.Setenv("POD_NAMESPACE", systemNamespace)
			defer func() {
				_ = os.Unsetenv("POD_NAMESPACE")
			}()

			customGateway := fixture.Gateway(customGatewayName,
				fixture.InNamespace[*gatewayapiv1.Gateway](systemNamespace),
				fixture.WithClassName(GatewayClassName),
				fixture.WithListener(gatewayapiv1.HTTPProtocolType),
			)
			Expect(envTest.Client.Create(ctx, customGateway)).Should(Succeed())

			config := fixture.LLMInferenceServiceConfig("system-config",
				fixture.InNamespace[*kservev1alpha1.LLMInferenceServiceConfig](systemNamespace),
			)
			config.Spec.Router = &kservev1alpha1.RouterSpec{
				Gateway: &kservev1alpha1.GatewaySpec{
					Refs: []kservev1alpha1.UntypedObjectReference{
						{
							Name:      gatewayapiv1.ObjectName(customGatewayName),
							Namespace: gatewayapiv1.Namespace(systemNamespace),
						},
					},
				},
			}
			Expect(envTest.Client.Create(ctx, config)).Should(Succeed())

			llmisvc := fixture.LLMInferenceService(LLMInferenceServiceName,
				fixture.InNamespace[*kservev1alpha1.LLMInferenceService](testNs),
				fixture.WithBaseRefs(corev1.LocalObjectReference{Name: "system-config"}),
			)
			Expect(envTest.Client.Create(ctx, llmisvc)).Should(Succeed())

			fixture.VerifyGatewayEnvoyFilterOwnerRef(ctx, envTest.Client, systemNamespace, customGatewayName)
		})

		It("should prioritize service namespace config over system namespace config", func(ctx SpecContext) {
			serviceGatewayName := pkgtest.GenerateUniqueTestName("service-gateway")
			systemGatewayName := pkgtest.GenerateUniqueTestName("system-gateway")

			systemNs := testutils.Namespaces.Create(ctx, envTest.Client)
			systemNamespace := systemNs.Name

			_ = os.Setenv("POD_NAMESPACE", systemNamespace)
			defer func() {
				_ = os.Unsetenv("POD_NAMESPACE")
			}()

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

			serviceConfig := fixture.LLMInferenceServiceConfig("shared-config",
				fixture.InNamespace[*kservev1alpha1.LLMInferenceServiceConfig](testNs),
			)
			serviceConfig.Spec.Router = &kservev1alpha1.RouterSpec{
				Gateway: &kservev1alpha1.GatewaySpec{
					Refs: []kservev1alpha1.UntypedObjectReference{
						{
							Name:      gatewayapiv1.ObjectName(serviceGatewayName),
							Namespace: gatewayapiv1.Namespace(testNs),
						},
					},
				},
			}
			Expect(envTest.Client.Create(ctx, serviceConfig)).Should(Succeed())

			systemConfig := fixture.LLMInferenceServiceConfig("shared-config",
				fixture.InNamespace[*kservev1alpha1.LLMInferenceServiceConfig](systemNamespace),
			)
			systemConfig.Spec.Router = &kservev1alpha1.RouterSpec{
				Gateway: &kservev1alpha1.GatewaySpec{
					Refs: []kservev1alpha1.UntypedObjectReference{
						{
							Name:      gatewayapiv1.ObjectName(systemGatewayName),
							Namespace: gatewayapiv1.Namespace(systemNamespace),
						},
					},
				},
			}
			Expect(envTest.Client.Create(ctx, systemConfig)).Should(Succeed())

			llmisvc := fixture.LLMInferenceService(LLMInferenceServiceName,
				fixture.InNamespace[*kservev1alpha1.LLMInferenceService](testNs),
				fixture.WithBaseRefs(corev1.LocalObjectReference{Name: "shared-config"}),
			)
			Expect(envTest.Client.Create(ctx, llmisvc)).Should(Succeed())

			fixture.VerifyGatewayEnvoyFilterOwnerRef(ctx, envTest.Client, testNs, serviceGatewayName)

			fixture.VerifyGatewayEnvoyFilterNotExist(ctx, envTest.Client, systemNamespace, systemGatewayName)
		})

		It("should handle config not found when POD_NAMESPACE is not set", func(ctx SpecContext) {
			llmisvc := fixture.LLMInferenceService(LLMInferenceServiceName,
				fixture.InNamespace[*kservev1alpha1.LLMInferenceService](testNs),
				fixture.WithBaseRefs(corev1.LocalObjectReference{Name: "missing-config"}),
			)
			Expect(envTest.Client.Create(ctx, llmisvc)).Should(Succeed())

			fixture.VerifyGatewayEnvoyFilterOwnerRef(ctx, envTest.Client, constants.DefaultGatewayNamespace, constants.DefaultGatewayName)
		})
	})

	Context("Multiple BaseRefs and Spec Merging", func() {
		It("should merge multiple config specs from BaseRefs and use the merged spec for EnvoyFilter creation", func(ctx SpecContext) {
			gateway1Name := pkgtest.GenerateUniqueTestName("gateway1")

			gateway1 := fixture.Gateway(gateway1Name,
				fixture.InNamespace[*gatewayapiv1.Gateway](testNs),
				fixture.WithClassName(GatewayClassName),
				fixture.WithListener(gatewayapiv1.HTTPProtocolType),
			)
			Expect(envTest.Client.Create(ctx, gateway1)).Should(Succeed())

			config1 := fixture.LLMInferenceServiceConfig("config1",
				fixture.InNamespace[*kservev1alpha1.LLMInferenceServiceConfig](testNs),
			)
			config1.Spec.Router = &kservev1alpha1.RouterSpec{
				Gateway: &kservev1alpha1.GatewaySpec{
					Refs: []kservev1alpha1.UntypedObjectReference{
						{
							Name:      gatewayapiv1.ObjectName(gateway1Name),
							Namespace: gatewayapiv1.Namespace(testNs),
						},
					},
				},
			}
			Expect(envTest.Client.Create(ctx, config1)).Should(Succeed())

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

			fixture.VerifyGatewayEnvoyFilterOwnerRef(ctx, envTest.Client, testNs, gateway1Name)
		})

		It("should allow service spec to override config specs", func(ctx SpecContext) {
			configGatewayName := pkgtest.GenerateUniqueTestName("config-gateway")
			serviceGatewayName := pkgtest.GenerateUniqueTestName("service-gateway")

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

			config := fixture.LLMInferenceServiceConfig("test-config",
				fixture.InNamespace[*kservev1alpha1.LLMInferenceServiceConfig](testNs),
			)
			config.Spec.Router = &kservev1alpha1.RouterSpec{
				Gateway: &kservev1alpha1.GatewaySpec{
					Refs: []kservev1alpha1.UntypedObjectReference{
						{
							Name:      gatewayapiv1.ObjectName(configGatewayName),
							Namespace: gatewayapiv1.Namespace(testNs),
						},
					},
				},
			}
			Expect(envTest.Client.Create(ctx, config)).Should(Succeed())

			llmisvc := fixture.LLMInferenceService(LLMInferenceServiceName,
				fixture.InNamespace[*kservev1alpha1.LLMInferenceService](testNs),
				fixture.WithBaseRefs(corev1.LocalObjectReference{Name: "test-config"}),
				fixture.WithGatewayRefs(kservev1alpha1.UntypedObjectReference{
					Name:      gatewayapiv1.ObjectName(serviceGatewayName),
					Namespace: gatewayapiv1.Namespace(testNs),
				}),
			)
			Expect(envTest.Client.Create(ctx, llmisvc)).Should(Succeed())

			fixture.VerifyGatewayEnvoyFilterOwnerRef(ctx, envTest.Client, testNs, serviceGatewayName)

			fixture.VerifyGatewayEnvoyFilterNotExist(ctx, envTest.Client, testNs, configGatewayName)
		})

		It("should handle config fetch failures gracefully and continue with available configs", func(ctx SpecContext) {
			gatewayName := pkgtest.GenerateUniqueTestName("gateway")

			gateway := fixture.Gateway(gatewayName,
				fixture.InNamespace[*gatewayapiv1.Gateway](testNs),
				fixture.WithClassName(GatewayClassName),
				fixture.WithListener(gatewayapiv1.HTTPProtocolType),
			)
			Expect(envTest.Client.Create(ctx, gateway)).Should(Succeed())

			config2 := fixture.LLMInferenceServiceConfig("config2",
				fixture.InNamespace[*kservev1alpha1.LLMInferenceServiceConfig](testNs),
			)
			config2.Spec.Router = &kservev1alpha1.RouterSpec{
				Gateway: &kservev1alpha1.GatewaySpec{
					Refs: []kservev1alpha1.UntypedObjectReference{
						{
							Name:      gatewayapiv1.ObjectName(gatewayName),
							Namespace: gatewayapiv1.Namespace(testNs),
						},
					},
				},
			}
			Expect(envTest.Client.Create(ctx, config2)).Should(Succeed())

			llmisvc := fixture.LLMInferenceService(LLMInferenceServiceName,
				fixture.InNamespace[*kservev1alpha1.LLMInferenceService](testNs),
				fixture.WithBaseRefs(
					corev1.LocalObjectReference{Name: "config1"},
					corev1.LocalObjectReference{Name: "config2"},
				),
			)
			Expect(envTest.Client.Create(ctx, llmisvc)).Should(Succeed())

			fixture.VerifyGatewayEnvoyFilterOwnerRef(ctx, envTest.Client, testNs, gatewayName)
		})
	})

	Context("Gateway filtering with BaseRefs", func() {
		It("should exclude gateway with managed=false label when using BaseRefs", func(ctx SpecContext) {
			unmanagedGatewayName := pkgtest.GenerateUniqueTestName("unmanaged-gateway")

			unmanagedGateway := fixture.Gateway(unmanagedGatewayName,
				fixture.InNamespace[*gatewayapiv1.Gateway](testNs),
				fixture.WithClassName(GatewayClassName),
				fixture.WithListener(gatewayapiv1.HTTPProtocolType),
			)
			unmanagedGateway.Labels = map[string]string{
				constants.ODHManagedLabel: "false",
			}
			Expect(envTest.Client.Create(ctx, unmanagedGateway)).Should(Succeed())

			config := fixture.LLMInferenceServiceConfig("test-config",
				fixture.InNamespace[*kservev1alpha1.LLMInferenceServiceConfig](testNs),
			)
			config.Spec.Router = &kservev1alpha1.RouterSpec{
				Gateway: &kservev1alpha1.GatewaySpec{
					Refs: []kservev1alpha1.UntypedObjectReference{
						{
							Name:      gatewayapiv1.ObjectName(unmanagedGatewayName),
							Namespace: gatewayapiv1.Namespace(testNs),
						},
					},
				},
			}
			Expect(envTest.Client.Create(ctx, config)).Should(Succeed())

			llmisvc := fixture.LLMInferenceService(LLMInferenceServiceName,
				fixture.InNamespace[*kservev1alpha1.LLMInferenceService](testNs),
				fixture.WithBaseRefs(corev1.LocalObjectReference{Name: "test-config"}),
			)
			Expect(envTest.Client.Create(ctx, llmisvc)).Should(Succeed())

			fixture.VerifyGatewayEnvoyFilterNotExist(ctx, envTest.Client, testNs, unmanagedGatewayName)
		})
	})
})
