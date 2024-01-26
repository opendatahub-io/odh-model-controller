package resources_test

import (
	"context"

	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/opendatahub-io/odh-model-controller/controllers/resources"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"knative.dev/pkg/apis"
	duckv1 "knative.dev/pkg/apis/duck/v1"
	knservingv1 "knative.dev/serving/pkg/apis/serving/v1"
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

		It("should resovle UserDefined template", func() {
			typeName := types.NamespacedName{
				Name:      "test",
				Namespace: "test-ns",
			}

			ac, err := resources.NewStaticTemplateLoader().Load(
				context.Background(),
				resources.UserDefined,
				typeName)

			Expect(err).To(Succeed())
			Expect("test").To(Equal(ac.Spec.Authorization["kubernetes-rbac"].KubernetesSubjectAccessReview.ResourceAttributes.Namespace))
		})
	})
})
