package controllers

import (
	"errors"
	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/opendatahub-io/odh-model-controller/controllers/constants"
	routev1 "github.com/openshift/api/route/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("The KServe Raw reconciler", func() {
	var testNs string
	createServingRuntime := func(namespace, path string) *kservev1alpha1.ServingRuntime {
		servingRuntime := &kservev1alpha1.ServingRuntime{}
		err := convertToStructuredResource(path, servingRuntime)
		Expect(err).NotTo(HaveOccurred())
		servingRuntime.SetNamespace(namespace)
		if err := cli.Create(ctx, servingRuntime); err != nil && !apierrs.IsAlreadyExists(err) {
			Expect(err).NotTo(HaveOccurred())
		}
		return servingRuntime
	}

	createInferenceService := func(namespace, name string, path string) *kservev1beta1.InferenceService {
		inferenceService := &kservev1beta1.InferenceService{}
		err := convertToStructuredResource(path, inferenceService)
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
		testNs = Namespaces.Create(cli).Name

		inferenceServiceConfig := &corev1.ConfigMap{}
		Expect(convertToStructuredResource(InferenceServiceConfigPath1, inferenceServiceConfig)).To(Succeed())
		if err := cli.Create(ctx, inferenceServiceConfig); err != nil && !apierrs.IsAlreadyExists(err) {
			Fail(err.Error())
		}

	})

	When("deploying a Kserve RawDeployment model with route disabled", func() {
		It("it should create a clusterrolebinding for auth but not create a route", func() {
			_ = createServingRuntime(testNs, KserveServingRuntimePath1)
			inferenceService := createInferenceService(testNs, KserveOvmsInferenceServiceName, KserveInferenceServicePath1)
			if err := cli.Create(ctx, inferenceService); err != nil && !apierrs.IsAlreadyExists(err) {
				Expect(err).NotTo(HaveOccurred())
			}

			crb := &rbacv1.ClusterRoleBinding{}
			Eventually(func() error {
				key := types.NamespacedName{Name: inferenceService.Namespace + "-" + constants.KserveServiceAccountName + "-auth-delegator",
					Namespace: inferenceService.Namespace}
				return cli.Get(ctx, key, crb)
			}, timeout, interval).ShouldNot(HaveOccurred())

			route := &routev1.Route{}
			Eventually(func() error {
				key := types.NamespacedName{Name: inferenceService.Name, Namespace: inferenceService.Namespace}
				return cli.Get(ctx, key, route)
			}, timeout, interval).Should(HaveOccurred())
		})
	})
	When("namespace no longer has any RawDeployment models", func() {
		It("should delete the clusterrolebinding", func() {
			_ = createServingRuntime(testNs, KserveServingRuntimePath1)
			inferenceService := createInferenceService(testNs, KserveOvmsInferenceServiceName, KserveInferenceServicePath1)
			if err := cli.Create(ctx, inferenceService); err != nil && !apierrs.IsAlreadyExists(err) {
				Expect(err).NotTo(HaveOccurred())
			}
			inferenceServiceList := &kservev1beta1.InferenceServiceList{}
			if err := cli.List(ctx, inferenceServiceList, client.InNamespace(testNs)); err != nil {
				Expect(err).NotTo(HaveOccurred())
			}
			for _, isvc := range inferenceServiceList.Items {
				Expect(cli.Delete(ctx, &isvc)).Should(Succeed())
			}
			crb := &rbacv1.ClusterRoleBinding{}
			Eventually(func() error {
				namespacedNamed := types.NamespacedName{Name: testNs + "-" + constants.KserveServiceAccountName + "-auth-delegator", Namespace: WorkingNamespace}
				err := cli.Get(ctx, namespacedNamed, crb)
				if apierrs.IsNotFound(err) {
					return nil
				} else {
					return errors.New("crb deletion not detected")
				}
			}, timeout, interval).ShouldNot(HaveOccurred())
			//Eventually(func() error {
			//	crb := &rbacv1.ClusterRoleBinding{}
			//	key := types.NamespacedName{Name: testNs + "-" + constants.KserveServiceAccountName + "-auth-delegator", Namespace: testNs}
			//	err := cli.Get(ctx, key, crb)
			//	return err
			//}, timeout, interval).ShouldNot(Succeed())
		})
	})
})
