package reconcilers

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/google/uuid"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	mrapi "github.com/opendatahub-io/model-registry/pkg/api"
	"github.com/opendatahub-io/model-registry/pkg/openapi"
	"github.com/opendatahub-io/odh-model-controller/controllers/comparators"
	"github.com/opendatahub-io/odh-model-controller/controllers/constants"
	"github.com/opendatahub-io/odh-model-controller/controllers/processors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ModelRegistryInferenceServiceReconciler struct {
	client         client.Client
	deltaProcessor processors.DeltaProcessor
}

func NewModelRegistryInferenceServiceReconciler(client client.Client) *ModelRegistryInferenceServiceReconciler {
	return &ModelRegistryInferenceServiceReconciler{
		client:         client,
		deltaProcessor: processors.NewDeltaProcessor(),
	}
}

func (r *ModelRegistryInferenceServiceReconciler) Reconcile(ctx context.Context, log logr.Logger, namespace string, mrClient mrapi.ModelRegistryApi, servingRuntimeName string) error {
	// Fetch the ServingEnvironment corresponding to the current namespace
	env, err := mrClient.GetServingEnvironmentByParams(&namespace, nil)
	if err != nil {
		return err
	}

	// Fetch all inference services for the given ServingEnvironment and Runtime
	// TODO: handle paging as we could have exceeding results
	isList, err := mrClient.GetInferenceServices(mrapi.ListOptions{}, env.Id, &servingRuntimeName)
	if err != nil {
		return err
	}

	if isList.Size == 0 {
		log.Info("No inference services found in model registry for serving env: " + namespace + " and runtime: " + servingRuntimeName)
		return nil
	}

	// For each model registry InferenceService apply reconciliation
	for _, is := range isList.Items {
		// Initialize with the id and override with the name if provided
		isName := getInferenceServiceName(&is)
		isLog := log.WithValues("InferenceService", isName)
		err := r.ReconcileInferenceService(ctx, isLog, namespace, mrClient, &is)
		if err != nil {
			// Log error and continue to the next IS
			isLog.Error(err, "Error reconciling model registry InferenceService")
		}
	}

	return nil
}

