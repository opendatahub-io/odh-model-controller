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
	"fmt"
	"os"

	"github.com/go-logr/logr"
	"github.com/hashicorp/go-multierror"
	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kservellmisvc "github.com/kserve/kserve/pkg/controller/llmisvc"
	kuadrantv1 "github.com/kuadrant/kuadrant-operator/api/v1"
	istioclientv1alpha3 "istio.io/client-go/pkg/apis/networking/v1alpha3"
	v1 "k8s.io/api/rbac/v1"
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

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/resources"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/serving/llm/reconcilers"
	parentreconcilers "github.com/opendatahub-io/odh-model-controller/internal/controller/serving/reconcilers"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
)

type LLMInferenceServiceReconciler struct {
	client.Client
	Scheme                 *runtime.Scheme
	subResourceReconcilers []parentreconcilers.LLMSubResourceReconciler
	authPolicyMatcher      resources.AuthPolicyMatcher
	envoyFilterMatcher     resources.EnvoyFilterMatcher
}

var ownedBySelfPredicate = predicate.NewPredicateFuncs(func(o client.Object) bool {
	return o.GetLabels()["app.kubernetes.io/managed-by"] == "odh-model-controller"
})

func NewLLMInferenceServiceReconciler(client client.Client, scheme *runtime.Scheme, config *rest.Config) *LLMInferenceServiceReconciler {
	var subResourceReconcilers []parentreconcilers.LLMSubResourceReconciler
	subResourceReconcilers = append(subResourceReconcilers,
		parentreconcilers.NewLLMRoleReconciler(client),
		parentreconcilers.NewLLMRoleBindingReconciler(client),
	)

	if ok, err := utils.IsCrdAvailable(config, kuadrantv1.GroupVersion.String(), constants.AuthPolicyKind); err == nil && ok {
		subResourceReconcilers = append(subResourceReconcilers,
			reconcilers.NewKserveAuthPolicyReconciler(client, scheme),
		)
	}
	if ok, err := utils.IsCrdAvailable(config, istioclientv1alpha3.SchemeGroupVersion.String(), constants.EnvoyFilterKind); err == nil && ok {
		subResourceReconcilers = append(subResourceReconcilers,
			reconcilers.NewKserveEnvoyFilterReconciler(client, scheme),
		)
	}

	return &LLMInferenceServiceReconciler{
		Client:                 client,
		Scheme:                 scheme,
		subResourceReconcilers: subResourceReconcilers,
		authPolicyMatcher:      resources.NewKServeAuthPolicyMatcher(client),
		envoyFilterMatcher:     resources.NewKServeEnvoyFilterMatcher(client),
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

	// TODO: Reuse logic in Kserve, currently private and not very reusable.
	specs := make([]kservev1alpha1.LLMInferenceServiceSpec, 0, 1)
	for _, ref := range llmisvc.Spec.BaseRefs {
		cfg, err := r.getConfig(ctx, llmisvc, ref.Name)
		if err != nil {
			logger.Error(err, "Failed to fetch the config")
			return ctrl.Result{}, fmt.Errorf("failed to get config: %w", err)
		}
		if cfg != nil {
			specs = append(specs, cfg.Spec)
		}
	}
	// Append the service's own spec last so it takes precedence
	specs = append(specs, llmisvc.Spec)

	spec, err := kservellmisvc.MergeSpecs(ctx, specs...)
	if err != nil {
		logger.Error(err, "Failed to merge specs")
		return ctrl.Result{}, fmt.Errorf("failed to merge specs: %w", err)
	}
	// Do not override spec
	llmisvc = llmisvc.DeepCopy()
	llmisvc.Spec = spec

	if err := r.reconcileSubResources(ctx, logger, llmisvc); err != nil {
		logger.Error(err, "Failed to reconcile LLMInferenceService sub-resources")
		return ctrl.Result{}, err
	}

	logger.V(1).Info("Successfully reconciled LLMInferenceService")
	return ctrl.Result{}, nil
}

// +kubebuilder:rbac:groups=serving.kserve.io,resources=llminferenceservices,verbs=get;list;watch;update;patch;post
// +kubebuilder:rbac:groups=serving.kserve.io,resources=llminferenceservices/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=serving.kserve.io,resources=llminferenceservices/finalizers,verbs=update;patch
// +kubebuilder:rbac:groups=serving.kserve.io,resources=llminferenceserviceconfigs,verbs=get;list;watch
// +kubebuilder:rbac:groups=kuadrant.io,resources=authpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kuadrant.io,resources=authpolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=rolebindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.istio.io,resources=envoyfilters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gateways,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gateways/finalizers,verbs=update;patch
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes,verbs=get;list;watch
// +kubebuilder:rbac:groups=config.openshift.io,resources=authentications,verbs=get;list;watch

func (r *LLMInferenceServiceReconciler) SetupWithManager(mgr ctrl.Manager, setupLog logr.Logger) error {
	b := ctrl.NewControllerManagedBy(mgr).
		For(&kservev1alpha1.LLMInferenceService{}).
		Owns(&v1.Role{}, ctrlbuilder.WithPredicates(ownedBySelfPredicate)).
		Owns(&v1.RoleBinding{}, ctrlbuilder.WithPredicates(ownedBySelfPredicate)).
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
					return utils.IsManagedByOpenDataHub(e.ObjectNew)
				},
				DeleteFunc: func(e event.DeleteEvent) bool {
					return utils.IsManagedByOpenDataHub(e.Object)
				},
			}))
	}

	if ok, err := utils.IsCrdAvailable(mgr.GetConfig(), istioclientv1alpha3.SchemeGroupVersion.String(), "EnvoyFilter"); err != nil {
		setupLog.Error(err, "Failed to check CRD availability for EnvoyFilter")
	} else if ok {
		b = b.Watches(&istioclientv1alpha3.EnvoyFilter{},
			r.enqueueOnEnvoyFilterChange(),
			ctrlbuilder.WithPredicates(predicate.Funcs{
				CreateFunc: func(e event.CreateEvent) bool {
					return false
				},
				UpdateFunc: func(e event.UpdateEvent) bool {
					return utils.IsManagedByOpenDataHub(e.ObjectNew)
				},
				DeleteFunc: func(e event.DeleteEvent) bool {
					return utils.IsManagedByOpenDataHub(e.Object)
				},
			}))
	}

	return b.Complete(r)
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

