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

package serving

import (
	"fmt"
	"time"

	kedaapi "github.com/kedacore/keda/v2/apis/keda/v1alpha1"
	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/serving/reconcilers"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apiserver/pkg/storage/names"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	. "github.com/opendatahub-io/odh-model-controller/test/matchers"
	testutils "github.com/opendatahub-io/odh-model-controller/test/utils"
)

const (
	KserveOvmsInferenceServiceName         = "example-onnx-mnist"
	UnsupportedMetricsInferenceServiceName = "sklearn-v2-iris"

	UnsupportedMetricsInferenceServicePath = "./testdata/deploy/kserve-unsupported-metrics-inference-service.yaml"
	UnsupprtedMetricsServingRuntimePath    = "./testdata/deploy/kserve-unsupported-metrics-serving-runtime.yaml"
)

var _ = Describe("InferenceService Controller", func() {

	Describe("Dashboard reconciler", func() {
		var testNs string

		createServingRuntime := func(namespace, path string) *kservev1alpha1.ServingRuntime {
			servingRuntime := &kservev1alpha1.ServingRuntime{}
			err := testutils.ConvertToStructuredResource(path, servingRuntime)
			Expect(err).NotTo(HaveOccurred())
			servingRuntime.SetNamespace(namespace)
			if err := k8sClient.Create(ctx, servingRuntime); err != nil && !k8sErrors.IsAlreadyExists(err) {
				Fail(err.Error())
			}
			return servingRuntime
		}

		createInferenceService := func(namespace, name string, path string, isRaw ...bool) *kservev1beta1.InferenceService {
			inferenceService := &kservev1beta1.InferenceService{}
			err := testutils.ConvertToStructuredResource(path, inferenceService)
			Expect(err).NotTo(HaveOccurred())
			inferenceService.SetNamespace(namespace)
			if len(name) != 0 {
				inferenceService.Name = name
			}
			raw := len(isRaw) > 0 && isRaw[0]
			if raw {
				if inferenceService.Annotations == nil {
					inferenceService.Annotations = map[string]string{}
				}
				inferenceService.Annotations["serving.kserve.io/deploymentMode"] = "RawDeployment"
			}
			if err := k8sClient.Create(ctx, inferenceService); err != nil && !k8sErrors.IsAlreadyExists(err) {
				Fail(err.Error())
			}
			return inferenceService
		}

		BeforeEach(func() {
			testNs = testutils.Namespaces.Create(ctx, k8sClient).Name

			inferenceServiceConfig := &corev1.ConfigMap{}
			Expect(testutils.ConvertToStructuredResource(InferenceServiceConfigPath1, inferenceServiceConfig)).To(Succeed())
			if err := k8sClient.Create(ctx, inferenceServiceConfig); err != nil {
				Fail(err.Error())
			}
		})

		When("deleting the deployed models", func() {
			timeout10s := time.Second * 10
			interval4s := time.Second * 4
			It("[raw] it should delete the associated configmap", func() {
				_ = createServingRuntime(testNs, KserveServingRuntimePath1)
				OvmsInferenceService := createInferenceService(testNs, KserveOvmsInferenceServiceName, KserveInferenceServicePath1, true)

				Expect(k8sClient.Delete(ctx, OvmsInferenceService)).Should(Succeed())
				Eventually(func() error {
					configmap := &corev1.ConfigMap{}
					key := types.NamespacedName{Name: KserveOvmsInferenceServiceName + constants.KserveMetricsConfigMapNameSuffix, Namespace: OvmsInferenceService.Namespace}
					err := k8sClient.Get(ctx, key, configmap)
					return err
				}, timeout10s, interval4s).ShouldNot(Succeed())

				_ = createServingRuntime(testNs, UnsupprtedMetricsServingRuntimePath)
				SklearnInferenceService := createInferenceService(testNs, UnsupportedMetricsInferenceServiceName, UnsupportedMetricsInferenceServicePath, true)

				Expect(k8sClient.Delete(ctx, SklearnInferenceService)).Should(Succeed())
				Eventually(func() error {
					configmap := &corev1.ConfigMap{}
					key := types.NamespacedName{Name: UnsupportedMetricsInferenceServiceName + constants.KserveMetricsConfigMapNameSuffix, Namespace: SklearnInferenceService.Namespace}
					err := k8sClient.Get(ctx, key, configmap)
					return err
				}, timeout10s, interval4s).ShouldNot(Succeed())
			})
		})
	})

	Describe("The KServe Raw reconciler", func() {
		var testNs string
		createServingRuntime := func(namespace, path string) *kservev1alpha1.ServingRuntime {
			servingRuntime := &kservev1alpha1.ServingRuntime{}
			err := testutils.ConvertToStructuredResource(path, servingRuntime)
			Expect(err).NotTo(HaveOccurred())
			servingRuntime.SetNamespace(namespace)
			if err := k8sClient.Create(ctx, servingRuntime); err != nil && !k8sErrors.IsAlreadyExists(err) {
				Expect(err).NotTo(HaveOccurred())
			}
			return servingRuntime
		}

		createInferenceService := func(namespace, name string, path string) *kservev1beta1.InferenceService {
			inferenceService := &kservev1beta1.InferenceService{}
			err := testutils.ConvertToStructuredResource(path, inferenceService)
			Expect(err).NotTo(HaveOccurred())
			inferenceService.SetNamespace(namespace)
			if len(name) != 0 {
				inferenceService.Name = name
			}
			inferenceService.Annotations = map[string]string{}
			inferenceService.Annotations["serving.kserve.io/deploymentMode"] = "RawDeployment"
			return inferenceService
		}

		BeforeEach(func() {
			testNs = testutils.Namespaces.Create(ctx, k8sClient).Name

			inferenceServiceConfig := &corev1.ConfigMap{}
			Expect(testutils.ConvertToStructuredResource(InferenceServiceConfigPath1, inferenceServiceConfig)).To(Succeed())
			if err := k8sClient.Create(ctx, inferenceServiceConfig); err != nil && !k8sErrors.IsAlreadyExists(err) {
				Fail(err.Error())
			}
		})

		When("deploying a Kserve RawDeployment model", func() {
			It("it should create a metrics service and servicemonitor auth", func() {
				_ = createServingRuntime(testNs, KserveServingRuntimePath1)
				inferenceService := createInferenceService(testNs, KserveOvmsInferenceServiceName, KserveInferenceServicePath1)
				if err := k8sClient.Create(ctx, inferenceService); err != nil {
					Expect(err).NotTo(HaveOccurred())
				}
				metricsService := &corev1.Service{}
				Eventually(func() error {
					key := types.NamespacedName{Name: inferenceService.Name + "-metrics", Namespace: inferenceService.Namespace}
					return k8sClient.Get(ctx, key, metricsService)
				}, timeout, interval).Should(Succeed())

				serviceMonitor := &monitoringv1.ServiceMonitor{}
				Eventually(func() error {
					key := types.NamespacedName{Name: inferenceService.Name + "-metrics", Namespace: inferenceService.Namespace}
					return k8sClient.Get(ctx, key, serviceMonitor)
				}, timeout, interval).Should(Succeed())

				Expect(serviceMonitor.Spec.Selector.MatchLabels).To(HaveKeyWithValue("name", inferenceService.Name+"-metrics"))
			})
		})
		When("deleting a Kserve RawDeployment model", func() {
			It("the associated metrics service and servicemonitor should be deleted", func() {
				_ = createServingRuntime(testNs, KserveServingRuntimePath1)
				inferenceService := createInferenceService(testNs, KserveOvmsInferenceServiceName, KserveInferenceServicePath1)
				if err := k8sClient.Create(ctx, inferenceService); err != nil {
					Expect(err).NotTo(HaveOccurred())
				}

				Expect(k8sClient.Delete(ctx, inferenceService)).Should(Succeed())

				metricsService := &corev1.Service{}
				Eventually(func() error {
					key := types.NamespacedName{Name: inferenceService.Name, Namespace: inferenceService.Namespace}
					return k8sClient.Get(ctx, key, metricsService)
				}, timeout, interval).Should(HaveOccurred())

				serviceMonitor := &monitoringv1.ServiceMonitor{}
				Eventually(func() error {
					key := types.NamespacedName{Name: inferenceService.Name, Namespace: inferenceService.Namespace}
					return k8sClient.Get(ctx, key, serviceMonitor)
				}, timeout, interval).Should(HaveOccurred())
			})
		})
	})

	Describe("KServe KEDA Reconciler", func() {
		var (
			testNs         string
			kedaReconciler *reconcilers.KserveKEDAReconciler
		)

		BeforeEach(func() {
			testNs = testutils.Namespaces.Create(ctx, k8sClient).Name
			kedaReconciler = reconcilers.NewKServeKEDAReconciler(k8sClient)
		})

		Context("when InferenceServices are configured to scale on KEDA metrics", func() {
			var isvc *kservev1beta1.InferenceService
			var isvc2 *kservev1beta1.InferenceService

			const fake = "fake" // for the love of the linter :)

			BeforeEach(func() {
				isvc = makeKedaTestISVC(testNs, names.SimpleNameGenerator.GenerateName("keda-isvc"), true)
				Expect(k8sClient.Create(ctx, isvc)).Should(Succeed())
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: isvc.Name, Namespace: isvc.Namespace}, isvc)).Should(Succeed())

				isvc2 = makeKedaTestISVC(testNs, names.SimpleNameGenerator.GenerateName("keda-isvc-2"), true)
				Expect(k8sClient.Create(ctx, isvc2)).Should(Succeed())
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: isvc2.Name, Namespace: isvc2.Namespace}, isvc2)).Should(Succeed())

				// Safety checks
				Expect(isvc.Spec.Predictor.AutoScaling).ToNot(BeNil())
				Expect(isvc2.Spec.Predictor.AutoScaling).ToNot(BeNil())
				Expect(isvc.ObjectMeta.UID).ToNot(BeEmpty())
				Expect(isvc2.ObjectMeta.UID).ToNot(BeEmpty())

				Expect(kedaReconciler.Reconcile(ctx, GinkgoLogr, isvc)).NotTo(HaveOccurred())
				Expect(kedaReconciler.Reconcile(ctx, GinkgoLogr, isvc2)).NotTo(HaveOccurred())
			})

			It("Must have owner reference to InferenceService", func() {
				for _, obj := range getAllKedaTestResources(ctx, k8sClient, testNs) {
					Expect(obj).To(Not(BeNil()), fmt.Sprintf("Obj: %#v", obj))
					Expect(obj).To(HaveOwnerReferenceByUID(isvc.UID))
					Expect(obj).To(HaveOwnerReferenceByUID(isvc2.UID))
				}
			})

			It("Must remove owner reference from deleted InferenceService", func() {
				Expect(k8sClient.Delete(ctx, isvc2)).Should(Succeed())
				Expect(kedaReconciler.Delete(ctx, GinkgoLogr, isvc2)).To(Succeed())

				for _, obj := range getAllKedaTestResources(ctx, k8sClient, testNs) {
					Expect(obj).To(Not(BeNil()), fmt.Sprintf("Obj: %#v", obj))
					Expect(obj).To(HaveOwnerReferenceByUID(isvc.UID))
					Expect(obj).ToNot(HaveOwnerReferenceByUID(isvc2.UID))
				}
			})

			It("Must update managed role when it diverges", func() {
				role := &rbacv1.Role{}
				Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: isvc.Namespace, Name: reconcilers.KEDAPrometheusAuthMetricsReaderRoleName}, role)).Should(Succeed())
				role.Rules[0].Resources = []string{fake}
				Expect(k8sClient.Update(ctx, role)).To(Succeed())

				Expect(kedaReconciler.Reconcile(ctx, GinkgoLogr, isvc)).To(Succeed())

				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(role), role)).Should(Succeed())

				Expect(role.Rules[0].Resources[0]).ToNot(Equal(fake))
			})

			It("Must update managed service account when it diverges", func() {
				sa := &corev1.ServiceAccount{}
				Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: isvc.Namespace, Name: reconcilers.KEDAPrometheusAuthMetricsReaderRoleName}, sa)).Should(Succeed())
				sa.Labels[reconcilers.KEDAResourcesLabelKey] = fake
				Expect(k8sClient.Update(ctx, sa)).To(Succeed())

				Expect(kedaReconciler.Reconcile(ctx, GinkgoLogr, isvc)).To(Succeed())

				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sa), sa)).Should(Succeed())

				Expect(sa.Labels[reconcilers.KEDAResourcesLabelKey]).To(Equal(reconcilers.KEDAResourcesLabelValue))
			})

			It("Must update managed role binding when it diverges", func() {
				rb := &rbacv1.RoleBinding{}
				Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: isvc.Namespace, Name: reconcilers.KEDAPrometheusAuthMetricsReaderRoleBindingName}, rb)).Should(Succeed())
				rb.Subjects[0].Kind = "User"
				rb.Subjects[0].Name = fake
				Expect(k8sClient.Update(ctx, rb)).To(Succeed())

				Expect(kedaReconciler.Reconcile(ctx, GinkgoLogr, isvc)).To(Succeed())

				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(rb), rb)).Should(Succeed())

				Expect(rb.Subjects[0].Kind).ToNot(Equal("User"))
				Expect(rb.Subjects[0].Name).ToNot(Equal(fake))
			})

			It("Must update managed secret when it diverges", func() {
				secret := &corev1.Secret{}
				Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: isvc.Namespace, Name: reconcilers.KEDAPrometheusAuthTriggerSecretName}, secret)).Should(Succeed())
				secret.Labels[reconcilers.KEDAResourcesLabelKey] = fake
				Expect(k8sClient.Update(ctx, secret)).To(Succeed())

				Expect(kedaReconciler.Reconcile(ctx, GinkgoLogr, isvc)).To(Succeed())

				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(secret), secret)).Should(Succeed())

				Expect(secret.Labels[reconcilers.KEDAResourcesLabelKey]).To(Equal(reconcilers.KEDAResourcesLabelValue))
			})

			It("Must update managed trigger authentication when it diverges", func() {
				ta := &kedaapi.TriggerAuthentication{}
				Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: isvc.Namespace, Name: reconcilers.KEDAPrometheusAuthTriggerAuthName}, ta)).Should(Succeed())
				ta.Spec.SecretTargetRef = []kedaapi.AuthSecretTargetRef{}
				Expect(k8sClient.Update(ctx, ta)).To(Succeed())

				Expect(kedaReconciler.Reconcile(ctx, GinkgoLogr, isvc)).To(Succeed())

				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(ta), ta)).Should(Succeed())

				Expect(ta.Spec.SecretTargetRef).To(HaveLen(2))
			})

			AfterEach(func() {
				Expect(k8sClient.Delete(ctx, isvc)).Should(Succeed())
				Expect(kedaReconciler.Delete(ctx, GinkgoLogr, isvc)).To(Succeed())

				// isvc2 may already be deleted by specific tests (e.g., owner reference removal test)
				if err := k8sClient.Delete(ctx, isvc2); err == nil {
					Expect(kedaReconciler.Delete(ctx, GinkgoLogr, isvc2)).To(Succeed())
				} else if !k8sErrors.IsNotFound(err) {
					Expect(err).ToNot(HaveOccurred())
				}

				Expect(kedaReconciler.Cleanup(ctx, GinkgoLogr, testNs)).To(Succeed())

				Expect(getAllKedaTestResources(ctx, k8sClient, testNs)).To(BeEmpty())
			})
		})

		Context("when an InferenceService no longer requires KEDA metrics", func() {
			var isvc *kservev1beta1.InferenceService

			BeforeEach(func() {
				// Create ISVC initially requiring KEDA
				isvc = makeKedaTestISVC(testNs, names.SimpleNameGenerator.GenerateName("keda-isvc"), true)
				Expect(k8sClient.Create(ctx, isvc)).Should(Succeed())
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: isvc.Name, Namespace: isvc.Namespace}, isvc)).Should(Succeed())

				err := kedaReconciler.Reconcile(ctx, GinkgoLogr, isvc)
				Expect(err).NotTo(HaveOccurred())

				// Verify initial ownership
				for _, obj := range getAllKedaTestResources(ctx, k8sClient, testNs) {
					Expect(obj).To(Not(BeNil()), fmt.Sprintf("Obj: %#v", obj))
					Expect(obj).To(HaveOwnerReferenceByUID(isvc.UID))
				}

				// Update ISVC to no longer require KEDA
				// Fetch the latest version first
				latestIsvc := &kservev1beta1.InferenceService{}
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: isvc.Name, Namespace: isvc.Namespace}, latestIsvc)).Should(Succeed())

				updatedIsvc := makeKedaTestISVC(testNs, isvc.Name, false)
				latestIsvc.Spec = updatedIsvc.Spec // Only update spec
				Expect(k8sClient.Update(ctx, latestIsvc)).Should(Succeed())
				isvc = latestIsvc // Use the updated ISVC for the Reconcile call
			})

			It("should remove the ISVC owner reference from all KEDA resources", func() {
				err := kedaReconciler.Reconcile(ctx, GinkgoLogr, isvc)
				Expect(err).NotTo(HaveOccurred())

				// Verify owner reference is removed
				for _, obj := range getAllKedaTestResources(ctx, k8sClient, testNs) {
					// Refetch the object to get its latest state
					currentObj := obj.DeepCopyObject().(client.Object)
					Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(currentObj), currentObj)).Should(Succeed())
					Expect(obj).ToNot(HaveOwnerReferenceByUID(isvc.UID))
				}
			})
		})

		Context("when an ISVC no longer requires KEDA and resources have other owners", func() {
			var isvc1 *kservev1beta1.InferenceService
			var otherOwnerRef *metav1.OwnerReference

			BeforeEach(func() {
				isvc1 = makeKedaTestISVC(testNs, "isvc1-keda", true)
				Expect(k8sClient.Create(ctx, isvc1)).Should(Succeed())

				// Create a second InferenceService so that the resources are not deleted.
				isvc2 := makeKedaTestISVC(testNs, "isvc2-keda", true)
				Expect(k8sClient.Create(ctx, isvc2)).Should(Succeed())

				// Dummy "other" owner
				otherOwnerRef = &metav1.OwnerReference{
					APIVersion: "v1",
					Kind:       "Pod", // Arbitrary kind for testing
					Name:       "other-owner-pod",
					UID:        "other-owner-uid",
				}

				// Create KEDA resources owned by ISVC1 and otherOwner
				_ = createTestKedaSA(ctx, k8sClient, testNs, isvc1, otherOwnerRef)
				_ = createTestKedaSecret(ctx, k8sClient, testNs, isvc1, otherOwnerRef)
				_ = createTestKedaRole(ctx, k8sClient, testNs, isvc1, otherOwnerRef)
				_ = createTestKedaRoleBinding(ctx, k8sClient, testNs, isvc1, otherOwnerRef)
				_ = createTestKedaTA(ctx, k8sClient, testNs, isvc1, otherOwnerRef)

				// Update ISVC1 to no longer require KEDA
				latestIsvc1 := &kservev1beta1.InferenceService{}
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: isvc1.Name, Namespace: isvc1.Namespace}, latestIsvc1)).Should(Succeed())
				updatedIsvc1 := makeKedaTestISVC(testNs, "isvc1-keda", false)
				latestIsvc1.Spec = updatedIsvc1.Spec
				Expect(k8sClient.Update(ctx, latestIsvc1)).Should(Succeed())
				isvc1 = latestIsvc1
			})

			It("should remove only the specific ISVC's owner reference, preserving others", func() {
				err := kedaReconciler.Reconcile(ctx, GinkgoLogr, isvc1)
				Expect(err).NotTo(HaveOccurred())

				sa := &corev1.ServiceAccount{}
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: reconcilers.KEDAPrometheusAuthResourceName, Namespace: testNs}, sa)).Should(Succeed())
				Expect(sa).ToNot(HaveOwnerReferenceByUID(isvc1.UID))
				Expect(sa).To(HaveOwnerReferenceByUID(otherOwnerRef.UID))

				secret := &corev1.Secret{}
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: reconcilers.KEDAPrometheusAuthTriggerSecretName, Namespace: testNs}, secret)).Should(Succeed())
				Expect(secret).ToNot(HaveOwnerReferenceByUID(isvc1.UID))
				Expect(secret).To(HaveOwnerReferenceByUID(otherOwnerRef.UID))

				ta := &kedaapi.TriggerAuthentication{}
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: reconcilers.KEDAPrometheusAuthTriggerAuthName, Namespace: testNs}, ta)).Should(Succeed())
				Expect(ta).ToNot(HaveOwnerReferenceByUID(isvc1.UID))
				Expect(ta).To(HaveOwnerReferenceByUID(otherOwnerRef.UID))

				role := &rbacv1.Role{}
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: reconcilers.KEDAPrometheusAuthMetricsReaderRoleName, Namespace: testNs}, role)).Should(Succeed())
				Expect(role).ToNot(HaveOwnerReferenceByUID(isvc1.UID))
				Expect(role).To(HaveOwnerReferenceByUID(otherOwnerRef.UID))

				roleBinding := &rbacv1.RoleBinding{}
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: reconcilers.KEDAPrometheusAuthMetricsReaderRoleBindingName, Namespace: testNs}, roleBinding)).Should(Succeed())
				Expect(roleBinding).ToNot(HaveOwnerReferenceByUID(isvc1.UID))
				Expect(roleBinding).To(HaveOwnerReferenceByUID(otherOwnerRef.UID))
			})
		})

		Context("when KEDA resources do not exist and autoscaling is not configured", func() {
			var isvc *kservev1beta1.InferenceService

			BeforeEach(func() {
				isvc = makeKedaTestISVC(testNs, "isvc-no-keda-res", false)
				Expect(k8sClient.Create(ctx, isvc)).Should(Succeed())
			})

			It("should complete without error (no-op)", func() {
				err := kedaReconciler.Reconcile(ctx, GinkgoLogr, isvc)
				Expect(err).NotTo(HaveOccurred())

				sa := &corev1.ServiceAccount{}
				err = k8sClient.Get(ctx, types.NamespacedName{Name: reconcilers.KEDAPrometheusAuthResourceName, Namespace: testNs}, sa)
				Expect(err).To(HaveOccurred())
				Expect(k8sErrors.IsNotFound(err)).To(BeTrue())

				secret := &corev1.Secret{}
				err = k8sClient.Get(ctx, types.NamespacedName{Name: reconcilers.KEDAPrometheusAuthResourceName, Namespace: testNs}, secret)
				Expect(err).To(HaveOccurred())
				Expect(k8sErrors.IsNotFound(err)).To(BeTrue())

				ta := &kedaapi.TriggerAuthentication{}
				err = k8sClient.Get(ctx, types.NamespacedName{Name: reconcilers.KEDAPrometheusAuthResourceName, Namespace: testNs}, ta)
				Expect(err).To(HaveOccurred())
				Expect(k8sErrors.IsNotFound(err)).To(BeTrue())

				role := &rbacv1.Role{}
				err = k8sClient.Get(ctx, types.NamespacedName{Name: reconcilers.KEDAPrometheusAuthResourceName, Namespace: testNs}, role)
				Expect(err).To(HaveOccurred())
				Expect(k8sErrors.IsNotFound(err)).To(BeTrue())

				roleBinding := &rbacv1.RoleBinding{}
				err = k8sClient.Get(ctx, types.NamespacedName{Name: reconcilers.KEDAPrometheusAuthResourceName, Namespace: testNs}, roleBinding)
				Expect(err).To(HaveOccurred())
				Expect(k8sErrors.IsNotFound(err)).To(BeTrue())
			})
		})

		Context("when KEDA resource does not have the target ISVC as an owner and autoscaling is not configured", func() {
			var isvc *kservev1beta1.InferenceService
			var sa *corev1.ServiceAccount

			BeforeEach(func() {
				isvc = makeKedaTestISVC(testNs, "isvc-not-owner", false)
				Expect(k8sClient.Create(ctx, isvc)).Should(Succeed())

				// Create a second InferenceService so that the resources are not deleted.
				isvc2 := makeKedaTestISVC(testNs, "isvc2-keda", true)
				Expect(k8sClient.Create(ctx, isvc2)).Should(Succeed())

				// Create SA but not owned by this ISVC
				sa = createTestKedaSA(ctx, k8sClient, testNs, nil, nil) // No owners
			})

			It("should complete without error and not modify the resource's owners", func() {
				originalSAOwnerRefs := sa.GetOwnerReferences()
				err := kedaReconciler.Reconcile(ctx, GinkgoLogr, isvc)
				Expect(err).NotTo(HaveOccurred())

				updatedSA := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: reconcilers.KEDAPrometheusAuthServiceAccountName, Namespace: testNs}}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sa), updatedSA)).Should(Succeed())
				Expect(updatedSA.GetOwnerReferences()).To(Equal(originalSAOwnerRefs)) // Should be unchanged
			})
		})

		Context("when ISVC does require KEDA resources", func() {
			var isvc *kservev1beta1.InferenceService
			var sa *corev1.ServiceAccount

			BeforeEach(func() {
				isvc = makeKedaTestISVC(testNs, "isvc-not-owner", false)
				Expect(k8sClient.Create(ctx, isvc)).Should(Succeed())

				// Create SA
				sa = createTestKedaSA(ctx, k8sClient, testNs, nil, nil) // No owners
			})

			It("should cleanup resources", func() {
				err := kedaReconciler.Reconcile(ctx, GinkgoLogr, isvc)
				Expect(err).NotTo(HaveOccurred())

				updatedSA := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: reconcilers.KEDAPrometheusAuthServiceAccountName, Namespace: testNs}}
				err = k8sClient.Get(ctx, client.ObjectKeyFromObject(sa), updatedSA)
				Expect(err).Should(HaveOccurred())
				Expect(k8sErrors.IsNotFound(err)).Should(BeTrue())
			})
		})
	})
})
