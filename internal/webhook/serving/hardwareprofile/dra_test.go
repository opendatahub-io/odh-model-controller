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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

func TestParseDRAWithKueue(t *testing.T) {
	profile := &unstructured.Unstructured{Object: map[string]any{
		"spec": map[string]any{
			"dra": map[string]any{"resourceClaimTemplateName": "gpu-template"},
			"scheduling": map[string]any{
				"kueue": map[string]any{"localQueueName": "gpu-queue"},
			},
		},
	}}
	resolved, err := parseProfile(profile)
	require.NoError(t, err)
	assert.Equal(t, "gpu-template", resolved.ResourceClaimTemplateName)
	assert.Equal(t, "gpu-queue", resolved.KueueQueueName)

	profile.Object["spec"].(map[string]any)["dra"] = map[string]any{"resourceClaimTemplateName": 3}
	_, err = parseProfile(profile)
	require.ErrorContains(t, err, "DRA template name")
}

func TestProfileReferenceChangedFromRequest(t *testing.T) {
	old := metav1.ObjectMeta{
		Namespace: "workload-ns",
		Annotations: map[string]string{
			HardwareProfileAnnotationName: "shared-profile",
		},
	}
	oldRaw, err := json.Marshal(struct {
		Metadata metav1.ObjectMeta `json:"metadata"`
	}{Metadata: old})
	require.NoError(t, err)
	req := admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
		Operation: admissionv1.Update,
		Namespace: old.Namespace,
		OldObject: runtime.RawExtension{Raw: oldRaw},
	}}

	newMeta := old.DeepCopy()
	oldName, referenceChanged, err := ProfileReferenceChangedFromRequest(req, newMeta, true)
	require.NoError(t, err)
	assert.Equal(t, "shared-profile", oldName)
	assert.False(t, referenceChanged)

	newMeta.Annotations[HardwareProfileAnnotationNamespace] = "workload-ns"
	oldName, referenceChanged, err = ProfileReferenceChangedFromRequest(req, newMeta, true)
	require.NoError(t, err)
	assert.Equal(t, "shared-profile", oldName)
	assert.False(t, referenceChanged, "explicit default namespace is the same effective reference")
	assert.False(t, ProfileReferenceChanged(&old, newMeta, true), "the effective reference is unchanged")

	newMeta.Annotations[HardwareProfileAnnotationNamespace] = "shared-ns"
	oldName, referenceChanged, err = ProfileReferenceChangedFromRequest(req, newMeta, true)
	require.NoError(t, err)
	assert.Equal(t, "shared-profile", oldName)
	assert.True(t, referenceChanged)

	noProfileRaw, err := json.Marshal(struct {
		Metadata metav1.ObjectMeta `json:"metadata"`
	}{Metadata: metav1.ObjectMeta{Namespace: old.Namespace}})
	require.NoError(t, err)
	req.OldObject.Raw = noProfileRaw
	newMeta = &metav1.ObjectMeta{Namespace: old.Namespace, Annotations: map[string]string{
		HardwareProfileAnnotationName: "new-profile",
	}}
	oldName, referenceChanged, err = ProfileReferenceChangedFromRequest(req, newMeta, true)
	require.NoError(t, err)
	assert.Empty(t, oldName)
	assert.True(t, referenceChanged)
	_, referenceChanged, err = ProfileReferenceChangedFromRequest(req, newMeta, false)
	require.NoError(t, err)
	assert.False(t, referenceChanged)

	req.OldObject.Raw = []byte("{")
	_, _, err = ProfileReferenceChangedFromRequest(req, newMeta, true)
	require.ErrorContains(t, err, "failed reading previous HardwareProfile reference")
	assert.True(t, ProfileChanged(req, "new-profile", old.Namespace), "malformed old objects conservatively trigger scheduling replacement")
}

func TestProfileReferenceChangedPreservesSchedulingInitialAssignment(t *testing.T) {
	old := &metav1.ObjectMeta{Namespace: "workload-ns"}
	newMeta := &metav1.ObjectMeta{Namespace: "workload-ns", Annotations: map[string]string{
		HardwareProfileAnnotationName: "profile",
	}}
	assert.False(t, ProfileReferenceChanged(old, newMeta, false))
	assert.True(t, ProfileReferenceChanged(old, newMeta, true))

	withProfile := &metav1.ObjectMeta{Namespace: "workload-ns", Annotations: map[string]string{
		HardwareProfileAnnotationName: "profile",
	}}
	explicitDefault := withProfile.DeepCopy()
	explicitDefault.Annotations[HardwareProfileAnnotationNamespace] = "workload-ns"
	assert.False(t, ProfileReferenceChanged(withProfile, explicitDefault, true))
	assert.True(t, ProfileReferenceChanged(withProfile, &metav1.ObjectMeta{Namespace: "workload-ns"}, true))
}

