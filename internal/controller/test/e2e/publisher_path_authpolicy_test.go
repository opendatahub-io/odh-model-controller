//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// publisherFixture holds per-test resources for publisher-path authorization tests.
type publisherFixture struct {
	ns                string
	modelUserToken    string
	noAccessToken     string
	delegateToken     string
	instanceUserToken string
}

// setupPublisherFixture creates a namespace with an echo server, HTTPRoutes for
// publisher and per-participant paths, and ServiceAccounts with model-level and
// instance-level RBAC.
func setupPublisherFixture(t *testing.T) *publisherFixture {
	t.Helper()

	ns := authEnv.createNamespace(t, "e2e-publisher", nil)
	authEnv.deployEchoServer(t, ns)

	// Per-participant inference route (backward compat).
	authEnv.createHTTPRoute(t, ns, "echo-inference", fmt.Sprintf("/%s/echo-server", ns))
	// Publisher route: /publishers/<ns>/models/echo-server
	authEnv.createHTTPRoute(t, ns, "echo-publisher", fmt.Sprintf("/publishers/%s/models/echo-server", ns))

	// model-user: has model-level serve access on ALL models (publisher paths).
	authEnv.createServiceAccount(t, ns, "model-user")
	authEnv.grantModelAccess(t, ns, ns, "model-user")

	// no-access: authenticated but no RBAC at all.
	authEnv.createServiceAccount(t, ns, "no-access")

	// delegate-user: has model serve + serve-delegate (for delegation tests).
	authEnv.createServiceAccount(t, ns, "delegate-user")
	authEnv.grantModelAccess(t, ns, ns, "delegate-user")
	authEnv.grantModelDelegateAccess(t, ns, ns, "delegate-user")

	// instance-user: has instance-level get access only (per-participant paths).
	authEnv.createServiceAccount(t, ns, "instance-user")
	authEnv.grantInferenceAccess(t, ns, ns, "instance-user")

	modelUserToken := authEnv.requestToken(t, ns, "model-user")
	noAccessToken := authEnv.requestToken(t, ns, "no-access")
	delegateToken := authEnv.requestToken(t, ns, "delegate-user")
	instanceUserToken := authEnv.requestToken(t, ns, "instance-user")

	authEnv.waitForGatewayRoute(t, fmt.Sprintf("/%s/echo-server/test", ns), instanceUserToken)
	authEnv.waitForGatewayRoute(t, fmt.Sprintf("/publishers/%s/models/echo-server/test", ns), modelUserToken)
	t.Log("publisher fixture setup complete")

	return &publisherFixture{
		ns:                ns,
		modelUserToken:    modelUserToken,
		noAccessToken:     noAccessToken,
		delegateToken:     delegateToken,
		instanceUserToken: instanceUserToken,
	}
}

// --- Publisher-path model-access rules ---

