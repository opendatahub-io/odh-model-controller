//go:build e2e

package e2e

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8slabels "k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
)

const (
	pollInterval   = time.Second
	pollTimeout    = 60 * time.Second
	requestRetries = 3
	retryDelay     = 2 * time.Second

	maxResponseBodyBytes = 1 << 20 // 1 MiB

	defaultEchoServerImage = "docker.io/ealen/echo-server:0.9.2@sha256:006b92e1d6682442e29d1d0d73c34a61166d42f8a5bf1ea10c318f07ad4790e2"
)

// batchTestEnv holds shared clients and configuration for e2e tests.
type batchTestEnv struct {
	gatewayURL       string
	gatewayName      string
	gatewayNamespace string
	echoServerImage  string
	clientset        *kubernetes.Clientset
	k8sClient        client.Client
	httpClient       *http.Client
	portForwardStop  chan struct{}
	// sharedBatchNS is the namespace holding shared batch HTTPRoutes (/v1/batches, /v1/files).
	// Created during init, cleaned up in close().
	sharedBatchNS string
}

// close cleans up resources held by batchTestEnv, such as port-forward tunnels
// and shared namespaces.
func (e *batchTestEnv) close() {
	if e.sharedBatchNS != "" {
		log.Printf("deleting shared batch namespace %s", e.sharedBatchNS)
		_ = e.clientset.CoreV1().Namespaces().Delete(context.Background(), e.sharedBatchNS, metav1.DeleteOptions{})
	}
	if e.portForwardStop != nil {
		log.Println("stopping gateway port-forward tunnel")
		close(e.portForwardStop)
	}
}

// newTestEnv reads environment variables, creates Kubernetes clients, discovers
// the gateway URL from the Gateway resource status, and returns a ready-to-use batchTestEnv.
// If the gateway address is cluster-internal, a port-forward tunnel is set up
// automatically. Call close() when done to release resources.
func newTestEnv() (*batchTestEnv, error) {
	log.Println("initializing e2e test environment")

	gwName := os.Getenv("GATEWAY_NAME")
	if gwName == "" {
		gwName = constants.DefaultGatewayName
	}

	gwNamespace := os.Getenv("GATEWAY_NAMESPACE")
	if gwNamespace == "" {
		gwNamespace = constants.DefaultGatewayNamespace
	}

	echoImage := os.Getenv("ECHO_SERVER_IMAGE")
	if echoImage == "" {
		echoImage = defaultEchoServerImage
	}

	log.Printf("gateway: %s/%s, echo image: %s", gwNamespace, gwName, echoImage)

	log.Println("creating kubernetes clients...")
	cfg := ctrl.GetConfigOrDie()

	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("create kubernetes clientset: %w", err)
	}

	scheme := runtime.NewScheme()
	utilruntime.Must(gatewayapiv1.Install(scheme))
	utilruntime.Must(corev1.AddToScheme(scheme))
	utilruntime.Must(appsv1.AddToScheme(scheme))

	k8sClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("create controller-runtime client: %w", err)
	}
	log.Println("kubernetes clients ready")

	httpClient := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				MinVersion:         tls.VersionTLS12,
				InsecureSkipVerify: true, //nolint:gosec // e2e tests use self-signed certs
			},
		},
	}

	// Discover gateway address from the Gateway resource status.
	log.Printf("discovering gateway address from %s/%s status...", gwNamespace, gwName)
	gw := &gatewayapiv1.Gateway{}
	if err := k8sClient.Get(context.Background(), client.ObjectKey{
		Name:      gwName,
		Namespace: gwNamespace,
	}, gw); err != nil {
		return nil, fmt.Errorf("get gateway %s/%s: %w", gwNamespace, gwName, err)
	}

	if len(gw.Status.Addresses) == 0 {
		return nil, fmt.Errorf("gateway %s/%s has no addresses in status", gwNamespace, gwName)
	}

	gwAddr := gw.Status.Addresses[0].Value
	gwPort := gatewayListenerPort(gw)
	log.Printf("gateway address: %s, listener port: %d", gwAddr, gwPort)

	var gatewayURL string
	var portForwardStop chan struct{}

	if isInternalAddress(gwAddr) {
		log.Printf("gateway address %s is cluster-internal, setting up port-forward...", gwAddr)
		localPort, stopCh, pfErr := portForwardToGateway(cfg, clientset, gwNamespace, gwAddr, gwPort)
		if pfErr != nil {
			return nil, fmt.Errorf("gateway address %s is cluster-internal, port-forward failed: %w", gwAddr, pfErr)
		}
		portForwardStop = stopCh
		gatewayURL = fmt.Sprintf("http://localhost:%d", localPort)
		log.Printf("port-forward established: %s -> localhost:%d", gwAddr, localPort)
	} else {
		log.Printf("gateway address %s is externally reachable, using directly", gwAddr)
		if gwPort == 80 {
			gatewayURL = "http://" + gwAddr
		} else {
			gatewayURL = fmt.Sprintf("http://%s", net.JoinHostPort(gwAddr, fmt.Sprintf("%d", gwPort)))
		}
	}

	log.Printf("e2e environment ready, gateway URL: %s", gatewayURL)

	env := &batchTestEnv{
		gatewayURL:       gatewayURL,
		gatewayName:      gwName,
		gatewayNamespace: gwNamespace,
		echoServerImage:  echoImage,
		clientset:        clientset,
		k8sClient:        k8sClient,
		httpClient:       httpClient,
		portForwardStop:  portForwardStop,
	}

	if err := env.setupSharedBatchRoutes(); err != nil {
		env.close()
		return nil, fmt.Errorf("setup shared batch routes: %w", err)
	}

	return env, nil
}

