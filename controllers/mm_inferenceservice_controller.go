package controllers

import (
	"context"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
)

func (r *OpenshiftInferenceServiceReconciler) ReconcileModelMeshInference(ctx context.Context, req ctrl.Request, inferenceService *kservev1beta1.InferenceService) error {
	// Initialize logger format
	log := r.Log.WithValues("InferenceService", inferenceService.Name, "namespace", inferenceService.Namespace)

	log.Info("Reconciling Route for InferenceService")
	err := r.ReconcileRoute(inferenceService, ctx)
	if err != nil {
		return err
	}

	log.Info("Reconciling ServiceAccount for InferenceSercvice")
	err = r.ReconcileSA(inferenceService, ctx)
	if err != nil {
		return err
	}
	return nil
}
