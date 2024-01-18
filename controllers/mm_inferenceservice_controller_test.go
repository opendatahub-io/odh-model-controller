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
	"github.com/opendatahub-io/odh-model-controller/controllers/comparators"
	routev1 "github.com/openshift/api/route/v1"
	"k8s.io/apimachinery/pkg/types"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("The Openshift model controller", func() {

	When("creating a ServiceRuntime & InferenceService with 'enable-route' enabled", func() {

		BeforeEach(func() {
			ctx := context.Background()

			servingRuntime1 := &kservev1alpha1.ServingRuntime{}
			err := convertToStructuredResource(ServingRuntimePath1, servingRuntime1)
			Expect(err).NotTo(HaveOccurred())
			Expect(cli.Create(ctx, servingRuntime1)).Should(Succeed())

			servingRuntime2 := &kservev1alpha1.ServingRuntime{}
			err = convertToStructuredResource(ServingRuntimePath2, servingRuntime2)
			Expect(err).NotTo(HaveOccurred())
			Expect(cli.Create(ctx, servingRuntime2)).Should(Succeed())
		})

		It("when InferenceService specifies a runtime, should create a Route to expose the traffic externally", func() {
			inferenceService := &kservev1beta1.InferenceService{}
			err := convertToStructuredResource(InferenceService1, inferenceService)
			Expect(err).NotTo(HaveOccurred())
			Expect(cli.Create(ctx, inferenceService)).Should(Succeed())

			By("By checking that the controller has created the Route")

			route := &routev1.Route{}
			Eventually(func() error {
				key := types.NamespacedName{Name: inferenceService.Name, Namespace: inferenceService.Namespace}
				return cli.Get(ctx, key, route)
			}, timeout, interval).ShouldNot(HaveOccurred())

			expectedRoute := &routev1.Route{}
			err = convertToStructuredResource(ExpectedRoutePath, expectedRoute)
			Expect(err).NotTo(HaveOccurred())

			Expect(comparators.GetMMRouteComparator()(route, expectedRoute)).Should(BeTrue())
		})

		It("when InferenceService does not specifies a runtime, should automatically pick a runtime and create a Route", func() {
			inferenceService := &kservev1beta1.InferenceService{}
			err := convertToStructuredResource(InferenceServiceNoRuntime, inferenceService)
			Expect(err).NotTo(HaveOccurred())
			Expect(cli.Create(ctx, inferenceService)).Should(Succeed())

			route := &routev1.Route{}
			Eventually(func() error {
				key := types.NamespacedName{Name: inferenceService.Name, Namespace: inferenceService.Namespace}
				return cli.Get(ctx, key, route)
			}, timeout, interval).ShouldNot(HaveOccurred())

			expectedRoute := &routev1.Route{}
			err = convertToStructuredResource(ExpectedRouteNoRuntimePath, expectedRoute)
			Expect(err).NotTo(HaveOccurred())

			Expect(comparators.GetMMRouteComparator()(route, expectedRoute)).Should(BeTrue())
		})
	})
})
