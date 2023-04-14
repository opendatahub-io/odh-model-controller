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

	mmv1alpha1 "github.com/kserve/modelmesh-serving/apis/serving/v1alpha1"
	inferenceservicev1 "github.com/kserve/modelmesh-serving/apis/serving/v1beta1"
	routev1 "github.com/openshift/api/route/v1"
	virtualservicev1 "istio.io/client-go/pkg/apis/networking/v1alpha3"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	mfc "github.com/manifestival/controller-runtime-client"
	mf "github.com/manifestival/manifestival"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("The Openshift model controller", func() {

	Context("When creating a ServiceRuntime & InferenceService with 'enable-route' enabled", func() {
		var opts mf.Option
		var ctx context.Context
		var err error
		var namespace *corev1.Namespace
		var namespaceName string
		var inferenceService *inferenceservicev1.InferenceService

		// setup pr context in BeforeEach
		var namespacePath string

		JustBeforeEach(func() {
			namespaceName = "ns-" + RandStringRunes(5)
			opts = mf.UseClient(mfc.NewClient(cli))
			ctx = context.Background()

			namespace = &corev1.Namespace{}
			err = convertToStructuredResource(namespacePath, namespaceName, namespace, opts)
			Expect(err).NotTo(HaveOccurred())
			Expect(cli.Create(ctx, namespace)).Should(Succeed())

			servingRuntime := &mmv1alpha1.ServingRuntime{}
			err := convertToStructuredResource(ServingRuntimePath1, namespace.Name, servingRuntime, opts)
			Expect(err).NotTo(HaveOccurred())
			Expect(cli.Create(ctx, servingRuntime)).Should(Succeed())

			inferenceService = &inferenceservicev1.InferenceService{}
			err = convertToStructuredResource(InferenceService1, namespace.Name, inferenceService, opts)
			Expect(err).NotTo(HaveOccurred())
			Expect(cli.Create(ctx, inferenceService)).Should(Succeed())
		})
		Context("When Service Mesh is enabled in the namespace", func() {

			BeforeEach(func() {
				namespacePath = NamespaceServiceMeshPath1
			})

			It("Should create a VirtualService to expose the traffic externally via the istio ingress", func() {
				By("By checking that the controller has created the VirtualService")

				virtualService := &virtualservicev1.VirtualService{}
				Eventually(func() error {
					key := types.NamespacedName{Name: inferenceService.Name, Namespace: namespace.Name}
					return cli.Get(ctx, key, virtualService)
				}, timeout, interval).ShouldNot(HaveOccurred())

				expectedVirtualService := &virtualservicev1.VirtualService{}
				err = convertToStructuredResource(ExpectedVirtualServiceRoutePath, namespace.Name, expectedVirtualService, opts)
				Expect(err).NotTo(HaveOccurred())

				Expect(CompareInferenceServiceVirtualServices(virtualService, expectedVirtualService)).Should(BeTrue())
			})
			PIt("when InferenceService does not specifies a runtime, should automatically pick a runtime and create a Route", func() {
				// Rework test suite levels. Make equal between Route and VS versions.
				// Namespace (mesh or not)
				//  ServerRuntime (enable-route or not)
				//    InferenceService (with or without SA)
			})
		})

		Context("When Service Mesh is not enabled in the namespace", func() {

			BeforeEach(func() {
				namespacePath = NamespacePath1
			})

			It("Should create a Route to expose the traffic externally", func() {
				By("By checking that the controller has created the Route")

				route := &routev1.Route{}
				Eventually(func() error {
					key := types.NamespacedName{Name: inferenceService.Name, Namespace: namespace.Name}
					return cli.Get(ctx, key, route)
				}, timeout, interval).ShouldNot(HaveOccurred())

				expectedRoute := &routev1.Route{}
				err = convertToStructuredResource(ExpectedRoutePath, namespace.Name, expectedRoute, opts)
				Expect(err).NotTo(HaveOccurred())

				Expect(CompareInferenceServiceRoutes(*route, *expectedRoute)).Should(BeTrue())
			})

			It("when InferenceService does not specifies a runtime, should automatically pick a runtime and create a Route", func() {
				inferenceService := &inferenceservicev1.InferenceService{}
				err := convertToStructuredResource(InferenceServiceNoRuntime, namespace.Name, inferenceService, opts)
				Expect(err).NotTo(HaveOccurred())
				Expect(cli.Create(ctx, inferenceService)).Should(Succeed())

				route := &routev1.Route{}
				Eventually(func() error {
					key := types.NamespacedName{Name: inferenceService.Name, Namespace: inferenceService.Namespace}
					return cli.Get(ctx, key, route)
				}, timeout, interval).ShouldNot(HaveOccurred())

				expectedRoute := &routev1.Route{}
				err = convertToStructuredResource(ExpectedRouteNoRuntimePath, namespace.Name, expectedRoute, opts)
				Expect(err).NotTo(HaveOccurred())

				Expect(CompareInferenceServiceRoutes(*route, *expectedRoute)).Should(BeTrue())
			})
		})
	})

	Context("When creating a ServiceRuntime & InferenceService with 'enable-route' disabled", func() {
		var opts mf.Option
		var ctx context.Context
		var err error
		var namespace *corev1.Namespace
		var namespaceName string
		var inferenceService *inferenceservicev1.InferenceService

		// setup pr context in BeforeEach
		var namespacePath string

		JustBeforeEach(func() {
			namespaceName = "ns-" + RandStringRunes(5)
			opts = mf.UseClient(mfc.NewClient(cli))
			ctx = context.Background()

			namespace = &corev1.Namespace{}
			err = convertToStructuredResource(namespacePath, namespaceName, namespace, opts)
			Expect(err).NotTo(HaveOccurred())
			Expect(cli.Create(ctx, namespace)).Should(Succeed())

			servingRuntime := &mmv1alpha1.ServingRuntime{}
			err := convertToStructuredResource(ServingRuntimeNoRoutePath1, namespace.Name, servingRuntime, opts)
			Expect(err).NotTo(HaveOccurred())
			Expect(cli.Create(ctx, servingRuntime)).Should(Succeed())

			inferenceService = &inferenceservicev1.InferenceService{}
			err = convertToStructuredResource(InferenceService1, namespace.Name, inferenceService, opts)
			Expect(err).NotTo(HaveOccurred())
			Expect(cli.Create(ctx, inferenceService)).Should(Succeed())
		})

		Context("When Service Mesh is enabled in the namespace", func() {

			BeforeEach(func() {
				namespacePath = NamespaceServiceMeshPath1
			})

			It("Should create a VirtualService to control traffic internally", func() {
				By("By checking that the controller has created the VirtualService with no Gateway connected")

				virtualService := &virtualservicev1.VirtualService{}
				Eventually(func() error {
					key := types.NamespacedName{Name: inferenceService.Name, Namespace: namespace.Name}
					return cli.Get(ctx, key, virtualService)
				}, timeout, interval).ShouldNot(HaveOccurred())

				expectedVirtualService := &virtualservicev1.VirtualService{}
				err = convertToStructuredResource(ExpectedVirtualServiceNoRoutePath, namespace.Name, expectedVirtualService, opts)
				Expect(err).NotTo(HaveOccurred())

				Expect(CompareInferenceServiceVirtualServices(virtualService, expectedVirtualService)).Should(BeTrue())
			})
		})
	})
})
