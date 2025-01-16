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

package e2e

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	routev1 "github.com/openshift/api/route/v1"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	virtualservicev1 "istio.io/client-go/pkg/apis/networking/v1alpha3"
	istiosecurityv1beta1 "istio.io/client-go/pkg/apis/security/v1beta1"
	telemetryv1alpha1 "istio.io/client-go/pkg/apis/telemetry/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	k8sclient "k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	maistrav1 "maistra.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	clientconfig "sigs.k8s.io/controller-runtime/pkg/client/config"
)

const (
	KubectlCmdEnv                              = "KUBECTL"
	WorkingNamespace                           = "default"
	ModelRegistryDeploymentPath                = "./test/data/model-registry/modelregistry_deployment.yaml"
	ModelRegistryDatabaseDeploymentPath        = "./test/data/model-registry/database_deployment.yaml"
	ServingRuntimePath1                        = "./test/data/deploy/kserve-openvino-serving-runtime-1.yaml"
	InferenceServiceWithModelVersionPath       = "./test/data/deploy/inference-service-with-model-version.yaml"
	InferenceServiceWithoutModelVersionPath    = "./test/data/deploy/inference-service-without-model-version.yaml"
	InferenceServiceWithoutRegisteredModelPath = "./test/data/deploy/inference-service-without-registered-model.yaml"
	InferenceServiceWithInfServiceIdPath       = "./test/data/deploy/inference-service-to-delete.yaml"
	timeout                                    = time.Second * 30
	interval                                   = time.Millisecond * 50
)

var (
	scheme  = runtime.NewScheme()
	kubectl = "kubectl"

	ctx    context.Context
	cancel context.CancelFunc
	cli    client.Client
	kc     *k8sclient.Clientset
)

var (
	// Optional Environment Variables:
	// - PROMETHEUS_INSTALL_SKIP=true: Skips Prometheus Operator installation during test setup.
	// - CERT_MANAGER_INSTALL_SKIP=true: Skips CertManager installation during test setup.
	// These variables are useful if Prometheus or CertManager is already installed, avoiding
	// re-installation and conflicts.
	// skipPrometheusInstall  = os.Getenv("PROMETHEUS_INSTALL_SKIP") == "true"
	// skipCertManagerInstall = os.Getenv("CERT_MANAGER_INSTALL_SKIP") == "true"
	// isPrometheusOperatorAlreadyInstalled will be set true when prometheus CRDs be found on the cluster
	// isPrometheusOperatorAlreadyInstalled = false
	// isCertManagerAlreadyInstalled will be set true when CertManager CRDs be found on the cluster
	// isCertManagerAlreadyInstalled = false

	// projectImage is the name of the image which will be build and loaded
	// with the code source changes to be tested.
	projectImage = "example.com/odh-model-controller:v0.0.1"
)

// TestE2E runs the end-to-end (e2e) test suite for the project. These tests execute in an isolated,
// temporary environment to validate project changes with the the purposed to be used in CI jobs.
// The default setup requires Kind, builds/loads the Manager Docker image locally, and installs
// CertManager and Prometheus.
func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	_, _ = fmt.Fprintf(GinkgoWriter, "Starting odh-model-controller integration test suite\n")
	RunSpecs(t, "e2e suite")
}

