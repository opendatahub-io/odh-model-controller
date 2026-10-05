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
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/go-logr/logr"
	kservev1alpha2 "github.com/kserve/kserve/pkg/apis/serving/v1alpha2"
	kservellmisvc "github.com/kserve/kserve/pkg/controller/v1alpha2/llmisvc"
	kserveutils "github.com/kserve/kserve/pkg/utils"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"
	"knative.dev/pkg/kmeta"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
)

var gridProviderGVK = schema.GroupVersionKind{Group: "grid.praxis.fast", Version: "v1alpha1", Kind: "InferenceProvider"}

// GridInferenceProviderReconciler publishes cluster-scoped Grid providers.
// A finalizer is necessary because a namespaced service cannot own them.
type GridInferenceProviderReconciler struct {
	client.Client
	APIReader client.Reader
	Config    GridConfig
}

func gridProvider() *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(gridProviderGVK)
	return obj
}

func gridResourceName(svc *kservev1alpha2.LLMInferenceService) string {
	// UID prevents a recreated service from acquiring a previous incarnation's
	// provider or authorization. The name also fits namespaced RBAC limits.
	parent := "grid-" + svc.Namespace + "-" + strings.ReplaceAll(svc.Name, ".", "-")
	return kmeta.ChildName(parent, "-"+string(svc.UID))
}

// +kubebuilder:rbac:groups=grid.praxis.fast,resources=inferenceproviders,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles;rolebindings,verbs=get;list;watch;create;update;patch;delete

func (r *GridInferenceProviderReconciler) SetupWithManager(mgr ctrl.Manager, logger logr.Logger) error {
	// Even when publishing is disabled, run cleanup for existing finalizers.
	b := ctrl.NewControllerManagedBy(mgr).For(&kservev1alpha2.LLMInferenceService{}).
		Owns(&rbacv1.Role{}).Owns(&rbacv1.RoleBinding{}).
		Watches(&corev1.Namespace{}, r.namespaceResync(logger)).
		Watches(&corev1.ServiceAccount{}, r.resync(logger), builder.WithPredicates(r.serviceAccountPredicate())).
		Watches(&kservev1alpha2.LLMInferenceServiceConfig{}, r.resync(logger)).
		Named("grid-inference-provider")
	ok, err := utils.IsCrdAvailable(mgr.GetConfig(), gridProviderGVK.GroupVersion().String(), gridProviderGVK.Kind)
	if err != nil {
		return err
	}
	if ok {
		b = b.Watches(gridProvider(), handler.EnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []reconcile.Request {
			annotations := obj.GetAnnotations()
			if annotations[gridSourceName] == "" || annotations[gridSourceNamespace] == "" {
				return nil
			}
			return []reconcile.Request{{NamespacedName: client.ObjectKey{Name: annotations[gridSourceName], Namespace: annotations[gridSourceNamespace]}}}
		}))
	}
	return b.Complete(r)
}

func (r *GridInferenceProviderReconciler) resync(logger logr.Logger) handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, _ client.Object) []reconcile.Request {
		return r.resyncRequests(ctx, logger)
	})
}

func (r *GridInferenceProviderReconciler) namespaceResync(logger logr.Logger) handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, object client.Object) []reconcile.Request {
		return r.resyncRequests(ctx, logger, client.InNamespace(object.GetName()))
	})
}

func (r *GridInferenceProviderReconciler) serviceAccountPredicate() predicate.Predicate {
	return predicate.NewPredicateFuncs(func(object client.Object) bool {
		return object.GetNamespace() == r.Config.Namespace && object.GetName() == r.Config.ServiceAccount
	})
}