// setupSharedBatchRoutes creates the shared namespace, echo server, and
// HTTPRoutes for /v1/batches and /v1/files. Called once during env init
// (not bound to any test's lifecycle).
func (e *batchTestEnv) setupSharedBatchRoutes() error {
	log.Println("setting up shared batch routes...")

	ctx := context.Background()

	// Create namespace.
	ns, err := e.clientset.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "e2e-batch-shared-",
			Labels:       map[string]string{"batchTestEnv": "odh-test"},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("create shared batch namespace: %w", err)
	}
	e.sharedBatchNS = ns.Name
	log.Printf("created shared batch namespace %s", ns.Name)

	// Deploy echo server (inline — can't use t-based helpers here).
	labels := map[string]string{"app.kubernetes.io/name": "echo-server"}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "echo-server", Namespace: ns.Name, Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To[int32](1),
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "echo-server",
						Image: e.echoServerImage,
						Ports: []corev1.ContainerPort{{ContainerPort: 8080, Protocol: corev1.ProtocolTCP}},
						Env:   []corev1.EnvVar{{Name: "PORT", Value: "8080"}},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("50m"),
								corev1.ResourceMemory: resource.MustParse("64Mi"),
							},
							Limits: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("200m"),
								corev1.ResourceMemory: resource.MustParse("128Mi"),
							},
						},
					}},
				},
			},
		},
	}
	if _, err := e.clientset.AppsV1().Deployments(ns.Name).Create(ctx, dep, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("create echo-server deployment: %w", err)
	}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "echo-server", Namespace: ns.Name},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports:    []corev1.ServicePort{{Port: 80, TargetPort: intstr.FromInt32(8080), Protocol: corev1.ProtocolTCP}},
		},
	}
	if _, err := e.clientset.CoreV1().Services(ns.Name).Create(ctx, svc, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("create echo-server service: %w", err)
	}

	log.Printf("waiting for echo server in %s to become ready...", ns.Name)
	if err := wait.PollUntilContextTimeout(ctx, pollInterval, pollTimeout, true,
		func(ctx context.Context) (bool, error) {
			d, getErr := e.clientset.AppsV1().Deployments(ns.Name).Get(ctx, "echo-server", metav1.GetOptions{})
			if getErr != nil {
				return false, nil
			}
			return d.Status.ReadyReplicas >= 1, nil
		}); err != nil {
		return fmt.Errorf("echo-server not ready in %s: %w", ns.Name, err)
	}

	// Create HTTPRoutes for batch paths.
	for _, r := range []struct{ name, path string }{
		{"batch-routes-batches", "/v1/batches"},
		{"batch-routes-files", "/v1/files"},
	} {
		if err := e.createHTTPRouteRaw(ctx, ns.Name, r.name, r.path); err != nil {
			return err
		}
	}

	// Verify route reachability with a temporary token.
	sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "route-checker", Namespace: ns.Name}}
	if _, err := e.clientset.CoreV1().ServiceAccounts(ns.Name).Create(ctx, sa, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("create route-checker SA: %w", err)
	}
	expSeconds := int64(3600)
	tokenResult, err := e.clientset.CoreV1().ServiceAccounts(ns.Name).CreateToken(ctx, "route-checker",
		&authenticationv1.TokenRequest{Spec: authenticationv1.TokenRequestSpec{ExpirationSeconds: &expSeconds}},
		metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("request route-checker token: %w", err)
	}

	log.Println("waiting for batch routes to become reachable...")
	if err := wait.PollUntilContextTimeout(ctx, pollInterval, pollTimeout, true,
		func(ctx context.Context) (bool, error) {
			resp, _, reqErr := e.gatewayDo("/v1/batches", tokenResult.Status.Token, nil)
			if reqErr != nil {
				return false, nil
			}
			return resp.StatusCode != http.StatusBadGateway && resp.StatusCode != http.StatusServiceUnavailable, nil
		}); err != nil {
		return fmt.Errorf("batch routes not reachable: %w", err)
	}

	log.Println("shared batch routes ready")
	return nil
}

