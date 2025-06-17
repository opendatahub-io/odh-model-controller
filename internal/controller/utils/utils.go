package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"

	"github.com/go-logr/logr"
	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"github.com/kuadrant/authorino/pkg/log"
	operatorv1 "github.com/openshift/api/operator/v1"
	v1 "github.com/openshift/api/route/v1"
	"github.com/pkg/errors"
	v1beta12 "istio.io/api/security/v1beta1"
	"istio.io/client-go/pkg/apis/security/v1beta1"
	corev1 "k8s.io/api/core/v1"
	apierr "k8s.io/apimachinery/pkg/api/errors"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
)

var (
	gvResourcesCache map[string]*metav1.APIResourceList

	_appNamespace *string
)

const (
	KServeDeploymentModeAnnotation = "serving.kserve.io/deploymentMode"
	KserveConfigMapName            = "inferenceservice-config"
	KServeWithServiceMeshComponent = "kserve-service-mesh"
)

func GetDeploymentModeForKServeResource(ctx context.Context, cli client.Client, annotations map[string]string) (constants.KServeDeploymentMode, error) {

	// If explicitly sets deployment mode using an annotation, return bool depending on value
	value, exists := annotations[KServeDeploymentModeAnnotation]
	if exists {
		switch value {
		case string(constants.ModelMesh):
			return constants.ModelMesh, nil
		case string(constants.Serverless):
			return constants.Serverless, nil
		case string(constants.RawDeployment):
			return constants.RawDeployment, nil
		default:
			return "", fmt.Errorf("the deployment mode '%s' of the KServe resource is invalid", value)
		}
	} else {
		// There is no explicit deployment mode using an annotation, determine the default from configmap
		controllerNs := os.Getenv("POD_NAMESPACE")
		inferenceServiceConfigMap := &corev1.ConfigMap{}
		err := cli.Get(ctx, client.ObjectKey{
			Namespace: controllerNs,
			Name:      KserveConfigMapName,
		}, inferenceServiceConfigMap)
		if err != nil {
			return "", fmt.Errorf("error getting configmap 'inferenceservice-config'. %w", err)
		}
		var deployData map[string]interface{}
		if err = json.Unmarshal([]byte(inferenceServiceConfigMap.Data["deploy"]), &deployData); err != nil {
			return "", fmt.Errorf("error retrieving value for key 'deploy' from configmap %s. %w", KserveConfigMapName, err)
		}
		defaultDeploymentMode := deployData["defaultDeploymentMode"]
		switch defaultDeploymentMode {
		case string(constants.ModelMesh):
			return constants.ModelMesh, nil
		case string(constants.Serverless):
			return constants.Serverless, nil
		case string(constants.RawDeployment):
			return constants.RawDeployment, nil
		default:
			return "", fmt.Errorf("the deployment mode '%s' of the Inference Service is invalid", defaultDeploymentMode)
		}
	}
}

// VerifyIfComponentIsEnabled will query the DCS in the cluster and see if the desired componentName is enabled
func VerifyIfComponentIsEnabled(ctx context.Context, cli client.Client, componentName string) (bool, error) {
	// Query the custom object
	objectList := &unstructured.UnstructuredList{}
	objectList.SetAPIVersion(GVK.DataScienceCluster.GroupVersion().String())
	objectList.SetKind(GVK.DataScienceCluster.Kind)

	if err := cli.List(ctx, objectList); err != nil {
		return false, fmt.Errorf("not able to read %s: %w", objectList, err)
	}

	// there must be only one dsc
	if len(objectList.Items) == 1 {
		fields := []string{"spec", "components", componentName, "managementState"}
		if componentName == KServeWithServiceMeshComponent {
			// For KServe, Authorino is required when serving is enabled
			// By Disabling ServiceMesh for RawDeployment, it should reflect on disabling
			// the Authorino integration as well.
			fields = []string{"spec", "components", "kserve", "serving", "managementState"}
			kserveFields := []string{"spec", "components", "kserve", "managementState"}

			serving, _, errServing := unstructured.NestedString(objectList.Items[0].Object, fields...)
			if errServing != nil {
				return false, fmt.Errorf("failed to retrieve the component [%s] status from %+v. %w",
					componentName, objectList.Items[0], errServing)
			}

			kserve, _, errKserve := unstructured.NestedString(objectList.Items[0].Object, kserveFields...)
			if errKserve != nil {
				return false, fmt.Errorf("failed to retrieve the component [%s] status from %+v. %w",
					componentName, objectList.Items[0], errKserve)
			}

			return (serving == string(operatorv1.Managed) || serving == string(operatorv1.Unmanaged)) && kserve == string(operatorv1.Managed), nil
		}

		val, _, err := unstructured.NestedString(objectList.Items[0].Object, fields...)
		if err != nil {
			return false, fmt.Errorf("failed to retrieve the component [%s] status from %+v",
				componentName, objectList.Items[0])
		}
		return val == string(operatorv1.Managed), nil
	} else {
		return false, fmt.Errorf("there is no %s available in the cluster", GVK.DataScienceCluster.Kind)
	}
}

// AuthorinoEnabledWhenOperatorNotMissing is a helper function to check if Authorino is enabled when the Operator is not missing.
// This is defined by Condition.Reason=MissingOperator. In any other case, it is assumed that Authorino is enabled but DSC might not be
// in the Ready state and will reconcile.
func AuthorinoEnabledWhenOperatorNotMissing(_, reason string) bool {
	return reason != "MissingOperator"
}

