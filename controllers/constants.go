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

package controllers

const (
	// EnableServiceMeshLabel is the Kubernetes label that must be set to "true" in a namespace to enable Service Mesh features.
	EnableServiceMeshLabel = "opendatahub.io/service-mesh"

	// InferenceServiceFinalizerName is the name of the finalizer that is added to InferenceService CRs to do proper clean-up
	// of objects created by the controller. For now, this is for ensuring that Istio VirtualServices for traffic splitting
	// are properly removed when no longer needed.
	InferenceServiceFinalizerName = "serving.opendatahub.io/finalizer"

	// InferenceServiceModelTagLabel is the Kubernetes label or annotation set on resources to record the tag grouping
	// together a set of InferenceServices. At the moment, this is being set by the end-user in InferenceService CRs to specify
	// the group name of several of ISVCs, and set by the controller on VirtualServices as a cross-reference for proper
	// clean-up of resources when the group vanishes.
	InferenceServiceModelTagLabel = "serving.kserve.io/model-tag"

	// InferenceServiceSplitPercentAnnotation is the Kubernetes annotation set by the end-user on InferenceService resources
	// to configure the percentage of traffic that should go to the InferenceService when part of a group.
	InferenceServiceSplitPercentAnnotation = "serving.kserve.io/canaryTrafficPercent"

	// IstioGatewayNameAnnotation is the Kubernetes annotation key set by the end-user on Namespaces
	// to specify the name (wihtout namespace) of the Istio Gateway resource to use when publicly exposing models/ISVCs.
	// This annotation may be set automatically by the `odh-project-controller`.
	IstioGatewayNameAnnotation = "maistra.io/gateway-name"

	// IstioGatewayNamespaceAnnotation is the Kubernetes annotation key set by the end-user on Namespaces
	// to specify the Namespace of the Istio Gateway resource to use when publicly exposing models/ISVCs.
	// This annotation may be set automatically by the `odh-project-controller`.
	IstioGatewayNamespaceAnnotation = "maistra.io/gateway-namespace"

	// VirtualServiceForTrafficSplitAnnotation is the Kubernetes annotation set by the controller on InferenceService
	// resources to record the VirtualService name that is related to an InferenceService group (model-tag) and was
	// created to control traffic splitting.
	VirtualServiceForTrafficSplitAnnotation = "serving.opendatahub.io/vs-traffic-splitting"
)
