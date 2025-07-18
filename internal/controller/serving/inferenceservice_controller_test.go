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

package serving

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	kedaapi "github.com/kedacore/keda/v2/apis/keda/v1alpha1"
	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	authorinov1beta2 "github.com/kuadrant/authorino/api/v1beta2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	gomegatypes "github.com/onsi/gomega/types"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/serving/reconcilers"
	routev1 "github.com/openshift/api/route/v1"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	istioclientv1beta1 "istio.io/client-go/pkg/apis/networking/v1beta1"
	istiosecv1b1 "istio.io/client-go/pkg/apis/security/v1beta1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apiserver/pkg/storage/names"
	"k8s.io/client-go/dynamic"
	"k8s.io/utils/ptr"
	"knative.dev/pkg/apis"
	duckv1 "knative.dev/pkg/apis/duck/v1"
	maistrav1 "maistra.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/comparators"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
	. "github.com/opendatahub-io/odh-model-controller/test/matchers"
	testutils "github.com/opendatahub-io/odh-model-controller/test/utils"
)

const (
	KserveOvmsInferenceServiceName         = "example-onnx-mnist"
	UnsupportedMetricsInferenceServiceName = "sklearn-v2-iris"
	NilRuntimeInferenceServiceName         = "sklearn-v2-iris-no-runtime"
	NilModelInferenceServiceName           = "custom-runtime"

	UnsupportedMetricsInferenceServicePath = "./testdata/deploy/kserve-unsupported-metrics-inference-service.yaml"
	UnsupprtedMetricsServingRuntimePath    = "./testdata/deploy/kserve-unsupported-metrics-serving-runtime.yaml"
	NilRuntimeInferenceServicePath         = "./testdata/deploy/kserve-nil-runtime-inference-service.yaml"
	NilModelInferenceServicePath           = "./testdata/deploy/kserve-nil-model-inference-service.yaml"
	testIsvcSvcPath                        = "./testdata/servingcert-service/test-isvc-svc.yaml"
	kserveLocalGatewayPath                 = "./testdata/gateway/kserve-local-gateway.yaml"
	testIsvcSvcSecretPath                  = "./testdata/gateway/test-isvc-svc-secret.yaml"
)

