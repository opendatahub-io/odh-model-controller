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
	"context"
	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/opendatahub-io/odh-model-controller/controllers/constants"
	routev1 "github.com/openshift/api/route/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"knative.dev/pkg/apis"
	"time"
)

var _ = Describe("The Openshift Kserve model controller", func() {

	When("creating a Kserve ServiceRuntime & InferenceService", func() {
		BeforeEach(func() {
			ctx := context.Background()
			istioNamespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name:      constants.IstioNamespace,
					Namespace: constants.IstioNamespace,
				},
			}
			Expect(cli.Create(ctx, istioNamespace)).Should(Succeed())

			inferenceServiceConfig := &corev1.ConfigMap{}
			err := convertToStructuredResource(InferenceServiceConfigPath1, inferenceServiceConfig)
			Expect(err).NotTo(HaveOccurred())
			Expect(cli.Create(ctx, inferenceServiceConfig)).Should(Succeed())

			servingRuntime := &kservev1alpha1.ServingRuntime{}
			err = convertToStructuredResource(KserveServingRuntimePath1, servingRuntime)
			Expect(err).NotTo(HaveOccurred())
			Expect(cli.Create(ctx, servingRuntime)).Should(Succeed())

		})

		It("With Kserve InferenceService a Route be created", func() {
			inferenceService := &kservev1beta1.InferenceService{}
			err := convertToStructuredResource(KserveInferenceServicePath1, inferenceService)
			Expect(err).NotTo(HaveOccurred())
			Expect(cli.Create(ctx, inferenceService)).Should(Succeed())

			By("By checking that the controller has not created the Route")
			//time.Sleep(5000 * time.Millisecond)
			Consistently(func() error {
				route := &routev1.Route{}
				key := types.NamespacedName{Name: getKServeRouteName(inferenceService), Namespace: constants.IstioNamespace}
				err = cli.Get(ctx, key, route)
				return err
			}, time.Second*1, interval).Should(HaveOccurred())

			deployedInferenceService := &kservev1beta1.InferenceService{}
			err = cli.Get(ctx, types.NamespacedName{Name: inferenceService.Name, Namespace: inferenceService.Namespace}, deployedInferenceService)
			Expect(err).NotTo(HaveOccurred())

			url, err := apis.ParseURL("https://example-onnx-mnist-default.test.com")
			Expect(err).NotTo(HaveOccurred())
			deployedInferenceService.Status.URL = url

			err = cli.Status().Update(ctx, deployedInferenceService)
			Expect(err).NotTo(HaveOccurred())

			By("By checking that the controller has created the Route")
			Eventually(func() error {
				route := &routev1.Route{}
				key := types.NamespacedName{Name: getKServeRouteName(inferenceService), Namespace: constants.IstioNamespace}
				err = cli.Get(ctx, key, route)
				return err
			}, timeout, interval).ShouldNot(HaveOccurred())
		})
	})
})

func getKServeRouteName(isvc *kservev1beta1.InferenceService) string {
	return isvc.Name + "-" + isvc.Namespace
}