// createHTTPRouteRaw creates an HTTPRoute without test helpers (for use in init).
func (e *batchTestEnv) createHTTPRouteRaw(ctx context.Context, ns, name, pathPrefix string) error {
	log.Printf("creating HTTPRoute %s/%s for path prefix %s", ns, name, pathPrefix)
	gwNS := gatewayapiv1.Namespace(e.gatewayNamespace)
	route := &gatewayapiv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: gatewayapiv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayapiv1.CommonRouteSpec{
				ParentRefs: []gatewayapiv1.ParentReference{{
					Name:      gatewayapiv1.ObjectName(e.gatewayName),
					Namespace: &gwNS,
				}},
			},
			Rules: []gatewayapiv1.HTTPRouteRule{{
				Matches: []gatewayapiv1.HTTPRouteMatch{{
					Path: &gatewayapiv1.HTTPPathMatch{
						Type:  ptr.To(gatewayapiv1.PathMatchPathPrefix),
						Value: ptr.To(pathPrefix),
					},
				}},
				BackendRefs: []gatewayapiv1.HTTPBackendRef{{
					BackendRef: gatewayapiv1.BackendRef{
						BackendObjectReference: gatewayapiv1.BackendObjectReference{
							Name: "echo-server",
							Port: ptr.To(gatewayapiv1.PortNumber(80)),
						},
					},
				}},
			}},
		},
	}
	if err := e.k8sClient.Create(ctx, route); err != nil {
		return fmt.Errorf("create HTTPRoute %s/%s: %w", ns, name, err)
	}

	// Wait for AuthPolicy enforcement.
	log.Printf("waiting for AuthPolicy to be enforced on HTTPRoute %s/%s...", ns, name)
	if err := wait.PollUntilContextTimeout(ctx, pollInterval, pollTimeout, true,
		func(ctx context.Context) (bool, error) {
			r := &gatewayapiv1.HTTPRoute{}
			if getErr := e.k8sClient.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, r); getErr != nil {
				return false, nil
			}
			for _, parent := range r.Status.RouteStatus.Parents {
				if parent.ControllerName != "kuadrant.io/policy-controller" {
					continue
				}
				for _, cond := range parent.Conditions {
					if cond.Type == "kuadrant.io/AuthPolicyAffected" && cond.Status == metav1.ConditionTrue {
						return true, nil
					}
				}
			}
			return false, nil
		}); err != nil {
		return fmt.Errorf("AuthPolicy not enforced on HTTPRoute %s/%s: %w", ns, name, err)
	}
	log.Printf("AuthPolicy enforced on HTTPRoute %s/%s", ns, name)
	return nil
}

