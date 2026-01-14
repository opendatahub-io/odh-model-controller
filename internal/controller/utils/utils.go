package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"

	ocpconfigv1 "github.com/openshift/api/config/v1"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	"knative.dev/pkg/kmeta"

	"github.com/go-logr/logr"
	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	v1 "github.com/openshift/api/route/v1"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
)

var (
	gvResourcesCache map[string]*metav1.APIResourceList

	_appNamespace *string
)

const (
	KserveConfigMapName = "inferenceservice-config"
)

// GetApplicationNamespace returns the namespace where the application components are installed.
// defaults to: RHOAI - redhat-ods-applications, ODH: opendatahub
func GetApplicationNamespace(ctx context.Context, cli client.Client) (string, error) {
	if _appNamespace != nil {
		return *_appNamespace, nil
	}
	logger := log.FromContext(ctx)

	podNamespace := os.Getenv("POD_NAMESPACE")
	objectList, err := getDSCIObject(ctx, cli)
	if err != nil {
		logger.V(0).Error(err, "Failed to fetch the DSCI object")
		return "", err
	}

	for _, item := range objectList.Items {
		ns, _, _ := unstructured.NestedString(item.Object, "spec", "applicationsNamespace")
		if len(ns) > 0 {
			podNamespace = ns
		}
	}
	_appNamespace = &podNamespace
	return podNamespace, nil
}

func IsNil(i any) bool {
	return reflect.ValueOf(i).IsNil()
}

func IsNotNil(i any) bool {
	return !IsNil(i)
}

// Query the DSCI from the cluster
func getDSCIObject(ctx context.Context, cli client.Client) (*unstructured.UnstructuredList, error) {
	objectList := &unstructured.UnstructuredList{}
	objectList.SetAPIVersion(GVK.DataScienceClusterInitialization.GroupVersion().String())
	objectList.SetKind(GVK.DataScienceClusterInitialization.Kind)

	if err := cli.List(ctx, objectList); err != nil {
		return objectList, fmt.Errorf("not able to read %s: %w", objectList, err)
	}

	return objectList, nil
}

// IsCrdAvailable checks if a given CRD is present in the cluster by verifying the
// existence of its API.
func IsCrdAvailable(config *rest.Config, groupVersion, kind string) (bool, error) {
	gvResources, err := GetAvailableResourcesForApi(config, groupVersion)
	if err != nil {
		return false, err
	}

	found := false
	if gvResources != nil {
		for _, crd := range gvResources.APIResources {
			if crd.Kind == kind {
				found = true
				break
			}
		}
	}

	return found, nil
}

// GetAvailableResourcesForApi returns the list of discovered resources that belong
// to the API specified in groupVersion. The first query to a specifig groupVersion will
// query the cluster API server to discover the available resources and the discovered
// resources will be cached and returned to subsequent invocations to prevent additional
// queries to the API server.
func GetAvailableResourcesForApi(config *rest.Config, groupVersion string) (*metav1.APIResourceList, error) {
	var gvResources *metav1.APIResourceList
	var ok bool

	if gvResources, ok = gvResourcesCache[groupVersion]; !ok {
		discoveryClient, newClientErr := discovery.NewDiscoveryClientForConfig(config)
		if newClientErr != nil {
			return nil, newClientErr
		}

		var getGvResourcesErr error
		gvResources, getGvResourcesErr = discoveryClient.ServerResourcesForGroupVersion(groupVersion)
		if getGvResourcesErr != nil && !apierrs.IsNotFound(getGvResourcesErr) {
			return nil, getGvResourcesErr
		}

		SetAvailableResourcesForApi(groupVersion, gvResources)
	}

	return gvResources, nil
}

// SetAvailableResourcesForApi stores the value fo resources argument in the global cache
// of discovered API resources. This function should never be called directly. It is exported
// for usage in tests.
func SetAvailableResourcesForApi(groupVersion string, resources *metav1.APIResourceList) {
	if gvResourcesCache == nil {
		gvResourcesCache = make(map[string]*metav1.APIResourceList)
	}

	gvResourcesCache[groupVersion] = resources
}

