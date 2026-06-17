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
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	routev1 "github.com/openshift/api/route/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	controllerutils "github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
)

var _ = Describe("KserveRawRouteReconciler", func() {
	var scheme *runtime.Scheme

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(kservev1beta1.AddToScheme(scheme)).To(Succeed())
		Expect(routev1.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		controllerutils.RegisterSchemes(scheme)
	})

	Describe("setRouteTargetPort", func() {

		When("auth is enabled and service has https named port", func() {
			It("should return https as a named targetPort", func() {
				svc := createService("svc", nil, []corev1.ServicePort{
					{Name: "http", Port: 80},
					{Name: "https", Port: 443},
				})

				tp, err := setRouteTargetPort(true, svc)
				Expect(err).NotTo(HaveOccurred())
				Expect(tp.Type).To(Equal(intstr.String))
				Expect(tp.StrVal).To(Equal("https"))
			})
		})

		When("auth is enabled but https port is missing", func() {
			It("should fall back to any named port", func() {
				svc := createService("svc", nil, []corev1.ServicePort{
					{Name: "foo", Port: 1234},
				})

				tp, err := setRouteTargetPort(true, svc)
				Expect(err).NotTo(HaveOccurred())
				Expect(tp.Type).To(Equal(intstr.String))
				Expect(tp.StrVal).To(Equal("foo"))
			})
		})

		When("auth is disabled and service has http named port", func() {
			It("should return http as a named targetPort", func() {
				svc := createService("svc", nil, []corev1.ServicePort{
					{Name: "http", Port: 80},
					{Name: "foo", Port: 1234},
				})

				tp, err := setRouteTargetPort(false, svc)
				Expect(err).NotTo(HaveOccurred())
				Expect(tp.Type).To(Equal(intstr.String))
				Expect(tp.StrVal).To(Equal("http"))
			})
		})

		When("auth is disabled and no http port exists", func() {
			It("should fall back to any named port", func() {
				svc := createService("svc", nil, []corev1.ServicePort{
					{Name: "foo", Port: 1234},
				})

				tp, err := setRouteTargetPort(false, svc)
				Expect(err).NotTo(HaveOccurred())
				Expect(tp.Type).To(Equal(intstr.String))
				Expect(tp.StrVal).To(Equal("foo"))
			})
		})

		When("no named ports exist", func() {
			It("should fall back to first numeric port", func() {
				svc := createService("svc", nil, []corev1.ServicePort{
					{Name: "", Port: 8080},
				})

				tp, err := setRouteTargetPort(false, svc)
				Expect(err).NotTo(HaveOccurred())
				Expect(tp.Type).To(Equal(intstr.Int))
				Expect(tp.IntVal).To(Equal(int32(8080)))
			})
		})

		When("service has no ports", func() {
			It("should return an error", func() {
				svc := createService("svc", nil, []corev1.ServicePort{})

				_, err := setRouteTargetPort(false, svc)
				Expect(err).To(HaveOccurred())
			})
		})
	})

	Describe("checkRouteTargetPort", func() {

		When("route already has a valid targetPort", func() {
			It("should be a no-op", func(ctx SpecContext) {
				svc := createService("pred", nil, []corev1.ServicePort{
					{Name: "http", Port: 80},
				})

				route := createRoute("pred", intstr.FromString("http"))

				client := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(svc).
					Build()

				err := checkRouteTargetPort(ctx, client, route)
				Expect(err).NotTo(HaveOccurred())
				Expect(route.Spec.Port.TargetPort.StrVal).To(Equal("http"))
			})
		})

		When("route is missing targetPort", func() {
			It("should fill targetPort from service", func(ctx SpecContext) {
				svc := createService("pred", nil, []corev1.ServicePort{
					{Name: "http", Port: 80},
				})

				route := createRoute("pred", intstr.IntOrString{})

				client := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(svc).
					Build()

				err := checkRouteTargetPort(ctx, client, route)
				Expect(err).NotTo(HaveOccurred())
				Expect(route.Spec.Port).NotTo(BeNil())
				Expect(route.Spec.Port.TargetPort.Type).To(Equal(intstr.String))
				Expect(route.Spec.Port.TargetPort.StrVal).To(Equal("http"))
			})
		})

		When("target service does not exist", func() {
			It("should return an error", func(ctx SpecContext) {
				route := createRoute("missing", intstr.IntOrString{})

				client := fake.NewClientBuilder().
					WithScheme(scheme).
					Build()

				err := checkRouteTargetPort(ctx, client, route)
				Expect(err).To(HaveOccurred())
			})
		})

		When("target service has no ports", func() {
			It("should return an error", func(ctx SpecContext) {
				svc := createService("pred", nil, []corev1.ServicePort{})

				route := createRoute("pred", intstr.IntOrString{})

				client := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(svc).
					Build()

				err := checkRouteTargetPort(ctx, client, route)
				Expect(err).To(HaveOccurred())
			})
		})
	})

	Describe("createDesiredResource", func() {

		When("visibility label is not present", func() {
			It("should return nil route and nil error (skip creation)", func(ctx SpecContext) {
				isvc := createISVC(nil)

				client := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(isvc).
					Build()

				reconciler := NewKserveRawRouteReconciler(client)
				route, err := reconciler.createDesiredResource(ctx, log.Log, isvc)

				Expect(err).NotTo(HaveOccurred())
				Expect(route).To(BeNil())
			})
		})

		When("transformer service exists", func() {
			It("should target transformer service", func(ctx SpecContext) {
				isvc := createISVC(
					map[string]string{
						constants.KserveNetworkVisibility: constants.LabelEnableKserveRawRoute,
					},
				)

				transformerSvc := createService("test-transformer",
					map[string]string{
						constants.KserveGroupAnnotation: "test",
						"component":                     "transformer",
					},
					[]corev1.ServicePort{{Name: "http", Port: 80}},
				)

				predictorSvc := createService("test-predictor",
					map[string]string{
						constants.KserveGroupAnnotation: "test",
						"component":                     "predictor",
					},
					[]corev1.ServicePort{{Name: "http", Port: 80}},
				)

				client := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(isvc, transformerSvc, predictorSvc).
					Build()

				reconciler := NewKserveRawRouteReconciler(client)
				route, err := reconciler.createDesiredResource(ctx, log.Log, isvc)

				Expect(err).NotTo(HaveOccurred())
				Expect(route).NotTo(BeNil())
				Expect(route.Spec.To.Name).To(Equal("test-transformer"))
				Expect(route.Spec.Port.TargetPort.StrVal).To(Equal("http"))
			})
		})

		When("canonical predictor service exists", func() {
			It("should prefer <isvc>-predictor service over other predictors", func(ctx SpecContext) {
				isvc := createISVC(
					map[string]string{
						constants.KserveNetworkVisibility: constants.LabelEnableKserveRawRoute,
					},
				)

				canonicalPredictor := createService("test-predictor",
					map[string]string{
						constants.KserveGroupAnnotation: "test",
						"component":                     "predictor",
					},
					[]corev1.ServicePort{{Name: "http", Port: 80}},
				)

				otherPredictor := createService("random-predictor",
					map[string]string{
						constants.KserveGroupAnnotation: "test",
						"component":                     "predictor",
					},
					[]corev1.ServicePort{{Name: "http", Port: 80}},
				)

				client := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(isvc, canonicalPredictor, otherPredictor).
					Build()

				reconciler := NewKserveRawRouteReconciler(client)
				route, err := reconciler.createDesiredResource(ctx, log.Log, isvc)

				Expect(err).NotTo(HaveOccurred())
				Expect(route).NotTo(BeNil())
				Expect(route.Spec.To.Name).To(Equal("test-predictor"))
			})
		})

		When("canonical predictor is missing but another predictor exists", func() {
			It("should fall back to any component=predictor service", func(ctx SpecContext) {
				isvc := createISVC(
					map[string]string{
						constants.KserveNetworkVisibility: constants.LabelEnableKserveRawRoute,
					},
				)

				fallbackPredictor := createService("some-predictor",
					map[string]string{
						constants.KserveGroupAnnotation: "test",
						"component":                     "predictor",
					},
					[]corev1.ServicePort{{Name: "http", Port: 80}},
				)

				client := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(isvc, fallbackPredictor).
					Build()

				reconciler := NewKserveRawRouteReconciler(client)
				route, err := reconciler.createDesiredResource(ctx, log.Log, isvc)

				Expect(err).NotTo(HaveOccurred())
				Expect(route).NotTo(BeNil())
				Expect(route.Spec.To.Name).To(Equal("some-predictor"))
			})
		})
	})
})

func createISVC(labels map[string]string) *kservev1beta1.InferenceService {
	return &kservev1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "ns",
			Labels:    labels,
		},
		Spec: kservev1beta1.InferenceServiceSpec{},
	}
}

func createService(name string, labels map[string]string, ports []corev1.ServicePort) *corev1.Service {
	if labels == nil {
		labels = map[string]string{}
	}
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "ns",
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Ports: ports,
		},
	}
}

func createRoute(targetSvc string, targetPort intstr.IntOrString) *routev1.Route {
	rt := &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "ns",
		},
		Spec: routev1.RouteSpec{
			To: routev1.RouteTargetReference{
				Kind: "Service",
				Name: targetSvc,
			},
		},
	}

	if targetPort.Type != 0 || targetPort.IntVal != 0 || targetPort.StrVal != "" {
		rt.Spec.Port = &routev1.RoutePort{
			TargetPort: targetPort,
		}
	}

	// ensure Port struct exists for mutation in tests
	if rt.Spec.Port == nil {
		rt.Spec.Port = &routev1.RoutePort{}
	}

	return rt
}
