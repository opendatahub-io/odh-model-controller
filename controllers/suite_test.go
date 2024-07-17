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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"math/rand"
	"os"
	"path/filepath"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	authorinov1beta2 "github.com/kuadrant/authorino/api/v1beta2"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"go.uber.org/zap/zapcore"
	k8srbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	routev1 "github.com/openshift/api/route/v1"
	istioclientv1beta1 "istio.io/client-go/pkg/apis/networking/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/opendatahub-io/odh-model-controller/controllers/constants"
	"github.com/opendatahub-io/odh-model-controller/controllers/utils"
	//+kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

// +kubebuilder:docs-gen:collapse=Imports

var (
	cli        client.Client
	envTest    *envtest.Environment
	ctx        context.Context
	cancel     context.CancelFunc
	Namespaces NamespaceHolder
)

const (
	WorkingNamespace                = "default"
	MonitoringNS                    = "monitoring-ns"
	RoleBindingPath                 = "./testdata/results/model-server-ns-role.yaml"
	ServingRuntimePath1             = "./testdata/deploy/test-openvino-serving-runtime-1.yaml"
	KserveServingRuntimePath1       = "./testdata/deploy/kserve-openvino-serving-runtime-1.yaml"
	ServingRuntimePath2             = "./testdata/deploy/test-openvino-serving-runtime-2.yaml"
	InferenceService1               = "./testdata/deploy/openvino-inference-service-1.yaml"
	InferenceServiceNoRuntime       = "./testdata/deploy/openvino-inference-service-no-runtime.yaml"
	KserveInferenceServicePath1     = "./testdata/deploy/kserve-openvino-inference-service-1.yaml"
	InferenceServiceConfigPath1     = "./testdata/configmaps/inferenceservice-config.yaml"
	ExpectedRoutePath               = "./testdata/results/example-onnx-mnist-route.yaml"
	ExpectedRouteNoRuntimePath      = "./testdata/results/example-onnx-mnist-no-runtime-route.yaml"
	DSCIWithAuthorization           = "./testdata/dsci-with-authorino-enabled.yaml"
	DSCIWithoutAuthorization        = "./testdata/dsci-with-authorino-missing.yaml"
	KServeAuthorizationPolicy       = "./testdata/kserve-authorization-policy.yaml"
	odhtrustedcabundleConfigMapPath = "./testdata/configmaps/odh-trusted-ca-bundle-configmap.yaml"
	timeout                         = time.Second * 20
	interval                        = time.Millisecond * 10
)

func init() {
	rand.Seed(time.Now().UnixNano())
	Namespaces = NamespaceHolder{}
}

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "Controller & Webhook Suite")
}

var _ = BeforeSuite(func() {
	ctx, cancel = context.WithCancel(context.TODO())

	// Initialize logger
	opts := zap.Options{
		Development: true,
		TimeEncoder: zapcore.TimeEncoderOfLayout(time.RFC3339),
	}
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseFlagOptions(&opts)))

	// Initialize test environment:
	By("Bootstrapping test environment")
	envTest = &envtest.Environment{
		CRDInstallOptions: envtest.CRDInstallOptions{
			Paths:              []string{filepath.Join("..", "config", "crd", "external")},
			ErrorIfPathMissing: true,
			CleanUpAfterUse:    false,
		},
	}

	cfg, err := envTest.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	// Register API objects
	testScheme := runtime.NewScheme()
	utils.RegisterSchemes(testScheme)
	utilruntime.Must(authorinov1beta2.AddToScheme(testScheme))
	utilruntime.Must(istioclientv1beta1.AddToScheme(testScheme))

	// +kubebuilder:scaffold:scheme

	// Initialize Kubernetes client
	cli, err = client.New(cfg, client.Options{Scheme: testScheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(cli).NotTo(BeNil())

	// Create istio-system namespace
	istioNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:      constants.IstioNamespace,
			Namespace: constants.IstioNamespace,
		},
	}
	Expect(cli.Create(ctx, istioNamespace)).Should(Succeed())

	// Setup controller manager
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:         testScheme,
		LeaderElection: false,
		Metrics: server.Options{
			BindAddress: "0",
		},
	})

	Expect(err).NotTo(HaveOccurred())

	err = (NewOpenshiftInferenceServiceReconciler(
		mgr.GetClient(),
		mgr.GetAPIReader(),
		ctrl.Log.WithName("controllers").WithName("InferenceService-controller"),
		false)).
		SetupWithManager(mgr)
	Expect(err).ToNot(HaveOccurred())

	err = (&MonitoringReconciler{
		Client:       cli,
		Log:          ctrl.Log.WithName("controllers").WithName("monitoring-controller"),
		MonitoringNS: MonitoringNS,
	}).SetupWithManager(mgr)
	Expect(err).ToNot(HaveOccurred())

	err = (&StorageSecretReconciler{
		Client: cli,
		Log:    ctrl.Log.WithName("controllers").WithName("Storage-Secret-Controller"),
	}).SetupWithManager(mgr)
	Expect(err).ToNot(HaveOccurred())

	err = (&ModelRegistryInferenceServiceReconciler{
		client: cli,
		log:    ctrl.Log.WithName("controllers").WithName("ModelRegistry-InferenceService-Controller"),
	}).SetupWithManager(mgr)
	Expect(err).ToNot(HaveOccurred())

	err = (&KServeCustomCACertReconciler{
		Client: cli,
		Log:    ctrl.Log.WithName("controllers").WithName("KServe-Custom-CA-Bundle-ConfigMap-Controller"),
	}).SetupWithManager(mgr)
	Expect(err).ToNot(HaveOccurred())

	// Start the manager
	go func() {
		defer GinkgoRecover()
		err = mgr.Start(ctx)
		Expect(err).ToNot(HaveOccurred(), "Failed to run manager")
	}()

}, 60)

