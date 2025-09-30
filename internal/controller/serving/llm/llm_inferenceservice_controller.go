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
	"github.com/hashicorp/go-multierror"
	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kuadrantv1 "github.com/kuadrant/kuadrant-operator/api/v1"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/resources"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/serving/llm/reconcilers"
	parentreconcilers "github.com/opendatahub-io/odh-model-controller/internal/controller/serving/reconcilers"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlbuilder "sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type LLMInferenceServiceReconciler struct {
	client.Client
	Scheme                 *runtime.Scheme
	subResourceReconcilers []parentreconcilers.LLMSubResourceReconciler
	authPolicyMatcher      resources.AuthPolicyMatcher
}

func NewLLMInferenceServiceReconciler(client client.Client, scheme *runtime.Scheme, config *rest.Config) *LLMInferenceServiceReconciler {
	var subResourceReconcilers []parentreconcilers.LLMSubResourceReconciler

	if ok, err := utils.IsCrdAvailable(config, kuadrantv1.GroupVersion.String(), "AuthPolicy"); err == nil && ok {
		subResourceReconcilers = append(subResourceReconcilers, reconcilers.NewKserveAuthPolicyReconciler(client, scheme))
	}

	return &LLMInferenceServiceReconciler{
		Client:                 client,
		Scheme:                 scheme,
		subResourceReconcilers: subResourceReconcilers,
		authPolicyMatcher:      resources.NewKServeAuthPolicyMatcher(client),
	}
}

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
		if err := r.DeleteResourcesIfNoLLMIsvcExists(ctx, logger, llmisvc.Namespace); err != nil {
			logger.Error(err, "Failed to cleanup namespace resources")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	if err := r.reconcileSubResources(ctx, logger, llmisvc); err != nil {
		logger.Error(err, "Failed to reconcile LLMInferenceService sub-resources")
		return ctrl.Result{}, err
	}

	logger.V(1).Info("Successfully reconciled LLMInferenceService")
	return ctrl.Result{}, nil
}

// +kubebuilder:rbac:groups=serving.kserve.io,resources=llminferenceservices,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=serving.kserve.io,resources=llminferenceservices/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=serving.kserve.io,resources=llminferenceservices/finalizers,verbs=update
// +kubebuilder:rbac:groups=kuadrant.io,resources=authpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kuadrant.io,resources=authpolicies/status,verbs=get;update;patch

func (r *LLMInferenceServiceReconciler) SetupWithManager(mgr ctrl.Manager, setupLog logr.Logger) error {
	b := ctrl.NewControllerManagedBy(mgr).
		For(&kservev1alpha1.LLMInferenceService{}).
		Named("llminferenceservice")

	setupLog.Info("Setting up LLMInferenceService controller")

	if ok, err := utils.IsCrdAvailable(mgr.GetConfig(), kuadrantv1.GroupVersion.String(), "AuthPolicy"); err != nil {
		setupLog.Error(err, "Failed to check CRD availability for AuthPolicy")
	} else if ok {
		b = b.Watches(&kuadrantv1.AuthPolicy{},
			r.enqueueOnAuthPolicyChange(),
			ctrlbuilder.WithPredicates(predicate.Funcs{
				CreateFunc: func(e event.CreateEvent) bool {
					return false
				},
				UpdateFunc: func(e event.UpdateEvent) bool {
					return hasOpenDataHubManagedLabel(e.ObjectNew)
				},
				DeleteFunc: func(e event.DeleteEvent) bool {
					return hasOpenDataHubManagedLabel(e.Object)
				},
			}))
	}

	return b.Complete(r)
}

func hasOpenDataHubManagedLabel(obj client.Object) bool {
	if labels := obj.GetLabels(); labels != nil {
		return labels["opendatahub.io/managed"] == "true"
	}
	return false
}

func (r *LLMInferenceServiceReconciler) enqueueOnAuthPolicyChange() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, object client.Object) []reconcile.Request {
		authPolicy := object.(*kuadrantv1.AuthPolicy)
		targetKind := authPolicy.Spec.TargetRef.Kind

		if targetKind == "HTTPRoute" {
			if namespacedName, found := r.authPolicyMatcher.FindLLMServiceFromHTTPRouteAuthPolicy(authPolicy); found {
				return []reconcile.Request{{NamespacedName: namespacedName}}
			}
		}

		if namespacedNames, err := r.authPolicyMatcher.FindLLMServiceFromGatewayAuthPolicy(ctx, authPolicy); err == nil && len(namespacedNames) > 0 {
			requests := make([]reconcile.Request, len(namespacedNames))
			for i, namespacedName := range namespacedNames {
				requests[i] = reconcile.Request{NamespacedName: namespacedName}
			}
			return requests
		}
		return []reconcile.Request{}
	})
}

func (r *LLMInferenceServiceReconciler) onDeletion(ctx context.Context, log logr.Logger, llmisvc *kservev1alpha1.LLMInferenceService) error {
	log.V(1).Info("Triggering Delete for LLMInferenceService", "name", llmisvc.Name)
	var deleteErrors *multierror.Error

	for _, subReconciler := range r.subResourceReconcilers {
		if err := subReconciler.Delete(ctx, log, llmisvc); err != nil {
			log.Error(err, "Failed to delete sub-resource")
			deleteErrors = multierror.Append(deleteErrors, err)
		}
	}

	return deleteErrors.ErrorOrNil()
}

func (r *LLMInferenceServiceReconciler) reconcileSubResources(ctx context.Context, log logr.Logger, llmisvc *kservev1alpha1.LLMInferenceService) error {
	log.V(1).Info("Reconciling LLMInferenceService sub-resources")
	var reconcileErrors *multierror.Error

	for _, subReconciler := range r.subResourceReconcilers {
		if err := subReconciler.Reconcile(ctx, log, llmisvc); err != nil {
			log.Error(err, "Failed to reconcile sub-resource")
			reconcileErrors = multierror.Append(reconcileErrors, err)
		}
	}

	return reconcileErrors.ErrorOrNil()
}

func (r *LLMInferenceServiceReconciler) Cleanup(ctx context.Context, log logr.Logger, isvcNs string) error {
	log.V(1).Info("Cleaning up LLMInferenceService sub-resources", "namespace", isvcNs)
	var cleanupErrors *multierror.Error

	for _, subReconciler := range r.subResourceReconcilers {
		if err := subReconciler.Cleanup(ctx, log, isvcNs); err != nil {
			log.Error(err, "Failed to cleanup sub-resource")
			cleanupErrors = multierror.Append(cleanupErrors, err)
		}
	}

	return cleanupErrors.ErrorOrNil()
}

func (r *LLMInferenceServiceReconciler) DeleteResourcesIfNoLLMIsvcExists(ctx context.Context, log logr.Logger, namespace string) error {
	llmInferenceServiceList := &kservev1alpha1.LLMInferenceServiceList{}
	if err := r.Client.List(ctx, llmInferenceServiceList, client.InNamespace(namespace)); err != nil {
		return err
	}

	var existingLLMIsvcs []kservev1alpha1.LLMInferenceService
	for _, llmisvc := range llmInferenceServiceList.Items {
		if llmisvc.GetDeletionTimestamp() == nil {
			existingLLMIsvcs = append(existingLLMIsvcs, llmisvc)
		}
	}

	if len(existingLLMIsvcs) == 0 {
		log.V(1).Info("Triggering LLMInferenceService Cleanup for Namespace", "namespace", namespace)
		if err := r.Cleanup(ctx, log, namespace); err != nil {
			return err
		}
	}

	return nil
}
