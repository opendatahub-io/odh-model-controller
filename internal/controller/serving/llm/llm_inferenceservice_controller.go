/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package llm

import (
	"context"

	"github.com/go-logr/logr"
	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/serving/reconcilers"
)

type LLMInferenceServiceReconciler struct {
	client.Client
	Scheme            *runtime.Scheme
	kClient           kubernetes.Interface
	restConfig        *rest.Config
	llmIsvcReconciler *reconcilers.KserveLLMInferenceServiceReconciler
}

func NewLLMInferenceServiceReconciler(client client.Client, scheme *runtime.Scheme,
	kClient kubernetes.Interface, restConfig *rest.Config) *LLMInferenceServiceReconciler {

	return &LLMInferenceServiceReconciler{
		Client:            client,
		Scheme:            scheme,
		kClient:           kClient,
		restConfig:        restConfig,
		llmIsvcReconciler: reconcilers.NewKServeLLMInferenceServiceReconciler(client, kClient, restConfig),
	}
}

// +kubebuilder:rbac:groups=serving.kserve.io,resources=llminferenceservices,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=serving.kserve.io,resources=llminferenceservices/finalizers,verbs=get;list;watch;update;create;patch;delete
// +kubebuilder:rbac:groups=kuadrant.io,resources=authpolicies,verbs=get;list;watch;create;update;patch;delete
func (r *LLMInferenceServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("LLMInferenceService", req.Name, "namespace", req.Namespace)

	llmisvc := &kservev1alpha1.LLMInferenceService{}
	err := r.Client.Get(ctx, req.NamespacedName, llmisvc)
	if err != nil && apierrs.IsNotFound(err) {
		logger.Info("Stop LLMInferenceService reconciliation")
		return ctrl.Result{}, nil
	} else if err != nil {
		logger.Error(err, "Unable to fetch the LLMInferenceService")
		return ctrl.Result{}, err
	}

	if !llmisvc.GetDeletionTimestamp().IsZero() {
		logger.Info("LLMInferenceService being deleted, cleaning up sub-resources")
		if err := r.onDeletion(ctx, logger, llmisvc); err != nil {
			logger.Error(err, "Failed to cleanup sub-resources during LLMInferenceService deletion")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Main reconciliation logic for all sub-resources
	if err := r.llmIsvcReconciler.Reconcile(ctx, logger, llmisvc); err != nil {
		logger.Error(err, "Failed to reconcile LLMInferenceService sub-resources")
		return ctrl.Result{}, err
	}

	logger.V(1).Info("Successfully reconciled LLMInferenceService")
	return ctrl.Result{}, nil
}

func (r *LLMInferenceServiceReconciler) SetupWithManager(mgr ctrl.Manager, setupLog logr.Logger) error {
	builder := ctrl.NewControllerManagedBy(mgr).
		For(&kservev1alpha1.LLMInferenceService{}).
		Named("llminferenceservice")

	setupLog.Info("Setting up LLMInferenceService controller")

	return builder.Complete(r)
}

func (r *LLMInferenceServiceReconciler) onDeletion(ctx context.Context, log logr.Logger, llmisvc *kservev1alpha1.LLMInferenceService) error {
	log.V(1).Info("Triggering Delete for LLMInferenceService: " + llmisvc.Name)

	if err := r.llmIsvcReconciler.Delete(ctx, log, llmisvc); err != nil {
		log.Error(err, "Failed to delete sub-resources during LLMInferenceService deletion")
		return err
	}

	return nil
}
