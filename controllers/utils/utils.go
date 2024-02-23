package utils

import (
	"reflect"

	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
)

const (
	inferenceServiceDeploymentModeAnnotation      = "serving.kserve.io/deploymentMode"
	inferenceServiceDeploymentModeAnnotationValue = "ModelMesh"
)

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