var _ = Describe("InferenceService Controller", func() {
	Describe("Openshift KServe integrations", func() {

		When("creating a Kserve ServiceRuntime & InferenceService", func() {
			var testNs string

			BeforeEach(func() {
				ctx := context.Background()
				testNamespace := testutils.Namespaces.Create(ctx, k8sClient)
				testNs = testNamespace.Name

				inferenceServiceConfig := &corev1.ConfigMap{}
				Expect(testutils.ConvertToStructuredResource(InferenceServiceConfigPath1, inferenceServiceConfig)).To(Succeed())
				if err := k8sClient.Create(ctx, inferenceServiceConfig); err != nil && !k8sErrors.IsAlreadyExists(err) {
					Fail(err.Error())
				}

				servingRuntime := &kservev1alpha1.ServingRuntime{}
				Expect(testutils.ConvertToStructuredResource(KserveServingRuntimePath1, servingRuntime)).To(Succeed())
				servingRuntime.SetNamespace(testNs)
				if err := k8sClient.Create(ctx, servingRuntime); err != nil && !k8sErrors.IsAlreadyExists(err) {
					Fail(err.Error())
				}
			})

			It("With Kserve InferenceService a Route be created", func() {
				_, meshNamespace := utils.GetIstioControlPlaneName(ctx, k8sClient)
				inferenceService := &kservev1beta1.InferenceService{}
				err := testutils.ConvertToStructuredResource(KserveInferenceServicePath1, inferenceService)
				Expect(err).NotTo(HaveOccurred())
				inferenceService.SetNamespace(testNs)

				Expect(k8sClient.Create(ctx, inferenceService)).Should(Succeed())

				By("By checking that the controller has not created the Route")
				Consistently(func() error {
					route := &routev1.Route{}
					key := types.NamespacedName{Name: getKServeRouteName(inferenceService), Namespace: meshNamespace}
					err = k8sClient.Get(ctx, key, route)
					return err
				}, timeout, interval).Should(HaveOccurred())

				deployedInferenceService := &kservev1beta1.InferenceService{}
				err = k8sClient.Get(ctx, types.NamespacedName{Name: inferenceService.Name, Namespace: inferenceService.Namespace}, deployedInferenceService)
				Expect(err).NotTo(HaveOccurred())

				url, err := apis.ParseURL("https://example-onnx-mnist-default.test.com")
				Expect(err).NotTo(HaveOccurred())
				deployedInferenceService.Status.URL = url

				err = k8sClient.Status().Update(ctx, deployedInferenceService)
				Expect(err).NotTo(HaveOccurred())

				By("By checking that the controller has created the Route")
				Eventually(func() error {
					route := &routev1.Route{}
					key := types.NamespacedName{Name: getKServeRouteName(inferenceService), Namespace: meshNamespace}
					err = k8sClient.Get(ctx, key, route)
					return err
				}, timeout, interval).Should(Succeed())
			})

			It("Should create a Route with custom timeout", func() {
				By("Creating an inference service with a timeout value defined in the component spec")
				_, meshNamespace := utils.GetIstioControlPlaneName(ctx, k8sClient)
				inferenceService := &kservev1beta1.InferenceService{}
				err := testutils.ConvertToStructuredResource(KserveInferenceServicePath2, inferenceService)
				Expect(err).NotTo(HaveOccurred())
				inferenceService.SetNamespace(testNs)
				Expect(k8sClient.Create(ctx, inferenceService)).Should(Succeed())
				// Update inference service status with url to create the route
				deployedInferenceService := &kservev1beta1.InferenceService{}
				err = k8sClient.Get(ctx, types.NamespacedName{Name: inferenceService.Name, Namespace: inferenceService.Namespace}, deployedInferenceService)
				Expect(err).NotTo(HaveOccurred())
				url, err := apis.ParseURL("https://example-onnx-mnist-timeout-default.test.com")
				Expect(err).NotTo(HaveOccurred())
				deployedInferenceService.Status.URL = url
				err = k8sClient.Status().Update(ctx, deployedInferenceService)
				Expect(err).NotTo(HaveOccurred())

				// Checking that the controller has created the Route with the haproxy.router.openshift.io/timeout annotation added
				Eventually(func() error {
					return checkRouteTimeout(types.NamespacedName{Name: getKServeRouteName(inferenceService), Namespace: meshNamespace}, "135s")
				}, timeout, interval).Should(Succeed())

				By("By updating an existing inference service with the haproxy.router.openshift.io/timeout annotation")
				updatedInferenceService := &kservev1beta1.InferenceService{}
				err = k8sClient.Get(ctx, types.NamespacedName{Name: inferenceService.Name, Namespace: inferenceService.Namespace}, updatedInferenceService)
				Expect(err).NotTo(HaveOccurred())
				updatedInferenceService.Annotations = make(map[string]string)
				updatedInferenceService.Annotations[constants.RouteTimeoutAnnotationKey] = "1m"
				err = k8sClient.Update(ctx, updatedInferenceService)
				Expect(err).NotTo(HaveOccurred())

				// Checking that the controller has created the Route with the updated haproxy.router.openshift.io/timeout annotation added
				Eventually(func() error {
					return checkRouteTimeout(types.NamespacedName{Name: getKServeRouteName(inferenceService), Namespace: meshNamespace}, "1m")
				}, timeout, interval).Should(Succeed())
			})

			It("Should create a Route with default timeout", func() {
				By("Creating an inference service with no timeout value defined in the component spec")
				_, meshNamespace := utils.GetIstioControlPlaneName(ctx, k8sClient)
				inferenceService := &kservev1beta1.InferenceService{}
				err := testutils.ConvertToStructuredResource(KserveInferenceServicePath3, inferenceService)
				Expect(err).NotTo(HaveOccurred())
				inferenceService.SetNamespace(testNs)
				Expect(k8sClient.Create(ctx, inferenceService)).Should(Succeed())
				// Update inference service status with url to create the route
				deployedInferenceService := &kservev1beta1.InferenceService{}
				err = k8sClient.Get(ctx, types.NamespacedName{Name: inferenceService.Name, Namespace: inferenceService.Namespace}, deployedInferenceService)
				Expect(err).NotTo(HaveOccurred())
				url, err := apis.ParseURL("https://example-onnx-mnist-timeout-default.test.com")
				Expect(err).NotTo(HaveOccurred())
				deployedInferenceService.Status.URL = url
				err = k8sClient.Status().Update(ctx, deployedInferenceService)
				Expect(err).NotTo(HaveOccurred())

				// Checking that the controller has created the Route with the haproxy.router.openshift.io/timeout annotation added
				Eventually(func() error {
					return checkRouteTimeout(types.NamespacedName{Name: getKServeRouteName(inferenceService), Namespace: meshNamespace}, "90s")
				}, timeout, interval).Should(Succeed())
			})

			It("With a new Kserve InferenceService, serving cert annotation should be added to the runtime Service object.", func() {
				// We need to stub the cluster state and indicate where is istio namespace (reusing authConfig test data)
				if dsciErr := createDSCI(DSCIWithoutAuthorization); dsciErr != nil && !k8sErrors.IsAlreadyExists(dsciErr) {
					Fail(dsciErr.Error())
				}
				// Create a new InferenceService
				inferenceService := &kservev1beta1.InferenceService{}
				err := testutils.ConvertToStructuredResource(KserveInferenceServicePath1, inferenceService)
				Expect(err).NotTo(HaveOccurred())
				inferenceService.SetNamespace(testNs)
				Expect(k8sClient.Create(ctx, inferenceService)).Should(Succeed())
				// Update the URL of the InferenceService to indicate it is ready.
				deployedInferenceService := &kservev1beta1.InferenceService{}
				err = k8sClient.Get(ctx, types.NamespacedName{Name: inferenceService.Name, Namespace: inferenceService.Namespace}, deployedInferenceService)
				Expect(err).NotTo(HaveOccurred())
				// url, err := apis.ParseURL("https://example-onnx-mnist-default.test.com")
				Expect(err).NotTo(HaveOccurred())
				newAddress := &duckv1.Addressable{
					URL: apis.HTTPS("example-onnx-mnist-default.test.com"),
				}
				deployedInferenceService.Status.Address = newAddress
				err = k8sClient.Status().Update(ctx, deployedInferenceService)
				Expect(err).NotTo(HaveOccurred())
				// Stub: Create a Kserve Service, which must be created by the KServe operator.
				svc := &corev1.Service{}
				err = testutils.ConvertToStructuredResource(testIsvcSvcPath, svc)
				Expect(err).NotTo(HaveOccurred())
				svc.SetNamespace(inferenceService.Namespace)
				Expect(k8sClient.Create(ctx, svc)).Should(Succeed())
				err = k8sClient.Status().Update(ctx, deployedInferenceService)
				Expect(err).NotTo(HaveOccurred())
				// isvcService, err := waitForService(cli, testNs, inferenceService.Name, 5, 2*time.Second)
				// Expect(err).NotTo(HaveOccurred())

				isvcService := &corev1.Service{}
				Eventually(func() error {
					err := k8sClient.Get(ctx, client.ObjectKey{Namespace: inferenceService.Namespace, Name: inferenceService.Name}, isvcService)
					if err != nil {
						return err
					}
					if isvcService.Annotations == nil || isvcService.Annotations[constants.ServingCertAnnotationKey] == "" {

						return fmt.Errorf("Annotation[constants.ServingCertAnnotationKey] is not added yet")
					}
					return nil
				}, timeout, interval).Should(Succeed())

				Expect(isvcService.Annotations[constants.ServingCertAnnotationKey]).Should(Equal(inferenceService.Name))
			})

			It("should create a secret for runtime and update kserve local gateway in the istio-system namespace", func() {
				// We need to stub the cluster state and indicate where is istio namespace (reusing authConfig test data)
				if dsciErr := createDSCI(DSCIWithoutAuthorization); dsciErr != nil && !k8sErrors.IsAlreadyExists(dsciErr) {
					Fail(dsciErr.Error())
				}
				// Stub: Create a kserve-local-gateway, which must be created by the OpenDataHub operator.
				kserveLocalGateway := &istioclientv1beta1.Gateway{}
				err := testutils.ConvertToStructuredResource(kserveLocalGatewayPath, kserveLocalGateway)
				Expect(err).NotTo(HaveOccurred())
				Expect(k8sClient.Create(ctx, kserveLocalGateway)).Should(Succeed())

				// Stub: Create a certificate Secret, which must be created by the openshift service-ca operator.
				secret := &corev1.Secret{}
				err = testutils.ConvertToStructuredResource(testIsvcSvcSecretPath, secret)
				Expect(err).NotTo(HaveOccurred())
				secret.SetNamespace(testNs)
				Expect(k8sClient.Create(ctx, secret)).Should(Succeed())

				// Create a new InferenceService
				inferenceService := &kservev1beta1.InferenceService{}
				err = testutils.ConvertToStructuredResource(KserveInferenceServicePath1, inferenceService)
				Expect(err).NotTo(HaveOccurred())
				inferenceService.SetNamespace(testNs)

				Expect(k8sClient.Create(ctx, inferenceService)).Should(Succeed())

				// Update the URL of the InferenceService to indicate it is ready.
				deployedInferenceService := &kservev1beta1.InferenceService{}
				err = k8sClient.Get(ctx, types.NamespacedName{Name: inferenceService.Name, Namespace: inferenceService.Namespace}, deployedInferenceService)
				Expect(err).NotTo(HaveOccurred())

				newAddress := &duckv1.Addressable{
					URL: apis.HTTPS("example-onnx-mnist-default.test.com"),
				}
				deployedInferenceService.Status.Address = newAddress

				err = k8sClient.Status().Update(ctx, deployedInferenceService)
				Expect(err).NotTo(HaveOccurred())

				_, meshNamespace := utils.GetIstioControlPlaneName(ctx, k8sClient)

				// Verify that the certificate secret is created in the istio-system namespace.
				Eventually(func() error {
					secret := &corev1.Secret{}
					return k8sClient.Get(ctx, client.ObjectKey{Namespace: meshNamespace, Name: fmt.Sprintf("%s-%s", inferenceService.Name, inferenceService.Namespace)}, secret)
				}, timeout, interval).Should(Succeed())

				// Verify that the gateway is updated in the istio-system namespace.
				var gateway *istioclientv1beta1.Gateway
				Eventually(func() error {
					gateway, err = waitForUpdatedGatewayCompletion(k8sClient, "add", meshNamespace, constants.KServeGatewayName, inferenceService.Name)
					return err
				}, timeout, interval).Should(Succeed())

				// Ensure that the server is successfully added to the KServe local gateway within the istio-system namespace.
				targetServerExist := hasServerFromGateway(gateway, fmt.Sprintf("%s-%s", "https", inferenceService.Name))
				Expect(targetServerExist).Should(BeTrue())
			})

			It("should create required network policies when KServe is used", func() {
				// given
				inferenceService := &kservev1beta1.InferenceService{}
				Expect(testutils.ConvertToStructuredResource(KserveInferenceServicePath1, inferenceService)).To(Succeed())
				inferenceService.SetNamespace(testNs)

				// when
				Expect(k8sClient.Create(ctx, inferenceService)).Should(Succeed())

				// then
				By("ensuring that the controller has created required network policies")
				networkPolicies := &v1.NetworkPolicyList{}
				Eventually(func() []v1.NetworkPolicy {
					err := k8sClient.List(ctx, networkPolicies, client.InNamespace(inferenceService.Namespace))
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

		Context("when there is a existing inferenceService", func() {
			var testNs string
			var isvcName string

			BeforeEach(func() {
				ctx := context.Background()
				testNamespace := testutils.Namespaces.Create(ctx, k8sClient)
				testNs = testNamespace.Name
				_, meshNamespace := utils.GetIstioControlPlaneName(ctx, k8sClient)

				inferenceServiceConfig := &corev1.ConfigMap{}
				Expect(testutils.ConvertToStructuredResource(InferenceServiceConfigPath1, inferenceServiceConfig)).To(Succeed())
				if err := k8sClient.Create(ctx, inferenceServiceConfig); err != nil && !k8sErrors.IsAlreadyExists(err) {
					Fail(err.Error())
				}

				// We need to stub the cluster state and indicate where is istio namespace (reusing authConfig test data)
				if dsciErr := createDSCI(DSCIWithoutAuthorization); dsciErr != nil && !k8sErrors.IsAlreadyExists(dsciErr) {
					Fail(dsciErr.Error())
				}

				servingRuntime := &kservev1alpha1.ServingRuntime{}
				Expect(testutils.ConvertToStructuredResource(KserveServingRuntimePath1, servingRuntime)).To(Succeed())
				if err := k8sClient.Create(ctx, servingRuntime); err != nil && !k8sErrors.IsAlreadyExists(err) {
					Fail(err.Error())
				}

				// Stub: Create a kserve-local-gateway, which must be created by the OpenDataHub operator.
				kserveLocalGateway := &istioclientv1beta1.Gateway{}
				err := testutils.ConvertToStructuredResource(kserveLocalGatewayPath, kserveLocalGateway)
				Expect(err).NotTo(HaveOccurred())
				Expect(k8sClient.Create(ctx, kserveLocalGateway)).Should(Succeed())

				// Stub: Create a certificate Secret, which must be created by the openshift service-ca operator.
				secret := &corev1.Secret{}
				err = testutils.ConvertToStructuredResource(testIsvcSvcSecretPath, secret)
				Expect(err).NotTo(HaveOccurred())
				secret.SetNamespace(testNs)
				Expect(k8sClient.Create(ctx, secret)).Should(Succeed())

				// Create a new InferenceService
				inferenceService := &kservev1beta1.InferenceService{}
				err = testutils.ConvertToStructuredResource(KserveInferenceServicePath1, inferenceService)
				Expect(err).NotTo(HaveOccurred())
				inferenceService.SetNamespace(testNs)
				// Ensure the Delete method is called when the InferenceService (ISVC) is deleted.
				inferenceService.SetFinalizers([]string{"finalizer.inferenceservice"})

				Expect(k8sClient.Create(ctx, inferenceService)).Should(Succeed())
				isvcName = inferenceService.Name

				// Update the URL of the InferenceService to indicate it is ready.
				deployedInferenceService := &kservev1beta1.InferenceService{}
				err = k8sClient.Get(ctx, types.NamespacedName{Name: inferenceService.Name, Namespace: testNs}, deployedInferenceService)
				Expect(err).NotTo(HaveOccurred())

				newAddress := &duckv1.Addressable{
					URL: apis.HTTPS("example-onnx-mnist-default.test.com"),
				}
				deployedInferenceService.Status.Address = newAddress

				err = k8sClient.Status().Update(ctx, deployedInferenceService)
				Expect(err).NotTo(HaveOccurred())

				// Verify that the certificate secret is created in the istio-system namespace.
				Eventually(func() error {
					secret := &corev1.Secret{}
					return k8sClient.Get(ctx, types.NamespacedName{Name: inferenceService.Name, Namespace: inferenceService.Namespace}, secret)
				}, timeout, interval).Should(Succeed())

				Eventually(func() error {
					return k8sClient.Get(ctx, client.ObjectKey{Namespace: meshNamespace, Name: fmt.Sprintf("%s-%s", inferenceService.Name, inferenceService.Namespace)}, secret)
				}, timeout, interval).Should(Succeed())

				// Verify that the gateway is updated in the istio-system namespace.
				var gateway *istioclientv1beta1.Gateway
				Eventually(func() error {
					gateway, err = waitForUpdatedGatewayCompletion(k8sClient, "add", meshNamespace, constants.KServeGatewayName, inferenceService.Name)
					return err
				}, timeout, interval).Should(Succeed())

				// Ensure that the server is successfully added to the KServe local gateway within the istio-system namespace.
				targetServerExist := hasServerFromGateway(gateway, fmt.Sprintf("%s-%s", "https", inferenceService.Name))
				Expect(targetServerExist).Should(BeTrue())
			})

			When("serving cert Secret is rotated", func() {
				It("should re-sync serving cert Secret to istio-system", func() {
					deployedInferenceService := &kservev1beta1.InferenceService{}
					err := k8sClient.Get(ctx, types.NamespacedName{Name: isvcName, Namespace: testNs}, deployedInferenceService)
					Expect(err).NotTo(HaveOccurred())

					// Get source secret
					srcSecret := &corev1.Secret{}
					err = k8sClient.Get(ctx, client.ObjectKey{Namespace: testNs, Name: deployedInferenceService.Name}, srcSecret)
					Expect(err).NotTo(HaveOccurred())

					// Update source secret
					updatedDataString := "updateData"
					srcSecret.Data["tls.crt"] = []byte(updatedDataString)
					srcSecret.Data["tls.key"] = []byte(updatedDataString)
					Expect(k8sClient.Update(ctx, srcSecret)).Should(Succeed())

					// Get destination secret
					err = k8sClient.Get(ctx, client.ObjectKey{Namespace: testNs, Name: deployedInferenceService.Name}, srcSecret)
					Expect(err).NotTo(HaveOccurred())

					// Verify that the certificate secret in the istio-system namespace is updated.
					_, meshNamespace := utils.GetIstioControlPlaneName(ctx, k8sClient)
					destSecret := &corev1.Secret{}
					Eventually(func() error {
						Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: meshNamespace, Name: fmt.Sprintf("%s-%s", deployedInferenceService.Name, deployedInferenceService.Namespace)}, destSecret)).Should(Succeed())
						if string(destSecret.Data["tls.crt"]) != updatedDataString {
							return fmt.Errorf("destSecret is not updated yet")
						}
						return nil
					}, timeout, interval).Should(Succeed())

					Expect(destSecret.Data).To(Equal(srcSecret.Data))
				})
			})

			When("infereceService is deleted", func() {
				It("should remove the Server from the kserve local gateway in istio-system and delete the created Secret", func() {
					// Delete the existing ISVC
					deployedInferenceService := &kservev1beta1.InferenceService{}
					err := k8sClient.Get(ctx, types.NamespacedName{Name: isvcName, Namespace: testNs}, deployedInferenceService)
					Expect(err).NotTo(HaveOccurred())
					Expect(k8sClient.Delete(ctx, deployedInferenceService)).Should(Succeed())

					_, meshNamespace := utils.GetIstioControlPlaneName(ctx, k8sClient)

					// Verify that the gateway is updated in the istio-system namespace.
					var gateway *istioclientv1beta1.Gateway
					Eventually(func() error {
						gateway, err = waitForUpdatedGatewayCompletion(k8sClient, "delete", meshNamespace, constants.KServeGatewayName, isvcName)
						return err
					}, timeout, interval).Should(Succeed())

					// Ensure that the server is successfully removed from the KServe local gateway within the istio-system namespace.
					targetServerExist := hasServerFromGateway(gateway, isvcName)
					Expect(targetServerExist).Should(BeFalse())

					// Ensure that the synced Secret is successfully deleted within the istio-system namespace.
					secret := &corev1.Secret{}
					Eventually(func() error {
						return k8sClient.Get(ctx, client.ObjectKey{Namespace: meshNamespace, Name: fmt.Sprintf("%s-%s", isvcName, meshNamespace)}, secret)
					}, timeout, interval).ShouldNot(Succeed())
				})
			})
		})

	})

	Describe("Openshift ModelMesh integrations", func() {

		When("creating a ServiceRuntime & InferenceService with 'enable-route' enabled", func() {

			BeforeEach(func() {
				ctx := context.Background()

				servingRuntime1 := &kservev1alpha1.ServingRuntime{}
				err := testutils.ConvertToStructuredResource(ServingRuntimePath1, servingRuntime1)
				Expect(err).NotTo(HaveOccurred())
				Expect(k8sClient.Create(ctx, servingRuntime1)).Should(Succeed())

				servingRuntime2 := &kservev1alpha1.ServingRuntime{}
				err = testutils.ConvertToStructuredResource(ServingRuntimePath2, servingRuntime2)
				Expect(err).NotTo(HaveOccurred())
				Expect(k8sClient.Create(ctx, servingRuntime2)).Should(Succeed())
			})

			It("when InferenceService specifies a runtime, should create a Route to expose the traffic externally", func() {
				inferenceService := &kservev1beta1.InferenceService{}
				err := testutils.ConvertToStructuredResource(InferenceService1, inferenceService)
				Expect(err).NotTo(HaveOccurred())
				Expect(k8sClient.Create(ctx, inferenceService)).Should(Succeed())

				By("By checking that the controller has created the Route")

				route := &routev1.Route{}
				Eventually(func() error {
					key := types.NamespacedName{Name: inferenceService.Name, Namespace: inferenceService.Namespace}
					return k8sClient.Get(ctx, key, route)
				}, timeout, interval).ShouldNot(HaveOccurred())

				expectedRoute := &routev1.Route{}
				err = testutils.ConvertToStructuredResource(ExpectedRoutePath, expectedRoute)
				Expect(err).NotTo(HaveOccurred())

				Expect(comparators.GetMMRouteComparator()(route, expectedRoute)).Should(BeTrue())
			})

			It("when InferenceService does not specifies a runtime, should automatically pick a runtime and create a Route", func() {
				inferenceService := &kservev1beta1.InferenceService{}
				err := testutils.ConvertToStructuredResource(InferenceServiceNoRuntime, inferenceService)
				Expect(err).NotTo(HaveOccurred())
				Expect(k8sClient.Create(ctx, inferenceService)).Should(Succeed())

				route := &routev1.Route{}
				Eventually(func() error {
					key := types.NamespacedName{Name: inferenceService.Name, Namespace: inferenceService.Namespace}
					return k8sClient.Get(ctx, key, route)
				}, timeout, interval).ShouldNot(HaveOccurred())

				expectedRoute := &routev1.Route{}
				err = testutils.ConvertToStructuredResource(ExpectedRouteNoRuntimePath, expectedRoute)
				Expect(err).NotTo(HaveOccurred())

				Expect(comparators.GetMMRouteComparator()(route, expectedRoute)).Should(BeTrue())
			})

			It("Should create a Route with custom timeout to expose the traffic externally", func() {
				By("Creating an inference service with a timeout value defined in the component spec")
				inferenceService := &kservev1beta1.InferenceService{}
				err := testutils.ConvertToStructuredResource(InferenceService2, inferenceService)
				Expect(err).NotTo(HaveOccurred())
				Expect(k8sClient.Create(ctx, inferenceService)).Should(Succeed())

				// Checking that the controller has created the Route with the haproxy.router.openshift.io/timeout annotation added
				Eventually(func() error {
					return checkRouteTimeout(types.NamespacedName{Name: inferenceService.Name, Namespace: inferenceService.Namespace}, "135s")
				}, timeout, interval).Should(Succeed())

				By("Updating an existing inference service with the haproxy.router.openshift.io/timeout annotation")
				deployedInferenceService := &kservev1beta1.InferenceService{}
				err = k8sClient.Get(ctx, types.NamespacedName{Name: inferenceService.Name, Namespace: inferenceService.Namespace}, deployedInferenceService)
				Expect(err).NotTo(HaveOccurred())
				deployedInferenceService.Annotations[constants.RouteTimeoutAnnotationKey] = "1m"
				err = k8sClient.Update(ctx, deployedInferenceService)
				Expect(err).NotTo(HaveOccurred())

				// Checking that the controller has created the Route with the updated haproxy.router.openshift.io/timeout annotation added
				Eventually(func() error {
					return checkRouteTimeout(types.NamespacedName{Name: inferenceService.Name, Namespace: inferenceService.Namespace}, "1m")
				}, timeout, interval).Should(Succeed())
			})

			It("Should create a Route with default timeout to expose the traffic externally", func() {
				By("Creating an inference service with no timeout value defined in the component spec")
				inferenceService := &kservev1beta1.InferenceService{}
				err := testutils.ConvertToStructuredResource(InferenceService3, inferenceService)
				Expect(err).NotTo(HaveOccurred())
				Expect(k8sClient.Create(ctx, inferenceService)).Should(Succeed())

				// Checking that the controller has created the Route with the haproxy.router.openshift.io/timeout annotation added
				Eventually(func() error {
					return checkRouteTimeout(types.NamespacedName{Name: inferenceService.Name, Namespace: inferenceService.Namespace}, "90s")
				}, timeout, interval).Should(Succeed())
			})
		})
	})

	Describe("Mesh reconciler", func() {
		var testNs string

		createInferenceService := func(namespace, name string) *kservev1beta1.InferenceService {
			inferenceService := &kservev1beta1.InferenceService{}
			err := testutils.ConvertToStructuredResource(KserveInferenceServicePath1, inferenceService)
			Expect(err).NotTo(HaveOccurred())
			inferenceService.SetNamespace(namespace)
			if len(name) != 0 {
				inferenceService.Name = name
			}
			Expect(k8sClient.Create(ctx, inferenceService)).Should(Succeed())

			return inferenceService
		}

		expectOwnedSmmCreated := func(namespace string) {
			Eventually(func() error {
				smm := &maistrav1.ServiceMeshMember{}
				key := types.NamespacedName{Name: constants.ServiceMeshMemberName, Namespace: namespace}
				err := k8sClient.Get(ctx, key, smm)
				return err
			}, timeout, interval).Should(Succeed())
		}

		createUserOwnedMeshEnrolment := func(namespace string) *maistrav1.ServiceMeshMember {
			controlPlaneName, meshNamespace := utils.GetIstioControlPlaneName(ctx, k8sClient)
			smm := &maistrav1.ServiceMeshMember{
				ObjectMeta: metav1.ObjectMeta{
					Name:        constants.ServiceMeshMemberName,
					Namespace:   namespace,
					Labels:      nil,
					Annotations: nil,
				},
				Spec: maistrav1.ServiceMeshMemberSpec{
					ControlPlaneRef: maistrav1.ServiceMeshControlPlaneRef{
						Name:      controlPlaneName,
						Namespace: meshNamespace,
					}},
				Status: maistrav1.ServiceMeshMemberStatus{},
			}
			Expect(k8sClient.Create(ctx, smm)).Should(Succeed())

			return smm
		}

		BeforeEach(func() {
			testNamespace := testutils.Namespaces.Create(ctx, k8sClient)
			testNs = testNamespace.Name

			inferenceServiceConfig := &corev1.ConfigMap{}
			Expect(testutils.ConvertToStructuredResource(InferenceServiceConfigPath1, inferenceServiceConfig)).To(Succeed())
			if err := k8sClient.Create(ctx, inferenceServiceConfig); err != nil && !k8sErrors.IsAlreadyExists(err) {
				Fail(err.Error())
			}
		})

		When("deploying the first model in a namespace", func() {
			It("if the namespace is not part of the service mesh, it should enroll the namespace to the mesh", func() {
				inferenceService := createInferenceService(testNs, "")
				expectOwnedSmmCreated(inferenceService.Namespace)
			})

			It("if the namespace is already enrolled to the service mesh by the user, it should not modify the enrollment", func() {
				smm := createUserOwnedMeshEnrolment(testNs)
				inferenceService := createInferenceService(testNs, "")

				Consistently(func() bool {
					actualSmm := &maistrav1.ServiceMeshMember{}
					key := types.NamespacedName{Name: constants.ServiceMeshMemberName, Namespace: inferenceService.Namespace}
					err := k8sClient.Get(ctx, key, actualSmm)
					return err == nil && reflect.DeepEqual(actualSmm, smm)
				}).Should(BeTrue())
			})

			It("if the namespace is already enrolled to some other control plane, it should anyway not modify the enrollment", func() {
				smm := &maistrav1.ServiceMeshMember{
					ObjectMeta: metav1.ObjectMeta{
						Name:        constants.ServiceMeshMemberName,
						Namespace:   testNs,
						Labels:      nil,
						Annotations: nil,
					},
					Spec: maistrav1.ServiceMeshMemberSpec{
						ControlPlaneRef: maistrav1.ServiceMeshControlPlaneRef{
							Name:      "random-control-plane-vbfr238497",
							Namespace: "random-namespace-a234h",
						}},
					Status: maistrav1.ServiceMeshMemberStatus{},
				}
				Expect(k8sClient.Create(ctx, smm)).Should(Succeed())

				inferenceService := createInferenceService(testNs, "")

				Consistently(func() bool {
					actualSmm := &maistrav1.ServiceMeshMember{}
					key := types.NamespacedName{Name: constants.ServiceMeshMemberName, Namespace: inferenceService.Namespace}
					err := k8sClient.Get(ctx, key, actualSmm)
					return err == nil && reflect.DeepEqual(actualSmm, smm)
				}).Should(BeTrue())
			})
		})

		When("deleting the last model in a namespace", func() {
			It("it should remove the owned service mesh enrolment", func() {
				inferenceService := createInferenceService(testNs, "")
				expectOwnedSmmCreated(inferenceService.Namespace)

				Expect(k8sClient.Delete(ctx, inferenceService)).Should(Succeed())
				Eventually(func() error {
					smm := &maistrav1.ServiceMeshMember{}
					key := types.NamespacedName{Name: constants.ServiceMeshMemberName, Namespace: inferenceService.Namespace}
					err := k8sClient.Get(ctx, key, smm)
					return err
				}, timeout, interval).ShouldNot(Succeed())
			})

			It("it should not remove a user-owned service mesh enrolment", func() {
				createUserOwnedMeshEnrolment(testNs)
				inferenceService := createInferenceService(testNs, "")

				Expect(k8sClient.Delete(ctx, inferenceService)).Should(Succeed())
				Consistently(func() int {
					smmList := &maistrav1.ServiceMeshMemberList{}
					Expect(k8sClient.List(ctx, smmList, client.InNamespace(inferenceService.Namespace))).Should(Succeed())
					return len(smmList.Items)
				}).Should(Equal(1))
			})
		})

		When("deleting a model, but there are other models left in the namespace", func() {
			It("it should not remove the owned service mesh enrolment", func() {
				inferenceService1 := createInferenceService(testNs, "")
				createInferenceService(testNs, "secondary-isvc")
				expectOwnedSmmCreated(inferenceService1.Namespace)

				Expect(k8sClient.Delete(ctx, inferenceService1)).Should(Succeed())
				Consistently(func() error {
					smm := &maistrav1.ServiceMeshMember{}
					key := types.NamespacedName{Name: constants.ServiceMeshMemberName, Namespace: inferenceService1.Namespace}
					err := k8sClient.Get(ctx, key, smm)
					return err
				}, timeout, interval).Should(Succeed())
			})
		})
	})

	Describe("Dashboard reconciler", func() {
		var testNs string

		createServingRuntime := func(namespace, path string) *kservev1alpha1.ServingRuntime {
			servingRuntime := &kservev1alpha1.ServingRuntime{}
			err := testutils.ConvertToStructuredResource(path, servingRuntime)
			Expect(err).NotTo(HaveOccurred())
			servingRuntime.SetNamespace(namespace)
			if err := k8sClient.Create(ctx, servingRuntime); err != nil && !k8sErrors.IsAlreadyExists(err) {
				Fail(err.Error())
			}
			return servingRuntime
		}

		createInferenceService := func(namespace, name string, path string, isRaw ...bool) *kservev1beta1.InferenceService {
			inferenceService := &kservev1beta1.InferenceService{}
			err := testutils.ConvertToStructuredResource(path, inferenceService)
			Expect(err).NotTo(HaveOccurred())
			inferenceService.SetNamespace(namespace)
			if len(name) != 0 {
				inferenceService.Name = name
			}
			raw := len(isRaw) > 0 && isRaw[0]
			if raw {
				if inferenceService.Annotations == nil {
					inferenceService.Annotations = map[string]string{}
				}
				inferenceService.Annotations["serving.kserve.io/deploymentMode"] = "RawDeployment"
			}
			if err := k8sClient.Create(ctx, inferenceService); err != nil && !k8sErrors.IsAlreadyExists(err) {
				Fail(err.Error())
			}
			return inferenceService
		}

		verifyConfigMap := func(isvcName string, namespace string, supported bool, metricsData string) {
			metricsConfigMap, err := testutils.WaitForConfigMap(k8sClient, namespace, isvcName+constants.KserveMetricsConfigMapNameSuffix, 30, 1*time.Second)
			Expect(err).NotTo(HaveOccurred())
			Expect(metricsConfigMap).NotTo(BeNil())
			var expectedMetricsConfigMap *corev1.ConfigMap
			if supported {
				finaldata := utils.SubstituteVariablesInQueries(metricsData, namespace, isvcName)
				expectedMetricsConfigMap = &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      isvcName + constants.KserveMetricsConfigMapNameSuffix,
						Namespace: namespace,
					},
					Data: map[string]string{
						"supported": "true",
						"metrics":   finaldata,
					},
				}
			} else {
				expectedMetricsConfigMap = &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      UnsupportedMetricsInferenceServiceName + constants.KserveMetricsConfigMapNameSuffix,
						Namespace: testNs,
					},
					Data: map[string]string{
						"supported": "false",
					},
				}
			}
			Expect(testutils.CompareConfigMap(metricsConfigMap, expectedMetricsConfigMap)).Should(BeTrue())
			Expect(expectedMetricsConfigMap.Data).NotTo(HaveKeyWithValue("metrics", ContainSubstring("${REQUEST_RATE_INTERVAL}")))
		}

		BeforeEach(func() {
			testNs = testutils.Namespaces.Create(ctx, k8sClient).Name

			inferenceServiceConfig := &corev1.ConfigMap{}
			Expect(testutils.ConvertToStructuredResource(InferenceServiceConfigPath1, inferenceServiceConfig)).To(Succeed())
			if err := k8sClient.Create(ctx, inferenceServiceConfig); err != nil {
				Fail(err.Error())
			}
		})

		When("deploying a Kserve model", func() {
			It("[serverless] if the runtime is supported for metrics, it should create a configmap with prometheus queries", func() {
				_ = createServingRuntime(testNs, KserveServingRuntimePath1)
				_ = createInferenceService(testNs, KserveOvmsInferenceServiceName, KserveInferenceServicePath1)

				verifyConfigMap(KserveOvmsInferenceServiceName, testNs, true, constants.OvmsMetricsData)
			})

			It("[raw] if the runtime is supported for metrics, it should create a configmap with prometheus queries", func() {
				_ = createServingRuntime(testNs, KserveServingRuntimePath1)
				_ = createInferenceService(testNs, KserveOvmsInferenceServiceName, KserveInferenceServicePath1, true)

				verifyConfigMap(KserveOvmsInferenceServiceName, testNs, true, constants.OvmsMetricsData)
			})

			It("[serverless] if the runtime is not supported for metrics, it should create a configmap with the unsupported config", func() {
				_ = createServingRuntime(testNs, UnsupprtedMetricsServingRuntimePath)
				_ = createInferenceService(testNs, UnsupportedMetricsInferenceServiceName, UnsupportedMetricsInferenceServicePath)

				verifyConfigMap(UnsupportedMetricsInferenceServiceName, testNs, false, "")
			})

			It("[raw] if the runtime is not supported for metrics, it should create a configmap with the unsupported config", func() {
				_ = createServingRuntime(testNs, UnsupprtedMetricsServingRuntimePath)
				_ = createInferenceService(testNs, UnsupportedMetricsInferenceServiceName, UnsupportedMetricsInferenceServicePath, true)

				verifyConfigMap(UnsupportedMetricsInferenceServiceName, testNs, false, "")
			})

			It("[serverless] if the isvc does not have a runtime specified and there is no supported runtime, an unsupported metrics configmap should be created", func() {
				_ = createInferenceService(testNs, NilRuntimeInferenceServiceName, NilRuntimeInferenceServicePath)

				verifyConfigMap(NilRuntimeInferenceServiceName, testNs, false, "")
			})

			It("[raw] if the isvc does not have a runtime specified and there is no supported runtime, an unsupported metrics configmap should be created", func() {
				_ = createInferenceService(testNs, NilRuntimeInferenceServiceName, NilRuntimeInferenceServicePath, true)

				verifyConfigMap(NilRuntimeInferenceServiceName, testNs, false, "")
			})

			It("[serverless] if the isvc does not have the model field specified, an unsupported metrics configmap should be created", func() {
				_ = createInferenceService(testNs, NilModelInferenceServiceName, NilModelInferenceServicePath)

				verifyConfigMap(NilModelInferenceServiceName, testNs, false, "")
			})

			It("[raw] if the isvc does not have the model field specified, an unsupported metrics configmap should be created", func() {
				_ = createInferenceService(testNs, NilModelInferenceServiceName, NilModelInferenceServicePath, true)

				verifyConfigMap(NilModelInferenceServiceName, testNs, false, "")
			})
		})

		When("deleting the deployed models", func() {
			timeout10s := time.Second * 10
			interval4s := time.Second * 4
			It("[serverless] it should delete the associated configmap", func() {
				_ = createServingRuntime(testNs, KserveServingRuntimePath1)
				OvmsInferenceService := createInferenceService(testNs, KserveOvmsInferenceServiceName, KserveInferenceServicePath1)

				Expect(k8sClient.Delete(ctx, OvmsInferenceService)).Should(Succeed())
				Eventually(func() error {
					configmap := &corev1.ConfigMap{}
					key := types.NamespacedName{Name: KserveOvmsInferenceServiceName + constants.KserveMetricsConfigMapNameSuffix, Namespace: OvmsInferenceService.Namespace}
					err := k8sClient.Get(ctx, key, configmap)
					return err
				}, timeout10s, interval4s).ShouldNot(Succeed())

				_ = createServingRuntime(testNs, UnsupprtedMetricsServingRuntimePath)
				SklearnInferenceService := createInferenceService(testNs, UnsupportedMetricsInferenceServiceName, UnsupportedMetricsInferenceServicePath)

				Expect(k8sClient.Delete(ctx, SklearnInferenceService)).Should(Succeed())
				Eventually(func() error {
					configmap := &corev1.ConfigMap{}
					key := types.NamespacedName{Name: UnsupportedMetricsInferenceServiceName + constants.KserveMetricsConfigMapNameSuffix, Namespace: SklearnInferenceService.Namespace}
					err := k8sClient.Get(ctx, key, configmap)
					return err
				}, timeout10s, interval4s).ShouldNot(Succeed())
			})
			It("[raw] it should delete the associated configmap", func() {
				_ = createServingRuntime(testNs, KserveServingRuntimePath1)
				OvmsInferenceService := createInferenceService(testNs, KserveOvmsInferenceServiceName, KserveInferenceServicePath1, true)

				Expect(k8sClient.Delete(ctx, OvmsInferenceService)).Should(Succeed())
				Eventually(func() error {
					configmap := &corev1.ConfigMap{}
					key := types.NamespacedName{Name: KserveOvmsInferenceServiceName + constants.KserveMetricsConfigMapNameSuffix, Namespace: OvmsInferenceService.Namespace}
					err := k8sClient.Get(ctx, key, configmap)
					return err
				}, timeout10s, interval4s).ShouldNot(Succeed())

				_ = createServingRuntime(testNs, UnsupprtedMetricsServingRuntimePath)
				SklearnInferenceService := createInferenceService(testNs, UnsupportedMetricsInferenceServiceName, UnsupportedMetricsInferenceServicePath, true)

				Expect(k8sClient.Delete(ctx, SklearnInferenceService)).Should(Succeed())
				Eventually(func() error {
					configmap := &corev1.ConfigMap{}
					key := types.NamespacedName{Name: UnsupportedMetricsInferenceServiceName + constants.KserveMetricsConfigMapNameSuffix, Namespace: SklearnInferenceService.Namespace}
					err := k8sClient.Get(ctx, key, configmap)
					return err
				}, timeout10s, interval4s).ShouldNot(Succeed())
			})
		})
	})

	Describe("InferenceService Authorization", func() {
		var (
			namespace *corev1.Namespace
			isvc      *kservev1beta1.InferenceService
		)

		When("not configured for the cluster", func() {
			BeforeEach(func() {
				ctx := context.Background()
				namespace = &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: testutils.Namespaces.Get(),
					},
				}
				Expect(k8sClient.Create(ctx, namespace)).Should(Succeed())
				inferenceServiceConfig := &corev1.ConfigMap{}

				Expect(testutils.ConvertToStructuredResource(InferenceServiceConfigPath1, inferenceServiceConfig)).To(Succeed())
				if err := k8sClient.Create(ctx, inferenceServiceConfig); err != nil && !k8sErrors.IsAlreadyExists(err) {
					Fail(err.Error())
				}

				// We need to stub the cluster state and indicate if Authorino is configured as authorization layer
				if dsciErr := createDSCI(DSCIWithoutAuthorization); dsciErr != nil && !k8sErrors.IsAlreadyExists(dsciErr) {
					Fail(dsciErr.Error())
				}

				isvc = createISVCWithoutAuth(namespace.Name)
			})

			AfterEach(func() {
				Expect(deleteDSCI(DSCIWithoutAuthorization)).To(Succeed())
			})

			It("should not create auth config", func() {
				Consistently(func() error {
					ac := &authorinov1beta2.AuthConfig{}
					return getAuthConfig(namespace.Name, isvc.Name, ac)
				}).
					WithTimeout(timeout).
					WithPolling(interval).
					Should(Not(Succeed()))
			})
		})

		When("configured for the cluster", func() {

			BeforeEach(func() {
				ctx := context.Background()
				namespace = &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: testutils.Namespaces.Get(),
					},
				}
				Expect(k8sClient.Create(ctx, namespace)).Should(Succeed())
				inferenceServiceConfig := &corev1.ConfigMap{}

				Expect(testutils.ConvertToStructuredResource(InferenceServiceConfigPath1, inferenceServiceConfig)).To(Succeed())
				if err := k8sClient.Create(ctx, inferenceServiceConfig); err != nil && !k8sErrors.IsAlreadyExists(err) {
					Fail(err.Error())
				}

				// // We need to stub the cluster state and indicate that Authorino is configured as authorization layer
				// if dsciErr := createDSCI(DSCIWithAuthorization); dsciErr != nil && !k8sErrors.IsAlreadyExists(dsciErr) {
				//	Fail(dsciErr.Error())
				// }

				// TODO: See utils.VerifyIfMeshAuthorizationIsEnabled func
				if authPolicyErr := createAuthorizationPolicy(KServeAuthorizationPolicy); authPolicyErr != nil && !k8sErrors.IsAlreadyExists(authPolicyErr) {
					Fail(authPolicyErr.Error())
				}
			})

			AfterEach(func() {
				// Expect(deleteDSCI(DSCIWithAuthorization)).To(Succeed())
				Expect(deleteAuthorizationPolicy(KServeAuthorizationPolicy)).To(Succeed())
			})

			Context("when InferenceService is not ready", func() {
				BeforeEach(func() {
					isvc = createISVCMissingStatus(namespace.Name)
				})

				It("should not create auth config on missing status.URL", func() {

					Consistently(func() error {
						ac := &authorinov1beta2.AuthConfig{}
						return getAuthConfig(namespace.Name, isvc.Name, ac)
					}).
						WithTimeout(timeout).
						WithPolling(interval).
						Should(Not(Succeed()))
				})
			})

			Context("when InferenceService is ready", func() {

				Context("auth not enabled", func() {
					BeforeEach(func() {
						isvc = createISVCWithoutAuth(namespace.Name)
					})

					It("should create anonymous auth config", func() {
						Expect(updateISVCStatus(isvc)).To(Succeed())

						Eventually(func(g Gomega) {
							ac := &authorinov1beta2.AuthConfig{}
							g.Expect(getAuthConfig(namespace.Name, isvc.Name, ac)).To(Succeed())
							g.Expect(ac.Spec.Authorization["anonymous-access"]).NotTo(BeNil())
						}).
							WithTimeout(timeout).
							WithPolling(interval).
							Should(Succeed())
					})

					It("should update to non anonymous on enable", func() {
						Expect(updateISVCStatus(isvc)).To(Succeed())

						Eventually(func(g Gomega) {
							ac := &authorinov1beta2.AuthConfig{}
							g.Expect(getAuthConfig(namespace.Name, isvc.Name, ac)).To(Succeed())
							g.Expect(ac.Spec.Authorization["anonymous-access"]).NotTo(BeNil())
						}).
							WithTimeout(timeout).
							WithPolling(interval).
							Should(Succeed())

						Expect(enableAuth(isvc)).To(Succeed())
						Eventually(func(g Gomega) {
							ac := &authorinov1beta2.AuthConfig{}
							g.Expect(ac.Spec.Authorization["kubernetes-user"]).NotTo(BeNil())
							g.Expect(getAuthConfig(namespace.Name, isvc.Name, ac)).To(Succeed())
						}).
							WithTimeout(timeout).
							WithPolling(interval).
							Should(Succeed())
					})
				})

				Context("auth enabled", func() {
					BeforeEach(func() {
						isvc = createISVCWithAuth(namespace.Name)
					})

					It("should create user defined auth config", func() {
						Expect(updateISVCStatus(isvc)).To(Succeed())

						Eventually(func(g Gomega) {
							ac := &authorinov1beta2.AuthConfig{}
							g.Expect(getAuthConfig(namespace.Name, isvc.Name, ac)).To(Succeed())
							g.Expect(ac.Spec.Authorization["kubernetes-user"]).NotTo(BeNil())
						}).
							WithTimeout(timeout).
							WithPolling(interval).
							Should(Succeed())
					})

					It("should update to anonymous on disable", func() {
						Expect(updateISVCStatus(isvc)).To(Succeed())

						Eventually(func(g Gomega) {
							ac := &authorinov1beta2.AuthConfig{}
							g.Expect(getAuthConfig(namespace.Name, isvc.Name, ac)).To(Succeed())
							g.Expect(ac.Spec.Authorization["kubernetes-user"]).NotTo(BeNil())
						}).
							WithTimeout(timeout).
							WithPolling(interval).
							Should(Succeed())

						Expect(disableAuth(isvc)).To(Succeed())
						Eventually(func(g Gomega) {
							ac := &authorinov1beta2.AuthConfig{}
							g.Expect(getAuthConfig(namespace.Name, isvc.Name, ac)).To(Succeed())
							g.Expect(ac.Spec.Authorization["anonymous-access"]).NotTo(BeNil())
						}).
							WithTimeout(timeout).
							WithPolling(interval).
							Should(Succeed())
					})
				})
			})
		})

	})

	Describe("The KServe Raw reconciler", func() {
		var testNs string
		createServingRuntime := func(namespace, path string) *kservev1alpha1.ServingRuntime {
			servingRuntime := &kservev1alpha1.ServingRuntime{}
			err := testutils.ConvertToStructuredResource(path, servingRuntime)
			Expect(err).NotTo(HaveOccurred())
			servingRuntime.SetNamespace(namespace)
			if err := k8sClient.Create(ctx, servingRuntime); err != nil && !k8sErrors.IsAlreadyExists(err) {
				Expect(err).NotTo(HaveOccurred())
			}
			return servingRuntime
		}

		createInferenceService := func(namespace, name string, path string) *kservev1beta1.InferenceService {
			inferenceService := &kservev1beta1.InferenceService{}
			err := testutils.ConvertToStructuredResource(path, inferenceService)
			Expect(err).NotTo(HaveOccurred())
			inferenceService.SetNamespace(namespace)
			if len(name) != 0 {
				inferenceService.Name = name
			}
			inferenceService.Annotations = map[string]string{}
			inferenceService.Annotations["serving.kserve.io/deploymentMode"] = "RawDeployment"
			return inferenceService
		}

		BeforeEach(func() {
			testNs = testutils.Namespaces.Create(ctx, k8sClient).Name

			inferenceServiceConfig := &corev1.ConfigMap{}
			Expect(testutils.ConvertToStructuredResource(InferenceServiceConfigPath1, inferenceServiceConfig)).To(Succeed())
			if err := k8sClient.Create(ctx, inferenceServiceConfig); err != nil && !k8sErrors.IsAlreadyExists(err) {
				Fail(err.Error())
			}

		})

		When("deploying a Kserve RawDeployment model", func() {
			It("it should create a default clusterrolebinding for auth", func() {
				_ = createServingRuntime(testNs, KserveServingRuntimePath1)
				inferenceService := createInferenceService(testNs, KserveOvmsInferenceServiceName, KserveInferenceServicePath1)
				inferenceService.Annotations[constants.EnableAuthODHAnnotation] = "true"
				if err := k8sClient.Create(ctx, inferenceService); err != nil && !k8sErrors.IsAlreadyExists(err) {
					Expect(err).NotTo(HaveOccurred())
				}

				crb := &rbacv1.ClusterRoleBinding{}
				Eventually(func() error {
					key := types.NamespacedName{Name: inferenceService.Namespace + "-" + constants.KserveServiceAccountName + "-auth-delegator",
						Namespace: inferenceService.Namespace}
					return k8sClient.Get(ctx, key, crb)
				}, timeout, interval).ShouldNot(HaveOccurred())

				route := &routev1.Route{}
				Eventually(func() error {
					key := types.NamespacedName{Name: inferenceService.Name, Namespace: inferenceService.Namespace}
					return k8sClient.Get(ctx, key, route)
				}, timeout, interval).Should(HaveOccurred())
			})
			It("it should create a custom rolebinding if isvc has a SA defined", func() {
				serviceAccountName := "custom-sa"
				_ = createServingRuntime(testNs, KserveServingRuntimePath1)
				inferenceService := createInferenceService(testNs, KserveOvmsInferenceServiceName, KserveInferenceServicePath1)
				inferenceService.Annotations[constants.EnableAuthODHAnnotation] = "true"
				inferenceService.Spec.Predictor.ServiceAccountName = serviceAccountName
				if err := k8sClient.Create(ctx, inferenceService); err != nil && !k8sErrors.IsAlreadyExists(err) {
					Expect(err).NotTo(HaveOccurred())
				}

				crb := &rbacv1.ClusterRoleBinding{}
				Eventually(func() error {
					key := types.NamespacedName{Name: inferenceService.Namespace + "-" + serviceAccountName + "-auth-delegator",
						Namespace: inferenceService.Namespace}
					return k8sClient.Get(ctx, key, crb)
				}, timeout, interval).ShouldNot(HaveOccurred())
			})
			It("it should create a route if isvc has the label to expose route", func() {
				inferenceService := createInferenceService(testNs, KserveOvmsInferenceServiceName, KserveInferenceServicePath1)
				inferenceService.Labels = map[string]string{}
				inferenceService.Labels[constants.KserveNetworkVisibility] = constants.LabelEnableKserveRawRoute
				// The service is manually created before the isvc otherwise the unit test risks running into a race condition
				// where the reconcile loop finishes before the service is created, leading to no route being created.
				isvcService := getDefaultService(inferenceService.Namespace)
				if err := k8sClient.Create(ctx, isvcService); err != nil && !k8sErrors.IsAlreadyExists(err) {
					Expect(err).NotTo(HaveOccurred())
				}
				service := &corev1.Service{}
				Eventually(func() error {
					key := types.NamespacedName{Name: isvcService.Name, Namespace: isvcService.Namespace}
					return k8sClient.Get(ctx, key, service)
				}, timeout, interval).Should(Succeed())

				_ = createServingRuntime(testNs, KserveServingRuntimePath1)
				if err := k8sClient.Create(ctx, inferenceService); err != nil && !k8sErrors.IsAlreadyExists(err) {
					Expect(err).NotTo(HaveOccurred())
				}

				route := &routev1.Route{}
				Eventually(func() error {
					key := types.NamespacedName{Name: inferenceService.Name, Namespace: inferenceService.Namespace}
					return k8sClient.Get(ctx, key, route)
				}, timeout, interval).ShouldNot(HaveOccurred())
			})
			It("Should create a route with custom timeout if isvc has the label to expose route", func() {
				By("Creating an inference service with a timeout value defined in the component spec")
				inferenceService := createInferenceService(testNs, KserveOvmsInferenceServiceName, KserveInferenceServicePath2)
				inferenceService.Labels = map[string]string{}
				inferenceService.Labels[constants.KserveNetworkVisibility] = constants.LabelEnableKserveRawRoute
				// The service is manually created before the isvc otherwise the unit test risks running into a race condition
				// where the reconcile loop finishes before the service is created, leading to no route being created.
				isvcService := getDefaultService(inferenceService.Namespace)
				if err := k8sClient.Create(ctx, isvcService); err != nil && !k8sErrors.IsAlreadyExists(err) {
					Expect(err).NotTo(HaveOccurred())
				}
				service := &corev1.Service{}
				Eventually(func() error {
					key := types.NamespacedName{Name: isvcService.Name, Namespace: isvcService.Namespace}
					return k8sClient.Get(ctx, key, service)
				}, timeout, interval).Should(Succeed())
				_ = createServingRuntime(testNs, KserveServingRuntimePath1)
				if err := k8sClient.Create(ctx, inferenceService); err != nil && !k8sErrors.IsAlreadyExists(err) {
					Expect(err).NotTo(HaveOccurred())
				}

				// Checking that the controller has created the Route with the haproxy.router.openshift.io/timeout annotation added
				Eventually(func() error {
					return checkRouteTimeout(types.NamespacedName{Name: inferenceService.Name, Namespace: inferenceService.Namespace}, "135s")
				}, timeout, interval).Should(Succeed())

				By("Updating an existing inference service with the haproxy.router.openshift.io/timeout annotation")
				deployedInferenceService := &kservev1beta1.InferenceService{}
				err := k8sClient.Get(ctx, types.NamespacedName{Name: inferenceService.Name, Namespace: inferenceService.Namespace}, deployedInferenceService)
				Expect(err).NotTo(HaveOccurred())
				deployedInferenceService.Annotations[constants.RouteTimeoutAnnotationKey] = "1m"
				err = k8sClient.Update(ctx, deployedInferenceService)
				Expect(err).NotTo(HaveOccurred())

				// Checking that the controller has created the Route with the updated haproxy.router.openshift.io/timeout annotation added
				Eventually(func() error {
					return checkRouteTimeout(types.NamespacedName{Name: inferenceService.Name, Namespace: inferenceService.Namespace}, "1m")
				}, timeout, interval).Should(Succeed())
			})
			It("Should create a route with default timeout if isvc has the label to expose route", func() {
				By("Creating an inference service with no timeout value defined in the component spec")
				inferenceService := createInferenceService(testNs, KserveOvmsInferenceServiceName, KserveInferenceServicePath3)
				inferenceService.Labels = map[string]string{}
				inferenceService.Labels[constants.KserveNetworkVisibility] = constants.LabelEnableKserveRawRoute
				// The service is manually created before the isvc otherwise the unit test risks running into a race condition
				// where the reconcile loop finishes before the service is created, leading to no route being created.
				isvcService := getDefaultService(inferenceService.Namespace)
				if err := k8sClient.Create(ctx, isvcService); err != nil && !k8sErrors.IsAlreadyExists(err) {
					Expect(err).NotTo(HaveOccurred())
				}
				service := &corev1.Service{}
				Eventually(func() error {
					key := types.NamespacedName{Name: isvcService.Name, Namespace: isvcService.Namespace}
					return k8sClient.Get(ctx, key, service)
				}, timeout, interval).Should(Succeed())
				_ = createServingRuntime(testNs, KserveServingRuntimePath1)
				if err := k8sClient.Create(ctx, inferenceService); err != nil && !k8sErrors.IsAlreadyExists(err) {
					Expect(err).NotTo(HaveOccurred())
				}

				// Checking that the controller has created the Route with the haproxy.router.openshift.io/timeout annotation added
				Eventually(func() error {
					return checkRouteTimeout(types.NamespacedName{Name: inferenceService.Name, Namespace: inferenceService.Namespace}, "90s")
				}, timeout, interval).Should(Succeed())
			})

			It("it should not delete the route that is not owned by the isvc - manual created routes should not be deleted", func() {
				inferenceService := createInferenceService(testNs, KserveOvmsInferenceServiceName, KserveInferenceServicePath1)
				// The service is manually created before the isvc otherwise the unit test risks running into a race condition
				// where the reconcile loop finishes before the service is created, leading to no route being created.
				isvcService := getDefaultService(inferenceService.Namespace)
				if err := k8sClient.Create(ctx, isvcService); err != nil && !k8sErrors.IsAlreadyExists(err) {
					Expect(err).NotTo(HaveOccurred())
				}

				service := &corev1.Service{}
				Eventually(func() error {
					key := types.NamespacedName{Name: isvcService.Name, Namespace: isvcService.Namespace}
					return k8sClient.Get(ctx, key, service)
				}, timeout, interval).Should(Succeed())

				_ = createServingRuntime(testNs, KserveServingRuntimePath1)
				if err := k8sClient.Create(ctx, inferenceService); err != nil && !k8sErrors.IsAlreadyExists(err) {
					Expect(err).NotTo(HaveOccurred())
				}

				// create a route with the same name than the isvc
				userRoute := &routev1.Route{
					ObjectMeta: metav1.ObjectMeta{
						Name:      inferenceService.Name,
						Namespace: inferenceService.Namespace,
					},
					Spec: routev1.RouteSpec{
						To: routev1.RouteTargetReference{
							Kind:   "Service",
							Name:   isvcService.Name,
							Weight: ptr.To(int32(100)),
						},
						Port: &routev1.RoutePort{
							TargetPort: isvcService.Spec.Ports[0].TargetPort,
						},
						WildcardPolicy: routev1.WildcardPolicyNone,
					},
					Status: routev1.RouteStatus{
						Ingress: []routev1.RouteIngress{},
					},
				}
				Expect(k8sClient.Create(ctx, userRoute)).Should(Succeed())

				// check if the route was created
				route := &routev1.Route{}
				Eventually(func() bool {
					key := types.NamespacedName{Name: inferenceService.Name, Namespace: inferenceService.Namespace}
					_ = k8sClient.Get(ctx, key, route)
					return route.Name == inferenceService.Name
				}, timeout, interval).Should(BeTrue())

				// delete isvc
				Expect(k8sClient.Delete(ctx, inferenceService)).Should(Succeed())

				// make sure isvc is gone
				Eventually(func() bool {
					key := types.NamespacedName{Name: inferenceService.Name, Namespace: inferenceService.Namespace}
					err := k8sClient.Get(ctx, key, &kservev1beta1.InferenceService{})
					return k8sErrors.IsNotFound(err)
				}, timeout, interval).Should(BeTrue())

				// route should remain
				route2 := &routev1.Route{}
				Eventually(func() bool {
					key := types.NamespacedName{Name: inferenceService.Name, Namespace: inferenceService.Namespace}
					_ = k8sClient.Get(ctx, key, route2)
					return route.Name == inferenceService.Name && route.Spec.To.Name == fmt.Sprintf("%s-predictor", inferenceService.Name)
				}, timeout, interval).Should(BeTrue())
			})

			It("it should create a metrics service and servicemonitor auth", func() {
				_ = createServingRuntime(testNs, KserveServingRuntimePath1)
				inferenceService := createInferenceService(testNs, KserveOvmsInferenceServiceName, KserveInferenceServicePath1)
				if err := k8sClient.Create(ctx, inferenceService); err != nil {
					Expect(err).NotTo(HaveOccurred())
				}
				metricsService := &corev1.Service{}
				Eventually(func() error {
					key := types.NamespacedName{Name: inferenceService.Name + "-metrics", Namespace: inferenceService.Namespace}
					return k8sClient.Get(ctx, key, metricsService)
				}, timeout, interval).Should(Succeed())

				serviceMonitor := &monitoringv1.ServiceMonitor{}
				Eventually(func() error {
					key := types.NamespacedName{Name: inferenceService.Name + "-metrics", Namespace: inferenceService.Namespace}
					return k8sClient.Get(ctx, key, serviceMonitor)
				}, timeout, interval).Should(Succeed())

				Expect(serviceMonitor.Spec.Selector.MatchLabels).To(HaveKeyWithValue("name", inferenceService.Name+"-metrics"))
			})
		})
		When("deleting a Kserve RawDeployment model", func() {
			It("the associated route should be deleted", func() {
				_ = createServingRuntime(testNs, KserveServingRuntimePath1)
				inferenceService := createInferenceService(testNs, KserveOvmsInferenceServiceName, KserveInferenceServicePath1)
				if err := k8sClient.Create(ctx, inferenceService); err != nil && !k8sErrors.IsAlreadyExists(err) {
					Expect(err).NotTo(HaveOccurred())
				}

				Expect(k8sClient.Delete(ctx, inferenceService)).Should(Succeed())

				route := &routev1.Route{}
				Eventually(func() error {
					key := types.NamespacedName{Name: inferenceService.Name, Namespace: inferenceService.Namespace}
					return k8sClient.Get(ctx, key, route)
				}, timeout, interval).Should(HaveOccurred())
			})
			It("the associated metrics service and servicemonitor should be deleted", func() {
				_ = createServingRuntime(testNs, KserveServingRuntimePath1)
				inferenceService := createInferenceService(testNs, KserveOvmsInferenceServiceName, KserveInferenceServicePath1)
				if err := k8sClient.Create(ctx, inferenceService); err != nil {
					Expect(err).NotTo(HaveOccurred())
				}

				Expect(k8sClient.Delete(ctx, inferenceService)).Should(Succeed())

				metricsService := &corev1.Service{}
				Eventually(func() error {
					key := types.NamespacedName{Name: inferenceService.Name, Namespace: inferenceService.Namespace}
					return k8sClient.Get(ctx, key, metricsService)
				}, timeout, interval).Should(HaveOccurred())

				serviceMonitor := &monitoringv1.ServiceMonitor{}
				Eventually(func() error {
					key := types.NamespacedName{Name: inferenceService.Name, Namespace: inferenceService.Namespace}
					return k8sClient.Get(ctx, key, serviceMonitor)
				}, timeout, interval).Should(HaveOccurred())
			})
			It("CRB is deleted only when all associated isvcs are deleted", func() {
				customServiceAccountName := "custom-sa"
				_ = createServingRuntime(testNs, KserveServingRuntimePath1)
				// create 2 isvcs with no SA (i.e default) and 2 with a custom SA
				defaultIsvc1 := createInferenceService(testNs, "default-1", KserveInferenceServicePath1)
				defaultIsvc1.Annotations[constants.EnableAuthODHAnnotation] = "true"
				if err := k8sClient.Create(ctx, defaultIsvc1); err != nil {
					Expect(err).NotTo(HaveOccurred())
				}
				defaultIsvc2 := createInferenceService(testNs, "default-2", KserveInferenceServicePath1)
				defaultIsvc2.Annotations[constants.EnableAuthODHAnnotation] = "true"
				if err := k8sClient.Create(ctx, defaultIsvc2); err != nil {
					Expect(err).NotTo(HaveOccurred())
				}
				customIsvc1 := createInferenceService(testNs, "custom-1", KserveInferenceServicePath1)
				customIsvc1.Annotations[constants.EnableAuthODHAnnotation] = "true"
				customIsvc1.Spec.Predictor.ServiceAccountName = customServiceAccountName
				if err := k8sClient.Create(ctx, customIsvc1); err != nil {
					Expect(err).NotTo(HaveOccurred())
				}
				customIsvc2 := createInferenceService(testNs, "custom-2", KserveInferenceServicePath1)
				customIsvc2.Annotations[constants.EnableAuthODHAnnotation] = "true"
				customIsvc2.Spec.Predictor.ServiceAccountName = customServiceAccountName
				if err := k8sClient.Create(ctx, customIsvc2); err != nil {
					Expect(err).NotTo(HaveOccurred())
				}
				// confirm that default CRB exists
				crb := &rbacv1.ClusterRoleBinding{}
				Eventually(func() error {
					key := types.NamespacedName{Name: defaultIsvc1.Namespace + "-" + constants.KserveServiceAccountName + "-auth-delegator",
						Namespace: defaultIsvc1.Namespace}
					return k8sClient.Get(ctx, key, crb)
				}, timeout, interval).ShouldNot(HaveOccurred())
				// confirm that custom CRB exists
				customCrb := &rbacv1.ClusterRoleBinding{}
				Eventually(func() error {
					key := types.NamespacedName{Name: defaultIsvc1.Namespace + "-" + customServiceAccountName + "-auth-delegator",
						Namespace: defaultIsvc1.Namespace}
					return k8sClient.Get(ctx, key, customCrb)
				}, timeout, interval).ShouldNot(HaveOccurred())

				// Delete isvc and isvc2 (one with default SA and one with custom SA)
				Expect(k8sClient.Delete(ctx, defaultIsvc1)).Should(Succeed())
				Expect(k8sClient.Delete(ctx, customIsvc1)).Should(Succeed())

				// confirm that CRBs are not deleted
				Consistently(func() error {
					key := types.NamespacedName{Name: defaultIsvc1.Namespace + "-" + constants.KserveServiceAccountName + "-auth-delegator",
						Namespace: defaultIsvc1.Namespace}
					return k8sClient.Get(ctx, key, crb)
				}, timeout, interval).ShouldNot(HaveOccurred())
				Consistently(func() error {
					key := types.NamespacedName{Name: defaultIsvc1.Namespace + "-" + customServiceAccountName + "-auth-delegator",
						Namespace: defaultIsvc1.Namespace}
					return k8sClient.Get(ctx, key, customCrb)
				}, timeout, interval).ShouldNot(HaveOccurred())

				// Delete rest of the isvcs
				Expect(k8sClient.Delete(ctx, defaultIsvc2)).Should(Succeed())
				Expect(k8sClient.Delete(ctx, customIsvc2)).Should(Succeed())

				crblist := &rbacv1.ClusterRoleBindingList{}
				listOpts := client.ListOptions{Namespace: testNs}
				if err := k8sClient.List(ctx, crblist, &listOpts); err != nil {
					Fail(err.Error())
				}

				Eventually(func() error {
					crb := &rbacv1.ClusterRoleBinding{}
					key := types.NamespacedName{Name: defaultIsvc1.Namespace + "-" + constants.KserveServiceAccountName + "-auth-delegator", Namespace: defaultIsvc2.Namespace}
					return k8sClient.Get(ctx, key, crb)
				}, timeout, interval).Should(HaveOccurred())
				Eventually(func() error {
					customCrb := &rbacv1.ClusterRoleBinding{}
					key := types.NamespacedName{Name: defaultIsvc1.Namespace + "-" + customServiceAccountName + "-auth-delegator", Namespace: customIsvc2.Namespace}
					return k8sClient.Get(ctx, key, customCrb)
				}, timeout, interval).Should(HaveOccurred())
			})
		})
		When("namespace no longer has any RawDeployment models", func() {
			It("should delete the default clusterrolebinding", func() {
				_ = createServingRuntime(testNs, KserveServingRuntimePath1)
				inferenceService := createInferenceService(testNs, KserveOvmsInferenceServiceName, KserveInferenceServicePath1)
				if err := k8sClient.Create(ctx, inferenceService); err != nil && !k8sErrors.IsAlreadyExists(err) {
					Expect(err).NotTo(HaveOccurred())
				}
				Expect(k8sClient.Delete(ctx, inferenceService)).Should(Succeed())
				crb := &rbacv1.ClusterRoleBinding{}
				Eventually(func() error {
					namespacedNamed := types.NamespacedName{Name: testNs + "-" + constants.KserveServiceAccountName + "-auth-delegator", Namespace: WorkingNamespace}
					err := k8sClient.Get(ctx, namespacedNamed, crb)
					if k8sErrors.IsNotFound(err) {
						return nil
					} else {
						return errors.New("crb deletion not detected")
					}
				}, timeout, interval).ShouldNot(HaveOccurred())
			})
		})
	})

	Describe("KServe KEDA Reconciler", func() {
		var (
			testNs         string
			kedaReconciler *reconcilers.KserveKEDAReconciler
		)

		BeforeEach(func() {
			testNs = testutils.Namespaces.Create(ctx, k8sClient).Name
			kedaReconciler = reconcilers.NewKServeKEDAReconciler(k8sClient)
		})

		Context("when InferenceServices are configured to scale on KEDA metrics", func() {
			var isvc *kservev1beta1.InferenceService
			var isvc2 *kservev1beta1.InferenceService

			const fake = "fake" // for the love of the linter :)

			BeforeEach(func() {
				isvc = makeKedaTestISVC(testNs, names.SimpleNameGenerator.GenerateName("keda-isvc"), true)
				Expect(k8sClient.Create(ctx, isvc)).Should(Succeed())
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: isvc.Name, Namespace: isvc.Namespace}, isvc)).Should(Succeed())

				isvc2 = makeKedaTestISVC(testNs, names.SimpleNameGenerator.GenerateName("keda-isvc-2"), true)
				Expect(k8sClient.Create(ctx, isvc2)).Should(Succeed())
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: isvc2.Name, Namespace: isvc2.Namespace}, isvc2)).Should(Succeed())

				// Safety checks
				Expect(isvc.Spec.Predictor.AutoScaling).ToNot(BeNil())
				Expect(isvc2.Spec.Predictor.AutoScaling).ToNot(BeNil())
				Expect(isvc.ObjectMeta.UID).ToNot(BeEmpty())
				Expect(isvc2.ObjectMeta.UID).ToNot(BeEmpty())

				Expect(kedaReconciler.Reconcile(ctx, GinkgoLogr, isvc)).NotTo(HaveOccurred())
				Expect(kedaReconciler.Reconcile(ctx, GinkgoLogr, isvc2)).NotTo(HaveOccurred())
			})

			It("Must have owner reference to InferenceService", func() {
				for _, obj := range getAllKedaTestResources(ctx, k8sClient, testNs) {
					Expect(obj).To(Not(BeNil()), fmt.Sprintf("Obj: %#v", obj))
					Expect(obj).To(HaveOwnerReferenceByUID(isvc.UID))
					Expect(obj).To(HaveOwnerReferenceByUID(isvc2.UID))
				}
			})

			It("Must remove owner reference from deleted InferenceService", func() {
				Expect(k8sClient.Delete(ctx, isvc2)).Should(Succeed())
				Expect(kedaReconciler.Delete(ctx, GinkgoLogr, isvc2)).To(Succeed())

				for _, obj := range getAllKedaTestResources(ctx, k8sClient, testNs) {
					Expect(obj).To(Not(BeNil()), fmt.Sprintf("Obj: %#v", obj))
					Expect(obj).To(HaveOwnerReferenceByUID(isvc.UID))
					Expect(obj).ToNot(HaveOwnerReferenceByUID(isvc2.UID))
				}
			})

			It("Must update managed role when it diverges", func() {
				role := &rbacv1.Role{}
				Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: isvc.Namespace, Name: reconcilers.KEDAPrometheusAuthMetricsReaderRoleName}, role)).Should(Succeed())
				role.Rules[0].Resources = []string{fake}
				Expect(k8sClient.Update(ctx, role)).To(Succeed())

				Expect(kedaReconciler.Reconcile(ctx, GinkgoLogr, isvc)).To(Succeed())

				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(role), role)).Should(Succeed())

				Expect(role.Rules[0].Resources[0]).ToNot(Equal(fake))
			})

			It("Must update managed service account when it diverges", func() {
				sa := &corev1.ServiceAccount{}
				Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: isvc.Namespace, Name: reconcilers.KEDAPrometheusAuthMetricsReaderRoleName}, sa)).Should(Succeed())
				sa.Labels[reconcilers.KEDAResourcesLabelKey] = fake
				Expect(k8sClient.Update(ctx, sa)).To(Succeed())

				Expect(kedaReconciler.Reconcile(ctx, GinkgoLogr, isvc)).To(Succeed())

				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sa), sa)).Should(Succeed())

				Expect(sa.Labels[reconcilers.KEDAResourcesLabelKey]).To(Equal(reconcilers.KEDAResourcesLabelValue))
			})

			It("Must update managed role binding when it diverges", func() {
				rb := &rbacv1.RoleBinding{}
				Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: isvc.Namespace, Name: reconcilers.KEDAPrometheusAuthMetricsReaderRoleBindingName}, rb)).Should(Succeed())
				rb.Subjects[0].Kind = "User"
				rb.Subjects[0].Name = fake
				Expect(k8sClient.Update(ctx, rb)).To(Succeed())

				Expect(kedaReconciler.Reconcile(ctx, GinkgoLogr, isvc)).To(Succeed())

				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(rb), rb)).Should(Succeed())

				Expect(rb.Subjects[0].Kind).ToNot(Equal("User"))
				Expect(rb.Subjects[0].Name).ToNot(Equal(fake))
			})

			It("Must update managed secret when it diverges", func() {
				secret := &corev1.Secret{}
				Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: isvc.Namespace, Name: reconcilers.KEDAPrometheusAuthTriggerSecretName}, secret)).Should(Succeed())
				secret.Labels[reconcilers.KEDAResourcesLabelKey] = fake
				Expect(k8sClient.Update(ctx, secret)).To(Succeed())

				Expect(kedaReconciler.Reconcile(ctx, GinkgoLogr, isvc)).To(Succeed())

				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(secret), secret)).Should(Succeed())

				Expect(secret.Labels[reconcilers.KEDAResourcesLabelKey]).To(Equal(reconcilers.KEDAResourcesLabelValue))
			})

			It("Must update managed trigger authentication when it diverges", func() {
				ta := &kedaapi.TriggerAuthentication{}
				Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: isvc.Namespace, Name: reconcilers.KEDAPrometheusAuthTriggerAuthName}, ta)).Should(Succeed())
				ta.Spec.SecretTargetRef = []kedaapi.AuthSecretTargetRef{}
				Expect(k8sClient.Update(ctx, ta)).To(Succeed())

				Expect(kedaReconciler.Reconcile(ctx, GinkgoLogr, isvc)).To(Succeed())

				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(ta), ta)).Should(Succeed())

				Expect(ta.Spec.SecretTargetRef).To(HaveLen(2))
			})

			AfterEach(func() {
				Expect(k8sClient.Delete(ctx, isvc)).Should(Succeed())

				Expect(kedaReconciler.Delete(ctx, GinkgoLogr, isvc)).To(Succeed())
				Expect(kedaReconciler.Cleanup(ctx, GinkgoLogr, testNs)).To(Succeed())

				Expect(getAllKedaTestResources(ctx, k8sClient, testNs)).To(BeEmpty())
			})
		})

		Context("when an InferenceService no longer requires KEDA metrics", func() {
			var isvc *kservev1beta1.InferenceService

			BeforeEach(func() {
				// Create ISVC initially requiring KEDA
				isvc = makeKedaTestISVC(testNs, names.SimpleNameGenerator.GenerateName("keda-isvc"), true)
				Expect(k8sClient.Create(ctx, isvc)).Should(Succeed())
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: isvc.Name, Namespace: isvc.Namespace}, isvc)).Should(Succeed())

				err := kedaReconciler.Reconcile(ctx, GinkgoLogr, isvc)
				Expect(err).NotTo(HaveOccurred())

				// Verify initial ownership
				for _, obj := range getAllKedaTestResources(ctx, k8sClient, testNs) {
					Expect(obj).To(Not(BeNil()), fmt.Sprintf("Obj: %#v", obj))
					Expect(obj).To(HaveOwnerReferenceByUID(isvc.UID))
				}

				// Update ISVC to no longer require KEDA
				// Fetch the latest version first
				latestIsvc := &kservev1beta1.InferenceService{}
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: isvc.Name, Namespace: isvc.Namespace}, latestIsvc)).Should(Succeed())

				updatedIsvc := makeKedaTestISVC(testNs, isvc.Name, false)
				latestIsvc.Spec = updatedIsvc.Spec // Only update spec
				Expect(k8sClient.Update(ctx, latestIsvc)).Should(Succeed())
				isvc = latestIsvc // Use the updated ISVC for the Reconcile call
			})

			It("should remove the ISVC owner reference from all KEDA resources", func() {
				err := kedaReconciler.Reconcile(ctx, GinkgoLogr, isvc)
				Expect(err).NotTo(HaveOccurred())

				// Verify owner reference is removed
				for _, obj := range getAllKedaTestResources(ctx, k8sClient, testNs) {
					// Refetch the object to get its latest state
					currentObj := obj.DeepCopyObject().(client.Object)
					Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(currentObj), currentObj)).Should(Succeed())
					Expect(obj).ToNot(HaveOwnerReferenceByUID(isvc.UID))
				}
			})
		})

		Context("when an ISVC no longer requires KEDA and resources have other owners", func() {
			var isvc1 *kservev1beta1.InferenceService
			var otherOwnerRef *metav1.OwnerReference

			BeforeEach(func() {
				isvc1 = makeKedaTestISVC(testNs, "isvc1-keda", true)
				Expect(k8sClient.Create(ctx, isvc1)).Should(Succeed())

				// Create a second InferenceService so that the resources are not deleted.
				isvc2 := makeKedaTestISVC(testNs, "isvc2-keda", true)
				Expect(k8sClient.Create(ctx, isvc2)).Should(Succeed())

				// Dummy "other" owner
				otherOwnerRef = &metav1.OwnerReference{
					APIVersion: "v1",
					Kind:       "Pod", // Arbitrary kind for testing
					Name:       "other-owner-pod",
					UID:        "other-owner-uid",
				}

				// Create KEDA resources owned by ISVC1 and otherOwner
				_ = createTestKedaSA(ctx, k8sClient, testNs, isvc1, otherOwnerRef)
				_ = createTestKedaSecret(ctx, k8sClient, testNs, isvc1, otherOwnerRef)
				_ = createTestKedaRole(ctx, k8sClient, testNs, isvc1, otherOwnerRef)
				_ = createTestKedaRoleBinding(ctx, k8sClient, testNs, isvc1, otherOwnerRef)
				_ = createTestKedaTA(ctx, k8sClient, testNs, isvc1, otherOwnerRef)

				// Update ISVC1 to no longer require KEDA
				latestIsvc1 := &kservev1beta1.InferenceService{}
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: isvc1.Name, Namespace: isvc1.Namespace}, latestIsvc1)).Should(Succeed())
				updatedIsvc1 := makeKedaTestISVC(testNs, "isvc1-keda", false)
				latestIsvc1.Spec = updatedIsvc1.Spec
				Expect(k8sClient.Update(ctx, latestIsvc1)).Should(Succeed())
				isvc1 = latestIsvc1
			})

			It("should remove only the specific ISVC's owner reference, preserving others", func() {
				err := kedaReconciler.Reconcile(ctx, GinkgoLogr, isvc1)
				Expect(err).NotTo(HaveOccurred())

				sa := &corev1.ServiceAccount{}
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: reconcilers.KEDAPrometheusAuthResourceName, Namespace: testNs}, sa)).Should(Succeed())
				Expect(sa).ToNot(HaveOwnerReferenceByUID(isvc1.UID))
				Expect(sa).To(HaveOwnerReferenceByUID(otherOwnerRef.UID))

				secret := &corev1.Secret{}
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: reconcilers.KEDAPrometheusAuthTriggerSecretName, Namespace: testNs}, secret)).Should(Succeed())
				Expect(secret).ToNot(HaveOwnerReferenceByUID(isvc1.UID))
				Expect(secret).To(HaveOwnerReferenceByUID(otherOwnerRef.UID))

				ta := &kedaapi.TriggerAuthentication{}
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: reconcilers.KEDAPrometheusAuthTriggerAuthName, Namespace: testNs}, ta)).Should(Succeed())
				Expect(ta).ToNot(HaveOwnerReferenceByUID(isvc1.UID))
				Expect(ta).To(HaveOwnerReferenceByUID(otherOwnerRef.UID))

				role := &rbacv1.Role{}
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: reconcilers.KEDAPrometheusAuthMetricsReaderRoleName, Namespace: testNs}, role)).Should(Succeed())
				Expect(role).ToNot(HaveOwnerReferenceByUID(isvc1.UID))
				Expect(role).To(HaveOwnerReferenceByUID(otherOwnerRef.UID))

				roleBinding := &rbacv1.RoleBinding{}
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: reconcilers.KEDAPrometheusAuthMetricsReaderRoleBindingName, Namespace: testNs}, roleBinding)).Should(Succeed())
				Expect(roleBinding).ToNot(HaveOwnerReferenceByUID(isvc1.UID))
				Expect(roleBinding).To(HaveOwnerReferenceByUID(otherOwnerRef.UID))
			})
		})

		Context("when KEDA resources do not exist and autoscaling is not configured", func() {
			var isvc *kservev1beta1.InferenceService

			BeforeEach(func() {
				isvc = makeKedaTestISVC(testNs, "isvc-no-keda-res", false)
				Expect(k8sClient.Create(ctx, isvc)).Should(Succeed())
			})

			It("should complete without error (no-op)", func() {
				err := kedaReconciler.Reconcile(ctx, GinkgoLogr, isvc)
				Expect(err).NotTo(HaveOccurred())

				sa := &corev1.ServiceAccount{}
				err = k8sClient.Get(ctx, types.NamespacedName{Name: reconcilers.KEDAPrometheusAuthResourceName, Namespace: testNs}, sa)
				Expect(err).To(HaveOccurred())
				Expect(k8sErrors.IsNotFound(err)).To(BeTrue())

				secret := &corev1.Secret{}
				err = k8sClient.Get(ctx, types.NamespacedName{Name: reconcilers.KEDAPrometheusAuthResourceName, Namespace: testNs}, secret)
				Expect(err).To(HaveOccurred())
				Expect(k8sErrors.IsNotFound(err)).To(BeTrue())

				ta := &kedaapi.TriggerAuthentication{}
				err = k8sClient.Get(ctx, types.NamespacedName{Name: reconcilers.KEDAPrometheusAuthResourceName, Namespace: testNs}, ta)
				Expect(err).To(HaveOccurred())
				Expect(k8sErrors.IsNotFound(err)).To(BeTrue())

				role := &rbacv1.Role{}
				err = k8sClient.Get(ctx, types.NamespacedName{Name: reconcilers.KEDAPrometheusAuthResourceName, Namespace: testNs}, role)
				Expect(err).To(HaveOccurred())
				Expect(k8sErrors.IsNotFound(err)).To(BeTrue())

				roleBinding := &rbacv1.RoleBinding{}
				err = k8sClient.Get(ctx, types.NamespacedName{Name: reconcilers.KEDAPrometheusAuthResourceName, Namespace: testNs}, roleBinding)
				Expect(err).To(HaveOccurred())
				Expect(k8sErrors.IsNotFound(err)).To(BeTrue())
			})
		})

		Context("when KEDA resource does not have the target ISVC as an owner and autoscaling is not configured", func() {
			var isvc *kservev1beta1.InferenceService
			var sa *corev1.ServiceAccount

			BeforeEach(func() {
				isvc = makeKedaTestISVC(testNs, "isvc-not-owner", false)
				Expect(k8sClient.Create(ctx, isvc)).Should(Succeed())

				// Create a second InferenceService so that the resources are not deleted.
				isvc2 := makeKedaTestISVC(testNs, "isvc2-keda", true)
				Expect(k8sClient.Create(ctx, isvc2)).Should(Succeed())

				// Create SA but not owned by this ISVC
				sa = createTestKedaSA(ctx, k8sClient, testNs, nil, nil) // No owners
			})

			It("should complete without error and not modify the resource's owners", func() {
				originalSAOwnerRefs := sa.GetOwnerReferences()
				err := kedaReconciler.Reconcile(ctx, GinkgoLogr, isvc)
				Expect(err).NotTo(HaveOccurred())

				updatedSA := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: reconcilers.KEDAPrometheusAuthServiceAccountName, Namespace: testNs}}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sa), updatedSA)).Should(Succeed())
				Expect(updatedSA.GetOwnerReferences()).To(Equal(originalSAOwnerRefs)) // Should be unchanged
			})
		})

		Context("when ISVC does require KEDA resources", func() {
			var isvc *kservev1beta1.InferenceService
			var sa *corev1.ServiceAccount

			BeforeEach(func() {
				isvc = makeKedaTestISVC(testNs, "isvc-not-owner", false)
				Expect(k8sClient.Create(ctx, isvc)).Should(Succeed())

				// Create SA
				sa = createTestKedaSA(ctx, k8sClient, testNs, nil, nil) // No owners
			})

			It("should cleanup resources", func() {
				err := kedaReconciler.Reconcile(ctx, GinkgoLogr, isvc)
				Expect(err).NotTo(HaveOccurred())

				updatedSA := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: reconcilers.KEDAPrometheusAuthServiceAccountName, Namespace: testNs}}
				err = k8sClient.Get(ctx, client.ObjectKeyFromObject(sa), updatedSA)
				Expect(err).Should(HaveOccurred())
				Expect(k8sErrors.IsNotFound(err)).Should(BeTrue())
			})
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