func (r *ModelRegistryInferenceServiceReconciler) ReconcileInferenceService(
	ctx context.Context,
	log logr.Logger,
	namespace string,
	mrClient mrapi.ModelRegistryApi,
	is *openapi.InferenceService,
) error {
	// Model Registry ServeModel states:
	// NEW: ISVC just created in the namespace
	// RUNNING: deployed model (associated to the ISVC) is running in the namespace
	// FAILED: Something went wrong creating the ISVC, deployment failed
	// UNKNOWN: Something went wrong deleting the ISVC, deployment is in unknown state
	// COMPLETE: The ISVC has been correctly removed, the deployment is removed

	isName := getInferenceServiceName(is)

	// Ensure runtime is not nil, it should not but to be sure no panic is thrown adding this check
	if is.Runtime == nil {
		return fmt.Errorf("missing required Runtime field")
	}

	// Look for existing ISVC by label and namespace
	existingISVC, err := r.findInferenceService(ctx, log, is, namespace)
	if err != nil {
		log.Error(err, "Unable to find ISVC in the current namespace")
	}

	isInDeployedState := isInferenceServiceDeployed(is)
	isvcExists := existingISVC != nil

	if !isvcExists && isInDeployedState { // Create new desired ISVC

		log.Info("ISVC not existing and IS state is DEPLOYED, creating ISVC: " + isName)

		// Create ServeModel
		sm, err := r.createServeModel(mrClient, is)
		if err != nil {
			// if something went worng here, do not stop reconciliation
			log.Error(err, "something went wrong creating desired ServeModel")
		}

		desiredISVC, err := r.createDesiredInferenceService(ctx, namespace, isName, mrClient, is, sm)
		if err != nil {
			return fmt.Errorf("something went wrong creating desired InferenceService CR: %w", err)
		}

		if err = r.processDelta(ctx, log, desiredISVC, nil); err != nil {
			r.updateServeModelState(mrClient, is, sm, openapi.EXECUTIONSTATE_FAILED)
			return fmt.Errorf("something went wrong applying the desired InferenceService CR")
		}

		log.Info("Created desired ISVC: " + isName)

	} else if isvcExists && !isInDeployedState { // Delete existing ISVC

		log.Info("ISVC existing and IS state is UNDEPLOYED, removing ISVC: " + isName)

		// Get corresponding ServeModel
		var sm *openapi.ServeModel
		smId, ok := existingISVC.Labels[constants.ModelRegistryInferenceServiceSMLabel]
		if ok {
			// get by id
			sm, err = mrClient.GetServeModelById(smId)
		} else {
			// get latest
			sm, err = r.getLatestServeModel(mrClient, is)
		}
		if err != nil {
			log.Error(err, "Error retrieving ServeModel")
		}

		// Skip deletion if GetDeletionTimestamp != nil
		if existingISVC.GetDeletionTimestamp() == nil {
			if err = r.processDelta(ctx, log, nil, existingISVC); err != nil {
				_, err = r.updateServeModelState(mrClient, is, sm, openapi.EXECUTIONSTATE_UNKNOWN)
				if err != nil {
					log.Error(err, "Error updating ServeModel")
				}
				return fmt.Errorf("something went wrong deleting InferenceService CR")
			}
		} else {
			log.Info("ISVC already marked to be deleted")
		}

		_, err = r.updateServeModelState(mrClient, is, sm, openapi.EXECUTIONSTATE_COMPLETE)
		if err != nil {
			log.Error(err, "Error updating ServeModel")
		}

		log.Info("Deleted ISVC: " + isName)

	} else if isvcExists && isInDeployedState { // Update the existing ISVC

		log.Info("ISVC existing and IS state is DEPLOYED")

		// Get corresponding ServeModel
		var sm *openapi.ServeModel
		smId, ok := existingISVC.Labels[constants.ModelRegistryInferenceServiceSMLabel]
		if ok {
			// get by id
			sm, err = mrClient.GetServeModelById(smId)
		} else {
			// get latest
			sm, err = r.getLatestServeModel(mrClient, is)
		}
		if err != nil {
			log.Error(err, "Error retrieving ServeModel")
		}

		// TODO: we could check ISVC.Status.Conditions
		_, err = r.updateServeModelState(mrClient, is, sm, openapi.EXECUTIONSTATE_RUNNING)
		if err != nil {
			log.Error(err, "Error updating ServeModel")
		}

		desiredISVC, err := r.createDesiredInferenceService(ctx, namespace, isName, mrClient, is, sm)
		if err != nil {
			return fmt.Errorf("something went wrong creating desired InferenceService CR: %w", err)
		}

		// Update the existing CR if there are changes
		if err = r.processDelta(ctx, log, desiredISVC, existingISVC); err != nil {
			_, err = r.updateServeModelState(mrClient, is, sm, openapi.EXECUTIONSTATE_UNKNOWN)
			if err != nil {
				log.Error(err, "Error updating ServeModel")
			}
			return fmt.Errorf("something went wrong deleting InferenceService CR")
		}

	} else { // Skip InferenceService reconciliation

		log.Info("ISVC not existing and IS state is UNDEPLOYED, skipping InferenceService")

		// get latest and check it is in COMPLETE state if it exists
		sm, err := r.getLatestServeModel(mrClient, is)
		if err != nil {
			log.Error(err, "Error retrieving ServeModel")
		}
		if sm != nil && !isServeModelCompleted(sm) {
			_, err = r.updateServeModelState(mrClient, is, sm, openapi.EXECUTIONSTATE_COMPLETE)
			if err != nil {
				log.Error(err, "Error updating ServeModel")
			}
		}

	}

	return nil
}

