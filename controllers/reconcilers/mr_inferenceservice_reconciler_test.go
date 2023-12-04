package reconcilers

import (
	"testing"

	"github.com/opendatahub-io/model-registry/pkg/openapi"
)

func TestIsInferenceServiceDeployed(t *testing.T) {
	var result bool

	result = isInferenceServiceDeployed(&openapi.InferenceService{State: openapi.INFERENCESERVICESTATE_DEPLOYED.Ptr()})
	if !result {
		t.Errorf("provided inference service should result in DEPLOYED state")
	}

	result = isInferenceServiceDeployed(&openapi.InferenceService{State: openapi.INFERENCESERVICESTATE_UNDEPLOYED.Ptr()})
	if result {
		t.Errorf("provided inference service should result in UNDEPLOYED state")
	}

	result = isInferenceServiceDeployed(&openapi.InferenceService{})
	if result {
		t.Errorf("provided inference service should result in UNDEPLOYED state")
	}
}

func TestGetInferenceServiceName(t *testing.T) {
	var result string
	id := "1"
	name := "is-name"

	result = getInferenceServiceName(&openapi.InferenceService{Id: &id, Name: &name})
	if result != name {
		t.Errorf("inference service name should be equal to the Name field")
	}

	result = getInferenceServiceName(&openapi.InferenceService{Id: &id})
	if result != id {
		t.Errorf("inference service name should be equal to the Id field")
	}

	result = getInferenceServiceName(&openapi.InferenceService{})
	if result != "" {
		t.Errorf("inference service name should be '' as neither Name nor Id are provided")
	}
}

func TestIsServeModelCompleted(t *testing.T) {
	var result bool

	result = isServeModelCompleted(&openapi.ServeModel{LastKnownState: openapi.EXECUTIONSTATE_COMPLETE.Ptr()})
	if !result {
		t.Errorf("provided inference service should result in DEPLOYED state")
	}

	result = isServeModelCompleted(&openapi.ServeModel{LastKnownState: openapi.EXECUTIONSTATE_CANCELED.Ptr()})
	if result {
		t.Errorf("provided inference service should result in UNDEPLOYED state")
	}

	result = isServeModelCompleted(&openapi.ServeModel{})
	if result {
		t.Errorf("provided inference service should NOT result in COMPLETE state")
	}
}
