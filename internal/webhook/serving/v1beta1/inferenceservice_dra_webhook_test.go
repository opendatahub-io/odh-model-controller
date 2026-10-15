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

package v1beta1

import (
	"context"

	servingv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/opendatahub-io/odh-model-controller/internal/webhook/serving/hardwareprofile"
	hwptestutil "github.com/opendatahub-io/odh-model-controller/internal/webhook/serving/hardwareprofile/testutil"
)

func isvcDRAResourceClaimTemplate(namespace, template string) *resourcev1.ResourceClaimTemplate {
	return &resourcev1.ResourceClaimTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: template, Namespace: namespace},
	}
}

var _ = Describe("InferenceService HardwareProfile DRA", func() {
	It("injects a complete claim pair alongside Kueue and resource identifiers", func() {
		spec := hwptestutil.ResourceSpec([]string{"cpu", "4"})
		spec["dra"] = map[string]any{"resourceClaimTemplateName": "gpu-template"}
		spec["scheduling"] = map[string]any{"kueue": map[string]any{"localQueueName": "gpu-queue"}}
		hwp := hwptestutil.NewHardwareProfile("gpu-profile", "shared-ns", spec)
		d := newISVCDefaulter(newDefaulterFakeClient(hwp))
		isvc := buildISVCForHWP(hwptestutil.HWPAnnotationsWithNS("gpu-profile", "shared-ns"), nil)
		isvc.Spec.Canary = []servingv1beta1.CanarySpec{{
			Predictor: servingv1beta1.PredictorSpec{Model: &servingv1beta1.ModelSpec{}},
		}}

		Expect(d.Default(isvcCreateCtx(isvc), isvc)).To(Succeed())
		Expect(isvc.Spec.Predictor.ResourceClaims).To(HaveLen(1))
		Expect(isvc.Spec.Predictor.ResourceClaims[0].Name).To(Equal(hardwareprofile.DRAClaimName))
		Expect(*isvc.Spec.Predictor.ResourceClaims[0].ResourceClaimTemplateName).To(Equal("gpu-template"))
		Expect(isvc.Spec.Predictor.Model.Resources.Claims).To(Equal([]corev1.ResourceClaim{{Name: hardwareprofile.DRAClaimName}}))
		Expect(isvc.Spec.Predictor.Model.Resources.Requests).To(HaveKey(corev1.ResourceCPU))
		Expect(isvc.Labels[hardwareprofile.KueueQueueNameLabel]).To(Equal("gpu-queue"))
		Expect(isvc.Annotations[hardwareprofile.DRATemplateAnnotation]).To(Equal("gpu-template"))
		Expect(isvc.Spec.Canary[0].Predictor.ResourceClaims).To(BeEmpty())
		Expect(isvc.Spec.Canary[0].Predictor.Model.Resources.Claims).To(BeEmpty())
	})

	It("keeps the admitted template on an unchanged reference and cleans it on a profile switch", func() {
		original := hwptestutil.NewHardwareProfile("gpu-profile", hwpTestNS,
			map[string]any{"dra": map[string]any{"resourceClaimTemplateName": "gpu-template"}})
		d := newISVCDefaulter(newDefaulterFakeClient(original))
		old := buildISVCForHWP(hwptestutil.HWPAnnotationsWithNS("gpu-profile", hwpTestNS), nil)
		Expect(d.Default(isvcCreateCtx(old), old)).To(Succeed())

		edited := hwptestutil.NewHardwareProfile("gpu-profile", hwpTestNS,
			map[string]any{"dra": map[string]any{"resourceClaimTemplateName": "edited-template"}})
		unchanged := old.DeepCopy()
		unchanged.Spec.Predictor.ResourceClaims = nil
		unchanged.Spec.Predictor.Model.Resources.Claims = nil
		Expect(newISVCDefaulter(newDefaulterFakeClient(edited)).Default(isvcUpdateCtx(unchanged, old), unchanged)).To(Succeed())
		Expect(*unchanged.Spec.Predictor.ResourceClaims[0].ResourceClaimTemplateName).To(Equal("gpu-template"))
		Expect(unchanged.Spec.Predictor.Model.Resources.Claims).To(HaveLen(1))

		withoutDRA := hwptestutil.NewHardwareProfile("plain-profile", hwpTestNS, nil)
		switched := unchanged.DeepCopy()
		switched.Annotations[hardwareprofile.HardwareProfileAnnotationName] = "plain-profile"
		Expect(newISVCDefaulter(newDefaulterFakeClient(withoutDRA)).Default(isvcUpdateCtx(switched, unchanged), switched)).To(Succeed())
		Expect(switched.Spec.Predictor.ResourceClaims).To(BeEmpty())
		Expect(switched.Spec.Predictor.Model.Resources.Claims).To(BeEmpty())
		Expect(switched.Annotations).NotTo(HaveKey(hardwareprofile.DRATemplateAnnotation))
	})

	It("cleans its claim when the profile annotation is removed after the old profile was deleted", func() {
		old := buildISVCForHWP(hwptestutil.HWPAnnotationsWithNS("deleted-profile", hwpTestNS), nil)
		old.Annotations[hardwareprofile.DRATemplateAnnotation] = "gpu-template"
		old.Spec.Predictor.ResourceClaims = []corev1.PodResourceClaim{{Name: hardwareprofile.DRAClaimName}}
		old.Spec.Predictor.Model.Resources.Claims = []corev1.ResourceClaim{{Name: hardwareprofile.DRAClaimName}}
		old.Spec.Predictor.Containers = []corev1.Container{{Name: "sidecar", Resources: corev1.ResourceRequirements{
			Claims: []corev1.ResourceClaim{{Name: hardwareprofile.DRAClaimName}},
		}}}
		old.Spec.Predictor.InitContainers = []corev1.Container{{Name: "init", Resources: corev1.ResourceRequirements{
			Claims: []corev1.ResourceClaim{{Name: hardwareprofile.DRAClaimName}},
		}}}
		old.Spec.Predictor.EphemeralContainers = []corev1.EphemeralContainer{{EphemeralContainerCommon: corev1.EphemeralContainerCommon{
			Name: "debug", Resources: corev1.ResourceRequirements{Claims: []corev1.ResourceClaim{{Name: hardwareprofile.DRAClaimName}}},
		}}}
		updated := old.DeepCopy()
		delete(updated.Annotations, hardwareprofile.HardwareProfileAnnotationName)

		Expect(newISVCDefaulter(newDefaulterFakeClient()).Default(isvcUpdateCtx(updated, old), updated)).To(Succeed())
		Expect(updated.Spec.Predictor.ResourceClaims).To(BeEmpty())
		Expect(updated.Spec.Predictor.Model.Resources.Claims).To(BeEmpty())
		Expect(updated.Spec.Predictor.Containers[0].Resources.Claims).To(BeEmpty())
		Expect(updated.Spec.Predictor.InitContainers[0].Resources.Claims).To(BeEmpty())
		Expect(updated.Spec.Predictor.EphemeralContainers[0].Resources.Claims).To(BeEmpty())
		Expect(updated.Annotations).NotTo(HaveKey(hardwareprofile.DRATemplateAnnotation))
	})

	It("rejects DRA on a non-Model predictor and rejects an unowned claim name", func() {
		hwp := hwptestutil.NewHardwareProfile("gpu-profile", hwpTestNS,
			map[string]any{"dra": map[string]any{"resourceClaimTemplateName": "gpu-template"}})
		d := newISVCDefaulter(newDefaulterFakeClient(hwp))
		withoutModel := buildISVCNoModelForHWP(hwptestutil.HWPAnnotations("gpu-profile"))
		Expect(d.Default(isvcCreateCtx(withoutModel), withoutModel)).To(MatchError(ContainSubstring("spec.predictor.model")))

		collision := buildISVCForHWP(hwptestutil.HWPAnnotations("gpu-profile"), nil)
		collision.Spec.Predictor.ResourceClaims = []corev1.PodResourceClaim{{Name: hardwareprofile.DRAClaimName}}
		Expect(d.Default(isvcCreateCtx(collision), collision)).To(MatchError(ContainSubstring("already used")))

		sidecar := buildISVCForHWP(hwptestutil.HWPAnnotations("gpu-profile"), nil)
		sidecar.Spec.Predictor.Containers = []corev1.Container{{Name: "sidecar", Resources: corev1.ResourceRequirements{
			Claims: []corev1.ResourceClaim{{Name: hardwareprofile.DRAClaimName}},
		}}}
		Expect(d.Default(isvcCreateCtx(sidecar), sidecar)).To(MatchError(ContainSubstring(`non-target container "sidecar"`)))

		init := buildISVCForHWP(hwptestutil.HWPAnnotations("gpu-profile"), nil)
		init.Spec.Predictor.InitContainers = []corev1.Container{{Name: "init", Resources: corev1.ResourceRequirements{
			Claims: []corev1.ResourceClaim{{Name: hardwareprofile.DRAClaimName}},
		}}}
		Expect(d.Default(isvcCreateCtx(init), init)).To(MatchError(ContainSubstring(`init container "init"`)))
	})

	It("validates the template in the workload namespace only when the HWP annotation changes", func() {
		hwp := hwptestutil.NewHardwareProfile("gpu-profile", "shared-ns",
			map[string]any{"dra": map[string]any{"resourceClaimTemplateName": "gpu-template"}})
		old := buildISVCForHWP(nil, nil)
		newISVC := buildISVCForHWP(hwptestutil.HWPAnnotationsWithNS("gpu-profile", "shared-ns"), nil)
		wrongNamespace := newDefaulterFakeClient(hwp, isvcDRAResourceClaimTemplate("shared-ns", "gpu-template"))
		validator := &InferenceServiceCustomValidator{client: wrongNamespace, apiReader: wrongNamespace}
		_, err := validator.ValidateUpdate(context.Background(), old, newISVC)
		Expect(err).To(MatchError(ContainSubstring(`ResourceClaimTemplate "gpu-template" not found in workload namespace "test-hwp-ns"`)))

		inWorkloadNamespace := newDefaulterFakeClient(hwp, isvcDRAResourceClaimTemplate(hwpTestNS, "gpu-template"))
		validator = &InferenceServiceCustomValidator{client: inWorkloadNamespace, apiReader: inWorkloadNamespace}
		_, err = validator.ValidateUpdate(context.Background(), old, newISVC)
		Expect(err).NotTo(HaveOccurred())

		// A later template deletion does not block an unrelated workload update.
		newISVC.Annotations[hardwareprofile.DRATemplateAnnotation] = "gpu-template"
		validator = &InferenceServiceCustomValidator{client: wrongNamespace, apiReader: wrongNamespace}
		_, err = validator.ValidateUpdate(context.Background(), newISVC.DeepCopy(), newISVC)
		Expect(err).NotTo(HaveOccurred())
	})
})
