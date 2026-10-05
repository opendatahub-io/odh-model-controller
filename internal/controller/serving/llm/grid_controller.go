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
	"crypto/sha256"
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
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/events"
	"k8s.io/utils/ptr"
	"knative.dev/pkg/kmeta"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
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
	Recorder  events.EventRecorder
}

func gridProvider() *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(gridProviderGVK)
	return obj
}

func gridResourceName(svc *kservev1alpha2.LLMInferenceService, gateway *gatewayv1.Gateway) string {
	// Source UID prevents recreated services from acquiring old access. The
	// Gateway digest distinguishes attachments and Gateway reincarnations.
	parent := "grid-" + svc.Namespace + "-" + strings.ReplaceAll(svc.Name, ".", "-")
	identity := sha256.Sum256([]byte(gateway.Namespace + "/" + gateway.Name + "/" + string(gateway.UID)))
	return kmeta.ChildName(parent, fmt.Sprintf("-%s-%x", svc.UID, identity[:6]))
}

// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gateways,verbs=get;list;watch
// +kubebuilder:rbac:groups=grid.praxis.fast,resources=inferenceproviders,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles;rolebindings,verbs=get;list;watch;create;update;patch;delete

func (r *GridInferenceProviderReconciler) SetupWithManager(mgr ctrl.Manager, logger logr.Logger) error {
	// Even when publishing is disabled, run cleanup for existing finalizers.
	b := ctrl.NewControllerManagedBy(mgr).For(&kservev1alpha2.LLMInferenceService{}).
		Owns(&rbacv1.Role{}).Owns(&rbacv1.RoleBinding{}).
		Watches(&corev1.Namespace{}, r.namespaceResync(logger)).
		Watches(&corev1.ServiceAccount{}, r.resync(logger)).
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
	gatewayAvailable, err := utils.IsCrdAvailable(mgr.GetConfig(), gatewayv1.GroupVersion.String(), "Gateway")
	if err != nil {
		return err
	}
	if gatewayAvailable {
		b = b.Watches(&gatewayv1.Gateway{}, r.resync(logger))
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

func (r *GridInferenceProviderReconciler) resyncRequests(ctx context.Context, logger logr.Logger, options ...client.ListOption) []reconcile.Request {
	list := &kservev1alpha2.LLMInferenceServiceList{}
	if err := r.List(ctx, list, options...); err != nil {
		logger.Error(err, "Failed to resync Grid providers")
		return nil
	}
	requests := make([]reconcile.Request, 0, len(list.Items))
	for _, svc := range list.Items {
		requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&svc)})
	}
	return requests
}

type gridPublication struct {
	gateway    *gatewayv1.Gateway
	config     *GridConfig
	attachment gridAttachment
}

func (r *GridInferenceProviderReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	svc := &kservev1alpha2.LLMInferenceService{}
	if err := r.Get(ctx, req.NamespacedName, svc); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	plan, err := r.desiredPublications(ctx, svc)
	if err != nil {
		return ctrl.Result{}, err
	}
	publications := plan.publications
	reconcileErrors := plan.errors
	publishSvc := plan.service
	keep := map[string]bool{}
	for _, publication := range publications {
		keep[gridResourceName(svc, publication.gateway)] = true
	}
	if len(publications) > 0 && !controllerutil.ContainsFinalizer(svc, gridFinalizer) {
		if err := r.setFinalizer(ctx, svc, true); err != nil {
			return ctrl.Result{}, err
		}
	}
	complete := true
	if controllerutil.ContainsFinalizer(svc, gridFinalizer) {
		var err error
		complete, err = r.cleanupExcept(ctx, svc, keep)
		if err != nil {
			return ctrl.Result{}, err
		}
	}
	for _, publication := range publications {
		if err := r.reconcileAccess(ctx, svc, publication); err != nil {
			r.providerWarning(svc, publication.gateway, err)
			reconcileErrors = append(reconcileErrors, err)
			continue
		}
		effective := publishSvc.DeepCopy()
		effective.Spec.Model.Name = ptr.To(publication.attachment.Model)
		if err := r.reconcileProvider(ctx, effective, publication); err != nil {
			r.providerWarning(svc, publication.gateway, err)
			reconcileErrors = append(reconcileErrors, err)
		}
	}
	if len(publications) == 0 && complete && controllerutil.ContainsFinalizer(svc, gridFinalizer) {
		if err := r.setFinalizer(ctx, svc, false); err != nil {
			return ctrl.Result{}, err
		}
	}
	if err := errors.Join(reconcileErrors...); err != nil {
		return ctrl.Result{}, err
	}
	if !complete {
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}
	// Secrets may be outside the label-filtered cache; refresh their validity.
	if len(publications) > 0 {
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}
	return ctrl.Result{}, nil
}

type gridPublicationPlan struct {
	service      *kservev1alpha2.LLMInferenceService
	publications []gridPublication
	errors       []error
}

