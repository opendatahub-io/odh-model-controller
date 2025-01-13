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
	"errors"
	"fmt"
	"os"
	"reflect"
	"time"

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	k8srbacv1 "k8s.io/api/rbac/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	testutils "github.com/opendatahub-io/odh-model-controller/test/utils"
)

const (
	multinodeServingRuntimePath = "./testdata/deploy/vllm-multinode-servingruntime.yaml"
)

func deployServingRuntime(path string, ctx context.Context) {
	servingRuntime := &kservev1alpha1.ServingRuntime{}
	err := testutils.ConvertToStructuredResource(path, servingRuntime)
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient.Create(ctx, servingRuntime)).Should(Succeed())
}

func deleteServingRuntime(path string, ctx context.Context) {
	servingRuntime := &kservev1alpha1.ServingRuntime{}
	err := testutils.ConvertToStructuredResource(path, servingRuntime)
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient.Delete(ctx, servingRuntime)).Should(Succeed())
}

var _ = Describe("ServingRuntime Controller (ODH Monitoring Controller)", func() {
	Context("In a modelmesh enabled namespace", func() {
		BeforeEach(func() {
			os.Setenv("MONITORING_NAMESPACE", "monitoring-ns")
			ns := &corev1.Namespace{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: WorkingNamespace}, ns)).NotTo(HaveOccurred())
			ns.Labels["modelmesh-enabled"] = "true"
			Eventually(func() error {
				return k8sClient.Update(ctx, ns)
			}, timeout, interval).ShouldNot(HaveOccurred())
		})

		It("Should manage Monitor Rolebindings ", func() {
			By("create a Rolebinding if a Serving Runtime exists.")
			deployServingRuntime(ServingRuntimePath1, ctx)

			expectedRB := &k8srbacv1.RoleBinding{}
			Expect(testutils.ConvertToStructuredResource(RoleBindingPath, expectedRB)).NotTo(HaveOccurred())
			expectedRB.Subjects[0].Namespace = MonitoringNS

			actualRB := &k8srbacv1.RoleBinding{}
			Eventually(func() error {
				namespacedNamed := types.NamespacedName{Name: expectedRB.Name, Namespace: WorkingNamespace}
				return k8sClient.Get(ctx, namespacedNamed, actualRB)
			}, timeout, interval).ShouldNot(HaveOccurred())

			Expect(RoleBindingsAreEqual(*expectedRB, *actualRB)).Should(BeTrue())

			By("create the Monitoring Rolebinding if it is removed.")

			Expect(k8sClient.Delete(ctx, actualRB)).Should(Succeed())
			Eventually(func() error {
				namespacedNamed := types.NamespacedName{Name: expectedRB.Name, Namespace: WorkingNamespace}
				return k8sClient.Get(ctx, namespacedNamed, actualRB)
			}, timeout, interval).ShouldNot(HaveOccurred())
			Expect(RoleBindingsAreEqual(*expectedRB, *actualRB)).Should(BeTrue())

			By("do not remove the Monitoring RB if at least one Serving Runtime Remains.")

			deployServingRuntime(ServingRuntimePath2, ctx)
			deleteServingRuntime(ServingRuntimePath1, ctx)
			Eventually(func() error {
				namespacedNamed := types.NamespacedName{Name: expectedRB.Name, Namespace: WorkingNamespace}
				return k8sClient.Get(ctx, namespacedNamed, actualRB)
			}, timeout, interval).ShouldNot(HaveOccurred())
			Expect(RoleBindingsAreEqual(*expectedRB, *actualRB)).Should(BeTrue())

			By("remove the Monitoring RB if no Serving Runtime exists.")

			deleteServingRuntime(ServingRuntimePath2, ctx)
			Eventually(func() error {
				namespacedNamed := types.NamespacedName{Name: expectedRB.Name, Namespace: WorkingNamespace}
				err := k8sClient.Get(ctx, namespacedNamed, actualRB)
				if apierrs.IsNotFound(err) {
					return nil
				} else {
					return errors.New("monitor Role-binding Deletion not detected")
				}
			}, timeout, interval).ShouldNot(HaveOccurred())
		})
	})
})

