package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// AccessChecker checks whether a user token has permission to perform
// an action in a namespace.
type AccessChecker interface {
	CheckAccess(ctx context.Context, userToken, namespace string) (bool, error)
}

// Discoverer defines the interface for gateway discovery, enabling
// testing with mock implementations.
type Discoverer interface {
	Discover(ctx context.Context, userToken, targetNamespace string) ([]GatewayRef, error)
}

const discoveryTimeout = 5 * time.Second

// KubeDiscoverer implements Discoverer using Kubernetes API calls.
type KubeDiscoverer struct {
	SAClient             client.Client
	AccessChecker        AccessChecker
	GatewayLabelSelector map[string]string
}

// Discover performs the full gateway discovery flow:
// 1. RBAC check using the user's token
// 2. List gateways using the ServiceAccount
// 3. Filter listeners by target namespace
func (d *KubeDiscoverer) Discover(ctx context.Context, userToken, targetNamespace string) ([]GatewayRef, error) {
	ctx, cancel := context.WithTimeout(ctx, discoveryTimeout)
	defer cancel()

	allowed, err := d.AccessChecker.CheckAccess(ctx, userToken, targetNamespace)
	if err != nil {
		// Return empty list on access check errors to avoid leaking
		// information about whether the error was auth-related or not.
		slog.Error("access check failed", "error", err)
		return []GatewayRef{}, nil
	}
	if !allowed {
		return []GatewayRef{}, nil
	}

	gateways, err := d.listAllGateways(ctx)
	if err != nil {
		return nil, fmt.Errorf("list gateways: %w", err)
	}

	var nsLabels map[string]string
	if NeedsNamespaceLabels(gateways) {
		var ns corev1.Namespace
		if err := d.SAClient.Get(ctx, types.NamespacedName{Name: targetNamespace}, &ns); err != nil {
			return nil, fmt.Errorf("get namespace: %w", err)
		}
		nsLabels = ns.Labels
	}

	refs := FilterListeners(gateways, targetNamespace, nsLabels)
	if refs == nil {
		refs = []GatewayRef{}
	}
	return refs, nil
}

// listAllGateways lists all Gateway resources using the ServiceAccount client.
// No pagination (Limit/Continue) is used so the function works with both direct
// and cached (informer-backed) clients — the controller-runtime cache rejects
// Continue tokens with an error.
func (d *KubeDiscoverer) listAllGateways(ctx context.Context) ([]gatewayapiv1.Gateway, error) {
	var list gatewayapiv1.GatewayList
	var opts []client.ListOption
	if len(d.GatewayLabelSelector) > 0 {
		opts = append(opts, client.MatchingLabels(d.GatewayLabelSelector))
	}
	if err := d.SAClient.List(ctx, &list, opts...); err != nil {
		return nil, err
	}
	return list.Items, nil
}

// SelfSubjectAccessChecker performs SelfSubjectAccessReview calls using per-request
// clients that share a common HTTP transport to avoid connection pool churn.
type SelfSubjectAccessChecker struct {
	baseCfg   *rest.Config
	transport http.RoundTripper
}

// NewSelfSubjectAccessChecker creates an AccessChecker that reuses a shared
// HTTP transport for all per-request K8s clients.
func NewSelfSubjectAccessChecker(baseCfg *rest.Config) (*SelfSubjectAccessChecker, error) {
	// Create an anonymous config (no auth) to build a shared transport
	anonCfg := rest.AnonymousClientConfig(baseCfg)
	transport, err := rest.TransportFor(anonCfg)
	if err != nil {
		return nil, fmt.Errorf("create shared transport: %w", err)
	}
	return &SelfSubjectAccessChecker{
		baseCfg:   baseCfg,
		transport: transport,
	}, nil
}

func (c *SelfSubjectAccessChecker) CheckAccess(ctx context.Context, userToken, namespace string) (bool, error) {
	userCfg := rest.AnonymousClientConfig(c.baseCfg)
	// Clear TLS config since it's already handled by the shared transport.
	// Setting both Transport and TLS options causes client-go to reject
	// the config with "using a custom transport with TLS certificate options
	// is not allowed".
	userCfg.TLSClientConfig = rest.TLSClientConfig{}
	userCfg.Transport = &bearerTokenRoundTripper{
		token:    userToken,
		delegate: c.transport,
	}

	userClient, err := kubernetes.NewForConfig(userCfg)
	if err != nil {
		return false, fmt.Errorf("create user client: %w", err)
	}

	review := &authorizationv1.SelfSubjectAccessReview{
		Spec: authorizationv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authorizationv1.ResourceAttributes{
				Namespace: namespace,
				Verb:      "create",
				Group:     "serving.kserve.io",
				Resource:  "llminferenceservices",
			},
		},
	}

	result, err := userClient.AuthorizationV1().
		SelfSubjectAccessReviews().
		Create(ctx, review, metav1.CreateOptions{})
	if err != nil {
		return false, fmt.Errorf("self subject access review: %w", err)
	}

	return result.Status.Allowed, nil
}

// bearerTokenRoundTripper wraps a base RoundTripper and injects a Bearer
// token into each request's Authorization header.
type bearerTokenRoundTripper struct {
	token    string
	delegate http.RoundTripper
}

func (rt *bearerTokenRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+rt.token)
	return rt.delegate.RoundTrip(req)
}
