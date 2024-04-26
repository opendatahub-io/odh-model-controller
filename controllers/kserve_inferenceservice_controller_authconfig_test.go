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
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	authorinov1beta2 "github.com/kuadrant/authorino/api/v1beta2"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/opendatahub-io/odh-model-controller/controllers/constants"
	"github.com/opendatahub-io/odh-model-controller/controllers/utils"
	istiosec_v1b1 "istio.io/client-go/pkg/apis/security/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"knative.dev/pkg/apis"
)

var _ = Describe("InferenceService Authorization", func() {

	var (
		namespace *corev1.Namespace
		isvc      *kservev1beta1.InferenceService
	)

	When("not configured for the cluster", func() {
		BeforeEach(func() {
			ctx := context.Background()
			namespace = &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: Namespaces.Get(),
				},
			}
			Expect(cli.Create(ctx, namespace)).Should(Succeed())
			inferenceServiceConfig := &corev1.ConfigMap{}

			Expect(convertToStructuredResource(InferenceServiceConfigPath1, inferenceServiceConfig)).To(Succeed())
			if err := cli.Create(ctx, inferenceServiceConfig); err != nil && !errors.IsAlreadyExists(err) {
				Fail(err.Error())
			}

			// We need to stub the cluster state and indicate if Authorino is configured as authorization layer
			if dsciErr := createDSCI(DSCIWithoutAuthorization); dsciErr != nil && !errors.IsAlreadyExists(dsciErr) {
				Fail(dsciErr.Error())
			}

			isvc = createISVCWithoutAuth(namespace.Name)
		})

		AfterEach(func() {
			Expect(deleteDSCI(DSCIWithoutAuthorization)).To(Succeed())
		})

		It("should not create auth config", func() {
			Consistently(func() error {
				ac := &authorinov1beta2.AuthConfig{}
				return getAuthConfig(namespace.Name, isvc.Name, ac)
			}).
				WithTimeout(timeout).
				WithPolling(interval).
				Should(Not(Succeed()))
		})
	})

	When("configured for the cluster", func() {

		BeforeEach(func() {
			ctx := context.Background()
			namespace = &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: Namespaces.Get(),
				},
			}
			Expect(cli.Create(ctx, namespace)).Should(Succeed())
			inferenceServiceConfig := &corev1.ConfigMap{}

			Expect(convertToStructuredResource(InferenceServiceConfigPath1, inferenceServiceConfig)).To(Succeed())
			if err := cli.Create(ctx, inferenceServiceConfig); err != nil && !errors.IsAlreadyExists(err) {
				Fail(err.Error())
			}

			//// We need to stub the cluster state and indicate that Authorino is configured as authorization layer
			//if dsciErr := createDSCI(DSCIWithAuthorization); dsciErr != nil && !errors.IsAlreadyExists(dsciErr) {
			//	Fail(dsciErr.Error())
			//}

			// TODO: See utils.VerifyIfMeshAuthorizationIsEnabled func
			if authPolicyErr := createAuthorizationPolicy(KServeAuthorizationPolicy); authPolicyErr != nil && !errors.IsAlreadyExists(authPolicyErr) {
				Fail(authPolicyErr.Error())
			}
		})

		AfterEach(func() {
			//Expect(deleteDSCI(DSCIWithAuthorization)).To(Succeed())
			Expect(deleteAuthorizationPolicy(KServeAuthorizationPolicy)).To(Succeed())
		})

		Context("when InferenceService is not ready", func() {
			BeforeEach(func() {
				isvc = createISVCMissingStatus(namespace.Name)
			})

			It("should not create auth config on missing status.URL", func() {

				Consistently(func() error {
					ac := &authorinov1beta2.AuthConfig{}
					return getAuthConfig(namespace.Name, isvc.Name, ac)
				}).
					WithTimeout(timeout).
					WithPolling(interval).
					Should(Not(Succeed()))
			})
		})

		Context("when InferenceService is ready", func() {

			Context("auth not enabled", func() {
				BeforeEach(func() {
					isvc = createISVCWithoutAuth(namespace.Name)
				})

				It("should create anonymous auth config", func() {
					Expect(updateISVCStatus(isvc)).To(Succeed())

					Eventually(func(g Gomega) {
						ac := &authorinov1beta2.AuthConfig{}
						g.Expect(getAuthConfig(namespace.Name, isvc.Name, ac)).To(Succeed())
						g.Expect(ac.Spec.Authorization["anonymous-access"]).NotTo(BeNil())
					}).
						WithTimeout(timeout).
						WithPolling(interval).
						Should(Succeed())
				})

				It("should update to non anonymous on enable", func() {
					Expect(updateISVCStatus(isvc)).To(Succeed())

					Eventually(func(g Gomega) {
						ac := &authorinov1beta2.AuthConfig{}
						g.Expect(getAuthConfig(namespace.Name, isvc.Name, ac)).To(Succeed())
						g.Expect(ac.Spec.Authorization["anonymous-access"]).NotTo(BeNil())
					}).
						WithTimeout(timeout).
						WithPolling(interval).
						Should(Succeed())

					Expect(enableAuth(isvc)).To(Succeed())
					Eventually(func(g Gomega) {
						ac := &authorinov1beta2.AuthConfig{}
						g.Expect(ac.Spec.Authorization["kubernetes-user"]).NotTo(BeNil())
						g.Expect(getAuthConfig(namespace.Name, isvc.Name, ac)).To(Succeed())
					}).
						WithTimeout(timeout).
						WithPolling(interval).
						Should(Succeed())
				})
			})

			Context("auth enabled", func() {
				BeforeEach(func() {
					isvc = createISVCWithAuth(namespace.Name)
				})

				It("should create user defined auth config", func() {
					Expect(updateISVCStatus(isvc)).To(Succeed())

					Eventually(func(g Gomega) {
						ac := &authorinov1beta2.AuthConfig{}
						g.Expect(getAuthConfig(namespace.Name, isvc.Name, ac)).To(Succeed())
						g.Expect(ac.Spec.Authorization["kubernetes-user"]).NotTo(BeNil())
					}).
						WithTimeout(timeout).
						WithPolling(interval).
						Should(Succeed())
				})

				It("should update to anonymous on disable", func() {
					Expect(updateISVCStatus(isvc)).To(Succeed())

					Eventually(func(g Gomega) {
						ac := &authorinov1beta2.AuthConfig{}
						g.Expect(getAuthConfig(namespace.Name, isvc.Name, ac)).To(Succeed())
						g.Expect(ac.Spec.Authorization["kubernetes-user"]).NotTo(BeNil())
					}).
						WithTimeout(timeout).
						WithPolling(interval).
						Should(Succeed())

					Expect(disableAuth(isvc)).To(Succeed())
					Eventually(func(g Gomega) {
						ac := &authorinov1beta2.AuthConfig{}
						g.Expect(getAuthConfig(namespace.Name, isvc.Name, ac)).To(Succeed())
						g.Expect(ac.Spec.Authorization["anonymous-access"]).NotTo(BeNil())
					}).
						WithTimeout(timeout).
						WithPolling(interval).
						Should(Succeed())
				})
			})
		})
	})

})

