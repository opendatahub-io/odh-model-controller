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

package hardwareprofile

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	servingv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
)

// ApplyToIsvcResources injects HWP resource identifiers into the InferenceService predictor model.
//
// For each identifier in the profile, applies defaultCount to both requests and limits only when
// the resource name is absent from both maps (preserves user-set values). No-op when profile has
// no identifiers or when predictor.Model is nil.
//
// Parameters:
//   - profile: the resolved HardwareProfile with resource identifiers
//   - predictor: the PredictorSpec whose Model.Resources are mutated in-place
func ApplyToIsvcResources(profile *ResolvedProfile, predictor *servingv1beta1.PredictorSpec) {
	log := logf.Log.WithName("hwp-injector")

	if len(profile.Identifiers) == 0 {
		return
	}
	if predictor.Model == nil {
		log.Info("predictor.Model is nil, skipping HWP resource injection")
		return
	}

	for _, id := range profile.Identifiers {
		resName := corev1.ResourceName(id.Identifier)

		// Apply to requests if not already present (checks requests only).
		if _, exists := predictor.Model.Resources.Requests[resName]; !exists {
			if predictor.Model.Resources.Requests == nil {
				predictor.Model.Resources.Requests = corev1.ResourceList{}
			}
			predictor.Model.Resources.Requests[resName] = id.DefaultCount
		}

		// Apply to limits if not already present, using the current request value
		// (which may be the user's pre-existing value or the defaultCount just set above).
		if _, exists := predictor.Model.Resources.Limits[resName]; !exists {
			if reqVal, hasReq := predictor.Model.Resources.Requests[resName]; hasReq {
				if predictor.Model.Resources.Limits == nil {
					predictor.Model.Resources.Limits = corev1.ResourceList{}
				}
				predictor.Model.Resources.Limits[resName] = reqVal
			}
		}
	}
}

// ApplyToLLMIsvcResources injects HWP resource identifiers into the LLMInferenceService "main" container.
//
// When containers is empty, creates a minimal container named "main" before applying resources.
// When a non-empty slice has no "main" container, logs a warning and returns the unmodified slice.
// For each identifier, applies defaultCount to both requests and limits only when the resource name
// is absent from both maps.
//
// Parameters:
//   - profile: the resolved HardwareProfile with resource identifiers
//   - containers: the current container list from spec.template.containers
//
// Returns the updated container slice (may be a new slice if "main" was created).
func ApplyToLLMIsvcResources(profile *ResolvedProfile, containers []corev1.Container) ([]corev1.Container, error) {
	log := logf.Log.WithName("hwp-injector")

	if len(profile.Identifiers) == 0 {
		return containers, nil
	}

	if len(containers) == 0 {
		// Create a minimal "main" container as the injection target.
		containers = []corev1.Container{{Name: LLMIsvcMainContainerName}}
	}

	mainIdx := -1
	for i, c := range containers {
		if c.Name == LLMIsvcMainContainerName {
			mainIdx = i
			break
		}
	}

	if mainIdx == -1 {
		log.Info("no 'main' container found in non-empty LLMIsvc container list, skipping resource injection")
		return containers, nil
	}

	for _, id := range profile.Identifiers {
		resName := corev1.ResourceName(id.Identifier)

		// Apply to requests if not already present (checks requests only).
		if _, exists := containers[mainIdx].Resources.Requests[resName]; !exists {
			if containers[mainIdx].Resources.Requests == nil {
				containers[mainIdx].Resources.Requests = corev1.ResourceList{}
			}
			containers[mainIdx].Resources.Requests[resName] = id.DefaultCount
		}

		// Apply to limits if not already present, using the current request value
		// (which may be the user's pre-existing value or the defaultCount just set above).
		if _, exists := containers[mainIdx].Resources.Limits[resName]; !exists {
			if reqVal, hasReq := containers[mainIdx].Resources.Requests[resName]; hasReq {
				if containers[mainIdx].Resources.Limits == nil {
					containers[mainIdx].Resources.Limits = corev1.ResourceList{}
				}
				containers[mainIdx].Resources.Limits[resName] = reqVal
			}
		}
	}

	return containers, nil
}

// MergeNodeScheduling merges HWP node scheduling stanzas into existing nodeSelector and tolerations.
//
// For nodeSelector: HWP values overwrite existing values for conflicting keys; a warning is
// generated per overwritten key. HWP-only keys are added without conflict.
// For tolerations: HWP tolerations are prepended; existing tolerations whose TolerationKey does not
// match any HWP toleration are appended. Exact-match duplicates are dropped.
//
// Parameters:
//   - profile: the resolved HardwareProfile with NodeSelector and Tolerations
//   - nodeSelector: the current nodeSelector on the workload (may be nil)
//   - tolerations: the current tolerations on the workload (may be nil)
//   - hwpName: the HardwareProfile name, used in warning messages
//
// Returns the merged nodeSelector, merged tolerations, and any warning strings.
func MergeNodeScheduling(
	profile *ResolvedProfile,
	nodeSelector map[string]string,
	tolerations []corev1.Toleration,
	hwpName string,
) (map[string]string, []corev1.Toleration, []string) {
	var warnings []string

	// Merge nodeSelector: start with existing entries, then overwrite with HWP entries.
	merged := make(map[string]string, len(nodeSelector)+len(profile.NodeSelector))
	for k, v := range nodeSelector {
		merged[k] = v
	}
	for k, v := range profile.NodeSelector {
		if existing, exists := nodeSelector[k]; exists && existing != v {
			warnings = append(warnings, fmt.Sprintf(
				"nodeSelector key %q has value %q which will be overwritten by HardwareProfile %q with value %q",
				k, existing, hwpName, v))
		}
		merged[k] = v
	}
	if len(merged) == 0 {
		merged = nil
	}

	// Merge tolerations: HWP tolerations first, then non-conflicting existing ones.
	hwpTolKeys := make(map[string]bool, len(profile.Tolerations))
	for _, t := range profile.Tolerations {
		hwpTolKeys[TolerationKey(t)] = true
	}
	mergedTols := make([]corev1.Toleration, 0, len(profile.Tolerations)+len(tolerations))
	mergedTols = append(mergedTols, profile.Tolerations...)
	for _, t := range tolerations {
		if !hwpTolKeys[TolerationKey(t)] {
			mergedTols = append(mergedTols, t)
		}
	}

	return merged, mergedTols, warnings
}

