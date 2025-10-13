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

package serving

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"time"

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	testutils "github.com/opendatahub-io/odh-model-controller/test/utils"
)

const (
	multinodeServingRuntimePath = "./testdata/deploy/vllm-multinode-servingruntime.yaml"
)

func deployServingRuntime(path string, namespace string, ctx context.Context) {
	servingRuntime := &kservev1alpha1.ServingRuntime{}
	err := testutils.ConvertToStructuredResource(path, servingRuntime)
	Expect(err).NotTo(HaveOccurred())
	servingRuntime.SetNamespace(namespace)
	Expect(k8sClient.Create(ctx, servingRuntime)).Should(Succeed())
}

func deleteServingRuntime(path string, namespace string, ctx context.Context) {
	servingRuntime := &kservev1alpha1.ServingRuntime{}
	err := testutils.ConvertToStructuredResource(path, servingRuntime)
	Expect(err).NotTo(HaveOccurred())
	servingRuntime.SetNamespace(namespace)
	Expect(k8sClient.Delete(ctx, servingRuntime)).Should(Succeed())
}

func fetchSecret(name, namespace string, ctx context.Context) (*corev1.Secret, error) {
	secret := &corev1.Secret{}
	key := types.NamespacedName{Name: name, Namespace: namespace}
	err := k8sClient.Get(ctx, key, secret)
	return secret, err
}

