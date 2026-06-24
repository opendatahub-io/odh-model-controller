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
	"encoding/json"

	servingv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/opendatahub-io/odh-model-controller/internal/webhook/serving/hardwareprofile"
	hwptestutil "github.com/opendatahub-io/odh-model-controller/internal/webhook/serving/hardwareprofile/testutil"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

const (
	hwpTestNS   = "test-hwp-ns"
	hwpTestName = "test-hwp"
	hwpISVCName = "test-isvc"
)

// buildISVCForHWP creates a minimal InferenceService with a non-nil predictor Model.
// TypeMeta is always set so that json.Marshal produces the kind/apiVersion fields required by
// unstructured.UnmarshalJSON (used in handleHWPRemovalISVC to parse the old object).
func buildISVCForHWP(annotations, labels map[string]string) *servingv1beta1.InferenceService {
	return &servingv1beta1.InferenceService{
		TypeMeta: metav1.TypeMeta{
			Kind:       "InferenceService",
			APIVersion: "serving.kserve.io/v1beta1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        hwpISVCName,
			Namespace:   hwpTestNS,
			Annotations: annotations,
			Labels:      labels,
		},
		Spec: servingv1beta1.InferenceServiceSpec{
			Predictor: servingv1beta1.PredictorSpec{
				Model: &servingv1beta1.ModelSpec{},
			},
		},
	}
}

// buildISVCNoModelForHWP creates an InferenceService without a predictor Model field.
// TypeMeta is always set so that json.Marshal produces the kind/apiVersion fields.
func buildISVCNoModelForHWP(annotations map[string]string) *servingv1beta1.InferenceService {
	return &servingv1beta1.InferenceService{
		TypeMeta: metav1.TypeMeta{
			Kind:       "InferenceService",
			APIVersion: "serving.kserve.io/v1beta1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        hwpISVCName,
			Namespace:   hwpTestNS,
			Annotations: annotations,
		},
	}
}

// isvcCreateCtx returns a context carrying a CREATE admission request for isvc.
func isvcCreateCtx(isvc *servingv1beta1.InferenceService) context.Context {
	raw, _ := json.Marshal(isvc)
	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Create,
			Namespace: hwpTestNS,
			Object:    runtime.RawExtension{Raw: raw},
		},
	}
	return admission.NewContextWithRequest(context.Background(), req)
}

// isvcUpdateCtx returns a context carrying an UPDATE admission request.
func isvcUpdateCtx(newISVC, oldISVC *servingv1beta1.InferenceService) context.Context {
	newRaw, _ := json.Marshal(newISVC)
	oldRaw, _ := json.Marshal(oldISVC)
	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Update,
			Namespace: hwpTestNS,
			Object:    runtime.RawExtension{Raw: newRaw},
			OldObject: runtime.RawExtension{Raw: oldRaw},
		},
	}
	return admission.NewContextWithRequest(context.Background(), req)
}

// ─── tests ────────────────────────────────────────────────────────────────────