func (r *GridInferenceProviderReconciler) resyncRequests(ctx context.Context, logger logr.Logger, options ...client.ListOption) []reconcile.Request {
	list := &kservev1alpha2.LLMInferenceServiceList{}
	if err := r.List(ctx, list, options...); err != nil {
		logger.Error(err, "Failed to resync Grid providers")
		return nil
	}
	requests := make([]reconcile.Request, 0, len(list.Items))
	for _, svc := range list.Items {
		if svc.Annotations[GridEnabledAnnotation] == "true" || controllerutil.ContainsFinalizer(&svc, gridFinalizer) {
			requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&svc)})
		}
	}
	return requests
}

func (r *GridInferenceProviderReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	svc := &kservev1alpha2.LLMInferenceService{}
	if err := r.Get(ctx, req.NamespacedName, svc); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	eligible := svc.DeletionTimestamp.IsZero() && svc.Annotations[GridEnabledAnnotation] == "true" && !kserveutils.GetForceStopRuntime(svc)
	if eligible {
		ns := &corev1.Namespace{}
		if err := r.Get(ctx, client.ObjectKey{Name: svc.Namespace}, ns); err != nil {
			return ctrl.Result{}, err
		}
		eligible = r.Config.NamespaceSelector != nil && r.Config.NamespaceSelector.Matches(labels.Set(ns.Labels))
	}
	var attachment *gridAttachment
	publishSvc := svc.DeepCopy()
	if eligible {
		// Match the existing LLM controller's config merge semantics, including
		// inherited model names and routing-group settings.
		helper := &LLMInferenceServiceReconciler{Client: r.Client}
		specs := make([]kservev1alpha2.LLMInferenceServiceSpec, 0, len(svc.Spec.BaseRefs)+1)
		for _, ref := range svc.Spec.BaseRefs {
			cfg, err := helper.getConfig(ctx, svc, ref.Name)
			if err != nil {
				return ctrl.Result{}, err
			}
			if cfg != nil {
				specs = append(specs, cfg.Spec)
			}
		}
		spec, err := kservellmisvc.MergeSpecs(ctx, append(specs, svc.Spec)...)
		if err != nil {
			return ctrl.Result{}, err
		}
		publishSvc.Spec = spec
		attachment = gridAttachedEndpoint(ctx, publishSvc)
		eligible = attachment != nil
		if eligible {
			_, mappingErr := r.RESTMapper().RESTMapping(gridProviderGVK.GroupKind(), gridProviderGVK.Version)
			if mappingErr != nil && !meta.IsNoMatchError(mappingErr) {
				return ctrl.Result{}, mappingErr
			}
			eligible = mappingErr == nil
		}
	}
	if !eligible {
		if controllerutil.ContainsFinalizer(svc, gridFinalizer) {
			complete, err := r.cleanup(ctx, svc)
			if err != nil {
				return ctrl.Result{}, err
			}
			if !complete {
				return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
			}
			if err := r.setFinalizer(ctx, svc, false); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}
	if !controllerutil.ContainsFinalizer(svc, gridFinalizer) {
		if err := r.setFinalizer(ctx, svc, true); err != nil {
			return ctrl.Result{}, err
		}
	}
	// Credentials may be filtered out of the manager's Secret cache.
	if err := r.checkIdentity(ctx); err != nil {
		// Revoke existing access before reporting invalid credentials.
		complete, cleanupErr := r.cleanup(ctx, svc)
		if cleanupErr != nil {
			return ctrl.Result{}, cleanupErr
		}
		if !complete {
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}
		return ctrl.Result{}, err
	}
	if err := r.reconcileAccess(ctx, svc); err != nil {
		return ctrl.Result{}, err
	}
	publishSvc.Spec.Model.Name = ptr.To(attachment.Model)
	if err := r.reconcileProvider(ctx, publishSvc, attachment.Endpoint); err != nil {
		return ctrl.Result{}, err
	}
	// Refresh external credentials even when their Secret is outside the cache.
	return ctrl.Result{RequeueAfter: time.Minute}, nil
}

func (r *GridInferenceProviderReconciler) setFinalizer(ctx context.Context, svc *kservev1alpha2.LLMInferenceService, add bool) error {
	// Patch only the metadata delta: replacing a typed service would drop fields
	// introduced by a newer installed KServe schema. The resourceVersion check
	// also prevents overwriting finalizers added concurrently by other controllers.
	before := svc.DeepCopy()
	if add {
		controllerutil.AddFinalizer(svc, gridFinalizer)
	} else {
		controllerutil.RemoveFinalizer(svc, gridFinalizer)
	}
	return r.Patch(ctx, svc, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
}

func (r *GridInferenceProviderReconciler) checkIdentity(ctx context.Context) error {
	sa := &corev1.ServiceAccount{}
	if err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: r.Config.Namespace, Name: r.Config.ServiceAccount}, sa); err != nil {
		return fmt.Errorf("get Grid service account: %w", err)
	}
	secret := &corev1.Secret{}
	if err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: r.Config.Namespace, Name: r.Config.TokenSecret}, secret); err != nil {
		return fmt.Errorf("get Grid credential Secret: %w", err)
	}
	if secret.Type != corev1.SecretTypeServiceAccountToken || secret.Annotations[corev1.ServiceAccountNameKey] != sa.Name ||
		secret.Annotations[corev1.ServiceAccountUIDKey] != string(sa.UID) || len(secret.Data[corev1.ServiceAccountTokenKey]) == 0 {
		return fmt.Errorf("Grid credential Secret %s/%s must contain a populated token for service account %s", secret.Namespace, secret.Name, sa.Name)
	}
	return nil
}

