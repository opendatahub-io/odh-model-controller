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
	"errors"
	"fmt"
	"time"

	kedaapi "github.com/kedacore/keda/v2/apis/keda/v1alpha1"
	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/serving/reconcilers"
	routev1 "github.com/openshift/api/route/v1"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apiserver/pkg/storage/names"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

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
)

var _ = Describe("InferenceService Controller", func() {

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
			It("[raw] if the runtime is supported for metrics, it should create a configmap with prometheus queries", func() {
				_ = createServingRuntime(testNs, KserveServingRuntimePath1)
				_ = createInferenceService(testNs, KserveOvmsInferenceServiceName, KserveInferenceServicePath1, true)

				verifyConfigMap(KserveOvmsInferenceServiceName, testNs, true, constants.OvmsMetricsData)
			})

			It("[raw] if the runtime is not supported for metrics, it should create a configmap with the unsupported config", func() {
				_ = createServingRuntime(testNs, UnsupprtedMetricsServingRuntimePath)
				_ = createInferenceService(testNs, UnsupportedMetricsInferenceServiceName, UnsupportedMetricsInferenceServicePath, true)

				verifyConfigMap(UnsupportedMetricsInferenceServiceName, testNs, false, "")
			})

			It("[raw] if the isvc does not have a runtime specified and there is no supported runtime, an unsupported metrics configmap should be created", func() {
				_ = createInferenceService(testNs, NilRuntimeInferenceServiceName, NilRuntimeInferenceServicePath, true)

				verifyConfigMap(NilRuntimeInferenceServiceName, testNs, false, "")
			})

			It("[raw] if the isvc does not have the model field specified, an unsupported metrics configmap should be created", func() {
				_ = createInferenceService(testNs, NilModelInferenceServiceName, NilModelInferenceServicePath, true)

				verifyConfigMap(NilModelInferenceServiceName, testNs, false, "")
			})
		})

		When("deleting the deployed models", func() {
			timeout10s := time.Second * 10
			interval4s := time.Second * 4
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