// SetNodeScheduling sets the workload's node scheduling fields directly from the HWP profile.
//
// Called after a blanket clear when the HWP reference changed (profileChanged=true). The existing
// nodeSelector and tolerations are ignored; only the profile's values are used.
//
// Parameters:
//   - profile: the resolved HardwareProfile with NodeSelector and Tolerations
//   - nodeSelector: unused; present for API symmetry with MergeNodeScheduling
//   - tolerations: unused; present for API symmetry with MergeNodeScheduling
//
// Returns copies of the HWP nodeSelector and tolerations.
func SetNodeScheduling(
	profile *ResolvedProfile,
	nodeSelector map[string]string,
	tolerations []corev1.Toleration,
) (map[string]string, []corev1.Toleration) {
	var newNS map[string]string
	if len(profile.NodeSelector) > 0 {
		newNS = make(map[string]string, len(profile.NodeSelector))
		for k, v := range profile.NodeSelector {
			newNS[k] = v
		}
	}

	var newTols []corev1.Toleration
	if len(profile.Tolerations) > 0 {
		newTols = make([]corev1.Toleration, len(profile.Tolerations))
		copy(newTols, profile.Tolerations)
	}

	return newNS, newTols
}

// ApplyKueueLabel sets the kueue.x-k8s.io/queue-name label on the workload metadata.
//
// Always overwrites an existing value. When profileChanged is false and the existing label differs
// from the HWP value, a warning is generated. No-op when the profile has no Kueue queue name.
//
// Parameters:
//   - profile: the resolved HardwareProfile; KueueQueueName must be non-empty for this to do anything
//   - meta: the workload ObjectMeta to mutate
//   - profileChanged: suppresses the overwrite warning when the profile just switched
//   - hwpName: the HardwareProfile name, used in warning messages
//
// Returns any warning strings generated.
func ApplyKueueLabel(profile *ResolvedProfile, meta *metav1.ObjectMeta, profileChanged bool, hwpName string) []string {
	if profile.KueueQueueName == "" {
		return nil
	}

	var warnings []string
	if !profileChanged {
		if existing := meta.Labels[KueueQueueNameLabel]; existing != "" && existing != profile.KueueQueueName {
			warnings = append(warnings, fmt.Sprintf(
				"label %q has value %q which will be overwritten by HardwareProfile %q with value %q",
				KueueQueueNameLabel, existing, hwpName, profile.KueueQueueName))
		}
	}

	if meta.Labels == nil {
		meta.Labels = make(map[string]string)
	}
	meta.Labels[KueueQueueNameLabel] = profile.KueueQueueName

	return warnings
}

// RemoveNodeScheduling surgically removes HWP-applied nodeSelector entries and tolerations.
//
// For nodeSelector: removes keys whose current value exactly matches the HWP profile value.
// Keys with different values are preserved (they were manually set by the user after HWP injection).
// For tolerations: removes tolerations whose TolerationKey matches a HWP toleration. Others are kept.
//
// Parameters:
//   - profile: the old HardwareProfile whose entries should be removed
//   - nodeSelector: the current nodeSelector on the workload (may be nil)
//   - tolerations: the current tolerations on the workload (may be nil)
//
// Returns the cleaned nodeSelector and tolerations (nil when all entries were removed).
func RemoveNodeScheduling(
	profile *ResolvedProfile,
	nodeSelector map[string]string,
	tolerations []corev1.Toleration,
) (map[string]string, []corev1.Toleration) {
	var newNS map[string]string
	if len(nodeSelector) > 0 {
		newNS = make(map[string]string, len(nodeSelector))
		for k, v := range nodeSelector {
			newNS[k] = v
		}
		for k, v := range profile.NodeSelector {
			if current, exists := newNS[k]; exists && current == v {
				delete(newNS, k)
			}
		}
		if len(newNS) == 0 {
			newNS = nil
		}
	}

	var newTols []corev1.Toleration
	if len(tolerations) > 0 && len(profile.Tolerations) > 0 {
		hwpTolKeys := make(map[string]bool, len(profile.Tolerations))
		for _, t := range profile.Tolerations {
			hwpTolKeys[TolerationKey(t)] = true
		}
		for _, t := range tolerations {
			if !hwpTolKeys[TolerationKey(t)] {
				newTols = append(newTols, t)
			}
		}
	} else {
		newTols = tolerations
	}

	return newNS, newTols
}

// RemoveKueueLabel removes the kueue.x-k8s.io/queue-name label from the workload metadata.
// No-op when the label is not present.
//
// Parameters:
//   - meta: the workload ObjectMeta to mutate
func RemoveKueueLabel(meta *metav1.ObjectMeta) {
	if meta.Labels != nil {
		delete(meta.Labels, KueueQueueNameLabel)
	}
}
