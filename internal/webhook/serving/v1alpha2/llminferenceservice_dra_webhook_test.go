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

package v1alpha2

import (
	"context"

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kservev1alpha2 "github.com/kserve/kserve/pkg/apis/serving/v1alpha2"
	kserveconstants "github.com/kserve/kserve/pkg/constants"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/opendatahub-io/odh-model-controller/internal/webhook/serving/hardwareprofile"
	hwptestutil "github.com/opendatahub-io/odh-model-controller/internal/webhook/serving/hardwareprofile/testutil"
)

func llmDRAResourceClaimTemplate(template string) *resourcev1.ResourceClaimTemplate {
	return &resourcev1.ResourceClaimTemplate{ObjectMeta: metav1.ObjectMeta{Name: template, Namespace: llmHWPNS}}
}

var _ = Describe("LLMInferenceService HardwareProfile DRA", func() {
	It("injects only the primary template while retaining Kueue, identifiers, worker and prefill", func() {
		spec := hwptestutil.ResourceSpec([]string{"cpu", "4"})
		spec["dra"] = map[string]any{"resourceClaimTemplateName": "gpu-template"}
		spec["scheduling"] = map[string]any{"kueue": map[string]any{"localQueueName": "gpu-queue"}}
		hwp := hwptestutil.NewHardwareProfile("gpu-profile", "shared-ns", spec)
		llm := buildLLMISVCHWP(hwptestutil.HWPAnnotationsWithNS("gpu-profile", "shared-ns"), nil)
		llm.Spec.Worker = &corev1.PodSpec{Containers: []corev1.Container{{Name: "worker"}}}
		llm.Spec.Prefill = &kservev1alpha2.WorkloadSpec{Template: &corev1.PodSpec{Containers: []corev1.Container{{Name: "prefill"}}}}
		workerBefore := llm.Spec.Worker.DeepCopy()
		prefillBefore := llm.Spec.Prefill.DeepCopy()

		Expect(newLLMDefaulter(newLLMFakeClient(hwp)).Default(llmHWPCreateCtx(llm), llm)).To(Succeed())
		Expect(llm.Spec.Template).NotTo(BeNil())
		Expect(llm.Spec.Template.Containers).To(HaveLen(1))
		Expect(llm.Spec.Template.Containers[0].Name).To(Equal("main"))
		Expect(llm.Spec.Template.ResourceClaims).To(HaveLen(1))
		Expect(*llm.Spec.Template.ResourceClaims[0].ResourceClaimTemplateName).To(Equal("gpu-template"))
		Expect(llm.Spec.Template.Containers[0].Resources.Claims).To(Equal([]corev1.ResourceClaim{{Name: hardwareprofile.DRAClaimName}}))
		Expect(llm.Spec.Template.Containers[0].Resources.Requests).To(HaveKey(corev1.ResourceCPU))
		Expect(llm.Labels[hardwareprofile.KueueQueueNameLabel]).To(Equal("gpu-queue"))
		Expect(llm.Spec.Worker).To(Equal(workerBefore))
		Expect(llm.Spec.Prefill).To(Equal(prefillBefore))
	})

	It("handles v1alpha1 and retains the admitted template on an unchanged reference", func() {
		hwp := hwptestutil.NewHardwareProfile("gpu-profile", llmHWPNS,
			map[string]any{"dra": map[string]any{"resourceClaimTemplateName": "gpu-template"}})
		llm := &kservev1alpha1.LLMInferenceService{
			ObjectMeta: metav1.ObjectMeta{Name: "test-llm", Namespace: llmHWPNS,
				Annotations: hwptestutil.HWPAnnotations("gpu-profile")},
		}
		Expect(newLLMDefaulterV1alpha1(newLLMFakeClient(hwp)).Default(llmAdmissionCtx(admissionv1.Create, nil, false), llm)).To(Succeed())
		Expect(llm.Spec.Template.ResourceClaims).To(HaveLen(1))
		Expect(llm.Spec.Template.Containers[0].Resources.Claims).To(HaveLen(1))

		old := llm.DeepCopy()
		llm.Spec.Template.ResourceClaims = nil
		llm.Spec.Template.Containers[0].Resources.Claims = nil
		edited := hwptestutil.NewHardwareProfile("gpu-profile", llmHWPNS,
			map[string]any{"dra": map[string]any{"resourceClaimTemplateName": "edited-template"}})
		Expect(newLLMDefaulterV1alpha1(newLLMFakeClient(edited)).Default(llmAdmissionCtx(admissionv1.Update, old, false), llm)).To(Succeed())
		Expect(*llm.Spec.Template.ResourceClaims[0].ResourceClaimTemplateName).To(Equal("gpu-template"))
		Expect(llm.Spec.Template.Containers[0].Resources.Claims).To(HaveLen(1))
	})

	It("removes the owned pair after the old profile was deleted", func() {
		old := buildLLMISVCHWP(hwptestutil.HWPAnnotationsWithNS("deleted-profile", llmHWPNS), nil)
		old.Annotations[hardwareprofile.DRATemplateAnnotation] = "gpu-template"
		old.Spec.Template = &corev1.PodSpec{
			ResourceClaims: []corev1.PodResourceClaim{{Name: hardwareprofile.DRAClaimName}},
			Containers: []corev1.Container{
				{Name: "main", Resources: corev1.ResourceRequirements{Claims: []corev1.ResourceClaim{{Name: hardwareprofile.DRAClaimName}}}},
				{Name: "sidecar", Resources: corev1.ResourceRequirements{Claims: []corev1.ResourceClaim{{Name: hardwareprofile.DRAClaimName}}}},
			},
			InitContainers: []corev1.Container{{Name: "init", Resources: corev1.ResourceRequirements{
				Claims: []corev1.ResourceClaim{{Name: hardwareprofile.DRAClaimName}},
			}}},
			EphemeralContainers: []corev1.EphemeralContainer{{EphemeralContainerCommon: corev1.EphemeralContainerCommon{
				Name: "debug", Resources: corev1.ResourceRequirements{Claims: []corev1.ResourceClaim{{Name: hardwareprofile.DRAClaimName}}},
			}}},
		}
		updated := old.DeepCopy()
		delete(updated.Annotations, hardwareprofile.HardwareProfileAnnotationName)
		updated.Spec.Template.Containers[0].Name = "renamed-container"
		Expect(newLLMDefaulter(newLLMFakeClient()).Default(llmHWPUpdateCtx(updated, old), updated)).To(Succeed())
		Expect(updated.Spec.Template.ResourceClaims).To(BeEmpty())
		Expect(updated.Spec.Template.Containers[0].Resources.Claims).To(BeEmpty())
		Expect(updated.Spec.Template.Containers[1].Resources.Claims).To(BeEmpty())
		Expect(updated.Spec.Template.InitContainers[0].Resources.Claims).To(BeEmpty())
		Expect(updated.Spec.Template.EphemeralContainers[0].Resources.Claims).To(BeEmpty())
		Expect(updated.Annotations).NotTo(HaveKey(hardwareprofile.DRATemplateAnnotation))
	})

	It("rejects a missing main container and Managed DRA even with an empty annotation value", func() {
		hwp := hwptestutil.NewHardwareProfile("gpu-profile", llmHWPNS,
			map[string]any{"dra": map[string]any{"resourceClaimTemplateName": "gpu-template"}})
		d := newLLMDefaulter(newLLMFakeClient(hwp))
		withoutMain := buildLLMISVCHWP(hwptestutil.HWPAnnotations("gpu-profile"), nil)
		withoutMain.Spec.Template = &corev1.PodSpec{Containers: []corev1.Container{{Name: "sidecar"}}}
		Expect(d.Default(llmHWPCreateCtx(withoutMain), withoutMain)).To(MatchError(ContainSubstring(`requires a "main" container`)))

		managed := buildLLMISVCHWP(hwptestutil.HWPAnnotations("gpu-profile"), nil)
		managed.Annotations[kserveconstants.ManagedDRADeviceClassAnnotationKey] = ""
		Expect(d.Default(llmHWPCreateCtx(managed), managed)).To(MatchError(ContainSubstring("cannot be combined")))

		sidecar := buildLLMISVCHWP(hwptestutil.HWPAnnotations("gpu-profile"), nil)
		sidecar.Spec.Template = &corev1.PodSpec{Containers: []corev1.Container{
			{Name: "main"},
			{Name: "sidecar", Resources: corev1.ResourceRequirements{Claims: []corev1.ResourceClaim{{Name: hardwareprofile.DRAClaimName}}}},
		}}
		Expect(d.Default(llmHWPCreateCtx(sidecar), sidecar)).To(MatchError(ContainSubstring(`non-target container "sidecar"`)))

		init := buildLLMISVCHWP(hwptestutil.HWPAnnotations("gpu-profile"), nil)
		init.Spec.Template = &corev1.PodSpec{Containers: []corev1.Container{{Name: "main"}},
			InitContainers: []corev1.Container{{Name: "init", Resources: corev1.ResourceRequirements{
				Claims: []corev1.ResourceClaim{{Name: hardwareprofile.DRAClaimName}},
			}}}}
		Expect(d.Default(llmHWPCreateCtx(init), init)).To(MatchError(ContainSubstring(`init container "init"`)))
	})

	It("validates the workload RCT for both versions and skips rechecking an unchanged reference", func() {
		hwp := hwptestutil.NewHardwareProfile("gpu-profile", "shared-ns",
			map[string]any{"dra": map[string]any{"resourceClaimTemplateName": "gpu-template"}})
		llm2 := buildLLMISVCHWP(hwptestutil.HWPAnnotationsWithNS("gpu-profile", "shared-ns"), nil)
		llm1 := &kservev1alpha1.LLMInferenceService{
			ObjectMeta: metav1.ObjectMeta{Name: "test-llm", Namespace: llmHWPNS,
				Annotations: hwptestutil.HWPAnnotationsWithNS("gpu-profile", "shared-ns")},
		}
		missing := newLLMFakeClient(hwp)
		validator := &LLMInferenceServiceCustomValidator{apiReader: missing}
		_, err := (&llmISVCValidatorV1alpha2{validator}).ValidateCreate(context.Background(), llm2)
		Expect(err).To(MatchError(ContainSubstring(`ResourceClaimTemplate "gpu-template" not found in workload namespace`)))
		_, err = (&llmISVCValidatorV1alpha1{validator}).ValidateCreate(context.Background(), llm1)
		Expect(err).To(HaveOccurred())

		withRCT := newLLMFakeClient(hwp, llmDRAResourceClaimTemplate("gpu-template"))
		validator = &LLMInferenceServiceCustomValidator{apiReader: withRCT}
		_, err = (&llmISVCValidatorV1alpha2{validator}).ValidateCreate(context.Background(), llm2)
		Expect(err).NotTo(HaveOccurred())
		_, err = (&llmISVCValidatorV1alpha1{validator}).ValidateCreate(context.Background(), llm1)
		Expect(err).NotTo(HaveOccurred())

		llm2.Annotations[hardwareprofile.DRATemplateAnnotation] = "gpu-template"
		_, err = (&llmISVCValidatorV1alpha2{&LLMInferenceServiceCustomValidator{apiReader: missing}}).
			ValidateUpdate(context.Background(), llm2.DeepCopy(), llm2)
		Expect(err).NotTo(HaveOccurred())
		llm2.Annotations[kserveconstants.ManagedDRADeviceClassAnnotationKey] = ""
		_, err = (&llmISVCValidatorV1alpha2{validator}).ValidateUpdate(context.Background(), llm2.DeepCopy(), llm2)
		Expect(err).To(MatchError(ContainSubstring("cannot be combined")))
	})
})
