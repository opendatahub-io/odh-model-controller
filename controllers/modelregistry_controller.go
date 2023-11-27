package controllers

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	"github.com/opendatahub-io/model-registry/pkg/core"
	"github.com/opendatahub-io/odh-model-controller/controllers/reconcilers"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	corev1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// ModelRegistryReconciler holds the model registry controller configuration.
type ModelRegistryReconciler struct {
	Client         client.Client
	Scheme         *runtime.Scheme
	Log            logr.Logger
	Period         time.Duration
	mrISReconciler *reconcilers.ModelRegistryInferenceServiceReconciler
	mrSEReconciler *reconcilers.ModelRegistryServingEnvironmentReconciler
}

func NewModelRegistryReconciler(client client.Client, scheme *runtime.Scheme, log logr.Logger, period int) *ModelRegistryReconciler {
	return &ModelRegistryReconciler{
		Client:         client,
		Scheme:         scheme,
		Log:            log,
		Period:         time.Duration(period) * time.Second,
		mrISReconciler: reconcilers.NewModelRegistryInferenceServiceReconciler(client),
		mrSEReconciler: reconcilers.NewModelRegistryServingEnvironmentReconciler(client),
	}
}

func (r *ModelRegistryReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Initialize logger format
	log := r.Log.WithValues("ServingRuntime", req.Name, "namespace", req.Namespace)
	log.Info("Reconciling ModelRegistry serving for ServingRuntime: " + req.Name)

	// Check if model registry exists in this namespace, lookup the service by label

	opts := []client.ListOption{client.InNamespace(req.Namespace), client.MatchingLabels{
		"component": "model-registry",
	}}
	mrServiceList := &corev1.ServiceList{}
	err := r.Client.List(ctx, mrServiceList, opts...)
	if err != nil && apierrs.IsNotFound(err) {
		// No model registry deployed in the provided namespace, skipping serving reconciliation
		log.Info("Stop ModelRegistry serving reconciliation")
		return ctrl.Result{}, nil
	}

	if len(mrServiceList.Items) == 0 {
		log.Info("No Model Registry service found for Namespace: " + req.Namespace)
		log.Info("Stop ModelRegistry serving reconciliation")
		return ctrl.Result{}, nil
	}

	// Actually we could iterate over every mrService, as nothing prevents to setup multiple MR in the same namespace
	if len(mrServiceList.Items) > 1 {
		log.Error(fmt.Errorf("multiple services with component=model-registry for Namespace %s", req.Namespace), "Stop ModelRegistry serving reconciliation")
		return ctrl.Result{}, nil
	}

	mrService := mrServiceList.Items[0]

	// Setup model registry service

	// setup grpc connection to ml-metadata
	ctxTimeout, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	var grpcPort *int32
	for _, port := range mrService.Spec.Ports {
		if port.Name == "grpc-api" {
			grpcPort = &port.Port
			break
		}
	}

	if grpcPort == nil {
		log.Error(fmt.Errorf("cannot find grpc-api port for service %s", mrService.Name), "Stop ModelRegistry serving reconciliation")
		return ctrl.Result{}, nil
	}

	mlmdAddr := fmt.Sprintf("%s.%s.svc.cluster.local:%d", mrService.Name, req.Namespace, *grpcPort)
	log.Info("Connecting to " + mlmdAddr + "...")
	conn, err := grpc.DialContext(
		ctxTimeout,
		mlmdAddr,
		grpc.WithReturnConnectionError(),
		grpc.WithBlock(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		log.Error(err, "Stop ModelRegistry serving reconciliation")
		return ctrl.Result{}, nil
	}
	defer conn.Close()

	mr, err := core.NewModelRegistryService(conn)
	if err != nil {
		log.Error(err, "Stop ModelRegistry serving reconciliation")
		return ctrl.Result{}, nil
	}

	// Reconcile the ServingEnvironment from Model Registry
	err = r.mrSEReconciler.Reconcile(ctx, log, mr, req.Namespace)
	if err != nil {
		log.Error(err, "Stop ModelRegistry serving reconciliation")
		return ctrl.Result{}, nil
	}

	// Reconcile the InferenceService from Model Registry
	err = r.mrISReconciler.Reconcile(ctx, log, req.Namespace, mr, req.Name)
	if err != nil {
		log.Error(err, "Stop ModelRegistry serving reconciliation")
		return ctrl.Result{}, nil
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ModelRegistryReconciler) SetupWithManager(mgr ctrl.Manager) error {

	builder := ctrl.NewControllerManagedBy(mgr).
		For(&kservev1alpha1.ServingRuntime{})

	if r.Period != 0 {
		sourceEventChannel := make(chan event.GenericEvent)

		// periodically (r.period) check every namespace and for every ServingRuntime CR perform
		// reconciliation based on the model registry content
		go func() {
			ticker := time.NewTicker(r.Period)
			defer ticker.Stop()

			for range ticker.C {

				servingRuntimeList := &kservev1alpha1.ServingRuntimeList{}
				err := r.Client.List(context.TODO(), servingRuntimeList)
				if err != nil {
					r.Log.Info("Error getting list of ServingRuntime")
					continue
				}

				if len(servingRuntimeList.Items) == 0 {
					r.Log.Info("No ServingRuntime found across all namespaces")
					continue
				}

				for _, sr := range servingRuntimeList.Items {
					// need to use tmp var otherwise just the last ServingRuntime in the list would be added/triggered
					obj := sr
					sourceEventChannel <- event.GenericEvent{Object: &obj}
				}
			}

		}()

		builder = builder.Watches(&source.Channel{Source: sourceEventChannel}, &handler.EnqueueRequestForObject{})
	}

	err := builder.Complete(r)
	if err != nil {
		return err
	}

	return nil
}