func TestPublisherPathModelAccess(t *testing.T) {
	t.Parallel()
	f := setupPublisherFixture(t)

	path := fmt.Sprintf("/publishers/%s/models/echo-server/v1/chat/completions", f.ns)
	resp, _ := authEnv.gatewayGet(t, path, f.modelUserToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestPublisherPathNoModelRBAC(t *testing.T) {
	t.Parallel()
	f := setupPublisherFixture(t)

	path := fmt.Sprintf("/publishers/%s/models/echo-server/v1/chat/completions", f.ns)
	resp, _ := authEnv.gatewayGet(t, path, f.noAccessToken, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}

func TestPublisherPathMultiSegmentModelName(t *testing.T) {
	t.Parallel()
	f := setupPublisherFixture(t)

	// Multi-segment model name via direct publisher path. model-user has broad
	// (unscoped) serve on all models, so this should pass.
	path := fmt.Sprintf("/publishers/%s/models/echo-server/variant/v1/chat/completions", f.ns)
	resp, _ := authEnv.gatewayGet(t, path, f.modelUserToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 (broad RBAC covers multi-segment model), got %d", resp.StatusCode)
	}
}

// --- Cross-RBAC type ---

func TestModelUserOnParticipantPathNoInstanceRBAC(t *testing.T) {
	t.Parallel()
	f := setupPublisherFixture(t)

	// model-user has model RBAC but not instance RBAC. Per-participant path
	// uses inference-access which checks instance SAR -> 403.
	path := fmt.Sprintf("/%s/echo-server/v1/chat/completions", f.ns)
	resp, _ := authEnv.gatewayGet(t, path, f.modelUserToken, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}

// --- Namespace collision: ns=publishers ---

func TestNsPublishersParticipantPath(t *testing.T) {
	t.Parallel()
	f := setupPublisherFixture(t)

	// /publishers/some-model/v1/... has /v1/ not /models/ after the namespace
	// segment. Regex requires /publishers/<ns>/models/ so model-access-path
	// does NOT fire. inference-access fires -> instance SAR -> no RBAC -> 403.
	resp, _ := authEnv.gatewayGet(t, "/publishers/some-model/v1/chat/completions", f.instanceUserToken, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}

func TestNsPublishersPublisherRouteWrongNamespace(t *testing.T) {
	t.Parallel()
	f := setupPublisherFixture(t)

	// model-access-path fires but extracts namespace='publishers' which
	// doesn't match the RBAC namespace.
	path := "/publishers/publishers/models/echo-server/v1/chat/completions"
	resp, _ := authEnv.gatewayGet(t, path, f.modelUserToken, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}

// --- Per-participant path: header is inert (routing is by path) ---

func TestPerParticipantPathWithHeaderIgnored(t *testing.T) {
	t.Parallel()
	f := setupPublisherFixture(t)

	// Per-participant path (/<ns>/<name>/...) + valid model header. Gateway API
	// path-prefix precedence beats header-only routing, so the request routes by
	// path and the header is inert. The per-participant exclusion keeps
	// model-access-header from firing; inference-access authorizes via instance
	// SAR. instance-user has instance RBAC -> 200.
	path := fmt.Sprintf("/%s/echo-server/v1/chat/completions", f.ns)
	headers := map[string]string{
		"x-gateway-model-name": fmt.Sprintf("publishers/%s/models/echo-server", f.ns),
	}
	resp, _ := authEnv.gatewayGet(t, path, f.instanceUserToken, headers)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 (per-participant path, header inert, instance RBAC authorizes), got %d", resp.StatusCode)
	}
}

// --- Exclusion boundaries ---

func TestV1ModelsAuthnOnly(t *testing.T) {
	t.Parallel()
	f := setupPublisherFixture(t)

	resp, _ := authEnv.gatewayGet(t, "/v1/models", f.noAccessToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 (authn-only), got %d", resp.StatusCode)
	}
}

func TestV1PathExcludedFromInferenceAccess(t *testing.T) {
	t.Parallel()
	f := setupPublisherFixture(t)

	resp, _ := authEnv.gatewayGet(t, "/v1/chat/completions", f.noAccessToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 (authn-only, inference-access excluded from /v1/), got %d", resp.StatusCode)
	}
}

func TestHealthPathDepthGuard(t *testing.T) {
	t.Parallel()
	f := setupPublisherFixture(t)

	resp, _ := authEnv.gatewayGet(t, "/health", f.noAccessToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 (short path, patternRef guard), got %d", resp.StatusCode)
	}
}

func TestEmptyModelHeaderAuthnOnlyFallthrough(t *testing.T) {
	t.Parallel()
	f := setupPublisherFixture(t)

	// Empty model header: CEL 'header in request.headers' matches empty string,
	// but the regex doesn't match empty -> deny rule doesn't fire, no model-access
	// rule fires, authn-only.
	headers := map[string]string{
		"x-gateway-model-name": "",
	}
	resp, _ := authEnv.gatewayGet(t, "/v1/chat/completions", f.modelUserToken, headers)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 (empty header, authn-only fallthrough), got %d", resp.StatusCode)
	}
}

// --- /v1/ + model header gap ---

func TestV1PathWithModelHeaderDenied(t *testing.T) {
	t.Parallel()
	f := setupPublisherFixture(t)

	// /v1/chat/completions + valid model header: model-access-header fires and
	// runs a model SAR against the header identity. no-access has no RBAC -> 403.
	headers := map[string]string{
		"x-gateway-model-name": fmt.Sprintf("publishers/%s/models/echo-server", f.ns),
	}
	resp, _ := authEnv.gatewayGet(t, "/v1/chat/completions", f.noAccessToken, headers)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 (/v1/ + model header, no model RBAC), got %d", resp.StatusCode)
	}
}

func TestV1PathWithModelHeaderAuthorized(t *testing.T) {
	t.Parallel()
	f := setupPublisherFixture(t)

	// /v1/chat/completions + valid model header, from a user WITH model RBAC.
	// model-access-header authorizes against the header identity -> 200. This is
	// the model-based-routing path enabled by the gateway EnvoyFilter injecting
	// x-gateway-model-name from the request body.
	headers := map[string]string{
		"x-gateway-model-name": fmt.Sprintf("publishers/%s/models/echo-server", f.ns),
	}
	resp, _ := authEnv.gatewayGet(t, "/v1/chat/completions", f.modelUserToken, headers)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 (model-access-header authorizes valid model RBAC), got %d", resp.StatusCode)
	}
}

// --- /inference/v1/ model-API root (not a per-participant path) ---

func TestInferenceV1GenerateWithModelHeaderAuthorized(t *testing.T) {
	t.Parallel()
	f := setupPublisherFixture(t)

	// /inference/v1/generate is a model-API path (vLLM), not a per-participant
	// path. It has 2+ segments and does NOT start with /v1/, so the older
	// predicates would mis-extract ns=inference/name=v1 for inference-access.
	// The /inference/v1/ recognition routes it to model-access-header instead.
	headers := map[string]string{
		"x-gateway-model-name": fmt.Sprintf("publishers/%s/models/echo-server", f.ns),
	}
	resp, _ := authEnv.gatewayGet(t, "/inference/v1/generate", f.modelUserToken, headers)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 (model-access-header authorizes /inference/v1/generate), got %d", resp.StatusCode)
	}
}

func TestInferenceV1GenerateWithModelHeaderDenied(t *testing.T) {
	t.Parallel()
	f := setupPublisherFixture(t)

	// Same path with a valid header but no model RBAC -> 403. Confirms the path
	// is authorized via model-access-header (model SAR), not fenced off as a
	// per-participant path (which would run a garbage ns=inference/name=v1 SAR).
	headers := map[string]string{
		"x-gateway-model-name": fmt.Sprintf("publishers/%s/models/echo-server", f.ns),
	}
	resp, _ := authEnv.gatewayGet(t, "/inference/v1/generate", f.noAccessToken, headers)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 (/inference/v1/generate + model header, no model RBAC), got %d", resp.StatusCode)
	}
}

