package controllers

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	nimv1 "github.com/opendatahub-io/odh-model-controller/api/nim/v1"
	"github.com/opendatahub-io/odh-model-controller/controllers/webhook"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var _ = Describe("NIM Account validator webhook", func() {
	var validator admission.CustomValidator
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
				},
			}
			Expect(cli.Create(ctx, account)).To(Succeed())

			Eventually(func() bool {
				if err := cli.Get(ctx, client.ObjectKeyFromObject(account), account); err != nil {
					return false
				}
				return true
			}, timeout, interval).Should(BeTrue())
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
			Expect(cli.Delete(ctx, account)).To(Succeed())

			Eventually(func() bool {
				err := cli.Get(ctx, client.ObjectKeyFromObject(account), account)
				if err != nil && errors.IsNotFound(err) {
					return true
				}
				return false
			}, timeout, interval).Should(BeTrue())
		}
	}

	BeforeEach(func() {
		nimNamespace = Namespaces.Create(cli)
		validator = webhook.NewNimAccountValidator(cli)
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
