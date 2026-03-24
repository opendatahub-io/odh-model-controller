//go:build e2e

package e2e

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
)

const (
	saTestUser     = "test-user"
	saTestDelegate = "test-user-delegate"
)

var batchRoutesOnce sync.Once

// setupBatchRoutes creates the shared batch namespace, echo server, and
// HTTPRoutes for /v1/batches and /v1/files. Called once across all tests.
func setupBatchRoutes(t *testing.T) {
	t.Helper()
	batchRoutesOnce.Do(func() {
		t.Log("setting up shared batch routes (one-time)...")
		ns := batchEnv.createNamespace(t, "e2e-batch-shared", nil)
		batchEnv.deployEchoServer(t, ns)
		batchEnv.createHTTPRoute(t, ns, "batch-routes-batches", "/v1/batches")
		batchEnv.createHTTPRoute(t, ns, "batch-routes-files", "/v1/files")
		// Use a temporary token just to verify routes are reachable.
		batchEnv.createServiceAccount(t, ns, "route-checker")
		token := batchEnv.requestToken(t, ns, "route-checker")
		batchEnv.waitForGatewayRoute(t, "/v1/batches", token)
		t.Log("shared batch routes ready")
	})
}

// testFixture holds the shared per-test resources.
type testFixture struct {
	ns                string
	testUserToken     string
	testDelegateToken string
}

// setupFixture creates a namespace with an echo server, inference HTTPRoute,
// ServiceAccounts, and RBAC bindings needed by the batch AuthPolicy tests.
//
// Batch HTTPRoutes (/v1/batches, /v1/files) are created once via setupBatchRoutes
// because these global paths can only have one HTTPRoute — multiple tests cannot
// each claim the same path prefix.
//
// The inference HTTPRoute (/{ns}/echo-server/...) is unique per namespace.
func setupFixture(t *testing.T) *testFixture {
	t.Helper()

	// Ensure shared batch routes exist (created once across all tests).
	setupBatchRoutes(t)

	ns := batchEnv.createNamespace(t, "e2e-batch", nil)
	batchEnv.deployEchoServer(t, ns)
	batchEnv.createHTTPRoute(t, ns, "echo-inference", fmt.Sprintf("/%s/echo-server", ns))

	batchEnv.createServiceAccount(t, ns, saTestUser)
	batchEnv.createServiceAccount(t, ns, saTestDelegate)
	batchEnv.grantInferenceAccess(t, ns, ns, saTestUser)
	batchEnv.grantInferenceAccess(t, ns, ns, saTestDelegate)
	batchEnv.grantDelegateAccess(t, ns, ns, saTestDelegate)

	testUserToken := batchEnv.requestToken(t, ns, saTestUser)
	testDelegateToken := batchEnv.requestToken(t, ns, saTestDelegate)

	batchEnv.waitForGatewayRoute(t, fmt.Sprintf("/%s/echo-server/test", ns), testUserToken)
	t.Log("fixture setup complete")

	return &testFixture{
		ns:                ns,
		testUserToken:     testUserToken,
		testDelegateToken: testDelegateToken,
	}
}

// saIdentity returns the full ServiceAccount identity string.
func saIdentity(ns, name string) string {
	return fmt.Sprintf("system:serviceaccount:%s:%s", ns, name)
}

// TestBatchPathAuthnOnly verifies that a request to a batch path (/v1/batches)
// with a valid token succeeds (200) and receives the x-maas-user header injected
// by Authorino with the caller's ServiceAccount identity.
//
// Scenario 1 from BATCH.md: batch paths skip authorization, authn-only.
func TestBatchPathAuthnOnly(t *testing.T) {
	t.Parallel()
	f := setupFixture(t)

	resp, body := batchEnv.gatewayGet(t, "/v1/batches", f.testUserToken, nil)
	if resp == nil {
		t.Fatalf("expected non-nil response")
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", resp.StatusCode, body)
	}

	// The echo server reflects request headers in the response body.
	// Verify the x-maas-user header was injected by Authorino.
	expectedUser := saIdentity(f.ns, saTestUser)
	if !strings.Contains(string(body), expectedUser) {
		t.Errorf("response body does not contain expected x-maas-user %q:\n%s", expectedUser, body)
	}
}

