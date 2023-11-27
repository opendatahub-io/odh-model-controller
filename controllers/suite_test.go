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
	"os"
	"path/filepath"
	"testing"
	"time"

	"sigs.k8s.io/yaml"

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"github.com/opendatahub-io/odh-model-controller/controllers/reconcilers"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"go.uber.org/zap/zapcore"
	k8srbacv1 "k8s.io/api/rbac/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrl "sigs.k8s.io/controller-runtime"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	routev1 "github.com/openshift/api/route/v1"
	virtualservicev1 "istio.io/client-go/pkg/apis/networking/v1alpha3"
	istiosecurityv1beta1 "istio.io/client-go/pkg/apis/security/v1beta1"
	telemetryv1alpha1 "istio.io/client-go/pkg/apis/telemetry/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	maistrav1 "maistra.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	//+kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

// +kubebuilder:docs-gen:collapse=Imports

var (
	cli     client.Client
	envTest *envtest.Environment
	ctx     context.Context
	cancel  context.CancelFunc
)

const (
	WorkingNamespace            = "default"
	MonitoringNS                = "monitoring-ns"
	RoleBindingPath             = "./testdata/results/model-server-ns-role.yaml"
	ServingRuntimePath1         = "./testdata/deploy/test-openvino-serving-runtime-1.yaml"
	KserveServingRuntimePath1   = "./testdata/deploy/kserve-openvino-serving-runtime-1.yaml"
	ServingRuntimePath2         = "./testdata/deploy/test-openvino-serving-runtime-2.yaml"
	InferenceService1           = "./testdata/deploy/openvino-inference-service-1.yaml"
	InferenceServiceNoRuntime   = "./testdata/deploy/openvino-inference-service-no-runtime.yaml"
	KserveInferenceServicePath1 = "./testdata/deploy/kserve-openvino-inference-service-1.yaml"
	InferenceServiceConfigPath1 = "./testdata/configmaps/inferenceservice-config.yaml"
	ExpectedRoutePath           = "./testdata/results/example-onnx-mnist-route.yaml"
	ExpectedRouteNoRuntimePath  = "./testdata/results/example-onnx-mnist-no-runtime-route.yaml"
	timeout                     = time.Second * 20
	interval                    = time.Millisecond * 10
)

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
	utilruntime.Must(clientgoscheme.AddToScheme(scheme.Scheme))
	utilruntime.Must(kservev1alpha1.AddToScheme(scheme.Scheme))
	utilruntime.Must(kservev1beta1.AddToScheme(scheme.Scheme))
	utilruntime.Must(routev1.AddToScheme(scheme.Scheme))
	utilruntime.Must(virtualservicev1.AddToScheme(scheme.Scheme))
	utilruntime.Must(maistrav1.AddToScheme(scheme.Scheme))
	utilruntime.Must(monitoringv1.AddToScheme(scheme.Scheme))
	utilruntime.Must(corev1.AddToScheme(scheme.Scheme))
	utilruntime.Must(istiosecurityv1beta1.AddToScheme(scheme.Scheme))
	utilruntime.Must(telemetryv1alpha1.AddToScheme(scheme.Scheme))

	// +kubebuilder:scaffold:scheme

	// Initialize Kubernetes client
	cli, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(cli).NotTo(BeNil())

	// Setup controller manager
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:             scheme.Scheme,
		LeaderElection:     false,
		MetricsBindAddress: "0",
	})

	Expect(err).NotTo(HaveOccurred())

	err = (NewOpenshiftInferenceServiceReconciler(
		mgr.GetClient(),
		scheme.Scheme,
		ctrl.Log.WithName("controllers").WithName("InferenceService-controller"),
		false)).
		SetupWithManager(mgr)
	Expect(err).ToNot(HaveOccurred())

	err = (&MonitoringReconciler{
		Client:       cli,
		Log:          ctrl.Log.WithName("controllers").WithName("monitoring-controller"),
		Scheme:       scheme.Scheme,
		MonitoringNS: MonitoringNS,
	}).SetupWithManager(mgr)
	Expect(err).ToNot(HaveOccurred())

	err = (&StorageSecretReconciler{
		Client: cli,
		Log:    ctrl.Log.WithName("controllers").WithName("Storage-Secret-Controller"),
		Scheme: scheme.Scheme,
	}).SetupWithManager(mgr)
	Expect(err).ToNot(HaveOccurred())

	err = (&ModelRegistryReconciler{
		Client:         cli,
		Log:            ctrl.Log.WithName("controllers").WithName("Model-Registry-Controller"),
		Scheme:         scheme.Scheme,
		Period:         0, // no periodic checks
		mrISReconciler: reconcilers.NewModelRegistryInferenceServiceReconciler(cli),
		mrSEReconciler: reconcilers.NewModelRegistryServingEnvironmentReconciler(cli),
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
	inNamespace := client.InNamespace(WorkingNamespace)
	Expect(cli.DeleteAllOf(context.TODO(), &kservev1alpha1.ServingRuntime{}, inNamespace)).ToNot(HaveOccurred())
	Expect(cli.DeleteAllOf(context.TODO(), &kservev1beta1.InferenceService{}, inNamespace)).ToNot(HaveOccurred())
	Expect(cli.DeleteAllOf(context.TODO(), &routev1.Route{}, inNamespace)).ToNot(HaveOccurred())
	Expect(cli.DeleteAllOf(context.TODO(), &monitoringv1.ServiceMonitor{}, inNamespace)).ToNot(HaveOccurred())
	Expect(cli.DeleteAllOf(context.TODO(), &k8srbacv1.RoleBinding{}, inNamespace)).ToNot(HaveOccurred())
	Expect(cli.DeleteAllOf(context.TODO(), &corev1.Secret{}, inNamespace)).ToNot(HaveOccurred())

})

func convertToStructuredResource(path string, out interface{}) (err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	// Unmarshal the YAML data into the struct
	err = yaml.Unmarshal(data, out)
	if err != nil {
		return err
	}
	return nil
}
