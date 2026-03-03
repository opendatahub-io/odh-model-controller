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
	"fmt"
	"reflect"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
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

		Expect(k8sClient.Delete(ctx, odhtrustedcacertConfigMap)).Should(Succeed())
		Expect(k8sClient.Delete(ctx, openshiftServiceCAConfigMap)).Should(Succeed())

		// Check that the odh-kserve-custom-ca-bundle configmap is also deleted since no ca bundle data will remain
		WaitForResourceDeletion(ctx, k8sClient, WorkingNamespace, constants.KServeCACertConfigMapName, &corev1.ConfigMap{})
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

			expectedKserveCACertConfigmap := &corev1.ConfigMap{}
			err = convertToStructuredResource(odhKserveCustomCABundleConfigMapPath, expectedKserveCACertConfigmap)
			Expect(err).NotTo(HaveOccurred())
			// Trim out the last \n in the file
			expectedKserveCACertConfigmap.Data["cabundle.crt"] = strings.TrimSpace(expectedKserveCACertConfigmap.Data["cabundle.crt"])

			// Wait for ConfigMap with data populated
			kserveCACertConfigmap := WaitForResourceWithCondition(ctx, k8sClient, WorkingNamespace,
				constants.KServeCACertConfigMapName, &corev1.ConfigMap{},
				func(cm *corev1.ConfigMap) error {
					if len(cm.Data) == 0 {
						return fmt.Errorf("configmap data is empty")
					}
					if _, ok := cm.Data["cabundle.crt"]; !ok {
						return fmt.Errorf("cabundle.crt key not found in configmap")
					}
					return nil
				})

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

			// Wait for initial ConfigMap with data
			WaitForResourceWithCondition(ctx, k8sClient, WorkingNamespace,
				constants.KServeCACertConfigMapName, &corev1.ConfigMap{},
				func(cm *corev1.ConfigMap) error {
					if len(cm.Data) == 0 {
						return fmt.Errorf("configmap data is empty")
					}
					if _, ok := cm.Data["cabundle.crt"]; !ok {
						return fmt.Errorf("cabundle.crt key not found in configmap")
					}
					return nil
				})

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

			expectedKserveCACertConfigmap := &corev1.ConfigMap{}
			err = convertToStructuredResource(odhKserveCustomCABundleConfigMapUpdatedPath, expectedKserveCACertConfigmap)
			Expect(err).NotTo(HaveOccurred())
			// Trim out the last \n in the file
			expectedKserveCACertConfigmap.Data["cabundle.crt"] = strings.TrimSpace(expectedKserveCACertConfigmap.Data["cabundle.crt"])

			// Wait for ConfigMap to be updated with new data
			WaitForResourceWithCondition(ctx, k8sClient, WorkingNamespace,
				constants.KServeCACertConfigMapName, &corev1.ConfigMap{},
				func(cm *corev1.ConfigMap) error {
					if len(cm.Data) == 0 {
						return fmt.Errorf("configmap data is empty")
					}
					crt, ok := cm.Data["cabundle.crt"]
					if !ok {
						return fmt.Errorf("cabundle.crt key not found in configmap")
					}
					// Check that data has been updated by comparing with expected
					expectedCrt := expectedKserveCACertConfigmap.Data["cabundle.crt"]
					if crt != expectedCrt {
						return fmt.Errorf("configmap data not yet updated")
					}
					return nil
				})
		})
	})
})

// compareConfigMap checks if two ConfigMap data are equal, if not return false
func compareConfigMap(s1 *corev1.ConfigMap, s2 *corev1.ConfigMap) bool {
	// Two ConfigMap will be equal if the data is identical
	return reflect.DeepEqual(s1.Data, s2.Data)
}
