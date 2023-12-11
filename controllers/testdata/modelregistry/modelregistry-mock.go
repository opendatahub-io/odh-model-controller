package modelregistry

import (
	"fmt"

	"github.com/opendatahub-io/model-registry/pkg/api"
	"github.com/opendatahub-io/model-registry/pkg/openapi"
	"github.com/stretchr/testify/mock"
)

func NewModelRegistryServiceMocked() api.ModelRegistryApi {
	return &ModelRegistryServiceMocked{}
}

var (
	// ids
	servingEnvironmentId = "1"
	registeredModelId    = "2"
	modelVersionId       = "3"
	modelArtifactId      = "1"
	serveModelId         = "1"
	// data
	modelName          = "dummy-model"
	versionName        = "dummy-version"
	modelFormatName    = "onnx"
	modelFormatVersion = "1"
	storagePath        = "path/to/model"
	storageKey         = "aws-connection-models"
)

type ModelRegistryServiceMocked struct {
	mock.Mock
}

// GetInferenceServiceById implements api.ModelRegistryApi.
func (m *ModelRegistryServiceMocked) GetInferenceServiceById(id string) (*openapi.InferenceService, error) {
	panic("unimplemented")
}

// GetInferenceServiceByParams implements api.ModelRegistryApi.
func (m *ModelRegistryServiceMocked) GetInferenceServiceByParams(name *string, parentResourceId *string, externalId *string) (*openapi.InferenceService, error) {
	panic("unimplemented")
}

// GetInferenceServices implements api.ModelRegistryApi.
func (m *ModelRegistryServiceMocked) GetInferenceServices(listOptions api.ListOptions, servingEnvironmentId *string, runtime *string) (*openapi.InferenceServiceList, error) {
	args := m.MethodCalled("GetInferenceServices")
	return args[0].(*openapi.InferenceServiceList), asError(args[1])
}

// GetModelArtifactById implements api.ModelRegistryApi.
func (m *ModelRegistryServiceMocked) GetModelArtifactById(id string) (*openapi.ModelArtifact, error) {
	panic("unimplemented")
}

// GetModelArtifactByInferenceService implements api.ModelRegistryApi.
func (m *ModelRegistryServiceMocked) GetModelArtifactByInferenceService(inferenceServiceId string) (*openapi.ModelArtifact, error) {
	modelArtifactName := fmt.Sprintf("%s-artifact", versionName)
	return &openapi.ModelArtifact{
		Id:                 &modelArtifactId,
		Name:               &modelArtifactName,
		ModelFormatName:    &modelFormatName,
		ModelFormatVersion: &modelFormatVersion,
		StorageKey:         &storageKey,
		StoragePath:        &storagePath,
	}, nil
}

// GetModelArtifactByParams implements api.ModelRegistryApi.
func (m *ModelRegistryServiceMocked) GetModelArtifactByParams(artifactName *string, modelVersionId *string, externalId *string) (*openapi.ModelArtifact, error) {
	panic("unimplemented")
}

// GetModelArtifacts implements api.ModelRegistryApi.
func (m *ModelRegistryServiceMocked) GetModelArtifacts(listOptions api.ListOptions, modelVersionId *string) (*openapi.ModelArtifactList, error) {
	panic("unimplemented")
}

// GetModelVersionById implements api.ModelRegistryApi.
func (m *ModelRegistryServiceMocked) GetModelVersionById(id string) (*openapi.ModelVersion, error) {
	panic("unimplemented")
}

// GetModelVersionByInferenceService implements api.ModelRegistryApi.
func (m *ModelRegistryServiceMocked) GetModelVersionByInferenceService(inferenceServiceId string) (*openapi.ModelVersion, error) {
	return &openapi.ModelVersion{
		Id:   &modelVersionId,
		Name: &versionName,
	}, nil
}

// GetModelVersionByParams implements api.ModelRegistryApi.
func (m *ModelRegistryServiceMocked) GetModelVersionByParams(versionName *string, registeredModelId *string, externalId *string) (*openapi.ModelVersion, error) {
	panic("unimplemented")
}

// GetModelVersions implements api.ModelRegistryApi.
func (m *ModelRegistryServiceMocked) GetModelVersions(listOptions api.ListOptions, registeredModelId *string) (*openapi.ModelVersionList, error) {
	panic("unimplemented")
}

// GetRegisteredModelById implements api.ModelRegistryApi.
func (m *ModelRegistryServiceMocked) GetRegisteredModelById(id string) (*openapi.RegisteredModel, error) {
	panic("unimplemented")
}