func FindSupportingRuntimeForISvc(ctx context.Context, cli client.Client, log logr.Logger, isvc *kservev1beta1.InferenceService) (*kservev1alpha1.ServingRuntime, error) {
	desiredServingRuntime := &kservev1alpha1.ServingRuntime{}

	if isvc.Spec.Predictor.Model != nil && isvc.Spec.Predictor.Model.Runtime != nil {
		err := cli.Get(ctx, types.NamespacedName{
			Name:      *isvc.Spec.Predictor.Model.Runtime,
			Namespace: isvc.Namespace,
		}, desiredServingRuntime)
		if err != nil {
			if apierrs.IsNotFound(err) {
				return nil, err
			}
		}
		return desiredServingRuntime, nil
	} else {
		runtimes := &kservev1alpha1.ServingRuntimeList{}
		err := cli.List(ctx, runtimes, client.InNamespace(isvc.Namespace))
		if err != nil {
			return nil, err
		}

		// Sort by creation date, to be somewhat deterministic
		sort.Slice(runtimes.Items, func(i, j int) bool {
			// Sorting descending by creation time leads to picking the most recently created runtimes first
			if runtimes.Items[i].CreationTimestamp.Before(&runtimes.Items[j].CreationTimestamp) {
				return false
			}
			if runtimes.Items[i].CreationTimestamp.Equal(&runtimes.Items[j].CreationTimestamp) {
				// For Runtimes created at the same time, use alphabetical order.
				return runtimes.Items[i].Name < runtimes.Items[j].Name
			}
			return true
		})

		for _, runtime := range runtimes.Items {
			if runtime.Spec.Disabled != nil && *runtime.Spec.Disabled {
				continue
			}

			if runtime.Spec.MultiModel != nil && !*runtime.Spec.MultiModel {
				continue
			}

			for _, supportedFormat := range runtime.Spec.SupportedModelFormats {
				if supportedFormat.AutoSelect != nil && *supportedFormat.AutoSelect && supportedFormat.Name == isvc.Spec.Predictor.Model.ModelFormat.Name {
					desiredServingRuntime = &runtime
					log.Info("Automatic runtime selection for InferenceService", "runtime", desiredServingRuntime.Name)
					return desiredServingRuntime, nil
				}
			}
		}

		log.Info("No suitable Runtime available for InferenceService")
		return desiredServingRuntime, errors.New(constants.NoSuitableRuntimeError)
	}
}

func SubstituteVariablesInQueries(data string, namespace string, name string) string {
	replacer := strings.NewReplacer(
		"${NAMESPACE}", namespace,
		"${MODEL_NAME}", name,
		"${RATE_INTERVAL}", constants.IntervalValue,
		"${REQUEST_RATE_INTERVAL}", constants.RequestRateInterval,
		"${KV_CACHE_SAMPLING_RATE}", constants.GPUKVCacheSamplingInterval)
	return replacer.Replace(data)
}

func IsRayTLSSecret(name string) bool {
	return name == constants.RayCASecretName || name == constants.RayTLSSecretName
}

// SetOpenshiftRouteTimeoutForIsvc sets the timeout value for Openshift routes created for inference services.
func SetOpenshiftRouteTimeoutForIsvc(route *v1.Route, isvc *kservev1beta1.InferenceService) {
	// The timeout annotation will always be added to Openshift routes created for inference services.
	if route.Annotations == nil {
		route.Annotations = make(map[string]string)
	}

	// Allow for end users to override the default functionality by manually setting the annotation on the inference service.
	if _, ok := isvc.Annotations[constants.RouteTimeoutAnnotationKey]; ok {
		if route.Annotations == nil {
			route.Annotations = make(map[string]string)
		}
		route.Annotations[constants.RouteTimeoutAnnotationKey] = isvc.Annotations[constants.RouteTimeoutAnnotationKey]
		return
	}

	// By default the timeout will be set to the sum of all component timeouts.
	var timeout int64 = 0
	if isvc.Spec.Predictor.TimeoutSeconds != nil {
		timeout += *isvc.Spec.Predictor.TimeoutSeconds
	} else {
		timeout += constants.DefaultOpenshiftRouteTimeout
	}
	if isvc.Spec.Transformer != nil {
		if isvc.Spec.Transformer.TimeoutSeconds != nil {
			timeout += *isvc.Spec.Transformer.TimeoutSeconds
		} else {
			timeout += constants.DefaultOpenshiftRouteTimeout
		}
	}
	if isvc.Spec.Explainer != nil {
		if isvc.Spec.Explainer.TimeoutSeconds != nil {
			timeout += *isvc.Spec.Explainer.TimeoutSeconds
		} else {
			timeout += constants.DefaultOpenshiftRouteTimeout
		}
	}

	route.Annotations[constants.RouteTimeoutAnnotationKey] = fmt.Sprintf("%ds", timeout)
}

