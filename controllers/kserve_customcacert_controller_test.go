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
	"reflect"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/opendatahub-io/odh-model-controller/controllers/constants"
	corev1 "k8s.io/api/core/v1"
)

const (
	odhtrustedcabundleConfigMapUpdatedPath = "./testdata/configmaps/odh-trusted-ca-bundle-configmap-updated.yaml"
	kservecustomcacertConfigMapUpdatedPath = "./testdata/configmaps/odh-kserve-custom-ca-cert-configmap-updated.yaml"
)

var _ = Describe("KServe Custom CA Cert ConfigMap controller", func() {
	ctx := context.Background()

	Context("when a configmap 'odh-trusted-ca-bundle' exists", func() {
		It("should create a configmap that is for kserve custom ca cert", func() {
			By("creating odh-trusted-ca-bundle configmap")
			odhtrustedcacertConfigMap := &corev1.ConfigMap{}
			err := convertToStructuredResource(odhtrustedcabundleConfigMapPath, odhtrustedcacertConfigMap)
			Expect(err).NotTo(HaveOccurred())
			Expect(cli.Create(ctx, odhtrustedcacertConfigMap)).Should(Succeed())

			_, err = waitForConfigMap(cli, WorkingNamespace, constants.KServeCACertConfigMapName, 30, 1*time.Second)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("when a configmap 'odh-trusted-ca-bundle' updated", func() {
		It("should update kserve custom cert configmap", func() {
			By("creating odh-trusted-ca-bundle configmap")
			odhtrustedcacertConfigMap := &corev1.ConfigMap{}
			err := convertToStructuredResource(odhtrustedcabundleConfigMapPath, odhtrustedcacertConfigMap)
			Expect(err).NotTo(HaveOccurred())
			Expect(cli.Create(ctx, odhtrustedcacertConfigMap)).Should(Succeed())

			_, err = waitForConfigMap(cli, WorkingNamespace, constants.KServeCACertConfigMapName, 30, 1*time.Second)
			Expect(err).NotTo(HaveOccurred())

			By("updating odh-trusted-ca-bundle configmap")
			updatedOdhtrustedcacertConfigMap := &corev1.ConfigMap{}
			err = convertToStructuredResource(odhtrustedcabundleConfigMapUpdatedPath, updatedOdhtrustedcacertConfigMap)
			Expect(err).NotTo(HaveOccurred())
			Expect(cli.Update(ctx, updatedOdhtrustedcacertConfigMap)).Should(Succeed())

			// Wait for updating ConfigMap
			time.Sleep(1 * time.Second)
			kserveCACertConfigmap, err := waitForConfigMap(cli, WorkingNamespace, constants.KServeCACertConfigMapName, 30, 1*time.Second)
			Expect(err).NotTo(HaveOccurred())
			expectedKserveCACertConfigmap := &corev1.ConfigMap{}
			err = convertToStructuredResource(kservecustomcacertConfigMapUpdatedPath, expectedKserveCACertConfigmap)
			Expect(err).NotTo(HaveOccurred())

			Expect(compareConfigMap(kserveCACertConfigmap, expectedKserveCACertConfigmap)).Should((BeTrue()))
		})
	})
})

// compareConfigMap checks if two ConfigMap data are equal, if not return false
func compareConfigMap(s1 *corev1.ConfigMap, s2 *corev1.ConfigMap) bool {
	// Two ConfigMap will be equal if the data is identical
	return reflect.DeepEqual(s1.Data, s2.Data)
}
