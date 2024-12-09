package controllers

import (
	"time"

	"github.com/opendatahub-io/odh-model-controller/controllers/constants"
	"github.com/opendatahub-io/odh-model-controller/controllers/utils"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
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

var _ = Describe("The KServe Dashboard reconciler", func() {
	var testNs string

	createServingRuntime := func(namespace, path string) *kservev1alpha1.ServingRuntime {
		servingRuntime := &kservev1alpha1.ServingRuntime{}
		err := convertToStructuredResource(path, servingRuntime)
		Expect(err).NotTo(HaveOccurred())
		servingRuntime.SetNamespace(namespace)
		if err := cli.Create(ctx, servingRuntime); err != nil && !errors.IsAlreadyExists(err) {
			Fail(err.Error())
		}
		return servingRuntime
	}

	createInferenceService := func(namespace, name string, path string, isRaw ...bool) *kservev1beta1.InferenceService {
		inferenceService := &kservev1beta1.InferenceService{}
		err := convertToStructuredResource(path, inferenceService)
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
		if err := cli.Create(ctx, inferenceService); err != nil && !errors.IsAlreadyExists(err) {
			Fail(err.Error())
		}
		return inferenceService
	}

	verifyConfigMap := func(isvcName string, namespace string, supported bool, metricsData string) {
		metricsConfigMap, err := waitForConfigMap(cli, namespace, isvcName+constants.KserveMetricsConfigMapNameSuffix, 30, 1*time.Second)
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
		Expect(compareConfigMap(metricsConfigMap, expectedMetricsConfigMap)).Should(BeTrue())
		Expect(expectedMetricsConfigMap.Data).NotTo(HaveKeyWithValue("metrics", ContainSubstring("${REQUEST_RATE_INTERVAL}")))
	}

	BeforeEach(func() {
		testNs = Namespaces.Create(cli).Name

		inferenceServiceConfig := &corev1.ConfigMap{}
		Expect(convertToStructuredResource(InferenceServiceConfigPath1, inferenceServiceConfig)).To(Succeed())
		if err := cli.Create(ctx, inferenceServiceConfig); err != nil && !errors.IsAlreadyExists(err) {
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

		It("[serverless] if the isvc does not have a runtime specified, an unsupported metrics configmap should be created", func() {
			_ = createInferenceService(testNs, NilRuntimeInferenceServiceName, NilRuntimeInferenceServicePath)

			verifyConfigMap(NilRuntimeInferenceServiceName, testNs, false, "")
		})

		It("[raw] if the isvc does not have a runtime specified, an unsupported metrics configmap should be created", func() {
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
		It("[serverless] it should delete the associated configmap", func() {
			_ = createServingRuntime(testNs, KserveServingRuntimePath1)
			OvmsInferenceService := createInferenceService(testNs, KserveOvmsInferenceServiceName, KserveInferenceServicePath1)

			Expect(cli.Delete(ctx, OvmsInferenceService)).Should(Succeed())
			Eventually(func() error {
				configmap := &corev1.ConfigMap{}
				key := types.NamespacedName{Name: KserveOvmsInferenceServiceName + constants.KserveMetricsConfigMapNameSuffix, Namespace: OvmsInferenceService.Namespace}
				err := cli.Get(ctx, key, configmap)
				return err
			}, timeout, interval).ShouldNot(Succeed())

			_ = createServingRuntime(testNs, UnsupprtedMetricsServingRuntimePath)
			SklearnInferenceService := createInferenceService(testNs, UnsupportedMetricsInferenceServiceName, UnsupportedMetricsInferenceServicePath)

			Expect(cli.Delete(ctx, SklearnInferenceService)).Should(Succeed())
			Eventually(func() error {
				configmap := &corev1.ConfigMap{}
				key := types.NamespacedName{Name: UnsupportedMetricsInferenceServiceName + constants.KserveMetricsConfigMapNameSuffix, Namespace: SklearnInferenceService.Namespace}
				err := cli.Get(ctx, key, configmap)
				return err
			}, timeout, interval).ShouldNot(Succeed())
		})
		It("[raw] it should delete the associated configmap", func() {
			_ = createServingRuntime(testNs, KserveServingRuntimePath1)
			OvmsInferenceService := createInferenceService(testNs, KserveOvmsInferenceServiceName, KserveInferenceServicePath1, true)

			Expect(cli.Delete(ctx, OvmsInferenceService)).Should(Succeed())
			Eventually(func() error {
				configmap := &corev1.ConfigMap{}
				key := types.NamespacedName{Name: KserveOvmsInferenceServiceName + constants.KserveMetricsConfigMapNameSuffix, Namespace: OvmsInferenceService.Namespace}
				err := cli.Get(ctx, key, configmap)
				return err
			}, timeout, interval).ShouldNot(Succeed())

			_ = createServingRuntime(testNs, UnsupprtedMetricsServingRuntimePath)
			SklearnInferenceService := createInferenceService(testNs, UnsupportedMetricsInferenceServiceName, UnsupportedMetricsInferenceServicePath, true)

			Expect(cli.Delete(ctx, SklearnInferenceService)).Should(Succeed())
			Eventually(func() error {
				configmap := &corev1.ConfigMap{}
				key := types.NamespacedName{Name: UnsupportedMetricsInferenceServiceName + constants.KserveMetricsConfigMapNameSuffix, Namespace: SklearnInferenceService.Namespace}
				err := cli.Get(ctx, key, configmap)
				return err
			}, timeout, interval).ShouldNot(Succeed())
		})
	})
})
