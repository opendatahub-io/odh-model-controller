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

package nim

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	templatev1 "github.com/openshift/api/template/v1"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/reference"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1 "github.com/opendatahub-io/odh-model-controller/api/nim/v1"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/nim/handlers"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
)

// NOTE: We only test scenarios of that do not require reconciliation of the supporting resources or status updates here.
// Reconciliation-related scenarios are being tested in the handlers package.
var _ = Describe("NIM Account Controller", func() {
	var accountReconciler *AccountReconciler

	BeforeEach(func() {
		accountReconciler = &AccountReconciler{
			Client:  k8sClient,
			Scheme:  scheme.Scheme,
			KClient: nil, // the clientset is not being used when no reconciliation is required
		}
	})

	It("should add finalizer and re-queue when missing from the Account", func(ctx SpecContext) {
		tstAccountKey := types.NamespacedName{Name: "testing-nim-controller-1", Namespace: "testing-nim-controller-1"}

		By("Create testing Namespace " + tstAccountKey.Namespace)
		testNs := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: tstAccountKey.Namespace}}
		Expect(k8sClient.Create(ctx, testNs)).To(Succeed())

		By("Create an Account")
		acct := &v1.Account{
			// not including a finalizer, expecting the controller to add it and re-queue
			ObjectMeta: metav1.ObjectMeta{
				Name:      tstAccountKey.Name,
				Namespace: tstAccountKey.Namespace,
			},
			Spec: v1.AccountSpec{
				APIKeySecret:          corev1.ObjectReference{Name: "not-required", Namespace: "for-this-test-case"},
				ValidationRefreshRate: "24h",
				NIMConfigRefreshRate:  "24h",
			},
		}
		Expect(k8sClient.Create(ctx, acct)).To(Succeed())

		By("Reconciling")
		result, err := accountReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: tstAccountKey})

		By("Verify successful result and re-queueing")
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Requeue).To(BeTrue())

		By("Verify the finalizer was added to the Account")
		uAccount := &v1.Account{}
		Expect(k8sClient.Get(ctx, tstAccountKey, uAccount)).To(Succeed())
		Expect(uAccount.Finalizers).To(ContainElement(constants.NimCleanupFinalizer))

		By("Cleanups")
		Expect(k8sClient.Delete(ctx, uAccount)).To(Succeed())
		Expect(k8sClient.Delete(ctx, testNs)).To(Succeed())
	})

	It("should report healthy Account when no reconciliation is required and not re-queue", func(ctx SpecContext) {
		tstAccountKey := types.NamespacedName{Name: "testing-nim-controller-2", Namespace: "testing-nim-controller-2"}
		tstApiKey := "dummy-api-key-testing-account-2"

		By("Create testing Namespace " + tstAccountKey.Namespace)
		testNs := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: tstAccountKey.Namespace}}
		Expect(k8sClient.Create(ctx, testNs)).To(Succeed())

		By("Create an API Key Secret")
		apiKeySecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      tstAccountKey.Name + "-api-key",
				Namespace: tstAccountKey.Namespace,
				Labels:    map[string]string{"opendatahub.io/managed": "true"},
			},
			Data: map[string][]byte{
				"api_key": []byte(tstApiKey),
			},
		}
		Expect(k8sClient.Create(ctx, apiKeySecret)).To(Succeed())

		By("Create an Account")
		apiKeyRef, _ := reference.GetReference(k8sClient.Scheme(), apiKeySecret)
		acct := &v1.Account{
			ObjectMeta: metav1.ObjectMeta{
				Name:       tstAccountKey.Name,
				Namespace:  tstAccountKey.Namespace,
				Finalizers: []string{constants.NimCleanupFinalizer},
			},
			Spec: v1.AccountSpec{
				APIKeySecret:          *apiKeyRef,
				ValidationRefreshRate: "24h",
				NIMConfigRefreshRate:  "24h",
			},
		}
		Expect(k8sClient.Create(ctx, acct)).To(Succeed())

		By("Create the supporting ConfigMap")
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-cm", tstAccountKey.Name),
				Namespace: tstAccountKey.Namespace,
			},
			// no data required for this specific test case, we're stubbing the last refresh date to avoid reconciliation
		}
		Expect(k8sClient.Create(ctx, cm)).To(Succeed())
		cmRef, _ := reference.GetReference(scheme.Scheme, cm)

		By("Create the supporting pull Secret")
		data, _ := handlers.GetPullSecretData(tstApiKey)
		pullSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-pull", tstAccountKey.Name),
				Namespace: tstAccountKey.Namespace,
			},
			// reconciliation is determined based on the secret's data, we use the expected data to avoid reconciliation
			Data: data,
		}
		Expect(k8sClient.Create(ctx, pullSecret)).To(Succeed())
		pullSecretRef, _ := reference.GetReference(scheme.Scheme, pullSecret)

		By("Create the supporting Template")
		object, _ := utils.GetNimServingRuntimeTemplate(scheme.Scheme)
		template := &templatev1.Template{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-template", tstAccountKey.Name),
				Namespace: tstAccountKey.Namespace,
			},
			// reconciliation is determined based on the template's serving runtime object, we use the same object to avoid reconciliation
			Objects: []runtime.RawExtension{{Object: object}},
		}
		Expect(k8sClient.Create(ctx, template)).To(Succeed())
		templateRef, _ := reference.GetReference(scheme.Scheme, template)

		By("Update Account with successful status")
		acct.Status = v1.AccountStatus{
			RuntimeTemplate:             templateRef,
			NIMConfig:                   cmRef,
			NIMPullSecret:               pullSecretRef,
			LastSuccessfulValidation:    &metav1.Time{Time: time.Now()},
			LastSuccessfulConfigRefresh: &metav1.Time{Time: time.Now()},
			LastAccountCheck:            nil, // this should get updated when the reconciliation ends
			Conditions: []metav1.Condition{
				// setting only the account status to false, expecting the controller to change it to true because not reconciliation is required
				utils.MakeNimCondition(utils.NimConditionAccountStatus, metav1.ConditionFalse, 1, "AccountNotSuccessful", "Account Not Successful"),
				utils.MakeNimCondition(utils.NimConditionAPIKeyValidation, metav1.ConditionTrue, 1, "ValidationSuccessful", "Validation Successful"),
				utils.MakeNimCondition(utils.NimConditionSecretUpdate, metav1.ConditionTrue, 1, "PullSecretSuccessful", "Pull Secret Successful"),
				utils.MakeNimCondition(utils.NimConditionTemplateUpdate, metav1.ConditionTrue, 1, "TemplateSuccessful", "Template Successful"),
				utils.MakeNimCondition(utils.NimConditionConfigMapUpdate, metav1.ConditionTrue, 1, "ConfigMapSuccessful", "ConfigMap Successful"),
			},
		}
		Expect(k8sClient.Status().Update(ctx, acct)).To(Succeed())

		By("Reconciling")
		result, err := accountReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: tstAccountKey})

		By("Verify successful result no re-queueing required")
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Requeue).To(BeFalse())

		By("Verify the Account health and status updates")
		uAccount := &v1.Account{}
		Expect(k8sClient.Get(ctx, tstAccountKey, uAccount)).To(Succeed())
		Expect(uAccount.Status.LastAccountCheck).NotTo(BeNil())
		Expect(meta.IsStatusConditionTrue(uAccount.Status.Conditions, utils.NimConditionAccountStatus.String())).To(BeTrue())

		By("Cleanups")
		Expect(k8sClient.Delete(ctx, template)).To(Succeed())
		Expect(k8sClient.Delete(ctx, pullSecret)).To(Succeed())
		Expect(k8sClient.Delete(ctx, cm)).To(Succeed())
		Expect(k8sClient.Delete(ctx, uAccount)).To(Succeed())
		Expect(k8sClient.Delete(ctx, apiKeySecret)).To(Succeed())
		Expect(k8sClient.Delete(ctx, testNs)).To(Succeed())
	})

	It("should clean up supporting resources when the Account is being deleted", func(ctx SpecContext) {
		tstAccountKey := types.NamespacedName{Name: "testing-nim-controller-3", Namespace: "testing-nim-controller-3"}
		tstApiKey := "dummy-api-key-testing-account-3"

		By("Create testing Namespace " + tstAccountKey.Namespace)
		testNs := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: tstAccountKey.Namespace}}
		Expect(k8sClient.Create(ctx, testNs)).To(Succeed())

		By("Create an API Key Secret")
		apiKeySecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      tstAccountKey.Name + "-api-key",
				Namespace: tstAccountKey.Namespace,
				Labels:    map[string]string{"opendatahub.io/managed": "true"},
			},
			Data: map[string][]byte{
				"api_key": []byte(tstApiKey),
			},
		}
		Expect(k8sClient.Create(ctx, apiKeySecret)).To(Succeed())

		By("Create an Account")
		apiKeyRef, _ := reference.GetReference(k8sClient.Scheme(), apiKeySecret)
		acct := &v1.Account{
			ObjectMeta: metav1.ObjectMeta{
				Name:       tstAccountKey.Name,
				Namespace:  tstAccountKey.Namespace,
				Finalizers: []string{constants.NimCleanupFinalizer},
			},
			Spec: v1.AccountSpec{
				APIKeySecret:          *apiKeyRef,
				ValidationRefreshRate: "24h",
				NIMConfigRefreshRate:  "24h",
			},
		}
		Expect(k8sClient.Create(ctx, acct)).To(Succeed())

		By("Create the supporting ConfigMap")
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-cm", tstAccountKey.Name),
				Namespace: tstAccountKey.Namespace,
			},
			// no data required for this specific test case
		}
		Expect(k8sClient.Create(ctx, cm)).To(Succeed())
		cmRef, _ := reference.GetReference(scheme.Scheme, cm)

		By("Create the supporting pull Secret")
		pullSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-pull", tstAccountKey.Name),
				Namespace: tstAccountKey.Namespace,
			},
			// no data required for this specific test case
		}
		Expect(k8sClient.Create(ctx, pullSecret)).To(Succeed())
		pullSecretRef, _ := reference.GetReference(scheme.Scheme, pullSecret)

		By("Create the supporting Template")
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

		By("Update Account with successful status")
		acct.Status = v1.AccountStatus{
			RuntimeTemplate: templateRef,
			NIMConfig:       cmRef,
			NIMPullSecret:   pullSecretRef,
			// timestamps and conditions are not required for this test case
			LastSuccessfulValidation:    nil,
			LastSuccessfulConfigRefresh: nil,
			LastAccountCheck:            nil,
			Conditions:                  []metav1.Condition{},
		}
		Expect(k8sClient.Status().Update(ctx, acct)).To(Succeed())

		By("Deleting the Account")
		Expect(k8sClient.Delete(ctx, acct)).To(Succeed())

		By("Reconciling")
		result, err := accountReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: tstAccountKey})

		By("Verify successful result no re-queueing required")
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Requeue).To(BeFalse())

		By("Verify the Account and the supporting resources were removed")
		Eventually(func() error {
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cm), &corev1.ConfigMap{}); !k8serrors.IsNotFound(err) {
				return fmt.Errorf("expected configmap to be deleted")
			}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(template), &templatev1.Template{}); !k8serrors.IsNotFound(err) {
				return fmt.Errorf("expected template to be deleted")
			}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(pullSecret), &corev1.Secret{}); !k8serrors.IsNotFound(err) {
				return fmt.Errorf("expected pull secret to be deleted")
			}
			if err := k8sClient.Get(ctx, tstAccountKey, &v1.Account{}); !k8serrors.IsNotFound(err) {
				return fmt.Errorf("expected account to be deleted")
			}
			return nil
		}, testTimeout, testInterval).Should(Succeed())

		By("Cleanups")
		Expect(k8sClient.Delete(ctx, apiKeySecret)).To(Succeed())
		Expect(k8sClient.Delete(ctx, testNs)).To(Succeed())
	})
})