func GetEnvOr(key, defaultValue string) string {
	if env, defined := os.LookupEnv(key); defined {
		return env
	}
	return defaultValue
}

func GetAuthAudience(ctx context.Context, client client.Client, defaultAudience string) []string {
	// 1. Check environment variable first (explicit configuration)
	if aud := os.Getenv("AUTH_AUDIENCE"); aud != "" {
		audiences := strings.Split(aud, ",")
		for i := range audiences {
			audiences[i] = strings.TrimSpace(audiences[i])
		}
		return audiences
	}

	// 2. Discover Authentication cluster object for ROSA (auto-detection)
	authConfig := &ocpconfigv1.Authentication{}
	if err := client.Get(ctx, types.NamespacedName{Name: "cluster"}, authConfig); err == nil {
		if authConfig.Spec.ServiceAccountIssuer != "" {
			return []string{authConfig.Spec.ServiceAccountIssuer}
		}
	}

	// 3. Use default
	return []string{defaultAudience}
}

func GetInferenceServiceConfigMap(ctx context.Context, cli client.Client) (*corev1.ConfigMap, error) {
	controllerNs := os.Getenv("POD_NAMESPACE")
	inferenceServiceConfigMap := &corev1.ConfigMap{}
	err := cli.Get(ctx, client.ObjectKey{
		Namespace: controllerNs,
		Name:      KserveConfigMapName,
	}, inferenceServiceConfigMap)
	if err != nil {
		return nil, fmt.Errorf("error getting configmap 'inferenceservice-config'. %w", err)
	}
	return inferenceServiceConfigMap, nil
}

func GetGatewayInfoFromConfigMap(ctx context.Context, cli client.Client) (namespace, name string, err error) {
	configMap, err := GetInferenceServiceConfigMap(ctx, cli)
	if err != nil {
		return "", "", err
	}

	if ingressData := configMap.Data["ingress"]; ingressData != "" {
		var config map[string]any
		if json.Unmarshal([]byte(ingressData), &config) == nil {
			if gateway, ok := config["kserveIngressGateway"].(string); ok {
				if parts := strings.Split(gateway, "/"); len(parts) == 2 {
					return parts[0], parts[1], nil
				}
			}
		}
	}

	return "", "", fmt.Errorf("failed to parse gateway info from configmap")
}

// MergeUserLabelsAndAnnotations merges user-added labels and annotations from existing resource
// into desired resource while preserving template-defined values
func MergeUserLabelsAndAnnotations(desired, existing client.Object) {
	if existing.GetLabels() != nil {
		if desired.GetLabels() == nil {
			desired.SetLabels(make(map[string]string))
		}
		desiredLabels := desired.GetLabels()
		for k, v := range existing.GetLabels() {
			if _, exists := desiredLabels[k]; !exists {
				desiredLabels[k] = v
			}
		}
		desired.SetLabels(desiredLabels)
	}

	if existing.GetAnnotations() != nil {
		if desired.GetAnnotations() == nil {
			desired.SetAnnotations(make(map[string]string))
		}
		desiredAnnotations := desired.GetAnnotations()
		for k, v := range existing.GetAnnotations() {
			if _, exists := desiredAnnotations[k]; !exists {
				desiredAnnotations[k] = v
			}
		}
		desired.SetAnnotations(desiredAnnotations)
	}
}