// isInternalAddress returns true if the address is cluster-internal
// (a .svc.cluster.local hostname or a private IP address).
func isInternalAddress(addr string) bool {
	if strings.HasSuffix(addr, ".svc.cluster.local") || strings.HasSuffix(addr, ".svc") {
		return true
	}
	if ip := net.ParseIP(addr); ip != nil {
		return ip.IsPrivate() || ip.IsLinkLocalUnicast()
	}
	return false
}

// gatewayListenerPort returns the port of the first HTTP/HTTPS/TCP listener,
// defaulting to 80 if none is found.
func gatewayListenerPort(gw *gatewayapiv1.Gateway) int32 {
	for _, l := range gw.Spec.Listeners {
		switch l.Protocol {
		case gatewayapiv1.HTTPProtocolType, gatewayapiv1.HTTPSProtocolType, gatewayapiv1.TCPProtocolType:
			return int32(l.Port)
		}
	}
	return 80
}

// portForwardToGateway sets up a port-forward tunnel to a pod backing the
// gateway. It returns the local port, a stop channel (close to tear down),
// and any error.
func portForwardToGateway(cfg *rest.Config, clientset *kubernetes.Clientset, gwNS, gwAddr string, gwPort int32) (uint16, chan struct{}, error) {
	ctx := context.Background()

	// Find the Service backing the gateway.
	log.Printf("looking up service for gateway address %s in namespace %s...", gwAddr, gwNS)
	svc, err := findGatewayService(clientset, gwNS, gwAddr)
	if err != nil {
		return 0, nil, err
	}
	log.Printf("found gateway service %s/%s", svc.Namespace, svc.Name)

	// Resolve the target (container) port for port-forwarding.
	remotePort := gwPort
	for _, sp := range svc.Spec.Ports {
		if sp.Port == gwPort && sp.TargetPort.IntValue() > 0 {
			remotePort = int32(sp.TargetPort.IntValue())
			break
		}
	}
	log.Printf("resolved target port: service port %d -> container port %d", gwPort, remotePort)

	// Find a running pod matching the service selector.
	selector := k8slabels.SelectorFromSet(svc.Spec.Selector)
	log.Printf("finding running pod with selector %s...", selector.String())
	podList, err := clientset.CoreV1().Pods(svc.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector.String(),
	})
	if err != nil {
		return 0, nil, fmt.Errorf("list pods for service %s/%s: %w", svc.Namespace, svc.Name, err)
	}

	var podName string
	for _, pod := range podList.Items {
		if pod.Status.Phase == corev1.PodRunning {
			podName = pod.Name
			break
		}
	}
	if podName == "" {
		return 0, nil, fmt.Errorf("no running pod found for service %s/%s", svc.Namespace, svc.Name)
	}
	log.Printf("selected pod %s/%s for port-forward", svc.Namespace, podName)

	// Set up SPDY transport for port-forward.
	log.Printf("starting port-forward to %s/%s:%d...", svc.Namespace, podName, remotePort)
	transport, upgrader, err := spdy.RoundTripperFor(cfg)
	if err != nil {
		return 0, nil, fmt.Errorf("create SPDY round tripper: %w", err)
	}

	req := clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(svc.Namespace).
		Name(podName).
		SubResource("portforward")

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, req.URL())

	stopCh := make(chan struct{}, 1)
	readyCh := make(chan struct{})

	// Use port 0 to let the OS pick an available local port.
	fw, err := portforward.New(dialer, []string{fmt.Sprintf("0:%d", remotePort)}, stopCh, readyCh, io.Discard, io.Discard)
	if err != nil {
		return 0, nil, fmt.Errorf("create port forwarder: %w", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- fw.ForwardPorts()
	}()

	pfCtx, pfCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer pfCancel()

	select {
	case <-readyCh:
	case err := <-errCh:
		close(stopCh)
		return 0, nil, fmt.Errorf("port-forward failed: %w", err)
	case <-pfCtx.Done():
		close(stopCh)
		return 0, nil, fmt.Errorf("port-forward setup timed out")
	}

	ports, err := fw.GetPorts()
	if err != nil {
		close(stopCh)
		return 0, nil, fmt.Errorf("get forwarded ports: %w", err)
	}

	log.Printf("port-forward ready: localhost:%d -> %s/%s:%d", ports[0].Local, svc.Namespace, podName, remotePort)

	return ports[0].Local, stopCh, nil
}

