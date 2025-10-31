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

package reconcilers

import (
	"strings"

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

var _ = Describe("KserveMetricsDashboardReconciler", func() {
	var scheme *runtime.Scheme

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(kservev1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(kservev1beta1.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
	})

	Describe("When deploying a Inference Service", func() {
		When("NIM Runtime is used", func() {
			It("should create ConfigMap with supported=true and NIM metrics", func(ctx SpecContext) {
				// Create NIM ServingRuntime
				servingRuntime := createServingRuntime("nim-runtime", map[string]string{
					utils.IsNimRuntimeAnnotation: "true",
				})

				// Create InferenceService using NIM runtime
				isvc := &kservev1beta1.InferenceService{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "nim-model",
						Namespace: "test-namespace",
					},
					Spec: kservev1beta1.InferenceServiceSpec{
						Predictor: kservev1beta1.PredictorSpec{
							Model: &kservev1beta1.ModelSpec{
								ModelFormat: kservev1beta1.ModelFormat{Name: "test-format"},
								Runtime:     ptr.To("nim-runtime"),
							},
						},
					},
				}

				// Create fake k8s client and reconciler
				client := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(isvc, servingRuntime).
					Build()
				reconciler := NewKserveMetricsDashboardReconciler(client)

				// Run reconciler
				err := reconciler.Reconcile(ctx, log.Log, isvc)
				Expect(err).NotTo(HaveOccurred())

				// Query k8s API to verify ConfigMap
				configMap := &corev1.ConfigMap{}
				err = client.Get(ctx, k8stypes.NamespacedName{
					Name:      isvc.Name + constants.KserveMetricsConfigMapNameSuffix,
					Namespace: isvc.Namespace,
				}, configMap)
				Expect(err).NotTo(HaveOccurred())

				// retrieve the config from the getMetrics func to compare results
				fromGetMetrics, _ := getMetricsData(servingRuntime)
				finaldata := utils.SubstituteVariablesInQueries(fromGetMetrics, isvc.Namespace, isvc.Name)

				Expect(configMap.Data["supported"]).To(Equal("true"))
				Expect(configMap.Data["metrics"]).To(Equal(finaldata))
				Expect(configMap.Data["metrics"]).To(ContainSubstring("nim-model"))
				Expect(configMap.Data["metrics"]).To(ContainSubstring("test-namespace"))
			})
		})

		When("OpenVINO Runtime is used", func() {
			It("should create ConfigMap with supported=true and OVMS metrics", func(ctx SpecContext) {
				// Create OVMS ServingRuntime
				servingRuntime := createServingRuntime("ovms-runtime", map[string]string{
					constants.KServeRuntimeAnnotation: constants.OvmsRuntimeName,
				})

				// Create InferenceService using OVMS runtime
				isvc := &kservev1beta1.InferenceService{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "ovms-model",
						Namespace: "test-namespace",
					},
					Spec: kservev1beta1.InferenceServiceSpec{
						Predictor: kservev1beta1.PredictorSpec{
							Model: &kservev1beta1.ModelSpec{
								ModelFormat: kservev1beta1.ModelFormat{Name: "openvino_ir"},
								Runtime:     ptr.To("ovms-runtime"),
							},
						},
					},
				}

				// Create fake k8s client and reconciler
				client := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(isvc, servingRuntime).
					Build()
				reconciler := NewKserveMetricsDashboardReconciler(client)

				// Run reconciler
				err := reconciler.Reconcile(ctx, log.Log, isvc)
				Expect(err).NotTo(HaveOccurred())

				// Query k8s API to verify ConfigMap
				configMap := &corev1.ConfigMap{}
				err = client.Get(ctx, k8stypes.NamespacedName{
					Name:      isvc.Name + constants.KserveMetricsConfigMapNameSuffix,
					Namespace: isvc.Namespace,
				}, configMap)
				Expect(err).NotTo(HaveOccurred())

				// retrieve the config from the getMetrics func to compare results
				fromGetMetrics, _ := getMetricsData(servingRuntime)
				finaldata := utils.SubstituteVariablesInQueries(fromGetMetrics, isvc.Namespace, isvc.Name)

				Expect(configMap.Data["supported"]).To(Equal("true"))
				Expect(configMap.Data["metrics"]).To(Equal(finaldata))
				Expect(configMap.Data["metrics"]).To(ContainSubstring("ovms-model"))
				Expect(configMap.Data["metrics"]).To(ContainSubstring("test-namespace"))
			})
		})

		When("vLLM Runtime is used", func() {
			It("should create ConfigMap with supported=true and vLLM metrics", func(ctx SpecContext) {
				// Create vLLM ServingRuntime
				servingRuntime := createServingRuntime("vllm-runtime", map[string]string{
					constants.KServeRuntimeAnnotation: constants.VllmRuntimeName,
				})

				// Create InferenceService using vLLM runtime
				isvc := &kservev1beta1.InferenceService{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "vllm-model",
						Namespace: "test-namespace",
					},
					Spec: kservev1beta1.InferenceServiceSpec{
						Predictor: kservev1beta1.PredictorSpec{
							Model: &kservev1beta1.ModelSpec{
								ModelFormat: kservev1beta1.ModelFormat{Name: "huggingface"},
								Runtime:     ptr.To("vllm-runtime"),
							},
						},
					},
				}

				// Create fake k8s client and reconciler
				client := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(isvc, servingRuntime).
					Build()
				reconciler := NewKserveMetricsDashboardReconciler(client)

				// Run reconciler
				err := reconciler.Reconcile(ctx, log.Log, isvc)
				Expect(err).NotTo(HaveOccurred())

				// Query k8s API to verify ConfigMap
				configMap := &corev1.ConfigMap{}
				err = client.Get(ctx, k8stypes.NamespacedName{
					Name:      isvc.Name + constants.KserveMetricsConfigMapNameSuffix,
					Namespace: isvc.Namespace,
				}, configMap)
				Expect(err).NotTo(HaveOccurred())

				// retrieve the config from the getMetrics func to compare results
				fromGetMetrics, _ := getMetricsData(servingRuntime)
				finaldata := utils.SubstituteVariablesInQueries(fromGetMetrics, isvc.Namespace, isvc.Name)

				Expect(configMap.Data["supported"]).To(Equal("true"))
				Expect(configMap.Data["metrics"]).To(Equal(finaldata))
				Expect(configMap.Data["metrics"]).To(ContainSubstring("vllm-model"))
				Expect(configMap.Data["metrics"]).To(ContainSubstring("test-namespace"))
			})
		})

		When("TGIS Runtime is used", func() {
			It("should create ConfigMap with supported=true and TGIS metrics", func(ctx SpecContext) {
				// Create TGIS ServingRuntime
				servingRuntime := createServingRuntime("tgis-runtime", map[string]string{
					constants.KServeRuntimeAnnotation: constants.TgisRuntimeName,
				})

				// Create InferenceService using TGIS runtime
				isvc := &kservev1beta1.InferenceService{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "tgis-model",
						Namespace: "test-namespace",
					},
					Spec: kservev1beta1.InferenceServiceSpec{
						Predictor: kservev1beta1.PredictorSpec{
							Model: &kservev1beta1.ModelSpec{
								ModelFormat: kservev1beta1.ModelFormat{Name: "pytorch"},
								Runtime:     ptr.To("tgis-runtime"),
							},
						},
					},
				}

				// Create fake k8s client and reconciler
				client := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(isvc, servingRuntime).
					Build()
				reconciler := NewKserveMetricsDashboardReconciler(client)

				// Run reconciler
				err := reconciler.Reconcile(ctx, log.Log, isvc)
				Expect(err).NotTo(HaveOccurred())

				// Query k8s API to verify ConfigMap
				configMap := &corev1.ConfigMap{}
				err = client.Get(ctx, k8stypes.NamespacedName{
					Name:      isvc.Name + constants.KserveMetricsConfigMapNameSuffix,
					Namespace: isvc.Namespace,
				}, configMap)
				Expect(err).NotTo(HaveOccurred())

				// retrieve the config from the getMetrics func to compare results
				fromGetMetrics, _ := getMetricsData(servingRuntime)
				finaldata := utils.SubstituteVariablesInQueries(fromGetMetrics, isvc.Namespace, isvc.Name)

				Expect(configMap.Data["supported"]).To(Equal("true"))
				Expect(configMap.Data["metrics"]).To(Equal(finaldata))
				Expect(configMap.Data["metrics"]).To(ContainSubstring("tgis-model"))
				Expect(configMap.Data["metrics"]).To(ContainSubstring("test-namespace"))
			})
		})

		When("no valid runtime annotations are set", func() {
			It("should create ConfigMap with supported=false", func(ctx SpecContext) {
				// Create ServingRuntime without valid annotations
				servingRuntime := createServingRuntime("generic-runtime", nil)

				// Create InferenceService using generic runtime
				isvc := &kservev1beta1.InferenceService{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "generic-model",
						Namespace: "test-namespace",
					},
					Spec: kservev1beta1.InferenceServiceSpec{
						Predictor: kservev1beta1.PredictorSpec{
							Model: &kservev1beta1.ModelSpec{
								ModelFormat: kservev1beta1.ModelFormat{Name: "custom-format"},
								Runtime:     ptr.To("generic-runtime"),
							},
						},
					},
				}

				// Create fake k8s client and reconciler
				client := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(isvc, servingRuntime).
					Build()
				reconciler := NewKserveMetricsDashboardReconciler(client)

				// Run reconciler
				err := reconciler.Reconcile(ctx, log.Log, isvc)
				Expect(err).NotTo(HaveOccurred())

				// Query k8s API to verify ConfigMap
				configMap := &corev1.ConfigMap{}
				err = client.Get(ctx, k8stypes.NamespacedName{
					Name:      isvc.Name + constants.KserveMetricsConfigMapNameSuffix,
					Namespace: isvc.Namespace,
				}, configMap)
				Expect(err).NotTo(HaveOccurred())
				Expect(configMap.Data["supported"]).To(Equal("false"))
				Expect(configMap.Data["metrics"]).To(BeEmpty())
			})
		})
	})
})

// Helper function to create ServingRuntime with configurable annotations
func createServingRuntime(name string, annotations map[string]string) *kservev1alpha1.ServingRuntime {
	sr := &kservev1alpha1.ServingRuntime{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "test-namespace",
		},
		Spec: kservev1alpha1.ServingRuntimeSpec{
			SupportedModelFormats: []kservev1alpha1.SupportedModelFormat{
				{Name: "test-format", Version: ptr.To("1")},
			},
		},
	}
	if strings.Contains(name, "nim") {
		sr.ObjectMeta.Annotations = annotations
	} else {
		sr.Spec.Annotations = annotations
	}
	return sr
}
