package controllers

import (
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/opendatahub-io/odh-model-controller/controllers/webhook"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var _ = Describe("KServe Service mutator webhook", func() {
	var mutator admission.CustomDefaulter
	defaultIsvcName := "isvc-name"
	defaultNsName := "default"
	createServiceOwnedKserve := func() *corev1.Service {
		return &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      defaultIsvcName,
				Namespace: "default",
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion: "serving.kserve.io/v1beta1",
						Kind:       "InferenceService",
						Name:       "sklearn-example-isvc-iris-v2-rest",
					},
				},
			},
		}
	}

	BeforeEach(func() {
		mutator = webhook.NewKserveServiceMutator(cli)

	})

	It("adds serving cert annotation when Service have InferenceService ownerReference", func() {
		// Create a new InferenceService
		inferenceService := &kservev1beta1.InferenceService{
			ObjectMeta: metav1.ObjectMeta{
				Name:      defaultIsvcName,
				Namespace: defaultNsName,
			},
		}
		Expect(cli.Create(ctx, inferenceService)).Should(Succeed())

		kserveService := createServiceOwnedKserve()

		err := mutator.Default(ctx, kserveService)
		Expect(err).ShouldNot(HaveOccurred())

		// Verify that serving cert annoation is set
		Expect(kserveService.Annotations).To(HaveKey("service.beta.openshift.io/serving-cert-secret-name"))
		Expect(kserveService.Annotations["service.beta.openshift.io/serving-cert-secret-name"]).To(Equal(defaultIsvcName))
	})

	It("skips adding annotation when Service does not have InferenceService ownerReference", func() {
		kserveServiceName := "different-name"
		kserveService := createServiceOwnedKserve()
		kserveService.SetName(kserveServiceName)
		kserveService.SetOwnerReferences([]metav1.OwnerReference{})

		err := mutator.Default(ctx, kserveService)
		Expect(err).ShouldNot(HaveOccurred())

		// Verify that serving cert annoation is NOT set
		Expect(kserveService.Annotations).NotTo(HaveKey("service.beta.openshift.io/serving-cert-secret-name"))
	})
})
