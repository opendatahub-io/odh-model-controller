/*

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

package utils

import (
	"context"

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// GetGatewaysForLLMIsvc returns gateways referenced by the LLMInferenceService.
// If explicit refs are provided, returns those gateways. Otherwise falls back to the default gateway.
func GetGatewaysForLLMIsvc(ctx context.Context, c client.Client, llmisvc *kservev1alpha1.LLMInferenceService) []*gatewayapiv1.Gateway {
	logger := log.FromContext(ctx)
	var gateways []*gatewayapiv1.Gateway

	hasExplicitRefs := llmisvc.Spec.Router != nil &&
		llmisvc.Spec.Router.Gateway.HasRefs()

	if hasExplicitRefs {
		for _, ref := range llmisvc.Spec.Router.Gateway.Refs {
			gateway := &gatewayapiv1.Gateway{}
			if err := GetResource(ctx, c, string(ref.Namespace), string(ref.Name), gateway); err == nil {
				gateways = append(gateways, gateway)
			} else {
				logger.V(1).Info("Failed to get gateway", "namespace", ref.Namespace, "name", ref.Name, "error", err)
			}
		}
		return gateways
	}

	// Fall back to default gateway
	var ns, name string
	if userDefinedGatewayNS, userDefinedGatewayName, err := GetGatewayInfoFromConfigMap(ctx, c); err == nil {
		ns = userDefinedGatewayNS
		name = userDefinedGatewayName
	} else {
		logger.V(1).Info("Using default gateway values due to ConfigMap parsing failure",
			"error", err,
			"defaultNamespace", constants.DefaultGatewayNamespace,
			"defaultName", constants.DefaultGatewayName)
		ns = constants.DefaultGatewayNamespace
		name = constants.DefaultGatewayName
	}

	gateway := &gatewayapiv1.Gateway{}
	if err := GetResource(ctx, c, ns, name, gateway); err == nil {
		gateways = append(gateways, gateway)
	} else {
		logger.V(1).Info("Failed to get default gateway", "namespace", ns, "name", name, "error", err)
	}

	return gateways
}

// LLMIsvcUsesGateway checks if an LLMInferenceService uses the specified gateway.
// Returns true if the service explicitly references the gateway, or if the service
// uses the default gateway and the specified gateway matches the default.
func LLMIsvcUsesGateway(ctx context.Context, c client.Client, llmisvc *kservev1alpha1.LLMInferenceService, gatewayNamespace, gatewayName string) bool {
	// Check if service explicitly references gateways
	if llmisvc.Spec.Router != nil && llmisvc.Spec.Router.Gateway != nil && llmisvc.Spec.Router.Gateway.HasRefs() {
		for _, ref := range llmisvc.Spec.Router.Gateway.Refs {
			if string(ref.Name) == gatewayName && string(ref.Namespace) == gatewayNamespace {
				return true
			}
		}
		return false
	}

	// Service uses default gateway - check if specified gateway is the default
	defaultGatewayNamespace, defaultGatewayName, err := GetGatewayInfoFromConfigMap(ctx, c)
	if err != nil {
		defaultGatewayNamespace = constants.DefaultGatewayNamespace
		defaultGatewayName = constants.DefaultGatewayName
	}

	return gatewayNamespace == defaultGatewayNamespace && gatewayName == defaultGatewayName
}
