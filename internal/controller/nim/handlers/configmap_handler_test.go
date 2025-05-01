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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/reference"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "github.com/opendatahub-io/odh-model-controller/api/nim/v1"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/testdata"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
)

var _ = Describe("NIM ConfigMap Handler", func() {
	var cmHandler *ConfigMapHandler

	BeforeEach(func() {
		cmHandler = &ConfigMapHandler{
			Client:     testClient,
			Scheme:     testClient.Scheme(),
			KubeClient: k8sClient,
			KeyManager: &APIKeyManager{Client: testClient},
		}
	})

	It("should not reconcile the configmap if the validation failed", func(ctx SpecContext) {
		tstName := "testing-nim-configmap-handler-1"
		tstAccountKey := types.NamespacedName{Name: tstName, Namespace: tstName}

		By("Create testing Namespace " + tstAccountKey.Namespace)
		testNs := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: tstAccountKey.Namespace}}
		Expect(testClient.Create(ctx, testNs)).To(Succeed())

		By("Create an Account")
		acct := &v1.Account{
			ObjectMeta: metav1.ObjectMeta{
				Name:      tstAccountKey.Name,
				Namespace: tstAccountKey.Namespace,
			},
			Spec: v1.AccountSpec{
				APIKeySecret:          corev1.ObjectReference{Name: "not-required", Namespace: "for-this-test"},
				ValidationRefreshRate: "24h",
				NIMConfigRefreshRate:  "24h",
			},
		}
		Expect(testClient.Create(ctx, acct)).To(Succeed())

		By("Update the Account with a failed validation status")
		acct.Status = v1.AccountStatus{Conditions: []metav1.Condition{
			utils.MakeNimCondition(utils.NimConditionAPIKeyValidation, metav1.ConditionFalse, acct.Generation, "ApiKeyNotValidated", "api key failed validation")}}
		Expect(testClient.Status().Update(ctx, acct)).To(Succeed())

		By("Run the handler")
		resp := cmHandler.Handle(ctx, acct)

		By("Verify the response - expect to continue")
		Expect(resp.Error).ToNot(HaveOccurred())
		Expect(resp.Requeue).To(BeFalse())
		Expect(resp.Continue).To(BeTrue())

		By("Verify Account status and the ConfigMap was not created")
		account := &v1.Account{}
		Expect(testClient.Get(ctx, client.ObjectKeyFromObject(acct), account)).To(Succeed())
		// verify config map not refreshed or created
		Expect(account.Status.LastSuccessfulConfigRefresh).To(BeNil())
		Expect(account.Status.NIMConfig).To(BeNil())

		By("Cleanups")
		Expect(testClient.Delete(ctx, account)).To(Succeed())
		Expect(testClient.Delete(ctx, testNs)).To(Succeed())
	})

	It("should create a configmap if needed and requeue", func(ctx SpecContext) {
		tstName := "testing-nim-configmap-handler-2"
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
				"api_key": []byte(testdata.FakeApiKey),
			},
		}
		Expect(testClient.Create(ctx, apiKeySecret)).To(Succeed())
		apiKeySecretRef, refErr := reference.GetReference(testClient.Scheme(), apiKeySecret)
		Expect(refErr).NotTo(HaveOccurred())

		By("Create an Account without referencing the configmap")
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

		By("Update the Account with a successful validation status")
		acct.Status = v1.AccountStatus{Conditions: []metav1.Condition{
			utils.MakeNimCondition(utils.NimConditionAPIKeyValidation, metav1.ConditionTrue, acct.Generation, "ApiKeyValidated", "api key validated successfully")}}
		Expect(testClient.Status().Update(ctx, acct)).To(Succeed())

		By("Run the handler")
		resp := cmHandler.Handle(ctx, acct)

		By("Verify the response - expect a requeue")
		Expect(resp.Error).ToNot(HaveOccurred())
		Expect(resp.Requeue).To(BeTrue())
		Expect(resp.Continue).To(BeFalse())

		By("Verify Account status")
		account := &v1.Account{}
		Expect(testClient.Get(ctx, client.ObjectKeyFromObject(acct), account)).To(Succeed())
		// verify configmap to be created
		Expect(account.Status.LastSuccessfulConfigRefresh).NotTo(BeNil())
		Expect(account.Status.NIMConfig).NotTo(BeNil())

		By("Verify the ConfigMap")
		cm, cmErr := k8sClient.CoreV1().ConfigMaps(account.Status.NIMConfig.Namespace).
			Get(ctx, account.Status.NIMConfig.Name, metav1.GetOptions{})
		Expect(cmErr).NotTo(HaveOccurred())
		Expect(cm).NotTo(BeNil())

		By("Cleanups")
		Expect(testClient.Delete(ctx, cm)).To(Succeed())
		Expect(testClient.Delete(ctx, account)).To(Succeed())
		Expect(testClient.Delete(ctx, testNs)).To(Succeed())
	})

	It("should report error if failed to apply the configmap, i.e. the api key secret doesn't exist", func(ctx SpecContext) {
		tstName := "testing-nim-configmap-handler-3"
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

		By("Update the Account with a successful validation status")
		acct.Status = v1.AccountStatus{Conditions: []metav1.Condition{
			utils.MakeNimCondition(utils.NimConditionAPIKeyValidation, metav1.ConditionTrue, acct.Generation, "ApiKeyValidated", "api key validated successfully")}}
		Expect(testClient.Status().Update(ctx, acct)).To(Succeed())

		By("Run the handler")
		resp := cmHandler.Handle(ctx, acct)

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
		Expect(meta.IsStatusConditionTrue(account.Status.Conditions, utils.NimConditionConfigMapUpdate.String())).To(BeFalse())
		Expect(meta.IsStatusConditionTrue(account.Status.Conditions, utils.NimConditionAccountStatus.String())).To(BeFalse())
		// time account check for failures
		Expect(account.Status.LastAccountCheck).NotTo(BeNil())

		By("Cleanups")
		Expect(testClient.Delete(ctx, account)).To(Succeed())
		Expect(testClient.Delete(ctx, testNs)).To(Succeed())
	})

	It("should reconcile if the configmap was deleted and requeue", func(ctx SpecContext) {
		tstName := "testing-nim-configmap-handler-4"
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
				"api_key": []byte(testdata.FakeApiKey),
			},
		}
		Expect(testClient.Create(ctx, apiKeySecret)).To(Succeed())
		apiKeySecretRef, refErr := reference.GetReference(testClient.Scheme(), apiKeySecret)
		Expect(refErr).NotTo(HaveOccurred())

		By("Create an Account without referencing the configmap")
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

		By("Update the Account referencing a non existing ConfigMap")
		acct.Status = v1.AccountStatus{Conditions: []metav1.Condition{
			utils.MakeNimCondition(utils.NimConditionAPIKeyValidation, metav1.ConditionTrue, acct.Generation, "ApiKeyValidated", "api key validated successfully")}}
		acct.Status.NIMConfig = &corev1.ObjectReference{Name: "non-existing", Namespace: "configmap"}
		Expect(testClient.Status().Update(ctx, acct)).To(Succeed())

		By("Run the handler")
		resp := cmHandler.Handle(ctx, acct)

		By("Verify the response - expect a requeue")
		Expect(resp.Error).ToNot(HaveOccurred())
		Expect(resp.Requeue).To(BeTrue())
		Expect(resp.Continue).To(BeFalse())

		By("Verify Account status")
		account := &v1.Account{}
		Expect(testClient.Get(ctx, client.ObjectKeyFromObject(acct), account)).To(Succeed())
		// verify configmap to be created
		Expect(account.Status.LastSuccessfulConfigRefresh).NotTo(BeNil())

		By("Verify the ConfigMap")
		cm, cmErr := k8sClient.CoreV1().ConfigMaps(account.Status.NIMConfig.Namespace).
			Get(ctx, account.Status.NIMConfig.Name, metav1.GetOptions{})
		Expect(cmErr).NotTo(HaveOccurred())
		Expect(cm).NotTo(BeNil())

		By("Cleanups")
		Expect(testClient.Delete(ctx, cm)).To(Succeed())
		Expect(testClient.Delete(ctx, account)).To(Succeed())
		Expect(testClient.Delete(ctx, testNs)).To(Succeed())
	})

	It("should respect configmap user custom metadata and not requeue", func(ctx SpecContext) {
		tstName := "testing-nim-configmap-handler-5"
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
				"api_key": []byte(testdata.FakeApiKey),
			},
		}
		Expect(testClient.Create(ctx, apiKeySecret)).To(Succeed())
		apiKeySecretRef, refErr := reference.GetReference(testClient.Scheme(), apiKeySecret)
		Expect(refErr).NotTo(HaveOccurred())

		By("Create an Account without referencing the configmap")
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

		By("Create a ConfigMap")
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:            tstAccountKey.Name + "-cm",
				Namespace:       tstAccountKey.Namespace,
				Labels:          map[string]string{"opendatahub.io/managed": "true", "dummy-label-key": "dummy-label-value"},
				Annotations:     map[string]string{"dummy-ann-key": "dummy-ann-value"},
				OwnerReferences: acct.GetOwnerReferences(),
			},
			Data: map[string]string{"dummy_model": "dummy_model_info"},
		}
		Expect(testClient.Create(ctx, cm)).To(Succeed())
		cmRef, psErr := reference.GetReference(testClient.Scheme(), cm)
		Expect(psErr).NotTo(HaveOccurred())

		By("Update the Account referencing the ConfigMap")
		acct.Status = v1.AccountStatus{Conditions: []metav1.Condition{
			utils.MakeNimCondition(utils.NimConditionAPIKeyValidation, metav1.ConditionTrue, acct.Generation, "ApiKeyValidated", "api key validated successfully"),
			utils.MakeNimCondition(utils.NimConditionConfigMapUpdate, metav1.ConditionTrue, acct.Generation, "ConfigMapUpdated", "nim config reconciled successfully"),
		}}
		acct.Status.NIMConfig = cmRef
		acct.Status.LastSuccessfulValidation = &metav1.Time{Time: time.Now().Add(-time.Minute)}
		acct.Status.LastSuccessfulConfigRefresh = &metav1.Time{Time: time.Now()}
		Expect(testClient.Status().Update(ctx, acct)).To(Succeed())

		By("Run the handler")
		resp := cmHandler.Handle(ctx, acct)

		By("Verify the response - expect to continue")
		Expect(resp.Error).ToNot(HaveOccurred())
		Expect(resp.Requeue).To(BeFalse())
		Expect(resp.Continue).To(BeTrue())

		By("Verify Account status")
		account := &v1.Account{}
		Expect(testClient.Get(ctx, client.ObjectKeyFromObject(acct), account)).To(Succeed())
		// verify configmap was not updated
		Expect(account.Status.LastSuccessfulConfigRefresh.UTC()).To(Equal(acct.Status.LastSuccessfulConfigRefresh.UTC()))

		By("Cleanups")
		Expect(testClient.Delete(ctx, cm)).To(Succeed())
		Expect(testClient.Delete(ctx, account)).To(Succeed())
		Expect(testClient.Delete(ctx, testNs)).To(Succeed())
	})

	It("should reconcile if the api key was recently validated and requeue", func(ctx SpecContext) {
		tstName := "testing-nim-configmap-handler-6"
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
				"api_key": []byte(testdata.FakeApiKey),
			},
		}
		Expect(testClient.Create(ctx, apiKeySecret)).To(Succeed())
		apiKeySecretRef, refErr := reference.GetReference(testClient.Scheme(), apiKeySecret)
		Expect(refErr).NotTo(HaveOccurred())

		By("Create an Account without referencing the configmap")
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

		By("Create a ConfigMap")
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:            tstAccountKey.Name + "-cm",
				Namespace:       tstAccountKey.Namespace,
				Labels:          map[string]string{"opendatahub.io/managed": "true"},
				OwnerReferences: acct.GetOwnerReferences(),
			},
			Data: map[string]string{"dummy_model": "dummy_model_info"},
		}
		Expect(testClient.Create(ctx, cm)).To(Succeed())
		cmRef, psErr := reference.GetReference(testClient.Scheme(), cm)
		Expect(psErr).NotTo(HaveOccurred())

		By("Update the Account referencing the ConfigMap")
		acct.Status = v1.AccountStatus{Conditions: []metav1.Condition{
			utils.MakeNimCondition(utils.NimConditionAPIKeyValidation, metav1.ConditionTrue, acct.Generation, "ApiKeyValidated", "api key validated successfully"),
			utils.MakeNimCondition(utils.NimConditionConfigMapUpdate, metav1.ConditionTrue, acct.Generation, "ConfigMapUpdated", "nim config reconciled successfully"),
		}}
		acct.Status.NIMConfig = cmRef
		acct.Status.LastSuccessfulValidation = &metav1.Time{Time: time.Now()}
		// set config refresh to be older the validation to trigger a reconciliation
		acct.Status.LastSuccessfulConfigRefresh = &metav1.Time{Time: time.Now().Add(-time.Minute)}
		Expect(testClient.Status().Update(ctx, acct)).To(Succeed())

		By("Run the handler")
		resp := cmHandler.Handle(ctx, acct)

		By("Verify the response - expect a requeue")
		Expect(resp.Error).ToNot(HaveOccurred())
		Expect(resp.Requeue).To(BeTrue())
		Expect(resp.Continue).To(BeFalse())

		By("Verify Account status")
		account := &v1.Account{}
		Expect(testClient.Get(ctx, client.ObjectKeyFromObject(acct), account)).To(Succeed())
		// verify configmap was updated
		Expect(account.Status.LastSuccessfulConfigRefresh.Time.After(acct.Status.LastSuccessfulConfigRefresh.Time)).To(BeTrue())

		By("Cleanups")
		Expect(testClient.Delete(ctx, cm)).To(Succeed())
		Expect(testClient.Delete(ctx, account)).To(Succeed())
		Expect(testClient.Delete(ctx, testNs)).To(Succeed())
	})

	It("should reconcile if previous configmap reconciliation failed", func(ctx SpecContext) {
		tstName := "testing-nim-configmap-handler-7"
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
				"api_key": []byte(testdata.FakeApiKey),
			},
		}
		Expect(testClient.Create(ctx, apiKeySecret)).To(Succeed())
		apiKeySecretRef, refErr := reference.GetReference(testClient.Scheme(), apiKeySecret)
		Expect(refErr).NotTo(HaveOccurred())

		By("Create an Account without referencing the configmap")
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

		By("Create a ConfigMap")
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:            tstAccountKey.Name + "-cm",
				Namespace:       tstAccountKey.Namespace,
				Labels:          map[string]string{"opendatahub.io/managed": "true"},
				OwnerReferences: acct.GetOwnerReferences(),
			},
			Data: map[string]string{"dummy_model": "dummy_model_info"},
		}
		Expect(testClient.Create(ctx, cm)).To(Succeed())
		cmRef, psErr := reference.GetReference(testClient.Scheme(), cm)
		Expect(psErr).NotTo(HaveOccurred())

		By("Update the Account referencing the ConfigMap")
		acct.Status = v1.AccountStatus{Conditions: []metav1.Condition{
			utils.MakeNimCondition(utils.NimConditionAPIKeyValidation, metav1.ConditionTrue, acct.Generation, "ApiKeyValidated", "api key validated successfully"),
			utils.MakeNimCondition(utils.NimConditionConfigMapUpdate, metav1.ConditionFalse, acct.Generation, "ConfigMapNotUpdated", "here's the thing, something happen"),
		}}
		acct.Status.NIMConfig = cmRef
		acct.Status.LastSuccessfulValidation = &metav1.Time{Time: time.Now().Add(-time.Minute)}
		acct.Status.LastSuccessfulConfigRefresh = &metav1.Time{Time: time.Now().Add(-time.Second)}
		Expect(testClient.Status().Update(ctx, acct)).To(Succeed())

		By("Run the handler")
		resp := cmHandler.Handle(ctx, acct)

		By("Verify the response - expect a requeue")
		Expect(resp.Error).ToNot(HaveOccurred())
		Expect(resp.Requeue).To(BeTrue())
		Expect(resp.Continue).To(BeFalse())

		By("Verify Account status")
		account := &v1.Account{}
		Expect(testClient.Get(ctx, client.ObjectKeyFromObject(acct), account)).To(Succeed())
		// verify configmap was updated
		Expect(account.Status.LastSuccessfulConfigRefresh.Time.After(acct.Status.LastSuccessfulConfigRefresh.Time)).To(BeTrue())

		By("Cleanups")
		Expect(testClient.Delete(ctx, cm)).To(Succeed())
		Expect(testClient.Delete(ctx, account)).To(Succeed())
		Expect(testClient.Delete(ctx, testNs)).To(Succeed())
	})

	It("should requeue if required pull secret metadata modified, while respecting customization", func(ctx SpecContext) {
		tstName := "testing-nim-configmap-handler-8"
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
				"api_key": []byte(testdata.FakeApiKey),
			},
		}
		Expect(testClient.Create(ctx, apiKeySecret)).To(Succeed())
		apiKeySecretRef, refErr := reference.GetReference(testClient.Scheme(), apiKeySecret)
		Expect(refErr).NotTo(HaveOccurred())

		By("Create an Account without referencing the configmap")
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

		By("Create a ConfigMap")
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      tstAccountKey.Name + "-cm",
				Namespace: tstAccountKey.Namespace,
				// deliberately missing label ("opendatahub.io/managed": "true") - expected to be reconciled
				Labels:          map[string]string{"dummy-label-key": "dummy-label-value"},
				Annotations:     map[string]string{"dummy-ann-key": "dummy-ann-value"},
				OwnerReferences: acct.GetOwnerReferences(),
			},
			Data: map[string]string{"dummy_model": "dummy_model_info"},
		}
		Expect(testClient.Create(ctx, cm)).To(Succeed())
		cmRef, psErr := reference.GetReference(testClient.Scheme(), cm)
		Expect(psErr).NotTo(HaveOccurred())

		By("Update the Account referencing the ConfigMap")
		acct.Status = v1.AccountStatus{Conditions: []metav1.Condition{
			utils.MakeNimCondition(utils.NimConditionAPIKeyValidation, metav1.ConditionTrue, acct.Generation, "ApiKeyValidated", "api key validated successfully"),
			utils.MakeNimCondition(utils.NimConditionConfigMapUpdate, metav1.ConditionTrue, acct.Generation, "ConfigMapUpdated", "nim config reconciled successfully"),
		}}
		acct.Status.NIMConfig = cmRef
		acct.Status.LastSuccessfulValidation = &metav1.Time{Time: time.Now().Add(-time.Minute)}
		acct.Status.LastSuccessfulConfigRefresh = &metav1.Time{Time: time.Now().Add(-time.Second)}
		Expect(testClient.Status().Update(ctx, acct)).To(Succeed())

		By("Run the handler")
		resp := cmHandler.Handle(ctx, acct)

		By("Verify the response - expect a requeue")
		Expect(resp.Error).ToNot(HaveOccurred())
		Expect(resp.Requeue).To(BeTrue())
		Expect(resp.Continue).To(BeFalse())

		By("Verify Account status")
		account := &v1.Account{}
		Expect(testClient.Get(ctx, client.ObjectKeyFromObject(acct), account)).To(Succeed())
		// verify configmap was updated
		Expect(account.Status.LastSuccessfulConfigRefresh.Time.After(acct.Status.LastSuccessfulConfigRefresh.Time)).To(BeTrue())

		By("Verify the custom metadata was not removed and required one was reconciled")
		cmUpdate, psuErr := k8sClient.CoreV1().ConfigMaps(cm.Namespace).Get(ctx, cm.Name, metav1.GetOptions{})
		Expect(psuErr).NotTo(HaveOccurred())
		Expect(cmUpdate.Labels["dummy-label-key"]).To(Equal("dummy-label-value"))
		Expect(cmUpdate.Annotations["dummy-ann-key"]).To(Equal("dummy-ann-value"))
		for k, v := range commonBaseLabels {
			Expect(cmUpdate.Labels[k]).To(Equal(v))
		}

		By("Cleanups")
		Expect(testClient.Delete(ctx, cmUpdate)).To(Succeed())
		Expect(testClient.Delete(ctx, account)).To(Succeed())
		Expect(testClient.Delete(ctx, testNs)).To(Succeed())
	})

	It("should validate the api key if the previous validation was successful and the refresh rate requires", func(ctx SpecContext) {
		tstName := "testing-nim-configmap-handler-9"
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
				"api_key": []byte(testdata.FakeApiKey),
			},
		}
		Expect(testClient.Create(ctx, apiKeySecret)).To(Succeed())
		apiKeySecretRef, refErr := reference.GetReference(testClient.Scheme(), apiKeySecret)
		Expect(refErr).NotTo(HaveOccurred())

		By("Create an Account without referencing the configmap")
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

		By("Create a ConfigMap")
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:            tstAccountKey.Name + "-cm",
				Namespace:       tstAccountKey.Namespace,
				Labels:          map[string]string{"opendatahub.io/managed": "true"},
				OwnerReferences: acct.GetOwnerReferences(),
			},
			Data: map[string]string{"dummy_model": "dummy_model_info"},
		}
		Expect(testClient.Create(ctx, cm)).To(Succeed())
		cmRef, psErr := reference.GetReference(testClient.Scheme(), cm)
		Expect(psErr).NotTo(HaveOccurred())

		By("Update the Account with and old successful configmap status")
		acct.Status = v1.AccountStatus{Conditions: []metav1.Condition{
			utils.MakeNimCondition(utils.NimConditionAPIKeyValidation, metav1.ConditionTrue, acct.Generation, "ApiKeyValidated", "api key validated successfully"),
			utils.MakeNimCondition(utils.NimConditionConfigMapUpdate, metav1.ConditionTrue, acct.Generation, "ConfigMapUpdated", "nim config reconciled successfully"),
		}}
		acct.Status.NIMConfig = cmRef
		acct.Status.LastSuccessfulValidation = &metav1.Time{Time: time.Now().Add(-time.Hour * 26)}
		// 25 h
		acct.Status.LastSuccessfulConfigRefresh = &metav1.Time{Time: time.Now().Add(-time.Hour * 25)}
		Expect(testClient.Status().Update(ctx, acct)).To(Succeed())

		By("Run the handler")
		resp := cmHandler.Handle(ctx, acct)

		By("Verify the response - expect a requeue")
		Expect(resp.Error).ToNot(HaveOccurred())
		Expect(resp.Requeue).To(BeTrue())
		Expect(resp.Continue).To(BeFalse())

		By("Verify Account status")
		account := &v1.Account{}
		Expect(testClient.Get(ctx, client.ObjectKeyFromObject(acct), account)).To(Succeed())
		// verify configmap was updated
		Expect(account.Status.LastSuccessfulConfigRefresh.Time.After(acct.Status.LastSuccessfulConfigRefresh.Time)).To(BeTrue())

		By("Cleanups")
		Expect(testClient.Delete(ctx, cm)).To(Succeed())
		Expect(testClient.Delete(ctx, account)).To(Succeed())
		Expect(testClient.Delete(ctx, testNs)).To(Succeed())
	})

	It("should create a configmap with the data of the selected model if the selected model is specified", func(ctx SpecContext) {
		tstName := "testing-nim-configmap-handler-10"
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
				"api_key": []byte(testdata.FakeApiKey),
			},
		}
		Expect(testClient.Create(ctx, apiKeySecret)).To(Succeed())
		apiKeySecretRef, refErr := reference.GetReference(testClient.Scheme(), apiKeySecret)
		Expect(refErr).NotTo(HaveOccurred())

		By("Set a model list ConfigMap")
		modelSelectionConfig := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      tstAccountKey.Name + "-model-selection",
				Namespace: tstAccountKey.Namespace,
			},
			Data: map[string]string{
				"models": `["phi-3-mini-4k-instruct"]`,
			},
		}
		Expect(testClient.Create(ctx, modelSelectionConfig)).To(Succeed())
		modelSelectionConfigRef, refErr := reference.GetReference(testClient.Scheme(), modelSelectionConfig)
		Expect(refErr).NotTo(HaveOccurred())

		By("Create an Account without referencing the configmap")
		acct := &v1.Account{
			ObjectMeta: metav1.ObjectMeta{
				Name:      tstAccountKey.Name,
				Namespace: tstAccountKey.Namespace,
			},
			Spec: v1.AccountSpec{
				APIKeySecret:          *apiKeySecretRef,
				ModelListConfig:       modelSelectionConfigRef,
				ValidationRefreshRate: "24h",
				NIMConfigRefreshRate:  "24h",
			},
		}
		Expect(testClient.Create(ctx, acct)).To(Succeed())

		By("Update the Account with a successful validation status")
		acct.Status = v1.AccountStatus{Conditions: []metav1.Condition{
			utils.MakeNimCondition(utils.NimConditionAPIKeyValidation, metav1.ConditionTrue, acct.Generation, "ApiKeyValidated", "api key validated successfully")}}
		Expect(testClient.Status().Update(ctx, acct)).To(Succeed())

		By("Run the handler")
		resp := cmHandler.Handle(ctx, acct)

		By("Verify the response - expect a requeue")
		Expect(resp.Error).ToNot(HaveOccurred())
		Expect(resp.Requeue).To(BeTrue())
		Expect(resp.Continue).To(BeFalse())

		By("Verify Account status")
		account := &v1.Account{}
		Expect(testClient.Get(ctx, client.ObjectKeyFromObject(acct), account)).To(Succeed())
		// verify configmap to be created
		Expect(account.Status.LastSuccessfulConfigRefresh).NotTo(BeNil())
		Expect(account.Status.NIMConfig).NotTo(BeNil())

		By("Verify the ConfigMap")
		cm, cmErr := k8sClient.CoreV1().ConfigMaps(account.Status.NIMConfig.Namespace).
			Get(ctx, account.Status.NIMConfig.Name, metav1.GetOptions{})
		Expect(cmErr).NotTo(HaveOccurred())
		Expect(cm).NotTo(BeNil())
		Expect(cm.Data).Should(HaveLen(1))
		_, ok := cm.Data["phi-3-mini-4k-instruct"]
		Expect(ok).Should(BeTrue())

		By("Cleanups")
		Expect(testClient.Delete(ctx, apiKeySecret)).To(Succeed())
		Expect(testClient.Delete(ctx, modelSelectionConfig)).To(Succeed())
		Expect(testClient.Delete(ctx, cm)).To(Succeed())
		Expect(testClient.Delete(ctx, account)).To(Succeed())
		Expect(testClient.Delete(ctx, testNs)).To(Succeed())
	})

	It("should update the configmap if the selected model is updated", func(ctx SpecContext) {
		tstName := "testing-nim-configmap-handler-11"
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
				"api_key": []byte(testdata.FakeApiKey),
			},
		}
		Expect(testClient.Create(ctx, apiKeySecret)).To(Succeed())
		apiKeySecretRef, refErr := reference.GetReference(testClient.Scheme(), apiKeySecret)
		Expect(refErr).NotTo(HaveOccurred())

		By("Create an Account without referencing the configmap")
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

		By("Update the Account with a successful validation status")
		acct.Status = v1.AccountStatus{Conditions: []metav1.Condition{
			utils.MakeNimCondition(utils.NimConditionAPIKeyValidation, metav1.ConditionTrue, acct.Generation, "ApiKeyValidated", "api key validated successfully")}}
		Expect(testClient.Status().Update(ctx, acct)).To(Succeed())

		By("Run the handler")
		resp := cmHandler.Handle(ctx, acct)

		By("Verify the response - expect a requeue")
		Expect(resp.Error).ToNot(HaveOccurred())
		Expect(resp.Requeue).To(BeTrue())
		Expect(resp.Continue).To(BeFalse())

		By("Verify Account status")
		account := &v1.Account{}
		Expect(testClient.Get(ctx, client.ObjectKeyFromObject(acct), account)).To(Succeed())
		// verify configmap to be created
		Expect(account.Status.LastSuccessfulConfigRefresh).NotTo(BeNil())
		Expect(account.Status.NIMConfig).NotTo(BeNil())

		By("Verify the ConfigMap")
		cm, cmErr := k8sClient.CoreV1().ConfigMaps(account.Status.NIMConfig.Namespace).
			Get(ctx, account.Status.NIMConfig.Name, metav1.GetOptions{})
		Expect(cmErr).NotTo(HaveOccurred())
		Expect(cm).NotTo(BeNil())
		Expect(cm.Data).Should(HaveLen(2))

		By("Set a model list ConfigMap")
		modelSelectionConfig := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      tstAccountKey.Name + "-model-selection",
				Namespace: tstAccountKey.Namespace,
			},
			Data: map[string]string{
				"models": `["phi-3-mini-4k-instruct"]`,
			},
		}
		Expect(testClient.Create(ctx, modelSelectionConfig)).To(Succeed())
		modelSelectionConfigRef, refErr := reference.GetReference(testClient.Scheme(), modelSelectionConfig)
		Expect(refErr).NotTo(HaveOccurred())

		By("Update the Account with model selection ConfigMap")
		Eventually(func() error {
			account := &v1.Account{}
			if err := testClient.Get(ctx, client.ObjectKeyFromObject(acct), account); err != nil {
				return err
			}
			account.Spec.ModelListConfig = modelSelectionConfigRef
			if err := testClient.Update(ctx, account); err != nil {
				return err
			}
			return nil
		}, testTimeout, testInterval).Should(Succeed())

		By("Get the updated Account")
		Expect(testClient.Get(ctx, client.ObjectKeyFromObject(acct), account)).To(Succeed())
		account.Status.LastSuccessfulValidation = &metav1.Time{Time: time.Now()}

		By("Run the handler again")
		resp1 := cmHandler.Handle(ctx, account)

		By("Verify the response again - expect a requeue")
		Expect(resp1.Error).ToNot(HaveOccurred())
		Expect(resp1.Requeue).To(BeTrue())
		Expect(resp1.Continue).To(BeFalse())

		By("Verify Account status again")
		account1 := &v1.Account{}
		Expect(testClient.Get(ctx, client.ObjectKeyFromObject(acct), account1)).To(Succeed())
		// verify configmap to be created
		Expect(account1.Status.LastSuccessfulConfigRefresh).NotTo(BeNil())
		Expect(account1.Status.NIMConfig).NotTo(BeNil())

		By("Verify the ConfigMap again")
		cm1, cmErr1 := k8sClient.CoreV1().ConfigMaps(account.Status.NIMConfig.Namespace).
			Get(ctx, account.Status.NIMConfig.Name, metav1.GetOptions{})
		Expect(cmErr1).NotTo(HaveOccurred())
		Expect(cm1).NotTo(BeNil())
		Expect(cm1.Data).Should(HaveLen(1))
		_, ok := cm1.Data["phi-3-mini-4k-instruct"]
		Expect(ok).Should(BeTrue())

		By("Cleanups")
		Expect(testClient.Delete(ctx, apiKeySecret)).To(Succeed())
		Expect(testClient.Delete(ctx, modelSelectionConfig)).To(Succeed())
		Expect(testClient.Delete(ctx, cm1)).To(Succeed())
		Expect(testClient.Delete(ctx, account1)).To(Succeed())
		Expect(testClient.Delete(ctx, testNs)).To(Succeed())
	})
})