func gridMetadata(svc *kservev1alpha2.LLMInferenceService) metav1.ObjectMeta {
	return metav1.ObjectMeta{Name: gridResourceName(svc), Labels: map[string]string{constants.ODHManaged: "true"},
		Annotations: map[string]string{gridSourceUID: string(svc.UID), gridSourceName: svc.Name, gridSourceNamespace: svc.Namespace}}
}

func gridOwned(obj client.Object, svc *kservev1alpha2.LLMInferenceService) bool {
	return obj.GetAnnotations()[gridSourceUID] == string(svc.UID) && obj.GetAnnotations()[gridSourceName] == svc.Name && obj.GetAnnotations()[gridSourceNamespace] == svc.Namespace
}

func (r *GridInferenceProviderReconciler) reconcileAccess(ctx context.Context, svc *kservev1alpha2.LLMInferenceService) error {
	role := &rbacv1.Role{ObjectMeta: gridMetadata(svc)}
	role.Namespace = svc.Namespace
	binding := &rbacv1.RoleBinding{ObjectMeta: gridMetadata(svc)}
	binding.Namespace = svc.Namespace
	for _, obj := range []client.Object{role, binding} {
		_, err := controllerutil.CreateOrUpdate(ctx, r.Client, obj, func() error {
			if obj.GetResourceVersion() != "" && !gridOwned(obj, svc) {
				return fmt.Errorf("refusing to overwrite unmanaged Grid access %s", obj.GetName())
			}
			if !obj.GetDeletionTimestamp().IsZero() {
				return fmt.Errorf("Grid access %s is still deleting", obj.GetName())
			}
			if err := controllerutil.SetControllerReference(svc, obj, r.Scheme()); err != nil {
				return err
			}
			switch resource := obj.(type) {
			case *rbacv1.Role:
				resource.Rules = []rbacv1.PolicyRule{{APIGroups: []string{"serving.kserve.io"}, Resources: []string{"llminferenceservices"}, ResourceNames: []string{svc.Name}, Verbs: []string{"get"}}}
			case *rbacv1.RoleBinding:
				resource.RoleRef = rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: role.Name}
				resource.Subjects = []rbacv1.Subject{{Kind: "ServiceAccount", Name: r.Config.ServiceAccount, Namespace: r.Config.Namespace}}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *GridInferenceProviderReconciler) reconcileProvider(ctx context.Context, svc *kservev1alpha2.LLMInferenceService, endpoint string) error {
	provider := gridProvider()
	provider.SetName(gridResourceName(svc))
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, provider, func() error {
		if provider.GetResourceVersion() != "" && !gridOwned(provider, svc) {
			return fmt.Errorf("refusing to overwrite unmanaged Grid provider %s", provider.GetName())
		}
		if !provider.GetDeletionTimestamp().IsZero() {
			return fmt.Errorf("Grid provider %s is still deleting", provider.GetName())
		}
		metadata := gridMetadata(svc)
		annotations := provider.GetAnnotations()
		if annotations == nil {
			annotations = map[string]string{}
		}
		for key, value := range metadata.Annotations {
			annotations[key] = value
		}
		provider.SetAnnotations(annotations)
		providerLabels := provider.GetLabels()
		if providerLabels == nil {
			providerLabels = map[string]string{}
		}
		providerLabels[constants.ODHManaged] = "true"
		provider.SetLabels(providerLabels)
		model := svc.Name
		if svc.Spec.Model.Name != nil && *svc.Spec.Model.Name != "" {
			model = *svc.Spec.Model.Name
		}
		siteLabels := map[string]interface{}{}
		for key, value := range r.Config.SiteLabels {
			siteLabels[key] = value
		}
		// Preserve optional administrator-managed fields (trafficPolicy, metrics,
		// cost, healthCheck) while reconciling the discovery and identity fields.
		spec, _, err := unstructured.NestedMap(provider.Object, "spec")
		if err != nil {
			return err
		}
		if spec == nil {
			spec = map[string]interface{}{}
		}
		for key, value := range map[string]interface{}{
			"gridNetworkRef": r.Config.NetworkName, "providerKind": "vllm", "backendKind": "local_model",
			"endpoint":     endpoint,
			"models":       []interface{}{map[string]interface{}{"name": model, "capabilities": []interface{}{"text_generation"}}},
			"siteSelector": map[string]interface{}{"matchLabels": siteLabels},
			"auth":         map[string]interface{}{"strategy": "bearer_token", "manual": false, "secretRef": map[string]interface{}{"name": r.Config.TokenSecret, "namespace": r.Config.Namespace, "key": "token"}},
		} {
			spec[key] = value
		}
		return unstructured.SetNestedMap(provider.Object, spec, "spec")
	})
	return err
}

func (r *GridInferenceProviderReconciler) cleanup(ctx context.Context, svc *kservev1alpha2.LLMInferenceService) (bool, error) {
	provider := gridProvider()
	provider.SetName(gridResourceName(svc))
	objects := []client.Object{
		&rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: gridResourceName(svc), Namespace: svc.Namespace}},
		&rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: gridResourceName(svc), Namespace: svc.Namespace}},
		provider,
	}
	var cleanupErrors []error
	// Request every deletion even if one resource is held by another finalizer.
	for _, obj := range objects {
		if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
			if !apierrors.IsNotFound(err) && !meta.IsNoMatchError(err) {
				cleanupErrors = append(cleanupErrors, err)
			}
			continue
		}
		if !gridOwned(obj, svc) {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("refusing to delete unmanaged Grid resource %s", obj.GetName()))
			continue
		}
		uid := obj.GetUID()
		if err := r.Delete(ctx, obj, &client.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}}); client.IgnoreNotFound(err) != nil {
			cleanupErrors = append(cleanupErrors, err)
		}
	}
	if err := errors.Join(cleanupErrors...); err != nil {
		return false, err
	}
	// A terminating RoleBinding still grants access. Confirm actual absence of
	// every resource rather than relying on a cached, pre-delete finalizer list.
	complete := true
	for _, obj := range objects {
		if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
			if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
				continue
			}
			return false, err
		}
		complete = false
	}
	return complete, nil
}

