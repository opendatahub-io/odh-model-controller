package reconcilers

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	mrapi "github.com/opendatahub-io/model-registry/pkg/api"
	"github.com/opendatahub-io/model-registry/pkg/openapi"
	"github.com/opendatahub-io/odh-model-controller/controllers/constants"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ModelRegistryInferenceServiceReconciler struct {
	client client.Client
}

func NewModelRegistryInferenceServiceReconciler(client client.Client) *ModelRegistryInferenceServiceReconciler {
	return &ModelRegistryInferenceServiceReconciler{
		client: client,
	}
}

func (r *ModelRegistryInferenceServiceReconciler) Reconcile(ctx context.Context, log logr.Logger, namespace string, mrClient mrapi.ModelRegistryApi, servingRuntimeName string) error {
	// Model Registry ServeModel states:
	// NEW: ISVC just created in the namespace
	// RUNNING: deployed model (associated to the ISVC) is running in the namespace
	// FAILED: Something went wrong creating the ISVC, deployment failed
	// UNKNOWN: Something went wrong deleting the ISVC, deployment is in unknown state
	// COMPLETE: The ISVC has been correctly removed, the deployment is removed

	// Fetch the ServingEnvironment corresponding to the current ServingRuntime CR
	env, err := mrClient.GetServingEnvironmentByParams(&namespace, nil)
	if err != nil {
		return err
	}

	log.Info(fmt.Sprintf("Found serving environment in model registry: %s", *env.Id))

	// Fetch all inference services for the given serving environment
	// TODO: handle paging
	isList, err := mrClient.GetInferenceServices(mrapi.ListOptions{}, env.Id)
	if err != nil {
		return nil
	}

	if isList.Size == 0 {
		log.Info("No inference services found in model registry for serving env: " + servingRuntimeName)
		return nil
	}

	isListByRuntime := []openapi.InferenceService{}
	for _, is := range isList.Items {
		// Consider just those IS having servingRuntimeName as Runtime property
		if is.Runtime != nil && *is.Runtime == servingRuntimeName {
			isListByRuntime = append(isListByRuntime, is)
		}
	}

	if len(isListByRuntime) == 0 {
		log.Info("No inference services found in model registry for serving environment " + namespace + " and runtime " + servingRuntimeName)
		return nil
	}

	// For each inference, check if the corresponding one exist in the cluster
	for _, is := range isListByRuntime {
		if is.Id == nil {
			log.Error(fmt.Errorf("missing id for InferenceService"), "Retrieved InferenceService from model registry is missing its id, skipping")
			continue
		}

		// initialize with the id and override with the name if provided
		isName := *is.Id
		if is.Name != nil {
			isName = *is.Name
		}

		isLog := log.WithValues("InferenceService", isName)

		isvc, err := r.findInferenceService(ctx, isLog, &is, namespace)
		if err != nil {
			isLog.Error(err, "Unable to find inference service")
		} else if isvc == nil && is.State != nil && *is.State == openapi.INFERENCESERVICESTATE_DEPLOYED {
			// ISVC not existing in the namespace and InferenceService is in DEPLOYED state in model registry
			// Create new ISVC in the provided namespace for the corresponding InferenceService in model registry
			isLog.Info("ISVC not found for InferenceService " + isName + " and InferenceService state is DEPLOYED. Creating new ISVC.")
			isvc, err := r.createDesiredInferenceService(ctx, namespace, isName, mrClient, &is, servingRuntimeName)
			if err != nil {
				isLog.Error(err, "Something went wrong creating desired InferenceService CR")
				continue
			}

			if err = r.client.Create(ctx, isvc); err != nil {
				isLog.Error(err, "Something went wrong applying the desired InferenceService CR")
				_, err := r.updateServeModel(mrClient, &is, openapi.EXECUTIONSTATE_FAILED)
				if err != nil {
					isLog.Error(err, "Something went wrong updating ServeModel state to FAILED in model registry")
				}
			} else {
				isLog.Info("Created desired ISVC: " + isName)
				if _, err = r.logServeModel(mrClient, &is); err != nil {
					isLog.Error(err, "Something went wrong logging ServeModel in model registry")
				}
			}
		} else if isvc != nil && (is.State == nil || *is.State == openapi.INFERENCESERVICESTATE_UNDEPLOYED) {
			// ISVC already existing in the namespace and InferenceService is not in DEPLOYED state in model registry
			// Delete existing ISCV from the provided namespace
			isLog.Info("ISVC already existing, removing inference: " + isName)

			// Remove existing ISVC
			if err = r.client.Delete(ctx, isvc); err != nil {
				isLog.Error(err, "Something went wrong deleting InferenceService CR: "+isName)
				_, err := r.updateServeModel(mrClient, &is, openapi.EXECUTIONSTATE_UNKNOWN)
				if err != nil {
					isLog.Error(err, "Something went wrong updating ServeModel state to UNKNOWN in model registry")
				}
			} else {
				isLog.Info("Deleted ISVC: " + isName)
				if _, err = r.updateServeModel(mrClient, &is, openapi.EXECUTIONSTATE_COMPLETE); err != nil {
					isLog.Error(err, "Something went wrong updating ServeModel state to COMPLETE in model registry")
				}
			}
		} else if isvc != nil {
			// ISVC already existing in the namespace and InferenceService is in DEPLOYED state in model registry
			// Update the ServeModel to RUNNING state, no changes in the cluster
			isLog.Info("ISVC already existing: " + isName)

			// TODO: we could add some checks in the ISVC status to assure is really running
			if _, err = r.updateServeModel(mrClient, &is, openapi.EXECUTIONSTATE_RUNNING); err != nil {
				isLog.Error(err, "Something went wrong updating ServeModel state to RUNNING in model registry")
			}
		} else {
			// ISVC not existing but InferenceService in model registry is not in DEPLOYED state
			isLog.Info("ISVC not existing for " + isName + " but InferenceService state is not DEPLOYED. Do nothing.")
		}
	}

	return nil
}

