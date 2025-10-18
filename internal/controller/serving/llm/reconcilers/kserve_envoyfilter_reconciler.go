/*

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

package reconcilers

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	istioclientv1alpha3 "istio.io/client-go/pkg/apis/networking/v1alpha3"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/comparators"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/processors"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/resources"
	parentreconcilers "github.com/opendatahub-io/odh-model-controller/internal/controller/serving/reconcilers"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
)

var _ parentreconcilers.LLMSubResourceReconciler = (*KserveEnvoyFilterReconciler)(nil)

type KserveEnvoyFilterReconciler struct {
	client         client.Client
	scheme         *runtime.Scheme
	deltaProcessor processors.DeltaProcessor
	detector       resources.EnvoyFilterDetector
	templateLoader resources.EnvoyFilterTemplateLoader
	store          resources.EnvoyFilterStore
}

func NewKserveEnvoyFilterReconciler(client client.Client, scheme *runtime.Scheme) *KserveEnvoyFilterReconciler {
	return &KserveEnvoyFilterReconciler{
		client:         client,
		scheme:         scheme,
		deltaProcessor: processors.NewDeltaProcessor(),
		detector:       resources.NewKServeEnvoyFilterDetector(client),
		templateLoader: resources.NewKServeEnvoyFilterTemplateLoader(client),
		store:          resources.NewClientEnvoyFilterStore(client),
	}
}

func (r *KserveEnvoyFilterReconciler) Reconcile(ctx context.Context, log logr.Logger, llmisvc *kservev1alpha1.LLMInferenceService) error {
	log.V(1).Info("Starting EnvoyFilter reconciliation for LLMInferenceService")

	// Check if EnvoyFilter should be created based on annotations
	if !r.detector.Detect(ctx, llmisvc.GetAnnotations()) {
		log.V(1).Info("EnvoyFilter not required for this LLMInferenceService, skipping")
		return nil
	}

	if err := r.reconcileGatewayEnvoyFilter(ctx, log, llmisvc); err != nil {
		log.Error(err, "Failed to reconcile Gateway EnvoyFilter")
		return err
	}

	return nil
}

func (r *KserveEnvoyFilterReconciler) reconcileGatewayEnvoyFilter(ctx context.Context, log logr.Logger, llmisvc *kservev1alpha1.LLMInferenceService) error {
	log.V(1).Info("Reconciling Gateway EnvoyFilter")

	desiredEnvoyFilters, err := r.templateLoader.Load(ctx, llmisvc)
	if err != nil {
		log.Error(err, "Failed to load Gateway EnvoyFilter templates")
		return err
	}

	for _, desired := range desiredEnvoyFilters {
		existing, err := r.getExistingEnvoyFilter(ctx, desired.GetName(), desired.GetNamespace())
		if err != nil && !apierrors.IsNotFound(err) {
			log.Error(err, "Failed to get existing gateway EnvoyFilter", "name", desired.GetName())
			return err
		}

		if err := r.gatewayEnvoyFilterProcessDelta(ctx, log, desired, existing); err != nil {
			log.Error(err, "Failed to process Gateway EnvoyFilter delta", "name", desired.GetName())
			return err
		}
	}

	return nil
}

func (r *KserveEnvoyFilterReconciler) Delete(ctx context.Context, log logr.Logger, llmisvc *kservev1alpha1.LLMInferenceService) error {
	log.V(1).Info("EnvoyFilter cleanup is handled by Gateway OwnerReference")
	// Gateway OwnerReference handles cleanup automatically when Gateway is deleted
	// EnvoyFilters persist as long as Gateway exists, even if LLMInferenceService is deleted
	return nil
}

func (r *KserveEnvoyFilterReconciler) Cleanup(ctx context.Context, log logr.Logger, isvcNs string) error {
	return nil
}

func (r *KserveEnvoyFilterReconciler) getExistingEnvoyFilter(ctx context.Context, name, namespace string) (*istioclientv1alpha3.EnvoyFilter, error) {
	return r.store.Get(ctx, types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	})
}

func (r *KserveEnvoyFilterReconciler) getGatewayFromEnvoyFilter(ctx context.Context, envoyFilter *istioclientv1alpha3.EnvoyFilter) (*gatewayapiv1.Gateway, error) {
	if len(envoyFilter.Spec.TargetRefs) == 0 {
		return nil, fmt.Errorf("EnvoyFilter %s has no targetRefs", envoyFilter.GetName())
	}

	targetRef := envoyFilter.Spec.TargetRefs[0]
	if targetRef.Kind != "Gateway" {
		return nil, fmt.Errorf("EnvoyFilter %s does not target a Gateway", envoyFilter.GetName())
	}

	gatewayName := targetRef.Name
	gatewayNamespace := envoyFilter.GetNamespace()

	gateway := &gatewayapiv1.Gateway{}
	if err := utils.GetResource(ctx, r.client, gatewayNamespace, gatewayName, gateway); err != nil {
		return nil, fmt.Errorf("failed to get Gateway %s/%s: %w", gatewayNamespace, gatewayName, err)
	}

	return gateway, nil
}

func (r *KserveEnvoyFilterReconciler) gatewayEnvoyFilterProcessDelta(ctx context.Context, log logr.Logger, desired *istioclientv1alpha3.EnvoyFilter, existing *istioclientv1alpha3.EnvoyFilter) error {
	log.V(1).Info("Processing Gateway EnvoyFilter delta", "name", desired.GetName())

	delta := r.deltaProcessor.ComputeDelta(comparators.GetEnvoyFilterComparator(), desired, existing)

	if !delta.HasChanges() {
		log.V(1).Info("No delta found for Gateway EnvoyFilter", "name", desired.GetName())
		return nil
	}

	if delta.IsAdded() {
		log.V(1).Info("Delta found", "action", "create", "envoyfilter", desired.GetName())

		gateway, err := r.getGatewayFromEnvoyFilter(ctx, desired)
		if err != nil {
			return fmt.Errorf("failed to get Gateway for EnvoyFilter %s: %w", desired.GetName(), err)
		}
		if err := controllerutil.SetControllerReference(gateway, desired, r.scheme); err != nil {
			return fmt.Errorf("failed to set controller reference to Gateway for EnvoyFilter %s: %w", desired.GetName(), err)
		}

		if err := r.store.Create(ctx, desired); err != nil {
			return fmt.Errorf("failed to create Gateway EnvoyFilter %s: %w", desired.GetName(), err)
		}

	} else if delta.IsUpdated() {
		log.V(1).Info("Delta found", "action", "update", "envoyfilter", existing.GetName())

		gateway, err := r.getGatewayFromEnvoyFilter(ctx, desired)
		if err != nil {
			return fmt.Errorf("failed to get Gateway for EnvoyFilter %s: %w", desired.GetName(), err)
		}
		if err := controllerutil.SetControllerReference(gateway, desired, r.scheme); err != nil {
			return fmt.Errorf("failed to set controller reference to Gateway for EnvoyFilter %s: %w", desired.GetName(), err)
		}

		if err := r.store.Update(ctx, desired); err != nil {
			return fmt.Errorf("failed to update Gateway EnvoyFilter %s: %w", existing.GetName(), err)
		}
	}

	return nil
}