func getAuthConfig(namespace, name string, ac *authorinov1beta2.AuthConfig) error {
	return cli.Get(context.Background(), types.NamespacedName{Namespace: namespace, Name: name}, ac)
}

func createISVCMissingStatus(namespace string) *kservev1beta1.InferenceService {
	inferenceService := &kservev1beta1.InferenceService{}
	err := convertToStructuredResource(KserveInferenceServicePath1, inferenceService)
	Expect(err).NotTo(HaveOccurred())
	inferenceService.Namespace = namespace
	Expect(cli.Create(ctx, inferenceService)).Should(Succeed())
	return inferenceService
}

func createISVCWithAuth(namespace string) *kservev1beta1.InferenceService {
	inferenceService := createBasicISVC(namespace)
	inferenceService.Annotations[constants.LabelEnableAuth] = "true"
	Expect(cli.Create(ctx, inferenceService)).Should(Succeed())

	return inferenceService
}

func createISVCWithoutAuth(namespace string) *kservev1beta1.InferenceService {
	inferenceService := createBasicISVC(namespace)
	Expect(cli.Create(ctx, inferenceService)).Should(Succeed())

	return inferenceService
}

func createBasicISVC(namespace string) *kservev1beta1.InferenceService {
	inferenceService := &kservev1beta1.InferenceService{}
	err := convertToStructuredResource(KserveInferenceServicePath1, inferenceService)
	Expect(err).NotTo(HaveOccurred())
	inferenceService.Namespace = namespace
	if inferenceService.Annotations == nil {
		inferenceService.Annotations = map[string]string{}
	}
	return inferenceService
}

