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
	"time"

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/opendatahub-io/odh-model-controller/controllers/constants"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
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

	Context("when a multinode ServingRuntime created", func() {
		It("should create a 'ray-ca-cert' secret and 'ray-tls-scripts' configmap in the namespace where the SR exist", func() {
			testNamespace := Namespaces.Create(cli)
			testNs := testNamespace.Name
			
			// Create ray tls resource
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

			_, err = waitForConfigMap(cli, testNs, constants.RayTlsScriptConfigMapName, 30, 1*time.Second)
			Expect(err).NotTo(HaveOccurred())
			_, err = waitForSecret(cli, testNs, constants.RayCATlsSecretName, 30, 1*time.Second)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("when a multinode ServingRuntime exists", func() {
		var testNs string

		BeforeEach(func() {
			testNamespace := Namespaces.Create(cli)
			testNs = testNamespace.Name

			// Create ray tls resources
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

			// Create a multinode servingruntime
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

		It("should create a 'ray-ca-cert' secret when it is removed manually", func() {
			secret := &corev1.Secret{}
			err := cli.Get(ctx, types.NamespacedName{Name: constants.RayCATlsSecretName, Namespace: testNs}, secret)
			Expect(err).NotTo(HaveOccurred())

			By("deleting a 'ray-ca-cert' secret in the namespace")
			Expect(cli.Delete(ctx, secret)).To(Succeed())

			// Check if 'ray-ca-cert' secret is recreated
			_, err = waitForSecret(cli, testNs, constants.RayCATlsSecretName, 30, 1*time.Second)
			Expect(err).NotTo(HaveOccurred())
		})
		It("should create a 'ray-tls-scripts' configmap when it is removed manually", func() {
			configMap := &corev1.ConfigMap{}
			err := cli.Get(ctx, types.NamespacedName{Name: constants.RayTlsScriptConfigMapName, Namespace: testNs}, configMap)
			Expect(err).NotTo(HaveOccurred())

			By("deleting a 'ray-tls-scripts' configMap in the namespace")
			Expect(cli.Delete(ctx, configMap)).To(Succeed())

			// Check if 'ray-tls-scripts' configmap is recreated
			_, err = waitForConfigMap(cli, testNs, constants.RayTlsScriptConfigMapName, 30, 1*time.Second)
			Expect(err).NotTo(HaveOccurred())
		})
		It("should 'ray-tls-scripts' configmap in the namespace when original one updated", func() {
			By("updating 'ray-tls-scripts configmap")
			rayTlsScriptsUpdatedConfigMap := &corev1.ConfigMap{}
			err := convertToStructuredResource(rayTlsScriptsUpdatedPath, rayTlsScriptsUpdatedConfigMap)
			Expect(err).NotTo(HaveOccurred())
			rayTlsScriptsUpdatedConfigMap.SetNamespace(WorkingNamespace)
			Expect(cli.Update(ctx, rayTlsScriptsUpdatedConfigMap)).Should(Succeed())

			_, err = waitForConfigMap(cli, WorkingNamespace, constants.RayTlsScriptConfigMapName, 30, 1*time.Second)
			Expect(err).NotTo(HaveOccurred())

			// Check if 'ray-tls-scripts' configmap is updated.
			Eventually(func() bool {
				updatedConfigMapFromTestNs, err := waitForConfigMap(cli, testNs, constants.RayTlsScriptConfigMapName, 30, 1*time.Second)
				Expect(err).NotTo(HaveOccurred())

				return compareConfigMap(rayTlsScriptsUpdatedConfigMap, updatedConfigMapFromTestNs)

			}).WithTimeout(30 * time.Second).Should(BeTrue())

		})
		It("should update a 'ray-ca-cert' secret in the namespace when original one updated", func() {
			By("updating 'ray-ca-cert secret")
			rayCaCertUpdatedSecret := &corev1.Secret{}
			err := convertToStructuredResource(rayCaCertUpdatedPath, rayCaCertUpdatedSecret)
			Expect(err).NotTo(HaveOccurred())
			rayCaCertUpdatedSecret.SetNamespace(WorkingNamespace)
			Expect(cli.Update(ctx, rayCaCertUpdatedSecret)).Should(Succeed())

			_, err = waitForSecret(cli, WorkingNamespace, constants.RayCATlsSecretName, 30, 1*time.Second)
			Expect(err).NotTo(HaveOccurred())

			// Check if 'ray-ca-cert' secert is updated.
			Eventually(func() bool {
				updatedSecretFromTestNs, err := waitForSecret(cli, testNs, constants.RayCATlsSecretName, 30, 1*time.Second)
				Expect(err).NotTo(HaveOccurred())
				return compareSecrets(rayCaCertUpdatedSecret, updatedSecretFromTestNs)
			}).WithTimeout(30 * time.Second).Should(BeTrue())
		})
	})
	Context("when a multinode ServingRuntime removed", func() {
		var testNs string
		BeforeEach(func() {
			testNamespace := Namespaces.Create(cli)
			testNs = testNamespace.Name

			// Create ray tls resources
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

			// Create a multinode servingruntime
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
		It("ray resources should not be removed if there is a multinode ServingRuntime in the namespace", func() {
			By("creating another multinode servingruntime for test")
			// Create another multinode servingruntime
			multinodeServingRuntime := &kservev1alpha1.ServingRuntime{}
			err := convertToStructuredResource(multinodeServingRuntimePath, multinodeServingRuntime)
			Expect(err).NotTo(HaveOccurred())
			multinodeServingRuntime.SetNamespace(testNs)
			multinodeServingRuntime.SetName("another-multinode-servingruntime")
			Expect(cli.Create(ctx, multinodeServingRuntime)).Should(Succeed())

			By("deleting one multinode servingruntime")
			Expect(cli.Delete(ctx, multinodeServingRuntime)).Should(Succeed())

			// Check if all ray resources are NOT removed
			_, err = waitForConfigMap(cli, testNs, constants.RayTlsScriptConfigMapName, 30, 1*time.Second)
			Expect(err).NotTo(HaveOccurred())
			_, err = waitForSecret(cli, testNs, constants.RayCATlsSecretName, 30, 1*time.Second)
			Expect(err).NotTo(HaveOccurred())
		})
		It("ray resources should be removed if there is no multinode ServingRuntime in the namespace", func() {	
			By("deleting a multinode servingruntime")
			multinodeServingRuntime := &kservev1alpha1.ServingRuntime{}
			err := convertToStructuredResource(multinodeServingRuntimePath, multinodeServingRuntime)
			Expect(err).NotTo(HaveOccurred())
			multinodeServingRuntime.SetNamespace(testNs)			
			Expect(cli.Delete(ctx, multinodeServingRuntime)).Should(Succeed())

			// Check if all ray resources are removed
			configmap, err := waitForConfigMap(cli, testNs, constants.RayTlsScriptConfigMapName, 30, 1*time.Second)
			Expect(err).To(HaveOccurred())
			Expect(configmap).To(BeNil())

			secret, err := waitForSecret(cli, testNs, constants.RayCATlsSecretName, 30, 1*time.Second)
			Expect(err).To(HaveOccurred())
			Expect(secret).To(BeNil())
		})		
	})
})
