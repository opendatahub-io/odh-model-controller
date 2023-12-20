package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	. "github.com/onsi/ginkgo"
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
	WorkingNamespace                        = "default"
	ModelRegistryDeploymentPath             = "./test/data/model-registry/modelregistry_deployment.yaml"
	ModelRegistryDatabaseDeploymentPath     = "./test/data/model-registry/database_deployment.yaml"
	ServingRuntimePath1                     = "./test/data/deploy/kserve-openvino-serving-runtime-1.yaml"
	InferenceServiceWithModelVersionPath    = "./test/data/deploy/inference-service-with-model-version.yaml"
	InferenceServiceWithoutModelVersionPath = "./test/data/deploy/inference-service-without-model-version.yaml"
	timeout                                 = time.Second * 20
	interval                                = time.Millisecond * 50
)

var (
	scheme = runtime.NewScheme()

	ctx    context.Context
	cancel context.CancelFunc
	cli    client.Client
	kc     *k8sclient.Clientset
)

// Run e2e tests using the Ginkgo runner.
// ODH model controller should be already running in the cluster where
// these tests will run against
func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	fmt.Fprintf(GinkgoWriter, "Starting ODH Model Controller suite\n")
	RunSpecs(t, "ODH Model controller e2e suite")
}

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
