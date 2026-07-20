/*
Copyright 2026.

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

	"github.com/onsi/gomega"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
)

func VerifyGatewayPodMonitorExists(ctx context.Context, c client.Client, gatewayNamespace, gatewayName string) {
	VerifyResourceExists(ctx, c, gatewayNamespace, constants.GetGatewayPodMonitorName(gatewayName), &monitoringv1.PodMonitor{})
}

func VerifyGatewayPodMonitorNotExist(ctx context.Context, c client.Client, gatewayNamespace, gatewayName string) {
	VerifyResourceNotExist(ctx, c, gatewayNamespace, constants.GetGatewayPodMonitorName(gatewayName), &monitoringv1.PodMonitor{})
}

func VerifyGatewayPodMonitorOwnerRef(ctx context.Context, c client.Client, gatewayNamespace, gatewayName string) {
	gomega.Eventually(func(g gomega.Gomega) {
		podMonitor, err := GetResourceByName(ctx, c, gatewayNamespace, constants.GetGatewayPodMonitorName(gatewayName), &monitoringv1.PodMonitor{})
		g.Expect(err).NotTo(gomega.HaveOccurred())
		ownerRefs := podMonitor.GetOwnerReferences()
		g.Expect(ownerRefs).To(gomega.HaveLen(1))
		g.Expect(ownerRefs[0].Name).To(gomega.Equal(gatewayName))
		g.Expect(ownerRefs[0].Kind).To(gomega.Equal("Gateway"))
		g.Expect(ownerRefs[0].APIVersion).To(gomega.Equal("gateway.networking.k8s.io/v1"))
	}).WithContext(ctx).Should(gomega.Succeed())
}
