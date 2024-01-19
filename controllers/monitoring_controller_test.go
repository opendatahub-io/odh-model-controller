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
	"errors"
	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	k8srbacv1 "k8s.io/api/rbac/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
)

func deployServingRuntime(path string, ctx context.Context) {
	servingRuntime := &kservev1alpha1.ServingRuntime{}
	err := convertToStructuredResource(path, servingRuntime)
	Expect(err).NotTo(HaveOccurred())
	Expect(cli.Create(ctx, servingRuntime)).Should(Succeed())
}

func deleteServingRuntime(path string, ctx context.Context) {
	servingRuntime := &kservev1alpha1.ServingRuntime{}
	err := convertToStructuredResource(path, servingRuntime)
	Expect(err).NotTo(HaveOccurred())
	Expect(cli.Delete(ctx, servingRuntime)).Should(Succeed())
}

var _ = Describe("ODH Controller's Monitoring Controller", func() {

	ctx := context.Background()

	Context("In a modelmesh enabled namespace", func() {
		BeforeEach(func() {
			ns := &corev1.Namespace{}
			Expect(cli.Get(ctx, types.NamespacedName{Name: WorkingNamespace}, ns)).NotTo(HaveOccurred())
			ns.Labels["modelmesh-enabled"] = "true"
			Eventually(func() error {
				return cli.Update(ctx, ns)
			}, timeout, interval).ShouldNot(HaveOccurred())
		})

		It("Should manage Monitor Rolebindings ", func() {

			By("create a Rolebinding if a Serving Runtime exists.")

			deployServingRuntime(ServingRuntimePath1, ctx)

			expectedRB := &k8srbacv1.RoleBinding{}
			Expect(convertToStructuredResource(RoleBindingPath, expectedRB)).NotTo(HaveOccurred())
			expectedRB.Subjects[0].Namespace = MonitoringNS

			actualRB := &k8srbacv1.RoleBinding{}
			Eventually(func() error {
				namespacedNamed := types.NamespacedName{Name: expectedRB.Name, Namespace: WorkingNamespace}
				return cli.Get(ctx, namespacedNamed, actualRB)
			}, timeout, interval).ShouldNot(HaveOccurred())

			Expect(RoleBindingsAreEqual(*expectedRB, *actualRB)).Should(BeTrue())

			By("create the Monitoring Rolebinding if it is removed.")

			Expect(cli.Delete(ctx, actualRB)).Should(Succeed())
			Eventually(func() error {
				namespacedNamed := types.NamespacedName{Name: expectedRB.Name, Namespace: WorkingNamespace}
				return cli.Get(ctx, namespacedNamed, actualRB)
			}, timeout, interval).ShouldNot(HaveOccurred())
			Expect(RoleBindingsAreEqual(*expectedRB, *actualRB)).Should(BeTrue())

			By("do not remove the Monitoring RB if at least one Serving Runtime Remains.")

			deployServingRuntime(ServingRuntimePath2, ctx)
			deleteServingRuntime(ServingRuntimePath1, ctx)
			Eventually(func() error {
				namespacedNamed := types.NamespacedName{Name: expectedRB.Name, Namespace: WorkingNamespace}
				return cli.Get(ctx, namespacedNamed, actualRB)
			}, timeout, interval).ShouldNot(HaveOccurred())
			Expect(RoleBindingsAreEqual(*expectedRB, *actualRB)).Should(BeTrue())

			By("remove the Monitoring RB if no Serving Runtime exists.")

			deleteServingRuntime(ServingRuntimePath2, ctx)
			Eventually(func() error {
				namespacedNamed := types.NamespacedName{Name: expectedRB.Name, Namespace: WorkingNamespace}
				err := cli.Get(ctx, namespacedNamed, actualRB)
				if apierrs.IsNotFound(err) {
					return nil
				} else {
					return errors.New("monitor Role-binding Deletion not detected")
				}
			}, timeout, interval).ShouldNot(HaveOccurred())
		})
	})
})