// findGatewayService locates the Kubernetes Service backing the gateway.
// It first tries to parse a cluster-internal hostname, then falls back to
// matching by ClusterIP.
func findGatewayService(clientset *kubernetes.Clientset, gwNS, gwAddr string) (*corev1.Service, error) {
	ctx := context.Background()

	// Strategy 1: Parse cluster hostname (e.g., "svc-name.namespace.svc.cluster.local").
	if idx := strings.Index(gwAddr, ".svc"); idx > 0 {
		parts := strings.SplitN(gwAddr[:idx], ".", 2)
		if len(parts) == 2 {
			log.Printf("trying hostname lookup: service %s in namespace %s", parts[0], parts[1])
			svc, err := clientset.CoreV1().Services(parts[1]).Get(ctx, parts[0], metav1.GetOptions{})
			if err == nil {
				log.Printf("found service via hostname: %s/%s", svc.Namespace, svc.Name)
				return svc, nil
			}
			log.Printf("hostname lookup failed: %v, trying ClusterIP match...", err)
		}
	}

	// Strategy 2: Find Service by ClusterIP in the gateway namespace.
	log.Printf("searching for service with ClusterIP %s in namespace %s...", gwAddr, gwNS)
	svcList, err := clientset.CoreV1().Services(gwNS).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list services in %s: %w", gwNS, err)
	}
	for i := range svcList.Items {
		if svcList.Items[i].Spec.ClusterIP == gwAddr {
			log.Printf("found service via ClusterIP in %s: %s", gwNS, svcList.Items[i].Name)
			return &svcList.Items[i], nil
		}
	}

	return nil, fmt.Errorf("no service found for gateway address %s", gwAddr)
}

// createNamespace creates a namespace with a generated name based on the given
// prefix. The namespace is deleted on test cleanup unless the test failed.
func (e *batchTestEnv) createNamespace(t *testing.T, prefix string, labels map[string]string) string {
	t.Helper()

	merged := map[string]string{"batchTestEnv": "odh-test"}
	for k, v := range labels {
		merged[k] = v
	}

	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: prefix + "-",
			Labels:       merged,
		},
	}
	created, err := e.clientset.CoreV1().Namespaces().Create(context.Background(), ns, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create namespace %s: %v", prefix, err)
	}
	name := created.Name
	t.Logf("created namespace %s", name)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("keeping namespace %s for debugging (test failed)", name)
			return
		}
		t.Logf("deleting namespace %s", name)
		if delErr := e.clientset.CoreV1().Namespaces().Delete(context.Background(), name, metav1.DeleteOptions{}); delErr != nil {
			t.Logf("warning: failed to delete namespace %s: %v", name, delErr)
		}
	})
	return name
}

// createServiceAccount creates a ServiceAccount in the given namespace.
func (e *batchTestEnv) createServiceAccount(t *testing.T, ns, name string) {
	t.Helper()
	t.Logf("creating service account %s/%s", ns, name)
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
		},
	}
	_, err := e.clientset.CoreV1().ServiceAccounts(ns).Create(context.Background(), sa, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create service account %s/%s: %v", ns, name, err)
	}
	t.Cleanup(func() {
		_ = e.clientset.CoreV1().ServiceAccounts(ns).Delete(context.Background(), name, metav1.DeleteOptions{})
	})
}

