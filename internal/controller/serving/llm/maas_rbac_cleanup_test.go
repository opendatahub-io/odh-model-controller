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

package llm_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	llmcontroller "github.com/opendatahub-io/odh-model-controller/internal/controller/serving/llm"
	testutils "github.com/opendatahub-io/odh-model-controller/test/utils"
)

var _ = Describe("MaaS RBAC Cleanup", func() {
	var testNs string

	BeforeEach(func() {
		ctx := context.Background()
		testNamespace := testutils.Namespaces.Create(ctx, envTest.Client)
		testNs = testNamespace.Name
	})

	newRunner := func() *llmcontroller.MaaSRBACCleanupRunner {
		return &llmcontroller.MaaSRBACCleanupRunner{
			Client: envTest.Client,
			Logger: ctrl.Log.WithName("test-maas-cleanup"),
		}
	}

	It("should delete legacy MaaS Roles and RoleBindings", func(ctx SpecContext) {
		role := &rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-llmisvc-model-post-access",
				Namespace: testNs,
				Labels:    map[string]string{"app.kubernetes.io/managed-by": "odh-model-controller"},
			},
			Rules: []rbacv1.PolicyRule{
				{APIGroups: []string{"serving.kserve.io"}, Resources: []string{"llminferenceservices"}, Verbs: []string{"post"}},
			},
		}
		Expect(envTest.Create(ctx, role)).To(Succeed())

		rb := &rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-llmisvc-model-post-access-tier-binding",
				Namespace: testNs,
				Labels:    map[string]string{"app.kubernetes.io/managed-by": "odh-model-controller"},
			},
			Subjects: []rbacv1.Subject{{Kind: "Group", Name: "test-group", APIGroup: "rbac.authorization.k8s.io"}},
			RoleRef:  rbacv1.RoleRef{Kind: "Role", Name: "my-llmisvc-model-post-access", APIGroup: "rbac.authorization.k8s.io"},
		}
		Expect(envTest.Create(ctx, rb)).To(Succeed())

		Expect(newRunner().Start(ctx)).To(Succeed())

		Eventually(func() bool {
			err := envTest.Get(ctx, types.NamespacedName{Name: role.Name, Namespace: testNs}, &rbacv1.Role{})
			return err != nil
		}).WithContext(ctx).Should(BeTrue())

		Eventually(func() bool {
			err := envTest.Get(ctx, types.NamespacedName{Name: rb.Name, Namespace: testNs}, &rbacv1.RoleBinding{})
			return err != nil
		}).WithContext(ctx).Should(BeTrue())
	})

	It("should not delete Roles or RoleBindings without the managed-by label", func(ctx SpecContext) {
		role := &rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "unmanaged-model-post-access",
				Namespace: testNs,
			},
			Rules: []rbacv1.PolicyRule{
				{APIGroups: []string{"serving.kserve.io"}, Resources: []string{"llminferenceservices"}, Verbs: []string{"post"}},
			},
		}
		Expect(envTest.Create(ctx, role)).To(Succeed())

		rb := &rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "unmanaged-model-post-access-tier-binding",
				Namespace: testNs,
			},
			Subjects: []rbacv1.Subject{{Kind: "Group", Name: "test-group", APIGroup: "rbac.authorization.k8s.io"}},
			RoleRef:  rbacv1.RoleRef{Kind: "Role", Name: "unmanaged-model-post-access", APIGroup: "rbac.authorization.k8s.io"},
		}
		Expect(envTest.Create(ctx, rb)).To(Succeed())

		Expect(newRunner().Start(ctx)).To(Succeed())

		Consistently(func() error {
			return envTest.Get(ctx, types.NamespacedName{Name: role.Name, Namespace: testNs}, &rbacv1.Role{})
		}).WithContext(ctx).Should(Succeed())

		Consistently(func() error {
			return envTest.Get(ctx, types.NamespacedName{Name: rb.Name, Namespace: testNs}, &rbacv1.RoleBinding{})
		}).WithContext(ctx).Should(Succeed())
	})

	It("should not delete managed Roles with different name patterns", func(ctx SpecContext) {
		role := &rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "some-other-role",
				Namespace: testNs,
				Labels:    map[string]string{"app.kubernetes.io/managed-by": "odh-model-controller"},
			},
			Rules: []rbacv1.PolicyRule{
				{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get"}},
			},
		}
		Expect(envTest.Create(ctx, role)).To(Succeed())

		Expect(newRunner().Start(ctx)).To(Succeed())

		Consistently(func() error {
			return envTest.Get(ctx, types.NamespacedName{Name: role.Name, Namespace: testNs}, &rbacv1.Role{})
		}).WithContext(ctx).Should(Succeed())
	})

	It("should handle empty cluster gracefully", func(ctx SpecContext) {
		Expect(newRunner().Start(ctx)).To(Succeed())
	})

	It("should clean up resources across multiple namespaces", func(ctx SpecContext) {
		ns2 := testutils.Namespaces.Create(ctx, envTest.Client)

		role1 := &rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{
				Name: "svc1-model-post-access", Namespace: testNs,
				Labels: map[string]string{"app.kubernetes.io/managed-by": "odh-model-controller"},
			},
			Rules: []rbacv1.PolicyRule{{APIGroups: []string{"serving.kserve.io"}, Resources: []string{"llminferenceservices"}, Verbs: []string{"post"}}},
		}
		role2 := &rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{
				Name: "svc2-model-post-access", Namespace: ns2.Name,
				Labels: map[string]string{"app.kubernetes.io/managed-by": "odh-model-controller"},
			},
			Rules: []rbacv1.PolicyRule{{APIGroups: []string{"serving.kserve.io"}, Resources: []string{"llminferenceservices"}, Verbs: []string{"post"}}},
		}
		Expect(envTest.Create(ctx, role1)).To(Succeed())
		Expect(envTest.Create(ctx, role2)).To(Succeed())

		Expect(newRunner().Start(ctx)).To(Succeed())

		Eventually(func() bool {
			err := envTest.Get(ctx, types.NamespacedName{Name: role1.Name, Namespace: testNs}, &rbacv1.Role{})
			return err != nil
		}).WithContext(ctx).Should(BeTrue())

		Eventually(func() bool {
			err := envTest.Get(ctx, types.NamespacedName{Name: role2.Name, Namespace: ns2.Name}, &rbacv1.Role{})
			return err != nil
		}).WithContext(ctx).Should(BeTrue())
	})
})
