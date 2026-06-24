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
	"encoding/json"

	kservev1alpha2 "github.com/kserve/kserve/pkg/apis/serving/v1alpha2"
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
	llmHWPNS   = "test-llm-hwp-ns"
	llmHWPName = "test-llm-hwp"
	llmHWPISVC = "test-llmisvc"
)

// buildLLMISVCHWP creates a v1alpha2 LLMInferenceService for HWP tests.
// TypeMeta is always set so that json.Marshal produces the kind/apiVersion fields required by
// unstructured.UnmarshalJSON (used in handleHWPRemovalLLMISVC to parse the old object).
func buildLLMISVCHWP(annotations, labels map[string]string) *kservev1alpha2.LLMInferenceService {
	return &kservev1alpha2.LLMInferenceService{
		TypeMeta: metav1.TypeMeta{
			Kind:       "LLMInferenceService",
			APIVersion: "serving.kserve.io/v1alpha2",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        llmHWPISVC,
			Namespace:   llmHWPNS,
			Annotations: annotations,
			Labels:      labels,
		},
	}
}

// llmHWPCreateCtx returns a context carrying a CREATE admission request.
func llmHWPCreateCtx(llm *kservev1alpha2.LLMInferenceService) context.Context {
	raw, _ := json.Marshal(llm)
	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Create,
			Namespace: llmHWPNS,
			Object:    runtime.RawExtension{Raw: raw},
		},
	}
	return admission.NewContextWithRequest(context.Background(), req)
}

// llmHWPUpdateCtx returns a context carrying an UPDATE admission request.
func llmHWPUpdateCtx(newLLM, oldLLM *kservev1alpha2.LLMInferenceService) context.Context {
	newRaw, _ := json.Marshal(newLLM)
	oldRaw, _ := json.Marshal(oldLLM)
	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Update,
			Namespace: llmHWPNS,
			Object:    runtime.RawExtension{Raw: newRaw},
			OldObject: runtime.RawExtension{Raw: oldRaw},
		},
	}
	return admission.NewContextWithRequest(context.Background(), req)
}

// mainContainer returns a container named "main" with optional resources.
func mainContainer(requests, limits corev1.ResourceList) corev1.Container {
	return corev1.Container{
		Name: "main",
		Resources: corev1.ResourceRequirements{
			Requests: requests,
			Limits:   limits,
		},
	}
}

// ─── tests ────────────────────────────────────────────────────────────────────

