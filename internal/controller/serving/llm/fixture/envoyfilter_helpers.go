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

package fixture

import (
	"context"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"

	"github.com/onsi/gomega"
	istioclientv1alpha3 "istio.io/client-go/pkg/apis/networking/v1alpha3"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func VerifyGatewayEnvoyFilterExists(ctx context.Context, c client.Client, gatewayNamespace, gatewayName string) {
	VerifyResourceExists(ctx, c, gatewayNamespace, constants.GetGatewayEnvoyFilterName(gatewayName), &istioclientv1alpha3.EnvoyFilter{})
}

func VerifyGatewayEnvoyFilterNotExist(ctx context.Context, c client.Client, gatewayNamespace, gatewayName string) {
	VerifyResourceNotExist(ctx, c, gatewayNamespace, constants.GetGatewayEnvoyFilterName(gatewayName), &istioclientv1alpha3.EnvoyFilter{})
}

func VerifyGatewayEnvoyFilterOwnerRef(ctx context.Context, c client.Client, gatewayNamespace, gatewayName string) {
	gomega.Eventually(func() error {
		gatewayEnvoyFilter, err := GetResourceByName(ctx, c, gatewayNamespace, constants.GetGatewayEnvoyFilterName(gatewayName), &istioclientv1alpha3.EnvoyFilter{})
		if err != nil {
			return err
		}
		ownerRefs := gatewayEnvoyFilter.GetOwnerReferences()
		gomega.Expect(ownerRefs).To(gomega.HaveLen(1))
		gomega.Expect(ownerRefs[0].Name).To(gomega.Equal(gatewayName))
		gomega.Expect(ownerRefs[0].Kind).To(gomega.Equal("Gateway"))
		gomega.Expect(ownerRefs[0].APIVersion).To(gomega.Equal("gateway.networking.k8s.io/v1"))
		return nil
	}).WithContext(ctx).Should(gomega.Succeed())
}

func VerifyGatewayEnvoyFilterRestored(ctx context.Context, c client.Client, gatewayNamespace, gatewayName string, expectedTargetRefName string) {
	gomega.Eventually(func() bool {
		restored, err := GetResourceByName(ctx, c, gatewayNamespace, constants.GetGatewayEnvoyFilterName(gatewayName), &istioclientv1alpha3.EnvoyFilter{})
		if err != nil {
			return false
		}
		if len(restored.Spec.TargetRefs) == 0 {
			return false
		}
		return restored.Spec.TargetRefs[0].Name == expectedTargetRefName
	}).WithContext(ctx).Should(gomega.BeTrue())
}
