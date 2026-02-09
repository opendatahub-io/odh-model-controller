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
	kuadrantv1 "github.com/kuadrant/kuadrant-operator/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	istioclientv1alpha3 "istio.io/client-go/pkg/apis/networking/v1alpha3"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/serving/llm/fixture"
	pkgtest "github.com/opendatahub-io/odh-model-controller/internal/controller/testing"
	testutils "github.com/opendatahub-io/odh-model-controller/test/utils"
)

var _ = Describe("Gateway Controller", func() {
	var testNs string
	BeforeEach(func(specContext SpecContext) {
		testNamespace := testutils.Namespaces.Create(specContext, envTest.Client)
		testNs = testNamespace.Name
	})

	AfterEach(func(ctx SpecContext) {
		// Clean up LLMInferenceServices to prevent cross-test interference
		// from watches that list all services cluster-wide.
		llmList := &kservev1alpha1.LLMInferenceServiceList{}
		if err := envTest.Client.List(ctx, llmList, client.InNamespace(testNs)); err == nil {
			for i := range llmList.Items {
				_ = envTest.Client.Delete(ctx, &llmList.Items[i])
			}
		}
	})

	createGateway := func(ctx context.Context, name string, opts ...fixture.GatewayOption) *gatewayapiv1.Gateway {
		gw := fixture.ManagedGateway(name, testNs, GatewayClassName, opts...)
		ExpectWithOffset(1, envTest.Client.Create(ctx, gw)).Should(Succeed())
		return gw
	}

	createLLMService := func(ctx context.Context, name string, opts ...fixture.LLMInferenceServiceOption) *kservev1alpha1.LLMInferenceService {
		defaultOpts := []fixture.LLMInferenceServiceOption{
			fixture.InNamespace[*kservev1alpha1.LLMInferenceService](testNs),
		}
		llmSvc := fixture.LLMInferenceService(name, append(defaultOpts, opts...)...)
		ExpectWithOffset(1, envTest.Client.Create(ctx, llmSvc)).Should(Succeed())
		return llmSvc
	}

	createLLMServiceForGateway := func(ctx context.Context, gatewayName string) {
		createLLMService(ctx, "test-llmisvc",
			fixture.WithGatewayRefs(fixture.LLMGatewayRef(gatewayName, testNs)),
		)
	}

	setupReferencedGateway := func(ctx context.Context) string {
		gatewayName := pkgtest.GenerateUniqueTestName("managed-gateway")
		createGateway(ctx, gatewayName)
		createLLMServiceForGateway(ctx, gatewayName)

		fixture.VerifyGatewayEnvoyFilterExists(ctx, envTest.Client, testNs, gatewayName)
		fixture.VerifyGatewayAuthPolicyExists(ctx, envTest.Client, testNs, gatewayName)

		return gatewayName
	}

	Context("Gateway referenced by LLMInferenceService", func() {
		It("should create EnvoyFilter and AuthPolicy with correct owner references", func(ctx SpecContext) {
			gatewayName := setupReferencedGateway(ctx)

			fixture.VerifyGatewayEnvoyFilterOwnerRef(ctx, envTest.Client, testNs, gatewayName)
			fixture.VerifyGatewayAuthPolicyOwnerRef(ctx, envTest.Client, testNs, gatewayName)
		})
	})

	Context("Gateway not referenced by any LLMInferenceService", func() {
		It("should NOT create EnvoyFilter or AuthPolicy for unreferenced managed gateway", func(ctx SpecContext) {
			gatewayName := pkgtest.GenerateUniqueTestName("unreferenced-gateway")
			createGateway(ctx, gatewayName)

			fixture.VerifyGatewayEnvoyFilterNotExist(ctx, envTest.Client, testNs, gatewayName)
			fixture.VerifyGatewayAuthPolicyNotExist(ctx, envTest.Client, testNs, gatewayName)
		})
	})

	Context("Unmanaged and platform gateways", func() {
		DescribeTable("should NOT create EnvoyFilter or AuthPolicy",
			func(ctx SpecContext, opts ...fixture.GatewayOption) {
				gatewayName := pkgtest.GenerateUniqueTestName("gateway")
				createGateway(ctx, gatewayName, opts...)

				fixture.VerifyGatewayEnvoyFilterNotExist(ctx, envTest.Client, testNs, gatewayName)
				fixture.VerifyGatewayAuthPolicyNotExist(ctx, envTest.Client, testNs, gatewayName)
			},
			Entry("unmanaged gateway without bootstrap annotation",
				fixture.WithUnmanagedLabel(),
			),
			Entry("platform gateway without bootstrap annotation",
				fixture.WithControllerOwnerRef("GatewayConfig", "default-gateway", "services.platform.opendatahub.io/v1alpha1"),
			),
		)

		DescribeTable("should create EnvoyFilter but NOT AuthPolicy when Gateway has bootstrap annotation",
			func(ctx SpecContext, opts ...fixture.GatewayOption) {
				gatewayName := pkgtest.GenerateUniqueTestName("gateway")
				allOpts := append(opts, fixture.WithAuthorinoTLSBootstrapAnnotation("true"))
				createGateway(ctx, gatewayName, allOpts...)

				fixture.VerifyGatewayEnvoyFilterExists(ctx, envTest.Client, testNs, gatewayName)
				fixture.VerifyGatewayAuthPolicyNotExist(ctx, envTest.Client, testNs, gatewayName)
			},
			Entry("unmanaged gateway",
				fixture.WithUnmanagedLabel(),
			),
			Entry("platform gateway (with explicit tls-bootstrap annotation opt-in)",
				fixture.WithControllerOwnerRef("GatewayConfig", "default-gateway", "services.platform.opendatahub.io/v1alpha1"),
			),
		)
	})

	Context("Bootstrap annotation removal", func() {
		It("should delete EnvoyFilter when bootstrap annotation is removed from unmanaged gateway", func(ctx SpecContext) {
			gatewayName := pkgtest.GenerateUniqueTestName("bootstrap-gateway")
			createGateway(ctx, gatewayName,
				fixture.WithUnmanagedLabel(),
				fixture.WithAuthorinoTLSBootstrapAnnotation("true"),
			)

			fixture.VerifyGatewayEnvoyFilterExists(ctx, envTest.Client, testNs, gatewayName)

			Eventually(func() error {
				gw := &gatewayapiv1.Gateway{}
				if err := envTest.Client.Get(ctx, types.NamespacedName{Name: gatewayName, Namespace: testNs}, gw); err != nil {
					return err
				}
				delete(gw.Annotations, constants.AuthorinoTLSBootstrapAnnotation)
				return envTest.Client.Update(ctx, gw)
			}).WithContext(ctx).Should(Succeed())

			fixture.VerifyGatewayEnvoyFilterNotExist(ctx, envTest.Client, testNs, gatewayName)
		})
	})

	Context("Default gateway from ConfigMap", func() {
		It("should create resources for the default gateway without any LLMInferenceService existing", func(ctx SpecContext) {
			fixture.VerifyGatewayEnvoyFilterExists(ctx, envTest.Client, constants.DefaultGatewayNamespace, constants.DefaultGatewayName)
			fixture.VerifyGatewayAuthPolicyExists(ctx, envTest.Client, constants.DefaultGatewayNamespace, constants.DefaultGatewayName)
		})
	})

	Context("Resource restoration", func() {
		It("should recreate AuthPolicy when deleted", func(ctx SpecContext) {
			gatewayName := setupReferencedGateway(ctx)

			authPolicy := fixture.WaitForResource(ctx, envTest.Client, testNs, constants.GetAuthPolicyName(gatewayName), &kuadrantv1.AuthPolicy{})
			Expect(envTest.Client.Delete(ctx, authPolicy)).Should(Succeed())

			fixture.VerifyGatewayAuthPolicyExists(ctx, envTest.Client, testNs, gatewayName)
		})

		It("should recreate EnvoyFilter when deleted", func(ctx SpecContext) {
			gatewayName := setupReferencedGateway(ctx)

			envoyFilter := fixture.WaitForResource(ctx, envTest.Client, testNs, constants.GetGatewayEnvoyFilterName(gatewayName), &istioclientv1alpha3.EnvoyFilter{})
			Expect(envTest.Client.Delete(ctx, envoyFilter)).Should(Succeed())

			fixture.VerifyGatewayEnvoyFilterExists(ctx, envTest.Client, testNs, gatewayName)
		})

		It("should restore AuthPolicy when modified", func(ctx SpecContext) {
			gatewayName := setupReferencedGateway(ctx)

			authPolicy := fixture.WaitForResource(ctx, envTest.Client, testNs, constants.GetAuthPolicyName(gatewayName), &kuadrantv1.AuthPolicy{})
			originalTargetRefName := authPolicy.Spec.TargetRef.Name

			authPolicy.Spec.TargetRef.Name = "modified-gateway" //nolint:goconst // test value
			Expect(envTest.Client.Update(ctx, authPolicy)).Should(Succeed())

			fixture.VerifyGatewayAuthPolicyRestored(ctx, envTest.Client, testNs, gatewayName, originalTargetRefName)
		})

		It("should restore EnvoyFilter when modified", func(ctx SpecContext) {
			gatewayName := setupReferencedGateway(ctx)

			envoyFilter := fixture.WaitForResource(ctx, envTest.Client, testNs, constants.GetGatewayEnvoyFilterName(gatewayName), &istioclientv1alpha3.EnvoyFilter{})
			originalTargetRefName := envoyFilter.Spec.TargetRefs[0].Name

			envoyFilter.Spec.TargetRefs[0].Name = "modified-gateway" //nolint:goconst // test value
			Expect(envTest.Client.Update(ctx, envoyFilter)).Should(Succeed())

			fixture.VerifyGatewayEnvoyFilterRestored(ctx, envTest.Client, testNs, gatewayName, originalTargetRefName)
		})
	})

	Context("Gateway label changes", func() {
		It("should delete resources when Gateway is changed from managed to unmanaged", func(ctx SpecContext) {
			gatewayName := setupReferencedGateway(ctx)

			Eventually(func() error {
				gw := &gatewayapiv1.Gateway{}
				if err := envTest.Client.Get(ctx, types.NamespacedName{Name: gatewayName, Namespace: testNs}, gw); err != nil {
					return err
				}
				if gw.Labels == nil {
					gw.Labels = make(map[string]string)
				}
				gw.Labels[constants.ODHManaged] = "false" //nolint:goconst // standard label value
				return envTest.Client.Update(ctx, gw)
			}).WithContext(ctx).Should(Succeed())

			fixture.VerifyGatewayEnvoyFilterNotExist(ctx, envTest.Client, testNs, gatewayName)
			fixture.VerifyGatewayAuthPolicyNotExist(ctx, envTest.Client, testNs, gatewayName)
		})
	})

	Context("LLMInferenceService gateway ref changes", func() {
		It("should create resources when gateway ref is added to LLMInferenceService", func(ctx SpecContext) {
			gatewayName := pkgtest.GenerateUniqueTestName("managed-gateway")
			createGateway(ctx, gatewayName)

			// Create LLMInferenceService without any gateway ref
			llmSvc := createLLMService(ctx, "test-llmisvc")

			fixture.VerifyGatewayEnvoyFilterNotExist(ctx, envTest.Client, testNs, gatewayName)
			fixture.VerifyGatewayAuthPolicyNotExist(ctx, envTest.Client, testNs, gatewayName)

			// Add gateway ref
			Eventually(func() error {
				if err := envTest.Client.Get(ctx, types.NamespacedName{Name: llmSvc.Name, Namespace: testNs}, llmSvc); err != nil {
					return err
				}
				llmSvc.Spec.Router = &kservev1alpha1.RouterSpec{
					Gateway: &kservev1alpha1.GatewaySpec{
						Refs: []kservev1alpha1.UntypedObjectReference{
							fixture.LLMGatewayRef(gatewayName, testNs),
						},
					},
				}
				return envTest.Client.Update(ctx, llmSvc)
			}).WithContext(ctx).Should(Succeed())

			fixture.VerifyGatewayEnvoyFilterExists(ctx, envTest.Client, testNs, gatewayName)
			fixture.VerifyGatewayAuthPolicyExists(ctx, envTest.Client, testNs, gatewayName)
		})

		It("should keep resources stable when gateway refs are reordered", func(ctx SpecContext) {
			gatewayName := pkgtest.GenerateUniqueTestName("managed-gateway")
			secondGatewayName := pkgtest.GenerateUniqueTestName("managed-gateway-2")
			createGateway(ctx, gatewayName)
			createGateway(ctx, secondGatewayName)

			refA := fixture.LLMGatewayRef(gatewayName, testNs)
			refB := fixture.LLMGatewayRef(secondGatewayName, testNs)

			// Create with refs [A, B]
			llmSvc := createLLMService(ctx, "test-llmisvc",
				fixture.WithGatewayRefs(refA, refB),
			)

			fixture.VerifyGatewayEnvoyFilterExists(ctx, envTest.Client, testNs, gatewayName)
			fixture.VerifyGatewayEnvoyFilterExists(ctx, envTest.Client, testNs, secondGatewayName)

			// Reorder to [B, A] — should not trigger unnecessary reconciliation
			Eventually(func() error {
				if err := envTest.Client.Get(ctx, types.NamespacedName{Name: llmSvc.Name, Namespace: testNs}, llmSvc); err != nil {
					return err
				}
				llmSvc.Spec.Router.Gateway.Refs = []kservev1alpha1.UntypedObjectReference{refB, refA}
				return envTest.Client.Update(ctx, llmSvc)
			}).WithContext(ctx).Should(Succeed())

			// Resources should remain stable
			fixture.VerifyGatewayEnvoyFilterExists(ctx, envTest.Client, testNs, gatewayName)
			fixture.VerifyGatewayEnvoyFilterExists(ctx, envTest.Client, testNs, secondGatewayName)
		})
	})
})
