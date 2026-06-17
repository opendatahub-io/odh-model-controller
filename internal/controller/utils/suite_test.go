package utils

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"fmt"
	"path/filepath"
	osrt "runtime"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var testEnv *envtest.Environment
var testClient client.Client

func TestNIMUtils(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "NIM Utils Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "..", "config", "crd", "bases"),
			filepath.Join("..", "..", "..", "config", "crd", "external"),
		},
		BinaryAssetsDirectory: filepath.Join("..", "..", "..", "bin", "k8s",
			fmt.Sprintf("1.31.0-%s-%s", osrt.GOOS, osrt.GOARCH)),
	}

	cfg, err := testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	scheme := runtime.NewScheme()
	RegisterSchemes(scheme)

	testClient, err = client.New(cfg, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(testClient).NotTo(BeNil())
})

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})
