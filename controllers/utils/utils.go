package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"os"
	"reflect"

	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

func IsNil(i any) bool {
	return reflect.ValueOf(i).IsNil()
}

func IsNotNil(i any) bool {
	return !IsNil(i)
}