func waitForUpdatedGatewayCompletion(cli client.Client, op string, namespace, gatewayName string, isvcName string) (*istioclientv1beta1.Gateway, error) {
	ctx := context.Background()
	portName := fmt.Sprintf("%s-%s", "https", isvcName)
	gateway := &istioclientv1beta1.Gateway{}

	// Get the Gateway resource
	err := cli.Get(ctx, client.ObjectKey{Namespace: namespace, Name: gatewayName}, gateway)
	if err != nil {
		return nil, fmt.Errorf("failed to get Gateway: %w", err)
	}

	// Check conditions based on operation (op)
	switch op {
	case "add":
		if !hasServerFromGateway(gateway, portName) {
			return nil, fmt.Errorf("server %s not found in Gateway %s", portName, gatewayName)
		}
	case "delete":
		if hasServerFromGateway(gateway, portName) {
			return nil, fmt.Errorf("server %s still exists in Gateway %s", portName, gatewayName)
		}
	default:
		return nil, fmt.Errorf("unsupported operation: %s", op)
	}

	return gateway, nil
}

// checks if the server exists for the given gateway
func hasServerFromGateway(gateway *istioclientv1beta1.Gateway, portName string) bool {
	targetServerExist := false
	for _, server := range gateway.Spec.Servers {
		if server.Port.Name == portName {
			targetServerExist = true
			break
		}
	}
	return targetServerExist
}

