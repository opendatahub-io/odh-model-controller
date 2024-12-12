package reconcilers

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/go-logr/logr"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"github.com/opendatahub-io/model-registry/pkg/api"
	"github.com/opendatahub-io/model-registry/pkg/core"
	"github.com/opendatahub-io/model-registry/pkg/openapi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	corev1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/comparators"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/processors"
)

const modelRegistryFinalizer = "modelregistry.opendatahub.io/finalizer"

// ModelRegistryInferenceServiceReconciler holds the controller configuration.
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

// Reconcile performs the reconciliation of the model registry based on Kubeflow InferenceService CRs
func (r *ModelRegistryInferenceServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Initialize logger format
	logger := log.FromContext(ctx, "ModelRegistryInferenceService", req.Name, "namespace", req.Namespace)

	// Get the InferenceService object when a reconciliation event is triggered (create,
	// update, delete)
	isvc := &kservev1beta1.InferenceService{}
	err := r.client.Get(ctx, req.NamespacedName, isvc)
	if err != nil && apierrs.IsNotFound(err) {
		logger.V(1).Info("Stop ModelRegistry InferenceService reconciliation, ISVC not found.")
		return ctrl.Result{}, nil
	} else if err != nil {
		logger.Error(err, "Unable to fetch the InferenceService")
		return ctrl.Result{}, err
	}

	mrIsvcId, okMrIsvcId := isvc.Labels[constants.ModelRegistryInferenceServiceIdLabel]
	registeredModelId, okRegisteredModelId := isvc.Labels[constants.ModelRegistryRegisteredModelIdLabel]
	modelVersionId, _ := isvc.Labels[constants.ModelRegistryModelVersionIdLabel]

	if !okMrIsvcId && !okRegisteredModelId {
		// Early check: no model registry specific labels set in the ISVC, ignore the CR
		logger.Error(fmt.Errorf("missing model registry specific label, unable to link ISVC to Model Registry, skipping InferenceService"), "Stop ModelRegistry InferenceService reconciliation")
		return ctrl.Result{}, nil
	}

	var modelRegistryNamespace string
	if mrNSFromISVC, ok := isvc.Labels[constants.ModelRegistryNamespaceLabel]; ok {
		// look for model registry located at specific namespace, as specified in isvc
		modelRegistryNamespace = mrNSFromISVC
	} else {
		// look for model registry inside current namespace
		modelRegistryNamespace = req.Namespace
	}

	logger.Info("Creating model registry service..")
	mr, conn, err := r.initModelRegistryService(ctx, logger, modelRegistryNamespace)
	if err != nil {
		logger.Error(err, "Stop ModelRegistry InferenceService reconciliation")
		return ctrl.Result{Requeue: true, RequeueAfter: time.Second * 10}, err
	} else if mr == nil {
		// There is no model registry installed, do not requeue
		logger.Info("Cannot find ModelRegistry in given namespace, stopping reconciliation")
		return ctrl.Result{}, nil
	}

	if conn != nil {
		defer conn.Close()
	}

	// Retrieve or create the ServingEnvironment associated to the current namespace
	servingEnvironment, err := r.getOrCreateServingEnvironment(logger, mr, req.Namespace)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Check if the InferenceService instance is marked to be deleted, which is
	// indicated by the deletion timestamp being set.
	isMarkedToBeDeleted := isvc.GetDeletionTimestamp() != nil

	// Let's add a finalizer. Then, we can define some operations which should
	// occurs before the custom resource to be deleted.
	// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/finalizers
	if !isMarkedToBeDeleted && !controllerutil.ContainsFinalizer(isvc, modelRegistryFinalizer) {
		logger.Info("Adding Finalizer for ModelRegistry")
		if ok := controllerutil.AddFinalizer(isvc, modelRegistryFinalizer); !ok {
			logger.Error(err, "Failed to add finalizer into the InferenceService custom resource")
			return ctrl.Result{Requeue: true}, nil
		}

		if err = r.client.Update(ctx, isvc); err != nil {
			logger.Error(err, "Failed to update InferenceService custom resource to add finalizer")
			return ctrl.Result{}, err
		}
	}

	var is *openapi.InferenceService

	if okMrIsvcId {
		// Retrieve the IS from model registry using the id
		logger.Info("Retrieving model registry InferenceService by id", "mrIsvcId", mrIsvcId)
		is, err = mr.GetInferenceServiceById(mrIsvcId)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("unable to find InferenceService with id %s in model registry: %w", mrIsvcId, err)
		}
	} else if okRegisteredModelId {
		// No corresponding InferenceService in model registry, create new one
		is, err = r.createMRInferenceService(logger, mr, isvc, *servingEnvironment.Id, registeredModelId, modelVersionId)
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	if is == nil {
		// This should NOT happen
		return ctrl.Result{}, fmt.Errorf("unexpected nil model registry InferenceService")
	}

	if isMarkedToBeDeleted {
		err := r.onDeletion(mr, logger, isvc, is)
		if err != nil {
			return ctrl.Result{Requeue: true}, err
		}

		if controllerutil.ContainsFinalizer(isvc, modelRegistryFinalizer) {
			logger.Info("Removing Finalizer for modelRegistry after successfully perform the operations")
			if ok := controllerutil.RemoveFinalizer(isvc, modelRegistryFinalizer); !ok {
				logger.Error(err, "Failed to remove modelRegistry finalizer for InferenceService")
				return ctrl.Result{Requeue: true}, nil
			}

			if err = r.client.Update(ctx, isvc); IgnoreDeletingErrors(err) != nil {
				logger.Error(err, "Failed to remove modelRegistry finalizer for InferenceService")
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	} else {
		// Update the ISVC label, set the newly created IS id if not present yet
		desired := isvc.DeepCopy()
		desired.Labels[constants.ModelRegistryInferenceServiceIdLabel] = *is.Id

		err = r.processDelta(ctx, logger, desired, isvc)
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
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

		_, okIsId := desiredISVC.Labels[constants.ModelRegistryInferenceServiceIdLabel]
		_, okRegisteredModelId := desiredISVC.Labels[constants.ModelRegistryRegisteredModelIdLabel]
		_, okModelVersionId := desiredISVC.Labels[constants.ModelRegistryModelVersionIdLabel]

		if okIsId {
			rp.Labels[constants.ModelRegistryInferenceServiceIdLabel] = desiredISVC.Labels[constants.ModelRegistryInferenceServiceIdLabel]
		} else {
			delete(rp.Labels, constants.ModelRegistryInferenceServiceIdLabel)
		}

		if okRegisteredModelId {
			rp.Labels[constants.ModelRegistryRegisteredModelIdLabel] = desiredISVC.Labels[constants.ModelRegistryRegisteredModelIdLabel]
		} else {
			delete(rp.Labels, constants.ModelRegistryRegisteredModelIdLabel)
		}

		if okModelVersionId {
			rp.Labels[constants.ModelRegistryModelVersionIdLabel] = desiredISVC.Labels[constants.ModelRegistryModelVersionIdLabel]
		} else {
			delete(rp.Labels, constants.ModelRegistryModelVersionIdLabel)
		}

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

func (r *ModelRegistryInferenceServiceReconciler) getOrCreateServingEnvironment(log logr.Logger, mr api.ModelRegistryApi, namespace string) (*openapi.ServingEnvironment, error) {
	servingEnvironment, err := mr.GetServingEnvironmentByParams(&namespace, nil)
	if err != nil {
		log.Info("ServingEnvironment not found, creating it..")
		servingEnvironment, err = mr.UpsertServingEnvironment(&openapi.ServingEnvironment{
			Name: &namespace,
		})
		if err != nil {
			return nil, fmt.Errorf("unable to create ServingEnvironment: %w", err)
		}
	}
	return servingEnvironment, nil
}

// createMRInferenceService create a new model registry InferenceService resource based on provided input
func (r *ModelRegistryInferenceServiceReconciler) createMRInferenceService(
	log logr.Logger,
	mr api.ModelRegistryApi,
	isvc *kservev1beta1.InferenceService,
	servingEnvironmentId string,
	registeredModelId string,
	modelVersionId string,
) (*openapi.InferenceService, error) {
	modelVersionIdPtr := &modelVersionId
	if modelVersionId == "" {
		modelVersionIdPtr = nil
	}

	isName := fmt.Sprintf("%s/%s", isvc.Name, isvc.UID)

	is, err := mr.GetInferenceServiceByParams(&isName, &servingEnvironmentId, nil)
	if err != nil {
		log.Info("Creating new model registry InferenceService", "name", isName, "registeredModelId", registeredModelId, "modelVersionId", modelVersionId)
		is = &openapi.InferenceService{
			Name:                 &isName,
			ServingEnvironmentId: servingEnvironmentId,
			RegisteredModelId:    registeredModelId,
			ModelVersionId:       modelVersionIdPtr,
			Runtime:              isvc.Spec.Predictor.Model.Runtime,
			DesiredState:         openapi.INFERENCESERVICESTATE_DEPLOYED.Ptr(),
		}
		is, err = mr.UpsertInferenceService(is)
	}

	return is, err
}

// onDeletion mark model registry inference service to UNDEPLOYED desired state
func (r *ModelRegistryInferenceServiceReconciler) onDeletion(mr api.ModelRegistryApi, log logr.Logger, isvc *kservev1beta1.InferenceService, is *openapi.InferenceService) (err error) {
	log.Info("Running onDeletion logic")
	if is.DesiredState != nil && *is.DesiredState != openapi.INFERENCESERVICESTATE_UNDEPLOYED {
		log.Info("InferenceService going to be deleted from cluster, setting desired state to UNDEPLOYED in model registry")
		is.DesiredState = openapi.INFERENCESERVICESTATE_UNDEPLOYED.Ptr()

		_, err = mr.UpsertInferenceService(is)
	}
	return err
}

// initModelRegistryService setup a gRPC connection with MLMD server and initialize the model registry service
func (r *ModelRegistryInferenceServiceReconciler) initModelRegistryService(ctx context.Context, log logr.Logger, namespace string) (api.ModelRegistryApi, *grpc.ClientConn, error) {
	log1 := log.WithValues("mr-namespace", namespace)
	mlmdAddr, ok := os.LookupEnv(constants.MLMDAddressEnv)

	if !ok || mlmdAddr == "" {
		log1.Info("Retrieving mlmd address from deployed model registry service")
		// Env variable not set, look for existing model registry service
		opts := []client.ListOption{client.InNamespace(namespace), client.MatchingLabels{
			"component": "model-registry",
		}}
		mrServiceList := &corev1.ServiceList{}
		err := r.client.List(ctx, mrServiceList, opts...)
		if err != nil || len(mrServiceList.Items) == 0 {
			// No model registry deployed in the provided namespace, skipping serving reconciliation
			return nil, nil, fmt.Errorf("unable to find model registry in the given namespace: %s", namespace)
		}

		// Actually we could iterate over every mrService, as nothing prevents to setup multiple MR in the same namespace
		if len(mrServiceList.Items) > 1 {
			return nil, nil, fmt.Errorf("multiple services with component=model-registry for Namespace %s", namespace)
		}

		mrService := mrServiceList.Items[0]

		var grpcPort *int32
		for _, port := range mrService.Spec.Ports {
			if port.Name == "grpc-api" {
				grpcPort = &port.Port
				break
			}
		}

		if grpcPort == nil {
			return nil, nil, fmt.Errorf("cannot find grpc-api port for service %s", mrService.Name)
		}

		mlmdAddr = fmt.Sprintf("%s.%s.svc.cluster.local:%d", mrService.Name, namespace, *grpcPort)
	}

	if mlmdAddr == "" {
		log1.Info("Cannot connect to the model registry: empty mlmd address")
		return nil, nil, nil
	}

	// Setup model registry service
	log.Info("Connecting to " + mlmdAddr)
	conn, err := grpc.DialContext(
		ctx,
		mlmdAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, nil, err
	}

	mr, err := core.NewModelRegistryService(conn)
	return mr, conn, err
}

func IgnoreDeletingErrors(err error) error {
	if err == nil {
		return nil
	}
	if apierrs.IsNotFound(err) {
		return nil
	}
	return err
}
