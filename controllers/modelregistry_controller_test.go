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
	"github.com/opendatahub-io/model-registry/pkg/openapi"
	"github.com/stretchr/testify/mock"
	"k8s.io/apimachinery/pkg/types"
)

var (
	// mr content
	inferenceServiceName = "dummy-inference-service"
	// filled at runtime
	servingRuntime = &kservev1alpha1.ServingRuntime{}
)

var (
	// ids
	servingEnvironmentId = "1"
	registeredModelId    = "2"
	inferenceServiceId   = "4"
)

var _ = Describe("ModelRegistry controller", func() {
	ctx := context.Background()

	BeforeEach(func() {
		servingRuntime = &kservev1alpha1.ServingRuntime{}
		err := convertToStructuredResource(KserveServingRuntimePath1, servingRuntime)
		Expect(err).NotTo(HaveOccurred())
		Expect(cli.Create(ctx, servingRuntime)).Should(Succeed())
		resetCalls()
	})

	When("when a ServingRuntime is applied in WorkingNamespace", func() {
		It("should create an InferenceService CR when model registry state is DEPLOYED", func() {
			// simulate existing IS in model registry in DEPLOYED state for all calls
			modelRegistryMock.On("GetInferenceServices").Return(&openapi.InferenceServiceList{
				PageSize: 1,
				Size:     1,
				Items: []openapi.InferenceService{
					{
						Id:                   &inferenceServiceId,
						Name:                 &inferenceServiceName,
						RegisteredModelId:    registeredModelId,
						ServingEnvironmentId: servingEnvironmentId,
						Runtime:              &servingRuntime.Name,
						DesiredState:         openapi.INFERENCESERVICESTATE_DEPLOYED.Ptr(),
					},
				},
			}, nil)

			isvc := &kservev1beta1.InferenceService{}
			Eventually(func() error {
				key := types.NamespacedName{Name: inferenceServiceName, Namespace: WorkingNamespace}
				return cli.Get(ctx, key, isvc)
			}, timeout, interval).ShouldNot(HaveOccurred())
		})

		It("should delete InferenceService CR when model registry state is UNDEPLOYED", func() {
			// simulate existing IS in model registry in UNDEPLOYED state for all calls
			modelRegistryMock.On("GetInferenceServices").Return(&openapi.InferenceServiceList{
				PageSize: 1,
				Size:     1,
				Items: []openapi.InferenceService{
					{
						Id:                   &inferenceServiceId,
						Name:                 &inferenceServiceName,
						RegisteredModelId:    registeredModelId,
						ServingEnvironmentId: servingEnvironmentId,
						Runtime:              &servingRuntime.Name,
						DesiredState:         openapi.INFERENCESERVICESTATE_UNDEPLOYED.Ptr(),
					},
				},
			}, nil)

			// create an existing ISVC
			inferenceService := &kservev1beta1.InferenceService{}
			err := convertToStructuredResource(ModelRegistryInferenceServicePath1, inferenceService)
			Expect(err).NotTo(HaveOccurred())
			Expect(cli.Create(ctx, inferenceService)).Should(Succeed())
			By("by checking that the InferenceService CR is in WorkingNamespace")
			isvc := &kservev1beta1.InferenceService{}
			Eventually(func() error {
				key := types.NamespacedName{Name: inferenceServiceName, Namespace: WorkingNamespace}
				return cli.Get(ctx, key, isvc)
			}, timeout, interval).ShouldNot(HaveOccurred())

			By("by checking that the controller has removed the InferenceService CR from WorkingNamespace")
			isvc = &kservev1beta1.InferenceService{}
			Eventually(func() error {
				key := types.NamespacedName{Name: inferenceServiceName, Namespace: WorkingNamespace}
				return cli.Get(ctx, key, isvc)
			}, timeout, interval).Should(HaveOccurred())
		})

		It("should update InferenceService CR when model registry state is DEPLOYED and there is delta", func() {
			// simulate existing IS in model registry in DEPLOYED state for all other calls
			modelRegistryMock.On("GetInferenceServices").Return(&openapi.InferenceServiceList{
				PageSize: 1,
				Size:     1,
				Items: []openapi.InferenceService{
					{
						Id:                   &inferenceServiceId,
						Name:                 &inferenceServiceName,
						RegisteredModelId:    registeredModelId,
						ServingEnvironmentId: servingEnvironmentId,
						Runtime:              &servingRuntime.Name,
						DesiredState:         openapi.INFERENCESERVICESTATE_DEPLOYED.Ptr(),
					},
				},
			}, nil)

			// create an existing ISVC
			inferenceService := &kservev1beta1.InferenceService{}
			// storage path is /path/to/old/model
			err := convertToStructuredResource(ModelRegistryInferenceServicePath1, inferenceService)
			Expect(err).NotTo(HaveOccurred())
			Expect(cli.Create(ctx, inferenceService)).Should(Succeed())

			By("by checking that the controller has correctly updated the InferenceService CR storage path")
			isvc := &kservev1beta1.InferenceService{}
			Eventually(func() string {
				key := types.NamespacedName{Name: inferenceServiceName, Namespace: WorkingNamespace}
				err := cli.Get(ctx, key, isvc)
				if err != nil {
					return ""
				}
				return *isvc.Spec.Predictor.Model.Storage.Path
			}, timeout, interval).Should(Equal("path/to/model"))
		})
	})
})

// resetCalls reset expected and existing calls on mocked model registry
// this is a workaround as this functionality is not exported at the moment
func resetCalls() {
	modelRegistryMock.ExpectedCalls = []*mock.Call{}
	modelRegistryMock.Calls = []mock.Call{}
}