var _ = Describe("LLMInferenceService HardwareProfile Webhook", func() {

	Describe("Test Group 4 — Basic injection (CREATE)", func() {

		It("LLM-1: no annotation — LLMIsvc unmodified", func() {
			d := newLLMDefaulter(newLLMFakeClient())
			llm := buildLLMISVCHWP(nil, nil) // nil annotations exercises nil-map path through ProfileRef

			Expect(d.Default(llmHWPCreateCtx(llm), llm)).To(Succeed())
			Expect(llm.Spec.Template).To(BeNil())
			Expect(llm.Labels[hardwareprofile.KueueQueueNameLabel]).To(BeEmpty())
		})

		It("LLM-2: HWP with resources — injected into 'main' container", func() {
			hwp := hwptestutil.NewHardwareProfile(llmHWPName, llmHWPNS,
				hwptestutil.ResourceSpec([]string{"cpu", "4"}, []string{"memory", "8Gi"}))
			d := newLLMDefaulter(newLLMFakeClient(hwp))
			llm := buildLLMISVCHWP(hwptestutil.HWPAnnotations(llmHWPName), nil)
			llm.Spec.Template = &corev1.PodSpec{
				Containers: []corev1.Container{{Name: "main"}},
			}

			Expect(d.Default(llmHWPCreateCtx(llm), llm)).To(Succeed())
			Expect(llm.Spec.Template.Containers).To(HaveLen(1))
			req := llm.Spec.Template.Containers[0].Resources.Requests
			lim := llm.Spec.Template.Containers[0].Resources.Limits
			Expect(req[corev1.ResourceCPU]).To(Equal(resource.MustParse("4")))
			Expect(req[corev1.ResourceMemory]).To(Equal(resource.MustParse("8Gi")))
			Expect(lim[corev1.ResourceCPU]).To(Equal(resource.MustParse("4")))
			Expect(lim[corev1.ResourceMemory]).To(Equal(resource.MustParse("8Gi")))
		})

		It("LLM-3: HWP with node scheduling — spec.template fields updated (with tolerationSeconds)", func() {
			hwp := hwptestutil.NewHardwareProfile(llmHWPName, llmHWPNS,
				hwptestutil.NodeSpec(
					map[string]interface{}{"zone": "gpu-zone"},
					[]interface{}{hwptestutil.TolerationMapWithSeconds("nvidia.com/gpu", "Exists", "NoSchedule", 300)},
				))
			d := newLLMDefaulter(newLLMFakeClient(hwp))
			llm := buildLLMISVCHWP(hwptestutil.HWPAnnotations(llmHWPName), nil)

			Expect(d.Default(llmHWPCreateCtx(llm), llm)).To(Succeed())
			Expect(llm.Spec.Template).NotTo(BeNil())
			Expect(llm.Spec.Template.NodeSelector).To(HaveKeyWithValue("zone", "gpu-zone"))
			Expect(llm.Spec.Template.Tolerations).To(HaveLen(1))
			tol := llm.Spec.Template.Tolerations[0]
			Expect(tol.Key).To(Equal("nvidia.com/gpu"))
			Expect(tol.Operator).To(Equal(corev1.TolerationOpExists))
			Expect(tol.Effect).To(Equal(corev1.TaintEffectNoSchedule))
			Expect(tol.TolerationSeconds).NotTo(BeNil())
			Expect(*tol.TolerationSeconds).To(Equal(int64(300)))
		})

		It("LLM-4: HWP with Kueue scheduling — label set, node scheduling not applied", func() {
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
			hwp := hwptestutil.NewHardwareProfile(llmHWPName, llmHWPNS, spec)
			d := newLLMDefaulter(newLLMFakeClient(hwp))
			llm := buildLLMISVCHWP(hwptestutil.HWPAnnotations(llmHWPName), nil)
			llm.Labels = nil // explicitly nil to exercise nil-map initialisation in ApplyKueueLabel

			Expect(d.Default(llmHWPCreateCtx(llm), llm)).To(Succeed())
			Expect(llm.Labels[hardwareprofile.KueueQueueNameLabel]).To(Equal("test-queue"))
			// No template created and no nodeSelector set — node scheduling was not applied.
			Expect(llm.Spec.Template).To(BeNil())
		})

		It("LLM-5: HWP not found — admission blocked", func() {
			d := newLLMDefaulter(newLLMFakeClient()) // no HWP registered
			llm := buildLLMISVCHWP(hwptestutil.HWPAnnotations("missing-hwp"), nil)

			err := d.Default(llmHWPCreateCtx(llm), llm)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("missing-hwp"))
		})

		It("LLM-6: containers non-empty but no 'main' — ALL HWP application skipped", func() {
			spec := hwptestutil.ResourceSpec([]string{"cpu", "8"})
			spec["schedulingSpec"] = map[string]interface{}{
				"node": map[string]interface{}{
					"nodeSelector": map[string]interface{}{"zone": "gpu-zone"},
				},
			}
			hwp := hwptestutil.NewHardwareProfile(llmHWPName, llmHWPNS, spec)
			d := newLLMDefaulter(newLLMFakeClient(hwp))
			llm := buildLLMISVCHWP(hwptestutil.HWPAnnotations(llmHWPName), nil)
			llm.Spec.Template = &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "server", // no "main"
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2")},
						},
					},
				},
			}

			Expect(d.Default(llmHWPCreateCtx(llm), llm)).To(Succeed())
			// "server" container resources unchanged — HWP cpu "8" was not injected.
			Expect(llm.Spec.Template.Containers[0].Resources.Requests[corev1.ResourceCPU]).
				To(Equal(resource.MustParse("2")))
			// Node scheduling NOT applied — HWP nodeSelector was not injected.
			Expect(llm.Spec.Template.NodeSelector).To(BeNil())
			// Kueue label NOT applied.
			Expect(llm.Labels[hardwareprofile.KueueQueueNameLabel]).To(BeEmpty())
		})

		It("LLM-7: absent container list — 'main' created and resources injected", func() {
			// Build a spec with both resource identifiers (triggers "main" creation) and node scheduling.
			spec := hwptestutil.ResourceSpec([]string{"cpu", "4"})
			spec["schedulingSpec"] = map[string]interface{}{
				"node": map[string]interface{}{
					"nodeSelector": map[string]interface{}{"zone": "gpu-zone"},
				},
			}
			hwp := hwptestutil.NewHardwareProfile(llmHWPName, llmHWPNS, spec)
			d := newLLMDefaulter(newLLMFakeClient(hwp))
			llm := buildLLMISVCHWP(hwptestutil.HWPAnnotations(llmHWPName), nil)
			// Spec.Template is nil — no containers

			Expect(d.Default(llmHWPCreateCtx(llm), llm)).To(Succeed())
			Expect(llm.Spec.Template).NotTo(BeNil())
			Expect(llm.Spec.Template.Containers).To(HaveLen(1))
			Expect(llm.Spec.Template.Containers[0].Name).To(Equal("main"))
			Expect(llm.Spec.Template.Containers[0].Resources.Requests[corev1.ResourceCPU]).
				To(Equal(resource.MustParse("4")))
			Expect(llm.Spec.Template.NodeSelector).To(HaveKeyWithValue("zone", "gpu-zone"))
		})
	})

	Describe("Test Group 5 — Injection semantics (CREATE)", func() {

		It("LLM-8: existing 'main' resource preserved; HWP provides others", func() {
			hwp := hwptestutil.NewHardwareProfile(llmHWPName, llmHWPNS,
				hwptestutil.ResourceSpec([]string{"cpu", "4"}, []string{"nvidia.com/gpu", "2"}))
			d := newLLMDefaulter(newLLMFakeClient(hwp))
			llm := buildLLMISVCHWP(hwptestutil.HWPAnnotations(llmHWPName), nil)
			llm.Spec.Template = &corev1.PodSpec{
				Containers: []corev1.Container{
					mainContainer(
						corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2")},
						nil,
					),
				},
			}

			Expect(d.Default(llmHWPCreateCtx(llm), llm)).To(Succeed())
			req := llm.Spec.Template.Containers[0].Resources.Requests
			lim := llm.Spec.Template.Containers[0].Resources.Limits
			// CPU preserved
			Expect(req[corev1.ResourceCPU]).To(Equal(resource.MustParse("2")))
			Expect(lim[corev1.ResourceCPU]).To(Equal(resource.MustParse("2")))
			// GPU injected from HWP
			Expect(req["nvidia.com/gpu"]).To(Equal(resource.MustParse("2")))
			Expect(lim["nvidia.com/gpu"]).To(Equal(resource.MustParse("2")))
		})

		It("LLM-9: HWP overwrites conflicting nodeSelector key in spec.template", func() {
			hwp := hwptestutil.NewHardwareProfile(llmHWPName, llmHWPNS,
				hwptestutil.NodeSpec(
					map[string]interface{}{"zone": "eu-west"},
					nil,
				))
			d := newLLMDefaulter(newLLMFakeClient(hwp))
			llm := buildLLMISVCHWP(hwptestutil.HWPAnnotations(llmHWPName), nil)
			llm.Spec.Template = &corev1.PodSpec{
				NodeSelector: map[string]string{"zone": "us-east"},
			}

			Expect(d.Default(llmHWPCreateCtx(llm), llm)).To(Succeed())
			Expect(llm.Spec.Template.NodeSelector["zone"]).To(Equal("eu-west")) // HWP wins
		})

		It("LLM-10: HWP overwrites existing Kueue label on LLMIsvc metadata", func() {
			hwp := hwptestutil.NewHardwareProfile(llmHWPName, llmHWPNS, hwptestutil.KueueSpec("hwp-queue"))
			d := newLLMDefaulter(newLLMFakeClient(hwp))
			llm := buildLLMISVCHWP(hwptestutil.HWPAnnotations(llmHWPName),
				map[string]string{hardwareprofile.KueueQueueNameLabel: "user-queue"})

			Expect(d.Default(llmHWPCreateCtx(llm), llm)).To(Succeed())
			Expect(llm.Labels[hardwareprofile.KueueQueueNameLabel]).To(Equal("hwp-queue"))
		})
	})

	Describe("Test Group 6 — Profile change and annotation removal (UPDATE)", func() {

		It("LLM-11: different profile on UPDATE — ALL stanzas cleared, new profile applied", func() {
			hwpA := hwptestutil.NewHardwareProfile("hwp-a", llmHWPNS,
				hwptestutil.NodeSpec(
					map[string]interface{}{"zone": "eu-west"},
					[]interface{}{hwptestutil.TolerationMap("key-a", "Exists", "NoSchedule")},
				))
			hwpB := hwptestutil.NewHardwareProfile("hwp-b", llmHWPNS,
				hwptestutil.NodeSpec(
					map[string]interface{}{"tier": "gpu"},
					[]interface{}{hwptestutil.TolerationMap("key-b", "Exists", "NoSchedule")},
				))
			d := newLLMDefaulter(newLLMFakeClient(hwpA, hwpB))

			oldLLM := buildLLMISVCHWP(hwptestutil.HWPAnnotationsWithNS("hwp-a", llmHWPNS),
				map[string]string{hardwareprofile.KueueQueueNameLabel: "old-queue"})
			oldLLM.Spec.Template = &corev1.PodSpec{
				NodeSelector: map[string]string{"zone": "eu-west"},
				Tolerations: []corev1.Toleration{
					{Key: "key-a", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
				},
			}

			// apiserver carries over old stanzas in the new object body
			newLLM := buildLLMISVCHWP(hwptestutil.HWPAnnotationsWithNS("hwp-b", llmHWPNS),
				map[string]string{hardwareprofile.KueueQueueNameLabel: "old-queue"})
			newLLM.Spec.Template = &corev1.PodSpec{
				NodeSelector: map[string]string{"zone": "eu-west"},
				Tolerations: []corev1.Toleration{
					{Key: "key-a", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
				},
			}

			Expect(d.Default(llmHWPUpdateCtx(newLLM, oldLLM), newLLM)).To(Succeed())
			Expect(newLLM.Spec.Template.NodeSelector).To(HaveKeyWithValue("tier", "gpu"))
			Expect(newLLM.Spec.Template.NodeSelector).NotTo(HaveKey("zone"))
			keys := make([]string, 0)
			for _, t := range newLLM.Spec.Template.Tolerations {
				keys = append(keys, t.Key)
			}
			Expect(keys).To(ConsistOf("key-b"))
			Expect(newLLM.Labels).NotTo(HaveKey(hardwareprofile.KueueQueueNameLabel))
		})

		It("LLM-12: annotation removed on UPDATE — HWP stanzas surgically removed in spec.template", func() {
			hwpA := hwptestutil.NewHardwareProfile("hwp-a", llmHWPNS,
				hwptestutil.NodeSpec(
					map[string]interface{}{"zone": "eu-west"},
					[]interface{}{hwptestutil.TolerationMap("key-a", "", "")},
				))
			d := newLLMDefaulter(newLLMFakeClient(hwpA))

			oldLLM := buildLLMISVCHWP(hwptestutil.HWPAnnotationsWithNS("hwp-a", llmHWPNS), nil)
			oldLLM.Spec.Template = &corev1.PodSpec{
				NodeSelector: map[string]string{"zone": "eu-west"},
				Tolerations:  []corev1.Toleration{{Key: "key-a"}, {Key: "manual"}},
			}

			newLLM := buildLLMISVCHWP(nil, nil) // HWP annotation removed
			newLLM.Annotations = map[string]string{
				hardwareprofile.HardwareProfileAnnotationNamespace: llmHWPNS,
			}
			newLLM.Spec.Template = &corev1.PodSpec{
				NodeSelector: map[string]string{"zone": "eu-west"},
				Tolerations:  []corev1.Toleration{{Key: "key-a"}, {Key: "manual"}},
			}

			Expect(d.Default(llmHWPUpdateCtx(newLLM, oldLLM), newLLM)).To(Succeed())
			// HWP nodeSelector entry removed
			Expect(newLLM.Spec.Template.NodeSelector).NotTo(HaveKey("zone"))
			// HWP toleration removed; manual toleration preserved
			Expect(newLLM.Spec.Template.Tolerations).To(HaveLen(1))
			Expect(newLLM.Spec.Template.Tolerations[0].Key).To(Equal("manual"))
			// Namespace annotation removed
			Expect(newLLM.Annotations).NotTo(HaveKey(hardwareprofile.HardwareProfileAnnotationNamespace))
		})
	})

	Describe("Test Group 5 additions — Injection semantics (CREATE)", func() {

		It("LLM-13: resource present in 'main' container limits only — request injected, limit preserved", func() {
			hwp := hwptestutil.NewHardwareProfile(llmHWPName, llmHWPNS,
				hwptestutil.ResourceSpec([]string{"nvidia.com/gpu", "2"}))
			d := newLLMDefaulter(newLLMFakeClient(hwp))
			llm := buildLLMISVCHWP(hwptestutil.HWPAnnotations(llmHWPName), nil)
			llm.Spec.Template = &corev1.PodSpec{
				Containers: []corev1.Container{
					mainContainer(nil, corev1.ResourceList{"nvidia.com/gpu": resource.MustParse("1")}),
				},
			}

			Expect(d.Default(llmHWPCreateCtx(llm), llm)).To(Succeed())
			req := llm.Spec.Template.Containers[0].Resources.Requests
			lim := llm.Spec.Template.Containers[0].Resources.Limits
			// Request injected from HWP (was absent).
			Expect(req["nvidia.com/gpu"]).To(Equal(resource.MustParse("2")))
			// User-set limit preserved (not overwritten).
			Expect(lim["nvidia.com/gpu"]).To(Equal(resource.MustParse("1")))
		})

		It("LLM-14: pre-existing non-conflicting toleration — HWP toleration added, original preserved", func() {
			hwp := hwptestutil.NewHardwareProfile(llmHWPName, llmHWPNS,
				hwptestutil.NodeSpec(nil, []interface{}{
					hwptestutil.TolerationMap("hwp-key", "Exists", "NoSchedule"),
				}))
			d := newLLMDefaulter(newLLMFakeClient(hwp))
			llm := buildLLMISVCHWP(hwptestutil.HWPAnnotations(llmHWPName), nil)
			llm.Spec.Template = &corev1.PodSpec{
				Tolerations: []corev1.Toleration{{Key: "manual-key", Operator: corev1.TolerationOpExists}},
			}

			Expect(d.Default(llmHWPCreateCtx(llm), llm)).To(Succeed())
			Expect(llm.Spec.Template.Tolerations).To(HaveLen(2))
			keys := make([]string, 0, 2)
			for _, t := range llm.Spec.Template.Tolerations {
				keys = append(keys, t.Key)
			}
			Expect(keys).To(ConsistOf("hwp-key", "manual-key"))
		})

		It("LLM-15: Kueue label already matches HWP value — label unchanged", func() {
			hwp := hwptestutil.NewHardwareProfile(llmHWPName, llmHWPNS, hwptestutil.KueueSpec("my-queue"))
			d := newLLMDefaulter(newLLMFakeClient(hwp))
			llm := buildLLMISVCHWP(hwptestutil.HWPAnnotations(llmHWPName),
				map[string]string{hardwareprofile.KueueQueueNameLabel: "my-queue"})

			Expect(d.Default(llmHWPCreateCtx(llm), llm)).To(Succeed())
			Expect(llm.Labels[hardwareprofile.KueueQueueNameLabel]).To(Equal("my-queue"))
		})
	})

	Describe("Test Group 6 additions — Profile change and annotation removal (UPDATE)", func() {

		It("LLM-16: namespace-only profile change on UPDATE — ALL stanzas cleared, new profile applied", func() {
			hwpNS2 := hwptestutil.NewHardwareProfile(llmHWPName, "namespace-2",
				hwptestutil.NodeSpec(map[string]interface{}{"tier": "gpu"}, nil))
			d := newLLMDefaulter(newLLMFakeClient(hwpNS2))

			oldLLM := buildLLMISVCHWP(hwptestutil.HWPAnnotationsWithNS(llmHWPName, "namespace-1"), nil)
			oldLLM.Spec.Template = &corev1.PodSpec{
				NodeSelector: map[string]string{"zone": "eu-west"},
				Tolerations:  []corev1.Toleration{{Key: "old-key", Operator: corev1.TolerationOpExists}},
			}

			// apiserver carries over old stanzas in the new object body
			newLLM := buildLLMISVCHWP(hwptestutil.HWPAnnotationsWithNS(llmHWPName, "namespace-2"), nil)
			newLLM.Spec.Template = &corev1.PodSpec{
				NodeSelector: map[string]string{"zone": "eu-west"},
				Tolerations:  []corev1.Toleration{{Key: "old-key", Operator: corev1.TolerationOpExists}},
			}

			Expect(d.Default(llmHWPUpdateCtx(newLLM, oldLLM), newLLM)).To(Succeed())
			// New profile's nodeSelector applied; old entry cleared.
			Expect(newLLM.Spec.Template.NodeSelector).To(HaveKeyWithValue("tier", "gpu"))
			Expect(newLLM.Spec.Template.NodeSelector).NotTo(HaveKey("zone"))
			// Old toleration cleared (new profile has none).
			Expect(newLLM.Spec.Template.Tolerations).To(BeEmpty())
		})

		It("LLM-17: initial annotation assignment on UPDATE — merge semantics applied", func() {
			spec := hwptestutil.ResourceSpec([]string{"cpu", "4"})
			spec["schedulingSpec"] = map[string]interface{}{
				"node": map[string]interface{}{
					"nodeSelector": map[string]interface{}{"zone": "gpu-zone"},
				},
			}
			hwp := hwptestutil.NewHardwareProfile(llmHWPName, llmHWPNS, spec)
			d := newLLMDefaulter(newLLMFakeClient(hwp))

			oldLLM := buildLLMISVCHWP(nil, nil) // old object has no HWP annotation
			newLLM := buildLLMISVCHWP(hwptestutil.HWPAnnotations(llmHWPName), nil)
			newLLM.Spec.Template = &corev1.PodSpec{
				Containers: []corev1.Container{{Name: "main"}},
			}

			Expect(d.Default(llmHWPUpdateCtx(newLLM, oldLLM), newLLM)).To(Succeed())
			// Resources applied via merge semantics (same as CREATE — no blanket clear).
			Expect(newLLM.Spec.Template.Containers[0].Resources.Requests[corev1.ResourceCPU]).
				To(Equal(resource.MustParse("4")))
			// Node scheduling applied.
			Expect(newLLM.Spec.Template.NodeSelector).To(HaveKeyWithValue("zone", "gpu-zone"))
			// Kueue label not set (HWP uses node scheduling, not Kueue).
			Expect(newLLM.Labels).NotTo(HaveKey(hardwareprofile.KueueQueueNameLabel))
		})

		It("LLM-18: Kueue-to-Kueue profile switch — new label applied, no error", func() {
			hwpA := hwptestutil.NewHardwareProfile("hwp-a", llmHWPNS, hwptestutil.KueueSpec("queue-a"))
			hwpB := hwptestutil.NewHardwareProfile("hwp-b", llmHWPNS, hwptestutil.KueueSpec("queue-b"))
			d := newLLMDefaulter(newLLMFakeClient(hwpA, hwpB))

			oldLLM := buildLLMISVCHWP(hwptestutil.HWPAnnotationsWithNS("hwp-a", llmHWPNS),
				map[string]string{hardwareprofile.KueueQueueNameLabel: "queue-a"})
			newLLM := buildLLMISVCHWP(hwptestutil.HWPAnnotationsWithNS("hwp-b", llmHWPNS),
				map[string]string{hardwareprofile.KueueQueueNameLabel: "queue-a"}) // carried over

			Expect(d.Default(llmHWPUpdateCtx(newLLM, oldLLM), newLLM)).To(Succeed())
			Expect(newLLM.Labels[hardwareprofile.KueueQueueNameLabel]).To(Equal("queue-b"))
		})

		It("LLM-19: annotation removed, nodeSelector value differs from old HWP — key preserved", func() {
			hwpA := hwptestutil.NewHardwareProfile("hwp-a", llmHWPNS,
				hwptestutil.NodeSpec(map[string]interface{}{"zone": "eu-west"}, nil))
			d := newLLMDefaulter(newLLMFakeClient(hwpA))

			oldLLM := buildLLMISVCHWP(hwptestutil.HWPAnnotationsWithNS("hwp-a", llmHWPNS), nil)
			oldLLM.Spec.Template = &corev1.PodSpec{
				NodeSelector: map[string]string{"zone": "eu-west"},
			}

			newLLM := buildLLMISVCHWP(nil, nil) // annotation removed
			newLLM.Annotations = map[string]string{
				hardwareprofile.HardwareProfileAnnotationNamespace: llmHWPNS,
			}
			newLLM.Spec.Template = &corev1.PodSpec{
				NodeSelector: map[string]string{"zone": "us-east"}, // user-changed value
			}

			Expect(d.Default(llmHWPUpdateCtx(newLLM, oldLLM), newLLM)).To(Succeed())
			// Value differs from HWP record — key preserved.
			Expect(newLLM.Spec.Template.NodeSelector).To(HaveKeyWithValue("zone", "us-east"))
		})

		It("LLM-20: annotation removed, LLMIsvc has no HWP nodeSelector entries — no-op, no panic", func() {
			hwpA := hwptestutil.NewHardwareProfile("hwp-a", llmHWPNS,
				hwptestutil.NodeSpec(map[string]interface{}{"zone": "eu-west"}, nil))
			d := newLLMDefaulter(newLLMFakeClient(hwpA))

			oldLLM := buildLLMISVCHWP(hwptestutil.HWPAnnotationsWithNS("hwp-a", llmHWPNS), nil)
			oldLLM.Spec.Template = &corev1.PodSpec{
				NodeSelector: map[string]string{"zone": "eu-west"},
			}

			newLLM := buildLLMISVCHWP(nil, nil) // annotation removed; user already cleaned stanzas
			newLLM.Annotations = map[string]string{
				hardwareprofile.HardwareProfileAnnotationNamespace: llmHWPNS,
			}
			newLLM.Spec.Template = &corev1.PodSpec{}

			Expect(d.Default(llmHWPUpdateCtx(newLLM, oldLLM), newLLM)).To(Succeed())
			Expect(newLLM.Spec.Template.NodeSelector).To(BeNil())
		})

		It("LLM-21: annotation removed, user overwrote Kueue label — label removed unconditionally", func() {
			hwpA := hwptestutil.NewHardwareProfile("hwp-a", llmHWPNS, hwptestutil.KueueSpec("hwp-queue"))
			d := newLLMDefaulter(newLLMFakeClient(hwpA))

			oldLLM := buildLLMISVCHWP(hwptestutil.HWPAnnotationsWithNS("hwp-a", llmHWPNS),
				map[string]string{hardwareprofile.KueueQueueNameLabel: "hwp-queue"})
			newLLM := buildLLMISVCHWP(nil,
				map[string]string{hardwareprofile.KueueQueueNameLabel: "user-queue"}) // user changed it
			newLLM.Annotations = map[string]string{
				hardwareprofile.HardwareProfileAnnotationNamespace: llmHWPNS,
			}

			Expect(d.Default(llmHWPUpdateCtx(newLLM, oldLLM), newLLM)).To(Succeed())
			Expect(newLLM.Labels).NotTo(HaveKey(hardwareprofile.KueueQueueNameLabel))
		})

		It("LLM-22: annotation removed, no Kueue label present — no-op, no panic", func() {
			hwpA := hwptestutil.NewHardwareProfile("hwp-a", llmHWPNS,
				hwptestutil.NodeSpec(map[string]interface{}{"zone": "eu-west"}, nil)) // node scheduling only
			d := newLLMDefaulter(newLLMFakeClient(hwpA))

			oldLLM := buildLLMISVCHWP(hwptestutil.HWPAnnotationsWithNS("hwp-a", llmHWPNS), nil)
			oldLLM.Spec.Template = &corev1.PodSpec{
				NodeSelector: map[string]string{"zone": "eu-west"},
			}

			newLLM := buildLLMISVCHWP(nil, nil) // annotation removed, no Kueue label
			newLLM.Annotations = map[string]string{
				hardwareprofile.HardwareProfileAnnotationNamespace: llmHWPNS,
			}
			newLLM.Spec.Template = &corev1.PodSpec{
				NodeSelector: map[string]string{"zone": "eu-west"},
			}

			Expect(d.Default(llmHWPUpdateCtx(newLLM, oldLLM), newLLM)).To(Succeed())
			Expect(newLLM.Labels).NotTo(HaveKey(hardwareprofile.KueueQueueNameLabel))
		})
	})
})
