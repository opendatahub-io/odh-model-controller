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
	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	kserveconstants "github.com/kserve/kserve/pkg/constants"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"

	controllerutils "github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
)

var _ = Describe("KserveRawMetricsServiceMonitorReconciler", func() {
	var scheme *runtime.Scheme

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(kservev1beta1.AddToScheme(scheme)).To(Succeed())
		Expect(kservev1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(monitoringv1.AddToScheme(scheme)).To(Succeed())
		controllerutils.RegisterSchemes(scheme)
	})

	Describe("createDesiredResource", func() {
		const (
			runtimeName = "test-runtime"
			isvcName    = "test-isvc"
			namespace   = "ns"
		)

		createServingRuntime := func(annotations map[string]string) *kservev1alpha1.ServingRuntime {
			return &kservev1alpha1.ServingRuntime{
				ObjectMeta: metav1.ObjectMeta{
					Name:      runtimeName,
					Namespace: namespace,
				},
				Spec: kservev1alpha1.ServingRuntimeSpec{
					ServingRuntimePodSpec: kservev1alpha1.ServingRuntimePodSpec{
						Annotations: annotations,
					},
				},
			}
		}

		createInferenceService := func() *kservev1beta1.InferenceService {
			runtimeRef := runtimeName
			return &kservev1beta1.InferenceService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      isvcName,
					Namespace: namespace,
				},
				Spec: kservev1beta1.InferenceServiceSpec{
					Predictor: kservev1beta1.PredictorSpec{
						Model: &kservev1beta1.ModelSpec{
							Runtime: ptr.To(runtimeRef),
						},
					},
				},
			}
		}

		When("the prometheus.io/path annotation is set on the ServingRuntime", func() {
			It("should set Endpoint.Path on the ServiceMonitor", func(ctx SpecContext) {
				sr := createServingRuntime(map[string]string{
					kserveconstants.PrometheusPathAnnotationKey: "/v1/metrics",
					kserveconstants.PrometheusPortAnnotationKey: "8000",
				})
				isvc := createInferenceService()

				client := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(sr, isvc).
					Build()

				reconciler := NewKServeRawMetricsServiceMonitorReconciler(client)
				sm, err := reconciler.createDesiredResource(ctx, log.Log, isvc)

				Expect(err).NotTo(HaveOccurred())
				Expect(sm).NotTo(BeNil())
				Expect(sm.Spec.Endpoints).To(HaveLen(1))
				Expect(sm.Spec.Endpoints[0].Path).To(Equal("/v1/metrics"))
			})
		})

		When("the prometheus.io/path annotation is absent from the ServingRuntime", func() {
			It("should not set Endpoint.Path on the ServiceMonitor", func(ctx SpecContext) {
				sr := createServingRuntime(map[string]string{
					kserveconstants.PrometheusPortAnnotationKey: "8000",
				})
				isvc := createInferenceService()

				client := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(sr, isvc).
					Build()

				reconciler := NewKServeRawMetricsServiceMonitorReconciler(client)
				sm, err := reconciler.createDesiredResource(ctx, log.Log, isvc)

				Expect(err).NotTo(HaveOccurred())
				Expect(sm).NotTo(BeNil())
				Expect(sm.Spec.Endpoints).To(HaveLen(1))
				Expect(sm.Spec.Endpoints[0].Path).To(BeEmpty())
			})
		})

		When("the prometheus.io/path annotation is present but empty", func() {
			It("should not set Endpoint.Path on the ServiceMonitor", func(ctx SpecContext) {
				sr := createServingRuntime(map[string]string{
					kserveconstants.PrometheusPathAnnotationKey: "",
					kserveconstants.PrometheusPortAnnotationKey: "8000",
				})
				isvc := createInferenceService()

				client := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(sr, isvc).
					Build()

				reconciler := NewKServeRawMetricsServiceMonitorReconciler(client)
				sm, err := reconciler.createDesiredResource(ctx, log.Log, isvc)

				Expect(err).NotTo(HaveOccurred())
				Expect(sm).NotTo(BeNil())
				Expect(sm.Spec.Endpoints).To(HaveLen(1))
				Expect(sm.Spec.Endpoints[0].Path).To(BeEmpty())
			})
		})

		When("the prometheus.io/path annotation has no leading slash", func() {
			It("should prepend a slash and set Endpoint.Path on the ServiceMonitor", func(ctx SpecContext) {
				sr := createServingRuntime(map[string]string{
					kserveconstants.PrometheusPathAnnotationKey: "v1/metrics",
					kserveconstants.PrometheusPortAnnotationKey: "8000",
				})
				isvc := createInferenceService()

				client := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(sr, isvc).
					Build()

				reconciler := NewKServeRawMetricsServiceMonitorReconciler(client)
				sm, err := reconciler.createDesiredResource(ctx, log.Log, isvc)

				Expect(err).NotTo(HaveOccurred())
				Expect(sm).NotTo(BeNil())
				Expect(sm.Spec.Endpoints).To(HaveLen(1))
				Expect(sm.Spec.Endpoints[0].Path).To(Equal("/v1/metrics"))
			})
		})

		When("the prometheus.io/path annotation has surrounding whitespace", func() {
			It("should trim whitespace and set Endpoint.Path on the ServiceMonitor", func(ctx SpecContext) {
				sr := createServingRuntime(map[string]string{
					kserveconstants.PrometheusPathAnnotationKey: "  /v1/metrics  ",
					kserveconstants.PrometheusPortAnnotationKey: "8000",
				})
				isvc := createInferenceService()

				client := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(sr, isvc).
					Build()

				reconciler := NewKServeRawMetricsServiceMonitorReconciler(client)
				sm, err := reconciler.createDesiredResource(ctx, log.Log, isvc)

				Expect(err).NotTo(HaveOccurred())
				Expect(sm).NotTo(BeNil())
				Expect(sm.Spec.Endpoints).To(HaveLen(1))
				Expect(sm.Spec.Endpoints[0].Path).To(Equal("/v1/metrics"))
			})
		})

		When("the ServingRuntime has no annotations", func() {
			It("should not set Endpoint.Path on the ServiceMonitor", func(ctx SpecContext) {
				sr := createServingRuntime(nil)
				isvc := createInferenceService()

				client := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(sr, isvc).
					Build()

				reconciler := NewKServeRawMetricsServiceMonitorReconciler(client)
				sm, err := reconciler.createDesiredResource(ctx, log.Log, isvc)

				Expect(err).NotTo(HaveOccurred())
				Expect(sm).NotTo(BeNil())
				Expect(sm.Spec.Endpoints).To(HaveLen(1))
				Expect(sm.Spec.Endpoints[0].Path).To(BeEmpty())
			})
		})
	})
})