// --- Per-participant ops paths: adverse model header cannot escalate ---

func TestParticipantOpsPathWithModelHeaderUsesInstanceAuth(t *testing.T) {
	t.Parallel()
	f := setupPublisherFixture(t)

	// /<ns>/echo-server/scale_elastic_ep is a per-participant ops path (2+
	// segments, no /v1/ or /inference/v1/ root at position 0). An adverse client
	// sets a valid model header for a model it CAN access. Path-prefix routing
	// wins, so the header is inert and model-access-header does NOT fire (the
	// path is not a model-API root nor short). inference-access runs an instance
	// get SAR; model-user lacks instance RBAC -> 403. Escalation is closed.
	path := fmt.Sprintf("/%s/echo-server/scale_elastic_ep", f.ns)
	headers := map[string]string{
		"x-gateway-model-name": fmt.Sprintf("publishers/%s/models/echo-server", f.ns),
	}
	resp, _ := authEnv.gatewayGet(t, path, f.modelUserToken, headers)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 (ops path, header inert, model RBAC does not grant instance access), got %d", resp.StatusCode)
	}
}

func TestParticipantOpsPathInstanceUserAllowed(t *testing.T) {
	t.Parallel()
	f := setupPublisherFixture(t)

	// Same ops path + adverse model header, but from a user WITH instance RBAC.
	// inference-access authorizes via instance get SAR -> 200. Confirms the ops
	// path is governed by instance auth, not the (inert) header.
	path := fmt.Sprintf("/%s/echo-server/scale_elastic_ep", f.ns)
	headers := map[string]string{
		"x-gateway-model-name": fmt.Sprintf("publishers/%s/models/echo-server", f.ns),
	}
	resp, _ := authEnv.gatewayGet(t, path, f.instanceUserToken, headers)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 (ops path, instance RBAC authorizes), got %d", resp.StatusCode)
	}
}

