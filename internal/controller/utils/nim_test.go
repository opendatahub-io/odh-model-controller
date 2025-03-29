package utils

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	v1 "github.com/opendatahub-io/odh-model-controller/api/nim/v1"
	templatev1 "github.com/openshift/api/template/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("NIMCleanupRunner", func() {

	It("should not fail if no accounts exist", func(ctx SpecContext) {
		By("Not creating accounts")
		cleaner := &NIMCleanupRunner{Client: testClient}
		Expect(cleaner.Start(ctx)).To(Succeed())
	})

	It("should cleanup owned resources", func(ctx SpecContext) {
		By("Creating a namespace")
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "dummy-acct-test"}}
		Expect(testClient.Create(ctx, ns)).To(Succeed())

		By("Creating an Account")
		account := &v1.Account{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-dummy-acct-test",
				Namespace: ns.Name,
			},
			Spec: v1.AccountSpec{
				APIKeySecret:          corev1.ObjectReference{Name: "dummy-api-secret-name-acct-test"},
				ValidationRefreshRate: "24h",
				NIMConfigRefreshRate:  "24h",
			},
		}
		Expect(testClient.Create(ctx, account)).To(Succeed())

		By("Creating supporting resources")
		pull := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "fake-pull-secret-acct-test", Namespace: ns.Name}}
		Expect(testClient.Create(ctx, pull)).To(Succeed())

		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "fake-cm-acct-test", Namespace: ns.Name}}
		Expect(testClient.Create(ctx, cm)).To(Succeed())

		template := &templatev1.Template{
			ObjectMeta: metav1.ObjectMeta{Name: "fake-template-acct-test", Namespace: ns.Name},
			Objects:    []runtime.RawExtension{},
		}
		Expect(testClient.Create(ctx, template)).To(Succeed())

		By("Updating the Account status")
		acctStatusUp := &v1.Account{}
		Expect(testClient.Get(ctx, types.NamespacedName{Name: account.Name, Namespace: ns.Name}, acctStatusUp)).To(Succeed())

		acctStatusUp.Status = v1.AccountStatus{
			RuntimeTemplate: &corev1.ObjectReference{Name: template.Name, Namespace: ns.Name},
			NIMConfig:       &corev1.ObjectReference{Name: cm.Name, Namespace: ns.Name},
			NIMPullSecret:   &corev1.ObjectReference{Name: pull.Name, Namespace: ns.Name},
			Conditions: []metav1.Condition{
				MakeNimCondition(NimConditionAccountStatus, metav1.ConditionTrue, 1, "testing", "because-i-said-so"),
				MakeNimCondition(NimConditionTemplateUpdate, metav1.ConditionTrue, 1, "testing", "because-i-said-so"),
				MakeNimCondition(NimConditionSecretUpdate, metav1.ConditionTrue, 1, "testing", "because-i-said-so"),
				MakeNimCondition(NimConditionConfigMapUpdate, metav1.ConditionTrue, 1, "testing", "because-i-said-so"),
				MakeNimCondition(NimConditionAPIKeyValidation, metav1.ConditionTrue, 1, "testing", "because-i-said-so"),
			},
		}
		Expect(testClient.Status().Update(ctx, acctStatusUp)).To(Succeed())

		By("Running the Cleaner")
		cleaner := &NIMCleanupRunner{Client: testClient}
		Expect(cleaner.Start(ctx)).To(Succeed())

		By("Fetching the updated Account")
		updatedAccount := &v1.Account{}
		Expect(testClient.Get(ctx, types.NamespacedName{Name: account.Name, Namespace: ns.Name}, updatedAccount)).To(Succeed())

		By("Verifying supporting resources deletion")
		Expect(testClient.Get(ctx, types.NamespacedName{Name: pull.Name, Namespace: ns.Name}, &corev1.Secret{})).NotTo(Succeed())
		Expect(testClient.Get(ctx, types.NamespacedName{Name: cm.Name, Namespace: ns.Name}, &corev1.ConfigMap{})).NotTo(Succeed())
		Expect(testClient.Get(ctx, types.NamespacedName{Name: template.Name, Namespace: ns.Name}, &templatev1.Template{})).NotTo(Succeed())

		By("Fetching Account status")
		Expect(updatedAccount.Status.RuntimeTemplate).To(BeNil())
		Expect(updatedAccount.Status.NIMConfig).To(BeNil())
		Expect(updatedAccount.Status.NIMPullSecret).To(BeNil())

		for _, cond := range []NimConditionType{NimConditionAccountStatus, NimConditionAPIKeyValidation, NimConditionConfigMapUpdate, NimConditionTemplateUpdate, NimConditionSecretUpdate} {
			Expect(updatedAccount.Status.Conditions).To(ContainElement(MatchFields(IgnoreExtras, Fields{
				"Type":    Equal(cond.String()),
				"Message": Equal("NIM has been disabled"),
				"Status":  Equal(metav1.ConditionUnknown),
				"Reason":  ContainSubstring("NotReconciled"),
			})))
		}
	})
})
