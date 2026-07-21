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

package llm_test

import (
	"context"
	"time"

	kservev1alpha2 "github.com/kserve/kserve/pkg/apis/serving/v1alpha2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/serving/llm/fixture"
	testutils "github.com/opendatahub-io/odh-model-controller/test/utils"
)

var _ = Describe("Auth Posture Enforcement", func() {
	var testNs string

	BeforeEach(func(ctx SpecContext) {
		testNamespace := testutils.Namespaces.Create(ctx, envTest.Client)
		testNs = testNamespace.Name
	})

	AfterEach(func(ctx SpecContext) {
		llmList := &kservev1alpha2.LLMInferenceServiceList{}
		if err := envTest.Client.List(ctx, llmList, client.InNamespace(testNs)); err == nil {
			for i := range llmList.Items {
				_ = envTest.Client.Delete(ctx, &llmList.Items[i])
			}
		}
	})

	countAuthPostureMismatchEvents := func(ctx context.Context, namespace string) (int32, error) {
		eventList := &corev1.EventList{}
		if err := envTest.Client.List(ctx, eventList, client.InNamespace(namespace)); err != nil {
			return 0, err
		}
		var total int32
		for _, ev := range eventList.Items {
			if ev.Reason == "AuthPostureMismatch" {
				if ev.Count > 0 {
					total += ev.Count
				} else {
					total++
				}
			}
		}
		return total, nil
	}

	Context("routing group auth posture consistency", func() {
		It("should not emit events when two members have the same auth posture", func(ctx SpecContext) {
			svc1 := fixture.LLMInferenceService("llama-v1",
				fixture.InNamespace[*kservev1alpha2.LLMInferenceService](testNs),
				fixture.WithRoutingGroup("llama"),
				fixture.WithEnableAuth(true),
			)
			svc2 := fixture.LLMInferenceService("llama-v2",
				fixture.InNamespace[*kservev1alpha2.LLMInferenceService](testNs),
				fixture.WithRoutingGroup("llama"),
				fixture.WithEnableAuth(true),
			)
			Expect(envTest.Client.Create(ctx, svc1)).Should(Succeed())
			Expect(envTest.Client.Create(ctx, svc2)).Should(Succeed())

			Consistently(func(g Gomega) {
				count, err := countAuthPostureMismatchEvents(ctx, testNs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(count).To(BeZero())
			}).WithContext(ctx).
				WithTimeout(2 * time.Second).
				WithPolling(300 * time.Millisecond).
				Should(Succeed())
		})

		It("should emit Warning events when two members have different auth posture", func(ctx SpecContext) {
			svc1 := fixture.LLMInferenceService("llama-v1",
				fixture.InNamespace[*kservev1alpha2.LLMInferenceService](testNs),
				fixture.WithRoutingGroup("llama"),
				fixture.WithEnableAuth(true),
			)
			svc2 := fixture.LLMInferenceService("llama-v2",
				fixture.InNamespace[*kservev1alpha2.LLMInferenceService](testNs),
				fixture.WithRoutingGroup("llama"),
				fixture.WithEnableAuth(false),
			)
			Expect(envTest.Client.Create(ctx, svc1)).Should(Succeed())
			Expect(envTest.Client.Create(ctx, svc2)).Should(Succeed())

			Eventually(func(g Gomega) {
				count, err := countAuthPostureMismatchEvents(ctx, testNs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(count).To(BeNumerically(">=", 1))
			}).WithContext(ctx).Should(Succeed())
		})

		It("should not emit events for standalone services without a routing group", func(ctx SpecContext) {
			svc := fixture.LLMInferenceService("standalone",
				fixture.InNamespace[*kservev1alpha2.LLMInferenceService](testNs),
				fixture.WithEnableAuth(false),
			)
			Expect(envTest.Client.Create(ctx, svc)).Should(Succeed())

			Consistently(func(g Gomega) {
				count, err := countAuthPostureMismatchEvents(ctx, testNs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(count).To(BeZero())
			}).WithContext(ctx).
				WithTimeout(2 * time.Second).
				WithPolling(300 * time.Millisecond).
				Should(Succeed())
		})

		It("should emit Warning events when multiple members have mixed auth postures", func(ctx SpecContext) {
			for _, name := range []string{"llama-v1", "llama-v2", "llama-v3"} {
				svc := fixture.LLMInferenceService(name,
					fixture.InNamespace[*kservev1alpha2.LLMInferenceService](testNs),
					fixture.WithRoutingGroup("llama"),
					fixture.WithEnableAuth(true),
				)
				Expect(envTest.Client.Create(ctx, svc)).Should(Succeed())
			}
			svc4 := fixture.LLMInferenceService("llama-v4",
				fixture.InNamespace[*kservev1alpha2.LLMInferenceService](testNs),
				fixture.WithRoutingGroup("llama"),
				fixture.WithEnableAuth(false),
			)
			Expect(envTest.Client.Create(ctx, svc4)).Should(Succeed())

			Eventually(func(g Gomega) {
				count, err := countAuthPostureMismatchEvents(ctx, testNs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(count).To(BeNumerically(">=", 1))
			}).WithContext(ctx).Should(Succeed())
		})

		It("should stop emitting events when annotation is fixed to match", func(ctx SpecContext) {
			svc1 := fixture.LLMInferenceService("llama-v1",
				fixture.InNamespace[*kservev1alpha2.LLMInferenceService](testNs),
				fixture.WithRoutingGroup("llama"),
				fixture.WithEnableAuth(true),
			)
			svc2 := fixture.LLMInferenceService("llama-v2",
				fixture.InNamespace[*kservev1alpha2.LLMInferenceService](testNs),
				fixture.WithRoutingGroup("llama"),
				fixture.WithEnableAuth(false),
			)
			Expect(envTest.Client.Create(ctx, svc1)).Should(Succeed())
			Expect(envTest.Client.Create(ctx, svc2)).Should(Succeed())

			Eventually(func(g Gomega) {
				count, err := countAuthPostureMismatchEvents(ctx, testNs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(count).To(BeNumerically(">=", 1))
			}).WithContext(ctx).Should(Succeed())

			err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
				Expect(envTest.Client.Get(ctx, client.ObjectKeyFromObject(svc2), svc2)).To(Succeed())
				svc2.Annotations[constants.EnableAuthODHAnnotation] = "true"
				return envTest.Client.Update(ctx, svc2)
			})
			Expect(err).NotTo(HaveOccurred())

			snapshotCount, err := countAuthPostureMismatchEvents(ctx, testNs)
			Expect(err).NotTo(HaveOccurred())

			Consistently(func(g Gomega) {
				count, err := countAuthPostureMismatchEvents(ctx, testNs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(count).To(BeNumerically("<=", snapshotCount))
			}).WithContext(ctx).
				WithTimeout(2 * time.Second).
				WithPolling(300 * time.Millisecond).
				Should(Succeed())
		})

		It("should exclude terminating members from mismatch detection", func(ctx SpecContext) {
			svc1 := fixture.LLMInferenceService("llama-v1",
				fixture.InNamespace[*kservev1alpha2.LLMInferenceService](testNs),
				fixture.WithRoutingGroup("llama"),
				fixture.WithEnableAuth(true),
			)
			svc2 := fixture.LLMInferenceService("llama-v2",
				fixture.InNamespace[*kservev1alpha2.LLMInferenceService](testNs),
				fixture.WithRoutingGroup("llama"),
				fixture.WithEnableAuth(false),
			)
			Expect(envTest.Client.Create(ctx, svc1)).Should(Succeed())
			Expect(envTest.Client.Create(ctx, svc2)).Should(Succeed())

			Eventually(func(g Gomega) {
				count, err := countAuthPostureMismatchEvents(ctx, testNs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(count).To(BeNumerically(">=", 1))
			}).WithContext(ctx).Should(Succeed())

			// Mark svc2 with a deletion timestamp by setting a finalizer then deleting
			err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
				Expect(envTest.Client.Get(ctx, client.ObjectKeyFromObject(svc2), svc2)).To(Succeed())
				svc2.Finalizers = []string{"test.opendatahub.io/block-delete"}
				return envTest.Client.Update(ctx, svc2)
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(envTest.Client.Delete(ctx, svc2)).To(Succeed())

			// Verify svc2 is now terminating
			Eventually(func(g Gomega) {
				g.Expect(envTest.Client.Get(ctx, client.ObjectKeyFromObject(svc2), svc2)).To(Succeed())
				g.Expect(svc2.DeletionTimestamp.IsZero()).To(BeFalse())
			}).WithContext(ctx).Should(Succeed())

			// Trigger reconcile on svc1 by touching its annotations
			err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
				Expect(envTest.Client.Get(ctx, client.ObjectKeyFromObject(svc1), svc1)).To(Succeed())
				if svc1.Annotations == nil {
					svc1.Annotations = map[string]string{}
				}
				svc1.Annotations["test.opendatahub.io/trigger"] = metav1.Now().String()
				return envTest.Client.Update(ctx, svc1)
			})
			Expect(err).NotTo(HaveOccurred())

			snapshotCount, err := countAuthPostureMismatchEvents(ctx, testNs)
			Expect(err).NotTo(HaveOccurred())

			// With svc2 terminating, svc1 is the only active member - no mismatch possible
			Consistently(func(g Gomega) {
				count, err := countAuthPostureMismatchEvents(ctx, testNs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(count).To(BeNumerically("<=", snapshotCount))
			}).WithContext(ctx).
				WithTimeout(2 * time.Second).
				WithPolling(300 * time.Millisecond).
				Should(Succeed())

			// Clean up finalizer so envtest can fully delete
			Expect(envTest.Client.Get(ctx, client.ObjectKeyFromObject(svc2), svc2)).To(Succeed())
			svc2.Finalizers = nil
			Expect(envTest.Client.Update(ctx, svc2)).To(Succeed())
		})
	})
})
