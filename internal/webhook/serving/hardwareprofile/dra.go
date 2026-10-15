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
	"encoding/json"
	"fmt"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

const (
	// DRAClaimName is the pod-local name reserved for HardwareProfile DRA claims.
	DRAClaimName = "hwp-dra"
	// DRATemplateAnnotation records the template used at admission, so profile edits and
	// deletion cannot make us lose track of the claim we own on an existing workload.
	DRATemplateAnnotation = "opendatahub.io/hardware-profile-dra-template-name"
)

// DRAStateFromRequest returns the old admitted template and whether the effective
// HardwareProfile reference changed. This is intentionally separate from
// ProfileChanged, whose initial-assignment behavior controls scheduling merges.
func DRAStateFromRequest(req admission.Request, newMeta metav1.Object) (string, bool, error) {
	if req.Operation == admissionv1.Create {
		return "", true, nil
	}
	if req.OldObject.Raw == nil {
		return "", true, nil
	}

	var old struct {
		Metadata metav1.ObjectMeta `json:"metadata"`
	}
	if err := json.Unmarshal(req.OldObject.Raw, &old); err != nil {
		return "", false, fmt.Errorf("failed reading previous HardwareProfile DRA state: %w", err)
	}
	oldName, oldNamespace := ProfileRef(old.Metadata.Annotations, old.Metadata.Namespace)
	newName, newNamespace := ProfileRef(newMeta.GetAnnotations(), newMeta.GetNamespace())
	oldTemplate := old.Metadata.Annotations[DRATemplateAnnotation]
	return oldTemplate, oldName != newName || oldNamespace != newNamespace || (newName == "" && oldTemplate != ""), nil
}

// DesiredDRATemplate keeps the template captured on the previous admitted object
// when the profile reference is unchanged. A profile edit affects new bindings only.
func DesiredDRATemplate(selected, old string, referenceChanged bool) string {
	if referenceChanged {
		return selected
	}
	return old
}

// DRAReferenceAnnotationsChanged reports whether an added or changed HWP name or
// namespace annotation requires an admission-time template existence check.
func DRAReferenceAnnotationsChanged(oldMeta, newMeta *metav1.ObjectMeta) bool {
	newAnnotations := newMeta.Annotations
	if newAnnotations[HardwareProfileAnnotationName] == "" {
		return false
	}
	if oldMeta == nil {
		return true
	}
	oldAnnotations := oldMeta.Annotations
	for _, key := range []string{HardwareProfileAnnotationName, HardwareProfileAnnotationNamespace} {
		oldValue, oldPresent := oldAnnotations[key]
		newValue, newPresent := newAnnotations[key]
		if oldValue != newValue || oldPresent != newPresent {
			return true
		}
	}
	return false
}

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

// ReconcileDRAClaims changes only the HWP-owned claim name. oldTemplate must come
// from the previous admitted object, never from the incoming object's annotation.
func ReconcileDRAClaims(
	meta *metav1.ObjectMeta,
	podClaims *[]corev1.PodResourceClaim,
	resources *corev1.ResourceRequirements,
	oldTemplate, selectedTemplate string,
	referenceChanged bool,
) error {
	desired := DesiredDRATemplate(selectedTemplate, oldTemplate, referenceChanged)

	if referenceChanged && oldTemplate != "" {
		*podClaims = removePodClaim(*podClaims)
		if resources != nil {
			resources.Claims = removeContainerClaim(resources.Claims)
		}
	}

	if desired == "" {
		if meta.Annotations != nil {
			delete(meta.Annotations, DRATemplateAnnotation)
		}
		return nil
	}
	if resources == nil {
		return fmt.Errorf("HardwareProfile DRA requires a target container")
	}

	if referenceChanged && oldTemplate == "" && (hasPodClaim(*podClaims) || hasContainerClaim(resources.Claims)) {
		return fmt.Errorf("claim name %q is already used by a workload claim", DRAClaimName)
	}

	*podClaims = setPodClaim(*podClaims, desired)
	resources.Claims = setContainerClaim(resources.Claims)
	if meta.Annotations == nil {
		meta.Annotations = make(map[string]string)
	}
	meta.Annotations[DRATemplateAnnotation] = desired
	return nil
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