var _ = AfterSuite(func() {
	cancel()
	By("Tearing down the test environment")
	err := envTest.Stop()
	Expect(err).NotTo(HaveOccurred())
})

// Cleanup resources to not contaminate between tests
var _ = AfterEach(func() {
	cleanUp := func(namespace string, cli client.Client) {
		inNamespace := client.InNamespace(namespace)
		istioNamespace := client.InNamespace(constants.IstioNamespace)
		Expect(cli.DeleteAllOf(context.TODO(), &kservev1alpha1.ServingRuntime{}, inNamespace)).ToNot(HaveOccurred())
		Expect(cli.DeleteAllOf(context.TODO(), &kservev1beta1.InferenceService{}, inNamespace)).ToNot(HaveOccurred())
		Expect(cli.DeleteAllOf(context.TODO(), &routev1.Route{}, inNamespace)).ToNot(HaveOccurred())
		Expect(cli.DeleteAllOf(context.TODO(), &monitoringv1.ServiceMonitor{}, inNamespace)).ToNot(HaveOccurred())
		Expect(cli.DeleteAllOf(context.TODO(), &k8srbacv1.RoleBinding{}, inNamespace)).ToNot(HaveOccurred())
		Expect(cli.DeleteAllOf(context.TODO(), &corev1.Secret{}, inNamespace)).ToNot(HaveOccurred())
		Expect(cli.DeleteAllOf(context.TODO(), &authorinov1beta2.AuthConfig{}, inNamespace)).ToNot(HaveOccurred())
		Expect(cli.DeleteAllOf(context.TODO(), &corev1.ConfigMap{}, inNamespace)).ToNot(HaveOccurred())
		Expect(cli.DeleteAllOf(context.TODO(), &corev1.Service{}, inNamespace)).ToNot(HaveOccurred())
		Expect(cli.DeleteAllOf(context.TODO(), &istioclientv1beta1.Gateway{}, istioNamespace)).ToNot(HaveOccurred())
	}
	cleanUp(WorkingNamespace, cli)
	for _, ns := range Namespaces.All() {
		cleanUp(ns, cli)
	}
	Namespaces.Clear()
})

func convertToStructuredResource(path string, out runtime.Object) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	return utils.ConvertToStructuredResource(data, out)
}

func convertToUnstructuredResource(path string, out *unstructured.Unstructured) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	return utils.ConvertToUnstructuredResource(data, out)
}

type NamespaceHolder struct {
	namespaces []string
}

func (n *NamespaceHolder) Get() string {
	ns := createTestNamespaceName()
	n.namespaces = append(n.namespaces, ns)
	return ns
}

func (n *NamespaceHolder) All() []string {
	return n.namespaces
}

func (n *NamespaceHolder) Clear() {
	n.namespaces = []string{}
}

func (n *NamespaceHolder) Create(cli client.Client) *corev1.Namespace {
	testNs := Namespaces.Get()
	testNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testNs,
			Namespace: testNs,
		},
	}
	Expect(cli.Create(ctx, testNamespace)).Should(Succeed())
	return testNamespace
}

func createTestNamespaceName() string {
	n := 5
	letterRunes := []rune("abcdefghijklmnopqrstuvwxyz")

	b := make([]rune, n)
	for i := range b {
		b[i] = letterRunes[rand.Intn(len(letterRunes))]
	}
	return "test-ns-" + string(b)
}

func NewFakeClientsetWrapper(fakeClient *fake.Clientset) kubernetes.Interface {
	return fakeClient
}
