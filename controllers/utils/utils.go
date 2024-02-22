package utils

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"

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

func GetNamespaceName() (string, error) {
	// Kubernetes provides the namespace information in the file '/var/run/secrets/kubernetes.io/serviceaccount/namespace'
	namespacePath := filepath.Join("/var/run/secrets/kubernetes.io/serviceaccount", "namespace")

	// Read the namespace from the file
	nsBytes, err := os.ReadFile(namespacePath)
	if err != nil {
		return "", err
	}

	// Convert the byte slice to a string
	namespace := strings.TrimSpace(string(nsBytes))
	return namespace, nil
}
