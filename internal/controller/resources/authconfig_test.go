package resources_test

import (
	"context"
	"os"

	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/tidwall/gjson"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/apis"
	duckv1 "knative.dev/pkg/apis/duck/v1"
	knservingv1 "knative.dev/serving/pkg/apis/serving/v1"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/resources"
)

var _ = When("InferenceService is ready", func() {

	Context("Extract Hosts", func() {

		It("From all URL fields", func() {
			url_1, _ := apis.ParseURL("https://1.testing")
			url_2, _ := apis.ParseURL("https://2.testing")
			url_3, _ := apis.ParseURL("https://3.testing")
			url_4, _ := apis.ParseURL("https://4.testing")
			url_5, _ := apis.ParseURL("https://5.testing")
			url_6, _ := apis.ParseURL("https://6.testing")
			url_7, _ := apis.ParseURL("https://7.testing")
			isvc := &kservev1beta1.InferenceService{
				ObjectMeta: v1.ObjectMeta{
					Namespace: "kserve-demo",
				},
				Status: kservev1beta1.InferenceServiceStatus{
					URL: url_1,
					Address: &duckv1.Addressable{
						URL: url_2,
					},
					Components: map[kservev1beta1.ComponentType]kservev1beta1.ComponentStatusSpec{
						kservev1beta1.PredictorComponent: {
							URL:     url_3,
							GrpcURL: url_4,
							RestURL: url_5,
							Address: &duckv1.Addressable{
								URL: url_6,
							},
							Traffic: []knservingv1.TrafficTarget{
								{
									URL: url_7,
								},
							},
						},
					},
				},
			}

			hosts := resources.NewKServeInferenceServiceHostExtractor().Extract(isvc)
			Expect(hosts).To(HaveLen(7))
		})

		It("Expand to all internal formats", func() {
			url, _ := apis.ParseURL("https://x.svc.cluster.local")
			isvc := &kservev1beta1.InferenceService{
				ObjectMeta: v1.ObjectMeta{
					Namespace: "kserve-demo",
				},
				Status: kservev1beta1.InferenceServiceStatus{
					URL: url,
				},
			}

			hosts := resources.NewKServeInferenceServiceHostExtractor().Extract(isvc)
			Expect(hosts).To(HaveLen(3))
			Expect(hosts).To(ContainElements("x", "x.svc", "x.svc.cluster.local"))
		})

	})

	Context("Template loading", func() {
		dummyIsvc := kservev1beta1.InferenceService{
			ObjectMeta: v1.ObjectMeta{
				Namespace: "test-ns",
				Name:      "test",
			},
		}

		It("should resolve UserDefined template for InferenceService", func() {
			ac, err := resources.NewStaticTemplateLoader().Load(
				context.Background(),
				constants.UserDefined,
				&dummyIsvc)

			Expect(err).To(Succeed())
			Expect(gjson.ParseBytes(ac.Spec.Authorization["kubernetes-rbac"].KubernetesSubjectAccessReview.ResourceAttributes.Namespace.Value.Raw).String()).To(Equal(dummyIsvc.Namespace))
		})

		It("should default to kubernetes.default.svc Audience", func() {
			ac, err := resources.NewStaticTemplateLoader().Load(
				context.Background(),
				constants.UserDefined,
				&dummyIsvc)

			Expect(err).To(Succeed())
			Expect(ac.Spec.Authentication["kubernetes-user"].KubernetesTokenReview.Audiences).To(ContainElement("https://kubernetes.default.svc"))
		})

		It("should read AUTH_AUDIENCE env var for Audience", func() {
			_ = os.Setenv("AUTH_AUDIENCE", "http://test.com")
			defer func() {
				_ = os.Unsetenv("AUTH_AUDIENCE")
			}()

			ac, err := resources.NewStaticTemplateLoader().Load(
				context.Background(),
				constants.UserDefined,
				&dummyIsvc)

			Expect(err).To(Succeed())
			Expect(ac.Spec.Authentication["kubernetes-user"].KubernetesTokenReview.Audiences).To(ContainElement("http://test.com"))
		})

		It("should default to opendatahub.io.. AuthorinoLabel", func() {
			ac, err := resources.NewStaticTemplateLoader().Load(
				context.Background(),
				constants.UserDefined,
				&dummyIsvc)

			Expect(err).To(Succeed())
			Expect(ac.ObjectMeta.Labels["security.opendatahub.io/authorization-group"]).To(Equal("default"))
		})

		It("should read AUTH_AUDIENCE env var for Audience", func() {
			_ = os.Setenv("AUTHORINO_LABEL", "opendatahub=test")
			defer func() {
				_ = os.Unsetenv("AUTHORINO_LABEL")
			}()

			ac, err := resources.NewStaticTemplateLoader().Load(
				context.Background(),
				constants.UserDefined,
				&dummyIsvc)

			Expect(err).To(Succeed())
			Expect(ac.ObjectMeta.Labels["opendatahub"]).To(Equal("test"))
		})
	})
})
