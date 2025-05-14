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
	"context"
	"errors"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	templatev1 "github.com/openshift/api/template/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/reference"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"

	v1 "github.com/opendatahub-io/odh-model-controller/api/nim/v1"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/testdata"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
)

var _ = Describe("NIM Account Controller Test Cases", func() {

	// mock nvidia nim api client
	utils.NimHttpClient = &testdata.NimHttpClientMock{}

	It("Should reconcile resources for an Account with a valid API key", func() {
		ctx := context.TODO()
		nameNs := "testing-nim-account-1"

		By("Create testing Namespace " + nameNs)
		testNs := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: nameNs}}
		Expect(k8sClient.Create(ctx, testNs)).To(Succeed())

		By("Create an Account and an API Key Secret")
		acctSubject := types.NamespacedName{Name: nameNs, Namespace: nameNs}
		createApiKeySecretAndAccount(acctSubject, testdata.FakeApiKey)

		By("Verify successful Account")
		account := &v1.Account{}
		assertSuccessfulAccount(acctSubject, account).Should(Succeed())

		By("Verify resources created")
		expectedOwner := createOwnerReference(k8sClient.Scheme(), account)

		dataCmap := &corev1.ConfigMap{}
		dataCmapSubject := namespacedNameFromReference(account.Status.NIMConfig)
		Expect(k8sClient.Get(ctx, dataCmapSubject, dataCmap)).To(Succeed())
		Expect(dataCmapSubject.Name).To(HavePrefix(account.Name + "-"))
		Expect(dataCmap.OwnerReferences[0]).To(Equal(expectedOwner))

		runtimeTemplate := &templatev1.Template{}
		runtimeTemplateSubject := namespacedNameFromReference(account.Status.RuntimeTemplate)
		Expect(k8sClient.Get(ctx, runtimeTemplateSubject, runtimeTemplate)).To(Succeed())
		Expect(runtimeTemplateSubject.Name).To(HavePrefix(account.Name + "-"))
		Expect(runtimeTemplate.OwnerReferences[0]).To(Equal(expectedOwner))

		pullSecret := &corev1.Secret{}
		pullSecretSubject := namespacedNameFromReference(account.Status.NIMPullSecret)
		Expect(k8sClient.Get(ctx, pullSecretSubject, pullSecret)).To(Succeed())
		Expect(pullSecretSubject.Name).To(HavePrefix(account.Name + "-"))
		Expect(pullSecret.OwnerReferences[0]).To(Equal(expectedOwner))

		By("Verify only two models (the nemotron model fetch is not stubbed)")
		Expect(dataCmap.Data).To(HaveLen(2))

		By("Cleanups")
		apiKeySecret := &corev1.Secret{}
		apiKeySubject := namespacedNameFromReference(&account.Spec.APIKeySecret)
		Expect(k8sClient.Get(ctx, apiKeySubject, apiKeySecret)).Should(Succeed())

		Expect(k8sClient.Delete(ctx, account)).To(Succeed())
		Expect(k8sClient.Delete(ctx, apiKeySecret)).To(Succeed())

		// we delete the following because K8S GC is not working in envtest
		Expect(k8sClient.Delete(ctx, dataCmap)).To(Succeed())
		Expect(k8sClient.Delete(ctx, runtimeTemplate)).To(Succeed())
		Expect(k8sClient.Delete(ctx, pullSecret)).To(Succeed())

		Expect(k8sClient.Delete(ctx, testNs)).To(Succeed())
	})

	It("Should not reconcile resources for an account with an invalid API key", func() {
		nameNs := "testing-nim-account-2"

		By("Create testing Namespace " + nameNs)
		testNs := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: nameNs}}
		Expect(k8sClient.Create(ctx, testNs)).To(Succeed())

		By("Create an Account and a wrong API Key Secret")
		acctSubject := types.NamespacedName{Name: nameNs, Namespace: nameNs}
		createApiKeySecretAndAccount(acctSubject, "not-a-valid-key-should-fail")

		By("Verify failed Account")
		account := &v1.Account{}
		assertFailedAccount(acctSubject, account).Should(Succeed())

		By("Cleanups")
		apiKeySecret := &corev1.Secret{}
		apiKeySubject := namespacedNameFromReference(&account.Spec.APIKeySecret)
		Expect(k8sClient.Get(ctx, apiKeySubject, apiKeySecret)).Should(Succeed())

		Expect(k8sClient.Delete(ctx, apiKeySecret)).Should(Succeed())
		Expect(k8sClient.Delete(ctx, account)).Should(Succeed())
		Expect(k8sClient.Delete(ctx, testNs)).To(Succeed())
	})

	It("Should remove all resources if the API key Secret was deleted", func() {
		ctx := context.TODO()
		nameNs := "testing-nim-account-3"

		By("Create testing Namespace " + nameNs)
		testNs := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: nameNs}}
		Expect(k8sClient.Create(ctx, testNs)).To(Succeed())

		By("Create an Account and an API Key Secret")
		acctSubject := types.NamespacedName{Name: nameNs, Namespace: nameNs}
		createApiKeySecretAndAccount(acctSubject, testdata.FakeApiKey)

		By("Verify successful Account")
		account := &v1.Account{}
		assertSuccessfulAccount(acctSubject, account).Should(Succeed())

		By("Verify resources created")
		dataCmapSubject := namespacedNameFromReference(account.Status.NIMConfig)
		Expect(k8sClient.Get(ctx, dataCmapSubject, &corev1.ConfigMap{})).To(Succeed())

		runtimeTemplateSubject := namespacedNameFromReference(account.Status.RuntimeTemplate)
		Expect(k8sClient.Get(ctx, runtimeTemplateSubject, &templatev1.Template{})).To(Succeed())

		pullSecretSubject := namespacedNameFromReference(account.Status.NIMPullSecret)
		Expect(k8sClient.Get(ctx, pullSecretSubject, &corev1.Secret{})).To(Succeed())

		By("Delete API key Secret")
		apiKeySecret := &corev1.Secret{}
		apiKeySubject := namespacedNameFromReference(&account.Spec.APIKeySecret)
		Expect(k8sClient.Get(ctx, apiKeySubject, apiKeySecret)).Should(Succeed())
		Expect(k8sClient.Delete(ctx, apiKeySecret)).To(Succeed())

		By("Verify resources deleted")
		Eventually(func() error {
			if err := k8sClient.Get(ctx, dataCmapSubject, &corev1.ConfigMap{}); !k8serrors.IsNotFound(err) {
				return fmt.Errorf("expected configmap to be deleted")
			}
			if err := k8sClient.Get(ctx, runtimeTemplateSubject, &templatev1.Template{}); !k8serrors.IsNotFound(err) {
				return fmt.Errorf("expected template to be deleted")
			}
			if err := k8sClient.Get(ctx, pullSecretSubject, &corev1.Secret{}); !k8serrors.IsNotFound(err) {
				return fmt.Errorf("expected pull secret to be deleted")
			}
			return nil
		}, testTimeout, testInterval).Should(Succeed())

		By("Cleanups")
		Expect(k8sClient.Delete(ctx, account)).To(Succeed())
		Expect(k8sClient.Delete(ctx, testNs)).To(Succeed())
	})

	It("Should restrict the models in the ConfigMap", func() {
		ctx := context.TODO()
		nameNs := "testing-nim-account-4"

		By("Create testing Namespace " + nameNs)
		testNs := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: nameNs}}
		Expect(k8sClient.Create(ctx, testNs)).To(Succeed())

		By("Create an Account and an API Key Secret")
		acctSubject := types.NamespacedName{Name: nameNs, Namespace: nameNs}
		createApiKeySecretAndAccount(acctSubject, testdata.FakeApiKey)

		By("Verify successful Account")
		account := &v1.Account{}
		assertSuccessfulAccount(acctSubject, account).Should(Succeed())

		By("Verify only two models are in the ConfigMap")
		dataCmap := &corev1.ConfigMap{}
		dataCmapSubject := namespacedNameFromReference(account.Status.NIMConfig)
		Expect(k8sClient.Get(ctx, dataCmapSubject, dataCmap)).To(Succeed())
		Expect(dataCmap.Data).To(HaveLen(2))

		By("Set a model list ConfigMap")
		cmSubject := types.NamespacedName{Name: "model-selection", Namespace: nameNs}
		setModelListConfig(acctSubject, cmSubject)

		By("Verify only one model is in the ConfigMap")
		Eventually(func() error {
			if err := k8sClient.Get(ctx, dataCmapSubject, dataCmap); err != nil {
				return err
			}
			if len(dataCmap.Data) != 1 {
				return fmt.Errorf("expected there is only one model in the ConfigMap, got %d", len(dataCmap.Data))
			}
			return nil
		}, testTimeout, testInterval).Should(Succeed())

		By("Cleanups")
		modelSelectionConfig := &corev1.ConfigMap{}
		Expect(k8sClient.Get(ctx, cmSubject, modelSelectionConfig)).Should(Succeed())

		apiKeySecret := &corev1.Secret{}
		apiKeySubject := namespacedNameFromReference(&account.Spec.APIKeySecret)
		Expect(k8sClient.Get(ctx, apiKeySubject, apiKeySecret)).Should(Succeed())

		Expect(k8sClient.Delete(ctx, modelSelectionConfig)).To(Succeed())
		Expect(k8sClient.Delete(ctx, account)).To(Succeed())
		Expect(k8sClient.Delete(ctx, apiKeySecret)).To(Succeed())
		Expect(k8sClient.Delete(ctx, testNs)).To(Succeed())
	})
})

