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

package controllers

import (
	"context"
	"k8s.io/apimachinery/pkg/types"
	"time"

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/opendatahub-io/odh-model-controller/controllers/constants"
	corev1 "k8s.io/api/core/v1"
)

const (
	multinodeServingRuntimePath = "./testdata/deploy/vllm-multinode-servingruntime.yaml"
	rayTlsScriptsPath           = "./testdata/configmaps/ray-tls-scripts.yaml"
	rayTlsScriptsUpdatedPath    = "./testdata/configmaps/ray-tls-scripts-updated.yaml"
	rayCaCertPath               = "./testdata/secrets/ray-ca-cert.yaml"
	rayCaCertUpdatedPath        = "./testdata/secrets/ray-ca-cert-updated.yaml"
)

var _ = Describe("KServe Ray TLS controller", func() {
	ctx := context.Background()

	Context("when a non-multinode ServingRuntime created", func() {
		It("should not create a 'ray-ca-cert' Secret and 'ray-tls-scripts' ConfigMap in the testNs", func() {
			testNamespace := Namespaces.Create(cli)
			testNs := testNamespace.Name

			// Create ray tls resources in the ctrl namespace
			rayTlsScriptsConfigMap := &corev1.ConfigMap{}
			err := convertToStructuredResource(rayTlsScriptsPath, rayTlsScriptsConfigMap)
			Expect(err).NotTo(HaveOccurred())
			rayTlsScriptsConfigMap.SetNamespace(WorkingNamespace)
			Expect(cli.Create(ctx, rayTlsScriptsConfigMap)).Should(Succeed())

			rayCaCertSecret := &corev1.Secret{}
			err = convertToStructuredResource(rayCaCertPath, rayCaCertSecret)
			Expect(err).NotTo(HaveOccurred())
			rayCaCertSecret.SetNamespace(WorkingNamespace)
			Expect(cli.Create(ctx, rayCaCertSecret)).Should(Succeed())

			By("creating non-multinode ServingRuntime")
			nonMultinodeServingRuntime := &kservev1alpha1.ServingRuntime{}
			err = convertToStructuredResource(ServingRuntimePath1, nonMultinodeServingRuntime)
			Expect(err).NotTo(HaveOccurred())
			nonMultinodeServingRuntime.SetNamespace(testNs)
			Expect(cli.Create(ctx, nonMultinodeServingRuntime)).Should(Succeed())

			// Check if all ray tls resources are NOT created in the testNs
			configmap, err := waitForConfigMap(cli, testNs, constants.RayTlsScriptConfigMapName, 3, 1*time.Second)
			Expect(err).To(HaveOccurred())
			Expect(configmap).To(BeNil())

			secret, err := waitForSecret(cli, testNs, constants.RayCATlsSecretName, 3, 1*time.Second)
			Expect(err).To(HaveOccurred())
			Expect(secret).To(BeNil())
		})
	})

	Context("when a multinode ServingRuntime created", func() {
		It("should create a 'ray-ca-cert' Secret and 'ray-tls-scripts' ConfigMap in the testNs where the SR exist", func() {
			testNamespace := Namespaces.Create(cli)
			testNs := testNamespace.Name

			// Create ray tls resources in the ctrl namespace
			rayTlsScriptsConfigMap := &corev1.ConfigMap{}
			err := convertToStructuredResource(rayTlsScriptsPath, rayTlsScriptsConfigMap)
			Expect(err).NotTo(HaveOccurred())
			rayTlsScriptsConfigMap.SetNamespace(WorkingNamespace)
			Expect(cli.Create(ctx, rayTlsScriptsConfigMap)).Should(Succeed())

			rayCaCertSecret := &corev1.Secret{}
			err = convertToStructuredResource(rayCaCertPath, rayCaCertSecret)
			Expect(err).NotTo(HaveOccurred())
			rayCaCertSecret.SetNamespace(WorkingNamespace)
			Expect(cli.Create(ctx, rayCaCertSecret)).Should(Succeed())

			By("creating multinode ServingRuntime")
			multinodeServingRuntime := &kservev1alpha1.ServingRuntime{}
			err = convertToStructuredResource(multinodeServingRuntimePath, multinodeServingRuntime)
			Expect(err).NotTo(HaveOccurred())
			multinodeServingRuntime.SetNamespace(testNs)
			Expect(cli.Create(ctx, multinodeServingRuntime)).Should(Succeed())

			// Check if all ray tls resources are created in the testNs
			_, err = waitForConfigMap(cli, testNs, constants.RayTlsScriptConfigMapName, 30, 1*time.Second)
			Expect(err).NotTo(HaveOccurred())
			_, err = waitForSecret(cli, testNs, constants.RayCATlsSecretName, 30, 1*time.Second)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("when a multinode ServingRuntime exists", func() {
		var testNs string

		BeforeEach(func() {
			// Create a test namespace
			testNamespace := Namespaces.Create(cli)
			testNs = testNamespace.Name

			// Create ray tls resources in the ctrl namespace
			rayTlsScriptsConfigMap := &corev1.ConfigMap{}
			err := convertToStructuredResource(rayTlsScriptsPath, rayTlsScriptsConfigMap)
			Expect(err).NotTo(HaveOccurred())
			rayTlsScriptsConfigMap.SetNamespace(WorkingNamespace)
			Expect(cli.Create(ctx, rayTlsScriptsConfigMap)).Should(Succeed())

			rayCaCertSecret := &corev1.Secret{}
			err = convertToStructuredResource(rayCaCertPath, rayCaCertSecret)
			Expect(err).NotTo(HaveOccurred())
			rayCaCertSecret.SetNamespace(WorkingNamespace)
			Expect(cli.Create(ctx, rayCaCertSecret)).Should(Succeed())

			// Create a multinode ServingRuntime
			multinodeServingRuntime := &kservev1alpha1.ServingRuntime{}
			err = convertToStructuredResource(multinodeServingRuntimePath, multinodeServingRuntime)
			Expect(err).NotTo(HaveOccurred())
			multinodeServingRuntime.SetNamespace(testNs)
			Expect(cli.Create(ctx, multinodeServingRuntime)).Should(Succeed())

			// Check if all ray tls resources are created in the testNs
			_, err = waitForConfigMap(cli, testNs, constants.RayTlsScriptConfigMapName, 30, 1*time.Second)
			Expect(err).NotTo(HaveOccurred())
			_, err = waitForSecret(cli, testNs, constants.RayCATlsSecretName, 30, 1*time.Second)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should recreate a 'ray-ca-cert' Secret when it is removed manually", func() {
			secret := &corev1.Secret{}
			err := cli.Get(ctx, types.NamespacedName{Name: constants.RayCATlsSecretName, Namespace: testNs}, secret)
			Expect(err).NotTo(HaveOccurred())

			By("deleting a 'ray-ca-cert' Secret in the testNs")
			Expect(cli.Delete(ctx, secret)).To(Succeed())

			// Check if 'ray-ca-cert' Secret is recreated
			_, err = waitForSecret(cli, testNs, constants.RayCATlsSecretName, 30, 1*time.Second)
			Expect(err).NotTo(HaveOccurred())
		})
		It("should rollback 'ray-ca-cert' Secret in the target ns when it is changed", func() {
			By("updating existing 'ray-ca-cert' Secret in the testNs")
			rayCACertUpdatedSecret := &corev1.Secret{}
			err := convertToStructuredResource(rayCaCertUpdatedPath, rayCACertUpdatedSecret)
			Expect(err).NotTo(HaveOccurred())
			rayCACertUpdatedSecret.SetNamespace(testNs)
			Expect(cli.Update(ctx, rayCACertUpdatedSecret)).Should(Succeed())

			// Check if 'ray-ca-cert' Secret is rollback
			originalRayCaCertSecret, err := waitForSecret(cli, WorkingNamespace, constants.RayCATlsSecretName, 30, 1*time.Second)
			Expect(err).NotTo(HaveOccurred())
			Eventually(func() bool {
				updatedSecretFromTestNs, err := waitForSecret(cli, testNs, constants.RayCATlsSecretName, 1, 1*time.Second)
				Expect(err).NotTo(HaveOccurred())
				return compareSecrets(originalRayCaCertSecret, updatedSecretFromTestNs)
			}, timeout, interval).Should(BeTrue())
		})
		It("should create a 'ray-tls-scripts' ConfigMap when it is removed manually", func() {
			configMap := &corev1.ConfigMap{}
			err := cli.Get(ctx, types.NamespacedName{Name: constants.RayTlsScriptConfigMapName, Namespace: testNs}, configMap)
			Expect(err).NotTo(HaveOccurred())

			By("deleting a 'ray-tls-scripts' configMap in the testNs")
			Expect(cli.Delete(ctx, configMap)).To(Succeed())

			// Check if 'ray-tls-scripts' ConfigMap is recreated
			_, err = waitForConfigMap(cli, testNs, constants.RayTlsScriptConfigMapName, 30, 1*time.Second)
			Expect(err).NotTo(HaveOccurred())
		})
		It("should rollback 'ray-tls-scripts' ConfigMap in the target ns when it is changed", func() {
			By("updating existing 'ray-tls-scripts' ConfigMap in the testNs")
			rayTlsScriptsUpdatedConfigMap := &corev1.ConfigMap{}
			err := convertToStructuredResource(rayTlsScriptsUpdatedPath, rayTlsScriptsUpdatedConfigMap)
			Expect(err).NotTo(HaveOccurred())
			rayTlsScriptsUpdatedConfigMap.SetNamespace(testNs)
			Expect(cli.Update(ctx, rayTlsScriptsUpdatedConfigMap)).Should(Succeed())

			// Check if 'ray-tls-scripts' ConfigMap is rollback
			originalRayTlsScriptsConfigMap, err := waitForConfigMap(cli, testNs, constants.RayTlsScriptConfigMapName, 30, 1*time.Second)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() bool {
				updatedConfigMapFromTestNs, err := waitForConfigMap(cli, testNs, constants.RayTlsScriptConfigMapName, 1, 1*time.Second)
				Expect(err).NotTo(HaveOccurred())
				return compareConfigMap(originalRayTlsScriptsConfigMap, updatedConfigMapFromTestNs)
			}, timeout, interval).Should(BeTrue())
		})
		It("should 'ray-tls-scripts' ConfigMap in the testNs when original one in the ctrlNs updated", func() {
			By("updating original 'ray-tls-scripts' ConfigMap in the ctrlNs")
			rayTlsScriptsUpdatedConfigMap := &corev1.ConfigMap{}
			err := convertToStructuredResource(rayTlsScriptsUpdatedPath, rayTlsScriptsUpdatedConfigMap)
			Expect(err).NotTo(HaveOccurred())
			rayTlsScriptsUpdatedConfigMap.SetNamespace(WorkingNamespace)
			Expect(cli.Update(ctx, rayTlsScriptsUpdatedConfigMap)).Should(Succeed())

			_, err = waitForConfigMap(cli, WorkingNamespace, constants.RayTlsScriptConfigMapName, 30, 1*time.Second)
			Expect(err).NotTo(HaveOccurred())

			// Check if 'ray-tls-scripts' ConfigMap in the testNs is updated.
			Eventually(func() bool {
				updatedConfigMapFromTestNs, err := waitForConfigMap(cli, testNs, constants.RayTlsScriptConfigMapName, 1, 1*time.Second)
				Expect(err).NotTo(HaveOccurred())
				return compareConfigMap(rayTlsScriptsUpdatedConfigMap, updatedConfigMapFromTestNs)
			}, timeout, interval).Should(BeTrue())
		})
		It("should update a 'ray-ca-cert' Secret in the testNs when original one in the ctrlNs updated", func() {
			By("updating original 'ray-ca-cert Secret in the ctrlNs")
			rayCaCertUpdatedSecret := &corev1.Secret{}
			err := convertToStructuredResource(rayCaCertUpdatedPath, rayCaCertUpdatedSecret)
			Expect(err).NotTo(HaveOccurred())
			rayCaCertUpdatedSecret.SetNamespace(WorkingNamespace)
			Expect(cli.Update(ctx, rayCaCertUpdatedSecret)).Should(Succeed())

			_, err = waitForSecret(cli, WorkingNamespace, constants.RayCATlsSecretName, 30, 1*time.Second)
			Expect(err).NotTo(HaveOccurred())

			// Check if 'ray-ca-cert' Secret in the testNs is updated.
			Eventually(func() bool {
				updatedSecretFromTestNs, err := waitForSecret(cli, testNs, constants.RayCATlsSecretName, 1, 1*time.Second)
				Expect(err).NotTo(HaveOccurred())
				return compareSecrets(rayCaCertUpdatedSecret, updatedSecretFromTestNs)
			}, timeout, interval).Should(BeTrue())
		})
	})
	Context("when a multinode ServingRuntime removed from the testNs", func() {
		var testNs string
		BeforeEach(func() {
			testNamespace := Namespaces.Create(cli)
			testNs = testNamespace.Name

			// Create ray tls resources in the ctrl namespace
			rayTlsScriptsConfigMap := &corev1.ConfigMap{}
			err := convertToStructuredResource(rayTlsScriptsPath, rayTlsScriptsConfigMap)
			Expect(err).NotTo(HaveOccurred())
			rayTlsScriptsConfigMap.SetNamespace(WorkingNamespace)
			Expect(cli.Create(ctx, rayTlsScriptsConfigMap)).Should(Succeed())

			rayCaCertSecret := &corev1.Secret{}
			err = convertToStructuredResource(rayCaCertPath, rayCaCertSecret)
			Expect(err).NotTo(HaveOccurred())
			rayCaCertSecret.SetNamespace(WorkingNamespace)
			Expect(cli.Create(ctx, rayCaCertSecret)).Should(Succeed())

			// Create a multinode ServingRuntime in the testNs
			multinodeServingRuntime := &kservev1alpha1.ServingRuntime{}
			err = convertToStructuredResource(multinodeServingRuntimePath, multinodeServingRuntime)
			Expect(err).NotTo(HaveOccurred())
			multinodeServingRuntime.SetNamespace(testNs)
			Expect(cli.Create(ctx, multinodeServingRuntime)).Should(Succeed())

			_, err = waitForConfigMap(cli, testNs, constants.RayTlsScriptConfigMapName, 30, 1*time.Second)
			Expect(err).NotTo(HaveOccurred())
			_, err = waitForSecret(cli, testNs, constants.RayCATlsSecretName, 30, 1*time.Second)
			Expect(err).NotTo(HaveOccurred())
		})
		It("ray tls resources should not be removed if there is a multinode ServingRuntime in the testNs", func() {
			By("creating another multinode ServingRuntime in the testNs")
			// Create another multinode ServingRuntime
			multinodeServingRuntime := &kservev1alpha1.ServingRuntime{}
			err := convertToStructuredResource(multinodeServingRuntimePath, multinodeServingRuntime)
			Expect(err).NotTo(HaveOccurred())
			multinodeServingRuntime.SetNamespace(testNs)
			multinodeServingRuntime.SetName("another-multinode-servingruntime")
			Expect(cli.Create(ctx, multinodeServingRuntime)).Should(Succeed())

			By("deleting one multinode ServingRuntime in the testNs")
			Expect(cli.Delete(ctx, multinodeServingRuntime)).Should(Succeed())

			// Check if all ray tls resources are NOT removed
			_, err = waitForConfigMap(cli, testNs, constants.RayTlsScriptConfigMapName, 30, 1*time.Second)
			Expect(err).NotTo(HaveOccurred())
			_, err = waitForSecret(cli, testNs, constants.RayCATlsSecretName, 30, 1*time.Second)
			Expect(err).NotTo(HaveOccurred())
		})
		It("ray tls resources should be removed if there is no multinode ServingRuntime in the testNs", func() {
			By("deleting a multinode ServingRuntime")
			multinodeServingRuntime := &kservev1alpha1.ServingRuntime{}
			err := convertToStructuredResource(multinodeServingRuntimePath, multinodeServingRuntime)
			Expect(err).NotTo(HaveOccurred())
			multinodeServingRuntime.SetNamespace(testNs)
			Expect(cli.Delete(ctx, multinodeServingRuntime)).Should(Succeed())

			// Check if all ray tls resources are removed
			configmap, err := waitForConfigMap(cli, testNs, constants.RayTlsScriptConfigMapName, 3, 1*time.Second)
			Expect(err).To(HaveOccurred())
			Expect(configmap).To(BeNil())

			secret, err := waitForSecret(cli, testNs, constants.RayCATlsSecretName, 3, 1*time.Second)
			Expect(err).To(HaveOccurred())
			Expect(secret).To(BeNil())
		})
	})
})