// GetRegisteredModelByInferenceService implements api.ModelRegistryApi.
func (m *ModelRegistryServiceMocked) GetRegisteredModelByInferenceService(inferenceServiceId string) (*openapi.RegisteredModel, error) {
	panic("unimplemented")
}

// GetRegisteredModelByParams implements api.ModelRegistryApi.
func (m *ModelRegistryServiceMocked) GetRegisteredModelByParams(name *string, externalId *string) (*openapi.RegisteredModel, error) {
	return &openapi.RegisteredModel{
		Id:   &registeredModelId,
		Name: &modelName,
	}, nil
}

// GetRegisteredModels implements api.ModelRegistryApi.
func (m *ModelRegistryServiceMocked) GetRegisteredModels(listOptions api.ListOptions) (*openapi.RegisteredModelList, error) {
	panic("unimplemented")
}

// GetServeModelById implements api.ModelRegistryApi.
func (m *ModelRegistryServiceMocked) GetServeModelById(id string) (*openapi.ServeModel, error) {
	return &openapi.ServeModel{
		Id:             &id,
		LastKnownState: openapi.EXECUTIONSTATE_RUNNING.Ptr(),
		ModelVersionId: modelVersionId,
	}, nil
}

// GetServeModels implements api.ModelRegistryApi.
func (m *ModelRegistryServiceMocked) GetServeModels(listOptions api.ListOptions, inferenceServiceId *string) (*openapi.ServeModelList, error) {
	return &openapi.ServeModelList{
		PageSize: 1,
		Size:     1,
		Items: []openapi.ServeModel{
			openapi.ServeModel{
				Id:             &serveModelId,
				LastKnownState: openapi.EXECUTIONSTATE_RUNNING.Ptr(),
				ModelVersionId: modelVersionId,
			},
		}}, nil
}

// GetServingEnvironmentById implements api.ModelRegistryApi.
func (m *ModelRegistryServiceMocked) GetServingEnvironmentById(id string) (*openapi.ServingEnvironment, error) {
	panic("unimplemented")
}

// GetServingEnvironmentByParams implements api.ModelRegistryApi.
func (m *ModelRegistryServiceMocked) GetServingEnvironmentByParams(name *string, externalId *string) (*openapi.ServingEnvironment, error) {
	return &openapi.ServingEnvironment{
		Id:   &servingEnvironmentId,
		Name: name,
	}, nil
}

// GetServingEnvironments implements api.ModelRegistryApi.
func (m *ModelRegistryServiceMocked) GetServingEnvironments(listOptions api.ListOptions) (*openapi.ServingEnvironmentList, error) {
	panic("unimplemented")
}

// UpsertInferenceService implements api.ModelRegistryApi.
func (m *ModelRegistryServiceMocked) UpsertInferenceService(inferenceService *openapi.InferenceService) (*openapi.InferenceService, error) {
	panic("unimplemented")
}

// UpsertModelArtifact implements api.ModelRegistryApi.
func (m *ModelRegistryServiceMocked) UpsertModelArtifact(modelArtifact *openapi.ModelArtifact, modelVersionId *string) (*openapi.ModelArtifact, error) {
	panic("unimplemented")
}

// UpsertModelVersion implements api.ModelRegistryApi.
func (m *ModelRegistryServiceMocked) UpsertModelVersion(modelVersion *openapi.ModelVersion, registeredModelId *string) (*openapi.ModelVersion, error) {
	panic("unimplemented")
}

// UpsertRegisteredModel implements api.ModelRegistryApi.
func (m *ModelRegistryServiceMocked) UpsertRegisteredModel(registeredModel *openapi.RegisteredModel) (*openapi.RegisteredModel, error) {
	panic("unimplemented")
}

// UpsertServeModel implements api.ModelRegistryApi.
func (m *ModelRegistryServiceMocked) UpsertServeModel(serveModel *openapi.ServeModel, inferenceServiceId *string) (*openapi.ServeModel, error) {
	return &openapi.ServeModel{
		Id:             &serveModelId,
		LastKnownState: openapi.EXECUTIONSTATE_RUNNING.Ptr(),
		ModelVersionId: modelVersionId,
	}, nil
}

// UpsertServingEnvironment implements api.ModelRegistryApi.
func (m *ModelRegistryServiceMocked) UpsertServingEnvironment(registeredModel *openapi.ServingEnvironment) (*openapi.ServingEnvironment, error) {
	panic("unimplemented")
}

func asError(arg interface{}) (err error) {
	if arg != nil {
		err = arg.(error)
	}
	return
}