func updateISVCStatus(isvc *kservev1beta1.InferenceService) error {
	url, _ := apis.ParseURL("http://iscv-" + isvc.Namespace + "ns.apps.openshift.ai")
	isvc.Status = kservev1beta1.InferenceServiceStatus{
		URL: url,
	}
	return cli.Status().Update(context.Background(), isvc)
}

func disableAuth(isvc *kservev1beta1.InferenceService) error {
	delete(isvc.Annotations, constants.LabelEnableAuth)
	delete(isvc.Annotations, constants.LabelEnableAuthODH)
	return cli.Update(context.Background(), isvc)
}

func enableAuth(isvc *kservev1beta1.InferenceService) error {
	if isvc.Annotations == nil {
		isvc.Annotations = map[string]string{}
	}
	isvc.Annotations[constants.LabelEnableAuthODH] = "true"
	return cli.Update(context.Background(), isvc)
}

func createDSCI(dsci string) error {
	obj := &unstructured.Unstructured{}
	if err := convertToUnstructuredResource(dsci, obj); err != nil {
		return err
	}

	gvk := utils.GVK.DataScienceClusterInitialization
	obj.SetGroupVersionKind(gvk)
	dynamicClient, err := dynamic.NewForConfig(envTest.Config)
	if err != nil {
		return err
	}

	gvr := schema.GroupVersionResource{
		Group:    gvk.Group,
		Version:  gvk.Version,
		Resource: "dscinitializations",
	}
	resource := dynamicClient.Resource(gvr)
	createdObj, createErr := resource.Create(context.TODO(), obj, metav1.CreateOptions{})
	if createErr != nil {
		return nil
	}

	if status, found, err := unstructured.NestedFieldCopy(obj.Object, "status"); err != nil {
		return err
	} else if found {
		if err := unstructured.SetNestedField(createdObj.Object, status, "status"); err != nil {
			return err
		}
	}

	_, statusErr := resource.UpdateStatus(context.TODO(), createdObj, metav1.UpdateOptions{})

	return statusErr
}

func createAuthorizationPolicy(authPolicyFile string) error {
	obj := &unstructured.Unstructured{}
	if err := convertToUnstructuredResource(authPolicyFile, obj); err != nil {
		return err
	}

	obj.SetGroupVersionKind(istiosec_v1b1.SchemeGroupVersion.WithKind("AuthorizationPolicy"))
	dynamicClient, err := dynamic.NewForConfig(envTest.Config)
	if err != nil {
		return err
	}

	gvr := istiosec_v1b1.SchemeGroupVersion.WithResource("authorizationpolicies")
	resource := dynamicClient.Resource(gvr)
	_, createErr := resource.Namespace(constants.IstioNamespace).Create(context.TODO(), obj, metav1.CreateOptions{})

	return createErr
}

func deleteDSCI(dsci string) error {
	obj := &unstructured.Unstructured{}
	if err := convertToUnstructuredResource(dsci, obj); err != nil {
		return err
	}

	gvk := utils.GVK.DataScienceClusterInitialization
	obj.SetGroupVersionKind(gvk)
	dynamicClient, err := dynamic.NewForConfig(envTest.Config)
	if err != nil {
		return err
	}

	gvr := schema.GroupVersionResource{
		Group:    gvk.Group,
		Version:  gvk.Version,
		Resource: "dscinitializations",
	}
	return dynamicClient.Resource(gvr).Delete(context.TODO(), obj.GetName(), metav1.DeleteOptions{})
}

func deleteAuthorizationPolicy(authPolicyFile string) error {
	obj := &unstructured.Unstructured{}
	if err := convertToUnstructuredResource(authPolicyFile, obj); err != nil {
		return err
	}

	obj.SetGroupVersionKind(istiosec_v1b1.SchemeGroupVersion.WithKind("AuthorizationPolicy"))
	dynamicClient, err := dynamic.NewForConfig(envTest.Config)
	if err != nil {
		return err
	}

	gvr := istiosec_v1b1.SchemeGroupVersion.WithResource("authorizationpolicies")
	return dynamicClient.Resource(gvr).Namespace(constants.IstioNamespace).Delete(context.TODO(), obj.GetName(), metav1.DeleteOptions{})
}