// GetMaaSRoleName returns the name of the related Role resource for MaaS RBAC use cases
func GetMaaSRoleName(llmisvc *kservev1alpha1.LLMInferenceService) string {
	return kmeta.ChildName(llmisvc.Name, "-model-post-access")
}

// GetMaaSRoleBindingName returns the name of the related RoleBinding resource for MaaS RBAC use cases
func GetMaaSRoleBindingName(llmisvc *kservev1alpha1.LLMInferenceService) string {
	return kmeta.ChildName(llmisvc.Name, "-model-post-access-tier-binding")
}

func IsManagedByOdhController(obj client.Object) bool {
	if labels := obj.GetLabels(); labels != nil {
		return labels["app.kubernetes.io/managed-by"] == "odh-model-controller"
	}
	return false
}

// IsExplicitlyUnmanaged checks if "opendatahub.io/managed" label is explicitly set to "false"
func IsExplicitlyUnmanaged(obj client.Object) bool {
	if labels := obj.GetLabels(); labels != nil {
		if managedValue, ok := labels["opendatahub.io/managed"]; ok {
			return strings.EqualFold(strings.TrimSpace(managedValue), "false")
		}
	}
	return false
}

// IsAuthorinoTLSBootstrapEnabled checks if the gateway has the authorino-tls-bootstrap annotation set to "true".
// This allows EnvoyFilter creation for Authorino TLS even when the gateway is explicitly unmanaged.
func IsAuthorinoTLSBootstrapEnabled(obj client.Object) bool {
	if obj == nil {
		return false
	}
	if annotations := obj.GetAnnotations(); annotations != nil {
		if value, ok := annotations[constants.AuthorinoTLSBootstrapAnnotation]; ok {
			return strings.EqualFold(strings.TrimSpace(value), "true")
		}
	}
	return false
}

// IsManagedByOpenDataHub checks if a resource should be managed by checking "opendatahub.io/managed" label (true/false) or falling back to IsManagedByOdhController
func IsManagedByOpenDataHub(obj client.Object) bool {
	if IsExplicitlyUnmanaged(obj) {
		return false
	}
	return IsManagedByOdhController(obj)
}

// IsManagedResource checks if a resource is managed by verifying both the managed-by label and owner reference
func IsManagedResource(owner client.Object, resource client.Object) bool {
	if !IsManagedByOdhController(resource) {
		return false
	}

	for _, ownerRef := range resource.GetOwnerReferences() {
		if ownerRef.APIVersion == owner.GetObjectKind().GroupVersionKind().GroupVersion().String() &&
			ownerRef.Kind == owner.GetObjectKind().GroupVersionKind().Kind &&
			ownerRef.Name == owner.GetName() &&
			ownerRef.UID == owner.GetUID() &&
			ownerRef.Controller != nil && *ownerRef.Controller {
			return true
		}
	}

	return false
}

// ValidateInferenceServiceNameLength validates that the InferenceService name does not exceed the Kubernetes resource name limit
// when combined with the "-predictor" suffix.
func ValidateInferenceServiceNameLength(isvc *kservev1beta1.InferenceService) error {
	isvcName := isvc.GetName()
	if len(isvcName) > constants.MaxISVCLength {
		return apierrs.NewInvalid(
			schema.GroupKind{Group: isvc.GroupVersionKind().Group, Kind: isvc.Kind},
			isvcName,
			field.ErrorList{
				field.Invalid(
					field.NewPath("metadata").Child("name"),
					isvcName,
					fmt.Sprintf("InferenceService name is too long. The name exceeds the maximum length of %d characters (current length: %d)", constants.MaxISVCLength, len(isvcName)),
				),
			})
	}
	return nil
}

// ShouldCreateEnvoyFilterForGateway returns true if EnvoyFilter should be created for the gateway.
// This is true when the gateway is managed OR has the authorino-tls-bootstrap opt-in annotation.
func ShouldCreateEnvoyFilterForGateway(gateway *gatewayapiv1.Gateway) bool {
	return !IsExplicitlyUnmanaged(gateway) || IsAuthorinoTLSBootstrapEnabled(gateway)
}