func TestReconcileDRAClaimsLifecycle(t *testing.T) {
	manualTemplate := "manual-template"
	claims := []corev1.PodResourceClaim{{Name: "manual", ResourceClaimTemplateName: &manualTemplate}}
	resources := corev1.ResourceRequirements{Claims: []corev1.ResourceClaim{{Name: "manual"}}}

	require.NoError(t, ReconcileDRAClaims(&claims, &resources, false, "gpu-template", true))
	require.Len(t, claims, 2)
	assert.Equal(t, "gpu-template", *claims[1].ResourceClaimTemplateName)
	assert.Equal(t, DRAClaimName, claims[1].Name)
	assert.Equal(t, []corev1.ResourceClaim{{Name: "manual"}, {Name: DRAClaimName}}, resources.Claims)

	// Unchanged references preserve the submitted claim spec, including user edits.
	claims[1].ResourceClaimTemplateName = &manualTemplate
	resources.Claims = resources.Claims[:1]
	require.NoError(t, ReconcileDRAClaims(&claims, &resources, true, "edited-template", false))
	assert.Equal(t, []corev1.PodResourceClaim{{Name: "manual", ResourceClaimTemplateName: &manualTemplate}, {Name: DRAClaimName, ResourceClaimTemplateName: &manualTemplate}}, claims)
	assert.Equal(t, []corev1.ResourceClaim{{Name: "manual"}}, resources.Claims)

	// Switching profiles replaces only the owned pair.
	claims[1].ResourceClaimTemplateName = func() *string {
		v := "gpu-template"
		return &v
	}()
	resources.Claims = append(resources.Claims, corev1.ResourceClaim{Name: DRAClaimName})
	require.NoError(t, ReconcileDRAClaims(&claims, &resources, true, "other-template", true))
	assert.Equal(t, "other-template", *claims[1].ResourceClaimTemplateName)

	// Removal does not require fetching the old profile or template.
	require.NoError(t, ReconcileDRAClaims(&claims, &resources, true, "", true))
	assert.Equal(t, []corev1.PodResourceClaim{{Name: "manual", ResourceClaimTemplateName: &manualTemplate}}, claims)
	assert.Equal(t, []corev1.ResourceClaim{{Name: "manual"}}, resources.Claims)
}

func TestReconcileDRAClaimsRejectsUnownedName(t *testing.T) {
	manualTemplate := "other-template"
	claims := []corev1.PodResourceClaim{{Name: DRAClaimName, ResourceClaimTemplateName: &manualTemplate}}
	resources := corev1.ResourceRequirements{Claims: []corev1.ResourceClaim{{Name: DRAClaimName}}}

	err := ReconcileDRAClaims(&claims, &resources, false, "gpu-template", true)
	require.ErrorContains(t, err, "already used")
	assert.Equal(t, "other-template", *claims[0].ResourceClaimTemplateName)
}

func TestReconcileDRAClaimsAcceptsOwnClaimOnCreate(t *testing.T) {
	selected := "gpu-template"
	claims := []corev1.PodResourceClaim{{Name: DRAClaimName, ResourceClaimTemplateName: &selected}}
	resources := corev1.ResourceRequirements{Claims: []corev1.ResourceClaim{{Name: DRAClaimName}}}

	// Re-submitting an already admitted object as a create, e.g. oc get -o yaml | oc create.
	require.NoError(t, ReconcileDRAClaims(&claims, &resources, false, selected, true))
	assert.Equal(t, []corev1.PodResourceClaim{{Name: DRAClaimName, ResourceClaimTemplateName: &selected}}, claims)
	assert.Equal(t, []corev1.ResourceClaim{{Name: DRAClaimName}}, resources.Claims)

	// A claim carrying no template is still a user claim.
	untemplated := []corev1.PodResourceClaim{{Name: DRAClaimName}}
	err := ReconcileDRAClaims(&untemplated, &corev1.ResourceRequirements{}, false, selected, true)
	require.ErrorContains(t, err, "already used")
}

