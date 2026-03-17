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

package v1

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	testutils "github.com/opendatahub-io/odh-model-controller/test/utils"
	corev1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
)

const paramsEnvPath1 = "../../../controller/serving/testdata/configmaps/odh-model-controller-parameters.yaml"

var _ = Describe("Pod Mutator Webhook", func() {
	var defaulter PodMutatorDefaultor
	var multinodePod *corev1.Pod
	BeforeEach(func() {
		defaulter = PodMutatorDefaultor{client: k8sClient}

		multinodePod = &corev1.Pod{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "worker-container",
						Env: []corev1.EnvVar{
							{Name: constants.RayUseTlsEnvName, Value: "1"},
						},
					},
				},
			},
		}

		paramsConfigMap := &corev1.ConfigMap{}
		Expect(testutils.ConvertToStructuredResource(paramsEnvPath1, paramsConfigMap)).To(Succeed())
		if err := k8sClient.Create(ctx, paramsConfigMap); err != nil && !k8sErrors.IsAlreadyExists(err) {
			Fail(err.Error())
		}
	})

	Describe("Handle method", func() {
		It("should add init-container/volumes/volumeMounts to the pod if RAY_USE_TLS is set to 1", func() {
			// mutate multinode pad
			err := defaulter.Default(ctx, multinodePod)
			Expect(err).NotTo(HaveOccurred())

			// Verify that the InitContainer was added
			Expect(multinodePod.Spec.InitContainers).ShouldNot(BeNil())
			Expect(multinodePod.Spec.InitContainers).Should(HaveLen(1))
			Expect(multinodePod.Spec.InitContainers[0].Name).To(Equal(constants.RayTLSGeneratorInitContainerName))

			// Verify that the volumes were added
			Expect(multinodePod.Spec.Volumes).ShouldNot(BeNil())

			Expect(multinodePod.Spec.Volumes).Should(ContainElements(
				HaveField("Name", constants.RayTLSVolumeName),
				HaveField("Name", constants.RayTLSSecretVolumeName),
			))

			// Verify that the volumeMount was added
			container := &multinodePod.Spec.Containers[0]
			Expect(container.VolumeMounts).ShouldNot(BeNil())

			Expect(container.VolumeMounts).Should(ContainElement(
				HaveField("MountPath", constants.RayTLSVolumeMountPath),
			))
		})
		It("should not add init-container/volumes/volumeMounts to the pod if RAY_USE_TLS set to 0", func() {
			container := &multinodePod.Spec.Containers[0]
			// Update the environment variable
			for i := range container.Env {
				if container.Env[i].Name == constants.RayUseTlsEnvName {
					container.Env[i].Value = "0"
					break
				}
			}
			// mutate multinode pad
			err := defaulter.Default(ctx, multinodePod)
			Expect(err).NotTo(HaveOccurred())

			// Verify that the InitContainer was not added
			Expect(multinodePod.Spec.InitContainers).Should(BeNil())

			// Verify that the volumes were not added
			Expect(multinodePod.Spec.Volumes).Should(BeNil())

			// Verify that the volumeMount was not added
			Expect(container.VolumeMounts).Should(BeNil())
		})

		It("should not add init-container/volumes/volumeMounts to the pod if RAY_USE_TLS does not set", func() {
			container := &multinodePod.Spec.Containers[0]
			container.Env = []corev1.EnvVar{}
			// mutate multinode pad
			err := defaulter.Default(ctx, multinodePod)
			Expect(err).NotTo(HaveOccurred())

			// Verify that the InitContainer was not added
			Expect(multinodePod.Spec.InitContainers).Should(BeNil())

			// Verify that the volumes were not added
			Expect(multinodePod.Spec.Volumes).Should(BeNil())

			// Verify that the volumeMount was not added
			Expect(container.VolumeMounts).Should(BeNil())
		})
		It("should return true if RAY_USE_TLS is set to 1", func() {
			result := needToAddRayTLSGenerator(multinodePod)
			Expect(result).To(BeTrue())
		})

		It("should return false if RAY_USE_TLS is set to 0", func() {
			container := &multinodePod.Spec.Containers[0]
			// Update the environment variable
			for i := range container.Env {
				if container.Env[i].Name == constants.RayUseTlsEnvName {
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