func getAuthConfig(namespace, name string, ac *authorinov1beta2.AuthConfig) error {
	return k8sClient.Get(context.Background(), types.NamespacedName{Namespace: namespace, Name: name}, ac)
}

func createISVCMissingStatus(namespace string) *kservev1beta1.InferenceService {
	inferenceService := &kservev1beta1.InferenceService{}
	err := testutils.ConvertToStructuredResource(KserveInferenceServicePath1, inferenceService)
	Expect(err).NotTo(HaveOccurred())
	inferenceService.Namespace = namespace
	Expect(k8sClient.Create(ctx, inferenceService)).Should(Succeed())
	return inferenceService
}

func createISVCWithAuth(namespace string) *kservev1beta1.InferenceService {
	inferenceService := createBasicISVC(namespace)
	inferenceService.Annotations[constants.LabelEnableAuth] = "true"
	Expect(k8sClient.Create(ctx, inferenceService)).Should(Succeed())

	return inferenceService
}

func createISVCWithoutAuth(namespace string) *kservev1beta1.InferenceService {
	inferenceService := createBasicISVC(namespace)
	Expect(k8sClient.Create(ctx, inferenceService)).Should(Succeed())

	return inferenceService
}

func createBasicISVC(namespace string) *kservev1beta1.InferenceService {
	inferenceService := &kservev1beta1.InferenceService{}
	err := testutils.ConvertToStructuredResource(KserveInferenceServicePath1, inferenceService)
	Expect(err).NotTo(HaveOccurred())
	inferenceService.Namespace = namespace
	if inferenceService.Annotations == nil {
		inferenceService.Annotations = map[string]string{}
	}
	return inferenceService
}

