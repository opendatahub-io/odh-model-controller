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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	gomegatypes "github.com/onsi/gomega/types"
	"github.com/opendatahub-io/odh-model-controller/controllers/constants"
	routev1 "github.com/openshift/api/route/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"knative.dev/pkg/apis"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
)

var _ = Describe("The Openshift Kserve model controller", func() {

	When("creating a Kserve ServiceRuntime & InferenceService", func() {
		var testNs string

		BeforeEach(func() {
			ctx := context.Background()
			testNs = appendRandomNameTo("test-namespace")
			testNamespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testNs,
					Namespace: testNs,
				},
			}
			Expect(cli.Create(ctx, testNamespace)).Should(Succeed())

			inferenceServiceConfig := &corev1.ConfigMap{}
			Expect(convertToStructuredResource(InferenceServiceConfigPath1, inferenceServiceConfig)).To(Succeed())
			if err := cli.Create(ctx, inferenceServiceConfig); err != nil && !errors.IsAlreadyExists(err) {
				Fail(err.Error())
			}

			servingRuntime := &kservev1alpha1.ServingRuntime{}
			Expect(convertToStructuredResource(KserveServingRuntimePath1, servingRuntime)).To(Succeed())
			if err := cli.Create(ctx, servingRuntime); err != nil && !errors.IsAlreadyExists(err) {
				Fail(err.Error())
			}
		})

		It("With Kserve InferenceService a Route be created", func() {
			inferenceService := &kservev1beta1.InferenceService{}
			err := convertToStructuredResource(KserveInferenceServicePath1, inferenceService)
			Expect(err).NotTo(HaveOccurred())
			inferenceService.SetNamespace(testNs)

			Expect(cli.Create(ctx, inferenceService)).Should(Succeed())

			By("By checking that the controller has not created the Route")
			Consistently(func() error {
				route := &routev1.Route{}
				key := types.NamespacedName{Name: getKServeRouteName(inferenceService), Namespace: constants.IstioNamespace}
				err = cli.Get(ctx, key, route)
				return err
			}, timeout, interval).Should(HaveOccurred())

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
			}, timeout, interval).Should(Succeed())
		})

		It("should create required network policies when KServe is used", func() {
			// given
			inferenceService := &kservev1beta1.InferenceService{}
			Expect(convertToStructuredResource(KserveInferenceServicePath1, inferenceService)).To(Succeed())
			inferenceService.SetNamespace(testNs)

			// when
			Expect(cli.Create(ctx, inferenceService)).Should(Succeed())

			// then
			By("ensuring that the controller has created required network policies")
			networkPolicies := &v1.NetworkPolicyList{}
			Eventually(func() []v1.NetworkPolicy {
				err := cli.List(ctx, networkPolicies, client.InNamespace(inferenceService.Namespace))
				if err != nil {
					Fail(err.Error())
				}
				return networkPolicies.Items
			}, timeout, interval).Should(
				ContainElements(
					withMatchingNestedField("ObjectMeta.Name", Equal("allow-from-openshift-monitoring-ns")),
					withMatchingNestedField("ObjectMeta.Name", Equal("allow-openshift-ingress")),
					withMatchingNestedField("ObjectMeta.Name", Equal("allow-from-opendatahub-ns")),
				),
			)
		})

	})
})

func withMatchingNestedField(path string, matcher gomegatypes.GomegaMatcher) gomegatypes.GomegaMatcher {
	if path == "" {
		Fail("cannot handle empty path")
	}

	fields := strings.Split(path, ".")

	// Reverse the path, so we start composing matchers from the leaf up
	for i, j := 0, len(fields)-1; i < j; i, j = i+1, j-1 {
		fields[i], fields[j] = fields[j], fields[i]
	}

	matchFields := MatchFields(IgnoreExtras,
		Fields{fields[0]: matcher},
	)

	for i := 1; i < len(fields); i++ {
		matchFields = MatchFields(IgnoreExtras, Fields{fields[i]: matchFields})
	}

	return matchFields
}

func getKServeRouteName(isvc *kservev1beta1.InferenceService) string {
	return isvc.Name + "-" + isvc.Namespace
}
