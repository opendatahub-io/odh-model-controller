package core

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"

	corev1 "k8s.io/api/core/v1"
)

var _ = Describe("Pod Mutator Webhook", func() {
	var defaulter PodMutatorDefaultor
	var multinodePod *corev1.Pod
	BeforeEach(func() {
		defaulter = PodMutatorDefaultor{Client: k8sClient}

		multinodePod = &corev1.Pod{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "worker-container",
						Env: []corev1.EnvVar{
							{Name: "RAY_USE_TLS", Value: "1"},
						},
					},
				},
			},
		}
	})

	Describe("Handle method", func() {
		It("should add ray-tls-generator init-container to the pod if RAY_USE_TLS is set to 1", func() {
			// mutate multinode pad
			err := defaulter.podMutator(multinodePod)
			Expect(err).NotTo(HaveOccurred())

			// Verify that the InitContainer was added
			Expect(multinodePod.Spec.InitContainers).ShouldNot(BeNil())
			Expect(multinodePod.Spec.InitContainers).Should(HaveLen(1))
			Expect(multinodePod.Spec.InitContainers[0].Name).To(Equal(constants.RayTLSGeneratorInitContainerName))
		})
		It("should return true if RAY_USE_TLS is set to 1", func() {
			result := needToAddRayTLSGenerator(multinodePod)
			Expect(result).To(BeTrue())
		})

		It("should return true if RAY_USE_TLS is set to 0", func() {
			container := &multinodePod.Spec.Containers[0]
			// Update the environment variable
			for i := range container.Env {
				if container.Env[i].Name == "RAY_USE_TLS" {
					container.Env[i].Value = "0"
					break
				}
			}

			result := needToAddRayTLSGenerator(multinodePod)
			Expect(result).To(BeFalse())
		})

		It("should return false if RAY_USE_TLS is not set", func() {
			container := &multinodePod.Spec.Containers[0]
			container.Env = []corev1.EnvVar{}

			result := needToAddRayTLSGenerator(multinodePod)
			Expect(result).To(BeFalse())
		})
	})
})