func updateISVCStatus(isvc *kservev1beta1.InferenceService) error {
	latestISVC := isvc.DeepCopy()
	// Construct the URL and update the status
	url, _ := apis.ParseURL("http://iscv-" + isvc.Namespace + "ns.apps.openshift.ai")
	latestISVC.Status.URL = url
	// Patch the status to avoid conflicts
	err := k8sClient.Status().Patch(context.Background(), latestISVC, client.MergeFrom(isvc))
	if err != nil {
		if k8sErrors.IsConflict(err) {
			// Retry on conflict
			return updateISVCStatus(isvc)
		}
		return err
	}
	return nil
}

func disableAuth(isvc *kservev1beta1.InferenceService) error {
	// Retrieve the latest version of the InferenceService
	latestISVC := &kservev1beta1.InferenceService{}
	err := k8sClient.Get(context.Background(), types.NamespacedName{
		Name:      isvc.Name,
		Namespace: isvc.Namespace,
	}, latestISVC)
	if err != nil {
		return err
	}
	delete(latestISVC.Annotations, constants.LabelEnableAuth)
	delete(latestISVC.Annotations, constants.EnableAuthODHAnnotation)
	return k8sClient.Update(context.Background(), latestISVC)
}

func enableAuth(isvc *kservev1beta1.InferenceService) error {
	// Retrieve the latest version of the InferenceService
	latestISVC := &kservev1beta1.InferenceService{}
	err := k8sClient.Get(context.Background(), types.NamespacedName{
		Name:      isvc.Name,
		Namespace: isvc.Namespace,
	}, latestISVC)
	if err != nil {
		return err
	}
	if latestISVC.Annotations == nil {
		latestISVC.Annotations = map[string]string{}
	}
	latestISVC.Annotations[constants.EnableAuthODHAnnotation] = "true"
	return k8sClient.Update(context.Background(), latestISVC)
}