// The following code is scaffolded code by kubebuilder. It is commented out, as MR
// provided custom code. We should try to restore it, if we ever have controller E2Es here.
// var _ = BeforeSuite(func() {
//	By("Ensure that Prometheus is enabled")
//	_ = utils.UncommentCode("config/default/kustomization.yaml", "#- ../prometheus", "#")
//
//	By("generating files")
//	cmd := exec.Command("make", "generate")
//	_, err := utils.Run(cmd)
//	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to run make generate")
//
//	By("generating manifests")
//	cmd = exec.Command("make", "manifests")
//	_, err = utils.Run(cmd)
//	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to run make manifests")
//
//	By("building the manager(Operator) image")
//	cmd = exec.Command("make", "docker-build", fmt.Sprintf("IMG=%s", projectImage))
//	_, err = utils.Run(cmd)
//	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to build the manager(Operator) image")
//
//	// TODO(user): If you want to change the e2e test vendor from Kind, ensure the image is
//	// built and available before running the tests. Also, remove the following block.
//	By("loading the manager(Operator) image on Kind")
//	err = utils.LoadImageToKindClusterWithName(projectImage)
//	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to load the manager(Operator) image into Kind")
//
//	// The tests-e2e are intended to run on a temporary cluster that is created and destroyed for testing.
//	// To prevent errors when tests run in environments with Prometheus or CertManager already installed,
//	// we check for their presence before execution.
//	// Setup Prometheus and CertManager before the suite if not skipped and if not already installed
//	if !skipPrometheusInstall {
//		By("checking if prometheus is installed already")
//		isPrometheusOperatorAlreadyInstalled = utils.IsPrometheusCRDsInstalled()
//		if !isPrometheusOperatorAlreadyInstalled {
//			_, _ = fmt.Fprintf(GinkgoWriter, "Installing Prometheus Operator...\n")
//			Expect(utils.InstallPrometheusOperator()).To(Succeed(), "Failed to install Prometheus Operator")
//		} else {
//			_, _ = fmt.Fprintf(GinkgoWriter, "WARNING: Prometheus Operator is already installed. Skipping installation...\n")
//		}
//	}
//	if !skipCertManagerInstall {
//		By("checking if cert manager is installed already")
//		isCertManagerAlreadyInstalled = utils.IsCertManagerCRDsInstalled()
//		if !isCertManagerAlreadyInstalled {
//			_, _ = fmt.Fprintf(GinkgoWriter, "Installing CertManager...\n")
//			Expect(utils.InstallCertManager()).To(Succeed(), "Failed to install CertManager")
//		} else {
//			_, _ = fmt.Fprintf(GinkgoWriter, "WARNING: CertManager is already installed. Skipping installation...\n")
//		}
//	}
// })
//
// var _ = AfterSuite(func() {
//	// Teardown Prometheus and CertManager after the suite if not skipped and if they were not already installed
//	if !skipPrometheusInstall && !isPrometheusOperatorAlreadyInstalled {
//		_, _ = fmt.Fprintf(GinkgoWriter, "Uninstalling Prometheus Operator...\n")
//		utils.UninstallPrometheusOperator()
//	}
//	if !skipCertManagerInstall && !isCertManagerAlreadyInstalled {
//		_, _ = fmt.Fprintf(GinkgoWriter, "Uninstalling CertManager...\n")
//		utils.UninstallCertManager()
//	}
// })

var _ = BeforeSuite(func() {
	var err error
	ctx, cancel = context.WithCancel(context.TODO())
	// GetConfig(): If KUBECONFIG env variable is set, it is used to create
	// the client, else the inClusterConfig() is used.
	// Lastly if none of them are set, it uses  $HOME/.kube/config to create the client.
	config, err := clientconfig.GetConfig()
	Expect(err).NotTo(HaveOccurred())
	Expect(config).NotTo(BeNil())
	kc, err = k8sclient.NewForConfig(config)
	Expect(err).NotTo(HaveOccurred())
	Expect(kc).NotTo(BeNil())
	// Custom client to manages resources
	cli, err = client.New(config, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(cli).NotTo(BeNil())
	// Override kubectl cmd
	cmd, ok := os.LookupEnv(KubectlCmdEnv)
	if ok && cmd != "" {
		kubectl = cmd
	}
	// Register API objects
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(kservev1alpha1.AddToScheme(scheme))
	utilruntime.Must(kservev1beta1.AddToScheme(scheme))
	utilruntime.Must(routev1.AddToScheme(scheme))
	utilruntime.Must(virtualservicev1.AddToScheme(scheme))
	utilruntime.Must(maistrav1.AddToScheme(scheme))
	utilruntime.Must(monitoringv1.AddToScheme(scheme))
	utilruntime.Must(corev1.AddToScheme(scheme))
	utilruntime.Must(istiosecurityv1beta1.AddToScheme(scheme))
	utilruntime.Must(telemetryv1alpha1.AddToScheme(scheme))
})

// Cleanup resources to not contaminate between tests
var _ = AfterEach(func() {
	inNamespace := client.InNamespace(WorkingNamespace)
	Expect(cli.DeleteAllOf(context.TODO(), &kservev1alpha1.ServingRuntime{}, inNamespace)).ToNot(HaveOccurred())
	Expect(cli.DeleteAllOf(context.TODO(), &kservev1beta1.InferenceService{}, inNamespace)).ToNot(HaveOccurred())
	Expect(cli.DeleteAllOf(context.TODO(), &routev1.Route{}, inNamespace)).ToNot(HaveOccurred())
	Expect(cli.DeleteAllOf(context.TODO(), &monitoringv1.ServiceMonitor{}, inNamespace)).ToNot(HaveOccurred())
	// Expect(cli.DeleteAllOf(context.TODO(), &k8srbacv1.RoleBinding{}, inNamespace)).ToNot(HaveOccurred())
	Expect(cli.DeleteAllOf(context.TODO(), &corev1.Secret{}, inNamespace)).ToNot(HaveOccurred())
})
var _ = AfterSuite(func() {
	// TODO: remove all resources created in the current cluster
})