// --- Root-level single-segment endpoints (/tokenize) route by header ---

func TestDirectTokenizeWithModelHeader(t *testing.T) {
	t.Parallel()
	f := setupPublisherFixture(t)

	// /tokenize is a direct single-segment model endpoint (is-short-path). With a
	// valid model header it routes by header, so model-access-header fires and
	// runs a model SAR against the header identity.
	headers := map[string]string{
		"x-gateway-model-name": fmt.Sprintf("publishers/%s/models/echo-server", f.ns),
	}

	// model-user has model RBAC -> 200.
	resp, _ := authEnv.gatewayGet(t, "/tokenize", f.modelUserToken, headers)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 (/tokenize + model header, model RBAC), got %d", resp.StatusCode)
	}

	// no-access has no model RBAC -> 403.
	resp2, _ := authEnv.gatewayGet(t, "/tokenize", f.noAccessToken, headers)
	if resp2.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 (/tokenize + model header, no model RBAC), got %d", resp2.StatusCode)
	}
}

func TestV1PathWithNonPublisherHeaderAuthnOnly(t *testing.T) {
	t.Parallel()
	f := setupPublisherFixture(t)

	// /v1/chat/completions + non-publisher header: the header doesn't match the
	// publisher format regex, so it shouldn't trigger the deny rule. But it also
	// shouldn't grant access - without BBR, /v1/ paths are authn-only regardless.
	// This test documents the current behavior (authn-only) vs the ideal (denied).
	headers := map[string]string{
		"x-gateway-model-name": "some-random-model",
	}
	resp, _ := authEnv.gatewayGet(t, "/v1/chat/completions", f.noAccessToken, headers)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 (non-publisher header on /v1/ = authn-only), got %d", resp.StatusCode)
	}
}

func TestV1PathWithCrossTenantHeaderDenied(t *testing.T) {
	t.Parallel()
	f := setupPublisherFixture(t)

	// /v1/chat/completions + valid model header pointing to a different tenant.
	// The routing layer uses the header to route to the other tenant's backend;
	// model-access-header authorizes against that SAME header identity, so a user
	// without RBAC on the other tenant's model is denied.
	headers := map[string]string{
		"x-gateway-model-name": "publishers/other-tenant/models/secret-model",
	}
	resp, _ := authEnv.gatewayGet(t, "/v1/chat/completions", f.noAccessToken, headers)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 (/v1/ + cross-tenant model header should be denied), got %d", resp.StatusCode)
	}
}

// --- ns=v1 collision: user with RBAC for garbage SAR identity ---

func TestV1NamespaceCollisionWithRBAC(t *testing.T) {
	t.Parallel()

	// Create namespace "v1" and grant get llminferenceservices for name=chat.
	// The original template would run SAR for ns=v1/name=chat on /v1/chat/completions
	// and this user would pass. The current template should NOT authorize based on
	// this garbage path extraction.
	ns := "v1"
	_, err := authEnv.clientset.CoreV1().Namespaces().Create(
		context.Background(),
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}},
		metav1.CreateOptions{},
	)
	if err != nil {
		t.Logf("namespace v1 may already exist: %v", err)
	}
	t.Cleanup(func() {
		_ = authEnv.clientset.CoreV1().Namespaces().Delete(context.Background(), ns, metav1.DeleteOptions{})
	})

	// Create SA in v1 namespace with get llminferenceservices for name=chat
	authEnv.createServiceAccount(t, ns, "v1-collision-user")
	authEnv.grantAccess(t, ns, ns, "v1-collision-user", "v1-collision-chat-access", []rbacv1.PolicyRule{{
		APIGroups:     []string{"serving.kserve.io"},
		Resources:     []string{"llminferenceservices"},
		ResourceNames: []string{"chat"},
		Verbs:         []string{"get"},
	}})

	token := authEnv.requestToken(t, ns, "v1-collision-user")

	// /v1/chat/completions without header: in the original template, inference-access
	// would fire and SAR for ns=v1/name=chat would PASS (user has that RBAC).
	// The current template should NOT let this through as an authorized request.
	resp, _ := authEnv.gatewayGet(t, "/v1/chat/completions", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 (authn-only, no header on /v1/), got %d", resp.StatusCode)
	}

	// /v1/chat/completions with model header pointing elsewhere
	headers := map[string]string{
		"x-gateway-model-name": "publishers/other-ns/models/secret-model",
	}
	resp2, _ := authEnv.gatewayGet(t, "/v1/chat/completions", token, headers)
	t.Logf("ns=v1 collision: /v1/chat/completions + cross-tenant header = %d", resp2.StatusCode)

	// With header: model-access-header authorizes against the header identity
	// (other-ns/secret-model), which the ns=v1 RBAC does not cover -> 403.
	if resp2.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 (model-access-header blocks cross-tenant even with ns=v1 RBAC), got %d", resp2.StatusCode)
	}
}

