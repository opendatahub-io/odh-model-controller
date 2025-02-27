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

package constants

type KServeDeploymentMode string

const (
	InferenceServiceKind             = "InferenceService"
	InferenceServiceODHFinalizerName = "odh.inferenceservice.finalizers"

	ServiceMeshMemberRollName        = "default"
	ServiceMeshMemberName            = "default"
	IstioIngressService              = "istio-ingressgateway"
	IstioIngressServiceHTTPPortName  = "http2"
	IstioIngressServiceHTTPSPortName = "https"
	IstioSidecarInjectAnnotationName = "sidecar.istio.io/inject"
	KserveNetworkVisibility          = "networking.kserve.io/visibility"
	KserveGroupAnnotation            = "serving.kserve.io/inferenceservice"

	EnableAuthODHAnnotation   = "security.opendatahub.io/enable-auth"
	LabelAuthGroup            = "security.opendatahub.io/authorization-group"
	LabelEnableAuth           = "enable-auth"
	LabelEnableRoute          = "enable-route"
	LabelEnableKserveRawRoute = "exposed"

	CapabilityServiceMeshAuthorization = "CapabilityServiceMeshAuthorization"

	ModelMeshServiceAccountName = "modelmesh-serving-sa"
	KserveServiceAccountName    = "default"
)

// InferenceService container names
const (
	// TO-DO this will be replaced by upstream constants when 0.15 is released
	// WorkerContainerName is for worker node container
	WorkerContainerName = "worker-container"
)

// isvc modes
var (
	Serverless    KServeDeploymentMode = "Serverless"
	RawDeployment KServeDeploymentMode = "RawDeployment"
	ModelMesh     KServeDeploymentMode = "ModelMesh"
)

// model registry
const (
	MLMDAddressEnv                       = "MLMD_ADDRESS"
	ModelRegistryNamespaceLabel          = "modelregistry.opendatahub.io/namespace"
	ModelRegistryNameLabel               = "modelregistry.opendatahub.io/name"
	ModelRegistryUrlAnnotation           = "modelregistry.opendatahub.io/url"
	ModelRegistryInferenceServiceIdLabel = "modelregistry.opendatahub.io/inference-service-id"
	ModelRegistryModelVersionIdLabel     = "modelregistry.opendatahub.io/model-version-id"
	ModelRegistryRegisteredModelIdLabel  = "modelregistry.opendatahub.io/registered-model-id"
	ModelRegistryFinalizer               = "modelregistry.opendatahub.io/finalizer"
)

const (
	KServeCACertFileName       = "cabundle.crt"
	KServeCACertConfigMapName  = "odh-kserve-custom-ca-bundle"
	ODHGlobalCertConfigMapName = "odh-trusted-ca-bundle"
	ODHCustomCACertFileName    = "odh-ca-bundle.crt"
	KServeGatewayName          = "kserve-local-gateway"
)

const (
	KserveMetricsConfigMapNameSuffix = "-metrics-dashboard"
	DefaultStorageConfig             = "storage-config"
	IntervalValue                    = "1m"
	RequestRateInterval              = "5m"
	GPUKVCacheSamplingInterval       = "24h"
	OvmsImageName                    = "openvino_model_server"
	TgisImageName                    = "text-generation-inference"
	VllmImageName                    = "vllm"
	CaikitImageName                  = "caikit-nlp"
	ServingRuntimeFallBackImageName  = "unsupported"
)

// openshift
const (
	ServingCertAnnotationKey = "service.beta.openshift.io/serving-cert-secret-name"
)

// Events
const (
	// AuthUnavailable is logged in an Event when an InferenceGraph is configured to
	// be protected with auth, but Authorino is not configured.
	AuthUnavailable = "AuthStackUnavailable"
)

// errors
const (
	NoSuitableRuntimeError = "not found error: no suitable runtime found."
)

// NIM
const (
	NimApplyConfigFieldManager = "nim-account-controller"
)

// Ray
const (
	RayUseTlsEnvName                 = "RAY_USE_TLS"
	RayCASecretName                  = "ray-ca-tls"
	RayTLSSecretName                 = "ray-tls"
	RayTLSGeneratorInitContainerName = "ray-tls-generator"
)
