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

	"github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	templatev1 "github.com/openshift/api/template/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/reference"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
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
			Client:         testClient,
			Scheme:         scheme.Scheme,
			KClient:        k8sClient,
			TemplateClient: templateClient,
		}
	})

	It("should add finalizer and re-queue when missing from the Account", func(ctx SpecContext) {
		tstName := "testing-nim-controller-1"
		tstAccountKey := types.NamespacedName{Name: tstName, Namespace: tstName}

		By("Create testing Namespace " + tstAccountKey.Namespace)
		testNs := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: tstAccountKey.Namespace}}
		Expect(testClient.Create(ctx, testNs)).To(Succeed())

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
		Expect(testClient.Create(ctx, acct)).To(Succeed())

		By("Reconciling")
		result, err := accountReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: tstAccountKey})

		By("Verify successful result and re-queueing")
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Requeue).To(BeTrue())

		By("Verify the finalizer was added to the Account")
		uAccount := &v1.Account{}
		Expect(testClient.Get(ctx, tstAccountKey, uAccount)).To(Succeed())
		Expect(uAccount.Finalizers).To(ContainElement(constants.NimCleanupFinalizer))

		By("Cleanups")
		Expect(testClient.Delete(ctx, uAccount)).To(Succeed())
		Expect(testClient.Delete(ctx, testNs)).To(Succeed())
	})

	It("should report healthy Account when no reconciliation is required and not re-queue", func(ctx SpecContext) {
		tstName := "testing-nim-controller-2"
		tstAccountKey := types.NamespacedName{Name: tstName, Namespace: tstName}
		fakeApiKey := tstName

		By("Create testing Namespace " + tstAccountKey.Namespace)
		testNs := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: tstAccountKey.Namespace}}
		Expect(testClient.Create(ctx, testNs)).To(Succeed())

		By("Create an API Key Secret")
		apiKeySecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      tstAccountKey.Name + "-api-key",
				Namespace: tstAccountKey.Namespace,
				Labels:    map[string]string{"opendatahub.io/managed": "true"},
			},
			Data: map[string][]byte{
				"api_key": []byte(fakeApiKey),
			},
		}
		Expect(testClient.Create(ctx, apiKeySecret)).To(Succeed())

		By("Create an Account")
		apiKeyRef, _ := reference.GetReference(testClient.Scheme(), apiKeySecret)
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
		Expect(testClient.Create(ctx, acct)).To(Succeed())

		By("Create the supporting ConfigMap")
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-cm", tstAccountKey.Name),
				Namespace: tstAccountKey.Namespace,
				Labels:    map[string]string{"opendatahub.io/managed": "true"},
			},
			// no data required for this specific test case, we're stubbing the last refresh date to avoid reconciliation
		}
		Expect(testClient.Create(ctx, cm)).To(Succeed())
		cmRef, _ := reference.GetReference(scheme.Scheme, cm)

		By("Create the supporting pull Secret")
		data, _ := handlers.GetPullSecretData(fakeApiKey)
		pullSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-pull", tstAccountKey.Name),
				Namespace: tstAccountKey.Namespace,
				Labels:    map[string]string{"opendatahub.io/managed": "true"},
			},
			// reconciliation is determined based on the secret's data, we use the expected data to avoid reconciliation
			Data: data,
		}
		Expect(testClient.Create(ctx, pullSecret)).To(Succeed())
		pullSecretRef, _ := reference.GetReference(scheme.Scheme, pullSecret)

		By("Create the supporting Template")
		object, _ := utils.GetNimServingRuntimeTemplate(scheme.Scheme, false)
		gvk, _ := apiutil.GVKForObject(object, scheme.Scheme)
		object.SetGroupVersionKind(gvk)

		encoder := serializer.NewCodecFactory(scheme.Scheme).LegacyCodec(v1alpha1.SchemeGroupVersion)
		objByte, _ := runtime.Encode(encoder, object)

		template := &templatev1.Template{
			ObjectMeta: metav1.ObjectMeta{
				Name:        fmt.Sprintf("%s-template", tstAccountKey.Name),
				Namespace:   tstAccountKey.Namespace,
				Labels:      map[string]string{"opendatahub.io/managed": "true"},
				Annotations: map[string]string{"opendatahub.io/apiProtocol": "REST", "opendatahub.io/modelServingSupport": "[\"single\"]"},
			},
			// initially we attempted to use the same Object as expected and avoid reconciliation, but it turns out that
			// envtest is ignoring both Object and Raw, to overcome this, in the template handler, we used testing.Testing()
			Objects: []runtime.RawExtension{{Object: object, Raw: objByte}},
		}
		Expect(testClient.Create(ctx, template)).To(Succeed())
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
				// setting only the account status to false, expecting the controller to change it to true because no reconciliation is required
				utils.MakeNimCondition(utils.NimConditionAccountStatus, metav1.ConditionFalse, 1, "AccountNotSuccessful", "Account Not Successful"),
				// other conditions are set to successful
				utils.MakeNimCondition(utils.NimConditionAPIKeyValidation, metav1.ConditionTrue, 1, "ApiKeyValidated", "api key validated successfully"),
				utils.MakeNimCondition(utils.NimConditionSecretUpdate, metav1.ConditionTrue, 1, "SecretUpdated", "pull secret reconciled successfully"),
				utils.MakeNimCondition(utils.NimConditionTemplateUpdate, metav1.ConditionTrue, 1, "TemplateUpdated", "runtime template reconciled successfully"),
				utils.MakeNimCondition(utils.NimConditionConfigMapUpdate, metav1.ConditionTrue, 1, "ConfigMapUpdated", "nim config reconciled successfully"),
			},
		}
		Expect(testClient.Status().Update(ctx, acct)).To(Succeed())

		By("Reconciling")
		result, err := accountReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: tstAccountKey})

		By("Verify successful result no re-queueing required")
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Requeue).To(BeFalse())

		By("Verify the Account health and status updates")
		uAccount := &v1.Account{}
		Expect(testClient.Get(ctx, tstAccountKey, uAccount)).To(Succeed())
		Expect(uAccount.Status.LastAccountCheck).NotTo(BeNil())
		Expect(meta.IsStatusConditionTrue(uAccount.Status.Conditions, utils.NimConditionAccountStatus.String())).To(BeTrue())

		By("Cleanups")
		Expect(testClient.Delete(ctx, template)).To(Succeed())
		Expect(testClient.Delete(ctx, pullSecret)).To(Succeed())
		Expect(testClient.Delete(ctx, cm)).To(Succeed())
		Expect(testClient.Delete(ctx, uAccount)).To(Succeed())
		Expect(testClient.Delete(ctx, apiKeySecret)).To(Succeed())
		Expect(testClient.Delete(ctx, testNs)).To(Succeed())
	})

	It("should clean up supporting resources when the Account is being deleted", func(ctx SpecContext) {
		tstName := "testing-nim-controller-3"
		tstAccountKey := types.NamespacedName{Name: tstName, Namespace: tstName}
		fakeApiKey := tstName

		By("Create testing Namespace " + tstAccountKey.Namespace)
		testNs := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: tstAccountKey.Namespace}}
		Expect(testClient.Create(ctx, testNs)).To(Succeed())

		By("Create an API Key Secret")
		apiKeySecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      tstAccountKey.Name + "-api-key",
				Namespace: tstAccountKey.Namespace,
				Labels:    map[string]string{"opendatahub.io/managed": "true"},
			},
			Data: map[string][]byte{
				"api_key": []byte(fakeApiKey),
			},
		}
		Expect(testClient.Create(ctx, apiKeySecret)).To(Succeed())

		By("Create an Account")
		apiKeyRef, _ := reference.GetReference(testClient.Scheme(), apiKeySecret)
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
		Expect(testClient.Create(ctx, acct)).To(Succeed())

		By("Create the supporting ConfigMap")
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-cm", tstAccountKey.Name),
				Namespace: tstAccountKey.Namespace,
			},
			// no data required for this specific test case
		}
		Expect(testClient.Create(ctx, cm)).To(Succeed())
		cmRef, _ := reference.GetReference(scheme.Scheme, cm)

		By("Create the supporting pull Secret")
		pullSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-pull", tstAccountKey.Name),
				Namespace: tstAccountKey.Namespace,
			},
			// no data required for this specific test case
		}
		Expect(testClient.Create(ctx, pullSecret)).To(Succeed())
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
		Expect(testClient.Create(ctx, template)).To(Succeed())
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
		Expect(testClient.Status().Update(ctx, acct)).To(Succeed())

		By("Deleting the Account")
		Expect(testClient.Delete(ctx, acct)).To(Succeed())

		By("Reconciling")
		result, err := accountReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: tstAccountKey})

		By("Verify successful result no re-queueing required")
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Requeue).To(BeFalse())

		By("Verify the Account and the supporting resources were removed")
		Eventually(func() error {
			if err := testClient.Get(ctx, client.ObjectKeyFromObject(cm), &corev1.ConfigMap{}); !k8serrors.IsNotFound(err) {
				return fmt.Errorf("expected configmap to be deleted")
			}
			if err := testClient.Get(ctx, client.ObjectKeyFromObject(template), &templatev1.Template{}); !k8serrors.IsNotFound(err) {
				return fmt.Errorf("expected template to be deleted")
			}
			if err := testClient.Get(ctx, client.ObjectKeyFromObject(pullSecret), &corev1.Secret{}); !k8serrors.IsNotFound(err) {
				return fmt.Errorf("expected pull secret to be deleted")
			}
			if err := testClient.Get(ctx, tstAccountKey, &v1.Account{}); !k8serrors.IsNotFound(err) {
				return fmt.Errorf("expected account to be deleted")
			}
			return nil
		}, testTimeout, testInterval).Should(Succeed())

		By("Cleanups")
		Expect(testClient.Delete(ctx, apiKeySecret)).To(Succeed())
		Expect(testClient.Delete(ctx, testNs)).To(Succeed())
	})
})