var _ = Describe("InferenceService HardwareProfile Webhook", func() {

	Describe("Test Group 1 — Basic injection (CREATE)", func() {

		It("isvc-1: no annotation — isvc unmodified (nil annotation map)", func() {
			d := newISVCDefaulter(newDefaulterFakeClient())
			isvc := &servingv1beta1.InferenceService{
				ObjectMeta: metav1.ObjectMeta{
					Name:        hwpISVCName,
					Namespace:   hwpTestNS,
					Annotations: nil, // explicitly nil to exercise nil-map path in ProfileRef
				},
			}
			Expect(d.Default(isvcCreateCtx(isvc), isvc)).To(Succeed())
			Expect(isvc.Spec.Predictor.Model).To(BeNil())
			Expect(isvc.Spec.Predictor.NodeSelector).To(BeNil())
			Expect(isvc.Labels[hardwareprofile.KueueQueueNameLabel]).To(BeEmpty())
		})

		It("isvc-2: HWP with resources — injected to predictor model (requests and limits)", func() {
			hwp := hwptestutil.NewHardwareProfile(hwpTestName, hwpTestNS,
				hwptestutil.ResourceSpec(
					[]string{"cpu", "4"},
					[]string{"memory", "8Gi"},
					[]string{"nvidia.com/gpu", "2"},
				))
			d := newISVCDefaulter(newDefaulterFakeClient(hwp))
			isvc := buildISVCForHWP(hwptestutil.HWPAnnotations(hwpTestName), nil)

			Expect(d.Default(isvcCreateCtx(isvc), isvc)).To(Succeed())
			Expect(isvc.Spec.Predictor.Model).NotTo(BeNil())
			req := isvc.Spec.Predictor.Model.Resources.Requests
			lim := isvc.Spec.Predictor.Model.Resources.Limits
			Expect(req[corev1.ResourceCPU]).To(Equal(resource.MustParse("4")))
			Expect(req[corev1.ResourceMemory]).To(Equal(resource.MustParse("8Gi")))
			Expect(req["nvidia.com/gpu"]).To(Equal(resource.MustParse("2")))
			// Guaranteed QoS: limits == requests
			Expect(lim[corev1.ResourceCPU]).To(Equal(resource.MustParse("4")))
			Expect(lim[corev1.ResourceMemory]).To(Equal(resource.MustParse("8Gi")))
			Expect(lim["nvidia.com/gpu"]).To(Equal(resource.MustParse("2")))
		})

		It("isvc-3: HWP with node scheduling — nodeSelector and tolerations injected (with tolerationSeconds)", func() {
			hwp := hwptestutil.NewHardwareProfile(hwpTestName, hwpTestNS,
				hwptestutil.NodeSpec(
					map[string]interface{}{"nvidia.com/gpu.product": "A100"},
					[]interface{}{hwptestutil.TolerationMapWithSeconds("nvidia.com/gpu", "Exists", "NoSchedule", 300)},
				))
			d := newISVCDefaulter(newDefaulterFakeClient(hwp))
			isvc := buildISVCForHWP(hwptestutil.HWPAnnotations(hwpTestName), nil)

			Expect(d.Default(isvcCreateCtx(isvc), isvc)).To(Succeed())
			Expect(isvc.Spec.Predictor.NodeSelector).To(HaveKeyWithValue("nvidia.com/gpu.product", "A100"))
			Expect(isvc.Spec.Predictor.Tolerations).To(HaveLen(1))
			tol := isvc.Spec.Predictor.Tolerations[0]
			Expect(tol.Key).To(Equal("nvidia.com/gpu"))
			Expect(tol.Operator).To(Equal(corev1.TolerationOpExists))
			Expect(tol.Effect).To(Equal(corev1.TaintEffectNoSchedule))
			Expect(tol.TolerationSeconds).NotTo(BeNil())
			Expect(*tol.TolerationSeconds).To(Equal(int64(300)))
		})

		It("isvc-4: HWP with Kueue scheduling — label set, node scheduling not applied (nil labels init)", func() {
			// HWP spec contains both Kueue and node scheduling fields. parseProfile reads the
			// Kueue queue name first and returns early, so the node section is never parsed and
			// the ResolvedProfile has no NodeSelector. The webhook then sets only the Kueue label
			// and returns before reaching the node scheduling block.
			spec := map[string]interface{}{
				"schedulingSpec": map[string]interface{}{
					"kueue": map[string]interface{}{"localQueueName": "test-queue"},
					"node":  map[string]interface{}{"nodeSelector": map[string]interface{}{"zone": "gpu-zone"}},
				},
			}
			hwp := hwptestutil.NewHardwareProfile(hwpTestName, hwpTestNS, spec)
			d := newISVCDefaulter(newDefaulterFakeClient(hwp))
			isvc := buildISVCForHWP(hwptestutil.HWPAnnotations(hwpTestName), nil)
			isvc.Labels = nil // explicitly nil to exercise nil-map initialisation in ApplyKueueLabel

			Expect(d.Default(isvcCreateCtx(isvc), isvc)).To(Succeed())
			Expect(isvc.Labels[hardwareprofile.KueueQueueNameLabel]).To(Equal("test-queue"))
			// nodeSelector not set — node scheduling was not applied despite being present in the HWP spec.
			Expect(isvc.Spec.Predictor.NodeSelector).To(BeNil())
		})

		It("isvc-5: HWP not found — admission blocked", func() {
			d := newISVCDefaulter(newDefaulterFakeClient()) // no HWP registered
			isvc := buildISVCForHWP(hwptestutil.HWPAnnotations("missing-hwp"), nil)

			err := d.Default(isvcCreateCtx(isvc), isvc)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("missing-hwp"))
		})

		It("isvc-6: namespace annotation stamped when absent", func() {
			hwp := hwptestutil.NewHardwareProfile(hwpTestName, hwpTestNS, hwptestutil.KueueSpec("q"))
			d := newISVCDefaulter(newDefaulterFakeClient(hwp))
			isvc := buildISVCForHWP(hwptestutil.HWPAnnotations(hwpTestName), nil)

			Expect(d.Default(isvcCreateCtx(isvc), isvc)).To(Succeed())
			Expect(isvc.Annotations[hardwareprofile.HardwareProfileAnnotationNamespace]).To(Equal(hwpTestNS))
		})

		It("isvc-16: HWP identifier missing defaultCount — admission blocked", func() {
			spec := map[string]interface{}{
				"identifiers": []interface{}{
					map[string]interface{}{"identifier": "cpu"}, // no defaultCount
				},
			}
			hwp := hwptestutil.NewHardwareProfile(hwpTestName, hwpTestNS, spec)
			d := newISVCDefaulter(newDefaulterFakeClient(hwp))
			isvc := buildISVCForHWP(hwptestutil.HWPAnnotations(hwpTestName), nil)

			Expect(d.Default(isvcCreateCtx(isvc), isvc)).To(HaveOccurred())
		})

		It("isvc-17: HWP identifier invalid quantity — admission blocked", func() {
			spec := map[string]interface{}{
				"identifiers": []interface{}{
					map[string]interface{}{"identifier": "cpu", "defaultCount": "bad!"},
				},
			}
			hwp := hwptestutil.NewHardwareProfile(hwpTestName, hwpTestNS, spec)
			d := newISVCDefaulter(newDefaulterFakeClient(hwp))
			isvc := buildISVCForHWP(hwptestutil.HWPAnnotations(hwpTestName), nil)

			Expect(d.Default(isvcCreateCtx(isvc), isvc)).To(HaveOccurred())
		})

		It("isvc-18: HWP with completely empty spec — isvc unmodified, admitted", func() {
			hwp := hwptestutil.NewHardwareProfile(hwpTestName, hwpTestNS, map[string]interface{}{})
			d := newISVCDefaulter(newDefaulterFakeClient(hwp))
			isvc := buildISVCForHWP(hwptestutil.HWPAnnotations(hwpTestName), nil)

			Expect(d.Default(isvcCreateCtx(isvc), isvc)).To(Succeed())
			if isvc.Spec.Predictor.Model != nil {
				Expect(isvc.Spec.Predictor.Model.Resources.Requests).To(BeEmpty())
				Expect(isvc.Spec.Predictor.Model.Resources.Limits).To(BeEmpty())
			}
			Expect(isvc.Spec.Predictor.NodeSelector).To(BeNil())
			Expect(isvc.Labels[hardwareprofile.KueueQueueNameLabel]).To(BeEmpty())
			Expect(isvc.Annotations[hardwareprofile.HardwareProfileAnnotationNamespace]).To(Equal(hwpTestNS))
		})
	})

	Describe("Test Group 2 — Injection semantics (CREATE)", func() {

		It("isvc-7: existing request preserved; limit backfilled from preserved request value", func() {
			hwp := hwptestutil.NewHardwareProfile(hwpTestName, hwpTestNS,
				hwptestutil.ResourceSpec([]string{"cpu", "4"}, []string{"nvidia.com/gpu", "2"}))
			d := newISVCDefaulter(newDefaulterFakeClient(hwp))
			isvc := buildISVCForHWP(hwptestutil.HWPAnnotations(hwpTestName), nil)
			isvc.Spec.Predictor.Model.Resources.Requests = corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("2"),
			}

			Expect(d.Default(isvcCreateCtx(isvc), isvc)).To(Succeed())
			req := isvc.Spec.Predictor.Model.Resources.Requests
			lim := isvc.Spec.Predictor.Model.Resources.Limits
			// CPU: existing request preserved (not overwritten by HWP "4")
			Expect(req[corev1.ResourceCPU]).To(Equal(resource.MustParse("2")))
			// CPU: limit backfilled from the preserved request value, not HWP defaultCount
			Expect(lim[corev1.ResourceCPU]).To(Equal(resource.MustParse("2")))
			// GPU: absent from existing requests — injected from HWP
			Expect(req["nvidia.com/gpu"]).To(Equal(resource.MustParse("2")))
			Expect(lim["nvidia.com/gpu"]).To(Equal(resource.MustParse("2")))
		})

		It("isvc-8: HWP overwrites conflicting nodeSelector key; adds new key", func() {
			hwp := hwptestutil.NewHardwareProfile(hwpTestName, hwpTestNS,
				hwptestutil.NodeSpec(
					map[string]interface{}{"zone": "eu-west", "tier": "gpu"},
					nil,
				))
			d := newISVCDefaulter(newDefaulterFakeClient(hwp))
			isvc := buildISVCForHWP(hwptestutil.HWPAnnotations(hwpTestName), nil)
			isvc.Spec.Predictor.NodeSelector = map[string]string{"zone": "us-east"}

			Expect(d.Default(isvcCreateCtx(isvc), isvc)).To(Succeed())
			Expect(isvc.Spec.Predictor.NodeSelector["zone"]).To(Equal("eu-west")) // HWP wins
			Expect(isvc.Spec.Predictor.NodeSelector["tier"]).To(Equal("gpu"))     // HWP-only key added
		})

		It("isvc-9: identical HWP toleration deduplicated (not doubled)", func() {
			tol := hwptestutil.TolerationMap("nvidia.com/gpu", "Exists", "NoSchedule")
			hwp := hwptestutil.NewHardwareProfile(hwpTestName, hwpTestNS,
				hwptestutil.NodeSpec(nil, []interface{}{tol}))
			d := newISVCDefaulter(newDefaulterFakeClient(hwp))
			isvc := buildISVCForHWP(hwptestutil.HWPAnnotations(hwpTestName), nil)
			isvc.Spec.Predictor.Tolerations = []corev1.Toleration{
				{Key: "nvidia.com/gpu", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
			}

			Expect(d.Default(isvcCreateCtx(isvc), isvc)).To(Succeed())
			Expect(isvc.Spec.Predictor.Tolerations).To(HaveLen(1))
			Expect(isvc.Spec.Predictor.Tolerations[0].Key).To(Equal("nvidia.com/gpu"))
			Expect(isvc.Spec.Predictor.Tolerations[0].Operator).To(Equal(corev1.TolerationOpExists))
			Expect(isvc.Spec.Predictor.Tolerations[0].Effect).To(Equal(corev1.TaintEffectNoSchedule))
		})

		It("isvc-10: HWP overwrites existing Kueue label", func() {
			hwp := hwptestutil.NewHardwareProfile(hwpTestName, hwpTestNS, hwptestutil.KueueSpec("hwp-queue"))
			d := newISVCDefaulter(newDefaulterFakeClient(hwp))
			isvc := buildISVCForHWP(hwptestutil.HWPAnnotations(hwpTestName), map[string]string{
				hardwareprofile.KueueQueueNameLabel: "user-queue",
			})

			Expect(d.Default(isvcCreateCtx(isvc), isvc)).To(Succeed())
			Expect(isvc.Labels[hardwareprofile.KueueQueueNameLabel]).To(Equal("hwp-queue"))
		})

		It("isvc-19: resource present in limits only — request injected from HWP, limit preserved", func() {
			hwp := hwptestutil.NewHardwareProfile(hwpTestName, hwpTestNS,
				hwptestutil.ResourceSpec([]string{"nvidia.com/gpu", "2"}))
			d := newISVCDefaulter(newDefaulterFakeClient(hwp))
			isvc := buildISVCForHWP(hwptestutil.HWPAnnotations(hwpTestName), nil)
			isvc.Spec.Predictor.Model.Resources.Limits = corev1.ResourceList{
				"nvidia.com/gpu": resource.MustParse("1"),
			}

			Expect(d.Default(isvcCreateCtx(isvc), isvc)).To(Succeed())
			Expect(isvc.Spec.Predictor.Model.Resources.Requests["nvidia.com/gpu"]).
				To(Equal(resource.MustParse("2"))) // request injected from HWP (was absent)
			Expect(isvc.Spec.Predictor.Model.Resources.Limits["nvidia.com/gpu"]).
				To(Equal(resource.MustParse("1"))) // user-set limit preserved
		})

		It("isvc-20: nil predictor Model — resource injection skipped, node scheduling still applied", func() {
			hwp := hwptestutil.NewHardwareProfile(hwpTestName, hwpTestNS,
				hwptestutil.NodeSpec(map[string]interface{}{"zone": "gpu-zone"}, nil))
			d := newISVCDefaulter(newDefaulterFakeClient(hwp))
			isvc := buildISVCNoModelForHWP(hwptestutil.HWPAnnotations(hwpTestName))

			Expect(d.Default(isvcCreateCtx(isvc), isvc)).To(Succeed())
			Expect(isvc.Spec.Predictor.Model).To(BeNil())
			Expect(isvc.Spec.Predictor.NodeSelector).To(HaveKeyWithValue("zone", "gpu-zone"))
		})

		It("isvc-21: non-conflicting pre-existing toleration preserved alongside HWP toleration", func() {
			hwp := hwptestutil.NewHardwareProfile(hwpTestName, hwpTestNS,
				hwptestutil.NodeSpec(nil, []interface{}{
					hwptestutil.TolerationMap("hwp-key", "Exists", "NoSchedule"),
				}))
			d := newISVCDefaulter(newDefaulterFakeClient(hwp))
			isvc := buildISVCForHWP(hwptestutil.HWPAnnotations(hwpTestName), nil)
			isvc.Spec.Predictor.Tolerations = []corev1.Toleration{
				{Key: "manual-key", Operator: corev1.TolerationOpExists},
			}

			Expect(d.Default(isvcCreateCtx(isvc), isvc)).To(Succeed())
			Expect(isvc.Spec.Predictor.Tolerations).To(HaveLen(2))
			keys := make([]string, 0, 2)
			for _, t := range isvc.Spec.Predictor.Tolerations {
				keys = append(keys, t.Key)
			}
			Expect(keys).To(ConsistOf("hwp-key", "manual-key"))
		})

		It("isvc-22: Kueue label already matches HWP value — unchanged, no error", func() {
			hwp := hwptestutil.NewHardwareProfile(hwpTestName, hwpTestNS, hwptestutil.KueueSpec("my-queue"))
			d := newISVCDefaulter(newDefaulterFakeClient(hwp))
			isvc := buildISVCForHWP(hwptestutil.HWPAnnotations(hwpTestName), map[string]string{
				hardwareprofile.KueueQueueNameLabel: "my-queue",
			})

			Expect(d.Default(isvcCreateCtx(isvc), isvc)).To(Succeed())
			Expect(isvc.Labels[hardwareprofile.KueueQueueNameLabel]).To(Equal("my-queue"))
		})
	})

	Describe("Test Group 3 — Profile change and annotation removal (UPDATE)", func() {

		It("isvc-11: same profile on UPDATE — merge semantics applied", func() {
			spec := hwptestutil.ResourceSpec([]string{"cpu", "4"})
			spec["schedulingSpec"] = map[string]interface{}{
				"node": map[string]interface{}{
					"nodeSelector": map[string]interface{}{"zone": "gpu-zone"},
				},
			}
			hwp := hwptestutil.NewHardwareProfile(hwpTestName, hwpTestNS, spec)
			d := newISVCDefaulter(newDefaulterFakeClient(hwp))
			oldISVC := buildISVCForHWP(hwptestutil.HWPAnnotationsWithNS(hwpTestName, hwpTestNS), nil)
			newISVC := buildISVCForHWP(hwptestutil.HWPAnnotationsWithNS(hwpTestName, hwpTestNS), nil)

			Expect(d.Default(isvcUpdateCtx(newISVC, oldISVC), newISVC)).To(Succeed())
			// Resources applied (same as CREATE).
			Expect(newISVC.Spec.Predictor.Model.Resources.Requests[corev1.ResourceCPU]).
				To(Equal(resource.MustParse("4")))
			Expect(newISVC.Spec.Predictor.Model.Resources.Limits[corev1.ResourceCPU]).
				To(Equal(resource.MustParse("4")))
			// Node scheduling applied.
			Expect(newISVC.Spec.Predictor.NodeSelector).To(HaveKeyWithValue("zone", "gpu-zone"))
			// Kueue label not set (HWP uses node scheduling, not Kueue).
			Expect(newISVC.Labels).NotTo(HaveKey(hardwareprofile.KueueQueueNameLabel))
		})

		It("isvc-12: different profile on UPDATE — ALL stanzas cleared before new profile applied", func() {
			hwpA := hwptestutil.NewHardwareProfile("hwp-a", hwpTestNS,
				hwptestutil.NodeSpec(
					map[string]interface{}{"zone": "eu-west"},
					[]interface{}{hwptestutil.TolerationMap("key-a", "Exists", "NoSchedule")},
				))
			hwpB := hwptestutil.NewHardwareProfile("hwp-b", hwpTestNS,
				hwptestutil.NodeSpec(
					map[string]interface{}{"tier": "gpu"},
					[]interface{}{hwptestutil.TolerationMap("key-b", "Exists", "NoSchedule")},
				))
			d := newISVCDefaulter(newDefaulterFakeClient(hwpA, hwpB))

			oldISVC := buildISVCForHWP(hwptestutil.HWPAnnotationsWithNS("hwp-a", hwpTestNS),
				map[string]string{hardwareprofile.KueueQueueNameLabel: "old-queue"})
			oldISVC.Spec.Predictor.NodeSelector = map[string]string{"zone": "eu-west"}
			oldISVC.Spec.Predictor.Tolerations = []corev1.Toleration{
				{Key: "key-a", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
			}

			// apiserver carries over old stanzas in the new object body
			newISVC := buildISVCForHWP(hwptestutil.HWPAnnotationsWithNS("hwp-b", hwpTestNS),
				map[string]string{hardwareprofile.KueueQueueNameLabel: "old-queue"})
			newISVC.Spec.Predictor.NodeSelector = map[string]string{"zone": "eu-west"}
			newISVC.Spec.Predictor.Tolerations = []corev1.Toleration{
				{Key: "key-a", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
			}

			Expect(d.Default(isvcUpdateCtx(newISVC, oldISVC), newISVC)).To(Succeed())
			Expect(newISVC.Spec.Predictor.NodeSelector).To(HaveKeyWithValue("tier", "gpu"))
			Expect(newISVC.Spec.Predictor.NodeSelector).NotTo(HaveKey("zone"))
			keys := make([]string, 0)
			for _, t := range newISVC.Spec.Predictor.Tolerations {
				keys = append(keys, t.Key)
			}
			Expect(keys).To(ConsistOf("key-b"))
			Expect(newISVC.Labels).NotTo(HaveKey(hardwareprofile.KueueQueueNameLabel))
		})

		It("isvc-13: annotation removed on UPDATE — HWP stanzas surgically removed, manual entries preserved", func() {
			hwpA := hwptestutil.NewHardwareProfile("hwp-a", hwpTestNS,
				hwptestutil.NodeSpec(
					map[string]interface{}{"zone": "eu-west"},
					[]interface{}{hwptestutil.TolerationMap("key-a", "", "")},
				))
			d := newISVCDefaulter(newDefaulterFakeClient(hwpA))

			oldISVC := buildISVCForHWP(hwptestutil.HWPAnnotationsWithNS("hwp-a", hwpTestNS), nil)
			oldISVC.Spec.Predictor.NodeSelector = map[string]string{"zone": "eu-west"}
			oldISVC.Spec.Predictor.Tolerations = []corev1.Toleration{{Key: "key-a"}, {Key: "manual"}}

			newISVC := buildISVCForHWP(nil, nil) // HWP annotation removed
			newISVC.Annotations = map[string]string{
				hardwareprofile.HardwareProfileAnnotationNamespace: hwpTestNS,
			}
			newISVC.Spec.Predictor.NodeSelector = map[string]string{"zone": "eu-west"}
			newISVC.Spec.Predictor.Tolerations = []corev1.Toleration{{Key: "key-a"}, {Key: "manual"}}

			Expect(d.Default(isvcUpdateCtx(newISVC, oldISVC), newISVC)).To(Succeed())
			// HWP nodeSelector entry removed
			Expect(newISVC.Spec.Predictor.NodeSelector).NotTo(HaveKey("zone"))
			// HWP toleration removed; manual toleration preserved
			Expect(newISVC.Spec.Predictor.Tolerations).To(HaveLen(1))
			Expect(newISVC.Spec.Predictor.Tolerations[0].Key).To(Equal("manual"))
			// Namespace annotation removed
			Expect(newISVC.Annotations).NotTo(HaveKey(hardwareprofile.HardwareProfileAnnotationNamespace))
		})

		It("isvc-14: annotation removed, old HWP not found — admitted, namespace annotation cleaned", func() {
			d := newISVCDefaulter(newDefaulterFakeClient()) // HWP CR was deleted

			oldISVC := buildISVCForHWP(hwptestutil.HWPAnnotationsWithNS("hwp-a", hwpTestNS), nil)

			newISVC := buildISVCForHWP(nil, nil) // annotation removed
			newISVC.Annotations = map[string]string{
				hardwareprofile.HardwareProfileAnnotationNamespace: hwpTestNS,
			}
			newISVC.Spec.Predictor.NodeSelector = map[string]string{"zone": "eu-west"}

			Expect(d.Default(isvcUpdateCtx(newISVC, oldISVC), newISVC)).To(Succeed())
			Expect(newISVC.Annotations).NotTo(HaveKey(hardwareprofile.HardwareProfileAnnotationNamespace))
			// Stanzas remain because HWP is gone — cannot clean up
			Expect(newISVC.Spec.Predictor.NodeSelector).To(HaveKeyWithValue("zone", "eu-west"))
		})

		It("isvc-15: cross-namespace HWP reference — resources fetched from other-ns", func() {
			hwp := hwptestutil.NewHardwareProfile(hwpTestName, "other-ns",
				hwptestutil.ResourceSpec([]string{"cpu", "8"}))
			d := newISVCDefaulter(newDefaulterFakeClient(hwp))
			isvc := buildISVCForHWP(hwptestutil.HWPAnnotationsWithNS(hwpTestName, "other-ns"), nil)

			Expect(d.Default(isvcCreateCtx(isvc), isvc)).To(Succeed())
			Expect(isvc.Spec.Predictor.Model.Resources.Requests[corev1.ResourceCPU]).
				To(Equal(resource.MustParse("8")))
		})

		It("isvc-23: namespace-only profile change on UPDATE — ALL stanzas cleared, new profile applied", func() {
			hwpNS2 := hwptestutil.NewHardwareProfile(hwpTestName, "namespace-2",
				hwptestutil.NodeSpec(map[string]interface{}{"tier": "gpu"}, nil))
			d := newISVCDefaulter(newDefaulterFakeClient(hwpNS2))

			oldISVC := buildISVCForHWP(hwptestutil.HWPAnnotationsWithNS(hwpTestName, "namespace-1"), nil)
			oldISVC.Spec.Predictor.NodeSelector = map[string]string{"zone": "eu-west"}

			newISVC := buildISVCForHWP(hwptestutil.HWPAnnotationsWithNS(hwpTestName, "namespace-2"), nil)
			newISVC.Spec.Predictor.NodeSelector = map[string]string{"zone": "eu-west"} // carried over

			Expect(d.Default(isvcUpdateCtx(newISVC, oldISVC), newISVC)).To(Succeed())
			Expect(newISVC.Spec.Predictor.NodeSelector).To(HaveKeyWithValue("tier", "gpu"))
			Expect(newISVC.Spec.Predictor.NodeSelector).NotTo(HaveKey("zone"))
		})

		It("isvc-24: initial annotation assignment on UPDATE — merge semantics (no blanket clear)", func() {
			spec := hwptestutil.ResourceSpec([]string{"cpu", "4"})
			spec["schedulingSpec"] = map[string]interface{}{
				"node": map[string]interface{}{
					"nodeSelector": map[string]interface{}{"zone": "gpu-zone"},
				},
			}
			hwp := hwptestutil.NewHardwareProfile(hwpTestName, hwpTestNS, spec)
			d := newISVCDefaulter(newDefaulterFakeClient(hwp))

			oldISVC := buildISVCForHWP(nil, nil) // old object has no HWP annotation
			newISVC := buildISVCForHWP(hwptestutil.HWPAnnotations(hwpTestName), nil)

			Expect(d.Default(isvcUpdateCtx(newISVC, oldISVC), newISVC)).To(Succeed())
			// Resources applied via merge semantics (same as CREATE — no blanket clear).
			Expect(newISVC.Spec.Predictor.Model.Resources.Requests[corev1.ResourceCPU]).
				To(Equal(resource.MustParse("4")))
			Expect(newISVC.Spec.Predictor.Model.Resources.Limits[corev1.ResourceCPU]).
				To(Equal(resource.MustParse("4")))
			// Node scheduling applied.
			Expect(newISVC.Spec.Predictor.NodeSelector).To(HaveKeyWithValue("zone", "gpu-zone"))
			// Kueue label not set (HWP uses node scheduling, not Kueue).
			Expect(newISVC.Labels).NotTo(HaveKey(hardwareprofile.KueueQueueNameLabel))
		})

		It("isvc-25: Kueue-to-Kueue profile switch — new label applied, no error", func() {
			hwpA := hwptestutil.NewHardwareProfile("hwp-a", hwpTestNS, hwptestutil.KueueSpec("queue-a"))
			hwpB := hwptestutil.NewHardwareProfile("hwp-b", hwpTestNS, hwptestutil.KueueSpec("queue-b"))
			d := newISVCDefaulter(newDefaulterFakeClient(hwpA, hwpB))

			oldISVC := buildISVCForHWP(hwptestutil.HWPAnnotationsWithNS("hwp-a", hwpTestNS),
				map[string]string{hardwareprofile.KueueQueueNameLabel: "queue-a"})
			newISVC := buildISVCForHWP(hwptestutil.HWPAnnotationsWithNS("hwp-b", hwpTestNS),
				map[string]string{hardwareprofile.KueueQueueNameLabel: "queue-a"}) // carried over

			Expect(d.Default(isvcUpdateCtx(newISVC, oldISVC), newISVC)).To(Succeed())
			Expect(newISVC.Labels[hardwareprofile.KueueQueueNameLabel]).To(Equal("queue-b"))
		})

		It("isvc-26: annotation removed, nodeSelector value differs from old HWP — key preserved", func() {
			hwpA := hwptestutil.NewHardwareProfile("hwp-a", hwpTestNS,
				hwptestutil.NodeSpec(map[string]interface{}{"zone": "eu-west"}, nil))
			d := newISVCDefaulter(newDefaulterFakeClient(hwpA))

			oldISVC := buildISVCForHWP(hwptestutil.HWPAnnotationsWithNS("hwp-a", hwpTestNS), nil)
			oldISVC.Spec.Predictor.NodeSelector = map[string]string{"zone": "eu-west"}

			newISVC := buildISVCForHWP(nil, nil) // annotation removed
			newISVC.Annotations = map[string]string{
				hardwareprofile.HardwareProfileAnnotationNamespace: hwpTestNS,
			}
			newISVC.Spec.Predictor.NodeSelector = map[string]string{"zone": "us-east"} // user-changed value

			Expect(d.Default(isvcUpdateCtx(newISVC, oldISVC), newISVC)).To(Succeed())
			// Value differs from HWP record — key is preserved (not removed)
			Expect(newISVC.Spec.Predictor.NodeSelector).To(HaveKeyWithValue("zone", "us-east"))
		})

		It("isvc-27: annotation removed, isvc has no HWP stanzas — no-op, no panic", func() {
			hwpA := hwptestutil.NewHardwareProfile("hwp-a", hwpTestNS,
				hwptestutil.NodeSpec(map[string]interface{}{"zone": "eu-west"}, nil))
			d := newISVCDefaulter(newDefaulterFakeClient(hwpA))

			oldISVC := buildISVCForHWP(hwptestutil.HWPAnnotationsWithNS("hwp-a", hwpTestNS), nil)
			oldISVC.Spec.Predictor.NodeSelector = map[string]string{"zone": "eu-west"}

			newISVC := buildISVCForHWP(nil, nil) // annotation removed; user already cleaned stanzas
			newISVC.Annotations = map[string]string{
				hardwareprofile.HardwareProfileAnnotationNamespace: hwpTestNS,
			}

			Expect(d.Default(isvcUpdateCtx(newISVC, oldISVC), newISVC)).To(Succeed())
			Expect(newISVC.Spec.Predictor.NodeSelector).To(BeNil())
		})

		It("isvc-28: annotation removed, user overwrote Kueue label — removed unconditionally", func() {
			hwpA := hwptestutil.NewHardwareProfile("hwp-a", hwpTestNS, hwptestutil.KueueSpec("hwp-queue"))
			d := newISVCDefaulter(newDefaulterFakeClient(hwpA))

			oldISVC := buildISVCForHWP(hwptestutil.HWPAnnotationsWithNS("hwp-a", hwpTestNS),
				map[string]string{hardwareprofile.KueueQueueNameLabel: "hwp-queue"})
			newISVC := buildISVCForHWP(nil, map[string]string{
				hardwareprofile.KueueQueueNameLabel: "user-queue", // user changed it
			})
			newISVC.Annotations = map[string]string{
				hardwareprofile.HardwareProfileAnnotationNamespace: hwpTestNS,
			}

			Expect(d.Default(isvcUpdateCtx(newISVC, oldISVC), newISVC)).To(Succeed())
			Expect(newISVC.Labels).NotTo(HaveKey(hardwareprofile.KueueQueueNameLabel))
		})

		It("isvc-29: annotation removed, no Kueue label present — no-op, no panic", func() {
			hwpA := hwptestutil.NewHardwareProfile("hwp-a", hwpTestNS,
				hwptestutil.NodeSpec(map[string]interface{}{"zone": "eu-west"}, nil)) // node scheduling only
			d := newISVCDefaulter(newDefaulterFakeClient(hwpA))

			oldISVC := buildISVCForHWP(hwptestutil.HWPAnnotationsWithNS("hwp-a", hwpTestNS), nil)
			oldISVC.Spec.Predictor.NodeSelector = map[string]string{"zone": "eu-west"}

			newISVC := buildISVCForHWP(nil, nil) // annotation removed, no Kueue label
			newISVC.Annotations = map[string]string{
				hardwareprofile.HardwareProfileAnnotationNamespace: hwpTestNS,
			}
			newISVC.Spec.Predictor.NodeSelector = map[string]string{"zone": "eu-west"}

			Expect(d.Default(isvcUpdateCtx(newISVC, oldISVC), newISVC)).To(Succeed())
			Expect(newISVC.Labels).NotTo(HaveKey(hardwareprofile.KueueQueueNameLabel))
		})
	})
})
