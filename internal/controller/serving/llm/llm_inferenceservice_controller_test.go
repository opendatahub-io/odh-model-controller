/*
Copyright 2025.

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

package llm

import (
	"context"

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/types"

	testutils "github.com/opendatahub-io/odh-model-controller/test/utils"
)

const (
	LLMServicePath1 = "./testdata/deploy/test-llm-inference-service.yaml"
	LLMServicePath2 = "./testdata/deploy/test-llm-inference-service-2.yaml"
)

var _ = Describe("LLMInferenceService Controller", func() {
	Describe("MaaS Role Reconciler Integration", func() {
		var testNs string

		BeforeEach(func() {
			ctx := context.Background()
			testNamespace := testutils.Namespaces.Create(ctx, k8sClient)
			testNs = testNamespace.Name
		})

		When("creating an LLMInferenceService", func() {
			It("should create a Role with correct specifications and proper owner references", func() {
				ctx := context.Background()

				// Create LLMInferenceService
				llmisvc := createLLMInferenceService(testNs, "test-llm-service", LLMServicePath1)
				Expect(k8sClient.Create(ctx, llmisvc)).Should(Succeed())

				// Wait for Role to be created and verify its specification
				role := waitForRole(testNs, llmisvc.Name+"-model-user")
				verifyRoleSpecification(role, llmisvc)

				// Verify owner reference
				Expect(role.GetOwnerReferences()).To(HaveLen(1))
				ownerRef := role.GetOwnerReferences()[0]
				Expect(ownerRef.UID).To(Equal(llmisvc.UID))
				Expect(ownerRef.Kind).To(Equal("LLMInferenceService"))
				Expect(ownerRef.APIVersion).To(Equal("serving.kserve.io/v1alpha1"))
				Expect(*ownerRef.Controller).To(BeTrue())
			})
		})

		When("Role is manually modified", func() {
			It("should reconcile back to desired state", func() {
				ctx := context.Background()

				// Create LLMInferenceService with Role
				llmisvc := createLLMInferenceService(testNs, "test-llm-service", LLMServicePath1)
				Expect(k8sClient.Create(ctx, llmisvc)).Should(Succeed())

				roleName := llmisvc.Name + "-model-user"
				role := waitForRole(testNs, roleName)

				// Manually modify Role (change verb from "post" to "get")
				role.Rules[0].Verbs = []string{"get"}
				Expect(k8sClient.Update(ctx, role)).Should(Succeed())

				// Verify the role is restored to the correct state
				Eventually(func() bool {
					updatedRole := &rbacv1.Role{}
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name:      roleName,
						Namespace: testNs,
					}, updatedRole)
					if err != nil {
						return false
					}

					// Check if the role has been reconciled back to the correct state
					return len(updatedRole.Rules) > 0 &&
						len(updatedRole.Rules[0].Verbs) > 0 &&
						updatedRole.Rules[0].Verbs[0] == "post"
				}, TestTimeout, TestInterval).Should(BeTrue())
			})
		})

		When("multiple LLMInferenceServices exist", func() {
			It("should create individual Roles with correct specifications", func() {
				ctx := context.Background()

				// Create multiple LLMInferenceServices in same namespace
				llmisvc1 := createLLMInferenceService(testNs, "test-llm-service-1", LLMServicePath1)
				llmisvc1.Name = "test-llm-service-1"
				Expect(k8sClient.Create(ctx, llmisvc1)).Should(Succeed())

				llmisvc2 := createLLMInferenceService(testNs, "test-llm-service-2", LLMServicePath2)
				llmisvc2.Name = "test-llm-service-2"
				Expect(k8sClient.Create(ctx, llmisvc2)).Should(Succeed())

				// Verify each has its own Role with correct resource names
				role1Name := llmisvc1.Name + "-model-user"
				role2Name := llmisvc2.Name + "-model-user"

				role1 := waitForRole(testNs, role1Name)
				verifyRoleSpecification(role1, llmisvc1)

				role2 := waitForRole(testNs, role2Name)
				verifyRoleSpecification(role2, llmisvc2)
			})
		})
	})
})

// Helper Functions

// createLLMInferenceService creates an LLMInferenceService from a testdata file
func createLLMInferenceService(namespace, name, path string) *kservev1alpha1.LLMInferenceService {
	llmisvc := &kservev1alpha1.LLMInferenceService{}
	err := testutils.ConvertToStructuredResource(path, llmisvc)
	Expect(err).NotTo(HaveOccurred())
	llmisvc.SetNamespace(namespace)
	if name != "" {
		llmisvc.Name = name
	}
	return llmisvc
}

func waitForRole(namespace, name string) *rbacv1.Role {
	GinkgoT().Helper()

	role := &rbacv1.Role{}
	Eventually(func() error {
		return k8sClient.Get(context.Background(), types.NamespacedName{
			Name:      name,
			Namespace: namespace,
		}, role)
	}, TestTimeout, TestInterval).Should(Succeed())

	return role
}

// verifyRoleSpecification validates Role matches expected template
func verifyRoleSpecification(role *rbacv1.Role, llmIsvc *kservev1alpha1.LLMInferenceService) {
	GinkgoHelper()

	// Verify Role name
	expectedName := llmIsvc.Name + "-model-user"
	Expect(role.GetName()).To(Equal(expectedName))

	// Verify Role labels
	Expect(role.GetLabels()).To(HaveKeyWithValue("app.kubernetes.io/managed-by", "odh-model-controller"))

	// Verify Role rules
	Expect(role.Rules).To(HaveLen(1))

	rule := role.Rules[0]

	// Verify API Groups, Resources, Resource Names and Verbs
	Expect(rule.APIGroups).To(HaveExactElements("serving.kserve.io"))
	Expect(rule.Resources).To(HaveExactElements("llminferenceservices"))
	Expect(rule.ResourceNames).To(HaveExactElements(llmIsvc.Name))
	Expect(rule.Verbs).To(HaveExactElements("post"))
}
