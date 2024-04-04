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
	dataScienceClusterKind                   = "DataScienceCluster"
	dataScienceClusterApiVersion             = "datasciencecluster.opendatahub.io/v1"
	KserveAuthorinoComponent                 = "kserve-authorino"
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
	objectList.SetAPIVersion(dataScienceClusterApiVersion)
	objectList.SetKind(dataScienceClusterKind)

	if err := cli.List(ctx, objectList); err != nil {
		return false, fmt.Errorf("not able to read %s: %w", objectList, err)
	}

	// there must be only one dsc
	if len(objectList.Items) == 1 {
		fields := []string{"spec", "components", componentName, "managementState"}
		if componentName == KserveAuthorinoComponent {
			// For KServe, Authorino is required when ServiceMesh is enabled
			// By Disabling ServiceMesh for RawDeployment, it should reflect on disabling
			// the Authorino integration as well.
			fields = []string{"spec", "components", "kserve", "serving", "managementState"}
			kserveFields := []string{"spec", "components", "kserve", "managementState"}
			serving, _, err := unstructured.NestedString(objectList.Items[0].Object, fields...)
			kserve, _, err := unstructured.NestedString(objectList.Items[0].Object, kserveFields...)
			if err != nil {
				return false, fmt.Errorf("failed to retrieve the component [%s] status from %+v",
					componentName, objectList.Items[0])
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
		return false, fmt.Errorf("there is no %s available in the cluster", dataScienceClusterKind)
	}
}

func IsNil(i any) bool {
	return reflect.ValueOf(i).IsNil()
}

func IsNotNil(i any) bool {
	return !IsNil(i)
}
