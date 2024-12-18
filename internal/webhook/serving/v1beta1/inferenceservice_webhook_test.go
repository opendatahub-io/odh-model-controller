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
	"github.com/kserve/kserve/pkg/constants"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
	testutils "github.com/opendatahub-io/odh-model-controller/test/utils"
)

const InferenceServiceConfigPath1 = "../../../controller/serving/testdata/configmaps/inferenceservice-config.yaml"

var _ = Describe("InferenceService validator Webhook", func() {
	var meshNamespace, appsNamespace string

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
		_, meshNamespace = utils.GetIstioControlPlaneName(ctx, k8sClient)
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

		It("Should not allow the Inference Service in the ServiceMesh namespace", func() {
			isvc := createInferenceService(meshNamespace, "test-isvc")
			_, err := validator.ValidateCreate(ctx, isvc)
			Expect(err).To(HaveOccurred())
		})

		It("Should not allow the Inference Service in the ApplicationsNamespace namespace", func() {
			isvc := createInferenceService(appsNamespace, "test-isvc")
			_, err := validator.ValidateCreate(ctx, isvc)
			Expect(err).To(HaveOccurred())
		})

		It("Should not allow the Inference Service in the knative-serving namespace", func() {
			testNs := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "knative-serving",
					Namespace: "knative-serving",
				},
			}
			Expect(k8sClient.Create(ctx, testNs)).Should(Succeed())
			isvc := createInferenceService(testNs.Name, "test-isvc")
			_, err := validator.ValidateCreate(ctx, isvc)
			Expect(err).To(HaveOccurred())
		})
	})

	Context("When creating InferenceService under Defaulting Webhook", func() {
		var defaulter InferenceServiceCustomDefaulter

		BeforeEach(func() {
			defaulter = InferenceServiceCustomDefaulter{client: k8sClient}

			inferenceServiceConfig := &corev1.ConfigMap{}
			Expect(testutils.ConvertToStructuredResource(InferenceServiceConfigPath1, inferenceServiceConfig)).To(Succeed())
			if err := k8sClient.Create(ctx, inferenceServiceConfig); err != nil && !k8sErrors.IsAlreadyExists(err) {
				Fail(err.Error())
			}
		})

		It("Should apply default annotations for Serverless mode", func() {
			isvc := kservev1beta1.InferenceService{}
			Expect(defaulter.Default(ctx, &isvc)).To(Succeed())

			annotations := isvc.GetAnnotations()
			Expect(annotations).To(HaveKeyWithValue("serving.knative.openshift.io/enablePassthrough", "true"))
			Expect(annotations).To(HaveKeyWithValue("sidecar.istio.io/inject", "true"))
			Expect(annotations).To(HaveKeyWithValue("sidecar.istio.io/rewriteAppHTTPProbers", "true"))
		})

		It("Should keep user-specified annotations for Serverless mode", func() {
			isvc := kservev1beta1.InferenceService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-isvc",
					Namespace: "test-isvc",
					Annotations: map[string]string{
						"serving.knative.openshift.io/enablePassthrough": "false",
						"sidecar.istio.io/rewriteAppHTTPProbers":         "false",
						"sidecar.istio.io/inject":                        "false",
					},
				},
			}
			Expect(defaulter.Default(ctx, &isvc)).To(Succeed())

			annotations := isvc.GetAnnotations()
			Expect(annotations).To(HaveKeyWithValue("serving.knative.openshift.io/enablePassthrough", "false"))
			Expect(annotations).To(HaveKeyWithValue("sidecar.istio.io/inject", "false"))
			Expect(annotations).To(HaveKeyWithValue("sidecar.istio.io/rewriteAppHTTPProbers", "false"))
		})

		It("Should not apply default annotations for Raw mode specified in annotation", func() {
			isvc := kservev1beta1.InferenceService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-isvc",
					Namespace: "test-isvc",
					Annotations: map[string]string{
						constants.DeploymentMode: string(constants.RawDeployment),
					},
				},
			}
			Expect(defaulter.Default(ctx, &isvc)).To(Succeed())

			annotations := isvc.GetAnnotations()
			Expect(annotations).ToNot(HaveKey("serving.knative.openshift.io/enablePassthrough"))
			Expect(annotations).ToNot(HaveKey("sidecar.istio.io/inject"))
			Expect(annotations).ToNot(HaveKey("sidecar.istio.io/rewriteAppHTTPProbers"))
		})

		It("Should not apply default annotations for Raw mode specified in default configuration", func() {
			inferenceServiceConfig := &corev1.ConfigMap{}
			Expect(testutils.ConvertToStructuredResource(InferenceServiceConfigPath1, inferenceServiceConfig)).To(Succeed())
			inferenceServiceConfig.Data["deploy"] = "{\"defaultDeploymentMode\": \"RawDeployment\"}"
			Expect(k8sClient.Update(ctx, inferenceServiceConfig)).To(Succeed())

			isvc := kservev1beta1.InferenceService{}
			Expect(defaulter.Default(ctx, &isvc)).To(Succeed())

			annotations := isvc.GetAnnotations()
			Expect(annotations).ToNot(HaveKey("serving.knative.openshift.io/enablePassthrough"))
			Expect(annotations).ToNot(HaveKey("sidecar.istio.io/inject"))
			Expect(annotations).ToNot(HaveKey("sidecar.istio.io/rewriteAppHTTPProbers"))
		})
	})
})
