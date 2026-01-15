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
	authorinooperatorv1beta1 "github.com/kuadrant/authorino-operator/api/v1beta1"
	kuadrantv1 "github.com/kuadrant/kuadrant-operator/api/v1"
	kuadrantv1beta1 "github.com/kuadrant/kuadrant-operator/api/v1beta1"
	istioclientv1alpha3 "istio.io/client-go/pkg/apis/networking/v1alpha3"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/rbac/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlbuilder "sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/resources"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/serving/llm/reconcilers"
	parentreconcilers "github.com/opendatahub-io/odh-model-controller/internal/controller/serving/reconcilers"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
)

const (
	// Field indexer key and value for tier annotation lookup
	tierAnnotationIndexKey   = "metadata.annotations[alpha.maas.opendatahub.io/tiers]"
	tierAnnotationIndexValue = "__has_tier__"
)

type LLMInferenceServiceReconciler struct {
	client.Client
	Recorder               record.EventRecorder
	Scheme                 *runtime.Scheme
	subResourceReconcilers []parentreconcilers.LLMSubResourceReconciler
	authPolicyMatcher      resources.AuthPolicyMatcher
	envoyFilterMatcher     resources.EnvoyFilterMatcher
}

var ownedBySelfPredicate = predicate.NewPredicateFuncs(func(o client.Object) bool {
	return o.GetLabels()["app.kubernetes.io/managed-by"] == "odh-model-controller"
})