// --- Delegation on publisher paths ---

func TestDelegatedModelAccess(t *testing.T) {
	t.Parallel()
	f := setupPublisherFixture(t)

	path := fmt.Sprintf("/publishers/%s/models/echo-server/v1/chat/completions", f.ns)
	headers := map[string]string{
		"x-maas-user": saIdentity(f.ns, "model-user"),
	}
	// Caller (delegate-user) has serve-delegate, forwarded user (model-user) has serve.
	resp, _ := authEnv.gatewayGet(t, path, f.delegateToken, headers)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestSpoofedModelAccessCallerLacksDelegate(t *testing.T) {
	t.Parallel()
	f := setupPublisherFixture(t)

	path := fmt.Sprintf("/publishers/%s/models/echo-server/v1/chat/completions", f.ns)
	headers := map[string]string{
		"x-maas-user": saIdentity(f.ns, "delegate-user"),
	}
	// Caller (model-user) lacks serve-delegate, so model-access-path-delegate fails.
	resp, _ := authEnv.gatewayGet(t, path, f.modelUserToken, headers)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}

func TestDelegatedModelAccessForwardedUserNoRBAC(t *testing.T) {
	t.Parallel()
	f := setupPublisherFixture(t)

	path := fmt.Sprintf("/publishers/%s/models/echo-server/v1/chat/completions", f.ns)
	headers := map[string]string{
		"x-maas-user": "nonexistent-user",
	}
	// Caller (delegate-user) has post-delegate, but forwarded user has no model RBAC.
	resp, _ := authEnv.gatewayGet(t, path, f.delegateToken, headers)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 (forwarded user lacks model RBAC), got %d", resp.StatusCode)
	}
}

func TestDelegatedModelAccessForwardedUserDelegateOnly(t *testing.T) {
	t.Parallel()
	f := setupPublisherFixture(t)

	// Forwarded user (no-access) has no model RBAC at all. Even though the caller
	// (delegate-user) has both post and post-delegate, rule 1 checks the forwarded
	// user's model access and should reject.
	path := fmt.Sprintf("/publishers/%s/models/echo-server/v1/chat/completions", f.ns)
	headers := map[string]string{
		"x-maas-user": saIdentity(f.ns, "no-access"),
	}
	resp, _ := authEnv.gatewayGet(t, path, f.delegateToken, headers)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 (forwarded user has no model access), got %d", resp.StatusCode)
	}
}

// --- Authentication layer ---

func TestPublisherPathNoToken(t *testing.T) {
	t.Parallel()
	f := setupPublisherFixture(t)

	path := fmt.Sprintf("/publishers/%s/models/echo-server/v1/chat/completions", f.ns)
	resp, _ := authEnv.gatewayGet(t, path, "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 (no token), got %d", resp.StatusCode)
	}
}

// --- Response headers ---

func TestPublisherPathNoBatchHeaders(t *testing.T) {
	t.Parallel()
	f := setupPublisherFixture(t)

	path := fmt.Sprintf("/publishers/%s/models/echo-server/v1/chat/completions", f.ns)
	resp, body := authEnv.gatewayGet(t, path, f.modelUserToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// x-maas-user and x-maas-groups should NOT appear on publisher paths -
	// they are only injected for batch paths.
	bodyStr := string(body)
	if strings.Contains(bodyStr, "x-maas-user") {
		t.Errorf("publisher path response should not contain x-maas-user header")
	}
	if strings.Contains(bodyStr, "x-maas-groups") {
		t.Errorf("publisher path response should not contain x-maas-groups header")
	}
}
