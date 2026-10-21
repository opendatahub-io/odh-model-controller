//go:build e2e_kind

/*
Copyright 2026.

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

package kind_test

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	authorinov1beta3 "github.com/kuadrant/authorino/api/v1beta3"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	istionetworking "istio.io/api/networking/v1alpha3"
	networkingv1alpha3 "istio.io/client-go/pkg/apis/networking/v1alpha3"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/resources"
)

var (
	kube       client.Client
	clientset  *kubernetes.Clientset
	gatewayURL string
	httpClient = &http.Client{Timeout: 5 * time.Second}
)

func TestAuthorinoAuthorization(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Authorino EnvoyFilter transport and group authorization on Kind")
}

var _ = BeforeSuite(func() {
	path := os.Getenv("KUBECONFIG")
	Expect(path).NotTo(BeEmpty(), "set KUBECONFIG to the dedicated Kind kubeconfig")
	configFile, err := clientcmd.LoadFromFile(path)
	Expect(err).NotTo(HaveOccurred())
	clusterName := os.Getenv("KIND_CLUSTER_NAME")
	if clusterName == "" {
		clusterName = "authconfig-operator-e2e"
	}
	Expect(configFile.CurrentContext).To(Equal("kind-"+clusterName),
		"this smoke test modifies the shared test EnvoyFilter")
	gatewayURL = os.Getenv("AUTHCONFIG_E2E_URL")
	Expect(gatewayURL).NotTo(BeEmpty(), "set AUTHCONFIG_E2E_URL to the Istio gateway URL")

	scheme := runtime.NewScheme()
	Expect(clientgoscheme.AddToScheme(scheme)).To(Succeed())
	Expect(authorinov1beta3.AddToScheme(scheme)).To(Succeed())
	Expect(networkingv1alpha3.AddToScheme(scheme)).To(Succeed())
	config, err := clientcmd.NewDefaultClientConfig(*configFile, &clientcmd.ConfigOverrides{}).ClientConfig()
	Expect(err).NotTo(HaveOccurred())
	kube, err = client.New(config, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())
	clientset, err = kubernetes.NewForConfig(config)
	Expect(err).NotTo(HaveOccurred())

	DeferCleanup(httpClient.CloseIdleConnections)
})

var _ = DescribeTable("Headless Authorino transport and group-based access",
	Serial,
	func(ctx SpecContext, protectedResource client.Object, resourcePlural string) {
		modelNamespace := createNamespace(ctx)
		otherNamespace := createNamespace(ctx)
		protectedResource.SetName("model")
		protectedResource.SetNamespace(modelNamespace.Name)

		By("creating AuthConfigs from the templates used by the controller")
		config, err := resources.NewStaticTemplateLoader().Load(ctx, constants.UserDefined, protectedResource)
		Expect(err).NotTo(HaveOccurred())
		config.Name = protectedResource.GetName()
		config.Namespace = modelNamespace.Name
		host := config.Name + "." + modelNamespace.Name + ".example.test"
		config.Spec.Hosts = []string{host}
		Expect(kube.Create(ctx, &config)).To(Succeed())

		// A second resource proves that the Role's resourceNames restriction still applies.
		otherResource := protectedResource.DeepCopyObject().(client.Object)
		otherResource.SetName("other-model")
		otherConfig, err := resources.NewStaticTemplateLoader().Load(ctx, constants.UserDefined, otherResource)
		Expect(err).NotTo(HaveOccurred())
		otherConfig.Name = otherResource.GetName()
		otherConfig.Namespace = modelNamespace.Name
		otherHost := otherConfig.Name + "." + modelNamespace.Name + ".example.test"
		otherConfig.Spec.Hosts = []string{otherHost}
		Expect(kube.Create(ctx, &otherConfig)).To(Succeed())
		Expect(kube.Create(ctx, &networkingv1alpha3.VirtualService{
			ObjectMeta: metav1.ObjectMeta{Name: "model", Namespace: modelNamespace.Name},
			Spec: istionetworking.VirtualService{
				Hosts:    []string{host, otherHost},
				Gateways: []string{"authconfig-e2e/authconfig-gateway"},
				Http: []*istionetworking.HTTPRoute{{Route: []*istionetworking.HTTPRouteDestination{{
					Destination: &istionetworking.Destination{
						Host: "echo.authconfig-e2e.svc.cluster.local",
						Port: &istionetworking.PortSelector{Number: 8080},
					},
				}}}},
			},
		})).To(Succeed())
		for _, ac := range []*authorinov1beta3.AuthConfig{&config, &otherConfig} {
			Eventually(func(g Gomega) {
				g.Expect(kube.Get(ctx, client.ObjectKeyFromObject(ac), ac)).To(Succeed())
				g.Expect(ac.Status.Ready()).To(BeTrue())
			}).WithContext(ctx).WithTimeout(time.Minute).WithPolling(time.Second).Should(Succeed())
		}

		By("issuing tokens for service accounts in different namespace groups")
		audiences := config.Spec.Authentication["kubernetes-user"].KubernetesTokenReview.Audiences
		token := createToken(ctx, modelNamespace.Name, audiences)
		otherToken := createToken(ctx, otherNamespace.Name, audiences)
		expectAuthorization(ctx, host, "", http.StatusUnauthorized)
		expectAuthorization(ctx, host, token, http.StatusForbidden)
		expectAuthorization(ctx, host, otherToken, http.StatusForbidden)

		By("granting access only through the namespace's service-account group")
		role := &rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{Name: "model-reader", Namespace: modelNamespace.Name},
			Rules: []rbacv1.PolicyRule{{
				APIGroups: []string{"serving.kserve.io"}, Resources: []string{resourcePlural},
				ResourceNames: []string{protectedResource.GetName()}, Verbs: []string{"get"},
			}},
		}
		Expect(kube.Create(ctx, role)).To(Succeed())
		binding := &rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: role.Name, Namespace: modelNamespace.Name},
			RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: role.Name},
			Subjects: []rbacv1.Subject{{
				Kind: rbacv1.GroupKind, APIGroup: rbacv1.GroupName,
				Name: "system:serviceaccounts:" + modelNamespace.Name,
			}},
		}
		Expect(kube.Create(ctx, binding)).To(Succeed())
		expectAuthorization(ctx, host, token, http.StatusOK)

		By("requiring the scoped STRICT_DNS workaround for the authorized request")
		key := client.ObjectKey{Namespace: "istio-system", Name: "strict-dns-ext-authz"}
		filter := &networkingv1alpha3.EnvoyFilter{}
		Expect(kube.Get(ctx, key, filter)).To(Succeed())
		original := filter.DeepCopy()
		restoreFilter := func(ctx context.Context) {
			if err := kube.Get(ctx, key, &networkingv1alpha3.EnvoyFilter{}); apierrors.IsNotFound(err) {
				restored := original.DeepCopy()
				restored.ResourceVersion = ""
				restored.UID = ""
				restored.CreationTimestamp = metav1.Time{}
				restored.ManagedFields = nil
				Expect(kube.Create(ctx, restored)).To(Succeed())
			} else {
				Expect(err).NotTo(HaveOccurred())
			}
		}
		DeferCleanup(restoreFilter)
		Expect(kube.Delete(ctx, filter)).To(Succeed())
		expectAuthorization(ctx, host, token, http.StatusForbidden)
		restoreFilter(ctx)
		expectAuthorization(ctx, host, token, http.StatusOK)

		By("keeping other groups and other resource names denied")
		expectAuthorization(ctx, host, otherToken, http.StatusForbidden)
		expectAuthorization(ctx, otherHost, token, http.StatusForbidden)

		By("revoking access while using the same token")
		Expect(kube.Delete(ctx, binding)).To(Succeed())
		expectAuthorization(ctx, host, token, http.StatusForbidden)
	},
	Entry("for InferenceService", &kservev1beta1.InferenceService{}, "inferenceservices", SpecTimeout(5*time.Minute)),
	Entry("for InferenceGraph", &kservev1alpha1.InferenceGraph{}, "inferencegraphs", SpecTimeout(5*time.Minute)),
)

func createNamespace(ctx context.Context) *corev1.Namespace {
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "authconfig-groups-e2e-"}}
	ExpectWithOffset(1, kube.Create(ctx, namespace)).To(Succeed())
	DeferCleanup(func(ctx SpecContext) {
		Expect(kube.Delete(ctx, namespace)).To(Succeed())
		Eventually(func() error {
			return kube.Get(ctx, client.ObjectKeyFromObject(namespace), &corev1.Namespace{})
		}).WithContext(ctx).WithTimeout(time.Minute).WithPolling(time.Second).
			Should(Satisfy(apierrors.IsNotFound))
	}, NodeTimeout(90*time.Second))
	return namespace
}

func createToken(ctx context.Context, namespace string, audiences []string) string {
	account := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "model-client", Namespace: namespace}}
	ExpectWithOffset(1, kube.Create(ctx, account)).To(Succeed())
	token, err := clientset.CoreV1().ServiceAccounts(namespace).CreateToken(ctx, account.Name,
		&authenticationv1.TokenRequest{Spec: authenticationv1.TokenRequestSpec{Audiences: audiences}},
		metav1.CreateOptions{})
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	ExpectWithOffset(1, token.Status.Token).NotTo(BeEmpty(), "TokenRequest returned an empty token")
	return token.Status.Token
}

func expectAuthorization(ctx context.Context, host, token string, expected int) {
	EventuallyWithOffset(1, func(g Gomega) {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, gatewayURL+"/v1/chat/completions",
			strings.NewReader(`{"model":"model","messages":[{"role":"user","content":"hello"}],"max_tokens":1}`))
		g.Expect(err).NotTo(HaveOccurred())
		request.Host = host
		request.Header.Set("Content-Type", "application/json")
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		response, err := httpClient.Do(request)
		g.Expect(err).NotTo(HaveOccurred())
		defer func() { _ = response.Body.Close() }()
		// The echo response can contain the bearer token; discard it without logging it.
		_, err = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(response.StatusCode).To(Equal(expected))
	}).WithContext(ctx).WithTimeout(45 * time.Second).WithPolling(time.Second).Should(Succeed())
}
