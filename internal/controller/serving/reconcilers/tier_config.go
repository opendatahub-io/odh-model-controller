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
	"encoding/json"
	"errors"
	"fmt"

	"github.com/go-logr/logr"
	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/resources"
)

const (
	TierAnnotationKey = "alpha.maas.opendatahub.io/tiers"

	// TODO: Review if these constants can be moved to some configuration.
	TierConfigMapName      = "tier-to-group-mapping"
	DefaultTenantNamespace = "maas-api"
	DefaultTenantName      = "maas-default-gateway"

	// Environment variable names for configuration
	MaasNamespaceEnvVar  = "MAAS_NAMESPACE"
	MaasTenantNameEnvVar = "MAAS_TENANT_NAME"

	// Event reasons for tier-related events
	EventReasonTierNotFound         = "TierNotFound"
	EventReasonTierConfigUnavail    = "TierConfigUnavailable"
	EventReasonTierMisconfiguration = "TierMisconfiguration"
)

// GetMaasNamespace returns the MaaS namespace to use for tier configuration lookups.
// It reads from the MAAS_NAMESPACE environment variable if set, otherwise returns
// the default namespace "maas-api".
//
// The MaaS namespace is where the tier-to-group-mapping ConfigMap is expected to exist.
func GetMaasNamespace() string {
	return utils.GetEnvOr(MaasNamespaceEnvVar, DefaultTenantNamespace)
}

// GetMaasTenantName returns the MaaS tenant name to use for constructing service account groups.
// It reads from the MAAS_TENANT_NAME environment variable if set, otherwise returns
// the default tenant name "maas-default-gateway".
//
// The tenant name is used in the format: system:serviceaccounts:{tenantName}-tier-{tierName}
func GetMaasTenantName() string {
	return utils.GetEnvOr(MaasTenantNameEnvVar, DefaultTenantName)
}

// IsTierConfigMap checks if the given object is the well-known tier ConfigMap.
// It performs type assertion, namespace validation, and name validation.
// Returns true only if the object is a ConfigMap in the configured MaaS namespace
// with the well-known name.
func IsTierConfigMap(obj client.Object) bool {
	// Type assertion - ensure it's actually a ConfigMap
	cm, ok := obj.(*corev1.ConfigMap)
	if !ok {
		return false
	}

	// This prevents cross-namespace noise and unintended reconciliation storms
	if cm.GetNamespace() != GetMaasNamespace() {
		return false
	}

	return cm.GetName() == TierConfigMapName
}

// Tier represents a subscription tier with associated user groups and level.
//
// Level determines precedence, where higher values take precedence over lower values.
// This can be needed in scenarios when users belong to multiple groups across different tiers.
type Tier struct {
	Name        string   `yaml:"name"`                  // Tier name (e.g., "free", "premium", "enterprise")
	Description string   `yaml:"description,omitempty"` // Human-readable description
	Groups      []string `yaml:"groups"`                // List of groups that belong to this tier
	Level       int      `yaml:"level,omitempty"`       // Level for importance (higher wins)

	// TODO: This type was copied from maas-billing repository. By exporting the types in
	// that repo, we should be able to re-use here.
}

type AnnotationNotFoundError struct{}

func (e *AnnotationNotFoundError) Error() string {
	return "tier annotation not found"
}

// TierNotFoundError indicates that one or more requested tiers do not exist in the ConfigMap.
// This is a user misconfiguration error - the user requested tiers that aren't defined.
type TierNotFoundError struct {
	MissingTiers   []string
	AvailableTiers []string
}

func (e *TierNotFoundError) Error() string {
	return fmt.Sprintf("requested tiers not found in ConfigMap: %v (available: %v)", e.MissingTiers, e.AvailableTiers)
}

type TierConfigLoader struct {
	client           client.Client
	configMapHandler resources.ConfigMapHandler
}

func NewTierConfigLoader(client client.Client) *TierConfigLoader {
	return &TierConfigLoader{
		client:           client,
		configMapHandler: resources.NewConfigMapHandler(client),
	}
}

// ValidateTierAnnotation validates the tier annotation on an LLMInferenceService.
// It parses the annotation, loads the tier configuration, and validates the requested tiers.
// Returns the requested tiers, available tier names, and any error encountered.
// If the annotation is not found, returns (nil, nil, AnnotationNotFoundError).
func (s *TierConfigLoader) ValidateTierAnnotation(ctx context.Context, log logr.Logger, llmisvc *kservev1alpha1.LLMInferenceService) (requestedTiers []string, availableTiers []string, err error) {
	requestedTiers, err = s.parseAnnotation(llmisvc)
	if err != nil {
		return nil, nil, err
	}

	tiers, err := s.loadTiers(ctx, log, GetMaasNamespace())
	if err != nil {
		return requestedTiers, nil, err
	}

	availableTiers = make([]string, 0, len(tiers))
	for _, tier := range tiers {
		availableTiers = append(availableTiers, tier.Name)
	}

	if err := s.validateTiers(requestedTiers, tiers); err != nil {
		return requestedTiers, availableTiers, err
	}

	return requestedTiers, availableTiers, nil
}