func TestReconcileDRAClaimsLeavesNoProfileManualClaimUnchanged(t *testing.T) {
	manualTemplate := "manual-template"
	claims := []corev1.PodResourceClaim{{Name: DRAClaimName, ResourceClaimTemplateName: &manualTemplate}}
	resources := corev1.ResourceRequirements{Claims: []corev1.ResourceClaim{{Name: DRAClaimName}}}
	beforeClaims := append([]corev1.PodResourceClaim(nil), claims...)
	beforeResources := resources.DeepCopy()

	require.NoError(t, ReconcileDRAClaims(&claims, &resources, false, "", false))
	assert.Equal(t, beforeClaims, claims)
	assert.Equal(t, beforeResources, &resources)
}

func TestDRAContainerClaimIsolation(t *testing.T) {
	claim := corev1.ResourceClaim{Name: DRAClaimName}
	manual := corev1.ResourceClaim{Name: "manual"}
	containers := []corev1.Container{
		{Name: "main", Resources: corev1.ResourceRequirements{Claims: []corev1.ResourceClaim{claim, manual}}},
		{Name: "sidecar", Resources: corev1.ResourceRequirements{Claims: []corev1.ResourceClaim{claim, manual}}},
	}
	initContainers := []corev1.Container{{Name: "init", Resources: corev1.ResourceRequirements{Claims: []corev1.ResourceClaim{claim, manual}}}}
	ephemeralContainers := []corev1.EphemeralContainer{{EphemeralContainerCommon: corev1.EphemeralContainerCommon{
		Name: "debug", Resources: corev1.ResourceRequirements{Claims: []corev1.ResourceClaim{claim, manual}},
	}}}

	require.ErrorContains(t, ValidateDRAContainerClaims(containers, initContainers, ephemeralContainers, 0), `non-target container "sidecar"`)
	containers[1].Resources.Claims = []corev1.ResourceClaim{manual}
	require.ErrorContains(t, ValidateDRAContainerClaims(containers, initContainers, ephemeralContainers, 0), `init container "init"`)
	initContainers[0].Resources.Claims = []corev1.ResourceClaim{manual}
	require.ErrorContains(t, ValidateDRAContainerClaims(containers, initContainers, ephemeralContainers, 0), `ephemeral container "debug"`)
	ephemeralContainers[0].Resources.Claims = []corev1.ResourceClaim{manual}
	require.NoError(t, ValidateDRAContainerClaims(containers, initContainers, ephemeralContainers, 0))

	containers[1].Resources.Claims = []corev1.ResourceClaim{claim, manual}
	initContainers[0].Resources.Claims = []corev1.ResourceClaim{claim, manual}
	ephemeralContainers[0].Resources.Claims = []corev1.ResourceClaim{claim, manual}
	RemoveDRAContainerClaims(containers, initContainers, ephemeralContainers)
	for _, container := range containers {
		assert.Equal(t, []corev1.ResourceClaim{manual}, container.Resources.Claims)
	}
	assert.Equal(t, []corev1.ResourceClaim{manual}, initContainers[0].Resources.Claims)
	assert.Equal(t, []corev1.ResourceClaim{manual}, ephemeralContainers[0].Resources.Claims)
}

func TestValidateResourceClaimTemplateUsesWorkloadNamespace(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, resourcev1.AddToScheme(scheme))
	reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&resourcev1.ResourceClaimTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "gpu-template", Namespace: "workload-ns"},
	}).Build()
	require.NoError(t, ValidateResourceClaimTemplate(context.Background(), reader, "gpu-template", "workload-ns"))
	err := ValidateResourceClaimTemplate(context.Background(), reader, "gpu-template", "profile-ns")
	require.ErrorContains(t, err, `ResourceClaimTemplate "gpu-template" not found in workload namespace "profile-ns"`)

	forbidden := k8serr.NewForbidden(schema.GroupResource{Group: "resource.k8s.io", Resource: "resourceclaimtemplates"},
		"gpu-template", assert.AnError)
	err = ValidateResourceClaimTemplate(context.Background(), errClient{err: forbidden}, "gpu-template", "workload-ns")
	require.ErrorContains(t, err, `failed reading ResourceClaimTemplate "gpu-template" in workload namespace "workload-ns"`)
	require.ErrorIs(t, err, forbidden)
}
