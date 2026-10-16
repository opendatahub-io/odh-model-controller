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
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// DRAClaimName is the pod-local name reserved for HardwareProfile DRA claims.
	DRAClaimName = "hwp-dra"
)

// ValidateResourceClaimTemplate checks the workload namespace, which can differ
// from the namespace containing the HardwareProfile.
func ValidateResourceClaimTemplate(ctx context.Context, reader client.Reader, name, namespace string) error {
	err := reader.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, &resourcev1.ResourceClaimTemplate{})
	if k8serr.IsNotFound(err) {
		return fmt.Errorf("ResourceClaimTemplate %q not found in workload namespace %q", name, namespace)
	}
	if err != nil {
		return fmt.Errorf("failed reading ResourceClaimTemplate %q in workload namespace %q: %w", name, namespace, err)
	}
	return nil
}

// ReconcileDRAClaims changes only the reserved HWP claim name. When the effective
// HardwareProfile reference is unchanged, the submitted claim spec is preserved exactly.
func ReconcileDRAClaims(
	podClaims *[]corev1.PodResourceClaim,
	resources *corev1.ResourceRequirements,
	oldProfileSelected bool,
	selectedTemplate string,
	referenceChanged bool,
) error {
	if !referenceChanged {
		return nil
	}

	claimPresent := hasPodClaim(*podClaims) || (resources != nil && hasContainerClaim(resources.Claims))
	if !oldProfileSelected && selectedTemplate != "" && claimPresent {
		return fmt.Errorf("claim name %q is already used by a workload claim", DRAClaimName)
	}

	if oldProfileSelected {
		*podClaims = removePodClaim(*podClaims)
		if resources != nil {
			resources.Claims = removeContainerClaim(resources.Claims)
		}
	}

	if selectedTemplate == "" {
		return nil
	}
	if resources == nil {
		return fmt.Errorf("HardwareProfile DRA requires a target container")
	}

	*podClaims = setPodClaim(*podClaims, selectedTemplate)
	resources.Claims = setContainerClaim(resources.Claims)
	return nil
}

// HasDRAClaim reports whether a PodSpec contains the reserved HWP claim in either
// its pod-level claim list or any container resource claims.
func HasDRAClaim(spec *corev1.PodSpec) bool {
	if spec == nil {
		return false
	}
	if hasPodClaim(spec.ResourceClaims) {
		return true
	}
	for _, container := range spec.Containers {
		if hasContainerClaim(container.Resources.Claims) {
			return true
		}
	}
	for _, container := range spec.InitContainers {
		if hasContainerClaim(container.Resources.Claims) {
			return true
		}
	}
	for _, container := range spec.EphemeralContainers {
		if hasContainerClaim(container.Resources.Claims) {
			return true
		}
	}
	return false
}

func hasPodClaim(claims []corev1.PodResourceClaim) bool {
	for _, claim := range claims {
		if claim.Name == DRAClaimName {
			return true
		}
	}
	return false
}

func hasContainerClaim(claims []corev1.ResourceClaim) bool {
	for _, claim := range claims {
		if claim.Name == DRAClaimName {
			return true
		}
	}
	return false
}

func removePodClaim(claims []corev1.PodResourceClaim) []corev1.PodResourceClaim {
	var result []corev1.PodResourceClaim
	for _, claim := range claims {
		if claim.Name != DRAClaimName {
			result = append(result, claim)
		}
	}
	return result
}

func removeContainerClaim(claims []corev1.ResourceClaim) []corev1.ResourceClaim {
	var result []corev1.ResourceClaim
	for _, claim := range claims {
		if claim.Name != DRAClaimName {
			result = append(result, claim)
		}
	}
	return result
}

// ValidateDRAContainerClaims ensures only the selected regular container uses
// the HWP-owned claim. Pass -1 for targetIndex when the target is separate from
// the PodSpec containers, as it is for an InferenceService Model predictor.
func ValidateDRAContainerClaims(containers, initContainers []corev1.Container, ephemeralContainers []corev1.EphemeralContainer, targetIndex int) error {
	for i := range containers {
		if i != targetIndex && hasContainerClaim(containers[i].Resources.Claims) {
			return fmt.Errorf("claim name %q is already used by non-target container %q", DRAClaimName, containers[i].Name)
		}
	}
	for i := range initContainers {
		if hasContainerClaim(initContainers[i].Resources.Claims) {
			return fmt.Errorf("claim name %q is already used by init container %q", DRAClaimName, initContainers[i].Name)
		}
	}
	for i := range ephemeralContainers {
		if hasContainerClaim(ephemeralContainers[i].Resources.Claims) {
			return fmt.Errorf("claim name %q is already used by ephemeral container %q", DRAClaimName, ephemeralContainers[i].Name)
		}
	}
	return nil
}

// RemoveDRAContainerClaims clears a previously owned claim from every
// container, including a renamed target, when switching or removing profiles.
func RemoveDRAContainerClaims(containers, initContainers []corev1.Container, ephemeralContainers []corev1.EphemeralContainer) {
	for i := range containers {
		containers[i].Resources.Claims = removeContainerClaim(containers[i].Resources.Claims)
	}
	for i := range initContainers {
		initContainers[i].Resources.Claims = removeContainerClaim(initContainers[i].Resources.Claims)
	}
	for i := range ephemeralContainers {
		ephemeralContainers[i].Resources.Claims = removeContainerClaim(ephemeralContainers[i].Resources.Claims)
	}
}

func setPodClaim(claims []corev1.PodResourceClaim, template string) []corev1.PodResourceClaim {
	want := corev1.PodResourceClaim{Name: DRAClaimName, ResourceClaimTemplateName: &template}
	result := make([]corev1.PodResourceClaim, 0, len(claims)+1)
	found := false
	for _, claim := range claims {
		if claim.Name != DRAClaimName {
			result = append(result, claim)
		} else if !found {
			result = append(result, want)
			found = true
		}
	}
	if !found {
		result = append(result, want)
	}
	return result
}

func setContainerClaim(claims []corev1.ResourceClaim) []corev1.ResourceClaim {
	want := corev1.ResourceClaim{Name: DRAClaimName}
	result := make([]corev1.ResourceClaim, 0, len(claims)+1)
	found := false
	for _, claim := range claims {
		if claim.Name != DRAClaimName {
			result = append(result, claim)
		} else if !found {
			result = append(result, want)
			found = true
		}
	}
	if !found {
		result = append(result, want)
	}
	return result
}
