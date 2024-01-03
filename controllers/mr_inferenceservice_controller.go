package controllers

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/uuid"
	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"github.com/opendatahub-io/model-registry/pkg/api"
	"github.com/opendatahub-io/model-registry/pkg/core"
	"github.com/opendatahub-io/model-registry/pkg/openapi"
	"github.com/opendatahub-io/odh-model-controller/controllers/comparators"
	"github.com/opendatahub-io/odh-model-controller/controllers/constants"
	"github.com/opendatahub-io/odh-model-controller/controllers/processors"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	corev1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const modelRegistryFinalizer = "modelregistry.opendatahub.io/finalizer"

// ModelRegistryInferenceServiceReconciler holds the controller configuration.
type ModelRegistryInferenceServiceReconciler struct {
	client         client.Client
	scheme         *runtime.Scheme
	log            logr.Logger
	deltaProcessor processors.DeltaProcessor
}

func NewModelRegistryInferenceServiceReconciler(client client.Client, scheme *runtime.Scheme, log logr.Logger) *ModelRegistryInferenceServiceReconciler {
	return &ModelRegistryInferenceServiceReconciler{
		client:         client,
		scheme:         scheme,
		log:            log,
		deltaProcessor: processors.NewDeltaProcessor(),
	}
}

// Reconcile performs the reconciliation of the model registry based on Kubeflow InferenceService CRs
func (r *ModelRegistryInferenceServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Initialize logger format
	log := r.log.WithValues("ModelRegistryInferenceService", req.Name, "namespace", req.Namespace)

	mr, err := r.initModelRegistryService(ctx, log, req.Namespace)
	if err != nil {
		log.Error(err, "Stop ModelRegistry InferenceService reconciliation")
		return ctrl.Result{Requeue: true, RequeueAfter: time.Second * 5}, err
	}

	servingEnvironment, err := mr.GetServingEnvironmentByParams(&req.Namespace, nil)
	if err != nil || servingEnvironment.Id == nil {
		log.Error(err, "Stop ModelRegistry InferenceService reconciliation")
		return ctrl.Result{Requeue: true, RequeueAfter: time.Second * 5}, err
	}

	// Get the InferenceService object when a reconciliation event is triggered (create,
	// update, delete)
	isvc := &kservev1beta1.InferenceService{}
	err = r.client.Get(ctx, req.NamespacedName, isvc)

	if err != nil && apierrs.IsNotFound(err) {
		log.Error(err, "Stop ModelRegistry InferenceService reconciliation")
		return ctrl.Result{}, nil
	} else if err != nil {
		log.Error(err, "Unable to fetch the InferenceService")
		return ctrl.Result{}, err
	}

	// Let's add a finalizer. Then, we can define some operations which should
	// occurs before the custom resource to be deleted.
	// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/finalizers
	if !controllerutil.ContainsFinalizer(isvc, modelRegistryFinalizer) {
		log.Info("Adding Finalizer for ModelRegistry")
		if ok := controllerutil.AddFinalizer(isvc, modelRegistryFinalizer); !ok {
			log.Error(err, "Failed to add finalizer into the InferenceService custom resource")
			return ctrl.Result{Requeue: true}, nil
		}

		if err = r.client.Update(ctx, isvc); err != nil {
			log.Error(err, "Failed to update InferenceService custom resource to add finalizer")
			return ctrl.Result{}, err
		}
	}

	var is *openapi.InferenceService
	isId, okIsId := isvc.Labels[constants.ModelRegistryInferenceServiceIdLabel]
	registeredModelId, okRegisteredModelId := isvc.Labels[constants.ModelRegistryRegisteredModelIdLabel]
	modelVersionId, okModelVersionId := isvc.Labels[constants.ModelRegistryModelVersionIdLabel]

	if okIsId {
		// Retrieve the IS from model registry using the id
		log.Info("Retrieving model registry InferenceService by id", "isId", isId)
		is, err = mr.GetInferenceServiceById(isId)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("unable to find InferenceService with id %s in model registry: %w", isId, err)
		}
	} else if okRegisteredModelId || okModelVersionId {
		// No corresponding InferenceService in model registry, create new one
		is, err = r.createMRInferenceService(log, mr, isvc, *servingEnvironment.Id, registeredModelId, modelVersionId)
		if err != nil {
			return ctrl.Result{}, err
		}
	} else {
		// No expected labels set in the ISVC
		log.Error(fmt.Errorf("missing label, unable to link ISVC to Model Registry, skipping InferenceService"), "Stop ModelRegistry InferenceService reconciliation")
		return ctrl.Result{}, nil
	}

	if is == nil {
		// This should NOT happen
		return ctrl.Result{}, fmt.Errorf("unexpected nil model registry InferenceService")
	}

	// Check if the InferenceService instance is marked to be deleted, which is
	// indicated by the deletion timestamp being set.
	isMarkedToBeDeleted := isvc.GetDeletionTimestamp() != nil
	if isMarkedToBeDeleted {
		if controllerutil.ContainsFinalizer(isvc, modelRegistryFinalizer) {
			log.Info("InferenceService going to be deleted from cluster, setting desired state to UNDEPLOYED in model registry")
			err := r.onDeletion(mr, log, isvc, is)
			if err != nil {
				return ctrl.Result{Requeue: true}, err
			}

			log.Info("Removing Finalizer for modelRegistry after successfully perform the operations")
			if ok := controllerutil.RemoveFinalizer(isvc, modelRegistryFinalizer); !ok {
				log.Error(err, "Failed to remove modelRegistry finalizer for InferenceService")
				return ctrl.Result{Requeue: true}, nil
			}

			if err = r.client.Update(ctx, isvc); IgnoreDeletingErrors(err) != nil {
				log.Error(err, "Failed to remove modelRegistry finalizer for InferenceService")
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	} else {
		// Update the ISVC label, set the newly created IS id if not present yet
		desired := isvc.DeepCopy()
		desired.Labels[constants.ModelRegistryInferenceServiceIdLabel] = *is.Id
		delete(desired.Labels, constants.ModelRegistryRegisteredModelIdLabel)
		delete(desired.Labels, constants.ModelRegistryModelVersionIdLabel)

		err = r.processDelta(ctx, log, desired, isvc)
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ModelRegistryInferenceServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	builder := ctrl.NewControllerManagedBy(mgr).
		For(&kservev1beta1.InferenceService{}).
		Owns(&kservev1alpha1.ServingRuntime{}).
		Owns(&corev1.Namespace{}).
		Owns(&monitoringv1.PodMonitor{}).
		Watches(&source.Kind{Type: &kservev1alpha1.ServingRuntime{}},
			handler.EnqueueRequestsFromMapFunc(func(o client.Object) []reconcile.Request {
				r.log.Info("Reconcile event triggered by serving runtime: " + o.GetName())
				inferenceServicesList := &kservev1beta1.InferenceServiceList{}
				opts := []client.ListOption{client.InNamespace(o.GetNamespace())}

				// Todo: Get only Inference Services that are deploying on the specific serving runtime
				err := r.client.List(context.TODO(), inferenceServicesList, opts...)
				if err != nil {
					r.log.Info("Error getting list of inference services for namespace")
					return []reconcile.Request{}
				}

				if len(inferenceServicesList.Items) == 0 {
					r.log.Info("No InferenceServices found for Serving Runtime: " + o.GetName())
					return []reconcile.Request{}
				}

				reconcileRequests := make([]reconcile.Request, 0, len(inferenceServicesList.Items))
				for _, inferenceService := range inferenceServicesList.Items {
					reconcileRequests = append(reconcileRequests, reconcile.Request{
						NamespacedName: types.NamespacedName{
							Name:      inferenceService.Name,
							Namespace: inferenceService.Namespace,
						},
					})
				}
				return reconcileRequests
			}))

	return builder.Complete(r)
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

// createMRInferenceService create a new model registry InferenceService resource based on provided input
func (r *ModelRegistryInferenceServiceReconciler) createMRInferenceService(log logr.Logger, mr api.ModelRegistryApi, isvc *kservev1beta1.InferenceService, servingEnvironmentId string, registeredModelId string, modelVersionId string) (*openapi.InferenceService, error) {
	modelVersionIdPtr := &modelVersionId
	if modelVersionId == "" {
		modelVersionIdPtr = nil
	}

	isName := fmt.Sprintf("%s/%s", isvc.Name, uuid.New().String())

	log.Info("Creating new model registry InferenceService", "name", isName, "registeredModelId", registeredModelId, "modelVersionId", modelVersionId)
	is := &openapi.InferenceService{
		Name:                 &isName,
		ServingEnvironmentId: servingEnvironmentId,
		RegisteredModelId:    registeredModelId,
		ModelVersionId:       modelVersionIdPtr,
		Runtime:              isvc.Spec.Predictor.Model.Runtime,
	}

	return mr.UpsertInferenceService(is)
}

// onDeletion mark model registry inference service to UNDEPLOYED desired state
func (r *ModelRegistryInferenceServiceReconciler) onDeletion(mr api.ModelRegistryApi, log logr.Logger, isvc *kservev1beta1.InferenceService, is *openapi.InferenceService) error {
	log.Info("Running onDeletion logic")
	is.DesiredState = openapi.INFERENCESERVICESTATE_UNDEPLOYED.Ptr()

	_, err := mr.UpsertInferenceService(is)
	return err
}

// initModelRegistryService setup a gRPC connection with MLMD server and initialize the model registry service
func (r *ModelRegistryInferenceServiceReconciler) initModelRegistryService(ctx context.Context, log logr.Logger, namespace string) (api.ModelRegistryApi, error) {
	mlmdAddr, ok := os.LookupEnv(constants.MLMDAddressEnv)

	if !ok || mlmdAddr == "" {
		log.Info("Retrieving mlmd address from deployed model registry service..")
		// Env variable not set, look for existing model registry service
		opts := []client.ListOption{client.InNamespace(namespace), client.MatchingLabels{
			"component": "model-registry",
		}}
		mrServiceList := &corev1.ServiceList{}
		err := r.client.List(ctx, mrServiceList, opts...)
		if err != nil || len(mrServiceList.Items) == 0 {
			// No model registry deployed in the provided namespace, skipping serving reconciliation
			return nil, fmt.Errorf("unable to find model registry in the given namespace: %s", namespace)
		}

		// Actually we could iterate over every mrService, as nothing prevents to setup multiple MR in the same namespace
		if len(mrServiceList.Items) > 1 {
			return nil, fmt.Errorf("multiple services with component=model-registry for Namespace %s", namespace)
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
			return nil, fmt.Errorf("cannot find grpc-api port for service %s", mrService.Name)
		}

		mlmdAddr = fmt.Sprintf("%s.%s.svc.cluster.local:%d", mrService.Name, namespace, *grpcPort)
	}

	if mlmdAddr == "" {
		return nil, fmt.Errorf("cannot connect to the model registry: empty mlmd address")
	}
	// setup grpc connection to ml-metadata
	ctxTimeout, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	// Setup model registry service
	log.Info("Connecting to " + mlmdAddr)
	conn, err := grpc.DialContext(
		ctxTimeout,
		mlmdAddr,
		grpc.WithReturnConnectionError(),
		grpc.WithBlock(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, err
	}

	return core.NewModelRegistryService(conn)
}

func IgnoreDeletingErrors(err error) error {
	if err == nil {
		return nil
	}
	if apierrs.IsNotFound(err) || apierrs.IsConflict(err) {
		return nil
	}
	return err
}
