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

	"github.com/go-logr/logr"
	kuadrantv1 "github.com/kuadrant/kuadrant-operator/api/v1"
	istioclientv1alpha3 "istio.io/client-go/pkg/apis/networking/v1alpha3"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
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
	Scheme            *runtime.Scheme
	envoyFilterLoader resources.EnvoyFilterTemplateLoader
	envoyFilterStore  resources.EnvoyFilterStore
	authPolicyLoader  resources.AuthPolicyTemplateLoader
	authPolicyStore   resources.AuthPolicyStore
	deltaProcessor    processors.DeltaProcessor
}

func NewGatewayReconciler(client client.Client, scheme *runtime.Scheme) *GatewayReconciler {
	return &GatewayReconciler{
		Client:            client,
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

	shouldCreateEnvoyFilter := utils.ShouldCreateEnvoyFilterForGateway(gateway)
	shouldCreateAuthPolicy := !utils.IsExplicitlyUnmanaged(gateway) && !utils.IsOwnedByPlatformController(gateway)

	if shouldCreateEnvoyFilter {
		if err := r.reconcileEnvoyFilter(ctx, logger, gateway); err != nil {
			logger.Error(err, "Failed to reconcile EnvoyFilter")
			return ctrl.Result{}, err
		}
	} else {
		if err := r.deleteEnvoyFilterIfManaged(ctx, logger, gateway); err != nil {
			logger.Error(err, "Failed to delete EnvoyFilter")
			return ctrl.Result{}, err
		}
	}

	if shouldCreateAuthPolicy {
		if err := r.reconcileAuthPolicy(ctx, logger, gateway); err != nil {
			logger.Error(err, "Failed to reconcile AuthPolicy")
			return ctrl.Result{}, err
		}
	} else {
		if err := r.deleteAuthPolicyIfManaged(ctx, logger, gateway); err != nil {
			logger.Error(err, "Failed to delete AuthPolicy")
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
	desired, err := r.authPolicyLoader.Load(ctx, resources.AuthPolicyTarget{
		Kind:      "Gateway",
		Name:      gateway.Name,
		Namespace: gateway.Namespace,
		AuthType:  constants.UserDefined,
	},
		resources.WithLabels(map[string]string{"app.kubernetes.io/name": "llminferenceservice-auth"}),
		resources.WithAudiences(audiences),
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

func (r *GatewayReconciler) SetupWithManager(mgr ctrl.Manager, setupLog logr.Logger) error {
	if ok, err := utils.IsCrdAvailable(mgr.GetConfig(), gatewayapiv1.GroupVersion.String(), "Gateway"); err != nil {
		setupLog.Error(err, "Failed to check CRD availability for Gateway")
		return nil
	} else if !ok {
		setupLog.Info("Gateway CRD not available, skipping Gateway controller setup")
		return nil
	}

	if ok, err := utils.IsCrdAvailable(mgr.GetConfig(), istioclientv1alpha3.SchemeGroupVersion.String(), "EnvoyFilter"); err != nil {
		setupLog.Error(err, "Failed to check CRD availability for EnvoyFilter")
		return nil
	} else if !ok {
		setupLog.Info("EnvoyFilter CRD not available, skipping Gateway controller setup")
		return nil
	}

	if ok, err := utils.IsCrdAvailable(mgr.GetConfig(), kuadrantv1.GroupVersion.String(), "AuthPolicy"); err != nil {
		setupLog.Error(err, "Failed to check CRD availability for AuthPolicy")
		return nil
	} else if !ok {
		setupLog.Info("AuthPolicy CRD not available, skipping Gateway controller setup")
		return nil
	}

	setupLog.Info("Setting up Gateway controller for EnvoyFilter/AuthPolicy bootstrap")

	return ctrl.NewControllerManagedBy(mgr).
		For(&gatewayapiv1.Gateway{}).
		Owns(&istioclientv1alpha3.EnvoyFilter{}).
		Owns(&kuadrantv1.AuthPolicy{}).
		WithEventFilter(predicate.Funcs{
			CreateFunc: func(e event.CreateEvent) bool {
				if gw, ok := e.Object.(*gatewayapiv1.Gateway); ok {
					return utils.ShouldCreateEnvoyFilterForGateway(gw)
				}
				return true
			},
			UpdateFunc: func(e event.UpdateEvent) bool {
				if gw, ok := e.ObjectNew.(*gatewayapiv1.Gateway); ok {
					oldManaged := e.ObjectOld.GetLabels()[constants.ODHManagedLabel]
					newManaged := e.ObjectNew.GetLabels()[constants.ODHManagedLabel]
					if oldManaged != newManaged {
						return true
					}
					oldTLS := e.ObjectOld.GetAnnotations()[constants.AuthorinoTLSBootstrapAnnotation]
					newTLS := e.ObjectNew.GetAnnotations()[constants.AuthorinoTLSBootstrapAnnotation]
					if oldTLS != newTLS {
						return true
					}
					return utils.ShouldCreateEnvoyFilterForGateway(gw)
				}
				return utils.IsManagedByOpenDataHub(e.ObjectNew)
			},
			DeleteFunc: func(e event.DeleteEvent) bool {
				if _, ok := e.Object.(*gatewayapiv1.Gateway); ok {
					return false
				}
				return utils.IsManagedByOpenDataHub(e.Object)
			},
		}).
		Named("gateway-auth-bootstrap").
		Complete(r)
}