// requestToken creates a short-lived token for the given ServiceAccount.
func (e *batchTestEnv) requestToken(t *testing.T, ns, saName string) string {
	t.Helper()
	t.Logf("requesting token for %s/%s", ns, saName)
	expSeconds := int64(3600)
	result, err := e.clientset.CoreV1().ServiceAccounts(ns).CreateToken(
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

// grantInferenceAccess creates a Role and RoleBinding in targetNS that grants
// the ServiceAccount permission to get llminferenceservices.
func (e *batchTestEnv) grantInferenceAccess(t *testing.T, targetNS, saNamespace, saName string) {
	t.Helper()
	e.grantAccess(t, targetNS, saNamespace, saName,
		saName+"-inference-access",
		[]rbacv1.PolicyRule{{
			APIGroups: []string{"serving.kserve.io"},
			Resources: []string{"llminferenceservices"},
			Verbs:     []string{"get"},
		}},
	)
}

// grantDelegateAccess creates a Role and RoleBinding in targetNS that grants
// the ServiceAccount permission to post-delegate llminferenceservices/delegate.
func (e *batchTestEnv) grantDelegateAccess(t *testing.T, targetNS, saNamespace, saName string) {
	t.Helper()
	e.grantAccess(t, targetNS, saNamespace, saName,
		saName+"-delegate-access",
		[]rbacv1.PolicyRule{{
			APIGroups: []string{"serving.kserve.io"},
			Resources: []string{"llminferenceservices/delegate"},
			Verbs:     []string{"post-delegate"},
		}},
	)
}

func (e *batchTestEnv) grantAccess(t *testing.T, targetNS, saNamespace, saName, roleName string, rules []rbacv1.PolicyRule) {
	t.Helper()
	t.Logf("granting RBAC %s to %s/%s in namespace %s", roleName, saNamespace, saName, targetNS)

	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      roleName,
			Namespace: targetNS,
		},
		Rules: rules,
	}
	_, err := e.clientset.RbacV1().Roles(targetNS).Create(context.Background(), role, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create role %s/%s: %v", targetNS, roleName, err)
	}
	t.Cleanup(func() {
		_ = e.clientset.RbacV1().Roles(targetNS).Delete(context.Background(), roleName, metav1.DeleteOptions{})
	})

	bindingName := roleName + "-binding"
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
		Subjects: []rbacv1.Subject{{
			Kind:      "ServiceAccount",
			Name:      saName,
			Namespace: saNamespace,
		}},
	}
	_, err = e.clientset.RbacV1().RoleBindings(targetNS).Create(context.Background(), binding, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create role binding %s/%s: %v", targetNS, bindingName, err)
	}
	t.Cleanup(func() {
		_ = e.clientset.RbacV1().RoleBindings(targetNS).Delete(context.Background(), bindingName, metav1.DeleteOptions{})
	})
}

// deployEchoServer creates an echo server Deployment and Service in the given
// namespace and waits for it to become ready.
func (e *batchTestEnv) deployEchoServer(t *testing.T, ns string) {
	t.Helper()
	t.Logf("deploying echo server in namespace %s (image: %s)", ns, e.echoServerImage)

	labels := map[string]string{"app.kubernetes.io/name": "echo-server"}

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "echo-server",
			Namespace: ns,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To[int32](1),
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "echo-server",
						Image: e.echoServerImage,
						Ports: []corev1.ContainerPort{{
							ContainerPort: 8080,
							Protocol:      corev1.ProtocolTCP,
						}},
						Env: []corev1.EnvVar{{
							Name:  "PORT",
							Value: "8080",
						}},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("50m"),
								corev1.ResourceMemory: resource.MustParse("64Mi"),
							},
							Limits: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("200m"),
								corev1.ResourceMemory: resource.MustParse("128Mi"),
							},
						},
					}},
				},
			},
		},
	}
	_, err := e.clientset.AppsV1().Deployments(ns).Create(context.Background(), dep, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create echo-server deployment in %s: %v", ns, err)
	}
	t.Cleanup(func() {
		_ = e.clientset.AppsV1().Deployments(ns).Delete(context.Background(), "echo-server", metav1.DeleteOptions{})
	})

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "echo-server",
			Namespace: ns,
		},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports: []corev1.ServicePort{{
				Port:       80,
				TargetPort: intstr.FromInt32(8080),
				Protocol:   corev1.ProtocolTCP,
			}},
		},
	}
	_, err = e.clientset.CoreV1().Services(ns).Create(context.Background(), svc, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create echo-server service in %s: %v", ns, err)
	}
	t.Cleanup(func() {
		_ = e.clientset.CoreV1().Services(ns).Delete(context.Background(), "echo-server", metav1.DeleteOptions{})
	})

	// Wait for deployment to be ready.
	t.Logf("waiting for echo server deployment in %s to become ready...", ns)
	err = wait.PollUntilContextTimeout(context.Background(), pollInterval, pollTimeout, true,
		func(ctx context.Context) (bool, error) {
			d, getErr := e.clientset.AppsV1().Deployments(ns).Get(ctx, "echo-server", metav1.GetOptions{})
			if getErr != nil {
				return false, nil
			}
			return d.Status.ReadyReplicas >= 1, nil
		})
	if err != nil {
		t.Fatalf("echo-server deployment in %s not ready: %v", ns, err)
	}
	t.Logf("echo server in %s is ready", ns)
}

