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
	"errors"
	"math/rand"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	mmv1alpha1 "github.com/kserve/modelmesh-serving/apis/serving/v1alpha1"
	inferenceservicev1 "github.com/kserve/modelmesh-serving/apis/serving/v1beta1"
	mf "github.com/manifestival/manifestival"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"

	"go.uber.org/zap/zapcore"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrl "sigs.k8s.io/controller-runtime"

	predictorv1 "github.com/kserve/modelmesh-serving/apis/serving/v1alpha1"
	"github.com/manifestival/manifestival"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	routev1 "github.com/openshift/api/route/v1"
	virtualservicev1 "istio.io/client-go/pkg/apis/networking/v1alpha3"
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
	MonitoringNS                      = "monitoring-ns"
	RoleBindingPath                   = "./testdata/results/model-server-ns-role.yaml"
	NamespacePath1                    = "./testdata/deploy/test-namespace.yaml"
	NamespaceServiceMeshPath1         = "./testdata/deploy/test-namespace-servicemesh.yaml"
	ServingRuntimePath1               = "./testdata/deploy/test-openvino-serving-runtime-1.yaml"
	ServingRuntimePath2               = "./testdata/deploy/test-openvino-serving-runtime-2.yaml"
	ServingRuntimeNoRoutePath1        = "./testdata/deploy/test-openvino-serving-runtime-1-no-route.yaml"
	InferenceService1                 = "./testdata/deploy/openvino-inference-service-1.yaml"
	InferenceServiceNoRuntime         = "./testdata/deploy/openvino-inference-service-no-runtime.yaml"
	ExpectedRoutePath                 = "./testdata/results/example-onnx-mnist-route.yaml"
	ExpectedRouteNoRuntimePath        = "./testdata/results/example-onnx-mnist-no-runtime-route.yaml"
	ExpectedVirtualServiceRoutePath   = "./testdata/results/example-onnx-mnist-virtualservice-route.yaml"
	ExpectedVirtualServiceNoRoutePath = "./testdata/results/example-onnx-mnist-virtualservice-no-route.yaml"
	timeout                           = time.Second * 5
	interval                          = time.Millisecond * 10
)

func init() {
	rand.Seed(time.Now().UnixNano())
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
	utilruntime.Must(clientgoscheme.AddToScheme(scheme.Scheme))
	utilruntime.Must(predictorv1.AddToScheme(scheme.Scheme))
	utilruntime.Must(inferenceservicev1.AddToScheme(scheme.Scheme))
	utilruntime.Must(routev1.AddToScheme(scheme.Scheme))
	utilruntime.Must(virtualservicev1.AddToScheme(scheme.Scheme))
	utilruntime.Must(maistrav1.AddToScheme(scheme.Scheme))
	utilruntime.Must(monitoringv1.AddToScheme(scheme.Scheme))
	utilruntime.Must(mmv1alpha1.AddToScheme(scheme.Scheme))

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

	err = (&OpenshiftInferenceServiceReconciler{
		Client: cli,
		Log:    ctrl.Log.WithName("controllers").WithName("inferenceservice-controller"),
		Scheme: scheme.Scheme,
	}).SetupWithManager(mgr)
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

/*
Cleanup resources to not contaminate between tests

	var _ = AfterEach(func() {
		inNamespace := client.InNamespace(WorkingNamespace)
		Expect(cli.DeleteAllOf(context.TODO(), &mmv1alpha1.ServingRuntime{}, inNamespace)).ToNot(HaveOccurred())
		Expect(cli.DeleteAllOf(context.TODO(), &inferenceservicev1.InferenceService{}, inNamespace)).ToNot(HaveOccurred())
		Expect(cli.DeleteAllOf(context.TODO(), &routev1.Route{}, inNamespace)).ToNot(HaveOccurred())
		Expect(cli.DeleteAllOf(context.TODO(), &mmv1alpha1.ServingRuntime{}, inNamespace)).ToNot(HaveOccurred())
		Expect(cli.DeleteAllOf(context.TODO(), &monitoringv1.ServiceMonitor{}, inNamespace)).ToNot(HaveOccurred())
		Expect(cli.DeleteAllOf(context.TODO(), &k8srbacv1.RoleBinding{}, inNamespace)).ToNot(HaveOccurred())
		Expect(cli.DeleteAllOf(context.TODO(), &virtualservicev1.VirtualService{}, inNamespace)).ToNot(HaveOccurred())
	})
*/

func convertToStructuredResource(path string, namespace string, out interface{}, opts manifestival.Option) error {
	m, err := mf.ManifestFrom(mf.Recursive(path), opts)
	if err != nil {
		return err
	}

	transformers := []manifestival.Transformer{InjectNewNamespaceInValues(namespace), mf.InjectNamespace(namespace)}
	m, err = m.Transform(transformers...)
	if err != nil {
		return err
	}
	err = scheme.Scheme.Convert(&m.Resources()[0], out, nil)
	if err != nil {
		return err
	}
	return nil
}

var letterRunes = []rune("abcdefghijklmnopqrstuvwxyz")

func RandStringRunes(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letterRunes[rand.Intn(len(letterRunes))]
	}
	return string(b)
}

// InjectNewNamespaceInValues injects the current metadata.namespace in all strings where current namespace is found and updates the value with the new namespace.
// Should be first in the Transformer chain to detect the old used "current" namespace pattern to replace.
func InjectNewNamespaceInValues(namespace string) mf.Transformer {
	updateValue := func(val, newns, oldns string) string {
		return strings.ReplaceAll(val, oldns, newns)
	}
	var update func(obj interface{}, newns, oldns string) error
	update = func(obj interface{}, newns, oldns string) error {
		v := reflect.ValueOf(obj)
		switch v.Kind() {
		case reflect.Slice:
			if child, ok := obj.([]interface{}); ok {
				for _, c := range child {
					update(c, newns, oldns)
				}
			}
			return nil
		case reflect.Map:
			if m, ok := obj.(map[string]interface{}); ok {
				for key := range m {
					cv := m[key]
					if value, ok := cv.(string); ok {
						m[key] = updateValue(value, newns, oldns)
					} else {
						update(cv, newns, oldns)
					}
				}
			}
		}
		return nil
	}

	return func(u *unstructured.Unstructured) error {
		switch strings.ToLower(u.GetKind()) {
		case "virtualservice", "route":
			oldns, exists, err := unstructured.NestedFieldNoCopy(u.Object, "metadata", "namespace")
			if err != nil {
				return err
			}
			if !exists {
				return errors.New("Can not transform VirtualService fields without original namespace set")
			}
			return update(u.Object, namespace, oldns.(string))
		}
		return nil
	}
}
