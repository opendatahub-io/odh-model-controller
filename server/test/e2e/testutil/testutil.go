//go:build e2e

package testutil

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"testing"
	"time"

	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/opendatahub-io/odh-model-controller/server/gateway"
)

const (
	PollInterval = time.Second
	PollTimeout  = 10 * time.Second
)

// Env holds shared clients and configuration for e2e tests.
type Env struct {
	ServerURL    string
	MetricsURL   string
	GatewayClass string
	Clientset    *kubernetes.Clientset
	K8sClient    client.Client
	HTTPClient   *http.Client
}

// NewEnv reads environment variables, creates Kubernetes clients, and returns
// a ready-to-use Env. It returns nil if MODEL_SERVING_API_URL is not set.
func NewEnv() (*Env, error) {
	serverURL := os.Getenv("MODEL_SERVING_API_URL")
	if serverURL == "" {
		return nil, fmt.Errorf("MODEL_SERVING_API_URL must be set")
	}

	metricsURL := os.Getenv("MODEL_SERVING_API_METRICS_URL")
	if metricsURL == "" {
		return nil, fmt.Errorf("MODEL_SERVING_API_METRICS_URL must be set when MODEL_SERVING_API_URL is set")
	}

	gatewayClass := os.Getenv("MODEL_SERVING_API_GATEWAY_CLASS")
	if gatewayClass == "" {
		gatewayClass = "openshift-default"
	}

	cfg := ctrl.GetConfigOrDie()

	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("create kubernetes clientset: %w", err)
	}

	scheme := runtime.NewScheme()
	utilruntime.Must(gatewayapiv1.Install(scheme))
	utilruntime.Must(corev1.AddToScheme(scheme))

	k8sClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("create controller-runtime client: %w", err)
	}

	httpClient := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true, //nolint:gosec // e2e tests use OCP service-cert TLS
			},
		},
	}

	return &Env{
		ServerURL:    serverURL,
		MetricsURL:   metricsURL,
		GatewayClass: gatewayClass,
		Clientset:    clientset,
		K8sClient:    k8sClient,
		HTTPClient:   httpClient,
	}, nil
}

// CreateNamespace creates a namespace with a generated name based on the given
// prefix. The namespace is deleted on test cleanup unless the test failed.
func (e *Env) CreateNamespace(t *testing.T, prefix string, labels map[string]string) string {
	t.Helper()

	if labels != nil {
		labels = maps.Clone(labels)
	} else {
		labels = make(map[string]string)
	}
	labels["env"] = "odh-test"

	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: prefix + "-",
			Labels:       labels,
		},
	}
	created, err := e.Clientset.CoreV1().Namespaces().Create(context.Background(), ns, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create namespace %s: %v", prefix, err)
	}
	name := created.Name
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("keeping namespace %s for debugging (test failed)", name)
			return
		}
		_ = e.Clientset.CoreV1().Namespaces().Delete(context.Background(), name, metav1.DeleteOptions{})
	})
	return name
}

// CreateServiceAccount creates a ServiceAccount in the given namespace.
func (e *Env) CreateServiceAccount(t *testing.T, ns, name string) {
	t.Helper()
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
		},
	}
	_, err := e.Clientset.CoreV1().ServiceAccounts(ns).Create(context.Background(), sa, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create service account %s/%s: %v", ns, name, err)
	}
	t.Cleanup(func() {
		_ = e.Clientset.CoreV1().ServiceAccounts(ns).Delete(context.Background(), name, metav1.DeleteOptions{})
	})
}