func (r *ModelRegistryInferenceServiceReconciler) processDelta(ctx context.Context, log logr.Logger, desiredISVC *kservev1beta1.InferenceService, existingISVC *kservev1beta1.InferenceService) (err error) {
	comparator := comparators.GetInferenceServiceComparator()
	delta := r.deltaProcessor.ComputeDelta(comparator, desiredISVC, existingISVC)

	if !delta.HasChanges() {
		log.V(1).Info("No delta found")
		return nil
	}

	if delta.IsAdded() {
		log.V(1).Info("Delta found", "create", desiredISVC.GetName())
		if err = r.client.Create(ctx, desiredISVC); err != nil {
			return
		}
	}

	if delta.IsUpdated() {
		log.V(1).Info("Delta found", "update", existingISVC.GetName())
		rp := existingISVC.DeepCopy()
		rp.Spec.Predictor = desiredISVC.Spec.Predictor

		if err = r.client.Update(ctx, rp); err != nil {
			return
		}
	}

	if delta.IsRemoved() {
		log.V(1).Info("Delta found", "delete", existingISVC.GetName())
		if err = r.client.Delete(ctx, existingISVC); err != nil {
			return
		}
	}
	return nil
}

// findInferenceService looks for an InferenceService CR in the cluster having label constants.ModelRegistryInferenceServiceLabel equal to is.Id in the provided namespace
func (r *ModelRegistryInferenceServiceReconciler) findInferenceService(ctx context.Context, log logr.Logger, is *openapi.InferenceService, namespace string) (*kservev1beta1.InferenceService, error) {
	// TODO I should check the status of the ISVC, because there could have been a previous deletion operation
	inferenceServicesList := &kservev1beta1.InferenceServiceList{}
	opts := []client.ListOption{client.InNamespace(namespace), client.MatchingLabels{
		constants.ModelRegistryInferenceServiceIDLabel: *is.Id,
	}}

	err := r.client.List(context.TODO(), inferenceServicesList, opts...)
	if err != nil {
		return nil, fmt.Errorf("error getting list of inference services for namespace=%s and label %s=%s", namespace, constants.ModelRegistryInferenceServiceIDLabel, *is.Id)
	}

	if len(inferenceServicesList.Items) == 0 {
		log.Info("No InferenceService found for MR inference service: " + *is.Id)
		return nil, nil
	}

	if len(inferenceServicesList.Items) > 1 {
		return nil, fmt.Errorf("multiple inference services for namespace=%s and label %s=%s", namespace, constants.ModelRegistryInferenceServiceIDLabel, *is.Id)
	}

	return &inferenceServicesList.Items[0], nil
}

// createDesiredInferenceService fetch all required data from model registry and create a new kservev1beta1.InferenceService CR object based on that data
func (r *ModelRegistryInferenceServiceReconciler) createDesiredInferenceService(ctx context.Context, namespace string, isName string, mrClient mrapi.ModelRegistryApi, is *openapi.InferenceService, sm *openapi.ServeModel) (*kservev1beta1.InferenceService, error) {
	// Fetch the required information from model registry
	modelArtifact, err := mrClient.GetModelArtifactByInferenceService(*is.Id)
	if err != nil {
		return nil, err
	}

	// Assemble and return the desired InferenceService CR
	return r.assembleInferenceService(namespace, isName, is, sm, modelArtifact, true)
}

