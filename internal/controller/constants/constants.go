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

type IsvcDeploymentMode string

const (
	InferenceServiceKind = "InferenceService"

	ServiceMeshMemberRollName        = "default"
	ServiceMeshMemberName            = "default"
	IstioIngressService              = "istio-ingressgateway"
	IstioIngressServiceHTTPPortName  = "http2"
	IstioIngressServiceHTTPSPortName = "https"
	IstioSidecarInjectAnnotationName = "sidecar.istio.io/inject"
	KserveNetworkVisibility          = "networking.kserve.io/visibility"
	KserveGroupAnnotation            = "serving.kserve.io/inferenceservice"

	LabelAuthGroup            = "security.opendatahub.io/authorization-group"
	LabelEnableAuthODH        = "security.opendatahub.io/enable-auth"
	LabelEnableAuth           = "enable-auth"
	LabelEnableRoute          = "enable-route"
	LabelEnableKserveRawRoute = "exposed"

	CapabilityServiceMeshAuthorization = "CapabilityServiceMeshAuthorization"

	ModelMeshServiceAccountName = "modelmesh-serving-sa"
	KserveServiceAccountName    = "default"
)

// isvc modes
var (
	Serverless    IsvcDeploymentMode = "Serverless"
	RawDeployment IsvcDeploymentMode = "RawDeployment"
	ModelMesh     IsvcDeploymentMode = "ModelMesh"
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

// errors
const (
	NoSuitableRuntimeError = "not found error: no suitable runtime found."
)

// NIM
const (
	NimApplyConfigFieldManager = "nim-account-controller"
	NimDataConfigMapName       = "nvidia-nim-images-data"
	NimRuntimeTemplateName     = "nvidia-nim-serving-template"
	NimPullSecretName          = "nvidia-nim-image-pull"
)