// createHTTPRoute creates an HTTPRoute pointing to the echo-server Service.
func (e *batchTestEnv) createHTTPRoute(t *testing.T, ns, name, pathPrefix string) {
	t.Helper()
	t.Logf("creating HTTPRoute %s/%s for path prefix %s", ns, name, pathPrefix)

	gwNS := gatewayapiv1.Namespace(e.gatewayNamespace)
	route := &gatewayapiv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
		},
		Spec: gatewayapiv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayapiv1.CommonRouteSpec{
				ParentRefs: []gatewayapiv1.ParentReference{{
					Name:      gatewayapiv1.ObjectName(e.gatewayName),
					Namespace: &gwNS,
				}},
			},
			Rules: []gatewayapiv1.HTTPRouteRule{{
				Matches: []gatewayapiv1.HTTPRouteMatch{{
					Path: &gatewayapiv1.HTTPPathMatch{
						Type:  ptr.To(gatewayapiv1.PathMatchPathPrefix),
						Value: ptr.To(pathPrefix),
					},
				}},
				BackendRefs: []gatewayapiv1.HTTPBackendRef{{
					BackendRef: gatewayapiv1.BackendRef{
						BackendObjectReference: gatewayapiv1.BackendObjectReference{
							Name: "echo-server",
							Port: ptr.To(gatewayapiv1.PortNumber(80)),
						},
					},
				}},
			}},
		},
	}
	if err := e.k8sClient.Create(context.Background(), route); err != nil {
		t.Fatalf("create HTTPRoute %s/%s: %v", ns, name, err)
	}
	t.Cleanup(func() {
		_ = e.k8sClient.Delete(context.Background(), route)
	})

	e.waitForAuthPolicyAffected(t, ns, name)
}

// waitForAuthPolicyAffected polls the HTTPRoute status until the Kuadrant policy
// controller reports the route is affected by an AuthPolicy. This ensures the
// AuthPolicy ext-authz filter is active before tests send requests.
func (e *batchTestEnv) waitForAuthPolicyAffected(t *testing.T, ns, name string) {
	t.Helper()
	t.Logf("waiting for AuthPolicy to be enforced on HTTPRoute %s/%s...", ns, name)

	var lastRoute *gatewayapiv1.HTTPRoute
	err := wait.PollUntilContextTimeout(context.Background(), pollInterval, pollTimeout, true,
		func(ctx context.Context) (bool, error) {
			lastRoute = &gatewayapiv1.HTTPRoute{}
			if getErr := e.k8sClient.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, lastRoute); getErr != nil {
				return false, nil
			}
			for _, parent := range lastRoute.Status.RouteStatus.Parents {
				if parent.ControllerName != "kuadrant.io/policy-controller" {
					continue
				}
				for _, cond := range parent.Conditions {
					if cond.Type == "kuadrant.io/AuthPolicyAffected" && cond.Status == metav1.ConditionTrue {
						return true, nil
					}
				}
			}
			return false, nil
		})
	if err != nil {
		var statusSummary string
		if lastRoute != nil && len(lastRoute.Status.RouteStatus.Parents) > 0 {
			for _, parent := range lastRoute.Status.RouteStatus.Parents {
				statusSummary += fmt.Sprintf("\n  controller=%s:", parent.ControllerName)
				for _, cond := range parent.Conditions {
					statusSummary += fmt.Sprintf("\n    %s=%s (reason=%s, message=%q)", cond.Type, cond.Status, cond.Reason, cond.Message)
				}
			}
		} else {
			statusSummary = "\n  no parent status reported"
		}
		t.Fatalf("timed out waiting for AuthPolicy to be enforced on HTTPRoute %s/%s; current parent status:%s", ns, name, statusSummary)
	}
	t.Logf("AuthPolicy enforced on HTTPRoute %s/%s", ns, name)
}

