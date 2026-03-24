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

	kuadrantv1 "github.com/kuadrant/kuadrant-operator/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	istioclientv1alpha3 "istio.io/client-go/pkg/apis/networking/v1alpha3"
	"k8s.io/apimachinery/pkg/types"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/serving/llm/fixture"
	pkgtest "github.com/opendatahub-io/odh-model-controller/internal/controller/testing"
	testutils "github.com/opendatahub-io/odh-model-controller/test/utils"
)

var _ = Describe("Gateway Controller", func() {
	var testNs string
	BeforeEach(func() {
		ctx := context.Background()
		testNamespace := testutils.Namespaces.Create(ctx, envTest.Client)
		testNs = testNamespace.Name
	})

	// Helper to create a managed gateway with standard options
	createGateway := func(ctx context.Context, name string, opts ...fixture.GatewayOption) *gatewayapiv1.Gateway {
		gw := fixture.ManagedGateway(name, testNs, GatewayClassName, opts...)
		ExpectWithOffset(1, envTest.Client.Create(ctx, gw)).Should(Succeed())
		return gw
	}

	Context("Gateway with opendatahub.io/managed=true label", func() {
		It("should create EnvoyFilter and AuthPolicy when managed Gateway is created", func(ctx SpecContext) {
			gatewayName := pkgtest.GenerateUniqueTestName("managed-gateway")
			createGateway(ctx, gatewayName)

			fixture.VerifyGatewayEnvoyFilterExists(ctx, envTest.Client, testNs, gatewayName)
			fixture.VerifyGatewayAuthPolicyExists(ctx, envTest.Client, testNs, gatewayName)
		})

		It("should have Gateway as owner reference for EnvoyFilter and AuthPolicy", func(ctx SpecContext) {
			gatewayName := pkgtest.GenerateUniqueTestName("managed-gateway")
			createGateway(ctx, gatewayName)

			fixture.VerifyGatewayEnvoyFilterOwnerRef(ctx, envTest.Client, testNs, gatewayName)
			fixture.VerifyGatewayAuthPolicyOwnerRef(ctx, envTest.Client, testNs, gatewayName)
		})
	})

	Context("Gateway with opendatahub.io/managed=false label", func() {
		It("should NOT create EnvoyFilter or AuthPolicy for unmanaged gateway without bootstrap annotation", func(ctx SpecContext) {
			gatewayName := pkgtest.GenerateUniqueTestName("unmanaged-gateway")
			createGateway(ctx, gatewayName, fixture.WithUnmanagedLabel())

			fixture.VerifyGatewayEnvoyFilterNotExist(ctx, envTest.Client, testNs, gatewayName)
			fixture.VerifyGatewayAuthPolicyNotExist(ctx, envTest.Client, testNs, gatewayName)
		})
	})

	Context("Gateway with authorino-tls-bootstrap annotation", func() {
		It("should create EnvoyFilter but NOT AuthPolicy for unmanaged gateway with bootstrap annotation", func(ctx SpecContext) {
			gatewayName := pkgtest.GenerateUniqueTestName("bootstrap-gateway")
			createGateway(ctx, gatewayName,
				fixture.WithUnmanagedLabel(),
				fixture.WithAuthorinoTLSBootstrapAnnotation("true"),
			)

			fixture.VerifyGatewayEnvoyFilterExists(ctx, envTest.Client, testNs, gatewayName)
			fixture.VerifyGatewayAuthPolicyNotExist(ctx, envTest.Client, testNs, gatewayName)
		})

		It("should delete EnvoyFilter when bootstrap annotation is removed from unmanaged gateway", func(ctx SpecContext) {
			gatewayName := pkgtest.GenerateUniqueTestName("bootstrap-gateway")
			createGateway(ctx, gatewayName,
				fixture.WithUnmanagedLabel(),
				fixture.WithAuthorinoTLSBootstrapAnnotation("true"),
			)

			fixture.VerifyGatewayEnvoyFilterExists(ctx, envTest.Client, testNs, gatewayName)

			// Remove the bootstrap annotation
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

	Context("Gateway controller works independently of LLMInferenceService", func() {
		It("should create resources for Gateway without any LLMInferenceService existing", func(ctx SpecContext) {
			// This test verifies the main bug fix - resources should be created
			// when Gateway exists, even if no LLMInferenceService exists
			gatewayName := pkgtest.GenerateUniqueTestName("independent-gateway")
			createGateway(ctx, gatewayName)

			fixture.VerifyGatewayEnvoyFilterExists(ctx, envTest.Client, testNs, gatewayName)
			fixture.VerifyGatewayAuthPolicyExists(ctx, envTest.Client, testNs, gatewayName)
		})
	})

	Context("Platform gateway (owned by GatewayConfig)", func() {
		It("should NOT create EnvoyFilter or AuthPolicy for platform gateway without bootstrap annotation", func(ctx SpecContext) {
			gatewayName := pkgtest.GenerateUniqueTestName("platform-gateway")
			createGateway(ctx, gatewayName,
				fixture.WithControllerOwnerRef("GatewayConfig", "default-gateway", "services.platform.opendatahub.io/v1alpha1"),
			)

			fixture.VerifyGatewayEnvoyFilterNotExist(ctx, envTest.Client, testNs, gatewayName)
			fixture.VerifyGatewayAuthPolicyNotExist(ctx, envTest.Client, testNs, gatewayName)
		})

		It("should create EnvoyFilter but NOT AuthPolicy for platform gateway with bootstrap annotation", func(ctx SpecContext) {
			gatewayName := pkgtest.GenerateUniqueTestName("platform-gateway")
			createGateway(ctx, gatewayName,
				fixture.WithControllerOwnerRef("GatewayConfig", "default-gateway", "services.platform.opendatahub.io/v1alpha1"),
				fixture.WithAuthorinoTLSBootstrapAnnotation("true"),
			)

			fixture.VerifyGatewayEnvoyFilterExists(ctx, envTest.Client, testNs, gatewayName)
			fixture.VerifyGatewayAuthPolicyNotExist(ctx, envTest.Client, testNs, gatewayName)
		})
	})

	Context("Resource restoration", func() {
		It("should recreate AuthPolicy when deleted", func(ctx SpecContext) {
			gatewayName := pkgtest.GenerateUniqueTestName("managed-gateway")
			createGateway(ctx, gatewayName)

			authPolicy := fixture.WaitForResource(ctx, envTest.Client, testNs, constants.GetAuthPolicyName(gatewayName), &kuadrantv1.AuthPolicy{})
			Expect(envTest.Client.Delete(ctx, authPolicy)).Should(Succeed())

			fixture.VerifyGatewayAuthPolicyExists(ctx, envTest.Client, testNs, gatewayName)
		})

		It("should recreate EnvoyFilter when deleted", func(ctx SpecContext) {
			gatewayName := pkgtest.GenerateUniqueTestName("managed-gateway")
			createGateway(ctx, gatewayName)

			envoyFilter := fixture.WaitForResource(ctx, envTest.Client, testNs, constants.GetGatewayEnvoyFilterName(gatewayName), &istioclientv1alpha3.EnvoyFilter{})
			Expect(envTest.Client.Delete(ctx, envoyFilter)).Should(Succeed())

			fixture.VerifyGatewayEnvoyFilterExists(ctx, envTest.Client, testNs, gatewayName)
		})

		It("should restore AuthPolicy when modified", func(ctx SpecContext) {
			gatewayName := pkgtest.GenerateUniqueTestName("managed-gateway")
			createGateway(ctx, gatewayName)

			authPolicy := fixture.WaitForResource(ctx, envTest.Client, testNs, constants.GetAuthPolicyName(gatewayName), &kuadrantv1.AuthPolicy{})
			originalTargetRefName := authPolicy.Spec.TargetRef.Name

			authPolicy.Spec.TargetRef.Name = "modified-gateway" //nolint:goconst // test value
			Expect(envTest.Client.Update(ctx, authPolicy)).Should(Succeed())

			fixture.VerifyGatewayAuthPolicyRestored(ctx, envTest.Client, testNs, gatewayName, originalTargetRefName)
		})

		It("should restore EnvoyFilter when modified", func(ctx SpecContext) {
			gatewayName := pkgtest.GenerateUniqueTestName("managed-gateway")
			createGateway(ctx, gatewayName)

			envoyFilter := fixture.WaitForResource(ctx, envTest.Client, testNs, constants.GetGatewayEnvoyFilterName(gatewayName), &istioclientv1alpha3.EnvoyFilter{})
			originalTargetRefName := envoyFilter.Spec.TargetRefs[0].Name

			envoyFilter.Spec.TargetRefs[0].Name = "modified-gateway" //nolint:goconst // test value
			Expect(envTest.Client.Update(ctx, envoyFilter)).Should(Succeed())

			fixture.VerifyGatewayEnvoyFilterRestored(ctx, envTest.Client, testNs, gatewayName, originalTargetRefName)
		})
	})

	Context("Gateway label changes", func() {
		It("should delete resources when Gateway is changed from managed to unmanaged", func(ctx SpecContext) {
			gatewayName := pkgtest.GenerateUniqueTestName("managed-gateway")
			createGateway(ctx, gatewayName)

			fixture.VerifyGatewayEnvoyFilterExists(ctx, envTest.Client, testNs, gatewayName)
			fixture.VerifyGatewayAuthPolicyExists(ctx, envTest.Client, testNs, gatewayName)

			// Change gateway to unmanaged
			Eventually(func() error {
				gw := &gatewayapiv1.Gateway{}
				if err := envTest.Client.Get(ctx, types.NamespacedName{Name: gatewayName, Namespace: testNs}, gw); err != nil {
					return err
				}
				if gw.Labels == nil {
					gw.Labels = make(map[string]string)
				}
				gw.Labels[constants.ODHManagedLabel] = "false" //nolint:goconst // standard label value
				return envTest.Client.Update(ctx, gw)
			}).WithContext(ctx).Should(Succeed())

			fixture.VerifyGatewayEnvoyFilterNotExist(ctx, envTest.Client, testNs, gatewayName)
			fixture.VerifyGatewayAuthPolicyNotExist(ctx, envTest.Client, testNs, gatewayName)
		})
	})
})
