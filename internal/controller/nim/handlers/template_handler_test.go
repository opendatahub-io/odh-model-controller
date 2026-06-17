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
	"k8s.io/apimachinery/pkg/api/meta"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "github.com/opendatahub-io/odh-model-controller/api/nim/v1"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
	templatev1 "github.com/openshift/api/template/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/reference"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("NIM Template Handler", func() {
	var templateHandler *TemplateHandler

	BeforeEach(func() {
		templateHandler = &TemplateHandler{
			Client:         testClient,
			Scheme:         scheme.Scheme,
			TemplateClient: templateClient,
		}
	})

	It("should report error if failed to apply the template", func(ctx SpecContext) {
		tstName := "testing-nim-template-handler-1"
		tstAccountKey := types.NamespacedName{Name: tstName, Namespace: tstName}

		templatelessScheme := runtime.NewScheme()

		customTemplateHandler := &TemplateHandler{
			Client:         testClient,
			Scheme:         templatelessScheme,
			TemplateClient: templateClient,
		}

		By("Create testing Namespace " + tstAccountKey.Namespace)
		testNs := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: tstAccountKey.Namespace}}
		Expect(testClient.Create(ctx, testNs)).To(Succeed())

		By("Create an Account without a Template reference")
		acct := &v1.Account{
			ObjectMeta: metav1.ObjectMeta{
				Name:      tstAccountKey.Name,
				Namespace: tstAccountKey.Namespace,
			},
			Spec: v1.AccountSpec{
				APIKeySecret:          corev1.ObjectReference{Name: "im-not-required", Namespace: "for-this-test"},
				ValidationRefreshRate: "24h",
				NIMConfigRefreshRate:  "24h",
			},
		}
		Expect(testClient.Create(ctx, acct)).To(Succeed())

		By("Run the handler")
		resp := customTemplateHandler.Handle(ctx, acct)

		By("Verify the response - expect an error")
		Expect(resp.Error).To(HaveOccurred())
		Expect(resp.Requeue).To(BeFalse())
		Expect(resp.Continue).To(BeFalse())

		By("Verify Account status update")
		account := &v1.Account{}
		Expect(testClient.Get(ctx, client.ObjectKeyFromObject(acct), account)).To(Succeed())
		// template not created
		Expect(account.Status.RuntimeTemplate).To(BeNil())
		// report failed status
		Expect(meta.IsStatusConditionTrue(account.Status.Conditions, utils.NimConditionTemplateUpdate.String())).To(BeFalse())
		Expect(meta.IsStatusConditionTrue(account.Status.Conditions, utils.NimConditionAccountStatus.String())).To(BeFalse())
		// time account check for failures
		Expect(account.Status.LastAccountCheck).NotTo(BeNil())
	})

	It("should create a template if needed and requeue", func(ctx SpecContext) {
		tstName := "testing-nim-template-handler-2"
		tstAccountKey := types.NamespacedName{Name: tstName, Namespace: tstName}

		By("Create testing Namespace " + tstAccountKey.Namespace)
		testNs := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: tstAccountKey.Namespace}}
		Expect(testClient.Create(ctx, testNs)).To(Succeed())

		By("Create an Account without a Template reference")
		acct := &v1.Account{
			ObjectMeta: metav1.ObjectMeta{
				Name:      tstAccountKey.Name,
				Namespace: tstAccountKey.Namespace,
			},
			Spec: v1.AccountSpec{
				APIKeySecret:          corev1.ObjectReference{Name: "im-not-required", Namespace: "for-this-test"},
				ValidationRefreshRate: "24h",
				NIMConfigRefreshRate:  "24h",
			},
		}
		Expect(testClient.Create(ctx, acct)).To(Succeed())

		By("Run the handler")
		resp := templateHandler.Handle(ctx, acct)

		By("Verify the response - expect a requeue")
		Expect(resp.Error).ToNot(HaveOccurred())
		Expect(resp.Requeue).To(BeTrue())
		Expect(resp.Continue).To(BeFalse())

		By("Verify Account status update")
		account := &v1.Account{}
		Expect(testClient.Get(ctx, client.ObjectKeyFromObject(acct), account)).To(Succeed())
		// template created
		Expect(account.Status.RuntimeTemplate).NotTo(BeNil())
		// report successful status
		Expect(meta.IsStatusConditionTrue(account.Status.Conditions, utils.NimConditionTemplateUpdate.String())).To(BeTrue())
		// account check should not be timed yet
		Expect(account.Status.LastAccountCheck).To(BeNil())

		By("Verify the created Template")
		template, tErr := templateClient.TemplateV1().Templates(account.Status.RuntimeTemplate.Namespace).Get(ctx, account.Status.RuntimeTemplate.Name, metav1.GetOptions{})
		Expect(tErr).NotTo(HaveOccurred())
		Expect(template.Labels).To(Equal(commonBaseLabels))
		Expect(template.Annotations).To(Equal(templateBaseAnnotations))
		Expect(template.Objects).To(HaveLen(1))

		// envtest ignores Object/Raw
		// Expect(template.Objects[0].Object).NotTo(BeNil())
		// Expect(string(template.Objects[0].Raw)).NotTo(Equal("{}"))

		By("Cleanups")
		Expect(testClient.Delete(ctx, template)).To(Succeed())
		Expect(testClient.Delete(ctx, account)).To(Succeed())
		Expect(testClient.Delete(ctx, testNs)).To(Succeed())
	})

	It("should requeue if status update required", func(ctx SpecContext) {
		tstName := "testing-nim-template-handler-3"
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
				APIKeySecret:          corev1.ObjectReference{Name: "im-not-required", Namespace: "for-this-test"},
				ValidationRefreshRate: "24h",
				NIMConfigRefreshRate:  "24h",
			},
		}
		Expect(testClient.Create(ctx, acct)).To(Succeed())

		sr, _ := utils.GetNimServingRuntimeTemplate(scheme.Scheme, false)
		By("Create a Template")
		template := &templatev1.Template{
			ObjectMeta: metav1.ObjectMeta{
				Name:        tstAccountKey.Name + "-template",
				Namespace:   tstAccountKey.Namespace,
				Labels:      map[string]string{"opendatahub.io/managed": "true"},
				Annotations: map[string]string{"opendatahub.io/apiProtocol": "REST", "opendatahub.io/modelServingSupport": "[\"single\"]"},
			},
			// envtest ignores Object and Raw of the extension, we use testing.Testing to overcome this
			Objects: []runtime.RawExtension{{Object: sr}},
		}
		Expect(testClient.Create(ctx, template)).To(Succeed())
		templateRef, trErr := reference.GetReference(testClient.Scheme(), template)
		Expect(trErr).NotTo(HaveOccurred())

		By("Update the Account status to reference the Template, set conditions to fail")
		acct.Status.RuntimeTemplate = templateRef
		acct.Status.Conditions = []metav1.Condition{
			utils.MakeNimCondition(utils.NimConditionTemplateUpdate, metav1.ConditionFalse, acct.Generation, "TemplateFailed", "a made up reason"),
		}
		Expect(testClient.Status().Update(ctx, acct)).To(Succeed())

		By("Run the handler")
		resp := templateHandler.Handle(ctx, acct)

		By("Verify the response - expect a requeue")
		Expect(resp.Error).ToNot(HaveOccurred())
		Expect(resp.Requeue).To(BeTrue())
		Expect(resp.Continue).To(BeFalse())

		By("Verify Account status update")
		account := &v1.Account{}
		Expect(testClient.Get(ctx, client.ObjectKeyFromObject(acct), account)).To(Succeed())
		// verify successful status
		Expect(meta.IsStatusConditionTrue(account.Status.Conditions, utils.NimConditionTemplateUpdate.String())).To(BeTrue())

		By("Cleanups")
		Expect(testClient.Delete(ctx, template)).To(Succeed())
		Expect(testClient.Delete(ctx, account)).To(Succeed())
		Expect(testClient.Delete(ctx, testNs)).To(Succeed())
	})

	It("should respect template user custom metadata and not requeue", func(ctx SpecContext) {
		tstName := "testing-nim-template-handler-4"
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
				APIKeySecret:          corev1.ObjectReference{Name: "im-not-required", Namespace: "for-this-test"},
				ValidationRefreshRate: "24h",
				NIMConfigRefreshRate:  "24h",
			},
		}
		Expect(testClient.Create(ctx, acct)).To(Succeed())

		sr, _ := utils.GetNimServingRuntimeTemplate(scheme.Scheme, false)
		By("Create a Template")
		template := &templatev1.Template{
			ObjectMeta: metav1.ObjectMeta{
				Name:      tstAccountKey.Name + "-template",
				Namespace: tstAccountKey.Namespace,
				Labels:    map[string]string{"opendatahub.io/managed": "true", "dummy-label-key": "dummy-label-value"},
				Annotations: map[string]string{
					"opendatahub.io/apiProtocol":         "REST",
					"opendatahub.io/modelServingSupport": "[\"single\"]",
					"dummy-ann-key":                      "dummy-ann-value",
				},
			},
			// envtest ignores Object and Raw of the extensions, we use testing.Testing to overcome this
			Objects: []runtime.RawExtension{{Object: sr}},
		}
		Expect(testClient.Create(ctx, template)).To(Succeed())
		templateRef, trErr := reference.GetReference(testClient.Scheme(), template)
		Expect(trErr).NotTo(HaveOccurred())

		By("Update the Account status to reference the Template")
		acct.Status.RuntimeTemplate = templateRef
		acct.Status.Conditions = []metav1.Condition{
			utils.MakeNimCondition(utils.NimConditionTemplateUpdate, metav1.ConditionTrue, acct.Generation, "TemplateUpdated", "runtime template reconciled successfully"),
		}
		Expect(testClient.Status().Update(ctx, acct)).To(Succeed())

		By("Run the handler")
		resp := templateHandler.Handle(ctx, acct)

		By("Verify the response - expect to continue")
		Expect(resp.Error).ToNot(HaveOccurred())
		Expect(resp.Requeue).To(BeFalse())
		Expect(resp.Continue).To(BeTrue())

		By("Verify the custom metadata was not removed")
		tempUpdate, tuErr := templateClient.TemplateV1().Templates(template.Namespace).Get(ctx, template.Name, metav1.GetOptions{})
		Expect(tuErr).NotTo(HaveOccurred())
		Expect(tempUpdate.Labels["dummy-label-key"]).To(Equal("dummy-label-value"))
		Expect(tempUpdate.Annotations["dummy-ann-key"]).To(Equal("dummy-ann-value"))

		By("Cleanups")
		Expect(testClient.Delete(ctx, tempUpdate)).To(Succeed())
		Expect(testClient.Delete(ctx, acct)).To(Succeed())
		Expect(testClient.Delete(ctx, testNs)).To(Succeed())
	})

	It("should requeue if required template metadata modified, while respecting customization", func(ctx SpecContext) {
		tstName := "testing-nim-template-handler-5"
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
				APIKeySecret:          corev1.ObjectReference{Name: "im-not-required", Namespace: "for-this-test"},
				ValidationRefreshRate: "24h",
				NIMConfigRefreshRate:  "24h",
			},
		}
		Expect(testClient.Create(ctx, acct)).To(Succeed())

		By("Create a Template")
		template := &templatev1.Template{
			ObjectMeta: metav1.ObjectMeta{
				Name:        tstAccountKey.Name + "-template",
				Namespace:   tstAccountKey.Namespace,
				Labels:      map[string]string{"dummy-label-key": "dummy-label-value"},
				Annotations: map[string]string{"dummy-ann-key": "dummy-ann-value"},
			},
			// no point of adding extensions, envtest ignores their content we use testing.Testing to overcome this
			Objects: []runtime.RawExtension{},
		}
		Expect(testClient.Create(ctx, template)).To(Succeed())
		templateRef, trErr := reference.GetReference(testClient.Scheme(), template)
		Expect(trErr).NotTo(HaveOccurred())

		By("Update the Account status to reference the Template")
		acct.Status.RuntimeTemplate = templateRef
		acct.Status.Conditions = []metav1.Condition{
			utils.MakeNimCondition(utils.NimConditionTemplateUpdate, metav1.ConditionTrue, acct.Generation, "TemplateUpdated", "runtime template reconciled successfully"),
		}
		Expect(testClient.Status().Update(ctx, acct)).To(Succeed())

		By("Run the handler")
		resp := templateHandler.Handle(ctx, acct)

		By("Verify the response - expect to continue")
		Expect(resp.Error).ToNot(HaveOccurred())
		Expect(resp.Requeue).To(BeFalse())
		Expect(resp.Continue).To(BeTrue())

		By("Verify the custom metadata was not removed and required one was reconciled")
		tempUpdate, tuErr := templateClient.TemplateV1().Templates(template.Namespace).Get(ctx, template.Name, metav1.GetOptions{})
		Expect(tuErr).NotTo(HaveOccurred())
		Expect(tempUpdate.Labels["dummy-label-key"]).To(Equal("dummy-label-value"))
		Expect(tempUpdate.Annotations["dummy-ann-key"]).To(Equal("dummy-ann-value"))
		for k, v := range commonBaseLabels {
			Expect(tempUpdate.Labels[k]).To(Equal(v))
		}
		for k, v := range templateBaseAnnotations {
			Expect(tempUpdate.Annotations[k]).To(Equal(v))
		}

		By("Cleanups")
		Expect(testClient.Delete(ctx, tempUpdate)).To(Succeed())
		Expect(testClient.Delete(ctx, acct)).To(Succeed())
		Expect(testClient.Delete(ctx, testNs)).To(Succeed())
	})

	It("should reconcile if the template was deleted and requeue", func(ctx SpecContext) {
		tstName := "testing-nim-template-handler-6"
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
				APIKeySecret:          corev1.ObjectReference{Name: "im-not-required", Namespace: "for-this-test"},
				ValidationRefreshRate: "24h",
				NIMConfigRefreshRate:  "24h",
			},
		}
		Expect(testClient.Create(ctx, acct)).To(Succeed())

		By("Update the Account status to reference a non existing template")
		acct.Status.RuntimeTemplate = &corev1.ObjectReference{Name: "im-not-here-template", Namespace: "this-isn't-happening"}
		acct.Status.Conditions = []metav1.Condition{
			utils.MakeNimCondition(utils.NimConditionTemplateUpdate, metav1.ConditionTrue, acct.Generation, "TemplateUpdated", "runtime template reconciled successfully"),
		}
		Expect(testClient.Status().Update(ctx, acct)).To(Succeed())

		By("Run the handler")
		resp := templateHandler.Handle(ctx, acct)

		By("Verify the response - expect a requeue")
		Expect(resp.Error).ToNot(HaveOccurred())
		Expect(resp.Requeue).To(BeTrue())
		Expect(resp.Continue).To(BeFalse())

		By("Verify Account status update")
		account := &v1.Account{}
		Expect(testClient.Get(ctx, client.ObjectKeyFromObject(acct), account)).To(Succeed())
		// verify reference
		Expect(account.Status.RuntimeTemplate).NotTo(BeNil())

		By("Verify the Template")
		template, tempErr := templateClient.TemplateV1().Templates(account.Status.RuntimeTemplate.Namespace).
			Get(ctx, account.Status.RuntimeTemplate.Name, metav1.GetOptions{})
		Expect(tempErr).NotTo(HaveOccurred())
		Expect(template).NotTo(BeNil())

		By("Cleanups")
		Expect(testClient.Delete(ctx, template)).To(Succeed())
		Expect(testClient.Delete(ctx, account)).To(Succeed())
		Expect(testClient.Delete(ctx, testNs)).To(Succeed())
	})
})