func NewLLMInferenceServiceReconciler(client client.Client, scheme *runtime.Scheme, recorder record.EventRecorder) *LLMInferenceServiceReconciler {
	subResourceReconcilers := []parentreconcilers.LLMSubResourceReconciler{
		parentreconcilers.NewLLMRoleReconciler(client),
		parentreconcilers.NewLLMRoleBindingReconciler(client, recorder),
		reconcilers.NewKserveAuthPolicyReconciler(client, scheme),
		reconcilers.NewKserveEnvoyFilterReconciler(client, scheme),
	}

	return &LLMInferenceServiceReconciler{
		Client:                 client,
		Recorder:               recorder,
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
		r.Recorder.Eventf(llmisvc, corev1.EventTypeWarning, "ReconcileError", "Failed to reconcile LLMInferenceService: %v", err)
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
// +kubebuilder:rbac:groups=kuadrant.io,resources=kuadrants,verbs=get;list;watch
// +kubebuilder:rbac:groups=operator.authorino.kuadrant.io,resources=authorinos,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch

func (r *LLMInferenceServiceReconciler) SetupWithManager(mgr ctrl.Manager, setupLog logr.Logger) error {
	b := ctrl.NewControllerManagedBy(mgr).
		For(&kservev1alpha1.LLMInferenceService{}).
		Owns(&v1.Role{}, ctrlbuilder.WithPredicates(ownedBySelfPredicate)).
		Owns(&v1.RoleBinding{}, ctrlbuilder.WithPredicates(ownedBySelfPredicate)).
		Named("llminferenceservice")

	setupLog.Info("Setting up LLMInferenceService controller")

	// Setup field indexer for tier annotation BEFORE registering watch
	// This enables fast lookups when tier ConfigMap changes
	ctx := context.Background()
	if err := mgr.GetFieldIndexer().IndexField(ctx, &kservev1alpha1.LLMInferenceService{},
		tierAnnotationIndexKey, func(obj client.Object) []string {
			llmisvc := obj.(*kservev1alpha1.LLMInferenceService)
			if annotations := llmisvc.GetAnnotations(); annotations != nil {
				if _, found := annotations[parentreconcilers.TierAnnotationKey]; found {
					return []string{tierAnnotationIndexValue}
				}
			}
			return nil
		}); err != nil {
		setupLog.Error(err, "Failed to setup tier annotation field indexer")
		return err
	}

	// Watch tier mapping ConfigMap with namespace-scoped predicates
	b = b.Watches(&corev1.ConfigMap{},
		r.enqueueOnTierConfigMapChange(),
		ctrlbuilder.WithPredicates(predicate.Funcs{
			CreateFunc: func(e event.CreateEvent) bool {
				return parentreconcilers.IsTierConfigMap(e.Object)
			},
			UpdateFunc: func(e event.UpdateEvent) bool {
				return parentreconcilers.IsTierConfigMap(e.ObjectNew)
			},
			DeleteFunc: func(e event.DeleteEvent) bool {
				return parentreconcilers.IsTierConfigMap(e.Object)
			},
		}))

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

	if ok, err := utils.IsCrdAvailable(mgr.GetConfig(), kuadrantv1beta1.GroupVersion.String(), "Kuadrant"); err != nil {
		setupLog.Error(err, "Failed to check CRD availability for Kuadrant")
	} else if ok {
		b = b.Watches(&kuadrantv1beta1.Kuadrant{}, r.globalResync(setupLog))
	}

	if ok, err := utils.IsCrdAvailable(mgr.GetConfig(), authorinooperatorv1beta1.GroupVersion.String(), "Authorino"); err != nil {
		setupLog.Error(err, "Failed to check CRD availability for Authorino")
	} else if ok {
		b = b.Watches(&authorinooperatorv1beta1.Authorino{}, r.globalResync(setupLog))
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

	// Watch Gateway for managed label or authorino-tls-bootstrap annotation changes
	if ok, err := utils.IsCrdAvailable(mgr.GetConfig(), gatewayapiv1.GroupVersion.String(), "Gateway"); err != nil {
		setupLog.Error(err, "Failed to check CRD availability for Gateway")
	} else if ok {
		b = b.Watches(&gatewayapiv1.Gateway{},
			r.enqueueOnGatewayChange(),
			ctrlbuilder.WithPredicates(predicate.Funcs{
				CreateFunc: func(e event.CreateEvent) bool {
					return false
				},
				UpdateFunc: func(e event.UpdateEvent) bool {
					// Check if managed label changed
					oldManaged := e.ObjectOld.GetLabels()[constants.ODHManagedLabel]
					newManaged := e.ObjectNew.GetLabels()[constants.ODHManagedLabel]
					if oldManaged != newManaged {
						return true
					}
					// Check if authorino-tls-bootstrap annotation changed
					oldTLS := e.ObjectOld.GetAnnotations()[constants.AuthorinoTLSBootstrapAnnotation]
					newTLS := e.ObjectNew.GetAnnotations()[constants.AuthorinoTLSBootstrapAnnotation]
					return oldTLS != newTLS
				},
				DeleteFunc: func(e event.DeleteEvent) bool {
					return false
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

func (r *LLMInferenceServiceReconciler) globalResync(setupLog logr.Logger) handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, object client.Object) []reconcile.Request {

		llmSvcList := &kservev1alpha1.LLMInferenceServiceList{}
		if err := r.Client.List(ctx, llmSvcList); err != nil {
			setupLog.Error(err, "Failed to list LLMInferenceService")
			return nil
		}

		requests := make([]reconcile.Request, 0, len(llmSvcList.Items))
		for _, llmSvc := range llmSvcList.Items {
			requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{
				Name:      llmSvc.Name,
				Namespace: llmSvc.Namespace,
			}})
		}
		return requests
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

// enqueueOnTierConfigMapChange returns a handler that enqueues LLMInferenceServices
// with tier annotations when the tier mapping ConfigMap changes.
func (r *LLMInferenceServiceReconciler) enqueueOnTierConfigMapChange() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, object client.Object) []reconcile.Request {
		logger := log.FromContext(ctx)

		// Namespace and name scoping done in predicate, double-check here
		if !parentreconcilers.IsTierConfigMap(object) {
			return []reconcile.Request{}
		}

		logger.Info("Tier ConfigMap changed, enqueueing affected services",
			"configmap", object.GetName())

		llmSvcList := &kservev1alpha1.LLMInferenceServiceList{}
		if err := r.Client.List(ctx, llmSvcList,
			client.MatchingFields{tierAnnotationIndexKey: tierAnnotationIndexValue}); err != nil {
			logger.Error(err, "Failed to list indexed services")
			return []reconcile.Request{}
		}

		requests := make([]reconcile.Request, 0, len(llmSvcList.Items))
		for _, llmSvc := range llmSvcList.Items {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      llmSvc.Name,
					Namespace: llmSvc.Namespace,
				},
			})
		}

		logger.Info("Enqueued services for reconciliation", "count", len(requests))
		return requests
	})
}

func (r *LLMInferenceServiceReconciler) enqueueOnGatewayChange() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, object client.Object) []reconcile.Request {
		gateway := object.(*gatewayapiv1.Gateway)
		logger := log.FromContext(ctx).WithValues("gateway", gateway.Name, "namespace", gateway.Namespace)

		var requests []reconcile.Request
		continueToken := ""

		// Use pagination to handle large numbers of services efficiently
		for {
			llmSvcList := &kservev1alpha1.LLMInferenceServiceList{}
			if err := r.Client.List(ctx, llmSvcList, &client.ListOptions{Continue: continueToken}); err != nil {
				logger.Error(err, "Failed to list LLMInferenceService for gateway change")
				return nil
			}

			for _, llmSvc := range llmSvcList.Items {
				if utils.LLMIsvcUsesGateway(ctx, r.Client, &llmSvc, gateway.Namespace, gateway.Name) {
					requests = append(requests, reconcile.Request{
						NamespacedName: types.NamespacedName{
							Name:      llmSvc.Name,
							Namespace: llmSvc.Namespace,
						},
					})
				}
			}

			if llmSvcList.Continue == "" {
				break
			}
			continueToken = llmSvcList.Continue
		}

		return requests
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