// findInferenceService looks for an InferenceService CR in the cluster having is.Name as name in the provided namespace
func (r *ModelRegistryInferenceServiceReconciler) findInferenceService(ctx context.Context, log logr.Logger, is *openapi.InferenceService, namespace string) (*kservev1beta1.InferenceService, error) {
	// TODO I should check the status of the ISVC, because there could have been a previous deletion operation
	inferenceServicesList := &kservev1beta1.InferenceServiceList{}
	opts := []client.ListOption{client.InNamespace(namespace), client.MatchingLabels{
		constants.ModelRegistryInferenceServiceLabel: *is.Id,
	}}

	err := r.client.List(context.TODO(), inferenceServicesList, opts...)
	if err != nil {
		return nil, fmt.Errorf("error getting list of inference services for namespace=%s and label %s=%s", namespace, constants.ModelRegistryInferenceServiceLabel, *is.Id)
	}

	if len(inferenceServicesList.Items) == 0 {
		log.Info("No InferenceService found for MR inference service: " + *is.Id)
		return nil, nil
	}

	if len(inferenceServicesList.Items) > 1 {
		return nil, fmt.Errorf("multiple inference services for namespace=%s and label %s=%s", namespace, constants.ModelRegistryInferenceServiceLabel, *is.Id)
	}

	return &inferenceServicesList.Items[0], nil
}

// createDesiredInferenceService fetch all required data from model registry and create a new kservev1beta1.InferenceService CR object based on that data
func (r *ModelRegistryInferenceServiceReconciler) createDesiredInferenceService(ctx context.Context, namespace string, isName string, mrClient mrapi.ModelRegistryApi, is *openapi.InferenceService, servingEnvironmentName string) (*kservev1beta1.InferenceService, error) {
	// Fetch the required information from model registry
	modelVersion, err := mrClient.GetModelVersionByInferenceService(*is.Id)
	if err != nil {
		return nil, err
	}

	modelArtifactList, err := mrClient.GetModelArtifacts(mrapi.ListOptions{}, modelVersion.Id)
	if err != nil {
		return nil, err
	}

	if modelArtifactList.Size == 0 {
		return nil, fmt.Errorf("no model artifacts found for model version: %s", *modelVersion.Id)
	}

	// Right now we assume there is just one artifact for each model version
	modelArtifact := &modelArtifactList.Items[0]

	// Assemble the desired InferenceService CR
	desiredInferenceService := r.assembleInferenceService(namespace, isName, *is, *modelArtifact, true)

	return desiredInferenceService, nil
}

