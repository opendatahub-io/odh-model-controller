package utils

import (
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"os"
	"reflect"
)

const (
	inferenceServiceDeploymentModeAnnotation      = "serving.kserve.io/deploymentMode"
	inferenceServiceDeploymentModeAnnotationValue = "ModelMesh"
)

// GetApplicationNamespace determines namespace in which this controller is deployed.
func GetApplicationNamespace() (string, error) {
	ns, found := os.LookupEnv("POD_NAMESPACE")
	if !found {
		namespace, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
		if err != nil {
			return "", nil
		}

		return string(namespace), err
	}

	return ns, nil
}

func IsDeploymentModeForIsvcModelMesh(isvc *kservev1beta1.InferenceService) bool {
	value, exists := isvc.Annotations[inferenceServiceDeploymentModeAnnotation]
	if exists && value == inferenceServiceDeploymentModeAnnotationValue {
		return true
	}
	return false
}

func IsNil(i any) bool {
	return reflect.ValueOf(i).IsNil()
}

func IsNotNil(i any) bool {
	return !IsNil(i)
}
