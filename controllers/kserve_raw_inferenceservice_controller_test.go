package controllers

import (
	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/opendatahub-io/odh-model-controller/controllers/constants"
	routev1 "github.com/openshift/api/route/v1"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
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

	When("deploying a Kserve RawDeployment model", func() {
		_ = createServingRuntime(testNs, KserveServingRuntimePath1)
		inferenceService := createInferenceService(testNs, KserveOvmsInferenceServiceName, KserveInferenceServicePath1)
		if err := cli.Create(ctx, inferenceService); err != nil && !apierrs.IsAlreadyExists(err) {
			Expect(err).NotTo(HaveOccurred())
		}
		It("it should create a clusterrolebinding for auth", func() {
			crb := &rbacv1.ClusterRoleBinding{}
			Eventually(func() error {
				key := types.NamespacedName{Name: inferenceService.Namespace + constants.KserveServiceAccountName + "-auth-delegator",
					Namespace: inferenceService.Namespace}
				return cli.Get(ctx, key, crb)
			}, timeout, interval).ShouldNot(HaveOccurred())
		})
		It("it should create a metrics service and servicemonitor", func() {
			metricsService := &corev1.Service{}
			Eventually(func() error {
				key := types.NamespacedName{Name: inferenceService.Name + "-metrics", Namespace: inferenceService.Namespace}
				return cli.Get(ctx, key, metricsService)
			}, timeout, interval).ShouldNot(HaveOccurred())
			metricsServiceMonitor := &monitoringv1.ServiceMonitor{}
			Eventually(func() error {
				key := types.NamespacedName{Name: inferenceService.Name + "-metrics", Namespace: inferenceService.Namespace}
				return cli.Get(ctx, key, metricsServiceMonitor)
			}, timeout, interval).ShouldNot(HaveOccurred())
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
		inferenceService.Labels[constants.KserveNetworkVisibility] = constants.LabelEnableKserveRawRoute
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
			crb := &rbacv1.ClusterRoleBinding{}
			key := types.NamespacedName{Name: inferenceService.Namespace + "-" + constants.KserveServiceAccountName + "-auth-delegator", Namespace: inferenceService.Namespace}
			Expect(cli.Get(ctx, key, crb)).ShouldNot(Succeed())
		})
	})
})