func createDSCI(_ string) error {
	dsci := DSCIWithoutAuthorization
	obj := &unstructured.Unstructured{}
	if err := testutils.ConvertToUnstructuredResource(dsci, obj); err != nil {
		return err
	}

	gvk := utils.GVK.DataScienceClusterInitialization
	obj.SetGroupVersionKind(gvk)
	dynamicClient, err := dynamic.NewForConfig(testEnv.Config)
	if err != nil {
		return err
	}

	gvr := schema.GroupVersionResource{
		Group:    gvk.Group,
		Version:  gvk.Version,
		Resource: "dscinitializations",
	}
	resource := dynamicClient.Resource(gvr)
	createdObj, createErr := resource.Create(context.TODO(), obj, metav1.CreateOptions{})
	if createErr != nil {
		return nil
	}

	if status, found, err := unstructured.NestedFieldCopy(obj.Object, "status"); err != nil {
		return err
	} else if found {
		if err := unstructured.SetNestedField(createdObj.Object, status, "status"); err != nil {
			return err
		}
	}

	_, statusErr := resource.UpdateStatus(context.TODO(), createdObj, metav1.UpdateOptions{})

	return statusErr
}

func createAuthorizationPolicy(authPolicyFile string) error {
	obj := &unstructured.Unstructured{}
	if err := testutils.ConvertToUnstructuredResource(authPolicyFile, obj); err != nil {
		return err
	}

	obj.SetGroupVersionKind(istiosecv1b1.SchemeGroupVersion.WithKind("AuthorizationPolicy"))
	dynamicClient, err := dynamic.NewForConfig(testEnv.Config)
	if err != nil {
		return err
	}

	gvr := istiosecv1b1.SchemeGroupVersion.WithResource("authorizationpolicies")
	resource := dynamicClient.Resource(gvr)
	_, meshNamespace := utils.GetIstioControlPlaneName(ctx, k8sClient)
	_, createErr := resource.Namespace(meshNamespace).Create(context.TODO(), obj, metav1.CreateOptions{})

	return createErr
}

