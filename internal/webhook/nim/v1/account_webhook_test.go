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

package v1

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	testutils "github.com/opendatahub-io/odh-model-controller/test/utils"

	nimv1 "github.com/opendatahub-io/odh-model-controller/api/nim/v1"
)

var _ = Describe("NIM Account validator webhook", func() {
	var validator AccountCustomValidator
	var nimNamespace *v1.Namespace

	var secretName = "nim-api-key-secret"
	var accountName = "nim-account"

	createNIMAccount := func() func() {
		return func() {
			account := &nimv1.Account{
				ObjectMeta: metav1.ObjectMeta{
					Name:      accountName,
					Namespace: nimNamespace.Name,
				},
				Spec: nimv1.AccountSpec{
					APIKeySecret: v1.ObjectReference{
						Name:      secretName,
						Namespace: nimNamespace.Name,
					},
					ValidationRefreshRate: "24h",
					NIMConfigRefreshRate:  "24h",
				},
			}
			Expect(k8sClient.Create(ctx, account)).To(Succeed())

			Eventually(func() bool {
				if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(account), account); err != nil {
					return false
				}
				return true
			}, testTimeout, testInterval).Should(BeTrue())
		}
	}

	deleteNIMAccount := func() func() {
		return func() {
			account := &nimv1.Account{
				ObjectMeta: metav1.ObjectMeta{
					Name:      accountName,
					Namespace: nimNamespace.Name,
				},
			}
			Expect(k8sClient.Delete(ctx, account)).To(Succeed())

			Eventually(func() bool {
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(account), account)
				if err != nil && errors.IsNotFound(err) {
					return true
				}
				return false
			}, testTimeout, testInterval).Should(BeTrue())
		}
	}

	BeforeEach(func() {
		nimNamespace = testutils.Namespaces.Create(ctx, k8sClient)
		validator = AccountCustomValidator{k8sClient}
	})

	Context("when there is an existing NIM Account", func() {
		BeforeEach(createNIMAccount())
		AfterEach(deleteNIMAccount())

		It("should not accept creating a NIM Account", func() {
			account := &nimv1.Account{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "new-nim-account",
					Namespace: nimNamespace.Name,
				},
				Spec: nimv1.AccountSpec{
					APIKeySecret: v1.ObjectReference{
						Name:      secretName,
						Namespace: nimNamespace.Name,
					},
				},
			}
			_, err := validator.ValidateCreate(ctx, account)
			Expect(err).Should(HaveOccurred())
			Expect(err).Should(MatchError("rejecting creation of Account new-nim-account in namespace " +
				nimNamespace.Name + " because there is already an Account created in the namespace"))
		})
	})
})