func createApiKeySecretAndAccount(account types.NamespacedName, apiKey string) {
	apiKeySecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      account.Name + "-api-key",
			Namespace: account.Namespace,
			Labels:    map[string]string{"opendatahub.io/managed": "true"},
		},
		Data: map[string][]byte{
			"api_key": []byte(apiKey),
		},
	}
	Expect(k8sClient.Create(ctx, apiKeySecret)).To(Succeed())

	apiKeyRef, _ := reference.GetReference(k8sClient.Scheme(), apiKeySecret)
	acct := &v1.Account{
		ObjectMeta: metav1.ObjectMeta{
			Name:      account.Name,
			Namespace: account.Namespace,
		},
		Spec: v1.AccountSpec{
			APIKeySecret: *apiKeyRef,
		},
	}
	Expect(k8sClient.Create(ctx, acct)).To(Succeed())
}

func assertSuccessfulAccount(acctSubject types.NamespacedName, account *v1.Account) AsyncAssertion {
	return Eventually(func() error {
		if err := k8sClient.Get(ctx, acctSubject, account); err != nil {
			return err
		}

		for _, cond := range []utils.NimConditionType{
			utils.NimConditionAccountStatus,
			utils.NimConditionAPIKeyValidation,
			utils.NimConditionConfigMapUpdate,
			utils.NimConditionTemplateUpdate,
			utils.NimConditionSecretUpdate,
		} {
			current := cond.String()
			if !meta.IsStatusConditionTrue(account.Status.Conditions, current) {
				return errors.New("successful account status not updated yet for " + current)
			}
		}
		return nil
	}, testTimeout, testInterval)
}

