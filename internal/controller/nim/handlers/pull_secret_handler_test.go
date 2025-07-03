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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/testdata"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/reference"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "github.com/opendatahub-io/odh-model-controller/api/nim/v1"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
)

var _ = Describe("NIM Pull Secret Handler", func() {
	var pullSecretHandler *PullSecretHandler

	BeforeEach(func() {
		pullSecretHandler = &PullSecretHandler{
			Client:     testClient,
			Scheme:     scheme.Scheme,
			KubeClient: k8sClient,
			KeyManager: &APIKeyManager{Client: testClient},
		}
	})

	It("should report error if failed to apply the pull secret, i.e. the api key secret doesn't exist", func(ctx SpecContext) {
		tstName := "testing-nim-pull-handler-1"
		tstAccountKey := types.NamespacedName{Name: tstName, Namespace: tstName}

		By("Create testing Namespace " + tstAccountKey.Namespace)
		testNs := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: tstAccountKey.Namespace}}
		Expect(testClient.Create(ctx, testNs)).To(Succeed())

		By("Create an Account referencing a non existing API Key Secret")
		acct := &v1.Account{
			ObjectMeta: metav1.ObjectMeta{
				Name:      tstAccountKey.Name,
				Namespace: tstAccountKey.Namespace,
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

		By("Verify the response - expect an error")
		Expect(resp.Error.Error()).To(ContainSubstring("secrets \"im-not-here\" not found"))
		Expect(resp.Requeue).To(BeFalse())
		Expect(resp.Continue).To(BeFalse())

		By("Verify Account status update")
		account := &v1.Account{}
		Expect(testClient.Get(ctx, client.ObjectKeyFromObject(acct), account)).To(Succeed())
		// pull secret not created
		Expect(account.Status.NIMPullSecret).To(BeNil())
		// report failed status
		Expect(meta.IsStatusConditionTrue(account.Status.Conditions, utils.NimConditionSecretUpdate.String())).To(BeFalse())
		Expect(meta.IsStatusConditionTrue(account.Status.Conditions, utils.NimConditionAccountStatus.String())).To(BeFalse())
		// time account check for failures
		Expect(account.Status.LastAccountCheck).NotTo(BeNil())

		By("Cleanups")
		Expect(testClient.Delete(ctx, acct)).To(Succeed())
		Expect(testClient.Delete(ctx, testNs)).To(Succeed())
	})

	It("should create a pull secret if needed and requeue", func(ctx SpecContext) {
		tstName := "testing-nim-pull-handler-2"
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
		apiKeySecretRef, refErr := reference.GetReference(testClient.Scheme(), apiKeySecret)
		Expect(refErr).NotTo(HaveOccurred())

		By("Create an Account referencing the API Key Secret without a Pull Secret reference")
		acct := &v1.Account{
			ObjectMeta: metav1.ObjectMeta{
				Name:      tstAccountKey.Name,
				Namespace: tstAccountKey.Namespace,
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

		By("Verify the response - expect a requeue")
		Expect(resp.Error).ToNot(HaveOccurred())
		Expect(resp.Requeue).To(BeTrue())
		Expect(resp.Continue).To(BeFalse())

		By("Verify Account status update")
		account := &v1.Account{}
		Expect(testClient.Get(ctx, client.ObjectKeyFromObject(acct), account)).To(Succeed())
		// pull secret created
		Expect(account.Status.NIMPullSecret).NotTo(BeNil())
		// report successful status
		Expect(meta.IsStatusConditionTrue(account.Status.Conditions, utils.NimConditionSecretUpdate.String())).To(BeTrue())
		// account check should not be timed yet
		Expect(account.Status.LastAccountCheck).To(BeNil())

		By("Verify the created Pull Secret")
		pullSecret, psErr := k8sClient.CoreV1().Secrets(account.Status.NIMPullSecret.Namespace).Get(ctx, account.Status.NIMPullSecret.Name, metav1.GetOptions{})
		Expect(psErr).NotTo(HaveOccurred())
		Expect(pullSecret.Labels).To(Equal(commonBaseLabels))
		expectedData, _ := GetPullSecretData(fakeApiKey)
		Expect(pullSecret.Data).To(Equal(expectedData))

		By("Cleanups")
		Expect(testClient.Delete(ctx, pullSecret)).To(Succeed())
		Expect(testClient.Delete(ctx, apiKeySecret)).To(Succeed())
		Expect(testClient.Delete(ctx, account)).To(Succeed())
		Expect(testClient.Delete(ctx, testNs)).To(Succeed())
	})

	It("should requeue if status update required", func(ctx SpecContext) {
		tstName := "testing-nim-pull-handler-3"
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
		apiKeySecretRef, refErr := reference.GetReference(testClient.Scheme(), apiKeySecret)
		Expect(refErr).NotTo(HaveOccurred())

		By("Create an Account")
		acct := &v1.Account{
			ObjectMeta: metav1.ObjectMeta{
				Name:      tstAccountKey.Name,
				Namespace: tstAccountKey.Namespace,
			},
			Spec: v1.AccountSpec{
				APIKeySecret:          *apiKeySecretRef,
				ValidationRefreshRate: "24h",
				NIMConfigRefreshRate:  "24h",
			},
		}
		Expect(testClient.Create(ctx, acct)).To(Succeed())

		By("Create a Pull Secret")
		psData, _ := GetPullSecretData(fakeApiKey)
		pullSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:            tstAccountKey.Name + "-pull",
				Namespace:       tstAccountKey.Namespace,
				Labels:          map[string]string{"opendatahub.io/managed": "true", "dummy-label-key": "dummy-label-value"},
				Annotations:     map[string]string{"dummy-ann-key": "dummy-ann-value"},
				OwnerReferences: acct.GetOwnerReferences(),
			},
			Data: psData,
		}
		Expect(testClient.Create(ctx, pullSecret)).To(Succeed())
		pullSecretRef, psErr := reference.GetReference(testClient.Scheme(), pullSecret)
		Expect(psErr).NotTo(HaveOccurred())

		By("Update the Account status to reference the Pull Secret, set condition to fail")
		acct.Status.NIMPullSecret = pullSecretRef
		acct.Status.Conditions = []metav1.Condition{
			utils.MakeNimCondition(utils.NimConditionSecretUpdate, metav1.ConditionFalse, acct.Generation, "SecretFailed", "another made up reason"),
		}
		Expect(testClient.Status().Update(ctx, acct)).To(Succeed())

		By("Run the handler")
		resp := pullSecretHandler.Handle(ctx, acct)

		By("Verify the response - expect a requeue")
		Expect(resp.Error).ToNot(HaveOccurred())
		Expect(resp.Requeue).To(BeTrue())
		Expect(resp.Continue).To(BeFalse())

		By("Verify Account status update")
		account := &v1.Account{}
		Expect(testClient.Get(ctx, client.ObjectKeyFromObject(acct), account)).To(Succeed())
		// verify successful status
		Expect(meta.IsStatusConditionTrue(account.Status.Conditions, utils.NimConditionSecretUpdate.String())).To(BeTrue())

		By("Cleanups")
		Expect(testClient.Delete(ctx, pullSecret)).To(Succeed())
		Expect(testClient.Delete(ctx, apiKeySecret)).To(Succeed())
		Expect(testClient.Delete(ctx, acct)).To(Succeed())
		Expect(testClient.Delete(ctx, testNs)).To(Succeed())
	})

	It("should respect pull secret user custom metadata and not requeue", func(ctx SpecContext) {
		tstName := "testing-nim-pull-handler-4"
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
		apiKeySecretRef, refErr := reference.GetReference(testClient.Scheme(), apiKeySecret)
		Expect(refErr).NotTo(HaveOccurred())

		By("Create an Account referencing the API Key Secret")
		acct := &v1.Account{
			ObjectMeta: metav1.ObjectMeta{
				Name:      tstAccountKey.Name,
				Namespace: tstAccountKey.Namespace,
			},
			Spec: v1.AccountSpec{
				APIKeySecret:          *apiKeySecretRef,
				ValidationRefreshRate: "24h",
				NIMConfigRefreshRate:  "24h",
			},
		}
		Expect(testClient.Create(ctx, acct)).To(Succeed())

		By("Create a Pull Secret")
		psData, _ := GetPullSecretData(fakeApiKey)
		pullSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:            tstAccountKey.Name + "-pull",
				Namespace:       tstAccountKey.Namespace,
				Labels:          map[string]string{"opendatahub.io/managed": "true", "dummy-label-key": "dummy-label-value"},
				Annotations:     map[string]string{"dummy-ann-key": "dummy-ann-value"},
				OwnerReferences: acct.GetOwnerReferences(),
			},
			Data: psData,
		}
		Expect(testClient.Create(ctx, pullSecret)).To(Succeed())
		pullSecretRef, psErr := reference.GetReference(testClient.Scheme(), pullSecret)
		Expect(psErr).NotTo(HaveOccurred())

		By("Update the Account status to reference the Pull Secret")
		acct.Status.NIMPullSecret = pullSecretRef
		acct.Status.Conditions = []metav1.Condition{
			utils.MakeNimCondition(utils.NimConditionSecretUpdate, metav1.ConditionTrue, acct.Generation, "SecretUpdated", "pull secret reconciled successfully"),
		}
		Expect(testClient.Status().Update(ctx, acct)).To(Succeed())

		By("Run the handler")
		resp := pullSecretHandler.Handle(ctx, acct)

		By("Verify the response - expect to continue")
		Expect(resp.Error).ToNot(HaveOccurred())
		Expect(resp.Requeue).To(BeFalse())
		Expect(resp.Continue).To(BeTrue())

		By("Verify the custom metadata was not removed")
		psUpdate, psuErr := k8sClient.CoreV1().Secrets(pullSecretRef.Namespace).Get(ctx, pullSecret.Name, metav1.GetOptions{})
		Expect(psuErr).NotTo(HaveOccurred())
		Expect(psUpdate.Labels["dummy-label-key"]).To(Equal("dummy-label-value"))
		Expect(psUpdate.Annotations["dummy-ann-key"]).To(Equal("dummy-ann-value"))

		By("Cleanups")
		Expect(testClient.Delete(ctx, psUpdate)).To(Succeed())
		Expect(testClient.Delete(ctx, apiKeySecret)).To(Succeed())
		Expect(testClient.Delete(ctx, acct)).To(Succeed())
		Expect(testClient.Delete(ctx, testNs)).To(Succeed())
	})

	It("should requeue if required pull secret metadata modified, while respecting customization", func(ctx SpecContext) {
		tstName := "testing-nim-pull-handler-5"
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
		apiKeySecretRef, refErr := reference.GetReference(testClient.Scheme(), apiKeySecret)
		Expect(refErr).NotTo(HaveOccurred())

		By("Create an Account referencing the API Key Secret")
		acct := &v1.Account{
			ObjectMeta: metav1.ObjectMeta{
				Name:      tstAccountKey.Name,
				Namespace: tstAccountKey.Namespace,
			},
			Spec: v1.AccountSpec{
				APIKeySecret:          *apiKeySecretRef,
				ValidationRefreshRate: "24h",
				NIMConfigRefreshRate:  "24h",
			},
		}
		Expect(testClient.Create(ctx, acct)).To(Succeed())

		By("Create a Pull Secret")
		psData, _ := GetPullSecretData(fakeApiKey)
		pullSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      tstAccountKey.Name + "-pull",
				Namespace: tstAccountKey.Namespace,
				// deliberately missing label ("opendatahub.io/managed": "true") - expected to be reconciled
				Labels:          map[string]string{"dummy-label-key": "dummy-label-value"},
				Annotations:     map[string]string{"dummy-ann-key": "dummy-ann-value"},
				OwnerReferences: acct.GetOwnerReferences(),
			},
			Data: psData,
		}
		Expect(testClient.Create(ctx, pullSecret)).To(Succeed())
		pullSecretRef, psErr := reference.GetReference(testClient.Scheme(), pullSecret)
		Expect(psErr).NotTo(HaveOccurred())

		By("Update the Account status to reference the Pull Secret")
		acct.Status.NIMPullSecret = pullSecretRef
		acct.Status.Conditions = []metav1.Condition{
			utils.MakeNimCondition(utils.NimConditionSecretUpdate, metav1.ConditionTrue, acct.Generation, "SecretUpdated", "pull secret reconciled successfully"),
		}
		Expect(testClient.Status().Update(ctx, acct)).To(Succeed())

		By("Run the handler")
		resp := pullSecretHandler.Handle(ctx, acct)

		By("Verify the response - expect a requeue")
		Expect(resp.Error).ToNot(HaveOccurred())
		Expect(resp.Requeue).To(BeFalse())
		Expect(resp.Continue).To(BeTrue())

		By("Verify the custom metadata was not removed and required one was reconciled")
		psUpdate, psuErr := k8sClient.CoreV1().Secrets(pullSecret.Namespace).Get(ctx, pullSecret.Name, metav1.GetOptions{})
		Expect(psuErr).NotTo(HaveOccurred())
		Expect(psUpdate.Labels["dummy-label-key"]).To(Equal("dummy-label-value"))
		Expect(psUpdate.Annotations["dummy-ann-key"]).To(Equal("dummy-ann-value"))
		for k, v := range commonBaseLabels {
			Expect(psUpdate.Labels[k]).To(Equal(v))
		}

		By("Cleanups")
		Expect(testClient.Delete(ctx, psUpdate)).To(Succeed())
		Expect(testClient.Delete(ctx, apiKeySecret)).To(Succeed())
		Expect(testClient.Delete(ctx, acct)).To(Succeed())
		Expect(testClient.Delete(ctx, testNs)).To(Succeed())
	})

	It("should reconcile if the pull secret was deleted and requeue", func(ctx SpecContext) {
		tstName := "testing-nim-pull-handler-6"
		tstAccountKey := types.NamespacedName{Name: tstName, Namespace: tstName}

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
				"api_key": []byte(testdata.FakeLegacyApiKey),
			},
		}
		Expect(testClient.Create(ctx, apiKeySecret)).To(Succeed())
		apiKeySecretRef, refErr := reference.GetReference(testClient.Scheme(), apiKeySecret)
		Expect(refErr).NotTo(HaveOccurred())

		By("Create an Account")
		acct := &v1.Account{
			ObjectMeta: metav1.ObjectMeta{
				Name:      tstAccountKey.Name,
				Namespace: tstAccountKey.Namespace,
			},
			Spec: v1.AccountSpec{
				APIKeySecret:          *apiKeySecretRef,
				ValidationRefreshRate: "24h",
				NIMConfigRefreshRate:  "24h",
			},
		}
		Expect(testClient.Create(ctx, acct)).To(Succeed())

		By("Update the Account status to reference a non existing pull secret")
		acct.Status.NIMPullSecret = &corev1.ObjectReference{Name: "im-not-here-pull", Namespace: "this-isn't-happening"}
		acct.Status.Conditions = []metav1.Condition{
			utils.MakeNimCondition(utils.NimConditionSecretUpdate, metav1.ConditionTrue, acct.Generation, "SecretUpdated", "pull secret reconciled successfully"),
		}
		Expect(testClient.Status().Update(ctx, acct)).To(Succeed())

		By("Run the handler")
		resp := pullSecretHandler.Handle(ctx, acct)

		By("Verify the response - expect a requeue")
		Expect(resp.Error).ToNot(HaveOccurred())
		Expect(resp.Requeue).To(BeTrue())
		Expect(resp.Continue).To(BeFalse())

		By("Verify Account status update")
		account := &v1.Account{}
		Expect(testClient.Get(ctx, client.ObjectKeyFromObject(acct), account)).To(Succeed())
		// verify reference
		Expect(account.Status.NIMPullSecret).NotTo(BeNil())

		By("Verify the Pull Secret")
		pullSecret, psErr := k8sClient.CoreV1().Secrets(account.Status.NIMPullSecret.Namespace).
			Get(ctx, account.Status.NIMPullSecret.Name, metav1.GetOptions{})
		Expect(psErr).NotTo(HaveOccurred())
		Expect(pullSecret).NotTo(BeNil())

		By("Cleanups")
		Expect(testClient.Delete(ctx, pullSecret)).To(Succeed())
		Expect(testClient.Delete(ctx, apiKeySecret)).To(Succeed())
		Expect(testClient.Delete(ctx, account)).To(Succeed())
		Expect(testClient.Delete(ctx, testNs)).To(Succeed())
	})
})