// DefinedGroups loads tier configuration and returns the group names
// based on the tier annotation on the LLMInferenceService.
// Returns nil if the annotation is not found or if there's an error (errors are logged).
func (s *TierConfigLoader) DefinedGroups(ctx context.Context, log logr.Logger, llmisvc *kservev1alpha1.LLMInferenceService) ([]string, error) {
	requestedTiers, err := s.parseAnnotation(llmisvc)
	if err != nil {
		var annotationNotFoundError *AnnotationNotFoundError
		if errors.As(err, &annotationNotFoundError) {
			return nil, nil
		}
		return nil, err
	}

	tiers, err := s.loadTiers(ctx, log, GetMaasNamespace())
	if err != nil {
		return nil, err
	}

	// Build set of available tier names for quick lookup
	availableTierNames := make(map[string]bool, len(tiers))
	for _, tier := range tiers {
		availableTierNames[tier.Name] = true
	}

	var groupNames []string
	if len(requestedTiers) == 0 {
		// Empty array = all tiers
		for _, tier := range tiers {
			groupNames = append(groupNames, s.ProjectedSAGroup(tier.Name))
		}
	} else {
		// Validate that all requested tiers exist
		var missingTiers []string
		for _, tierName := range requestedTiers {
			if !availableTierNames[tierName] {
				missingTiers = append(missingTiers, tierName)
			}
		}

		if len(missingTiers) > 0 {
			availableList := make([]string, 0, len(tiers))
			for _, tier := range tiers {
				availableList = append(availableList, tier.Name)
			}
			return nil, &TierNotFoundError{
				MissingTiers:   missingTiers,
				AvailableTiers: availableList,
			}
		}

		// All requested tiers exist, build group names
		for _, tierName := range requestedTiers {
			groupNames = append(groupNames, s.ProjectedSAGroup(tierName))
		}
	}

	return groupNames, nil
}

func (s *TierConfigLoader) parseAnnotation(llmisvc *kservev1alpha1.LLMInferenceService) ([]string, error) {
	annotations := llmisvc.GetAnnotations()
	if annotations == nil {
		return nil, &AnnotationNotFoundError{}
	}

	tierAnnotationStr, found := annotations[TierAnnotationKey]
	if !found || tierAnnotationStr == "" {
		return nil, &AnnotationNotFoundError{}
	}

	var tiers []string
	if err := json.Unmarshal([]byte(tierAnnotationStr), &tiers); err != nil {
		return nil, fmt.Errorf("failed to unmarshal tier annotation: %w", err)
	}

	seen := make(map[string]bool)
	uniqueTiers := make([]string, 0, len(tiers))
	for _, tier := range tiers {
		if !seen[tier] {
			seen[tier] = true
			uniqueTiers = append(uniqueTiers, tier)
		}
	}

	return uniqueTiers, nil
}

func (s *TierConfigLoader) validateTiers(requestedTiers []string, tiers []Tier) error {
	if len(requestedTiers) == 0 {
		return nil
	}

	availableTiers := make(map[string]bool)
	for _, tier := range tiers {
		availableTiers[tier.Name] = true
	}

	for _, tier := range requestedTiers {
		if !availableTiers[tier] {
			return fmt.Errorf("tier '%s' not found in tier configuration", tier)
		}
	}

	return nil
}

func (s *TierConfigLoader) loadTiers(ctx context.Context, log logr.Logger, namespace string) ([]Tier, error) {
	log.V(1).Info("Loading tier configuration from ConfigMap", "configmap", TierConfigMapName, "namespace", namespace)

	configMap, err := s.configMapHandler.FetchConfigMap(ctx, log, types.NamespacedName{
		Name:      TierConfigMapName,
		Namespace: namespace,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch tier ConfigMap: %w", err)
	}
	if configMap == nil {
		return nil, fmt.Errorf("tier ConfigMap %s not found in namespace %s", TierConfigMapName, namespace)
	}

	tiersData, found := configMap.Data["tiers"]
	if !found {
		return nil, fmt.Errorf("'tiers' key not found in ConfigMap %s", TierConfigMapName)
	}

	var tiers []Tier
	if err := yaml.Unmarshal([]byte(tiersData), &tiers); err != nil {
		return nil, fmt.Errorf("failed to parse tier configuration: %w", err)
	}

	if len(tiers) == 0 {
		return nil, fmt.Errorf("no tiers configured in ConfigMap %s", TierConfigMapName)
	}

	return tiers, nil
}

func (s *TierConfigLoader) ProjectedSAGroup(tierName string) string {
	return fmt.Sprintf("system:serviceaccounts:%s-tier-%s", GetMaasTenantName(), tierName)
}
