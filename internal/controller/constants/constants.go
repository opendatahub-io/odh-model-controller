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

import (
	"time"

	"knative.dev/pkg/kmeta"
)

type KServeDeploymentMode string

const (
	InferenceServiceKind             = "InferenceService"
	InferenceServiceODHFinalizerName = "odh.inferenceservice.finalizers"
	InferenceServiceConfigMapName    = "inferenceservice-config"

	KserveNetworkVisibility = "networking.kserve.io/visibility"
	KserveGroupAnnotation   = "serving.kserve.io/inferenceservice"
	RhoaiObservabilityLabel = "monitoring.opendatahub.io/scrape"
	RuntimesBaseAnnotation  = "opendatahub.io"

	ODHManagedLabel         = "opendatahub.io/managed"
	EnableAuthODHAnnotation = "security.opendatahub.io/enable-auth"

	LabelEnableKserveRawRoute = "exposed"

	KserveServiceAccountName = "default"
)

// InferenceService container names
const (
	// WorkerContainerName is for worker node container
	// TO-DO this will be replaced by upstream constants when 0.15 is released
	WorkerContainerName = "worker-container"
)

// InferenceService validation constants
const (
	/* The k8s character limit for names is 63 characters, and we need to account for the
	"-predictor" suffix added to the deployment name */
	MaxISVCLength = 53
)

// isvc modes
var (
	RawDeployment KServeDeploymentMode = "RawDeployment"
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
	ModelRegistryServiceAnnotation       = "routing.opendatahub.io/external-address-rest"
)

// CA bundles
const (
	KServeCACertFileName       = "cabundle.crt"
	KServeCACertConfigMapName  = "odh-kserve-custom-ca-bundle"
	ODHGlobalCertConfigMapName = "odh-trusted-ca-bundle"
	ODHClusterCACertFileName   = "ca-bundle.crt"
	ODHCustomCACertFileName    = "odh-ca-bundle.crt"
	KServeGatewayName          = "kserve-local-gateway"
	ServiceCAConfigMapName     = "openshift-service-ca.crt"
	ServiceCACertFileName      = "service-ca.crt"
)

func CABundleConfigMaps() map[string][]string {
	return map[string][]string{
		ODHGlobalCertConfigMapName: {ODHClusterCACertFileName, ODHCustomCACertFileName},
		ServiceCAConfigMapName:     {ServiceCACertFileName},
	}
}

const (
	KserveMetricsConfigMapNameSuffix = "-metrics-dashboard"
	DefaultStorageConfig             = "storage-config"
	IntervalValue                    = "1m"
	RequestRateInterval              = "5m"
	GPUKVCacheSamplingInterval       = "24h"

	KServeRuntimeAnnotation = RuntimesBaseAnnotation + "/kserve-runtime"
	OvmsRuntimeName         = "ovms"
	TgisRuntimeName         = "tgis"
	VllmRuntimeName         = "vllm"
)

// openshift
const (
	ServingCertAnnotationKey  = "service.beta.openshift.io/serving-cert-secret-name"
	RouteTimeoutAnnotationKey = "haproxy.router.openshift.io/timeout"
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
	NimApplyConfigFieldManager   = "nim-account-controller"
	NimValidationRefreshRate     = time.Hour * 24
	NimConfigRefreshRate         = time.Hour * 24
	NimCleanupFinalizer          = "runtimes.opendatahub.io/nim-cleanup-finalizer"
	NimForceValidationAnnotation = "runtimes.opendatahub.io/nim-force-validation"
)

// Ray
const (
	RayUseTlsEnvName                 = "RAY_USE_TLS"
	RayCASecretName                  = "ray-ca-tls"
	RayTLSSecretName                 = "ray-tls"
	RayTLSGeneratorInitContainerName = "ray-tls-generator"
	RayTLSVolumeName                 = "ray-tls"
	RayTLSSecretVolumeName           = "ray-tls-secret"
	RayTLSVolumeMountPath            = "/etc/ray/tls"
	RayTLSSecretMountPath            = "/etc/ray-secret"
)

// Default timeout value for Openshift routes
const DefaultOpenshiftRouteTimeout int64 = 30

type AuthType string

const (
	UserDefined AuthType = "userdefined"
	Anonymous   AuthType = "anonymous"
)

const (
	AuthorinoLabel          = "AUTHORINO_LABEL"
	AuthPolicyNameSuffix    = "-authn"
	AuthPolicyGroup         = "kuadrant.io"
	AuthPolicyVersion       = "v1"
	AuthPolicyKind          = "AuthPolicy"
	EnvoyFilterKind         = "EnvoyFilter"
	EnvoyFilterNameSuffix   = "-authn-ssl"
	HTTPRouteNameSuffix     = "-kserve-route"
	KubernetesAudience      = "https://kubernetes.default.svc"
	DefaultGatewayName      = "openshift-ai-inference"
	DefaultGatewayNamespace = "openshift-ingress"
)

func GetHTTPRouteAuthPolicyName(httpRouteName string) string {
	return kmeta.ChildName(httpRouteName, AuthPolicyNameSuffix)
}

func GetGatewayAuthPolicyName(gatewayName string) string {
	return kmeta.ChildName(gatewayName, AuthPolicyNameSuffix)
}

func GetGatewayEnvoyFilterName(gatewayName string) string {
	return kmeta.ChildName(gatewayName, EnvoyFilterNameSuffix)
}

func GetHTTPRouteName(llmisvcName string) string {
	return kmeta.ChildName(llmisvcName, HTTPRouteNameSuffix)
}

func GetAuthPolicyGroupVersion() string {
	return AuthPolicyGroup + "/" + AuthPolicyVersion
}