func assertFailedAccount(acctSubject types.NamespacedName, account *v1.Account) AsyncAssertion {
	return Eventually(func() error {
		if err := k8sClient.Get(ctx, acctSubject, account); err != nil {
			return err
		}

		for _, cond := range []utils.NimConditionType{
			utils.NimConditionAccountStatus,
			utils.NimConditionAPIKeyValidation,
		} {
			current := cond.String()
			if !meta.IsStatusConditionFalse(account.Status.Conditions, current) {
				return errors.New("failed account status not updated yet for " + current)
			}
		}

		for _, cond := range []utils.NimConditionType{
			utils.NimConditionConfigMapUpdate,
			utils.NimConditionTemplateUpdate,
			utils.NimConditionSecretUpdate,
		} {
			current := cond.String()
			if !meta.IsStatusConditionPresentAndEqual(account.Status.Conditions, current, metav1.ConditionUnknown) {
				return errors.New("unknown account status not updated yet for " + current)
			}
		}

		for _, ref := range []*corev1.ObjectReference{
			account.Status.NIMPullSecret,
			account.Status.RuntimeTemplate,
			account.Status.NIMConfig,
		} {
			if ref != nil {
				return fmt.Errorf("found referenced object named %s of kind %s", ref.Name, ref.Kind)
			}
		}
		return nil
	}, testTimeout, testInterval)
}

func createOwnerReference(scheme *runtime.Scheme, account *v1.Account) metav1.OwnerReference {
	// check func createOwnerReferenceCfg for info about the gvk usage.
	gvk, _ := apiutil.GVKForObject(account, scheme)
	pTrue := true
	return metav1.OwnerReference{
		Kind:               gvk.Kind,
		Name:               account.Name,
		APIVersion:         gvk.GroupVersion().String(),
		UID:                account.GetUID(),
		BlockOwnerDeletion: &pTrue,
		Controller:         &pTrue,
	}
}

func namespacedNameFromReference(ref *corev1.ObjectReference) types.NamespacedName {
	return types.NamespacedName{Name: ref.Name, Namespace: ref.Namespace}
}

func setModelListConfig(accountSubject types.NamespacedName, cmSubject types.NamespacedName) {
	modelSelectionConfig := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cmSubject.Name,
			Namespace: cmSubject.Namespace,
		},
		Data: map[string]string{
			"models": `["phi-3-mini-4k-instruct"]`,
		},
	}
	Eventually(func() error {
		if err := k8sClient.Create(ctx, modelSelectionConfig); err != nil {
			return err
		}
		if err := k8sClient.Get(ctx, cmSubject, &corev1.ConfigMap{}); err != nil {
			return err
		}
		return nil
	}, testTimeout, testInterval).Should(Succeed())

	Eventually(func() error {
		account := &v1.Account{}
		if err := k8sClient.Get(ctx, accountSubject, account); err != nil {
			return err
		}
		account.Spec.ModelListConfig = &corev1.ObjectReference{
			Name:      cmSubject.Name,
			Namespace: cmSubject.Namespace,
		}
		if err := k8sClient.Update(ctx, account); err != nil {
			return err
		}
		return nil
	}, testTimeout, testInterval).Should(Succeed())
}
