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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/reference"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "github.com/opendatahub-io/odh-model-controller/api/nim/v1"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
)

var _ = Describe("NIM Pull Secret Handler", func() {
	var pullSecretHandler *PullSecretHandler

	BeforeEach(func() {
		pullSecretHandler = &PullSecretHandler{
			//ApiKey:     "my-fake-api-key",
			Client:     testClient,
			Scheme:     scheme.Scheme,
			KubeClient: k8sClient,
			KeyManager: &APIKeyManager{Client: testClient},
		}
	})

	Describe("when handling pull secrets reconciliation requests", func() {
		It("should requeue if the api key secret doesn't exist", func(ctx SpecContext) {
			tstAccountKey := types.NamespacedName{Name: "testing-nim-pull-handler-1", Namespace: "testing-nim-pull-handler-1"}

			By("Create testing Namespace " + tstAccountKey.Namespace)
			testNs := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: tstAccountKey.Namespace}}
			Expect(testClient.Create(ctx, testNs)).To(Succeed())

			By("Create an Account referencing a non existing API Key Secret")
			acct := &v1.Account{
				ObjectMeta: metav1.ObjectMeta{
					Name:       tstAccountKey.Name,
					Namespace:  tstAccountKey.Namespace,
					Finalizers: []string{constants.NimCleanupFinalizer},
				},
				Spec: v1.AccountSpec{
					APIKeySecret:          corev1.ObjectReference{Name: "im-not-here", Namespace: "this-is-not-happening"},
					ValidationRefreshRate: "24h",
					NIMConfigRefreshRate:  "24h",
				},
			}
			Expect(testClient.Create(ctx, acct)).To(Succeed())

			By("Run the handler")
			resp := pullSecretHandler.Handle(ctx, acct)

			By("Verify the response")
			Expect(resp.Error.Error()).To(Equal("secrets \"im-not-here\" not found"))
			Expect(resp.Requeue).To(BeFalse())
			Expect(resp.Continue).To(BeFalse())

			By("Clean up")
			Expect(testClient.Delete(ctx, acct)).To(Succeed())
			Expect(testClient.Delete(ctx, testNs)).To(Succeed())
		})

		It("should create if needed and re-queue", func(ctx SpecContext) {
			tstAccountKey := types.NamespacedName{Name: "testing-nim-pull-handler-2", Namespace: "testing-nim-pull-handler-2"}

			By("Create testing Namespace " + tstAccountKey.Namespace)
			testNs := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: tstAccountKey.Namespace}}
			Expect(testClient.Create(ctx, testNs)).To(Succeed())

			By("Create an API Key Secret")
			dummyApiKey := "fake-key-not-working-use-at-your-own-discretion"
			apiKeySecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tstAccountKey.Name + "-api-key",
					Namespace: tstAccountKey.Namespace,
					Labels:    map[string]string{"opendatahub.io/managed": "true"},
				},
				Data: map[string][]byte{
					"api_key": []byte(dummyApiKey),
				},
			}
			Expect(testClient.Create(ctx, apiKeySecret)).To(Succeed())
			apiKeySecretRef, refErr := reference.GetReference(testClient.Scheme(), apiKeySecret)
			Expect(refErr).NotTo(HaveOccurred())

			By("Create an Account referencing a creates API Key Secret")
			acct := &v1.Account{
				ObjectMeta: metav1.ObjectMeta{
					Name:       tstAccountKey.Name,
					Namespace:  tstAccountKey.Namespace,
					Finalizers: []string{constants.NimCleanupFinalizer},
				},
				Spec: v1.AccountSpec{
					APIKeySecret:          *apiKeySecretRef,
					ValidationRefreshRate: "24h",
					NIMConfigRefreshRate:  "24h",
				},
			}
			Expect(testClient.Create(ctx, acct)).To(Succeed())

			By("Run the handler")
			resp := pullSecretHandler.Handle(ctx, acct)

			By("Verify the response")
			Expect(resp.Error).To(BeNil())
			Expect(resp.Requeue).To(BeTrue())
			Expect(resp.Continue).To(BeFalse())

			By("Verify Account status update")
			account := &v1.Account{}
			Expect(testClient.Get(ctx, client.ObjectKeyFromObject(acct), account))
			Expect(account.Status.NIMPullSecret).NotTo(BeNil())

			By("Verify the created Pull Secret")
			pullSecret, psErr := k8sClient.CoreV1().Secrets(account.Status.NIMPullSecret.Namespace).Get(ctx, account.Status.NIMPullSecret.Name, metav1.GetOptions{})
			Expect(psErr).NotTo(HaveOccurred())
			Expect(pullSecret.Labels).To(Equal(commonBaseLabels))
			expectedData, _ := GetPullSecretData(dummyApiKey)
			Expect(pullSecret.Data).To(Equal(expectedData))

			By("Clean up")
			Expect(testClient.Delete(ctx, pullSecret)).To(Succeed())
			Expect(testClient.Delete(ctx, apiKeySecret)).To(Succeed())
			Expect(testClient.Delete(ctx, account)).To(Succeed())
			Expect(testClient.Delete(ctx, testNs)).To(Succeed())
		})

		It("should respect user added metadata and not reconcile", func(ctx SpecContext) {

		})
	})

	//////////////////////////////////////////////////////////////////////////////////////////////////

	Describe("when determining if a pull Secret reconciliation is required", func() {
		for idx, cases := range []map[string][]metav1.Condition{
			{"failed": {utils.MakeNimCondition(utils.NimConditionSecretUpdate, metav1.ConditionFalse, 1, "SomeFailureReason", "pull secret failed")}},
			{"is unknown": {utils.MakeNimCondition(utils.NimConditionSecretUpdate, metav1.ConditionUnknown, 1, "SomeUnknown", "not reconciled")}},
			{"was not set": {}},
		} {
			for title, conds := range cases {
				It(fmt.Sprintf("should return true if the status condition for the previous pull Secret reconciliation %s", title), func(ctx SpecContext) {
					testingName := fmt.Sprintf("testing-nim-pull-handler-2-%d", idx)
					tstAccountKey := types.NamespacedName{Name: testingName, Namespace: testingName}

					By("Create testing Namespace " + tstAccountKey.Namespace)
					testNs := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: tstAccountKey.Namespace}}
					Expect(testClient.Create(ctx, testNs)).To(Succeed())

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
					Expect(testClient.Create(ctx, acct)).To(Succeed())

					By("Update the Account Status")
					acct.Status = v1.AccountStatus{
						NIMPullSecret: &corev1.ObjectReference{Name: "does-not-matter", Namespace: "for-this-test-case"},
						Conditions:    conds,
					}
					Expect(testClient.Status().Update(ctx, acct)).To(Succeed())

					By("Checking if should reconcile and expect true")
					// Expect(pullSecretHandler.ShouldReconcile(ctx, acct)).To(BeTrue())

					By("Cleanups")
					Expect(testClient.Delete(ctx, acct)).To(Succeed())
					Expect(testClient.Delete(ctx, testNs)).To(Succeed())
				})
			}
		}

		It("should return true if the reference exists but the Secret doesn't", func(ctx SpecContext) {
			tstAccountKey := types.NamespacedName{Name: "testing-nim-pull-handler-3", Namespace: "testing-nim-pull-handler-3"}

			By("Create testing Namespace " + tstAccountKey.Namespace)
			testNs := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: tstAccountKey.Namespace}}
			Expect(testClient.Create(ctx, testNs)).To(Succeed())

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
			Expect(testClient.Create(ctx, acct)).To(Succeed())

			By("Update the Account Status with a successful status condition, without creating the actual pull Secret")
			acct.Status = v1.AccountStatus{
				NIMPullSecret: &corev1.ObjectReference{Name: "does-not-matter", Namespace: "for-this-test-case"},
				Conditions:    []metav1.Condition{utils.MakeNimCondition(utils.NimConditionSecretUpdate, metav1.ConditionTrue, 1, "PullSecretSuccessful", "we're good")},
			}
			Expect(testClient.Status().Update(ctx, acct)).To(Succeed())

			By("Checking if should reconcile and expect true")
			// Expect(pullSecretHandler.ShouldReconcile(ctx, acct)).To(BeTrue())

			By("Cleanups")
			Expect(testClient.Delete(ctx, acct)).To(Succeed())
			Expect(testClient.Delete(ctx, testNs)).To(Succeed())
		})

		It("should return true if the existing Secret's data differs from the expected", func(ctx SpecContext) {
			tstAccountKey := types.NamespacedName{Name: "testing-nim-pull-handler-4", Namespace: "testing-nim-pull-handler-4"}

			By("Create testing Namespace " + tstAccountKey.Namespace)
			testNs := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: tstAccountKey.Namespace}}
			Expect(testClient.Create(ctx, testNs)).To(Succeed())

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
			Expect(testClient.Create(ctx, acct)).To(Succeed())

			By("Create the supporting pull Secret with the wrong data and expect reconciliation")
			pullSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("%s-pull", tstAccountKey.Name),
					Namespace: tstAccountKey.Namespace,
				},
				Data: map[string][]byte{
					"wrong_key": []byte("wrong_value"),
				},
			}
			Expect(testClient.Create(ctx, pullSecret)).To(Succeed())
			pullSecretRef, _ := reference.GetReference(scheme.Scheme, pullSecret)

			By("Update the Account Status with a successful status condition, without creating the actual pull Secret")
			acct.Status = v1.AccountStatus{
				NIMPullSecret: pullSecretRef,
				Conditions:    []metav1.Condition{utils.MakeNimCondition(utils.NimConditionSecretUpdate, metav1.ConditionTrue, 1, "PullSecretSuccessful", "we're good")},
			}
			Expect(testClient.Status().Update(ctx, acct)).To(Succeed())

			By("Checking if should reconcile and expect true")
			// Expect(pullSecretHandler.ShouldReconcile(ctx, acct)).To(BeTrue())

			By("Cleanups")
			Expect(testClient.Delete(ctx, pullSecret)).To(Succeed())
			Expect(testClient.Delete(ctx, acct)).To(Succeed())
			Expect(testClient.Delete(ctx, testNs)).To(Succeed())
		})

		It("should return false if the existing Secret's data is as expected", func(ctx SpecContext) {
			tstAccountKey := types.NamespacedName{Name: "testing-nim-pull-handler-5", Namespace: "testing-nim-pull-handler-5"}

			By("Create testing Namespace " + tstAccountKey.Namespace)
			testNs := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: tstAccountKey.Namespace}}
			Expect(testClient.Create(ctx, testNs)).To(Succeed())

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
			Expect(testClient.Create(ctx, acct)).To(Succeed())

			By("Create the supporting pull Secret with the correct data, reconciliation is not expected")
			// data, _ := GetPullSecretData(pullSecretHandler.ApiKey)
			// pullSecret := &corev1.Secret{
			//	ObjectMeta: metav1.ObjectMeta{
			//		Name:      fmt.Sprintf("%s-pull", tstAccountKey.Name),
			//		Namespace: tstAccountKey.Namespace,
			//	},
			//	Data: data,
			// }
			// Expect(testClient.Create(ctx, pullSecret)).To(Succeed())
			// pullSecretRef, _ := reference.GetReference(scheme.Scheme, pullSecret)

			By("Update the Account Status with a successful status condition, without creating the actual pull Secret")
			// acct.Status = v1.AccountStatus{
			//	NIMPullSecret: pullSecretRef,
			//	Conditions:    []metav1.Condition{utils.MakeNimCondition(utils.NimConditionSecretUpdate, metav1.ConditionTrue, 1, "PullSecretSuccessful", "we're good")},
			// }
			Expect(testClient.Status().Update(ctx, acct)).To(Succeed())

			By("Checking if should reconcile and expect false")
			// Expect(pullSecretHandler.ShouldReconcile(ctx, acct)).To(BeFalse())

			By("Cleanups")
			// Expect(testClient.Delete(ctx, pullSecret)).To(Succeed())
			Expect(testClient.Delete(ctx, acct)).To(Succeed())
			Expect(testClient.Delete(ctx, testNs)).To(Succeed())
		})
	})

	Describe("when reconciling the pull Secret", func() {
		It("should create new pull Secret if one doesn't exists and re-queue", func(ctx SpecContext) {
			tstAccountKey := types.NamespacedName{Name: "testing-nim-pull-handler-6", Namespace: "testing-nim-pull-handler-6"}

			By("Create testing Namespace " + tstAccountKey.Namespace)
			testNs := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: tstAccountKey.Namespace}}
			Expect(testClient.Create(ctx, testNs)).To(Succeed())

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
			Expect(testClient.Create(ctx, acct)).To(Succeed())

			By("Reconciling")
			// result, err := pullSecretHandler.Reconcile(ctx, acct)

			By("Verify successful result and re-queueing")
			// Expect(err).NotTo(HaveOccurred())
			// Expect(result.Requeue).To(BeTrue())

			By("Verify the Account status")
			uAccount := &v1.Account{}
			Expect(testClient.Get(ctx, tstAccountKey, uAccount)).To(Succeed())
			Expect(uAccount.Status.NIMPullSecret).NotTo(BeNil())
			Expect(meta.IsStatusConditionPresentAndEqual(uAccount.Status.Conditions, utils.NimConditionSecretUpdate.String(), metav1.ConditionTrue)).To(BeTrue())

			By("Verify the created pull Secret")
			pullSecret := &corev1.Secret{}
			Expect(testClient.Get(ctx, utils.ObjectKeyFromReference(uAccount.Status.NIMPullSecret), pullSecret)).To(Succeed())
			// expectedData, _ := GetPullSecretData(pullSecretHandler.ApiKey)
			// Expect(utils.NimEqualities.DeepEqual(pullSecret.Data, expectedData)).To(BeTrue())

			By("Cleanups")
			Expect(testClient.Delete(ctx, pullSecret)).To(Succeed())
			Expect(testClient.Delete(ctx, uAccount)).To(Succeed())
			Expect(testClient.Delete(ctx, testNs)).To(Succeed())

		})

		It("should reconcile the data for existing Secrets, respecting metadata changes and re-queue", func(ctx SpecContext) {
			tstAccountKey := types.NamespacedName{Name: "testing-nim-pull-handler-7", Namespace: "testing-nim-pull-handler-7"}

			By("Create testing Namespace " + tstAccountKey.Namespace)
			testNs := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: tstAccountKey.Namespace}}
			Expect(testClient.Create(ctx, testNs)).To(Succeed())

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
			Expect(testClient.Create(ctx, acct)).To(Succeed())

			By("Create the supporting pull Secret")
			pullSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("%s-pull", tstAccountKey.Name),
					Namespace: tstAccountKey.Namespace,
					Labels: map[string]string{
						"this-label-should": "be-respected-in-reconciliation",
					},
					Annotations: map[string]string{
						"this-annotation-to": "is-expected-as-well",
					},
				},
				Data: map[string][]byte{corev1.DockerConfigJsonKey: []byte("this-should-be-replaced")},
			}
			Expect(testClient.Create(ctx, pullSecret)).To(Succeed())
			pullSecretRef, _ := reference.GetReference(scheme.Scheme, pullSecret)

			By("Update the Account Status with a successful status condition and a reference for the pull Secret")
			acct.Status = v1.AccountStatus{
				NIMPullSecret: pullSecretRef,
				Conditions:    []metav1.Condition{utils.MakeNimCondition(utils.NimConditionSecretUpdate, metav1.ConditionFalse, 1, "PullSecretNoySuccessful", "we're not ok")},
			}
			Expect(testClient.Status().Update(ctx, acct)).To(Succeed())

			By("Reconciling")
			// result, err := pullSecretHandler.Reconcile(ctx, acct)

			By("Verify successful result and re-queueing")
			// Expect(err).NotTo(HaveOccurred())
			// Expect(result.Requeue).To(BeTrue())

			By("Verify the Account status")
			uAccount := &v1.Account{}
			Expect(testClient.Get(ctx, tstAccountKey, uAccount)).To(Succeed())
			Expect(meta.IsStatusConditionPresentAndEqual(uAccount.Status.Conditions, utils.NimConditionSecretUpdate.String(), metav1.ConditionTrue)).To(BeTrue())

			By("Verify the updated pull Secret and metadata respect")
			uPullSecret := &corev1.Secret{}
			Expect(testClient.Get(ctx, utils.ObjectKeyFromReference(uAccount.Status.NIMPullSecret), uPullSecret)).To(Succeed())
			// expectedData, _ := GetPullSecretData(pullSecretHandler.ApiKey)
			// Expect(utils.NimEqualities.DeepEqual(uPullSecret.Data, expectedData)).To(BeTrue())
			Expect(uPullSecret.Labels).To(HaveKeyWithValue("this-label-should", "be-respected-in-reconciliation"))
			Expect(uPullSecret.Annotations).To(HaveKeyWithValue("this-annotation-to", "is-expected-as-well"))

			By("Cleanups")
			Expect(testClient.Delete(ctx, uPullSecret)).To(Succeed())
			Expect(testClient.Delete(ctx, uAccount)).To(Succeed())
			Expect(testClient.Delete(ctx, testNs)).To(Succeed())
		})

		It("should report an account failure and set check timestamp for failures to reconcile the pull Secret", func(ctx SpecContext) {
			// using a designated handler with an empty scheme without registering Secrets to force Secret creation failure
			// handler := &PullSecretHandler{
			//	ApiKey:    "my-fake-api-key",
			//	Client:    testClient,
			//	Clientset: testClientset,
			//	Scheme:    runtime.NewScheme(),
			// }

			tstAccountKey := types.NamespacedName{Name: "testing-nim-pull-handler-8", Namespace: "testing-nim-pull-handler-8"}

			By("Create testing Namespace " + tstAccountKey.Namespace)
			testNs := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: tstAccountKey.Namespace}}
			Expect(testClient.Create(ctx, testNs)).To(Succeed())

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
			Expect(testClient.Create(ctx, acct)).To(Succeed())

			By("Reconciling")
			// _, err := handler.Reconcile(ctx, acct)

			By("Verify unsuccessful result")
			// Expect(err).To(HaveOccurred())

			By("Verify the Account failure status and check timestamp")
			uAccount := &v1.Account{}
			Expect(testClient.Get(ctx, tstAccountKey, uAccount)).To(Succeed())
			Expect(meta.IsStatusConditionPresentAndEqual(uAccount.Status.Conditions, utils.NimConditionSecretUpdate.String(), metav1.ConditionFalse)).To(BeTrue())
			Expect(meta.IsStatusConditionPresentAndEqual(uAccount.Status.Conditions, utils.NimConditionAccountStatus.String(), metav1.ConditionFalse)).To(BeTrue())
			Expect(uAccount.Status.LastAccountCheck).NotTo(BeNil())

			By("Cleanups")
			Expect(testClient.Delete(ctx, uAccount)).To(Succeed())
			Expect(testClient.Delete(ctx, testNs)).To(Succeed())
		})
	})
})
