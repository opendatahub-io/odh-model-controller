package controllers

import (
	"context"
	"fmt"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/opendatahub-io/odh-model-controller/controllers/constants"
	routev1 "github.com/openshift/api/route/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"time"

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
)

var _ = FDescribe("The KServe Raw reconciler", func() {
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

	When("deploying a Kserve RawDeployment model", func() {
		_ = createServingRuntime(testNs, KserveServingRuntimePath1)
		inferenceService := createInferenceService(testNs, KserveOvmsInferenceServiceName, KserveInferenceServicePath1)
		if err := cli.Create(ctx, inferenceService); err != nil && !apierrs.IsAlreadyExists(err) {
			Expect(err).NotTo(HaveOccurred())
		}
		It("it should create a service account and clusterrolebinding for auth", func() {
			sa, err := waitForServiceAccount(cli, testNs, constants.KserveServiceAccountName, 30, 1*time.Second)
			Expect(err).NotTo(HaveOccurred())
			Expect(sa).NotTo(BeNil())
		})
		It("it should not create a route", func() {
			route := &routev1.Route{}
			Eventually(func() error {
				key := types.NamespacedName{Name: inferenceService.Name, Namespace: inferenceService.Namespace}
				return cli.Get(ctx, key, route)
			}, timeout, interval).Should(HaveOccurred())
		})
	})
	When("deploying a Kserve RawDeployment model that has route creation enabled", func() {
		_ = createServingRuntime(testNs, KserveServingRuntimePath1)
		inferenceService := createInferenceService(testNs, KserveOvmsInferenceServiceName, KserveInferenceServicePath1)
		if inferenceService.Labels == nil {
			inferenceService.Labels = map[string]string{}
		}
		inferenceService.Labels[constants.KserveNetworkVisibility] = constants.LabelEnableRoute
		if err := cli.Create(ctx, inferenceService); err != nil && !apierrs.IsAlreadyExists(err) {
			Expect(err).NotTo(HaveOccurred())
		}
		It("it should create a route", func() {
			route := &routev1.Route{}
			Eventually(func() error {
				key := types.NamespacedName{Name: inferenceService.Name, Namespace: inferenceService.Namespace}
				return cli.Get(ctx, key, route)
			}, timeout, interval).ShouldNot(HaveOccurred())
		})
	})
	When("namespace no longer has any RawDeployment models", func() {
		It("should delete the service account and clusterrolebinding", func() {
			servingRuntime := createServingRuntime(testNs, KserveServingRuntimePath1)
			inferenceService := createInferenceService(testNs, KserveOvmsInferenceServiceName, KserveInferenceServicePath1)
			if err := cli.Create(ctx, inferenceService); err != nil && !apierrs.IsAlreadyExists(err) {
				Expect(err).NotTo(HaveOccurred())
			}
			Expect(cli.Delete(ctx, inferenceService)).Should(Succeed())
			Expect(cli.Delete(ctx, servingRuntime)).Should(Succeed())
			sa := &corev1.ServiceAccount{}
			key := types.NamespacedName{Name: constants.KserveServiceAccountName, Namespace: inferenceService.Namespace}
			Expect(cli.Get(ctx, key, sa)).ShouldNot(Succeed())
			crb := &rbacv1.ClusterRoleBinding{}
			key = types.NamespacedName{Name: inferenceService.Namespace + "-" + constants.KserveServiceAccountName + "-auth-delegator", Namespace: inferenceService.Namespace}
			Expect(cli.Get(ctx, key, crb)).ShouldNot(Succeed())
		})
	})
})

func waitForServiceAccount(cli client.Client, namespace, saName string, maxTries int, delay time.Duration) (*corev1.ServiceAccount, error) {
	time.Sleep(delay)

	ctx := context.Background()
	sa := &corev1.ServiceAccount{}
	for try := 1; try <= maxTries; try++ {
		err := cli.Get(ctx, client.ObjectKey{Namespace: namespace, Name: saName}, sa)
		if err == nil {
			return sa, nil
		}
		if apierrs.IsNotFound(err) {
			return nil, fmt.Errorf("failed to get configmap %s/%s: %v", namespace, saName, err)
		}

		if try > maxTries {
			time.Sleep(1 * time.Second)
			return nil, err
		}
	}
	return sa, nil
}