// TestInferencePathStandardSAR verifies that a request to an inference path
// with a token for a SA that has `get llminferenceservices` succeeds (200).
//
// Scenario 2 from BATCH.md: standard SAR on inference path.
func TestInferencePathStandardSAR(t *testing.T) {
	t.Parallel()
	f := setupFixture(t)

	path := fmt.Sprintf("/%s/echo-server/v1/chat/completions", f.ns)
	resp, body := batchEnv.gatewayGet(t, path, f.testUserToken, nil)
	if resp == nil {
		t.Fatalf("expected non-nil response")
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", resp.StatusCode, body)
	}
}

// TestInferencePathDelegatedSAR verifies that a request to an inference path
// with x-maas-user pointing to a SA that has `post llminferenceservices/delegate`
// succeeds (200).
//
// Scenario 3 from BATCH.md: delegated SAR — forwarded user has delegate RBAC.
func TestInferencePathDelegatedSAR(t *testing.T) {
	t.Parallel()
	f := setupFixture(t)

	path := fmt.Sprintf("/%s/echo-server/v1/chat/completions", f.ns)
	headers := map[string]string{
		"x-maas-user": saIdentity(f.ns, saTestDelegate),
	}
	resp, body := batchEnv.gatewayGet(t, path, f.testUserToken, headers)
	if resp == nil {
		t.Fatalf("expected non-nil response")
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", resp.StatusCode, body)
	}
}

// TestBatchPathSpoofedHeader verifies that a request to a batch path with a
// spoofed x-maas-user header still succeeds (200) because batch paths skip
// authorization entirely.
//
// Scenario 4 from BATCH.md: batch path skips authz, spoofed header ignored.
func TestBatchPathSpoofedHeader(t *testing.T) {
	t.Parallel()
	f := setupFixture(t)

	headers := map[string]string{
		"x-maas-user": "spoofed-user",
	}
	resp, body := batchEnv.gatewayGet(t, "/v1/batches", f.testUserToken, headers)
	if resp == nil {
		t.Fatalf("expected non-nil response")
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", resp.StatusCode, body)
	}
}

// TestInferencePathSpoofedNoRBAC verifies that a request to an inference path
// with x-maas-user pointing to a nonexistent user fails with 403 because the
// delegated SAR checks the forwarded user's RBAC.
//
// Scenario 5 from BATCH.md: forwarded user has no RBAC.
func TestInferencePathSpoofedNoRBAC(t *testing.T) {
	t.Parallel()
	f := setupFixture(t)

	path := fmt.Sprintf("/%s/echo-server/v1/chat/completions", f.ns)
	headers := map[string]string{
		"x-maas-user": "nonexistent-user",
	}
	resp, body := batchEnv.gatewayGet(t, path, f.testUserToken, headers)
	if resp == nil {
		t.Fatalf("expected non-nil response")
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d; body: %s", resp.StatusCode, body)
	}
}

// TestInferencePathDelegatedNoDelegate verifies that a request to an inference
// path with x-maas-user pointing to a SA that has standard inference access
// (get llminferenceservices) but NOT delegate access fails with 403.
//
// Scenario 6 from BATCH.md: forwarded user lacks delegate RBAC.
func TestInferencePathDelegatedNoDelegate(t *testing.T) {
	t.Parallel()
	f := setupFixture(t)

	path := fmt.Sprintf("/%s/echo-server/v1/chat/completions", f.ns)
	headers := map[string]string{
		"x-maas-user": saIdentity(f.ns, saTestUser),
	}
	// Use test-user-delegate's token, but forward to test-user who lacks delegate RBAC.
	resp, body := batchEnv.gatewayGet(t, path, f.testDelegateToken, headers)
	if resp == nil {
		t.Fatalf("expected non-nil response")
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d; body: %s", resp.StatusCode, body)
	}
}
