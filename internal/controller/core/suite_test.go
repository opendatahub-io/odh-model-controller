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

package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	authorinov1beta2 "github.com/kuadrant/authorino/api/v1beta2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	routev1 "github.com/openshift/api/route/v1"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	istioclientv1beta1 "istio.io/client-go/pkg/apis/networking/v1beta1"
	istiov1beta1 "istio.io/client-go/pkg/apis/security/v1beta1"
	corev1 "k8s.io/api/core/v1"
	k8srbacv1 "k8s.io/api/rbac/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	k8sRuntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	// +kubebuilder:scaffold:imports

	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
	testutils "github.com/opendatahub-io/odh-model-controller/test/utils"
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var cfg *rest.Config
var k8sClient client.Client
var testEnv *envtest.Environment
var ctx context.Context
var cancel context.CancelFunc

const (
	WorkingNamespace                     = "default"
	odhtrustedcabundleConfigMapPath      = "./testdata/configmaps/odh-trusted-ca-bundle-configmap.yaml"
	odhKserveCustomCABundleConfigMapPath = "./testdata/configmaps/odh-kserve-custom-ca-cert-configmap.yaml"
)

func TestControllers(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx, cancel = context.WithCancel(context.TODO())

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "..", "config", "crd", "bases"),
			filepath.Join("..", "..", "..", "config", "crd", "external"),
		},
		ErrorIfCRDPathMissing: true,

		// The BinaryAssetsDirectory is only required if you want to run the tests directly
		// without call the makefile target test. If not informed it will look for the
		// default path defined in controller-runtime which is /usr/local/kubebuilder/.
		// Note that you must have the required binaries setup under the bin directory to perform
		// the tests directly. When we run make test it will be setup and used automatically.
		BinaryAssetsDirectory: filepath.Join("..", "..", "..", "bin", "k8s",
			fmt.Sprintf("1.31.0-%s-%s", runtime.GOOS, runtime.GOARCH)),
	}

	var err error
	// cfg is defined in this file globally.
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	// err = corev1.AddToScheme(scheme.Scheme)
	utils.RegisterSchemes(scheme.Scheme)
	// Expect(err).NotTo(HaveOccurred())

	// +kubebuilder:scaffold:scheme

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
		Client: client.Options{
			Cache: &client.CacheOptions{
				DisableFor: []client.Object{&istiov1beta1.AuthorizationPolicy{}},
			},
		},
		Cache: cache.Options{
			ByObject: map[client.Object]cache.ByObject{
				&corev1.Secret{}: {
					Label: labels.SelectorFromSet(labels.Set{
						"opendatahub.io/managed": "true",
					}),
				},
			},
		},
		Metrics: metricsserver.Options{BindAddress: "0"},
	})
	Expect(err).NotTo(HaveOccurred())

	err = (&SecretReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr)
	Expect(err).NotTo(HaveOccurred())

	err = (&ConfigMapReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr)
	Expect(err).NotTo(HaveOccurred())

	go func() {
		defer GinkgoRecover()
		err = mgr.Start(ctx)
		Expect(err).NotTo(HaveOccurred(), "failed to start manager")
	}()
})

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	cancel()
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})

// Cleanup resources to not contaminate between tests
var _ = AfterEach(func() {
	cleanUp := func(namespace string, cli client.Client) {
		inNamespace := client.InNamespace(namespace)
		_, meshNamespace := utils.GetIstioControlPlaneName(ctx, cli)
		istioNamespace := client.InNamespace(meshNamespace)
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
	cleanUp(WorkingNamespace, k8sClient)
	for _, ns := range testutils.Namespaces.All() {
		cleanUp(ns, k8sClient)
	}
	testutils.Namespaces.Clear()
})

func convertToStructuredResource(path string, out k8sRuntime.Object) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	return utils.ConvertToStructuredResource(data, out)
}

func waitForConfigMap(cli client.Client, namespace, configMapName string, maxTries int, delay time.Duration) (*corev1.ConfigMap, error) {
	time.Sleep(delay)

	ctx := context.Background()
	configMap := &corev1.ConfigMap{}
	for try := 1; try <= maxTries; try++ {
		err := cli.Get(ctx, client.ObjectKey{Namespace: namespace, Name: configMapName}, configMap)
		if err == nil {
			return configMap, nil
		}
		if !apierrs.IsNotFound(err) {
			return nil, fmt.Errorf("failed to get configmap %s/%s: %v", namespace, configMapName, err)
		}

		if try > maxTries {
			time.Sleep(1 * time.Second)
			return nil, err
		}
	}
	return configMap, nil
}
