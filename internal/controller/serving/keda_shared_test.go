/*

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

package serving

import (
	kedaapi "github.com/kedacore/keda/v2/apis/keda/v1alpha1"
	kservev1alpha2 "github.com/kserve/kserve/pkg/apis/serving/v1alpha2"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apiserver/pkg/storage/names"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/serving/reconcilers"
	. "github.com/opendatahub-io/odh-model-controller/test/matchers"
	testutils "github.com/opendatahub-io/odh-model-controller/test/utils"
)

// These specs cover the KEDA Prometheus authentication resources (ServiceAccount, Secret, Role, RoleBinding,
// TriggerAuthentication) shared between InferenceService (via KserveKEDAReconciler) and LLMInferenceService
// (via LLMKEDAReconciler). Both reconcilers are exercised directly against the shared, uncached k8sClient,
// mirroring the "KServe KEDA Reconciler" specs in inferenceservice_controller_test.go.
var _ = Describe("KEDA Prometheus auth resources shared across InferenceService and LLMInferenceService", func() {
	var (
		testNs         string
		kedaReconciler *reconcilers.KserveKEDAReconciler
		llmReconciler  *reconcilers.LLMKEDAReconciler
	)

	BeforeEach(func(ctx SpecContext) {
		testNs = testutils.Namespaces.Create(ctx, k8sClient).Name
		kedaReconciler = reconcilers.NewKServeKEDAReconciler(k8sClient)
		llmReconciler = reconcilers.NewLLMKEDAReconciler(k8sClient)
	})

	Context("when only an LLMInferenceService uses a direct KEDA Prometheus trigger", func() {
		var llmisvc *kservev1alpha2.LLMInferenceService

		BeforeEach(func(ctx SpecContext) {
			llmisvc = makeKedaTestLLMISVC(testNs, names.SimpleNameGenerator.GenerateName("keda-llmisvc"), true, false)
			Expect(k8sClient.Create(ctx, llmisvc)).Should(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(llmisvc), llmisvc)).Should(Succeed())
		})

		It("creates the shared auth resources owned by the LLMInferenceService", func() {
			Expect(llmReconciler.Reconcile(ctx, GinkgoLogr, llmisvc)).To(Succeed())

			for _, obj := range getAllKedaTestResources(ctx, k8sClient, testNs) {
				Expect(obj).To(Not(BeNil()))
				Expect(obj).To(HaveOwnerReferenceByUID(llmisvc.UID))
			}
		})

		It("removes the owner reference and cleans up when the trigger is removed", func() {
			Expect(llmReconciler.Reconcile(ctx, GinkgoLogr, llmisvc)).To(Succeed())

			// Persist the spec change to the server: cleanupNamespaceIfUnused re-lists LLMInferenceServices from
			// the server (not from the in-memory llmisvc), so the update must actually be written.
			noLongerScaling := makeKedaTestLLMISVC(testNs, llmisvc.Name, false, false)
			llmisvc.Spec = noLongerScaling.Spec
			Expect(k8sClient.Update(ctx, llmisvc)).Should(Succeed())
			Expect(llmReconciler.Reconcile(ctx, GinkgoLogr, llmisvc)).To(Succeed())

			sa := &corev1.ServiceAccount{}
			err := k8sClient.Get(ctx, types.NamespacedName{Name: reconcilers.KEDAPrometheusAuthServiceAccountName, Namespace: testNs}, sa)
			Expect(err).To(HaveOccurred())
			Expect(k8sErrors.IsNotFound(err)).To(BeTrue())
		})

		It("detects a Prometheus trigger declared on the disaggregated prefill workload too", func() {
			llmisvc = makeKedaTestLLMISVC(testNs, names.SimpleNameGenerator.GenerateName("keda-llmisvc-prefill"), false, true)
			Expect(k8sClient.Create(ctx, llmisvc)).Should(Succeed())

			Expect(llmReconciler.Reconcile(ctx, GinkgoLogr, llmisvc)).To(Succeed())

			for _, obj := range getAllKedaTestResources(ctx, k8sClient, testNs) {
				Expect(obj).To(Not(BeNil()))
				Expect(obj).To(HaveOwnerReferenceByUID(llmisvc.UID))
			}
		})
	})

	Context("when an InferenceService and an LLMInferenceService in the same namespace both use Prometheus KEDA", func() {
		var isvc *kservev1beta1.InferenceService
		var llmisvc *kservev1alpha2.LLMInferenceService

		BeforeEach(func(ctx SpecContext) {
			isvc = makeKedaTestISVC(testNs, names.SimpleNameGenerator.GenerateName("keda-isvc"), true)
			Expect(k8sClient.Create(ctx, isvc)).Should(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(isvc), isvc)).Should(Succeed())

			llmisvc = makeKedaTestLLMISVC(testNs, names.SimpleNameGenerator.GenerateName("keda-llmisvc"), true, false)
			Expect(k8sClient.Create(ctx, llmisvc)).Should(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(llmisvc), llmisvc)).Should(Succeed())

			Expect(kedaReconciler.Reconcile(ctx, GinkgoLogr, isvc)).To(Succeed())
			Expect(llmReconciler.Reconcile(ctx, GinkgoLogr, llmisvc)).To(Succeed())
		})

		It("co-owns a single shared set of auth resources", func() {
			for _, obj := range getAllKedaTestResources(ctx, k8sClient, testNs) {
				Expect(obj).To(Not(BeNil()))
				Expect(obj).To(HaveOwnerReferenceByUID(isvc.UID))
				Expect(obj).To(HaveOwnerReferenceByUID(llmisvc.UID))
			}
		})

		It("keeps the resources when only the InferenceService is deleted", func() {
			// Delete isvc from the server first (as the live InferenceService controller also observes and agrees
			// it's going away) before invoking the sub-reconciler's Delete() hook directly, so both converge on the
			// same outcome instead of racing (the live controller would otherwise keep re-adding the owner
			// reference, since isvc's own spec still declares the Prometheus KEDA metric).
			Expect(k8sClient.Delete(ctx, isvc)).Should(Succeed())
			Expect(kedaReconciler.Delete(ctx, GinkgoLogr, isvc)).To(Succeed())

			for _, obj := range getAllKedaTestResources(ctx, k8sClient, testNs) {
				Expect(obj).To(Not(BeNil()))
				Expect(obj).ToNot(HaveOwnerReferenceByUID(isvc.UID))
				Expect(obj).To(HaveOwnerReferenceByUID(llmisvc.UID))
			}
		})

		It("keeps the resources when only the LLMInferenceService is deleted", func() {
			Expect(k8sClient.Delete(ctx, llmisvc)).Should(Succeed())
			Expect(llmReconciler.Delete(ctx, GinkgoLogr, llmisvc)).To(Succeed())

			for _, obj := range getAllKedaTestResources(ctx, k8sClient, testNs) {
				Expect(obj).To(Not(BeNil()))
				Expect(obj).To(HaveOwnerReferenceByUID(isvc.UID))
				Expect(obj).ToNot(HaveOwnerReferenceByUID(llmisvc.UID))
			}
		})

		It("deletes the resources once both the InferenceService and LLMInferenceService are gone", func() {
			// Mirrors how the top-level controllers drive this in production: the owning object is deleted from
			// the server first (so cleanupNamespaceIfUnused's re-list no longer counts it), then the sub-reconciler's
			// Delete() hook runs as part of finalizer processing.
			Expect(k8sClient.Delete(ctx, isvc)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, llmisvc)).Should(Succeed())

			Expect(kedaReconciler.Delete(ctx, GinkgoLogr, isvc)).To(Succeed())
			Expect(llmReconciler.Delete(ctx, GinkgoLogr, llmisvc)).To(Succeed())

			Expect(getAllKedaTestResources(ctx, k8sClient, testNs)).To(BeEmpty())
		})

		It("does not delete the resources via LLMKEDAReconciler.Cleanup while the InferenceService still needs them", func() {
			// Simulates the top-level LLM controller calling Cleanup() once no LLMInferenceService remains in the
			// namespace, even though an InferenceService in that same namespace still requires the shared resources.
			Expect(llmReconciler.Cleanup(ctx, GinkgoLogr, testNs)).To(Succeed())

			for _, obj := range getAllKedaTestResources(ctx, k8sClient, testNs) {
				Expect(obj).To(Not(BeNil()))
				Expect(obj).To(HaveOwnerReferenceByUID(isvc.UID))
			}
		})

		It("does not delete the resources via KserveKEDAReconciler.Cleanup while the LLMInferenceService still needs them", func() {
			// Simulates the top-level ISVC controller calling Cleanup() once no InferenceService remains in the
			// namespace, even though an LLMInferenceService in that same namespace still requires the shared resources.
			Expect(kedaReconciler.Cleanup(ctx, GinkgoLogr, testNs)).To(Succeed())

			for _, obj := range getAllKedaTestResources(ctx, k8sClient, testNs) {
				Expect(obj).To(Not(BeNil()))
				Expect(obj).To(HaveOwnerReferenceByUID(llmisvc.UID))
			}
		})
	})

	Context("when an LLMInferenceService has only a non-Prometheus KEDA trigger", func() {
		var llmisvc *kservev1alpha2.LLMInferenceService

		BeforeEach(func(ctx SpecContext) {
			llmisvc = makeKedaTestLLMISVC(testNs, names.SimpleNameGenerator.GenerateName("keda-llmisvc-cpu"), false, false)
			llmisvc.Spec.WorkloadSpec.Scaling = &kservev1alpha2.ScalingSpec{
				MaxReplicas: 3,
				KEDA: &kservev1alpha2.DirectKEDAScalingSpec{
					Triggers: []kedaapi.ScaleTriggers{
						{
							Type:     "cpu",
							Metadata: map[string]string{"value": "80"},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, llmisvc)).Should(Succeed())
		})

		It("never adds an owner reference to the shared auth resources", func() {
			Expect(llmReconciler.Reconcile(ctx, GinkgoLogr, llmisvc)).To(Succeed())

			sa := &corev1.ServiceAccount{}
			err := k8sClient.Get(ctx, types.NamespacedName{Name: reconcilers.KEDAPrometheusAuthServiceAccountName, Namespace: testNs}, sa)
			Expect(err).To(HaveOccurred())
			Expect(k8sErrors.IsNotFound(err)).To(BeTrue())
		})
	})
})