var _ = Describe("ServingRuntime Controller (Multi Node Reconciler)", func() {
	controllerNS := os.Getenv("POD_NAMESPACE")

	Context("when a non-multinode ServingRuntime created", func() {
		It("should not create 'ray-tls' Secret in the testNs", func() {
			testNamespace := testutils.Namespaces.Create(ctx, k8sClient)
			testNs := testNamespace.Name

			By("creating non-multinode ServingRuntime")
			deployServingRuntime(KserveServingRuntimePath1, testNs, ctx)

			Consistently(func() error {
				_, err := fetchSecret(constants.RayTLSSecretName, testNs, ctx)
				return err
			}, time.Second*3, interval).ShouldNot(Succeed())
		})
	})
	Context("when a multinode ServingRuntime created", func() {
		It("should create 'ray-ca-tls' Secret in ctrlNS and 'ray-tls' Secret in the testNs", func() {
			testNamespace := testutils.Namespaces.Create(ctx, k8sClient)
			testNs := testNamespace.Name

			By("creating multinode ServingRuntime")
			deployServingRuntime(multinodeServingRuntimePath, testNs, ctx)

			Eventually(func() error {
				_, err := fetchSecret(constants.RayCASecretName, controllerNS, ctx)
				return err
			}, timeout, interval).Should(Succeed())

			Eventually(func() error {
				_, err := fetchSecret(constants.RayTLSSecretName, testNs, ctx)
				return err
			}, timeout, interval).Should(Succeed())
		})
	})

	Context("when a multinode ServingRuntime exists", func() {
		var testNs string
		BeforeEach(func() {
			testNamespace := testutils.Namespaces.Create(ctx, k8sClient)
			testNs = testNamespace.Name

			By("creating multinode ServingRuntime")
			deployServingRuntime(multinodeServingRuntimePath, testNs, ctx)

			Eventually(func() error {
				_, err := fetchSecret(constants.RayCASecretName, controllerNS, ctx)
				return err
			}, timeout, interval).Should(Succeed())

			Eventually(func() error {
				_, err := fetchSecret(constants.RayTLSSecretName, testNs, ctx)
				return err
			}, timeout, interval).Should(Succeed())

		})

		It("should rollback ca.crt in 'ray-tls' Secret in the testNs when ca.crt in the Secret is manually changed", func() {
			originalRayTlsSecret, err := fetchSecret(constants.RayTLSSecretName, testNs, ctx)

			updatedRayTlsSecret := originalRayTlsSecret.DeepCopy()
			updatedRayTlsSecret.Data["ca.crt"] = []byte("wrong-data")
			Expect(k8sClient.Update(ctx, updatedRayTlsSecret)).Should(Succeed())

			var rayTlsSecret *corev1.Secret
			Eventually(func() error {
				rayTlsSecret, err = fetchSecret(constants.RayTLSSecretName, testNs, ctx)

				if !reflect.DeepEqual(rayTlsSecret.Data["ca.crt"], []byte("wrong-data")) {
					err = fmt.Errorf("tls ca cert is not updated yet")
				}
				return err
			}, timeout, interval).Should(Succeed())

			Eventually(func() error {
				rayTlsSecret, err := fetchSecret(constants.RayTLSSecretName, testNs, ctx)
				if err != nil {
					return err
				}

				originalCaSecret, err := fetchSecret(constants.RayCASecretName, controllerNS, ctx)
				if err != nil {
					return err
				}

				if !reflect.DeepEqual(originalCaSecret.Data["tls.crt"], rayTlsSecret.Data["ca.crt"]) {
					return fmt.Errorf("tls ca.crt is not synced")
				}
				return nil
			}, timeout, interval).Should(Succeed())
		})
		It("should recreate 'ray-ca-tls' Secret in ctrlNS when it is manually deleted", func() {
			caSecret, _ := fetchSecret(constants.RayCASecretName, controllerNS, ctx)
			Expect(k8sClient.Delete(ctx, caSecret)).Should(Succeed())

			var reCreatedCaSecret *corev1.Secret
			Eventually(func() error {
				var err error
				reCreatedCaSecret, err = fetchSecret(constants.RayCASecretName, controllerNS, ctx)
				return err
			}, timeout, interval).Should(Succeed())

			// Check if the tls Secret updates ca.crt value
			Eventually(func() error {
				rayTlsSecret, err := fetchSecret(constants.RayTLSSecretName, testNs, ctx)
				if !reflect.DeepEqual(rayTlsSecret.Data["ca.crt"], reCreatedCaSecret.Data["tls.crt"]) {
					err = fmt.Errorf("ray tls ca.cert is not updated")
				}
				return err
			}, timeout, interval).Should(Succeed())
		})
		It("should recreate 'ray-tls' Secret in the testNs when it is manually deleted", func() {
			rayTlsSecret, _ := fetchSecret(constants.RayTLSSecretName, testNs, ctx)
			Expect(k8sClient.Delete(ctx, rayTlsSecret)).Should(Succeed())

			Eventually(func() error {
				_, err := fetchSecret(constants.RayTLSSecretName, testNs, ctx)
				return err
			}, timeout, interval).Should(Succeed())
		})
	})

	Context("when a multinode ServingRuntime removed", func() {
		var testNs string
		BeforeEach(func() {
			testNamespace := testutils.Namespaces.Create(ctx, k8sClient)
			testNs = testNamespace.Name

			By("creating multinode ServingRuntime")
			deployServingRuntime(multinodeServingRuntimePath, testNs, ctx)

			Eventually(func() error {
				_, err := fetchSecret(constants.RayCASecretName, controllerNS, ctx)
				return err
			}, timeout, interval).Should(Succeed())

			Eventually(func() error {
				_, err := fetchSecret(constants.RayTLSSecretName, testNs, ctx)
				return err
			}, timeout, interval).Should(Succeed())
		})

		It("should remove 'ray-tls' Secret in the testNs", func() {
			By("deleting multinode ServingRuntime")

			deleteServingRuntime(multinodeServingRuntimePath, testNs, ctx)

			Eventually(func() error {
				_, err := fetchSecret(constants.RayTLSSecretName, testNs, ctx)
				if apierrs.IsNotFound(err) {
					return nil // Success condition: the resource is not found
				}
				return fmt.Errorf("resource still exists or another error occurred: %v", err)
			}, timeout, interval).Should(Succeed(), "Expected the resource to be deleted, but it still exists")
		})
	})
})