// desiredPublications distinguishes invalid per-Gateway configuration (revoke
// only that pair) from transient discovery failures (preserve existing resources).
func (r *GridInferenceProviderReconciler) desiredPublications(ctx context.Context, svc *kservev1alpha2.LLMInferenceService) (gridPublicationPlan, error) {
	plan := gridPublicationPlan{service: svc.DeepCopy()}
	eligible := svc.DeletionTimestamp.IsZero() && !kserveutils.GetForceStopRuntime(svc)
	if eligible {
		ns := &corev1.Namespace{}
		if err := r.Get(ctx, client.ObjectKey{Name: svc.Namespace}, ns); err != nil {
			return plan, err
		}
		eligible = ns.Labels[GridAllowedLabel] == "true" && ns.DeletionTimestamp.IsZero()
	}
	publishSvc := plan.service
	if eligible {
		// Match KServe config merge semantics for model and routing-group settings.
		helper := &LLMInferenceServiceReconciler{Client: r.Client}
		specs := make([]kservev1alpha2.LLMInferenceServiceSpec, 0, len(svc.Spec.BaseRefs)+1)
		for _, ref := range svc.Spec.BaseRefs {
			cfg, err := helper.getConfig(ctx, svc, ref.Name)
			if err != nil {
				return plan, err
			}
			if cfg != nil {
				specs = append(specs, cfg.Spec)
			}
		}
		spec, err := kservellmisvc.MergeSpecs(ctx, append(specs, svc.Spec)...)
		if err != nil {
			return plan, err
		}
		publishSvc.Spec = spec
		_, err = r.RESTMapper().RESTMapping(gridProviderGVK.GroupKind(), gridProviderGVK.Version)
		if err != nil && !meta.IsNoMatchError(err) {
			return plan, err
		}
		if err == nil {
			for _, attachment := range gridAttachedEndpoints(ctx, publishSvc) {
				gateway := &gatewayv1.Gateway{}
				if err := r.Get(ctx, attachment.Gateway, gateway); err != nil {
					if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
						continue
					}
					// Preserve existing publications on transient API failures.
					return plan, err
				}
				if !gateway.DeletionTimestamp.IsZero() {
					continue
				}
				cfg, err := GridConfigFromGateway(gateway)
				if err != nil {
					r.providerWarning(svc, gateway, err)
					plan.errors = append(plan.errors, fmt.Errorf("Gateway %s: %w", attachment.Gateway, err))
					continue
				}
				if cfg == nil {
					continue
				}
				if err := r.checkIdentity(ctx, cfg); err != nil {
					r.providerWarning(svc, gateway, err)
					plan.errors = append(plan.errors, fmt.Errorf("Gateway %s: %w", attachment.Gateway, err))
					continue
				}
				plan.publications = append(plan.publications, gridPublication{gateway: gateway, config: cfg, attachment: attachment})
			}
		}
	}
	return plan, nil
}

func (r *GridInferenceProviderReconciler) providerWarning(svc *kservev1alpha2.LLMInferenceService, gateway *gatewayv1.Gateway, err error) {
	if r.Recorder != nil {
		r.Recorder.Eventf(svc, gateway, corev1.EventTypeWarning, "InferenceProviderReconcileFailed", "Reconcile", "Failed to publish Grid InferenceProvider through Gateway %s/%s: %v", gateway.Namespace, gateway.Name, err)
	}
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

func (r *GridInferenceProviderReconciler) checkIdentity(ctx context.Context, cfg *GridConfig) error {
	sa := &corev1.ServiceAccount{}
	if err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: cfg.Namespace, Name: cfg.ServiceAccount}, sa); err != nil {
		return fmt.Errorf("get Grid service account: %w", err)
	}
	secret := &corev1.Secret{}
	if err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: cfg.Namespace, Name: cfg.TokenSecret}, secret); err != nil {
		return fmt.Errorf("get Grid credential Secret: %w", err)
	}
	if secret.Type != corev1.SecretTypeServiceAccountToken || secret.Annotations[corev1.ServiceAccountNameKey] != sa.Name ||
		secret.Annotations[corev1.ServiceAccountUIDKey] != string(sa.UID) || len(secret.Data[corev1.ServiceAccountTokenKey]) == 0 {
		return fmt.Errorf("Grid credential Secret %s/%s must contain a populated token for service account %s", secret.Namespace, secret.Name, sa.Name)
	}
	return nil
}

func gridMetadata(svc *kservev1alpha2.LLMInferenceService, gateway *gatewayv1.Gateway) metav1.ObjectMeta {
	return metav1.ObjectMeta{Name: gridResourceName(svc, gateway), Labels: map[string]string{constants.ODHManaged: "true"},
		Annotations: map[string]string{gridSourceUID: string(svc.UID), gridSourceName: svc.Name, gridSourceNamespace: svc.Namespace, "grid.opendatahub.io/gateway-name": gateway.Name, "grid.opendatahub.io/gateway-namespace": gateway.Namespace, "grid.opendatahub.io/gateway-uid": string(gateway.UID)}}
}

func gridOwned(obj client.Object, svc *kservev1alpha2.LLMInferenceService) bool {
	return obj.GetAnnotations()[gridSourceUID] == string(svc.UID) && obj.GetAnnotations()[gridSourceName] == svc.Name && obj.GetAnnotations()[gridSourceNamespace] == svc.Namespace
}