func deleteDSCI(dsci string) error {
	obj := &unstructured.Unstructured{}
	if err := testutils.ConvertToUnstructuredResource(dsci, obj); err != nil {
		return err
	}

	gvk := utils.GVK.DataScienceClusterInitialization
	obj.SetGroupVersionKind(gvk)
	dynamicClient, err := dynamic.NewForConfig(testEnv.Config)
	if err != nil {
		return err
	}

	gvr := schema.GroupVersionResource{
		Group:    gvk.Group,
		Version:  gvk.Version,
		Resource: "dscinitializations",
	}
	return dynamicClient.Resource(gvr).Delete(context.TODO(), obj.GetName(), metav1.DeleteOptions{})
}

func deleteAuthorizationPolicy(authPolicyFile string) error {
	obj := &unstructured.Unstructured{}
	if err := testutils.ConvertToUnstructuredResource(authPolicyFile, obj); err != nil {
		return err
	}

	obj.SetGroupVersionKind(istiosecv1b1.SchemeGroupVersion.WithKind("AuthorizationPolicy"))
	dynamicClient, err := dynamic.NewForConfig(testEnv.Config)
	if err != nil {
		return err
	}

	gvr := istiosecv1b1.SchemeGroupVersion.WithResource("authorizationpolicies")
	_, meshNamespace := utils.GetIstioControlPlaneName(ctx, k8sClient)
	err = dynamicClient.Resource(gvr).Namespace(meshNamespace).Delete(context.TODO(), obj.GetName(), metav1.DeleteOptions{})
	return client.IgnoreNotFound(err)
}