// VerifyIfCapabilityIsEnabled checks if given DSCI capability is enabled. It only fails if client call to fetch DSCI fails.
// In other cases it assumes capability is not enabled.
func VerifyIfCapabilityIsEnabled(ctx context.Context, cli client.Client, capabilityName string, enabledWhen func(status, reason string) bool) (bool, error) {
	objectList, err := getDSCIObject(ctx, cli)

	if err != nil {
		return false, fmt.Errorf("not able to read %s: %w", objectList, err)
	}

	for _, item := range objectList.Items {
		statusField, found, err := unstructured.NestedFieldNoCopy(item.Object, "status", "conditions")
		if err != nil || !found {
			return false, nil
		}

		conditions, ok := statusField.([]interface{})
		if !ok {
			continue
		}

		for _, condition := range conditions {
			condMap, ok := condition.(map[string]interface{})
			if !ok {
				continue
			}

			if condType, ok := condMap["type"].(string); ok && condType == capabilityName {
				status, _ := condMap["status"].(string)
				reason, _ := condMap["reason"].(string)
				return enabledWhen(status, reason), nil
			}
		}
	}

	return false, nil
}

// GetIstioControlPlaneName return the name of the Istio Control Plane and the mesh namespace.
// It will first try to read the environment variables, if not found will then try to read the DSCI
// If the required value is not available in the DSCI, it will return the default values
func GetIstioControlPlaneName(ctx context.Context, cli client.Client) (istioControlPlane string, meshNamespace string) {
	// first try to retrieve it from the envs, it should be available through the service-mesh-refs ConfigMap
	istioControlPlane = os.Getenv("CONTROL_PLANE_NAME")
	meshNamespace = os.Getenv("MESH_NAMESPACE")

	if len(istioControlPlane) == 0 || len(meshNamespace) == 0 {
		log.V(1).Info("Trying to read Istio Control Plane name and namespace from DSCI")
		objectList, err := getDSCIObject(ctx, cli)
		if err != nil {
			log.V(0).Error(err, "Failed to fetch the DSCI object")
			panic("error reading Istio Control Plane name and namespace from DSCI.")
		}
		for _, item := range objectList.Items {
			if len(istioControlPlane) == 0 {
				name, _, _ := unstructured.NestedString(item.Object, "spec", "serviceMesh", "controlPlane", "name")
				if len(name) > 0 {
					istioControlPlane = name
				} else {
					log.V(1).Info("Istio Control Plane name is not set in DSCI")
					// at this point, it is not set anywhere, lets return an error
					panic("error setting Istio Control Plane in DSCI.")
				}
			}

			if len(meshNamespace) == 0 {
				namespace, _, _ := unstructured.NestedString(item.Object, "spec", "serviceMesh", "controlPlane", "namespace")
				if len(namespace) > 0 {
					meshNamespace = namespace
				} else {
					log.V(1).Info("Mesh Namespace is not set in DSCI")
					// at this point, it is not set anywhere, lets return an error
					panic("error setting Mesh Namespace in DSCI.")
				}
			}
		}
	} else {
		log.V(1).Info("Istio Control Plane name and namespace read from environment variables")
	}
	return istioControlPlane, meshNamespace
}

// VerifyIfMeshAuthorizationIsEnabled func checks if Authorization has been configured for
// ODH platform. That check is done by verifying the presence of an Istio AuthorizationPolicy
// resource. This resource is expected to be present for both the ODH managed and unmanaged
// setups of the authorization capability. If it is not present, it is inferred that the
// authorization capability is not available.
func VerifyIfMeshAuthorizationIsEnabled(ctx context.Context, cli client.Client) (bool, error) {
	// TODO: The VerifyIfCapabilityIsEnabled func was supposed to be a generic way to check
	// if ODH platform has some capability enabled. So, authorization capabilities would
	// have been detected using that func. The VerifyIfCapabilityIsEnabled does the check
	// by fetching the DSCInitialization resource and checking its `status` field.
	// However, the odh-operator would expose the status of the available capabilities *only* if
	// such capabilities are managed by the odh-operator. Since ODH allows the user
	// to manually do the setup of authorization, the user would own the setup and odh-operator
	// would no longer do the management of the stack. Unfortunately, this means that the
	// check via the DSCI is not reliable at the moment.
	// This func is a workaround to (hopefully, more reliably) find if the authorization
	// capability is available. To remove this workaround, an enhancement to odh-operator
	// should be done to reliably pass down to components if the authorization capability
	// is available, taking into account both managed an unmanaged configurations.

	authPolicies := &v1beta1.AuthorizationPolicyList{}
	_, meshNamespace := GetIstioControlPlaneName(ctx, cli)
	if err := cli.List(ctx, authPolicies, client.InNamespace(meshNamespace)); err != nil && !meta.IsNoMatchError(err) {
		return false, err
	}
	if len(authPolicies.Items) == 0 {
		return false, nil
	}

	for _, policy := range authPolicies.Items {
		if policy.Spec.Action == v1beta12.AuthorizationPolicy_CUSTOM {
			if policy.Spec.Selector != nil && policy.Spec.Selector.MatchLabels["component"] == "predictor" {
				return true, nil
			}
		}
	}

	return false, nil
}

// GetApplicationNamespace returns the namespace where the application components are installed.
// defaults to: RHOAI - redhat-ods-applications, ODH: opendatahub
func GetApplicationNamespace(ctx context.Context, cli client.Client) (string, error) {
	if _appNamespace != nil {
		return *_appNamespace, nil
	}

	podNamespace := os.Getenv("POD_NAMESPACE")
	objectList, err := getDSCIObject(ctx, cli)
	if err != nil {
		log.V(0).Error(err, "Failed to fetch the DSCI object")
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
		if getGvResourcesErr != nil && !apierr.IsNotFound(getGvResourcesErr) {
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