// assembleInferenceService create a new kservev1beta1.InferenceService CR object based on the provided data
func (r *ModelRegistryInferenceServiceReconciler) assembleInferenceService(namespace string, isName string, is openapi.InferenceService, modelArtifact openapi.ModelArtifact, isModelMesh bool) *kservev1beta1.InferenceService {
	desiredInferenceService := &kservev1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      isName,
			Namespace: namespace,
			Annotations: map[string]string{
				"openshift.io/display-name": isName,
			},
			Labels: map[string]string{
				constants.ModelRegistryInferenceServiceLabel: *is.Id,
				"name":                     isName,
				"opendatahub.io/dashboard": "true",
			},
		},
		Spec: kservev1beta1.InferenceServiceSpec{
			Predictor: kservev1beta1.PredictorSpec{
				Model: &kservev1beta1.ModelSpec{
					ModelFormat: kservev1beta1.ModelFormat{
						Name:    *modelArtifact.ModelFormatName,
						Version: modelArtifact.ModelFormatVersion,
					},
					Runtime: is.Runtime,
					PredictorExtensionSpec: kservev1beta1.PredictorExtensionSpec{
						Storage: &kservev1beta1.StorageSpec{
							StorageKey: modelArtifact.StorageKey,
							Path:       modelArtifact.StoragePath,
						},
					},
				},
			},
		},
	}

	if isModelMesh {
		desiredInferenceService.Annotations["serving.kserve.io/deploymentMode"] = "ModelMesh"
	} else {
		// kserve
		desiredInferenceService.Annotations["serving.knative.openshift.io/enablePassthrough"] = "true"
		desiredInferenceService.Annotations["sidecar.istio.io/inject"] = "true"
		desiredInferenceService.Annotations["sidecar.istio.io/rewriteAppHTTPProbers"] = "true"
	}

	return desiredInferenceService
}

// logServeModel create a new ServeModel instance in NEW state if not already existing for the provided InferenceService,
// otherwise simply return the existing one
func (r *ModelRegistryInferenceServiceReconciler) logServeModel(mrClient mrapi.ModelRegistryApi, is *openapi.InferenceService) (*openapi.ServeModel, error) {
	serveModelList, err := mrClient.GetServeModels(mrapi.ListOptions{}, is.Id)
	if err != nil {
		return nil, err
	}

	if serveModelList.Size == 0 {
		// Create new ServeModel
		modelVersion, err := mrClient.GetModelVersionByInferenceService(*is.Id)
		if err != nil {
			return nil, err
		}

		newState := openapi.EXECUTIONSTATE_NEW
		serveModel, err := mrClient.UpsertServeModel(&openapi.ServeModel{
			Name:           is.Name,
			ModelVersionId: *modelVersion.Id,
			LastKnownState: &newState,
		}, is.Id)

		if err != nil {
			return nil, err
		}

		return serveModel, nil
	}

	// Assume there is at most one ServeModel per InferenceService
	return &serveModelList.Items[0], nil
}

// updateServeModel update the LastKnownState of the configured ServeModel instance for the provided InferenceService.
// If a ServeModel is not already existing, try to create a new one and then update it.
func (r *ModelRegistryInferenceServiceReconciler) updateServeModel(mrClient mrapi.ModelRegistryApi, is *openapi.InferenceService, newState openapi.ExecutionState) (*openapi.ServeModel, error) {
	sm, err := r.logServeModel(mrClient, is)
	if err != nil {
		return nil, err
	}

	// Update the ServeModel state to the provided one
	sm.SetLastKnownState(newState)

	sm, err = mrClient.UpsertServeModel(sm, is.Id)
	if err != nil {
		return nil, err
	}
	return sm, nil
}
