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

package v1

import (
	"context"
	"fmt"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/serving/reconcilers"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	"sigs.k8s.io/yaml"
)

// +kubebuilder:webhook:path=/validate--v1-configmap,mutating=false,failurePolicy=fail,groups="",resources=configmaps,verbs=create;update,versions=v1,name=validating.configmap.odh-model-controller.opendatahub.io,admissionReviewVersions=v1,sideEffects=none

var tierConfigMaplog = logf.Log.WithName("TierConfigMapValidatingWebhook")

// SetupTierConfigMapWebhookWithManager registers the webhook for ConfigMap validation in the manager.
func SetupTierConfigMapWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).For(&corev1.ConfigMap{}).
		WithValidator(&TierConfigMapValidator{}).
		Complete()
}

// TierConfigMapValidator validates the tier-to-group-mapping ConfigMap
// to ensure no two tiers have the same level value.
type TierConfigMapValidator struct{}

var _ webhook.CustomValidator = &TierConfigMapValidator{}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type ConfigMap.
func (v *TierConfigMapValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	configMap, ok := obj.(*corev1.ConfigMap)
	if !ok {
		return nil, fmt.Errorf("expected a ConfigMap object but got %T", obj)
	}

	return v.validate(configMap)
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type ConfigMap.
func (v *TierConfigMapValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	configMap, ok := newObj.(*corev1.ConfigMap)
	if !ok {
		return nil, fmt.Errorf("expected a ConfigMap object but got %T", newObj)
	}

	return v.validate(configMap)
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type ConfigMap.
func (v *TierConfigMapValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

// validate checks the ConfigMap and validates tier levels if it's the tier-to-group-mapping ConfigMap.
func (v *TierConfigMapValidator) validate(configMap *corev1.ConfigMap) (admission.Warnings, error) {
	// Only validate the tier-to-group-mapping ConfigMap in the MaaS namespace
	maasNamespace := reconcilers.GetMaasNamespace()
	if configMap.Name != reconcilers.TierConfigMapName || configMap.Namespace != maasNamespace {
		return nil, nil
	}

	log := tierConfigMaplog.WithValues("namespace", configMap.Namespace, "name", configMap.Name)
	log.Info("Validating tier-to-group-mapping ConfigMap")

	if err := validateTierLevels(configMap); err != nil {
		log.Error(err, "Tier ConfigMap validation failed")
		return nil, err
	}

	log.Info("Tier ConfigMap validation passed")
	return nil, nil
}

// validateTierLevels checks that no two tiers have the same level value.
func validateTierLevels(configMap *corev1.ConfigMap) error {
	tiersData, found := configMap.Data["tiers"]
	if !found {
		return nil
	}

	var tiers []reconcilers.Tier
	if err := yaml.Unmarshal([]byte(tiersData), &tiers); err != nil {
		return fmt.Errorf("failed to parse tiers configuration: %w", err)
	}

	// Check for duplicate levels
	seenLevels := make(map[int]string)
	for _, tier := range tiers {
		if existingTierName, exists := seenLevels[tier.Level]; exists {
			tierConfigMaplog.Error(nil,
				"duplicate tier level detected",
				"level", tier.Level,
				"existingTier", existingTierName,
				"conflictingTier", tier.Name,
			)
			return fmt.Errorf("duplicate levels found: each tier must have a unique level")
		}
		seenLevels[tier.Level] = tier.Name
	}

	return nil
}
