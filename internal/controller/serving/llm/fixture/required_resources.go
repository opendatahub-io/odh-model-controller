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

	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/serving/reconcilers"
	pkgtest "github.com/opendatahub-io/odh-model-controller/internal/controller/testing"
)

// RequiredResources creates all the required resources for LLM inference service testing
func RequiredResources(ctx context.Context, c client.Client, ns string) {
	pkgtest.CreateNamespaceIfNotExists(ctx, c, ns)
	gomega.Expect(c.Create(ctx, InferenceServiceCfgMap(ns))).To(gomega.Succeed())

	// Setup global resources (GatewayClass and Gateway namespace and Gateway itself)
	gomega.Expect(c.Create(ctx, defaultGatewayClass())).To(gomega.Succeed())
	pkgtest.CreateNamespaceIfNotExists(ctx, c, constants.DefaultGatewayNamespace)

	// Create Gateway and ensure it's actually created and ready
	gomega.Eventually(func() error {
		return c.Create(ctx, defaultGateway())
	}).Should(gomega.Succeed())

	// Create tier configuration namespace and ConfigMap for MaaS tier testing
	pkgtest.CreateNamespaceIfNotExists(ctx, c, reconcilers.DefaultTenantNamespace)
	gomega.Expect(c.Create(ctx, tierConfigMap())).To(gomega.Succeed())
}

func InferenceServiceCfgMap(ns string) *corev1.ConfigMap {
	configs := map[string]string{
		"ingress": `{
				"enableGatewayApi": true,
				"kserveIngressGateway": "openshift-ingress/openshift-ai-inference",
				"ingressGateway": "knative-serving/knative-ingress-gateway",
				"localGateway": "knative-serving/knative-local-gateway",
				"localGatewayService": "knative-local-gateway.istio-system.svc.cluster.local",
				"additionalIngressDomains": ["additional.example.com"]
			}`,
		"storageInitializer": `{
				"memoryRequest": "100Mi",
				"memoryLimit": "1Gi",
				"cpuRequest": "100m",
				"cpuLimit": "1",
				"cpuModelcar": "10m",
				"memoryModelcar": "15Mi",
				"enableModelcar": true
			}`,
	}
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      constants.InferenceServiceConfigMapName,
			Namespace: ns,
		},
		Data: configs,
	}

	return configMap
}

func defaultGatewayClass() *gatewayapiv1.GatewayClass {
	return &gatewayapiv1.GatewayClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: "openshift-default",
		},
		Spec: gatewayapiv1.GatewayClassSpec{
			ControllerName: "openshift.io/gateway-controller/v1",
		},
	}
}

// defaultGateway creates a test Gateway resource
func defaultGateway() *gatewayapiv1.Gateway {
	return &gatewayapiv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Name:      constants.DefaultGatewayName,
			Namespace: constants.DefaultGatewayNamespace,
		},
		Spec: gatewayapiv1.GatewaySpec{
			GatewayClassName: "openshift-default",
			Listeners: []gatewayapiv1.Listener{
				{
					Name:     "http",
					Port:     80,
					Protocol: gatewayapiv1.HTTPProtocolType,
				},
			},
		},
	}
}

// tierConfigMap creates a ConfigMap with tier configuration for MaaS testing
func tierConfigMap() *corev1.ConfigMap {
	tiersYAML := `
- name: free
  description: Free tier for testing
  level: 1
  groups:
  - system:authenticated
- name: premium
  description: Premium tier for testing
  level: 10
  groups:
  - premium-users
- name: enterprise
  description: Enterprise tier for testing
  level: 20
  groups:
  - enterprise-users
`
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      reconcilers.TierConfigMapName,
			Namespace: reconcilers.DefaultTenantNamespace,
		},
		Data: map[string]string{
			"tiers": tiersYAML,
		},
	}
}

// TierConfigMap creates a ConfigMap with tier configuration for testing.
// This is exported for use in integration tests.
func TierConfigMap(namespace string, tiers string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      reconcilers.TierConfigMapName,
			Namespace: namespace,
		},
		Data: map[string]string{
			"tiers": tiers,
		},
	}
}