func getDefaultService(isvcNamespace string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      KserveOvmsInferenceServiceName + "-predictor",
			Namespace: isvcNamespace,
			Annotations: map[string]string{
				"openshift.io/display-name":        KserveOvmsInferenceServiceName,
				"serving.kserve.io/deploymentMode": "RawDeployment",
			},
			Labels: map[string]string{
				"app":                                "isvc." + KserveOvmsInferenceServiceName + "-predictor",
				"component":                          "predictor",
				"serving.kserve.io/inferenceservice": KserveOvmsInferenceServiceName,
			},
		},
		Spec: corev1.ServiceSpec{
			ClusterIP:  "None",
			IPFamilies: []corev1.IPFamily{"IPv4"},
			Ports: []corev1.ServicePort{
				{
					Name:       "http",
					Protocol:   corev1.ProtocolTCP,
					Port:       8888,
					TargetPort: intstr.FromString("http"),
				},
			},
			ClusterIPs: []string{"None"},
			Selector: map[string]string{
				"app": "isvc." + KserveOvmsInferenceServiceName + "-predictor",
			},
		},
	}
}

func checkRouteTimeout(key types.NamespacedName, expectedValue string) error {
	route := &routev1.Route{}
	err := k8sClient.Get(ctx, key, route)
	if err != nil {
		return err
	}
	val, found := route.Annotations[constants.RouteTimeoutAnnotationKey]
	if !found {
		return fmt.Errorf("%s annotation not present on route %s", constants.RouteTimeoutAnnotationKey, route.Name)
	}
	if val != expectedValue {
		return fmt.Errorf(
			"%s annotation on route %s has value %s, but expecting %s",
			constants.RouteTimeoutAnnotationKey,
			route.Name,
			val,
			expectedValue,
		)
	}
	return nil
}
