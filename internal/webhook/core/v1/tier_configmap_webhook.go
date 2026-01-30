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
	"strings"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/serving/reconcilers"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
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
	oldConfigMap, ok := oldObj.(*corev1.ConfigMap)
	if !ok {
		return nil, fmt.Errorf("expected a ConfigMap object but got %T", oldObj)
	}

	newConfigMap, ok := newObj.(*corev1.ConfigMap)
	if !ok {
		return nil, fmt.Errorf("expected a ConfigMap object but got %T", newObj)
	}

	// Only check rename prevention for tier-to-group-mapping ConfigMap
	maasNamespace := reconcilers.GetMaasNamespace()
	if newConfigMap.Name == reconcilers.TierConfigMapName && newConfigMap.Namespace == maasNamespace {
		if err := validateTierNamesNotRemoved(oldConfigMap, newConfigMap); err != nil {
			log := tierConfigMaplog.WithValues("namespace", newConfigMap.Namespace, "name", newConfigMap.Name)
			log.Error(err, "Tier rename prevention validation failed")
			return nil, err
		}
	}

	return v.validate(newConfigMap)
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type ConfigMap.
func (v *TierConfigMapValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

// validate checks the ConfigMap and validates tier levels and names if it's the tier-to-group-mapping ConfigMap.
func (v *TierConfigMapValidator) validate(configMap *corev1.ConfigMap) (admission.Warnings, error) {
	// Only validate the tier-to-group-mapping ConfigMap in the MaaS namespace
	maasNamespace := reconcilers.GetMaasNamespace()
	if configMap.Name != reconcilers.TierConfigMapName || configMap.Namespace != maasNamespace {
		return nil, nil
	}

	log := tierConfigMaplog.WithValues("namespace", configMap.Namespace, "name", configMap.Name)
	log.Info("Validating tier-to-group-mapping ConfigMap")

	if err := validateTierNames(configMap); err != nil {
		log.Error(err, "Tier name validation failed")
		return nil, err
	}

	if err := validateTierLevels(configMap); err != nil {
		log.Error(err, "Tier level validation failed")
		return nil, err
	}

	log.Info("Tier ConfigMap validation passed")
	return nil, nil
}

// validateTierLevels checks that no two tiers have the same level value.
func validateTierLevels(configMap *corev1.ConfigMap) error {
	tiers, err := reconcilers.ParseTiersFromConfigMap(configMap)
	if err != nil {
		return err
	}
	if tiers == nil {
		return nil
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

// validateTierNames checks that all tier names are valid Kubernetes DNS-1123 labels.
func validateTierNames(configMap *corev1.ConfigMap) error {
	tiers, err := reconcilers.ParseTiersFromConfigMap(configMap)
	if err != nil {
		return err
	}
	if tiers == nil {
		return nil
	}

	for _, tier := range tiers {
		if errs := validation.IsDNS1123Label(tier.Name); len(errs) > 0 {
			tierConfigMaplog.Error(nil,
				"invalid tier name",
				"tierName", tier.Name,
				"errors", errs,
			)
			return fmt.Errorf("invalid tier name '%s': %s", tier.Name, strings.Join(errs, ", "))
		}
	}

	return nil
}

// validateTierNamesNotRemoved checks that no existing tier names are removed or renamed.
func validateTierNamesNotRemoved(oldConfigMap, newConfigMap *corev1.ConfigMap) error {
	oldTierNames, err := extractTierNames(oldConfigMap)
	if err != nil {
		return fmt.Errorf("failed to parse old tier configuration: %w", err)
	}

	newTierNames, err := extractTierNames(newConfigMap)
	if err != nil {
		return fmt.Errorf("failed to parse new tier configuration: %w", err)
	}

	newTierSet := make(map[string]bool)
	for _, name := range newTierNames {
		newTierSet[name] = true
	}

	for _, oldName := range oldTierNames {
		if !newTierSet[oldName] {
			tierConfigMaplog.Error(nil,
				"tier removal/rename detected",
				"tierName", oldName,
			)
			return fmt.Errorf("tier '%s' cannot be removed or renamed: tier names are immutable", oldName)
		}
	}

	return nil
}

// extractTierNames extracts tier names from a ConfigMap.
func extractTierNames(configMap *corev1.ConfigMap) ([]string, error) {
	tiers, err := reconcilers.ParseTiersFromConfigMap(configMap)
	if err != nil {
		return nil, err
	}
	if tiers == nil {
		return nil, nil
	}

	names := make([]string, len(tiers))
	for i, tier := range tiers {
		names[i] = tier.Name
	}
	return names, nil
}
