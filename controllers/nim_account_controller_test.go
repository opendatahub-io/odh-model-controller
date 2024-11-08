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

package controllers

import (
	"context"
	"errors"
	"fmt"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	v1 "github.com/opendatahub-io/odh-model-controller/api/nim/v1"
	"github.com/opendatahub-io/odh-model-controller/controllers/testdata"
	"github.com/opendatahub-io/odh-model-controller/controllers/utils"
	templatev1 "github.com/openshift/api/template/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/reference"
)

var _ = Describe("NIM Account Controller Test Cases", func() {

	// mock nvidia nim api client
	utils.NimHttpClient = &testdata.NimHttpClientMock{}

	It("Should reconcile resources for an Account with a valid API key", func() {
		ctx := context.TODO()

		By("Create an Account and an API Key Secret")
		acctSubject := types.NamespacedName{Name: "testing-account-1", Namespace: WorkingNamespace}
		createApiKeySecretAndAccount(acctSubject, testdata.FakeApiKey)

		By("Verify successful Account")
		account := &v1.Account{}
		assertSuccessfulAccount(acctSubject, account).Should(Succeed())

		By("Verify resources created")
		expectedOwner := createOwnerReference(cli.Scheme(), account)

		dataCmap := &corev1.ConfigMap{}
		dataCmapSubject := namespacedNameFromReference(account.Status.NIMConfig)
		Expect(cli.Get(ctx, dataCmapSubject, dataCmap)).To(Succeed())
		Expect(dataCmap.OwnerReferences[0]).To(Equal(expectedOwner))

		runtimeTemplate := &templatev1.Template{}
		runtimeTemplateSubject := namespacedNameFromReference(account.Status.RuntimeTemplate)
		Expect(cli.Get(ctx, runtimeTemplateSubject, runtimeTemplate)).To(Succeed())
		Expect(runtimeTemplate.OwnerReferences[0]).To(Equal(expectedOwner))

		pullSecret := &corev1.Secret{}
		pullSecretSubject := namespacedNameFromReference(account.Status.NIMPullSecret)
		Expect(cli.Get(ctx, pullSecretSubject, pullSecret)).To(Succeed())
		Expect(pullSecret.OwnerReferences[0]).To(Equal(expectedOwner))

		By("Cleanups")
		apiKeySecret := &corev1.Secret{}
		apiKeySubject := namespacedNameFromReference(&account.Spec.APIKeySecret)
		Expect(cli.Get(ctx, apiKeySubject, apiKeySecret)).Should(Succeed())

		Expect(cli.Delete(ctx, account)).To(Succeed())
		Expect(cli.Delete(ctx, apiKeySecret)).To(Succeed())
		Expect(cli.Delete(ctx, dataCmap)).To(Succeed())
		Expect(cli.Delete(ctx, runtimeTemplate)).To(Succeed())
		Expect(cli.Delete(ctx, pullSecret)).To(Succeed())
	})

	It("Should not reconcile resources for an account with an invalid API key", func() {
		By("Create an Account and a wrong API Key Secret")
		acctSubject := types.NamespacedName{Name: "testing-account-3", Namespace: WorkingNamespace}
		createApiKeySecretAndAccount(acctSubject, "not-a-valid-key-should-fail")

		By("Verify failed Account")
		account := &v1.Account{}
		assertFailedAccount(acctSubject, account).Should(Succeed())

		By("Cleanups")
		apiKeySecret := &corev1.Secret{}
		apiKeySubject := namespacedNameFromReference(&account.Spec.APIKeySecret)
		Expect(cli.Get(ctx, apiKeySubject, apiKeySecret)).Should(Succeed())

		Expect(cli.Delete(ctx, apiKeySecret)).Should(Succeed())
		Expect(cli.Delete(ctx, account)).Should(Succeed())
	})
})

func createApiKeySecretAndAccount(account types.NamespacedName, apiKey string) {
	apiKeySecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      account.Name + "-api-key",
			Namespace: account.Namespace,
		},
		Data: map[string][]byte{
			"api_key": []byte(apiKey),
		},
	}
	Expect(cli.Create(ctx, apiKeySecret)).To(Succeed())

	apiKeyRef, _ := reference.GetReference(cli.Scheme(), apiKeySecret)
	acct := &v1.Account{
		ObjectMeta: metav1.ObjectMeta{
			Name:      account.Name,
			Namespace: account.Namespace,
		},
		Spec: v1.AccountSpec{
			APIKeySecret: *apiKeyRef,
		},
	}
	Expect(cli.Create(ctx, acct)).To(Succeed())
}

func assertSuccessfulAccount(acctSubject types.NamespacedName, account *v1.Account) AsyncAssertion {
	return Eventually(func() error {
		if err := cli.Get(ctx, acctSubject, account); err != nil {
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
	}, timeout, interval)
}

func assertFailedAccount(acctSubject types.NamespacedName, account *v1.Account) AsyncAssertion {
	return Eventually(func() error {
		if err := cli.Get(ctx, acctSubject, account); err != nil {
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
	}, timeout, interval)
}

func createOwnerReference(scheme *runtime.Scheme, account *v1.Account) metav1.OwnerReference {
	gvks, _, _ := scheme.ObjectKinds(account)
	pTrue := true
	return metav1.OwnerReference{
		Kind:               gvks[0].Kind,
		Name:               account.Name,
		APIVersion:         gvks[0].GroupVersion().String(),
		UID:                account.GetUID(),
		BlockOwnerDeletion: &pTrue,
		Controller:         &pTrue,
	}
}

func namespacedNameFromReference(ref *corev1.ObjectReference) types.NamespacedName {
	return types.NamespacedName{Name: ref.Name, Namespace: ref.Namespace}
}