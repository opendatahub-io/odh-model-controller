/*
Copyright 2024.

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
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
	testutils "github.com/opendatahub-io/odh-model-controller/test/utils"
)

var _ = Describe("InferenceService validator Webhook", func() {
	var appsNamespace string

	createInferenceService := func(namespace, name string) *kservev1beta1.InferenceService {
		inferenceService := &kservev1beta1.InferenceService{}
		err := testutils.ConvertToStructuredResource(KserveInferenceServicePath1, inferenceService)
		Expect(err).NotTo(HaveOccurred())
		inferenceService.SetNamespace(namespace)
		if len(name) != 0 {
			inferenceService.Name = name
		}
		return inferenceService
	}

	BeforeEach(func() {
		appsNamespace, _ = utils.GetApplicationNamespace(ctx, k8sClient)
	})

	Context("When creating or updating InferenceService under Validating Webhook", func() {
		var validator InferenceServiceCustomValidator

		BeforeEach(func() {
			validator = InferenceServiceCustomValidator{client: k8sClient}
		})

		It("Should allow the Inference Service in the test-model namespace", func() {
			testNs := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-model",
					Namespace: "test-model",
				},
			}
			Expect(k8sClient.Create(ctx, testNs)).Should(Succeed())

			isvc := createInferenceService(testNs.Name, "test-isvc")
			_, err := validator.ValidateCreate(ctx, isvc)
			Expect(err).ShouldNot(HaveOccurred())
		})

		It("Should not allow the Inference Service in the ApplicationsNamespace namespace", func() {
			isvc := createInferenceService(appsNamespace, "test-isvc")
			_, err := validator.ValidateCreate(ctx, isvc)
			Expect(err).To(HaveOccurred())
		})

		It("Should reject InferenceService with name that exceeds 63 characters when combined with -predictor suffix", func() {
			testNs := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-long-name",
					Namespace: "test-long-name",
				},
			}
			Expect(k8sClient.Create(ctx, testNs)).Should(Succeed())

			// Name with 54 characters + "-predictor" (10 chars) = 64 characters (exceeds 63)
			// Using a prefix and padding with 'x' to reach exactly 54 characters
			longName := "test-isvc-" + "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"[:44] // 10 + 44 = 54

			isvc := createInferenceService(testNs.Name, longName)
			_, err := validator.ValidateCreate(ctx, isvc)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("InferenceService name is too long"))
		})

		It("Should accept InferenceService with name at exactly 63 characters when combined with -predictor suffix", func() {
			testNs := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-max-name",
					Namespace: "test-max-name",
				},
			}
			Expect(k8sClient.Create(ctx, testNs)).Should(Succeed())

			// Name with 53 characters + "-predictor" (10 chars) = 63 characters (exactly at limit)
			// Using a prefix and padding with 'x' to reach exactly 53 characters
			maxName := "test-isvc-" + "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"[:43] // 10 + 43 = 53

			isvc := createInferenceService(testNs.Name, maxName)
			_, err := validator.ValidateCreate(ctx, isvc)
			Expect(err).ShouldNot(HaveOccurred())
		})

		It("Should accept InferenceService with short name", func() {
			testNs := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-short-name",
					Namespace: "test-short-name",
				},
			}
			Expect(k8sClient.Create(ctx, testNs)).Should(Succeed())

			isvc := createInferenceService(testNs.Name, "short-name")
			_, err := validator.ValidateCreate(ctx, isvc)
			Expect(err).ShouldNot(HaveOccurred())
		})
	})
})