// assembleInferenceService create a new kservev1beta1.InferenceService CR object based on the provided data
func (r *ModelRegistryInferenceServiceReconciler) assembleInferenceService(namespace string, isName string, is *openapi.InferenceService, sm *openapi.ServeModel, modelArtifact *openapi.ModelArtifact, isModelMesh bool) (*kservev1beta1.InferenceService, error) {
	// Ensure modelArtifact.ModelFormatName is provided, otherwise return error
	if modelArtifact.ModelFormatName == nil {
		return nil, fmt.Errorf("missing model format name in model artifact metadata")
	}

	desiredInferenceService := &kservev1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      isName,
			Namespace: namespace,
			Annotations: map[string]string{
				"openshift.io/display-name": isName,
			},
			Labels: map[string]string{
				constants.ModelRegistryInferenceServiceIDLabel: *is.Id,
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

	if sm != nil && sm.Id != nil {
		desiredInferenceService.Labels[constants.ModelRegistryInferenceServiceSMLabel] = *sm.Id
	}

	if isModelMesh {
		desiredInferenceService.Annotations["serving.kserve.io/deploymentMode"] = "ModelMesh"
	} else {
		// kserve
		desiredInferenceService.Annotations["serving.knative.openshift.io/enablePassthrough"] = "true"
		desiredInferenceService.Annotations["sidecar.istio.io/inject"] = "true"
		desiredInferenceService.Annotations["sidecar.istio.io/rewriteAppHTTPProbers"] = "true"
	}

	return desiredInferenceService, nil
}

// getLatestServeModel get the latest ServeModel instance
func (r *ModelRegistryInferenceServiceReconciler) getLatestServeModel(mrClient mrapi.ModelRegistryApi, is *openapi.InferenceService) (*openapi.ServeModel, error) {
	// Get latest ServeModel
	size := int32(1)
	serveModelList, err := mrClient.GetServeModels(mrapi.ListOptions{
		PageSize:  &size,
		OrderBy:   (*string)(openapi.ORDERBYFIELD_CREATE_TIME.Ptr()),
		SortOrder: (*string)(openapi.SORTORDER_DESC.Ptr()),
	}, is.Id)
	if err != nil {
		return nil, err
	}

	if serveModelList.Size == 0 {
		return nil, nil
	}

	return &serveModelList.Items[0], nil
}

// createServeModel create a fresh new instance of ServeModel
func (r *ModelRegistryInferenceServiceReconciler) createServeModel(mrClient mrapi.ModelRegistryApi, is *openapi.InferenceService) (*openapi.ServeModel, error) {

	// Need to create a new ServeModel
	modelVersion, err := mrClient.GetModelVersionByInferenceService(*is.Id)
	if err != nil {
		return nil, err
	}

	defaultState := openapi.EXECUTIONSTATE_UNKNOWN
	smName := fmt.Sprintf("%s/%s", getInferenceServiceName(is), uuid.New().String())
	newServeModel, err := mrClient.UpsertServeModel(&openapi.ServeModel{
		Name:           &smName,
		ModelVersionId: *modelVersion.Id,
		LastKnownState: &defaultState,
	}, is.Id)
	if err != nil {
		return nil, err
	}

	return newServeModel, nil
}

// updateLatestServeModelState update the LastKnownState of the configured ServeModel instance for the provided InferenceService.
func (r *ModelRegistryInferenceServiceReconciler) updateServeModelState(mrClient mrapi.ModelRegistryApi, is *openapi.InferenceService, sm *openapi.ServeModel, newState openapi.ExecutionState) (*openapi.ServeModel, error) {
	if sm == nil {
		return nil, nil
	}

	// Update the ServeModel state to the provided one
	sm.SetLastKnownState(newState)

	sm, err := mrClient.UpsertServeModel(sm, is.Id)
	if err != nil {
		return nil, err
	}
	return sm, nil
}

// Utils

// isInferenceServiceDeployed return true if the IS state is not nil and equal to DEPLOYED
func isInferenceServiceDeployed(is *openapi.InferenceService) bool {
	return is.State != nil && *is.State == openapi.INFERENCESERVICESTATE_DEPLOYED
}

// getInferenceServiceName compute the IS name, which is equal to IS.Name if not nil otherwise the IS.Id
// If also IS.Id is nil, return empty string
func getInferenceServiceName(is *openapi.InferenceService) string {
	if is.Name != nil {
		return *is.Name
	}
	if is.Id != nil {
		return *is.Id
	}
	return ""
}

// isServeModelCompleted return true if SM LastKnownState is not nil and equal to COMPLETE
func isServeModelCompleted(sm *openapi.ServeModel) bool {
	return sm.LastKnownState != nil && *sm.LastKnownState == openapi.EXECUTIONSTATE_COMPLETE
}