func (r *LLMInferenceServiceReconciler) enqueueOnEnvoyFilterChange() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, object client.Object) []reconcile.Request {
		envoyFilter := object.(*istioclientv1alpha3.EnvoyFilter)

		if namespacedNames, err := r.envoyFilterMatcher.FindLLMServiceFromEnvoyFilter(ctx, envoyFilter); err == nil && len(namespacedNames) > 0 {
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

// getConfig retrieves kserveapis.LLMInferenceServiceConfig with the given name from either the kserveapis.LLMInferenceService
// namespace or from the SystemNamespace (e.g. 'kserve'), prioritizing the former.
// TODO: Reuse logic in Kserve, currently private and not very reusable.
func (k *LLMInferenceServiceReconciler) getConfig(ctx context.Context, llmisvc *kservev1alpha1.LLMInferenceService, name string) (*kservev1alpha1.LLMInferenceServiceConfig, error) {
	cfg := &kservev1alpha1.LLMInferenceServiceConfig{}
	if err := k.Client.Get(ctx, client.ObjectKey{Name: name, Namespace: llmisvc.Namespace}, cfg); err != nil {
		if apierrs.IsNotFound(err) {
			systemNamespace := os.Getenv("POD_NAMESPACE")
			if systemNamespace == "" {
				return nil, nil
			}
			cfg = &kservev1alpha1.LLMInferenceServiceConfig{}
			if err := k.Client.Get(ctx, client.ObjectKey{Name: name, Namespace: systemNamespace}, cfg); err != nil {
				return nil, fmt.Errorf("failed to get LLMInferenceServiceConfig %q from namespaces [%q, %q]: %w", name, llmisvc.Namespace, systemNamespace, err)
			}
			return cfg, nil
		}
		return nil, fmt.Errorf("failed to get LLMInferenceServiceConfig %s/%s: %w", llmisvc.Namespace, name, err)
	}
	return cfg, nil
}