// gatewayDo performs a single HTTP GET through the gateway. Returns the response,
// body, and any error. Callers are responsible for handling errors.
func (e *batchTestEnv) gatewayDo(path, token string, extraHeaders map[string]string) (*http.Response, []byte, error) {
	url := e.gatewayURL + path
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("create request: %w", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("GET %s: %w", url, err)
	}

	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes+1))
	if err != nil {
		return nil, nil, fmt.Errorf("read response body: %w", err)
	}
	if len(body) > maxResponseBodyBytes {
		return nil, nil, fmt.Errorf("response body exceeds %d bytes", maxResponseBodyBytes)
	}
	return resp, body, nil
}

// gatewayGet performs an HTTP GET through the gateway with bearer token and
// optional extra headers. Retries on transient errors (502, 503, connection
// failures) up to requestRetries times. Returns the response and body bytes.
func (e *batchTestEnv) gatewayGet(t *testing.T, path, token string, extraHeaders map[string]string) (*http.Response, []byte) {
	t.Helper()

	var lastResp *http.Response
	var lastBody []byte
	var lastErr error

	for attempt := range requestRetries {
		lastResp, lastBody, lastErr = e.gatewayDo(path, token, extraHeaders)
		if lastErr != nil {
			t.Logf("GET %s attempt %d/%d failed: %v", e.gatewayURL+path, attempt+1, requestRetries, lastErr)
			time.Sleep(retryDelay)
			continue
		}

		// Retry on transient gateway errors.
		if lastResp.StatusCode == http.StatusBadGateway || lastResp.StatusCode == http.StatusServiceUnavailable {
			t.Logf("GET %s attempt %d/%d returned %d, retrying", e.gatewayURL+path, attempt+1, requestRetries, lastResp.StatusCode)
			time.Sleep(retryDelay)
			continue
		}

		return lastResp, lastBody
	}

	if lastErr != nil {
		t.Fatalf("GET %s failed after %d attempts: %v", e.gatewayURL+path, requestRetries, lastErr)
	}
	// All retries returned transient status codes — return the last response.
	return lastResp, lastBody
}

// waitForGatewayRoute polls the gateway until the given path returns a non-502/503
// response, indicating the HTTPRoute has been accepted and the backend is reachable.
func (e *batchTestEnv) waitForGatewayRoute(t *testing.T, path, token string) {
	t.Helper()
	t.Logf("waiting for gateway route %s to become reachable...", path)
	err := wait.PollUntilContextTimeout(context.Background(), pollInterval, pollTimeout, true,
		func(ctx context.Context) (bool, error) {
			resp, _, reqErr := e.gatewayDo(path, token, nil)
			if reqErr != nil {
				return false, nil // transient — keep polling
			}
			return resp.StatusCode != http.StatusBadGateway && resp.StatusCode != http.StatusServiceUnavailable, nil
		})
	if err != nil {
		t.Fatalf("timed out waiting for gateway route %s to become ready", path)
	}
	t.Logf("gateway route %s is reachable", path)
}