// GrantAccess creates a Role and RoleBinding in targetNS that grants the
// ServiceAccount (in saNamespace) permission to create llminferenceservices.
func (e *Env) GrantAccess(t *testing.T, targetNS, saNamespace, saName string) {
	t.Helper()

	roleName := saName + "-llm-access"
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      roleName,
			Namespace: targetNS,
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"serving.kserve.io"},
				Resources: []string{"llminferenceservices"},
				Verbs:     []string{"create"},
			},
		},
	}
	_, err := e.Clientset.RbacV1().Roles(targetNS).Create(context.Background(), role, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create role %s/%s: %v", targetNS, roleName, err)
	}
	t.Cleanup(func() {
		_ = e.Clientset.RbacV1().Roles(targetNS).Delete(context.Background(), roleName, metav1.DeleteOptions{})
	})

	bindingName := saName + "-llm-binding"
	binding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      bindingName,
			Namespace: targetNS,
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     roleName,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      saName,
				Namespace: saNamespace,
			},
		},
	}
	_, err = e.Clientset.RbacV1().RoleBindings(targetNS).Create(context.Background(), binding, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create role binding %s/%s: %v", targetNS, bindingName, err)
	}
	t.Cleanup(func() {
		_ = e.Clientset.RbacV1().RoleBindings(targetNS).Delete(context.Background(), bindingName, metav1.DeleteOptions{})
	})
}

// RequestToken creates a short-lived token for the given ServiceAccount.
func (e *Env) RequestToken(t *testing.T, ns, saName string) string {
	t.Helper()
	expSeconds := int64(3600)
	result, err := e.Clientset.CoreV1().ServiceAccounts(ns).CreateToken(
		context.Background(),
		saName,
		&authenticationv1.TokenRequest{
			Spec: authenticationv1.TokenRequestSpec{
				ExpirationSeconds: &expSeconds,
			},
		},
		metav1.CreateOptions{},
	)
	if err != nil {
		t.Fatalf("request token for %s/%s: %v", ns, saName, err)
	}
	return result.Status.Token
}

// CreateGateway creates a Gateway resource with the configured GatewayClass.
func (e *Env) CreateGateway(t *testing.T, ns, name string, listeners []gatewayapiv1.Listener, annotations map[string]string) {
	t.Helper()
	gw := &gatewayapiv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   ns,
			Annotations: annotations,
		},
		Spec: gatewayapiv1.GatewaySpec{
			GatewayClassName: gatewayapiv1.ObjectName(e.GatewayClass),
			Listeners:        listeners,
		},
	}
	if err := e.K8sClient.Create(context.Background(), gw); err != nil {
		t.Fatalf("create gateway %s/%s: %v", ns, name, err)
	}
	t.Cleanup(func() {
		_ = e.K8sClient.Delete(context.Background(), gw)
	})
}

// HTTPDo performs an HTTP request with the given method, URL, and optional
// bearer token. It returns the response and body bytes.
func (e *Env) HTTPDo(t *testing.T, method, url, token string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, url, nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := e.HTTPClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	return resp, body
}

// HTTPGet performs a GET request with an optional bearer token.
func (e *Env) HTTPGet(t *testing.T, url, token string) (*http.Response, []byte) {
	t.Helper()
	return e.HTTPDo(t, http.MethodGet, url, token)
}

// QueryGateways queries the gateway endpoint and returns the parsed response.
func (e *Env) QueryGateways(t *testing.T, namespace, token string) gateway.GatewaysResponse {
	t.Helper()
	resp, body := e.HTTPGet(t, e.ServerURL+"/api/v1/gateways?namespace="+namespace, token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", resp.StatusCode, http.StatusOK, body)
	}
	var result gateway.GatewaysResponse
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return result
}

// WaitForGateway polls the gateway endpoint until a gateway matching the
// given name and namespace appears in the response. Handles transient
// failures such as RBAC propagation delay.
func (e *Env) WaitForGateway(t *testing.T, namespace, token, gwName, gwNamespace string) gateway.GatewaysResponse {
	t.Helper()
	var lastResult gateway.GatewaysResponse
	err := wait.PollUntilContextTimeout(context.Background(), PollInterval, PollTimeout, true,
		func(ctx context.Context) (bool, error) {
			lastResult = e.QueryGateways(t, namespace, token)
			for _, gw := range lastResult.Gateways {
				if gw.Name == gwName && gw.Namespace == gwNamespace {
					return true, nil
				}
			}
			return false, nil
		})
	if err != nil {
		t.Fatalf("timed out waiting for gateway %s/%s; last response: %+v", gwNamespace, gwName, lastResult.Gateways)
	}
	return lastResult
}
