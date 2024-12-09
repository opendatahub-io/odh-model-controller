package controllers

import (
	"errors"

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/opendatahub-io/odh-model-controller/controllers/constants"
	routev1 "github.com/openshift/api/route/v1"
	v1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
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
		It("it should create a default clusterrolebinding for auth", func() {
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
		It("it should create a metrics service and servicemonitor auth", func() {
			_ = createServingRuntime(testNs, KserveServingRuntimePath1)
			inferenceService := createInferenceService(testNs, KserveOvmsInferenceServiceName, KserveInferenceServicePath1)
			if err := cli.Create(ctx, inferenceService); err != nil && !apierrs.IsAlreadyExists(err) {
				Expect(err).NotTo(HaveOccurred())
			}
			metricsService := &corev1.Service{}
			Eventually(func() error {
				key := types.NamespacedName{Name: inferenceService.Name + "-metrics", Namespace: inferenceService.Namespace}
				return cli.Get(ctx, key, metricsService)
			}, timeout, interval).Should(Succeed())

			serviceMonitor := &v1.ServiceMonitor{}
			Eventually(func() error {
				key := types.NamespacedName{Name: inferenceService.Name + "-metrics", Namespace: inferenceService.Namespace}
				return cli.Get(ctx, key, serviceMonitor)
			}, timeout, interval).Should(Succeed())

			Expect(serviceMonitor.Spec.Selector.MatchLabels).To(HaveKeyWithValue("name", inferenceService.Name+"-metrics"))
		})
		It("it should create a custom rolebinding if isvc has a SA defined", func() {
			serviceAccountName := "custom-sa"
			_ = createServingRuntime(testNs, KserveServingRuntimePath1)
			inferenceService := createInferenceService(testNs, KserveOvmsInferenceServiceName, KserveInferenceServicePath1)
			inferenceService.Spec.Predictor.ServiceAccountName = serviceAccountName
			if err := cli.Create(ctx, inferenceService); err != nil && !apierrs.IsAlreadyExists(err) {
				Expect(err).NotTo(HaveOccurred())
			}

			crb := &rbacv1.ClusterRoleBinding{}
			Eventually(func() error {
				key := types.NamespacedName{Name: inferenceService.Namespace + "-" + serviceAccountName + "-auth-delegator",
					Namespace: inferenceService.Namespace}
				return cli.Get(ctx, key, crb)
			}, timeout, interval).ShouldNot(HaveOccurred())
		})
		It("it should create a route if isvc has the label to expose route", func() {
			inferenceService := createInferenceService(testNs, KserveOvmsInferenceServiceName, KserveInferenceServicePath1)
			inferenceService.Labels = map[string]string{}
			inferenceService.Labels[constants.KserveNetworkVisibility] = constants.LabelEnableKserveRawRoute
			// The service is manually created before the isvc otherwise the unit test risks running into a race condition
			// where the reconcile loop finishes before the service is created, leading to no route being created.
			isvcService := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      KserveOvmsInferenceServiceName + "-predictor",
					Namespace: inferenceService.Namespace,
					Annotations: map[string]string{
						"openshift.io/display-name":        KserveOvmsInferenceServiceName,
						"serving.kserve.io/deploymentMode": "RawDeployment",
					},
					Labels: map[string]string{
						"app":                                "isvc." + KserveOvmsInferenceServiceName + "-predictor",
						"component":                          "predictor",
						"serving.kserve.io/inferenceservice": KserveOvmsInferenceServiceName,
					},
				},
				Spec: corev1.ServiceSpec{
					ClusterIP:  "None",
					IPFamilies: []corev1.IPFamily{"IPv4"},
					Ports: []corev1.ServicePort{
						{
							Name:       "https",
							Protocol:   corev1.ProtocolTCP,
							Port:       8888,
							TargetPort: intstr.FromString("https"),
						},
					},
					ClusterIPs: []string{"None"},
					Selector: map[string]string{
						"app": "isvc." + KserveOvmsInferenceServiceName + "-predictor",
					},
				},
			}
			if err := cli.Create(ctx, isvcService); err != nil && !apierrs.IsAlreadyExists(err) {
				Expect(err).NotTo(HaveOccurred())
			}
			service := &corev1.Service{}
			Eventually(func() error {
				key := types.NamespacedName{Name: isvcService.Name, Namespace: isvcService.Namespace}
				return cli.Get(ctx, key, service)
			}, timeout, interval).Should(Succeed())

			_ = createServingRuntime(testNs, KserveServingRuntimePath1)
			if err := cli.Create(ctx, inferenceService); err != nil && !apierrs.IsAlreadyExists(err) {
				Expect(err).NotTo(HaveOccurred())
			}

			route := &routev1.Route{}
			Eventually(func() error {
				key := types.NamespacedName{Name: inferenceService.Name, Namespace: inferenceService.Namespace}
				return cli.Get(ctx, key, route)
			}, timeout, interval).ShouldNot(HaveOccurred())
		})
	})
	When("deleting a Kserve RawDeployment model", func() {
		It("the associated route should be deleted", func() {
			_ = createServingRuntime(testNs, KserveServingRuntimePath1)
			inferenceService := createInferenceService(testNs, KserveOvmsInferenceServiceName, KserveInferenceServicePath1)
			if err := cli.Create(ctx, inferenceService); err != nil && !apierrs.IsAlreadyExists(err) {
				Expect(err).NotTo(HaveOccurred())
			}

			Expect(cli.Delete(ctx, inferenceService)).Should(Succeed())

			route := &routev1.Route{}
			Eventually(func() error {
				key := types.NamespacedName{Name: inferenceService.Name, Namespace: inferenceService.Namespace}
				return cli.Get(ctx, key, route)
			}, timeout, interval).Should(HaveOccurred())
		})
		It("the associated metrics service and servicemonitor should be deleted", func() {
			_ = createServingRuntime(testNs, KserveServingRuntimePath1)
			inferenceService := createInferenceService(testNs, KserveOvmsInferenceServiceName, KserveInferenceServicePath1)
			if err := cli.Create(ctx, inferenceService); err != nil && !apierrs.IsAlreadyExists(err) {
				Expect(err).NotTo(HaveOccurred())
			}

			Expect(cli.Delete(ctx, inferenceService)).Should(Succeed())

			metricsService := &corev1.Service{}
			Eventually(func() error {
				key := types.NamespacedName{Name: inferenceService.Name, Namespace: inferenceService.Namespace}
				return cli.Get(ctx, key, metricsService)
			}, timeout, interval).Should(HaveOccurred())

			serviceMonitor := &v1.ServiceMonitor{}
			Eventually(func() error {
				key := types.NamespacedName{Name: inferenceService.Name, Namespace: inferenceService.Namespace}
				return cli.Get(ctx, key, serviceMonitor)
			}, timeout, interval).Should(HaveOccurred())
		})
	})
	When("namespace no longer has any RawDeployment models", func() {
		It("should delete the default clusterrolebinding", func() {
			_ = createServingRuntime(testNs, KserveServingRuntimePath1)
			inferenceService := createInferenceService(testNs, KserveOvmsInferenceServiceName, KserveInferenceServicePath1)
			if err := cli.Create(ctx, inferenceService); err != nil && !apierrs.IsAlreadyExists(err) {
				Expect(err).NotTo(HaveOccurred())
			}
			Expect(cli.Delete(ctx, inferenceService)).Should(Succeed())
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
		})
	})
})
