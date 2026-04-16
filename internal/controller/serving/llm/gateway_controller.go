/*
Copyright 2026.

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
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/go-logr/logr"
	kservev1alpha2 "github.com/kserve/kserve/pkg/apis/serving/v1alpha2"
	kuadrantv1 "github.com/kuadrant/kuadrant-operator/api/v1"
	istioclientv1alpha3 "istio.io/client-go/pkg/apis/networking/v1alpha3"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlbuilder "sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/comparators"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/processors"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/resources"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
)

// GatewayReconciler reconciles Gateway resources to create EnvoyFilter and AuthPolicy
// independently of LLMInferenceService lifecycle. This ensures that TLS configuration
// for Authorino is available even when no models are deployed.
type GatewayReconciler struct {
	client.Client
	Recorder          record.EventRecorder
	Scheme            *runtime.Scheme
	envoyFilterLoader resources.EnvoyFilterTemplateLoader
	envoyFilterStore  resources.EnvoyFilterStore
	authPolicyLoader  resources.AuthPolicyTemplateLoader
	authPolicyStore   resources.AuthPolicyStore
	deltaProcessor    processors.DeltaProcessor
}

func NewGatewayReconciler(client client.Client, scheme *runtime.Scheme, recorder record.EventRecorder) *GatewayReconciler {
	return &GatewayReconciler{
		Client:            client,
		Recorder:          recorder,
		Scheme:            scheme,
		envoyFilterLoader: resources.NewKServeEnvoyFilterTemplateLoader(client),
		envoyFilterStore:  resources.NewClientEnvoyFilterStore(client),
		authPolicyLoader:  resources.NewKServeAuthPolicyTemplateLoader(client),
		authPolicyStore:   resources.NewClientAuthPolicyStore(client),
		deltaProcessor:    processors.NewDeltaProcessor(),
	}
}

// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gateways,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking.istio.io,resources=envoyfilters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kuadrant.io,resources=authpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch

func (r *GatewayReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("Gateway", req.Name, "namespace", req.Namespace)

	gateway := &gatewayapiv1.Gateway{}
	if err := r.Client.Get(ctx, req.NamespacedName, gateway); err != nil {
		if apierrors.IsNotFound(err) {
			logger.V(1).Info("Gateway not found, resources owned by Gateway will be garbage collected")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Unable to fetch Gateway")
		return ctrl.Result{}, err
	}

	if !gateway.GetDeletionTimestamp().IsZero() {
		logger.V(1).Info("Gateway being deleted, skipping reconciliation")
		return ctrl.Result{}, nil
	}

	referencedByLLMService, err := r.isGatewayReferencedByLLMService(ctx, gateway)
	if err != nil {
		logger.Error(err, "Unable to determine if Gateway is referenced by LLMInferenceService")
		return ctrl.Result{}, err
	}
	gatewayInUse := utils.IsAuthorinoTLSBootstrapEnabled(gateway) || referencedByLLMService
	shouldCreateEnvoyFilter := gatewayInUse && utils.ShouldCreateEnvoyFilterForGateway(gateway)
	shouldCreateAuthPolicy := gatewayInUse && !utils.IsExplicitlyUnmanaged(gateway) && !utils.IsOwnedByPlatformController(gateway)

	if shouldCreateEnvoyFilter {
		if err := r.reconcileEnvoyFilter(ctx, logger, gateway); err != nil && !meta.IsNoMatchError(err) {
			r.Recorder.Eventf(gateway, corev1.EventTypeWarning, "ReconcileError", "Failed to reconcile EnvoyFilter: %v", err)
			return ctrl.Result{}, err
		}
	} else {
		if err := r.deleteEnvoyFilterIfManaged(ctx, logger, gateway); err != nil && !meta.IsNoMatchError(err) {
			r.Recorder.Eventf(gateway, corev1.EventTypeWarning, "ReconcileError", "Failed to delete EnvoyFilter: %v", err)
			return ctrl.Result{}, err
		}
	}

	if shouldCreateAuthPolicy {
		if err := r.reconcileAuthPolicy(ctx, logger, gateway); err != nil && !meta.IsNoMatchError(err) {
			r.Recorder.Eventf(gateway, corev1.EventTypeWarning, "ReconcileError", "Failed to reconcile AuthPolicy: %v", err)
			return ctrl.Result{}, err
		}
	} else {
		if err := r.deleteAuthPolicyIfManaged(ctx, logger, gateway); err != nil && !meta.IsNoMatchError(err) {
			r.Recorder.Eventf(gateway, corev1.EventTypeWarning, "ReconcileError", "Failed to delete AuthPolicy: %v", err)
			return ctrl.Result{}, err
		}
	}

	logger.V(1).Info("Successfully reconciled Gateway resources")
	return ctrl.Result{}, nil
}

func (r *GatewayReconciler) reconcileEnvoyFilter(ctx context.Context, logger logr.Logger, gateway *gatewayapiv1.Gateway) error {
	logger.V(1).Info("Reconciling EnvoyFilter for Gateway")

	kuadrantNamespace := utils.GetKuadrantNamespace(ctx, r.Client)
	if !utils.IsAuthorinoTLSEnabled(ctx, r.Client, kuadrantNamespace) {
		logger.V(1).Info("Authorino TLS is not enabled, skipping EnvoyFilter creation")
		return nil
	}

	envoyFilterName := constants.GetGatewayEnvoyFilterName(gateway.Name)
	existing, err := r.envoyFilterStore.Get(ctx, types.NamespacedName{
		Name:      envoyFilterName,
		Namespace: gateway.Namespace,
	})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get existing EnvoyFilter: %w", err)
	}

	desired, err := r.envoyFilterLoader.Load(ctx, gateway.Namespace, gateway.Name)
	if err != nil {
		return fmt.Errorf("failed to load EnvoyFilter template: %w", err)
	}

	delta := r.deltaProcessor.ComputeDelta(comparators.GetEnvoyFilterComparator(), desired, existing)

	if !delta.HasChanges() {
		logger.V(1).Info("No changes detected for EnvoyFilter", "name", envoyFilterName)
		return nil
	}

	if delta.IsAdded() {
		logger.Info("Creating EnvoyFilter", "name", envoyFilterName)
		if err := controllerutil.SetControllerReference(gateway, desired, r.Scheme); err != nil {
			return fmt.Errorf("failed to set controller reference: %w", err)
		}
		if err := r.envoyFilterStore.Create(ctx, desired); err != nil {
			return fmt.Errorf("failed to create EnvoyFilter: %w", err)
		}
	} else if delta.IsUpdated() {
		logger.Info("Updating EnvoyFilter", "name", envoyFilterName)
		if err := controllerutil.SetControllerReference(gateway, desired, r.Scheme); err != nil {
			return fmt.Errorf("failed to set controller reference: %w", err)
		}
		if err := r.envoyFilterStore.Update(ctx, desired); err != nil {
			return fmt.Errorf("failed to update EnvoyFilter: %w", err)
		}
	}

	return nil
}

func (r *GatewayReconciler) reconcileAuthPolicy(ctx context.Context, logger logr.Logger, gateway *gatewayapiv1.Gateway) error {
	logger.V(1).Info("Reconciling AuthPolicy for Gateway")

	authPolicyName := constants.GetAuthPolicyName(gateway.Name)
	existing, err := r.authPolicyStore.Get(ctx, types.NamespacedName{
		Name:      authPolicyName,
		Namespace: gateway.Namespace,
	})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get existing AuthPolicy: %w", err)
	}

	if existing != nil && !utils.IsManagedByOpenDataHub(existing) {
		logger.V(1).Info("Skipping AuthPolicy reconciliation - not managed by odh-model-controller", "name", authPolicyName)
		return nil
	}

	audiences := utils.GetAuthAudience(ctx, r.Client, constants.KubernetesAudience)

	objectiveExpression := constants.DefaultObjectiveExpression
	if customExpr, ok := gateway.Annotations[constants.AuthPolicyObjectiveExpressionAnnotation]; ok && customExpr != "" {
		objectiveExpression = customExpr
	}

	desired, err := r.authPolicyLoader.Load(ctx, resources.AuthPolicyTarget{
		Kind:      "Gateway",
		Name:      gateway.Name,
		Namespace: gateway.Namespace,
		AuthType:  constants.UserDefined,
	},
		resources.WithLabels(map[string]string{"app.kubernetes.io/name": "llminferenceservice-auth"}),
		resources.WithAudiences(audiences),
		resources.WithObjectiveExpression(objectiveExpression),
	)
	if err != nil {
		return fmt.Errorf("failed to load AuthPolicy template: %w", err)
	}

	delta := r.deltaProcessor.ComputeDelta(comparators.GetAuthPolicyComparator(), desired, existing)

	if !delta.HasChanges() {
		logger.V(1).Info("No changes detected for AuthPolicy", "name", authPolicyName)
		return nil
	}

	if delta.IsAdded() {
		logger.Info("Creating AuthPolicy", "name", authPolicyName)
		if err := controllerutil.SetControllerReference(gateway, desired, r.Scheme); err != nil {
			return fmt.Errorf("failed to set controller reference: %w", err)
		}
		if err := r.authPolicyStore.Create(ctx, desired); err != nil {
			return fmt.Errorf("failed to create AuthPolicy: %w", err)
		}
	} else if delta.IsUpdated() {
		logger.Info("Updating AuthPolicy", "name", authPolicyName)
		if err := controllerutil.SetControllerReference(gateway, desired, r.Scheme); err != nil {
			return fmt.Errorf("failed to set controller reference: %w", err)
		}
		utils.MergeUserLabelsAndAnnotations(desired, existing)
		if err := r.authPolicyStore.Update(ctx, desired); err != nil {
			return fmt.Errorf("failed to update AuthPolicy: %w", err)
		}
	}

	return nil
}

func (r *GatewayReconciler) deleteEnvoyFilterIfManaged(ctx context.Context, logger logr.Logger, gateway *gatewayapiv1.Gateway) error {
	envoyFilterName := constants.GetGatewayEnvoyFilterName(gateway.Name)

	existing, err := r.envoyFilterStore.Get(ctx, types.NamespacedName{
		Name:      envoyFilterName,
		Namespace: gateway.Namespace,
	})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to get existing EnvoyFilter: %w", err)
	}

	if !utils.IsManagedByOpenDataHub(existing) {
		logger.V(1).Info("Skipping EnvoyFilter deletion - not managed by odh-model-controller", "name", envoyFilterName)
		return nil
	}

	logger.Info("Deleting EnvoyFilter", "name", envoyFilterName)
	if err := r.envoyFilterStore.Remove(ctx, types.NamespacedName{
		Name:      envoyFilterName,
		Namespace: gateway.Namespace,
	}); err != nil {
		return fmt.Errorf("failed to delete EnvoyFilter: %w", err)
	}

	return nil
}

func (r *GatewayReconciler) deleteAuthPolicyIfManaged(ctx context.Context, logger logr.Logger, gateway *gatewayapiv1.Gateway) error {
	authPolicyName := constants.GetAuthPolicyName(gateway.Name)

	existing, err := r.authPolicyStore.Get(ctx, types.NamespacedName{
		Name:      authPolicyName,
		Namespace: gateway.Namespace,
	})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to get existing AuthPolicy: %w", err)
	}

	if !utils.IsManagedByOpenDataHub(existing) {
		logger.V(1).Info("Skipping AuthPolicy deletion - not managed by odh-model-controller", "name", authPolicyName)
		return nil
	}

	logger.Info("Deleting AuthPolicy", "name", authPolicyName)
	if err := r.authPolicyStore.Remove(ctx, types.NamespacedName{
		Name:      authPolicyName,
		Namespace: gateway.Namespace,
	}); err != nil {
		return fmt.Errorf("failed to delete AuthPolicy: %w", err)
	}

	return nil
}

// isGatewayReferencedByLLMService checks whether the given gateway is the default gateway
// (from ConfigMap or hardcoded fallback) or is explicitly referenced by at least one
// LLMInferenceService (directly or via BaseRef configs). This prevents creating
// EnvoyFilter/AuthPolicy on gateways unrelated to LLM inference.
func (r *GatewayReconciler) isGatewayReferencedByLLMService(ctx context.Context, gateway *gatewayapiv1.Gateway) (bool, error) {
	defaultNs, defaultName, err := utils.GetDefaultGatewayRef(ctx, r.Client)
	if err != nil {
		defaultNs = constants.DefaultGatewayNamespace
		defaultName = constants.DefaultGatewayName
	}
	if gateway.Name == defaultName && gateway.Namespace == defaultNs {
		return true, nil
	}

	llmSvcList := &kservev1alpha2.LLMInferenceServiceList{}
	if err := r.Client.List(ctx, llmSvcList); err != nil {
		return false, fmt.Errorf("failed to list LLMInferenceServices: %w", err)
	}

	for i := range llmSvcList.Items {
		for _, ref := range r.getEffectiveGatewayRefs(ctx, gateway, &llmSvcList.Items[i]) {
			ns := string(ref.Namespace)
			if ns == "" {
				ns = llmSvcList.Items[i].Namespace
			}
			if string(ref.Name) == gateway.Name && ns == gateway.Namespace {
				return true, nil
			}
		}
	}
	return false, nil
}

// getEffectiveGatewayRefs returns the effective gateway references for an LLMInferenceService.
// The service's own gateway refs take precedence over those inherited from BaseRef configs,
// matching the "last one wins" merge semantics used by kserve's MergeSpecs.
func (r *GatewayReconciler) getEffectiveGatewayRefs(ctx context.Context, gateway *gatewayapiv1.Gateway, llmSvc *kservev1alpha2.LLMInferenceService) []kservev1alpha2.UntypedObjectReference {
	nsLabels := r.fetchNamespaceLabels(ctx, llmSvc.Namespace)

	// Service's own refs take precedence (replace config refs)
	if refs := getGatewayRefs(ctx, gateway, llmSvc, nsLabels); len(refs) > 0 {
		return refs
	}

	logger := log.FromContext(ctx)

	// Fall back to config refs; last BaseRef with gateway refs wins
	var refs []kservev1alpha2.UntypedObjectReference
	for _, baseRef := range llmSvc.Spec.BaseRefs {
		cfg, err := r.fetchLLMISvcConfig(ctx, baseRef.Name, llmSvc.Namespace)
		if err != nil {
			logger.Error(err, "Failed to fetch LLMInferenceServiceConfig", "name", baseRef.Name)
			continue
		}

		if cfg.Spec.Router != nil && cfg.Spec.Router.Gateway.HasRefs() {
			refs = cfg.Spec.Router.Gateway.Refs
		}
	}

	return filterAllowedRefs(ctx, gateway, refs, llmSvc.Namespace, nsLabels)
}

// fetchNamespaceLabels retrieves the labels for the given namespace.
// Returns nil if the namespace cannot be fetched.
func (r *GatewayReconciler) fetchNamespaceLabels(ctx context.Context, namespace string) map[string]string {
	ns := &corev1.Namespace{}
	if err := r.Client.Get(ctx, client.ObjectKey{Name: namespace}, ns); err != nil {
		log.FromContext(ctx).V(1).Info("Failed to fetch namespace labels, Selector-based allowedRoutes will be denied", "namespace", namespace, "error", err)
		return nil
	}
	return ns.Labels
}

func filterAllowedRefs(ctx context.Context, gateway *gatewayapiv1.Gateway, refs []kservev1alpha2.UntypedObjectReference, targetNS string, nsLabels map[string]string) []kservev1alpha2.UntypedObjectReference {
	if gateway == nil {
		return refs
	}

	var allowed []kservev1alpha2.UntypedObjectReference
	for _, ref := range refs {
		refNs := string(ref.Namespace)
		if refNs == "" {
			refNs = targetNS
		}
		if string(ref.Name) != gateway.Name || refNs != gateway.Namespace {
			// Ref points to a different gateway — keep it, we can't evaluate.
			allowed = append(allowed, ref)
			continue
		}

		if gatewayAllowsNamespace(ctx, gateway, targetNS, nsLabels) {
			allowed = append(allowed, ref)
		}
	}
	return allowed
}

// gatewayAllowsNamespace returns true if at least one listener on the gateway
// permits routes from targetNS according to the Gateway API allowedRoutes spec.
func gatewayAllowsNamespace(ctx context.Context, gateway *gatewayapiv1.Gateway, targetNS string, nsLabels map[string]string) bool {
	for _, listener := range gateway.Spec.Listeners {
		if listenerAllowsNamespace(ctx, listener, gateway.Namespace, targetNS, nsLabels) {
			return true
		}
	}
	return false
}

func listenerAllowsNamespace(ctx context.Context, l gatewayapiv1.Listener, gwNamespace, targetNS string, nsLabels map[string]string) bool {
	if l.AllowedRoutes == nil || l.AllowedRoutes.Namespaces == nil || l.AllowedRoutes.Namespaces.From == nil {
		// Default is "Same" per Gateway API spec.
		return gwNamespace == targetNS
	}

	switch *l.AllowedRoutes.Namespaces.From {
	case gatewayapiv1.NamespacesFromAll:
		return true
	case gatewayapiv1.NamespacesFromSame:
		return gwNamespace == targetNS
	case gatewayapiv1.NamespacesFromSelector:
		if l.AllowedRoutes.Namespaces.Selector == nil {
			return false
		}
		selector, err := metav1.LabelSelectorAsSelector(l.AllowedRoutes.Namespaces.Selector)
		if err != nil {
			log.FromContext(ctx).Error(err, "Failed to parse listener namespace selector, denying access",
				"listener", l.Name, "gwNamespace", gwNamespace)
			return false
		}
		return selector.Matches(labels.Set(nsLabels))
	default:
		return false
	}
}

// fetchLLMISvcConfig fetches LLMInferenceServiceConfig by name, first from the given namespace,
// then falling back to the system namespace (POD_NAMESPACE).
func (r *GatewayReconciler) fetchLLMISvcConfig(ctx context.Context, name, namespace string) (*kservev1alpha2.LLMInferenceServiceConfig, error) {
	cfg := &kservev1alpha2.LLMInferenceServiceConfig{}

	if err := r.Client.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, cfg); err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, err
		}
		systemNamespace := os.Getenv("POD_NAMESPACE")
		if systemNamespace == "" {
			return nil, err
		}
		if err := r.Client.Get(ctx, client.ObjectKey{Name: name, Namespace: systemNamespace}, cfg); err != nil {
			return nil, err
		}
	}

	return cfg, nil
}

// enqueueGatewaysFromLLMInferenceService returns an event handler that extracts gateway refs
// from LLMInferenceService (including BaseRef configs) and enqueues reconcile requests for
// the referenced gateways.
func (r *GatewayReconciler) enqueueGatewaysFromLLMInferenceService() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, object client.Object) []reconcile.Request {
		llmSvc, ok := object.(*kservev1alpha2.LLMInferenceService)
		if !ok {
			return nil
		}

		var requests []reconcile.Request
		for _, ref := range r.getEffectiveGatewayRefs(ctx, nil, llmSvc) {
			namespace := string(ref.Namespace)
			if namespace == "" {
				namespace = llmSvc.Namespace
			}
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      string(ref.Name),
					Namespace: namespace,
				},
			})
		}

		// Enqueue the default gateway if no effective refs
		if len(requests) == 0 {
			defaultNs, defaultName, err := utils.GetDefaultGatewayRef(ctx, r.Client)
			if err != nil {
				defaultNs = constants.DefaultGatewayNamespace
				defaultName = constants.DefaultGatewayName
			}
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      defaultName,
					Namespace: defaultNs,
				},
			})
		}

		return requests
	})
}

// enqueueGatewaysFromLLMInferenceServiceConfig returns an event handler that finds
// LLMInferenceServices referencing the changed config via BaseRefs, resolves their
// effective gateway refs, and enqueues reconcile requests for those gateways.
func (r *GatewayReconciler) enqueueGatewaysFromLLMInferenceServiceConfig() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, object client.Object) []reconcile.Request {
		cfg, ok := object.(*kservev1alpha2.LLMInferenceServiceConfig)
		if !ok {
			return nil
		}

		llmSvcList := &kservev1alpha2.LLMInferenceServiceList{}
		if err := r.Client.List(ctx, llmSvcList); err != nil {
			log.FromContext(ctx).Error(err, "Failed to list LLMInferenceServices for config change, falling back to config's own refs", "config", cfg.Name)
			return gatewayRequestsFromRefs(getConfigGatewayRefs(cfg), cfg.Namespace)
		}

		seen := make(map[types.NamespacedName]struct{})
		var requests []reconcile.Request
		for i := range llmSvcList.Items {
			svc := &llmSvcList.Items[i]
			if !referencesConfig(svc, cfg.Name) {
				continue
			}
			for _, ref := range r.getEffectiveGatewayRefs(ctx, nil, svc) {
				namespace := string(ref.Namespace)
				if namespace == "" {
					namespace = svc.Namespace
				}
				key := types.NamespacedName{Name: string(ref.Name), Namespace: namespace}
				if _, exists := seen[key]; exists {
					continue
				}
				seen[key] = struct{}{}
				requests = append(requests, reconcile.Request{NamespacedName: key})
			}
		}

		return requests
	})
}

// enqueueGatewaysFromNamespace returns an event handler that, on namespace label
// changes, finds gateways referenced by LLMInferenceServices in the changed
// namespace and enqueues those with Selector-based listeners.
func (r *GatewayReconciler) enqueueGatewaysFromNamespace() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, object client.Object) []reconcile.Request {
		ns := object.(*corev1.Namespace)

		llmSvcList := &kservev1alpha2.LLMInferenceServiceList{}
		if err := r.Client.List(ctx, llmSvcList, client.InNamespace(ns.Name)); err != nil {
			log.FromContext(ctx).Error(err, "Failed to list LLMInferenceServices for namespace label change", "namespace", ns.Name)
			return nil
		}
		if len(llmSvcList.Items) == 0 {
			return nil
		}

		// Collect unique gateway refs from all LLMInferenceServices in this namespace.
		seen := make(map[types.NamespacedName]struct{})
		var candidates []types.NamespacedName
		for i := range llmSvcList.Items {
			for _, ref := range r.getEffectiveGatewayRefs(ctx, nil, &llmSvcList.Items[i]) {
				namespace := string(ref.Namespace)
				if namespace == "" {
					namespace = ns.Name
				}
				key := types.NamespacedName{Name: string(ref.Name), Namespace: namespace}
				if _, exists := seen[key]; !exists {
					seen[key] = struct{}{}
					candidates = append(candidates, key)
				}
			}
		}

		// Only enqueue gateways that exist and have Selector-based listeners.
		var requests []reconcile.Request
		for _, key := range candidates {
			gw := &gatewayapiv1.Gateway{}
			if err := r.Client.Get(ctx, key, gw); err != nil {
				if apierrors.IsNotFound(err) {
					continue
				}
				log.FromContext(ctx).Error(err, "Failed to get Gateway for namespace label change", "gateway", key.Name, "namespace", key.Namespace)
				requests = append(requests, reconcile.Request{NamespacedName: key})
				continue
			}
			if hasSelectorListeners(gw) {
				requests = append(requests, reconcile.Request{NamespacedName: key})
			}
		}
		return requests
	})
}

// hasSelectorListeners returns true if any listener on the gateway uses
// NamespacesFromSelector in its allowedRoutes.
func hasSelectorListeners(gw *gatewayapiv1.Gateway) bool {
	for _, l := range gw.Spec.Listeners {
		if l.AllowedRoutes != nil &&
			l.AllowedRoutes.Namespaces != nil &&
			l.AllowedRoutes.Namespaces.From != nil &&
			*l.AllowedRoutes.Namespaces.From == gatewayapiv1.NamespacesFromSelector {
			return true
		}
	}
	return false
}

func getConfigGatewayRefs(cfg *kservev1alpha2.LLMInferenceServiceConfig) []kservev1alpha2.UntypedObjectReference {
	if cfg.Spec.Router != nil && cfg.Spec.Router.Gateway.HasRefs() {
		return cfg.Spec.Router.Gateway.Refs
	}
	return nil
}

func gatewayRequestsFromRefs(refs []kservev1alpha2.UntypedObjectReference, fallbackNamespace string) []reconcile.Request {
	requests := make([]reconcile.Request, 0, len(refs))
	for _, ref := range refs {
		namespace := string(ref.Namespace)
		if namespace == "" {
			namespace = fallbackNamespace
		}
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      string(ref.Name),
				Namespace: namespace,
			},
		})
	}
	return requests
}

// referencesConfig returns true if the LLMInferenceService has a BaseRef matching the given config name.
func referencesConfig(svc *kservev1alpha2.LLMInferenceService, configName string) bool {
	for _, ref := range svc.Spec.BaseRefs {
		if ref.Name == configName {
			return true
		}
	}
	return false
}

// gatewayRefsEqual reports whether two sets of gateway refs are equivalent (order-independent).
func gatewayRefsEqual(a, b []kservev1alpha2.UntypedObjectReference) bool {
	if len(a) != len(b) {
		return false
	}

	cmp := func(x, y kservev1alpha2.UntypedObjectReference) int {
		if c := strings.Compare(string(x.Namespace), string(y.Namespace)); c != 0 {
			return c
		}
		return strings.Compare(string(x.Name), string(y.Name))
	}

	sortedA := slices.Clone(a)
	sortedB := slices.Clone(b)
	slices.SortFunc(sortedA, cmp)
	slices.SortFunc(sortedB, cmp)

	for i := range sortedA {
		if sortedA[i].Name != sortedB[i].Name || sortedA[i].Namespace != sortedB[i].Namespace {
			return false
		}
	}
	return true
}

// configGatewayRefsChanged reports whether Router.Gateway.Refs differ between two LLMInferenceServiceConfig objects.
func configGatewayRefsChanged(oldCfg, newCfg *kservev1alpha2.LLMInferenceServiceConfig) bool {
	oldHasRefs := oldCfg.Spec.Router != nil && oldCfg.Spec.Router.Gateway.HasRefs()
	newHasRefs := newCfg.Spec.Router != nil && newCfg.Spec.Router.Gateway.HasRefs()

	if !oldHasRefs && !newHasRefs {
		return false
	}
	if oldHasRefs != newHasRefs {
		return true
	}

	return !gatewayRefsEqual(oldCfg.Spec.Router.Gateway.Refs, newCfg.Spec.Router.Gateway.Refs)
}

// gatewayRefsChanged reports whether gateway refs or BaseRefs differ between two LLMInferenceService objects.
// BaseRefs are compared because they can introduce gateway refs via LLMInferenceServiceConfig.
func gatewayRefsChanged(old, new *kservev1alpha2.LLMInferenceService) bool {
	if !gatewayRefsEqual(getGatewayRefs(context.Background(), nil, old, nil), getGatewayRefs(context.Background(), nil, new, nil)) {
		return true
	}

	// Also compare BaseRefs since they can introduce gateway refs via configs
	oldBaseRefs := slices.Clone(old.Spec.BaseRefs)
	newBaseRefs := slices.Clone(new.Spec.BaseRefs)

	if len(oldBaseRefs) != len(newBaseRefs) {
		return true
	}

	cmpBaseRefs := func(a, b corev1.LocalObjectReference) int {
		return strings.Compare(a.Name, b.Name)
	}

	slices.SortFunc(oldBaseRefs, cmpBaseRefs)
	slices.SortFunc(newBaseRefs, cmpBaseRefs)

	for i := range oldBaseRefs {
		if oldBaseRefs[i].Name != newBaseRefs[i].Name {
			return true
		}
	}

	return false
}

func getGatewayRefs(ctx context.Context, gateway *gatewayapiv1.Gateway, llmSvc *kservev1alpha2.LLMInferenceService, nsLabels map[string]string) []kservev1alpha2.UntypedObjectReference {
	if llmSvc.Spec.Router != nil && llmSvc.Spec.Router.Gateway.HasRefs() {
		return filterAllowedRefs(ctx, gateway, slices.Clone(llmSvc.Spec.Router.Gateway.Refs), llmSvc.Namespace, nsLabels)
	}
	return nil
}

func (r *GatewayReconciler) SetupWithManager(mgr ctrl.Manager, setupLog logr.Logger) error {
	isGatewayAvailable, err := utils.IsCrdAvailable(mgr.GetConfig(), gatewayapiv1.GroupVersion.String(), "Gateway")
	if err != nil {
		setupLog.V(1).Error(err, "could not determine if Gateway CRD is available")
		return err
	}
	if !isGatewayAvailable {
		setupLog.Info("Gateway CRD not available, skipping Gateway controller setup")
		return nil
	}

	isEnvoyFilterAvailable, err := utils.IsCrdAvailable(mgr.GetConfig(), istioclientv1alpha3.SchemeGroupVersion.String(), "EnvoyFilter")
	if err != nil {
		setupLog.V(1).Error(err, "could not determine if EnvoyFilter CRD is available")
		return err
	}

	isAuthPolicyAvailable, err := utils.IsCrdAvailable(mgr.GetConfig(), kuadrantv1.GroupVersion.String(), "AuthPolicy")
	if err != nil {
		setupLog.V(1).Error(err, "could not determine if AuthPolicy CRD is available")
		return err
	}

	setupLog.Info("Setting up Gateway controller for EnvoyFilter/AuthPolicy bootstrap",
		"envoyFilterCRD", isEnvoyFilterAvailable, "authPolicyCRD", isAuthPolicyAvailable)

	gatewayPredicate := predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return utils.ShouldCreateEnvoyFilterForGateway(e.Object.(*gatewayapiv1.Gateway))
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldLabManaged := e.ObjectOld.GetLabels()[constants.ODHManaged]
			newLabManaged := e.ObjectNew.GetLabels()[constants.ODHManaged]
			if oldLabManaged != newLabManaged {
				return true
			}
			oldAnnManaged := e.ObjectOld.GetAnnotations()[constants.ODHManaged]
			newAnnManaged := e.ObjectNew.GetAnnotations()[constants.ODHManaged]
			if oldAnnManaged != newAnnManaged {
				return true
			}
			oldTLS := e.ObjectOld.GetAnnotations()[constants.AuthorinoTLSBootstrapAnnotation]
			newTLS := e.ObjectNew.GetAnnotations()[constants.AuthorinoTLSBootstrapAnnotation]
			if oldTLS != newTLS {
				return true
			}
			return utils.ShouldCreateEnvoyFilterForGateway(e.ObjectNew.(*gatewayapiv1.Gateway))
		},
		DeleteFunc: func(_ event.DeleteEvent) bool {
			return false
		},
	}

	managedPredicate := predicate.NewPredicateFuncs(func(obj client.Object) bool {
		return utils.IsManagedByOpenDataHub(obj)
	})

	builder := ctrl.NewControllerManagedBy(mgr).
		For(&gatewayapiv1.Gateway{}, ctrlbuilder.WithPredicates(gatewayPredicate))

	if isEnvoyFilterAvailable {
		builder = builder.Owns(&istioclientv1alpha3.EnvoyFilter{}, ctrlbuilder.WithPredicates(managedPredicate))
	}
	if isAuthPolicyAvailable {
		builder = builder.Owns(&kuadrantv1.AuthPolicy{}, ctrlbuilder.WithPredicates(managedPredicate))
	}

	return builder.
		Watches(&kservev1alpha2.LLMInferenceService{},
			r.enqueueGatewaysFromLLMInferenceService(),
			ctrlbuilder.WithPredicates(predicate.Funcs{
				CreateFunc: func(_ event.CreateEvent) bool {
					return true
				},
				UpdateFunc: func(e event.UpdateEvent) bool {
					oldSvc := e.ObjectOld.(*kservev1alpha2.LLMInferenceService)
					newSvc := e.ObjectNew.(*kservev1alpha2.LLMInferenceService)
					return gatewayRefsChanged(oldSvc, newSvc)
				},
				DeleteFunc: func(_ event.DeleteEvent) bool {
					return true
				},
			})).
		Watches(&kservev1alpha2.LLMInferenceServiceConfig{},
			r.enqueueGatewaysFromLLMInferenceServiceConfig(),
			ctrlbuilder.WithPredicates(predicate.Funcs{
				CreateFunc: func(_ event.CreateEvent) bool {
					return true
				},
				UpdateFunc: func(e event.UpdateEvent) bool {
					oldCfg := e.ObjectOld.(*kservev1alpha2.LLMInferenceServiceConfig)
					newCfg := e.ObjectNew.(*kservev1alpha2.LLMInferenceServiceConfig)
					return configGatewayRefsChanged(oldCfg, newCfg)
				},
				DeleteFunc: func(_ event.DeleteEvent) bool {
					return true
				},
			})).
		Watches(&corev1.Namespace{},
			r.enqueueGatewaysFromNamespace(),
			ctrlbuilder.WithPredicates(predicate.Funcs{
				CreateFunc: func(_ event.CreateEvent) bool {
					return true
				},
				UpdateFunc: func(e event.UpdateEvent) bool {
					return !maps.Equal(e.ObjectOld.GetLabels(), e.ObjectNew.GetLabels())
				},
				DeleteFunc: func(_ event.DeleteEvent) bool {
					return false
				},
			})).
		Named("gateway-auth-bootstrap").
		Complete(r)
}
