package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"

	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	v1beta12 "istio.io/api/security/v1beta1"
	"istio.io/client-go/pkg/apis/security/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/opendatahub-io/odh-model-controller/controllers/constants"
)

type IsvcDeploymentMode string

var (
	Serverless    IsvcDeploymentMode = "Serverless"
	RawDeployment IsvcDeploymentMode = "RawDeployment"
	ModelMesh     IsvcDeploymentMode = "ModelMesh"
)

const (
	inferenceServiceDeploymentModeAnnotation = "serving.kserve.io/deploymentMode"
	KserveConfigMapName                      = "inferenceservice-config"
	KServeWithServiceMeshComponent           = "kserve-service-mesh"
)

func GetDeploymentModeForIsvc(ctx context.Context, cli client.Client, isvc *kservev1beta1.InferenceService) (IsvcDeploymentMode, error) {

	// If ISVC specifically sets deployment mode using an annotation, return bool depending on value
	value, exists := isvc.Annotations[inferenceServiceDeploymentModeAnnotation]
	if exists {
		switch value {
		case string(ModelMesh):
			return ModelMesh, nil
		case string(Serverless):
			return Serverless, nil
		case string(RawDeployment):
			return RawDeployment, nil
		default:
			return "", fmt.Errorf("the deployment mode '%s' of the Inference Service is invalid", value)
		}
	} else {
		// ISVC does not specifically set deployment mode using an annotation, determine the default from configmap
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
		case string(ModelMesh):
			return ModelMesh, nil
		case string(Serverless):
			return Serverless, nil
		case string(RawDeployment):
			return RawDeployment, nil
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

			return (serving == "Managed" || serving == "Unmanaged") && kserve == "Managed", nil
		}

		val, _, err := unstructured.NestedString(objectList.Items[0].Object, fields...)
		if err != nil {
			return false, fmt.Errorf("failed to retrieve the component [%s] status from %+v",
				componentName, objectList.Items[0])
		}
		return val == "Managed", nil
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
	objectList := &unstructured.UnstructuredList{}
	objectList.SetAPIVersion(GVK.DataScienceClusterInitialization.GroupVersion().String())
	objectList.SetKind(GVK.DataScienceClusterInitialization.Kind)

	if err := cli.List(ctx, objectList); err != nil {
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
	if err := cli.List(ctx, authPolicies, client.InNamespace(constants.IstioNamespace)); err != nil && !meta.IsNoMatchError(err) {
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

func IsNil(i any) bool {
	return reflect.ValueOf(i).IsNil()
}

func IsNotNil(i any) bool {
	return !IsNil(i)
}
