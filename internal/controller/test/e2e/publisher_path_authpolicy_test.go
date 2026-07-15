//go:build e2e

package e2e

import (
	"fmt"
	"net/http"
	"testing"
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

// --- Cross-tenant deny ---

func TestPerParticipantPathWithHeaderDenied(t *testing.T) {
	t.Parallel()
	f := setupPublisherFixture(t)

	// Per-participant path + valid model header = routing/authorization identity
	// mismatch. deny-misrouted-model-header fires -> 403.
	path := fmt.Sprintf("/%s/echo-server/v1/chat/completions", f.ns)
	headers := map[string]string{
		"x-gateway-model-name": fmt.Sprintf("publishers/%s/models/echo-server", f.ns),
	}
	resp, _ := authEnv.gatewayGet(t, path, f.instanceUserToken, headers)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 (deny-misrouted blocks cross-tenant mismatch), got %d", resp.StatusCode)
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
