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

package core

import (
	"context"
	"reflect"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	testutils "github.com/opendatahub-io/odh-model-controller/test/utils"
)

var _ = Describe("KServe Custom CA Cert ConfigMap Controller", func() {
	ctx := context.Background()

	AfterEach(func() {
		odhtrustedcacertConfigMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      constants.ODHGlobalCertConfigMapName,
				Namespace: WorkingNamespace,
			},
		}
		openshiftServiceCAConfigMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      constants.ServiceCAConfigMapName,
				Namespace: WorkingNamespace,
			},
		}

		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, odhtrustedcacertConfigMap))).Should(Succeed())
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, openshiftServiceCAConfigMap))).Should(Succeed())

		// Check that the odh-kserve-custom-ca-bundle configmap is also deleted since no ca bundle data will remain
		_, err := waitForConfigMap(k8sClient, WorkingNamespace, constants.KServeCACertConfigMapName, 30, 3*time.Second)
		Expect(err).To(HaveOccurred())
		Expect(err).To(BeAssignableToTypeOf(&apierrs.StatusError{}))
	})

	Context("when a configmap 'odh-trusted-ca-bundle' or 'openshift-service-ca.crt' exists", func() {
		It("should create a configmap that is for kserve custom ca cert including all data from the configmaps", func() {
			By("creating odh-trusted-ca-bundle configmap")
			odhtrustedcacertConfigMap := &corev1.ConfigMap{}
			err := convertToStructuredResource(odhtrustedcabundleConfigMapPath, odhtrustedcacertConfigMap)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Create(ctx, odhtrustedcacertConfigMap)).Should(Succeed())

			By("creating openshift-service-ca.crt configmap")
			openshiftServiceCAConfigMap := &corev1.ConfigMap{}
			err = convertToStructuredResource(openshiftServiceCAConfigMapPath, openshiftServiceCAConfigMap)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Create(ctx, openshiftServiceCAConfigMap)).Should(Succeed())

			kserveCACertConfigmap, err := waitForConfigMap(k8sClient, WorkingNamespace, constants.KServeCACertConfigMapName, 30, 1*time.Second)
			Expect(err).NotTo(HaveOccurred())
			expectedKserveCACertConfigmap := &corev1.ConfigMap{}
			err = convertToStructuredResource(odhKserveCustomCABundleConfigMapPath, expectedKserveCACertConfigmap)
			Expect(err).NotTo(HaveOccurred())
			// Trim out the last \n in the file
			expectedKserveCACertConfigmap.Data["cabundle.crt"] = strings.TrimSpace(expectedKserveCACertConfigmap.Data["cabundle.crt"])

			Expect(compareConfigMap(kserveCACertConfigmap, expectedKserveCACertConfigmap)).Should((BeTrue()))
		})
	})

	Context("when a configmap 'odh-trusted-ca-bundle' or 'openshift-service-ca.crt' is updated", func() {
		It("should update kserve custom cert configmap", func() {
			By("creating odh-trusted-ca-bundle configmap")
			odhtrustedcacertConfigMap := &corev1.ConfigMap{}
			err := convertToStructuredResource(odhtrustedcabundleConfigMapPath, odhtrustedcacertConfigMap)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Create(ctx, odhtrustedcacertConfigMap)).Should(Succeed())

			By("creating openshift-service-ca.crt configmap")
			openshiftServiceCAConfigMap := &corev1.ConfigMap{}
			err = convertToStructuredResource(openshiftServiceCAConfigMapPath, openshiftServiceCAConfigMap)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Create(ctx, openshiftServiceCAConfigMap)).Should(Succeed())

			_, err = waitForConfigMap(k8sClient, WorkingNamespace, constants.KServeCACertConfigMapName, 30, 1*time.Second)
			Expect(err).NotTo(HaveOccurred())

			By("updating odh-trusted-ca-bundle configmap")
			updatedOdhtrustedcacertConfigMap := &corev1.ConfigMap{}
			err = convertToStructuredResource(odhtrustedcabundleConfigMapUpdatedPath, updatedOdhtrustedcacertConfigMap)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Update(ctx, updatedOdhtrustedcacertConfigMap)).Should(Succeed())

			By("updating openshift-service-ca.crt configmap")
			updatedOpenshiftServiceCAConfigMap := &corev1.ConfigMap{}
			err = convertToStructuredResource(openshiftServiceCAConfigMapUpdatedPath, updatedOpenshiftServiceCAConfigMap)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Update(ctx, updatedOpenshiftServiceCAConfigMap)).Should(Succeed())

			// Wait for updating ConfigMap
			kserveCACertConfigmap, err := waitForConfigMap(k8sClient, WorkingNamespace, constants.KServeCACertConfigMapName, 30, 3*time.Second)
			Expect(err).NotTo(HaveOccurred())
			expectedKserveCACertConfigmap := &corev1.ConfigMap{}
			err = convertToStructuredResource(odhKserveCustomCABundleConfigMapUpdatedPath, expectedKserveCACertConfigmap)
			Expect(err).NotTo(HaveOccurred())
			// Trim out the last \n in the file
			expectedKserveCACertConfigmap.Data["cabundle.crt"] = strings.TrimSpace(expectedKserveCACertConfigmap.Data["cabundle.crt"])

			Expect(compareConfigMap(kserveCACertConfigmap, expectedKserveCACertConfigmap)).Should((BeTrue()))
		})
	})

	It("watches unlabelled CA ConfigMaps by name across namespaces and reconciles updates", func() {
		namespace := testutils.Namespaces.Create(ctx, k8sClient).Name
		odhTrustedCA := caInputConfigMap(namespace, constants.ODHGlobalCertConfigMapName, map[string]string{
			constants.ODHClusterCACertFileName: "odh-cluster-ca-v1",
			constants.ODHCustomCACertFileName:  "odh-custom-ca-v1",
		})
		serviceCA := caInputConfigMap(namespace, constants.ServiceCAConfigMapName, map[string]string{
			constants.ServiceCACertFileName: "service-ca-v1",
		})

		Expect(k8sClient.Create(ctx, odhTrustedCA)).To(Succeed())
		Expect(k8sClient.Create(ctx, serviceCA)).To(Succeed())
		expectKServeCABundleData(namespace, "odh-cluster-ca-v1\n\nodh-custom-ca-v1\n\nservice-ca-v1")

		By("reconciling a service CA update from the non-controller namespace")
		serviceCA = &corev1.ConfigMap{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: constants.ServiceCAConfigMapName}, serviceCA)).To(Succeed())
		serviceCA.Data[constants.ServiceCACertFileName] = "service-ca-v2"
		Expect(k8sClient.Update(ctx, serviceCA)).To(Succeed())
		expectKServeCABundleData(namespace, "odh-cluster-ca-v1\n\nodh-custom-ca-v1\n\nservice-ca-v2")

		By("reconciling an ODH trusted CA update from the non-controller namespace")
		odhTrustedCA = &corev1.ConfigMap{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: constants.ODHGlobalCertConfigMapName}, odhTrustedCA)).To(Succeed())
		odhTrustedCA.Data[constants.ODHClusterCACertFileName] = "odh-cluster-ca-v2"
		Expect(k8sClient.Update(ctx, odhTrustedCA)).To(Succeed())
		expectKServeCABundleData(namespace, "odh-cluster-ca-v2\n\nodh-custom-ca-v1\n\nservice-ca-v2")
	})

	It("keeps bundles isolated by namespace and reconciles partial source deletion", func() {
		namespaceA := testutils.Namespaces.Create(ctx, k8sClient).Name
		namespaceB := testutils.Namespaces.Create(ctx, k8sClient).Name

		for namespace, suffix := range map[string]string{
			namespaceA: "a",
			namespaceB: "b",
		} {
			Expect(k8sClient.Create(ctx, caInputConfigMap(namespace, constants.ODHGlobalCertConfigMapName, map[string]string{
				constants.ODHClusterCACertFileName: "odh-cluster-ca-" + suffix,
				constants.ODHCustomCACertFileName:  "odh-custom-ca-" + suffix,
			}))).To(Succeed())
			Expect(k8sClient.Create(ctx, caInputConfigMap(namespace, constants.ServiceCAConfigMapName, map[string]string{
				constants.ServiceCACertFileName: "service-ca-" + suffix,
			}))).To(Succeed())
		}

		expectKServeCABundleData(namespaceA, "odh-cluster-ca-a\n\nodh-custom-ca-a\n\nservice-ca-a")
		expectKServeCABundleData(namespaceB, "odh-cluster-ca-b\n\nodh-custom-ca-b\n\nservice-ca-b")

		By("removing only namespace A's service CA from its generated bundle")
		serviceCAA := &corev1.ConfigMap{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: namespaceA, Name: constants.ServiceCAConfigMapName}, serviceCAA)).To(Succeed())
		Expect(k8sClient.Delete(ctx, serviceCAA)).To(Succeed())
		expectKServeCABundleData(namespaceA, "odh-cluster-ca-a\n\nodh-custom-ca-a")
		expectKServeCABundleData(namespaceB, "odh-cluster-ca-b\n\nodh-custom-ca-b\n\nservice-ca-b")

		By("removing the last CA source and deleting only namespace A's bundle")
		odhTrustedCAA := &corev1.ConfigMap{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: namespaceA, Name: constants.ODHGlobalCertConfigMapName}, odhTrustedCAA)).To(Succeed())
		Expect(k8sClient.Delete(ctx, odhTrustedCAA)).To(Succeed())
		expectKServeCABundleAbsent(namespaceA)
		expectKServeCABundleData(namespaceB, "odh-cluster-ca-b\n\nodh-custom-ca-b\n\nservice-ca-b")
	})

	It("removes the generated bundle when the remaining CA data becomes empty", func() {
		namespace := testutils.Namespaces.Create(ctx, k8sClient).Name
		serviceCA := caInputConfigMap(namespace, constants.ServiceCAConfigMapName, map[string]string{
			constants.ServiceCACertFileName: "service-ca-v1",
		})
		Expect(k8sClient.Create(ctx, serviceCA)).To(Succeed())
		expectKServeCABundleData(namespace, "service-ca-v1")

		serviceCA = &corev1.ConfigMap{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: constants.ServiceCAConfigMapName}, serviceCA)).To(Succeed())
		serviceCA.Data[constants.ServiceCACertFileName] = " \n\t "
		Expect(k8sClient.Update(ctx, serviceCA)).To(Succeed())
		expectKServeCABundleAbsent(namespace)
	})

	It("ignores unrelated managed ConfigMaps", func() {
		namespace := testutils.Namespaces.Create(ctx, k8sClient).Name
		unrelated := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "unrelated-config",
				Namespace: namespace,
				Labels:    map[string]string{constants.ODHManaged: "true"},
			},
			Data: map[string]string{"payload": "not a CA source"},
		}
		Expect(k8sClient.Create(ctx, unrelated)).To(Succeed())

		Consistently(func() bool {
			bundle := &corev1.ConfigMap{}
			err := k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: constants.KServeCACertConfigMapName}, bundle)
			return apierrs.IsNotFound(err)
		}, time.Second, 100*time.Millisecond).Should(BeTrue())
	})
})

func caInputConfigMap(namespace, name string, data map[string]string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: data,
	}
}

func expectKServeCABundleData(namespace, expected string) {
	Eventually(func() string {
		bundle := &corev1.ConfigMap{}
		if err := k8sClient.Get(context.Background(), client.ObjectKey{Namespace: namespace, Name: constants.KServeCACertConfigMapName}, bundle); err != nil {
			return ""
		}
		return bundle.Data[constants.KServeCACertFileName]
	}, 30*time.Second, 100*time.Millisecond).Should(Equal(expected))
}

func expectKServeCABundleAbsent(namespace string) {
	Eventually(func() bool {
		bundle := &corev1.ConfigMap{}
		err := k8sClient.Get(context.Background(), client.ObjectKey{Namespace: namespace, Name: constants.KServeCACertConfigMapName}, bundle)
		return apierrs.IsNotFound(err)
	}, 30*time.Second, 100*time.Millisecond).Should(BeTrue())
}

// compareConfigMap checks if two ConfigMap data are equal, if not return false
func compareConfigMap(s1 *corev1.ConfigMap, s2 *corev1.ConfigMap) bool {
	// Two ConfigMap will be equal if the data is identical
	return reflect.DeepEqual(s1.Data, s2.Data)
}