var _ = Describe("ServingRuntime Controller (Multi Node Reconciler)", func() {
	controllerNS := os.Getenv("POD_NAMESPACE")
	ctx := context.Background()

	Context("when a non-multinode ServingRuntime created", func() {
		It("should not create 'ray-tls' Secret and 'ray-tls-secret-reader' role/rolebinding in the testNs", func() {
			testNamespace := testutils.Namespaces.Create(ctx, k8sClient)
			testNs := testNamespace.Name

			By("creating non-multinode ServingRuntime")
			nonMultinodeServingRuntime := &kservev1alpha1.ServingRuntime{}
			err := testutils.ConvertToStructuredResource(ServingRuntimePath1, nonMultinodeServingRuntime)
			Expect(err).NotTo(HaveOccurred())
			nonMultinodeServingRuntime.SetNamespace(testNs)
			Expect(k8sClient.Create(ctx, nonMultinodeServingRuntime)).Should(Succeed())

			Eventually(func() error {
				secret := &corev1.Secret{}
				key := types.NamespacedName{Name: constants.RayTLSSecretName, Namespace: testNs}
				err = k8sClient.Get(ctx, key, secret)
				return err
			}, time.Second*3, interval).ShouldNot(Succeed())

			Eventually(func() error {
				role := &k8srbacv1.Role{}
				key := types.NamespacedName{Name: constants.RayTLSSecretReaderRoleName, Namespace: testNs}
				err = k8sClient.Get(ctx, key, role)
				return err
			}, time.Second*3, interval).ShouldNot(Succeed())

			Eventually(func() error {
				rolebinding := &k8srbacv1.RoleBinding{}
				key := types.NamespacedName{Name: constants.RayTLSSecretReaderRoleBindingName, Namespace: testNs}
				err = k8sClient.Get(ctx, key, rolebinding)
				return err
			}, time.Second*3, interval).ShouldNot(Succeed())

		})
	})
	Context("when a multinode ServingRuntime created", func() {
		It("should create 'ray-ca-tls' Secret in ctrlNS and 'ray-tls' Secret, 'ray-tls-secret-reader' role/rolebinding in the testNs", func() {
			testNamespace := testutils.Namespaces.Create(ctx, k8sClient)
			testNs := testNamespace.Name

			By("creating multinode ServingRuntime")
			multinodeServingRuntime := &kservev1alpha1.ServingRuntime{}
			err := testutils.ConvertToStructuredResource(multinodeServingRuntimePath, multinodeServingRuntime)
			Expect(err).NotTo(HaveOccurred())
			multinodeServingRuntime.SetNamespace(testNs)
			Expect(k8sClient.Create(ctx, multinodeServingRuntime)).Should(Succeed())

			Eventually(func() error {
				secret := &corev1.Secret{}
				key := types.NamespacedName{Name: constants.RayCASecretName, Namespace: controllerNS}
				err = k8sClient.Get(ctx, key, secret)
				return err
			}, timeout, interval).Should(Succeed())

			Eventually(func() error {
				secret := &corev1.Secret{}
				key := types.NamespacedName{Name: constants.RayTLSSecretName, Namespace: testNs}
				err = k8sClient.Get(ctx, key, secret)
				return err
			}, timeout, interval).Should(Succeed())

			Eventually(func() error {
				role := &k8srbacv1.Role{}
				key := types.NamespacedName{Name: constants.RayTLSSecretReaderRoleName, Namespace: testNs}
				err = k8sClient.Get(ctx, key, role)
				return err
			}, timeout, interval).Should(Succeed())

			Eventually(func() error {
				rolebinding := &k8srbacv1.RoleBinding{}
				key := types.NamespacedName{Name: constants.RayTLSSecretReaderRoleBindingName, Namespace: testNs}
				err = k8sClient.Get(ctx, key, rolebinding)
				return err
			}, timeout, interval).Should(Succeed())
		})
	})

	Context("when a multinode ServingRuntime exists", func() {
		var testNs string
		var err error
		BeforeEach(func() {
			testNamespace := testutils.Namespaces.Create(ctx, k8sClient)
			testNs = testNamespace.Name

			By("creating multinode ServingRuntime")
			multinodeServingRuntime := &kservev1alpha1.ServingRuntime{}
			err = testutils.ConvertToStructuredResource(multinodeServingRuntimePath, multinodeServingRuntime)
			Expect(err).NotTo(HaveOccurred())
			multinodeServingRuntime.SetNamespace(testNs)
			Expect(k8sClient.Create(ctx, multinodeServingRuntime)).Should(Succeed())
			Eventually(func() error {
				caSecret := &corev1.Secret{}
				key := types.NamespacedName{Name: constants.RayCASecretName, Namespace: controllerNS}
				err = k8sClient.Get(ctx, key, caSecret)
				return err
			}, timeout, interval).Should(Succeed())

			Eventually(func() error {
				secret := &corev1.Secret{}
				key := types.NamespacedName{Name: constants.RayTLSSecretName, Namespace: testNs}
				err = k8sClient.Get(ctx, key, secret)
				return err
			}, timeout, interval).Should(Succeed())

			Eventually(func() error {
				role := &k8srbacv1.Role{}
				key := types.NamespacedName{Name: constants.RayTLSSecretReaderRoleName, Namespace: testNs}
				err = k8sClient.Get(ctx, key, role)
				return err
			}, timeout, interval).Should(Succeed())

			Eventually(func() error {
				rolebinding := &k8srbacv1.RoleBinding{}
				key := types.NamespacedName{Name: constants.RayTLSSecretReaderRoleBindingName, Namespace: testNs}
				err = k8sClient.Get(ctx, key, rolebinding)
				return err
			}, timeout, interval).Should(Succeed())
		})
		It("auto-recovery for required files of multi-node ServingRuntime when it is removed or updated", func() {
			// It should rollback ca.crt in 'ray-tls' Secret in the testNs when ca.crt in the Secret is manually changed
			originalRayTlsSecret := &corev1.Secret{}
			key := types.NamespacedName{Name: constants.RayTLSSecretName, Namespace: testNs}
			Expect(k8sClient.Get(ctx, key, originalRayTlsSecret)).Should(Succeed())

			updatedRayTlsSecret := originalRayTlsSecret.DeepCopy()
			updatedRayTlsSecret.Data["ca.crt"] = []byte("wrong-data")
			Expect(k8sClient.Update(ctx, updatedRayTlsSecret)).Should(Succeed())

			rayTlsSecret := &corev1.Secret{}
			Eventually(func() error {
				key := types.NamespacedName{Name: constants.RayCASecretName, Namespace: controllerNS}
				err = k8sClient.Get(ctx, key, rayTlsSecret)
				if reflect.DeepEqual((rayTlsSecret.Data["ca.crt"]), []byte("wrong-data")) {
					err = fmt.Errorf("it is not updated")
				}
				return err
			}, timeout, interval).Should(Succeed())

			Eventually(func() error {
				originalCaSecret := &corev1.Secret{}
				key := types.NamespacedName{Name: constants.RayCASecretName, Namespace: controllerNS}
				err = k8sClient.Get(ctx, key, originalCaSecret)
				if reflect.DeepEqual((originalCaSecret.Data["tls.crt"]), rayTlsSecret.Data["ca.crt"]) {
					return fmt.Errorf("it is not rollbacked")
				}
				return err
			}, timeout, interval).Should(Succeed())

			// It should recreate 'ray-ca-tls' Secret in ctrlNS when it is manually deleted
			caSecret := &corev1.Secret{}
			key = types.NamespacedName{Name: constants.RayCASecretName, Namespace: controllerNS}
			Expect(k8sClient.Get(ctx, key, caSecret)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, caSecret)).Should(Succeed())

			reCreatedCaSecret := &corev1.Secret{}
			Eventually(func() error {
				key := types.NamespacedName{Name: constants.RayCASecretName, Namespace: controllerNS}
				err = k8sClient.Get(ctx, key, reCreatedCaSecret)
				return err
			}, timeout, interval).Should(Succeed())

			//Check if the tls Secret updates ca.crt value
			Eventually(func() error {
				rayTlsSecret := &corev1.Secret{}
				key := types.NamespacedName{Name: constants.RayTLSSecretName, Namespace: testNs}
				err = k8sClient.Get(ctx, key, rayTlsSecret)
				if !reflect.DeepEqual((rayTlsSecret.Data["ca.crt"]), (reCreatedCaSecret.Data["tls.crt"])) {
					err = fmt.Errorf("ray tls ca.cert is not updated")
				}
				return err
			}, timeout, interval).Should(Succeed())

			// It should recreate 'ray-tls' Secret in the testNs when it is manually deleted
			rayTlsSecret = &corev1.Secret{}
			key = types.NamespacedName{Name: constants.RayTLSSecretName, Namespace: testNs}
			Expect(k8sClient.Get(ctx, key, rayTlsSecret)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, rayTlsSecret)).Should(Succeed())

			Eventually(func() error {
				updatedRayTlsSecret := &corev1.Secret{}
				key := types.NamespacedName{Name: constants.RayTLSSecretName, Namespace: testNs}
				err = k8sClient.Get(ctx, key, updatedRayTlsSecret)
				return err
			}, timeout, interval).Should(Succeed())

			// It should recreate 'ray-tls-secret-reader' role in the testNs when it is manually deleted
			originalRole := &k8srbacv1.Role{}
			key = types.NamespacedName{Name: constants.RayTLSSecretReaderRoleName, Namespace: testNs}
			Expect(k8sClient.Get(ctx, key, originalRole)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, originalRole)).Should(Succeed())

			Eventually(func() error {
				newRole := &k8srbacv1.Role{}
				key := types.NamespacedName{Name: constants.RayTLSSecretReaderRoleName, Namespace: testNs}
				err = k8sClient.Get(ctx, key, newRole)
				return err
			}, timeout, interval).Should(Succeed())

			// It should recreate 'ray-tls-secret-reader-rolebinding' rolebinding in the testNs when it is manually deleted
			originalRoleBinding := &k8srbacv1.RoleBinding{}
			key = types.NamespacedName{Name: constants.RayTLSSecretReaderRoleBindingName, Namespace: testNs}
			Expect(k8sClient.Get(ctx, key, originalRoleBinding)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, originalRoleBinding)).Should(Succeed())

			Eventually(func() error {
				newRolebinding := &k8srbacv1.RoleBinding{}
				key := types.NamespacedName{Name: constants.RayTLSSecretReaderRoleBindingName, Namespace: testNs}
				err = k8sClient.Get(ctx, key, newRolebinding)
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
			multinodeServingRuntime := &kservev1alpha1.ServingRuntime{}
			err := testutils.ConvertToStructuredResource(multinodeServingRuntimePath, multinodeServingRuntime)
			Expect(err).NotTo(HaveOccurred())
			multinodeServingRuntime.SetNamespace(testNs)
			Expect(k8sClient.Create(ctx, multinodeServingRuntime)).Should(Succeed())
			Eventually(func() error {
				caSecret := &corev1.Secret{}
				key := types.NamespacedName{Name: constants.RayCASecretName, Namespace: controllerNS}
				err = k8sClient.Get(ctx, key, caSecret)
				return err
			}, timeout, interval).Should(Succeed())

			Eventually(func() error {
				secret := &corev1.Secret{}
				key := types.NamespacedName{Name: constants.RayTLSSecretName, Namespace: testNs}
				err = k8sClient.Get(ctx, key, secret)
				return err
			}, timeout, interval).Should(Succeed())

			Eventually(func() error {
				role := &k8srbacv1.Role{}
				key := types.NamespacedName{Name: constants.RayTLSSecretReaderRoleName, Namespace: testNs}
				err = k8sClient.Get(ctx, key, role)
				return err
			}, timeout, interval).Should(Succeed())

			Eventually(func() error {
				rolebinding := &k8srbacv1.RoleBinding{}
				key := types.NamespacedName{Name: constants.RayTLSSecretReaderRoleBindingName, Namespace: testNs}
				err = k8sClient.Get(ctx, key, rolebinding)
				return err
			}, timeout, interval).Should(Succeed())
		})

		It("should remove 'ray-tls' Secret, 'ray-tls-secret-reader' role/rolebinding in the testNs", func() {
			By("deleting multinode ServingRuntime")
			multinodeServingRuntime := &kservev1alpha1.ServingRuntime{}
			err := testutils.ConvertToStructuredResource(multinodeServingRuntimePath, multinodeServingRuntime)
			Expect(err).NotTo(HaveOccurred())
			multinodeServingRuntime.SetNamespace(testNs)
			Expect(k8sClient.Delete(ctx, multinodeServingRuntime)).Should(Succeed())

			Eventually(func() error {
				rayTlsSecret := &corev1.Secret{}
				key := types.NamespacedName{Name: constants.RayTLSSecretName, Namespace: testNs}
				err = k8sClient.Get(ctx, key, rayTlsSecret)
				if apierrs.IsNotFound(err) {
					return nil // Success condition: the resource is not found
				}
				return fmt.Errorf("resource still exists or another error occurred: %v", err)
			}, timeout, interval).Should(Succeed(), "Expected the resource to be deleted, but it still exists")

			Eventually(func() error {
				rayTlsSecretReaderRole := &k8srbacv1.Role{}
				key := types.NamespacedName{Name: constants.RayTLSSecretReaderRoleName, Namespace: testNs}
				err = k8sClient.Get(ctx, key, rayTlsSecretReaderRole)
				if apierrs.IsNotFound(err) {
					return nil // Success condition: the resource is not found
				}
				return fmt.Errorf("resource still exists or another error occurred: %v", err)
			}, timeout, interval).Should(Succeed(), "Expected the resource to be deleted, but it still exists")

			Eventually(func() error {
				rayTlsSecretReaderRoleBinding := &k8srbacv1.RoleBinding{}
				key := types.NamespacedName{Name: constants.RayTLSSecretReaderRoleName, Namespace: testNs}
				err = k8sClient.Get(ctx, key, rayTlsSecretReaderRoleBinding)
				if apierrs.IsNotFound(err) {
					return nil // Success condition: the resource is not found
				}
				return fmt.Errorf("resource still exists or another error occurred: %v", err)
			}, timeout, interval).Should(Succeed(), "Expected the resource to be deleted, but it still exists")
		})
	})
})