type gridAttachment struct {
	Endpoint string
	Model    string
}

// gridAttachedEndpoint selects a named cluster-internal address reported by KServe.
// KServe owns HTTPRoute discovery and group routing; do not reconstruct its URLs.
func gridAttachedEndpoint(ctx context.Context, svc *kservev1alpha2.LLMInferenceService) *gridAttachment {
	if svc.Status.Router == nil {
		return nil
	}
	var candidates []gridAttachment
	for _, address := range svc.Status.Addresses {
		if address.Origin == nil || address.URL == nil {
			continue
		}
		name := ptr.Deref(address.Name, "")
		if name != "gateway-internal" && name != "gateway-internal-model-routing" {
			continue
		}
		if !kservellmisvc.IsClusterLocalURL(address.URL) ||
			(address.URL.Scheme != "http" && address.URL.Scheme != "https") || address.URL.User != nil {
			continue
		}
		origin := address.Origin
		originNS := string(ptr.Deref(origin.Namespace, gatewayv1.Namespace(svc.Namespace)))
		if originNS == "" {
			originNS = svc.Namespace
		}
		if string(origin.Kind) != "Gateway" || string(origin.Group) != gatewayv1.GroupName ||
			!slices.ContainsFunc(svc.Status.Router.Gateways, func(observed kservev1alpha2.ObservedGateway) bool {
				ns := string(ptr.Deref(observed.Namespace, gatewayv1.Namespace(svc.Namespace)))
				if ns == "" {
					ns = svc.Namespace
				}
				return observed.Name == origin.Name && ns == originNS
			}) {
			continue
		}
		if model := gridAddressModel(ctx, address, svc); model != "" {
			candidates = append(candidates, gridAttachment{Endpoint: address.URL.String(), Model: model})
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	return gridPreferredAttachment(candidates, svc)
}

func gridPreferredAttachment(candidates []gridAttachment, svc *kservev1alpha2.LLMInferenceService) *gridAttachment {
	// Prefer shared publisher and group model-routing URLs so KServe retains
	// weighted traffic splitting. Participant URLs remain a fallback.
	model := ptr.Deref(svc.Spec.Model.Name, svc.Name)
	if model == "" {
		model = svc.Name
	}
	rank := func(candidate gridAttachment) int {
		parsed, _ := url.Parse(candidate.Endpoint)
		if parsed != nil {
			path := strings.TrimRight(parsed.Path, "/")
			if path == "/publishers/"+svc.Namespace+"/models/"+model {
				return 0
			}
			if path == "" && (svc.Spec.Router.HasGroup() || (svc.Status.Router != nil && svc.Status.Router.Group != nil)) {
				return 1
			}
			if candidate.Model == model && path == "/"+svc.Namespace+"/"+svc.Name {
				return 2
			}
		}
		if candidate.Model == model {
			return 3
		}
		return 4
	}
	slices.SortFunc(candidates, func(a, b gridAttachment) int {
		if diff := rank(a) - rank(b); diff != 0 {
			return diff
		}
		if aHTTPS, bHTTPS := strings.HasPrefix(a.Endpoint, "https://"), strings.HasPrefix(b.Endpoint, "https://"); aHTTPS != bHTTPS {
			if aHTTPS {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Endpoint, b.Endpoint)
	})
	return &candidates[0]
}

func gridAddressModel(ctx context.Context, address kservev1alpha2.SourcedAddress, svc *kservev1alpha2.LLMInferenceService) string {
	model := ptr.Deref(svc.Spec.Model.Name, svc.Name)
	if model == "" {
		model = svc.Name
	}
	models := address.Models
	if len(models) == 0 {
		// Retain compatibility with addresses written before Models existed,
		// using KServe's own path-dependent model naming rules.
		effective := svc.DeepCopy()
		effective.Spec.Model.Name = ptr.To(model)
		models = kservellmisvc.SourcedAddress(ctx, kservellmisvc.DiscoveredURL{URL: address.URL}, effective).Models
	}
	for _, name := range []string{model, "publishers/" + svc.Namespace + "/models/" + model} {
		if slices.ContainsFunc(models, func(m kservev1alpha2.ModelSourcedAddressStatus) bool { return m.Name == name }) {
			return name
		}
	}
	return ""
}
