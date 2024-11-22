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

package controllers

import (
	"encoding/json"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/opendatahub-io/odh-model-controller/controllers/utils"
	"github.com/opendatahub-io/odh-model-controller/controllers/webhook"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var _ = Describe("Inference Service validator webhook", func() {
	var validator *webhook.IsvcValidator
	var meshNamespace, appsNamespace string

	createInferenceService := func(namespace, name string) *kservev1beta1.InferenceService {
		inferenceService := &kservev1beta1.InferenceService{}
		err := convertToStructuredResource(KserveInferenceServicePath1, inferenceService)
		Expect(err).NotTo(HaveOccurred())
		inferenceService.SetNamespace(namespace)
		if len(name) != 0 {
			inferenceService.Name = name
		}
		Expect(cli.Create(ctx, inferenceService)).Should(Succeed())
		return inferenceService
	}

	BeforeEach(func() {
		_, meshNamespace = utils.GetIstioControlPlaneName(ctx, cli)
		appsNamespace, _ = utils.GetApplicationNamespace(ctx, cli)
		validator = webhook.NewIsvcValidator(cli, admission.NewDecoder(cli.Scheme()))
	})

	It("Should allow the Inference Service in the test-model namespace", func() {
		testNs := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-model",
				Namespace: "test-model",
			},
		}
		Expect(cli.Create(ctx, testNs)).Should(Succeed())

		isvc := createInferenceService(testNs.Name, "test-isvc")
		isvcBytes, err := json.Marshal(isvc)
		Expect(err).NotTo(HaveOccurred())
		response := validator.Handle(ctx, admission.Request{
			AdmissionRequest: admissionv1.AdmissionRequest{Namespace: testNs.Name, Name: "test-isvc",
				Object: runtime.RawExtension{Raw: isvcBytes}}})
		Expect(response.Allowed).To(BeTrue())
	})

	It("Should not allow the Inference Service in the ServiceMesh namespace", func() {
		isvc := createInferenceService(meshNamespace, "test-isvc")
		isvcBytes, err := json.Marshal(isvc)
		Expect(err).NotTo(HaveOccurred())
		response := validator.Handle(ctx, admission.Request{
			AdmissionRequest: admissionv1.AdmissionRequest{Namespace: meshNamespace, Name: "test-isvc",
				Object: runtime.RawExtension{Raw: isvcBytes}}})
		Expect(response.Allowed).To(BeFalse())
	})

	It("Should not allow the Inference Service in the ApplicationsNamespace namespace", func() {
		isvc := createInferenceService(appsNamespace, "test-isvc")
		isvcBytes, err := json.Marshal(isvc)
		Expect(err).NotTo(HaveOccurred())
		response := validator.Handle(ctx, admission.Request{
			AdmissionRequest: admissionv1.AdmissionRequest{Namespace: appsNamespace, Name: "test-isvc",
				Object: runtime.RawExtension{Raw: isvcBytes}}})
		Expect(response.Allowed).To(BeFalse())
	})

	It("Should not allow the Inference Service in the knative-serving namespace", func() {
		testNs := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "knative-serving",
				Namespace: "knative-serving",
			},
		}
		Expect(cli.Create(ctx, testNs)).Should(Succeed())
		isvc := createInferenceService(testNs.Name, "test-isvc")
		isvcBytes, err := json.Marshal(isvc)
		Expect(err).NotTo(HaveOccurred())
		response := validator.Handle(ctx, admission.Request{
			AdmissionRequest: admissionv1.AdmissionRequest{Namespace: testNs.Name, Name: "test-isvc",
				Object: runtime.RawExtension{Raw: isvcBytes}}})
		Expect(response.Allowed).To(BeFalse())
	})
})