func (r *GridInferenceProviderReconciler) reconcileAccess(ctx context.Context, svc *kservev1alpha2.LLMInferenceService, publication gridPublication) error {
	cfg := publication.config
	role := &rbacv1.Role{ObjectMeta: gridMetadata(svc, publication.gateway)}
	role.Namespace = svc.Namespace
	binding := &rbacv1.RoleBinding{ObjectMeta: gridMetadata(svc, publication.gateway)}
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
				resource.Subjects = []rbacv1.Subject{{Kind: "ServiceAccount", Name: cfg.ServiceAccount, Namespace: cfg.Namespace}}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *GridInferenceProviderReconciler) reconcileProvider(ctx context.Context, svc *kservev1alpha2.LLMInferenceService, publication gridPublication) error {
	cfg := publication.config
	endpoint := publication.attachment.Endpoint
	provider := gridProvider()
	provider.SetName(gridResourceName(svc, publication.gateway))
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, provider, func() error {
		if provider.GetResourceVersion() != "" && !gridOwned(provider, svc) {
			return fmt.Errorf("refusing to overwrite unmanaged Grid provider %s", provider.GetName())
		}
		if !provider.GetDeletionTimestamp().IsZero() {
			return fmt.Errorf("Grid provider %s is still deleting", provider.GetName())
		}
		metadata := gridMetadata(svc, publication.gateway)
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
		for key, value := range cfg.SiteLabels {
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
			"gridNetworkRef": cfg.NetworkName, "providerKind": "vllm", "backendKind": "local_model",
			"endpoint":     endpoint,
			"models":       []interface{}{map[string]interface{}{"name": model, "capabilities": []interface{}{"text_generation"}}},
			"siteSelector": map[string]interface{}{"matchLabels": siteLabels},
			"auth":         map[string]interface{}{"strategy": "bearer_token", "manual": false, "secretRef": map[string]interface{}{"name": cfg.TokenSecret, "namespace": cfg.Namespace, "key": "token"}},
		} {
			spec[key] = value
		}
		return unstructured.SetNestedMap(provider.Object, spec, "spec")
	})
	return err
}

// cleanupExcept discovers resources by source ownership, so detached/deleted
// Gateways and resources from the old one-provider-per-service scheme are removed.
func (r *GridInferenceProviderReconciler) cleanupExcept(ctx context.Context, svc *kservev1alpha2.LLMInferenceService, keep map[string]bool) (bool, error) {
	var objects []client.Object
	roles := &rbacv1.RoleList{}
	bindings := &rbacv1.RoleBindingList{}
	if err := r.APIReader.List(ctx, roles, client.InNamespace(svc.Namespace)); err != nil {
		return false, err
	}
	if err := r.APIReader.List(ctx, bindings, client.InNamespace(svc.Namespace)); err != nil {
		return false, err
	}
	for i := range bindings.Items {
		if gridOwned(&bindings.Items[i], svc) && !keep[bindings.Items[i].Name] {
			objects = append(objects, &bindings.Items[i])
		}
	}
	for i := range roles.Items {
		if gridOwned(&roles.Items[i], svc) && !keep[roles.Items[i].Name] {
			objects = append(objects, &roles.Items[i])
		}
	}
	providers := &unstructured.UnstructuredList{}
	providers.SetGroupVersionKind(gridProviderGVK.GroupVersion().WithKind("InferenceProviderList"))
	if err := r.APIReader.List(ctx, providers); err != nil {
		if !meta.IsNoMatchError(err) && !apierrors.IsNotFound(err) {
			return false, err
		}
	} else {
		for i := range providers.Items {
			if gridOwned(&providers.Items[i], svc) && !keep[providers.Items[i].GetName()] {
				objects = append(objects, &providers.Items[i])
			}
		}
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
	Gateway  client.ObjectKey
	Endpoint string
	Model    string
}

// gridAttachedEndpoints selects one reported cluster-internal address per Gateway.
// KServe owns HTTPRoute discovery and group routing; do not reconstruct its URLs.
func gridAttachedEndpoints(ctx context.Context, svc *kservev1alpha2.LLMInferenceService) []gridAttachment {
	if svc.Status.Router == nil {
		return nil
	}
	candidates := map[client.ObjectKey][]gridAttachment{}
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
			key := client.ObjectKey{Namespace: originNS, Name: string(origin.Name)}
			candidates[key] = append(candidates[key], gridAttachment{Gateway: key, Endpoint: address.URL.String(), Model: model})
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	keys := make([]client.ObjectKey, 0, len(candidates))
	for key := range candidates {
		keys = append(keys, key)
	}
	slices.SortFunc(keys, func(a, b client.ObjectKey) int { return strings.Compare(a.String(), b.String()) })
	attachments := make([]gridAttachment, 0, len(keys))
	for _, key := range keys {
		attachments = append(attachments, *gridPreferredAttachment(candidates[key], svc))
	}
	return attachments
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
