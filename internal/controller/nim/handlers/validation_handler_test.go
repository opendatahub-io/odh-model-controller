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
	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/testdata"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
)

var _ = Describe("NIM Validation Handler", func() {
	var validationHandler *ValidationHandler

	BeforeEach(func() {
		validationHandler = &ValidationHandler{
			Client:     testClient,
			KeyManager: &APIKeyManager{Client: testClient},
		}
	})

	It("should validate the api key if not validated before", func(ctx SpecContext) {
		tstName := "testing-nim-validation-handler-2"
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

		By("Create an Account referencing the API Key Secret without a validation status")
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
		resp := validationHandler.Handle(ctx, acct)

		By("Verify the response - expect a requeue")
		Expect(resp.Error).ToNot(HaveOccurred())
		Expect(resp.Requeue).To(BeTrue())
		Expect(resp.Continue).To(BeFalse())

		By("Verify Account status update")
		account := &v1.Account{}
		Expect(testClient.Get(ctx, client.ObjectKeyFromObject(acct), account)).To(Succeed())
		// report successful status
		Expect(meta.IsStatusConditionTrue(account.Status.Conditions, utils.NimConditionAPIKeyValidation.String())).To(BeTrue())
		// successful validation timestamp should be set
		Expect(account.Status.LastSuccessfulValidation).NotTo(BeNil())

		By("Cleanups")
		Expect(testClient.Delete(ctx, apiKeySecret)).To(Succeed())
		Expect(testClient.Delete(ctx, account)).To(Succeed())
		Expect(testClient.Delete(ctx, testNs)).To(Succeed())
	})

	It("should validate the api key if the previous validation failed", func(ctx SpecContext) {
		tstName := "testing-nim-validation-handler-3"
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

		By("Create an Account referencing the API Key Secret without a validation status")
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

		By("Update the Account with a failed validation status")
		acct.Status = v1.AccountStatus{Conditions: []metav1.Condition{
			utils.MakeNimCondition(utils.NimConditionAPIKeyValidation, metav1.ConditionFalse, acct.Generation, "ApiKeyNotValidated", "api key failed validation")}}
		Expect(testClient.Status().Update(ctx, acct)).To(Succeed())

		By("Run the handler")
		resp := validationHandler.Handle(ctx, acct)

		By("Verify the response - expect a requeue")
		Expect(resp.Error).ToNot(HaveOccurred())
		Expect(resp.Requeue).To(BeTrue())
		Expect(resp.Continue).To(BeFalse())

		By("Verify Account status update")
		account := &v1.Account{}
		Expect(testClient.Get(ctx, client.ObjectKeyFromObject(acct), account)).To(Succeed())
		// report successful status
		Expect(meta.IsStatusConditionTrue(account.Status.Conditions, utils.NimConditionAPIKeyValidation.String())).To(BeTrue())
		// successful validation timestamp should be set
		Expect(account.Status.LastSuccessfulValidation).NotTo(BeNil())

		By("Cleanups")
		Expect(testClient.Delete(ctx, apiKeySecret)).To(Succeed())
		Expect(testClient.Delete(ctx, account)).To(Succeed())
		Expect(testClient.Delete(ctx, testNs)).To(Succeed())
	})

	It("should not validate the api key if the previous validation was successful", func(ctx SpecContext) {
		tstName := "testing-nim-validation-handler-4"
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
				"api_key": []byte("wrong-api-key-should-fail-our-mock-but-wont-because-we-expect-not-to-revalidate"),
			},
		}
		Expect(testClient.Create(ctx, apiKeySecret)).To(Succeed())
		apiKeySecretRef, refErr := reference.GetReference(testClient.Scheme(), apiKeySecret)
		Expect(refErr).NotTo(HaveOccurred())

		By("Create an Account referencing the API Key Secret without a validation status")
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
		acct.Status.LastSuccessfulValidation = &metav1.Time{Time: time.Now()}
		Expect(testClient.Status().Update(ctx, acct)).To(Succeed())

		By("Run the handler")
		resp := validationHandler.Handle(ctx, acct)

		By("Verify the response - expect to continue")
		Expect(resp.Error).ToNot(HaveOccurred())
		Expect(resp.Requeue).To(BeFalse())
		Expect(resp.Continue).To(BeTrue())

		By("Verify Account status update")
		account := &v1.Account{}
		Expect(testClient.Get(ctx, client.ObjectKeyFromObject(acct), account)).To(Succeed())
		// verify status was not updated
		newCond := meta.FindStatusCondition(account.Status.Conditions, utils.NimConditionAPIKeyValidation.String())
		oldCond := meta.FindStatusCondition(acct.Status.Conditions, utils.NimConditionAPIKeyValidation.String())
		Expect(newCond.LastTransitionTime.UTC()).To(Equal(oldCond.LastTransitionTime.UTC()))
		// verify successful timestamp was not updated
		Expect(account.Status.LastSuccessfulValidation.UTC()).To(Equal(acct.Status.LastSuccessfulValidation.UTC()))

		By("Cleanups")
		Expect(testClient.Delete(ctx, apiKeySecret)).To(Succeed())
		Expect(testClient.Delete(ctx, account)).To(Succeed())
		Expect(testClient.Delete(ctx, testNs)).To(Succeed())
	})

	It("should validate the api key if the previous validation was successful and the refresh rate requires", func(ctx SpecContext) {
		tstName := "testing-nim-validation-handler-5"
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
				"api_key": []byte("using-a-wrong-key-will-fail-our-mock-client-and-will-prove-we-attempt-a-revalidation"),
			},
		}
		Expect(testClient.Create(ctx, apiKeySecret)).To(Succeed())
		apiKeySecretRef, refErr := reference.GetReference(testClient.Scheme(), apiKeySecret)
		Expect(refErr).NotTo(HaveOccurred())

		By("Create an Account referencing the API Key Secret without a validation status")
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

		By("Update the Account with and old successful validation status")
		acct.Status = v1.AccountStatus{Conditions: []metav1.Condition{
			utils.MakeNimCondition(utils.NimConditionAPIKeyValidation, metav1.ConditionTrue, acct.Generation, "ApiKeyValidated", "api key validated successfully")}}
		acct.Status.LastSuccessfulValidation = &metav1.Time{Time: time.Now().Add(-(time.Hour * 25))}
		Expect(testClient.Status().Update(ctx, acct)).To(Succeed())

		By("Run the handler")
		resp := validationHandler.Handle(ctx, acct)

		By("Verify the response - expect to end the reconciliation")
		Expect(resp.Error).ToNot(HaveOccurred())
		Expect(resp.Requeue).To(BeFalse())
		Expect(resp.Continue).To(BeFalse())

		By("Verify Account status update")
		account := &v1.Account{}
		Expect(testClient.Get(ctx, client.ObjectKeyFromObject(acct), account)).To(Succeed())
		// verify unsuccessful operation
		Expect(meta.IsStatusConditionFalse(account.Status.Conditions, utils.NimConditionAPIKeyValidation.String())).To(BeTrue())
		Expect(meta.IsStatusConditionFalse(account.Status.Conditions, utils.NimConditionAccountStatus.String())).To(BeTrue())

		By("Cleanups")
		Expect(testClient.Delete(ctx, apiKeySecret)).To(Succeed())
		Expect(testClient.Delete(ctx, account)).To(Succeed())
		Expect(testClient.Delete(ctx, testNs)).To(Succeed())
	})

	It("should force validation if force annotation is on", func(ctx SpecContext) {
		tstName := "testing-nim-validation-handler-6"
		tstAccountKey := types.NamespacedName{Name: tstName, Namespace: tstName}

		By("Create testing Namespace " + tstAccountKey.Namespace)
		testNs := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: tstAccountKey.Namespace}}
		Expect(testClient.Create(ctx, testNs)).To(Succeed())

		By("Create an API Key Secret")
		apiKeySecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:        tstAccountKey.Name + "-api-key",
				Namespace:   tstAccountKey.Namespace,
				Labels:      map[string]string{"opendatahub.io/managed": "true"},
				Annotations: map[string]string{constants.NimForceValidationAnnotation: "true"},
			},
			Data: map[string][]byte{
				"api_key": []byte("wrong-key-will-fail-validation-and-prove-that-we-revalidate-for-force-annotation"),
			},
		}
		Expect(testClient.Create(ctx, apiKeySecret)).To(Succeed())
		apiKeySecretRef, refErr := reference.GetReference(testClient.Scheme(), apiKeySecret)
		Expect(refErr).NotTo(HaveOccurred())

		By("Create an Account referencing the API Key Secret without a validation status")
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
		acct.Status.LastSuccessfulValidation = &metav1.Time{Time: time.Now()}
		Expect(testClient.Status().Update(ctx, acct)).To(Succeed())

		By("Run the handler")
		resp := validationHandler.Handle(ctx, acct)

		By("Verify the response - expect the reconciliation to end (we don't requeue for failed validation)")
		Expect(resp.Error).ToNot(HaveOccurred())
		Expect(resp.Requeue).To(BeFalse())
		Expect(resp.Continue).To(BeFalse())

		By("Verify Account status update")
		account := &v1.Account{}
		Expect(testClient.Get(ctx, client.ObjectKeyFromObject(acct), account)).To(Succeed())
		// report failed status
		Expect(meta.IsStatusConditionTrue(account.Status.Conditions, utils.NimConditionAPIKeyValidation.String())).To(BeFalse())
		Expect(meta.IsStatusConditionTrue(account.Status.Conditions, utils.NimConditionAccountStatus.String())).To(BeFalse())
		// time account check for failures
		Expect(account.Status.LastAccountCheck).NotTo(BeNil())

		By("Cleanups")
		Expect(testClient.Delete(ctx, apiKeySecret)).To(Succeed())
		Expect(testClient.Delete(ctx, account)).To(Succeed())
		Expect(testClient.Delete(ctx, testNs)).To(Succeed())
	})

	It("should report error if failed to get the api key", func(ctx SpecContext) {
		tstName := "testing-nim-validation-handler-7"
		tstAccountKey := types.NamespacedName{Name: tstName, Namespace: tstName}

		By("Create testing Namespace " + tstAccountKey.Namespace)
		testNs := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: tstAccountKey.Namespace}}
		Expect(testClient.Create(ctx, testNs)).To(Succeed())

		By("Create an API Key Secret with the wrong data")
		apiKeySecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      tstAccountKey.Name + "-api-key",
				Namespace: tstAccountKey.Namespace,
				Labels:    map[string]string{"opendatahub.io/managed": "true"},
			},
			Data: map[string][]byte{
				"not_the_correct_key": []byte("the-value-dont-matter"),
			},
		}
		Expect(testClient.Create(ctx, apiKeySecret)).To(Succeed())
		apiKeySecretRef, refErr := reference.GetReference(testClient.Scheme(), apiKeySecret)
		Expect(refErr).NotTo(HaveOccurred())

		By("Create an Account referencing a the faulty API Key Secret")
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
		resp := validationHandler.Handle(ctx, acct)

		By("Verify the response - expect to continue")
		Expect(resp.Error.Error()).To(ContainSubstring("secret testing-nim-validation-handler-7-api-key has no api_key data"))
		Expect(resp.Requeue).To(BeFalse())
		Expect(resp.Continue).To(BeFalse())

		By("Verify Account status update")
		account := &v1.Account{}
		Expect(testClient.Get(ctx, client.ObjectKeyFromObject(acct), account)).To(Succeed())
		// report failed status
		Expect(meta.IsStatusConditionTrue(account.Status.Conditions, utils.NimConditionAPIKeyValidation.String())).To(BeFalse())
		Expect(meta.IsStatusConditionTrue(account.Status.Conditions, utils.NimConditionAccountStatus.String())).To(BeFalse())
		// time account check for failures
		Expect(account.Status.LastAccountCheck).NotTo(BeNil())

		By("Cleanups")
		Expect(testClient.Delete(ctx, account)).To(Succeed())
		Expect(testClient.Delete(ctx, testNs)).To(Succeed())
	})

	It("should stop reconciliation of api key secret not found", func(ctx SpecContext) {
		tstName := "testing-nim-validation-handler-8"
		tstAccountKey := types.NamespacedName{Name: tstName, Namespace: tstName}

		By("Create testing Namespace " + tstAccountKey.Namespace)
		testNs := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: tstAccountKey.Namespace}}
		Expect(testClient.Create(ctx, testNs)).To(Succeed())

		By("Create an Account referencing a non existing Secret")
		acct := &v1.Account{
			ObjectMeta: metav1.ObjectMeta{
				Name:      tstAccountKey.Name,
				Namespace: tstAccountKey.Namespace,
			},
			Spec: v1.AccountSpec{
				APIKeySecret:          corev1.ObjectReference{Name: "im-not-here-val", Namespace: "this-is-not-happening"},
				ValidationRefreshRate: "24h",
				NIMConfigRefreshRate:  "24h",
			},
		}
		Expect(testClient.Create(ctx, acct)).To(Succeed())

		By("Run the handler")
		resp := validationHandler.Handle(ctx, acct)

		By("Verify the response - expect to continue")
		Expect(resp.Error).NotTo(HaveOccurred())
		Expect(resp.Requeue).To(BeFalse())
		Expect(resp.Continue).To(BeFalse())

		By("Verify Account status update")
		account := &v1.Account{}
		Expect(testClient.Get(ctx, client.ObjectKeyFromObject(acct), account)).To(Succeed())
		// report failed status
		Expect(meta.IsStatusConditionTrue(account.Status.Conditions, utils.NimConditionAPIKeyValidation.String())).To(BeFalse())
		Expect(meta.IsStatusConditionTrue(account.Status.Conditions, utils.NimConditionAccountStatus.String())).To(BeFalse())
		// time account check for failures
		Expect(account.Status.LastAccountCheck).NotTo(BeNil())

		By("Cleanups")
		Expect(testClient.Delete(ctx, account)).To(Succeed())
		Expect(testClient.Delete(ctx, testNs)).To(Succeed())
	})

	It("should stop reconciliation if the api key is not validated against the selected model", func(ctx SpecContext) {
		tstName := "testing-nim-validation-handler-9"
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

		By("Set a model list ConfigMap")
		modelSelectionConfig := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      tstAccountKey.Name + "-model-selection",
				Namespace: tstAccountKey.Namespace,
			},
			Data: map[string]string{
				"models": `["llama-3.1-8b-instruct"]`,
			},
		}
		Expect(testClient.Create(ctx, modelSelectionConfig)).To(Succeed())
		modelSelectionConfigRef, refErr := reference.GetReference(testClient.Scheme(), modelSelectionConfig)
		Expect(refErr).NotTo(HaveOccurred())

		By("Create an Account referencing the API Key Secret and model list ConfigMap without a validation status")
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

		By("Run the handler")
		resp := validationHandler.Handle(ctx, acct)

		By("Verify the response - expect to continue")
		Expect(resp.Error).ToNot(HaveOccurred())
		Expect(resp.Requeue).To(BeFalse())
		Expect(resp.Continue).To(BeFalse())

		By("Verify Account status update")
		account := &v1.Account{}
		Expect(testClient.Get(ctx, client.ObjectKeyFromObject(acct), account)).To(Succeed())
		// report failed status
		Expect(meta.IsStatusConditionTrue(account.Status.Conditions, utils.NimConditionAPIKeyValidation.String())).To(BeFalse())
		Expect(meta.IsStatusConditionTrue(account.Status.Conditions, utils.NimConditionAccountStatus.String())).To(BeFalse())
		// time account check for failures
		Expect(account.Status.LastAccountCheck).NotTo(BeNil())

		By("Cleanups")
		Expect(testClient.Delete(ctx, apiKeySecret)).To(Succeed())
		Expect(testClient.Delete(ctx, modelSelectionConfig)).To(Succeed())
		Expect(testClient.Delete(ctx, account)).To(Succeed())
		Expect(testClient.Delete(ctx, testNs)).To(Succeed())
	})

	It("should validate personal api keys", func(ctx SpecContext) {
		tstName := "testing-nim-validation-handler-10"
		tstAccountKey := types.NamespacedName{Name: tstName, Namespace: tstName}

		By("Create testing Namespace " + tstAccountKey.Namespace)
		testNs := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: tstAccountKey.Namespace}}
		Expect(testClient.Create(ctx, testNs)).To(Succeed())

		By("Create an API Key Secret with a personal api key")
		apiKeySecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      tstAccountKey.Name + "-api-key",
				Namespace: tstAccountKey.Namespace,
				Labels:    map[string]string{"opendatahub.io/managed": "true"},
			},
			Data: map[string][]byte{
				"api_key": []byte(testdata.FakePersonalApiKey),
			},
		}
		Expect(testClient.Create(ctx, apiKeySecret)).To(Succeed())
		apiKeySecretRef, refErr := reference.GetReference(testClient.Scheme(), apiKeySecret)
		Expect(refErr).NotTo(HaveOccurred())

		By("Create an Account referencing the API Key Secret without a validation status")
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
		resp := validationHandler.Handle(ctx, acct)

		By("Verify the response - expect a requeue")
		Expect(resp.Error).ToNot(HaveOccurred())
		Expect(resp.Requeue).To(BeTrue())
		Expect(resp.Continue).To(BeFalse())

		By("Verify Account status update")
		account := &v1.Account{}
		Expect(testClient.Get(ctx, client.ObjectKeyFromObject(acct), account)).To(Succeed())
		// report successful status
		Expect(meta.IsStatusConditionTrue(account.Status.Conditions, utils.NimConditionAPIKeyValidation.String())).To(BeTrue())
		// successful validation timestamp should be set
		Expect(account.Status.LastSuccessfulValidation).NotTo(BeNil())

		By("Cleanups")
		Expect(testClient.Delete(ctx, apiKeySecret)).To(Succeed())
		Expect(testClient.Delete(ctx, account)).To(Succeed())
		Expect(testClient.Delete(ctx, testNs)).To(Succeed())
	})
})
