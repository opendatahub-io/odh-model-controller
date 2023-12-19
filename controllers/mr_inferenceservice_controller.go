package controllers

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/go-logr/logr"
	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"github.com/opendatahub-io/model-registry/pkg/api"
	"github.com/opendatahub-io/model-registry/pkg/core"
	"github.com/opendatahub-io/model-registry/pkg/openapi"
	"github.com/opendatahub-io/odh-model-controller/controllers/constants"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	corev1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// ModelRegistryInferenceServiceReconciler holds the controller configuration.
type ModelRegistryInferenceServiceReconciler struct {
	client client.Client
	scheme *runtime.Scheme
	log    logr.Logger
}

func NewModelRegistryInferenceServiceReconciler(client client.Client, scheme *runtime.Scheme, log logr.Logger, meshDisabled bool) *ModelRegistryInferenceServiceReconciler {
	return &ModelRegistryInferenceServiceReconciler{
		client: client,
		scheme: scheme,
		log:    log,
	}
}

// Reconcile performs the reconciliation of the model registry based on Kubeflow InferenceService CRs
func (r *ModelRegistryInferenceServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Initialize logger format
	log := r.log.WithValues("ModelRegistryInferenceService", req.Name, "namespace", req.Namespace)

	mr, err := r.initModelRegistryService(log)
	if err != nil {
		log.Error(err, "Stop ModelRegistry InferenceService reconciliation")
		return ctrl.Result{}, nil
	}

	servingEnvironment, err := mr.GetServingEnvironmentByParams(&req.Namespace, nil)
	if err != nil || servingEnvironment.Id == nil {
		log.Error(err, "Stop ModelRegistry InferenceService reconciliation")
		return ctrl.Result{}, err
	}

	// Get the InferenceService object when a reconciliation event is triggered (create,
	// update, delete)
	isvc := &kservev1beta1.InferenceService{}
	err = r.client.Get(ctx, req.NamespacedName, isvc)

	if err != nil && apierrs.IsNotFound(err) {
		log.Error(err, "Stop ModelRegistry InferenceService reconciliation")
		// TODO(user): Update IS resource in model registry (change state to UNDEPLOYED)
		return ctrl.Result{}, nil
	} else if err != nil {
		log.Error(err, "Unable to fetch the InferenceService")
		return ctrl.Result{}, err
	}

	// Retrieve or create a new InferenceService
	is, err := mr.GetInferenceServiceByParams(&isvc.Name, servingEnvironment.Id, nil)
	if err != nil {
		var err1 error
		if isId, ok := isvc.Labels[constants.ModelRegistryInferenceServiceIdLabel]; ok {
			// Retrieve the IS from model registry using the id
			is, err1 = mr.GetInferenceServiceById(isId)
		} else if registeredModelId, ok := isvc.Labels[constants.ModelRegistryRegisteredModelIdLabel]; ok {
			// Create new IS in model registry auditing the deployment of the latest or specific version of a registered model
			modelVersionId := isvc.Labels[constants.ModelRegistryModelVersionIdLabel]
			is, err1 = r.createMRInferenceService(mr, isvc, *servingEnvironment.Id, registeredModelId, &modelVersionId)
		} else {
			err1 = fmt.Errorf("unable to find a link to the model registry InferenceService resource")
		}

		if err1 != nil {
			return ctrl.Result{}, err1
		}
	}

	if isvc.GetDeletionTimestamp() != nil {
		return ctrl.Result{}, r.onDeletion(mr, log, isvc, is)
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

// createMRInferenceService create a new model registry InferenceService resource based on provided input
func (r *ModelRegistryInferenceServiceReconciler) createMRInferenceService(mr api.ModelRegistryApi, isvc *kservev1beta1.InferenceService, servingEnvironmentId string, registeredModelId string, modelVersionId *string) (*openapi.InferenceService, error) {
	if modelVersionId != nil && *modelVersionId == "" {
		modelVersionId = nil
	}

	is := &openapi.InferenceService{
		Name:                 &isvc.Name,
		ServingEnvironmentId: servingEnvironmentId,
		RegisteredModelId:    registeredModelId,
		ModelVersionId:       modelVersionId,
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
func (r *ModelRegistryInferenceServiceReconciler) initModelRegistryService(log logr.Logger) (api.ModelRegistryApi, error) {
	// fetch MLMD address from environment
	mlmdAddr := os.Getenv(constants.MLMDAddressEnv)

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
