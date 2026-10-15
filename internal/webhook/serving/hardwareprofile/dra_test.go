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

func TestDRAReferenceChangeAndOldState(t *testing.T) {
	old := metav1.ObjectMeta{
		Namespace: "workload-ns",
		Annotations: map[string]string{
			HardwareProfileAnnotationName: "shared-profile",
			DRATemplateAnnotation:         "original-template",
		},
	}
	oldRaw, err := json.Marshal(struct {
		Metadata metav1.ObjectMeta `json:"metadata"`
	}{Metadata: old})
	require.NoError(t, err)
	req := admission.Request{}
	req.OldObject.Raw = oldRaw

	newMeta := old.DeepCopy()
	gotTemplate, changed, err := DRAStateFromRequest(req, newMeta)
	require.NoError(t, err)
	assert.Equal(t, "original-template", gotTemplate)
	assert.False(t, changed)
	assert.Equal(t, "original-template", DesiredDRATemplate("edited-template", gotTemplate, changed))

	newMeta.Annotations[HardwareProfileAnnotationNamespace] = "workload-ns"
	_, changed, err = DRAStateFromRequest(req, newMeta)
	require.NoError(t, err)
	assert.False(t, changed, "explicit default namespace is the same effective reference")
	assert.True(t, DRAReferenceAnnotationsChanged(&old, newMeta), "the raw annotation was added")

	newMeta.Annotations[HardwareProfileAnnotationNamespace] = "shared-ns"
	_, changed, err = DRAStateFromRequest(req, newMeta)
	require.NoError(t, err)
	assert.True(t, changed)
}

func TestReconcileDRAClaimsLifecycle(t *testing.T) {
	manualTemplate := "manual-template"
	meta := metav1.ObjectMeta{}
	claims := []corev1.PodResourceClaim{{Name: "manual", ResourceClaimTemplateName: &manualTemplate}}
	resources := corev1.ResourceRequirements{Claims: []corev1.ResourceClaim{{Name: "manual"}}}

	require.NoError(t, ReconcileDRAClaims(&meta, &claims, &resources, "", "gpu-template", true))
	require.Len(t, claims, 2)
	assert.Equal(t, "gpu-template", *claims[1].ResourceClaimTemplateName)
	assert.Equal(t, DRAClaimName, claims[1].Name)
	assert.Equal(t, []corev1.ResourceClaim{{Name: "manual"}, {Name: DRAClaimName}}, resources.Claims)
	assert.Equal(t, "gpu-template", meta.Annotations[DRATemplateAnnotation])

	// Unchanged references retain the admitted template and repair attempted edits.
	claims[1].ResourceClaimTemplateName = &manualTemplate
	resources.Claims = resources.Claims[:1]
	require.NoError(t, ReconcileDRAClaims(&meta, &claims, &resources, "gpu-template", "edited-template", false))
	assert.Equal(t, "gpu-template", *claims[1].ResourceClaimTemplateName)
	assert.Equal(t, []corev1.ResourceClaim{{Name: "manual"}, {Name: DRAClaimName}}, resources.Claims)
	require.NoError(t, ReconcileDRAClaims(&meta, &claims, &resources, "gpu-template", "edited-template", false))
	assert.Len(t, claims, 2)
	assert.Len(t, resources.Claims, 2)

	// Switching profiles replaces only the owned pair.
	require.NoError(t, ReconcileDRAClaims(&meta, &claims, &resources, "gpu-template", "other-template", true))
	assert.Equal(t, "other-template", *claims[1].ResourceClaimTemplateName)
	assert.Equal(t, "other-template", meta.Annotations[DRATemplateAnnotation])

	// Removal does not require fetching the old profile or template.
	require.NoError(t, ReconcileDRAClaims(&meta, &claims, &resources, "other-template", "", true))
	assert.Equal(t, []corev1.PodResourceClaim{{Name: "manual", ResourceClaimTemplateName: &manualTemplate}}, claims)
	assert.Equal(t, []corev1.ResourceClaim{{Name: "manual"}}, resources.Claims)
	assert.NotContains(t, meta.Annotations, DRATemplateAnnotation)
}

func TestReconcileDRAClaimsRejectsUnownedName(t *testing.T) {
	manualTemplate := "other-template"
	meta := metav1.ObjectMeta{}
	claims := []corev1.PodResourceClaim{{Name: DRAClaimName, ResourceClaimTemplateName: &manualTemplate}}
	resources := corev1.ResourceRequirements{Claims: []corev1.ResourceClaim{{Name: DRAClaimName}}}

	err := ReconcileDRAClaims(&meta, &claims, &resources, "", "gpu-template", true)
	require.ErrorContains(t, err, "already used")
	assert.Equal(t, "other-template", *claims[0].ResourceClaimTemplateName)
	assert.Empty(t, meta.Annotations)

	// The incoming annotation is user writable and cannot establish ownership.
	spoofed := metav1.ObjectMeta{Annotations: map[string]string{DRATemplateAnnotation: "gpu-template"}}
	spoofedTemplate := "gpu-template"
	spoofedClaims := []corev1.PodResourceClaim{{Name: DRAClaimName, ResourceClaimTemplateName: &spoofedTemplate}}
	spoofedResources := corev1.ResourceRequirements{Claims: []corev1.ResourceClaim{{Name: DRAClaimName}}}
	err = ReconcileDRAClaims(&spoofed, &spoofedClaims, &spoofedResources, "", "gpu-template", true)
	require.ErrorContains(t, err, "already used")
	assert.Equal(t, "gpu-template", *spoofedClaims[0].ResourceClaimTemplateName)
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
