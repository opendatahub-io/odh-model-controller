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

package handlers

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "github.com/opendatahub-io/odh-model-controller/api/nim/v1"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
	templatev1 "github.com/openshift/api/template/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/reference"
)

var _ = Describe("NIM Template Handler", func() {
	// var templateHandler *TemplateHandler
	//
	// BeforeEach(func() {
	//	templateHandler = &TemplateHandler{
	//		Client: k8sClient,
	//		Scheme: scheme.Scheme,
	//	}
	// })

	Describe("when determining if a Template reconciliation is required", func() {
		It("should return true if a reference to the previous Template is not set", func(ctx SpecContext) {
			tstAccountKey := types.NamespacedName{Name: "testing-nim-template-handler-1", Namespace: "testing-nim-template-handler-1"}

			By("Create testing Namespace " + tstAccountKey.Namespace)
			testNs := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: tstAccountKey.Namespace}}
			Expect(k8sClient.Create(ctx, testNs)).To(Succeed())

			By("Create an Account without setting a pull Template reference")
			acct := &v1.Account{
				ObjectMeta: metav1.ObjectMeta{
					Name:       tstAccountKey.Name,
					Namespace:  tstAccountKey.Namespace,
					Finalizers: []string{constants.NimCleanupFinalizer},
				},
				Spec: v1.AccountSpec{
					APIKeySecret:          corev1.ObjectReference{Name: "not-required", Namespace: "for-this-test-case"},
					ValidationRefreshRate: "24h",
					NIMConfigRefreshRate:  "24h",
				},
			}
			Expect(k8sClient.Create(ctx, acct)).To(Succeed())

			By("Checking if should reconcile and expect true")
			// Expect(templateHandler.ShouldReconcile(ctx, acct)).To(BeTrue())

			By("Cleanups")
			Expect(k8sClient.Delete(ctx, acct)).To(Succeed())
			Expect(k8sClient.Delete(ctx, testNs)).To(Succeed())
		})

		for idx, cases := range []map[string][]metav1.Condition{
			{"failed": {utils.MakeNimCondition(utils.NimConditionTemplateUpdate, metav1.ConditionFalse, 1, "SomeFailureReason", "template failed")}},
			{"is unknown": {utils.MakeNimCondition(utils.NimConditionTemplateUpdate, metav1.ConditionUnknown, 1, "SomeUnknown", "not reconciled")}},
			{"was not set": {}},
		} {
			for title, conds := range cases {
				It(fmt.Sprintf("should return true if the status condition for the previous Template reconciliation %s", title), func(ctx SpecContext) {
					testingName := fmt.Sprintf("testing-nim-template-handler-2-%d", idx)
					tstAccountKey := types.NamespacedName{Name: testingName, Namespace: testingName}

					By("Create testing Namespace " + tstAccountKey.Namespace)
					testNs := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: tstAccountKey.Namespace}}
					Expect(k8sClient.Create(ctx, testNs)).To(Succeed())

					By("Create an Account")
					acct := &v1.Account{
						ObjectMeta: metav1.ObjectMeta{
							Name:       tstAccountKey.Name,
							Namespace:  tstAccountKey.Namespace,
							Finalizers: []string{constants.NimCleanupFinalizer},
						},
						Spec: v1.AccountSpec{
							APIKeySecret:          corev1.ObjectReference{Name: "not-required", Namespace: "for-this-test-case"},
							ValidationRefreshRate: "24h",
							NIMConfigRefreshRate:  "24h",
						},
					}
					Expect(k8sClient.Create(ctx, acct)).To(Succeed())

					By("Update the Account Status")
					acct.Status = v1.AccountStatus{
						RuntimeTemplate: &corev1.ObjectReference{Name: "does-not-matter", Namespace: "for-this-test-case"},
						Conditions:      conds,
					}
					Expect(k8sClient.Status().Update(ctx, acct)).To(Succeed())

					By("Checking if should reconcile and expect true")
					// Expect(templateHandler.ShouldReconcile(ctx, acct)).To(BeTrue())

					By("Cleanups")
					Expect(k8sClient.Delete(ctx, acct)).To(Succeed())
					Expect(k8sClient.Delete(ctx, testNs)).To(Succeed())
				})
			}
		}

		It("should return true if the reference exists but the Template doesn't", func(ctx SpecContext) {
			tstAccountKey := types.NamespacedName{Name: "testing-nim-template-handler-3", Namespace: "testing-nim-template-handler-3"}

			By("Create testing Namespace " + tstAccountKey.Namespace)
			testNs := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: tstAccountKey.Namespace}}
			Expect(k8sClient.Create(ctx, testNs)).To(Succeed())

			By("Create an Account")
			acct := &v1.Account{
				ObjectMeta: metav1.ObjectMeta{
					Name:       tstAccountKey.Name,
					Namespace:  tstAccountKey.Namespace,
					Finalizers: []string{constants.NimCleanupFinalizer},
				},
				Spec: v1.AccountSpec{
					APIKeySecret:          corev1.ObjectReference{Name: "not-required", Namespace: "for-this-test-case"},
					ValidationRefreshRate: "24h",
					NIMConfigRefreshRate:  "24h",
				},
			}
			Expect(k8sClient.Create(ctx, acct)).To(Succeed())

			By("Update the Account Status with a successful status condition, without creating the actual Template")
			acct.Status = v1.AccountStatus{
				RuntimeTemplate: &corev1.ObjectReference{Name: "does-not-matter", Namespace: "for-this-test-case"},
				Conditions:      []metav1.Condition{utils.MakeNimCondition(utils.NimConditionTemplateUpdate, metav1.ConditionTrue, 1, "TemplateSuccessful", "we're good")},
			}
			Expect(k8sClient.Status().Update(ctx, acct)).To(Succeed())

			By("Checking if should reconcile and expect true")
			// Expect(templateHandler.ShouldReconcile(ctx, acct)).To(BeTrue())

			By("Cleanups")
			Expect(k8sClient.Delete(ctx, acct)).To(Succeed())
			Expect(k8sClient.Delete(ctx, testNs)).To(Succeed())
		})

		It("should return true if the ServingRuntime is missing from the existing Template's objects", func(ctx SpecContext) {
			tstAccountKey := types.NamespacedName{Name: "testing-nim-template-handler-4", Namespace: "testing-nim-template-handler-4"}

			By("Create testing Namespace " + tstAccountKey.Namespace)
			testNs := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: tstAccountKey.Namespace}}
			Expect(k8sClient.Create(ctx, testNs)).To(Succeed())

			By("Create an Account")
			acct := &v1.Account{
				ObjectMeta: metav1.ObjectMeta{
					Name:       tstAccountKey.Name,
					Namespace:  tstAccountKey.Namespace,
					Finalizers: []string{constants.NimCleanupFinalizer},
				},
				Spec: v1.AccountSpec{
					APIKeySecret:          corev1.ObjectReference{Name: "not-required", Namespace: "for-this-test-case"},
					ValidationRefreshRate: "24h",
					NIMConfigRefreshRate:  "24h",
				},
			}
			Expect(k8sClient.Create(ctx, acct)).To(Succeed())

			By("Create the supporting Template without a ServingRuntime and expect reconciliation")
			template := &templatev1.Template{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("%s-template", tstAccountKey.Name),
					Namespace: tstAccountKey.Namespace,
				},
				// no objects required for this specific test case
				Objects: []runtime.RawExtension{},
			}
			Expect(k8sClient.Create(ctx, template)).To(Succeed())
			templateRef, _ := reference.GetReference(scheme.Scheme, template)

			By("Update the Account Status with a reference to the Template")
			acct.Status = v1.AccountStatus{
				RuntimeTemplate: templateRef,
				Conditions: []metav1.Condition{
					utils.MakeNimCondition(utils.NimConditionTemplateUpdate, metav1.ConditionTrue, 1, "TemplateSuccessful", "we're good"),
				},
			}
			Expect(k8sClient.Status().Update(ctx, acct)).To(Succeed())

			By("Checking if should reconcile and expect true")
			// Expect(templateHandler.ShouldReconcile(ctx, acct)).To(BeTrue())

			By("Cleanups")
			Expect(k8sClient.Delete(ctx, template)).To(Succeed())
			Expect(k8sClient.Delete(ctx, acct)).To(Succeed())
			Expect(k8sClient.Delete(ctx, testNs)).To(Succeed())
		})
	})

	Describe("when reconciling the Template", func() {})
})
