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

	mmv1alpha1 "github.com/kserve/modelmesh-serving/apis/serving/v1alpha1"
	inferenceservicev1 "github.com/kserve/modelmesh-serving/apis/serving/v1beta1"
	corev1 "k8s.io/api/core/v1"
	authv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/types"

	mfc "github.com/manifestival/controller-runtime-client"
	mf "github.com/manifestival/manifestival"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("The Openshift model controller", func() {

	Context("When creating a Service Account Name with or without custom configMap", func() {

		It("Should create a ServiceAccountName based on existence of Custom Config Map", func() {
			client := mfc.NewClient(cli)
			opts := mf.UseClient(client)
			ctx := context.Background()
			//Step 1: No custom configmap - create default SA

			//Step 2: Deploy custom config with no SA
			//Step 3: Reconcile SA and check
			//Step 4: Deploy custom config with SA
			//Step 5: Reconcile SA and check

			//customConfigMap := &corev1.ConfigMap{}

			servingRuntime := &mmv1alpha1.ServingRuntime{}
			err := convertToStructuredResource(ServingRuntimePath1, servingRuntime, opts)
			Expect(err).NotTo(HaveOccurred())
			Expect(cli.Create(ctx, servingRuntime)).Should(Succeed())

			inferenceService := &inferenceservicev1.InferenceService{}
			err = convertToStructuredResource(InferenceService1, inferenceService, opts)
			Expect(err).NotTo(HaveOccurred())
			Expect(cli.Create(ctx, inferenceService)).Should(Succeed())

			By("By checking that the controller has created the ServiceAccount")

			crb := &authv1.ClusterRoleBinding{}
			Eventually(func() error {
				key := types.NamespacedName{Name: inferenceService.Name, Namespace: inferenceService.Namespace}
				return cli.Get(ctx, key, crb)
			}, timeout, interval).ShouldNot(HaveOccurred())

			expectedCrb := &authv1.ClusterRoleBinding{}
			err = convertToStructuredResource(ExpectedCrbPath, expectedCrb, opts)
			Expect(err).NotTo(HaveOccurred())

			Expect(CompareInferenceServiceCRBs(*crb, *expectedCrb)).Should(BeTrue())

			//Deploy custom configmap with no SA
			customConfigMapNoSA := &corev1.ConfigMap{}
			err = convertToStructuredResource(CustomConfigPath, customConfigMapNoSA, opts)
			Expect(err).NotTo(HaveOccurred())
			Expect(cli.Create(ctx, customConfigMapNoSA)).Should(Succeed())

			crbNoSA := &authv1.ClusterRoleBinding{}
			Eventually(func() error {
				key := types.NamespacedName{Name: inferenceService.Name, Namespace: inferenceService.Namespace}
				return cli.Get(ctx, key, crbNoSA)
			}, timeout, interval).ShouldNot(HaveOccurred())

			expectedCrbNoSA := &authv1.ClusterRoleBinding{}
			err = convertToStructuredResource(ExpectedCrbPath, expectedCrbNoSA, opts)
			Expect(err).NotTo(HaveOccurred())

			Expect(CompareInferenceServiceCRBs(*crbNoSA, *expectedCrbNoSA)).Should(BeTrue())

			//Deploy custom configmap with SA
			customConfigMapSA := &corev1.ConfigMap{}
			err = convertToStructuredResource(CustomConfigSAPath, customConfigMapSA, opts)
			Expect(err).NotTo(HaveOccurred())
			Expect(cli.Create(ctx, customConfigMapSA)).Should(Succeed())

			crbSA := &authv1.ClusterRoleBinding{}
			Eventually(func() error {
				key := types.NamespacedName{Name: inferenceService.Name, Namespace: inferenceService.Namespace}
				return cli.Get(ctx, key, crbSA)
			}, timeout, interval).ShouldNot(HaveOccurred())

			expectedCrbSA := &authv1.ClusterRoleBinding{}
			err = convertToStructuredResource(ExpectedCrbPath, expectedCrbSA, opts)
			Expect(err).NotTo(HaveOccurred())

			Expect(CompareInferenceServiceCRBs(*crbSA, *expectedCrbSA)).Should(BeTrue())
		})
	})
})
