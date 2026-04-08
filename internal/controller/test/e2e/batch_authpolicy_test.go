//go:build e2e

package e2e

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"
)

const (
	saTestUser     = "test-user"
	saTestDelegate = "test-user-delegate"
)

// testFixture holds the shared per-test resources.
type testFixture struct {
	ns                string
	testUserToken     string
	testDelegateToken string
}

// setupFixture creates a namespace with an echo server, inference HTTPRoute,
// ServiceAccounts, and RBAC bindings needed by the batch AuthPolicy tests.
//
// Shared batch HTTPRoutes (/v1/batches, /v1/files) are created once in TestMain
// via batchEnv.setupSharedBatchRoutes().
//
// The inference HTTPRoute (/{ns}/echo-server/...) is unique per namespace.
func setupFixture(t *testing.T) *testFixture {
	t.Helper()

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
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// The echo server reflects request headers in the response body.
	// Verify the x-maas-user header was injected by Authorino.
	expectedUser := saIdentity(f.ns, saTestUser)
	if !strings.Contains(string(body), expectedUser) {
		t.Errorf("response body does not contain expected x-maas-user %q", expectedUser)
	}
}

// TestFilesPathAuthnOnly verifies that a request to the /v1/files batch path
// with a valid token succeeds (200) and receives the x-maas-user header injected
// by Authorino. This mirrors TestBatchPathAuthnOnly but for the /v1/files prefix.
func TestFilesPathAuthnOnly(t *testing.T) {
	t.Parallel()
	f := setupFixture(t)

	resp, body := batchEnv.gatewayGet(t, "/v1/files", f.testUserToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	expectedUser := saIdentity(f.ns, saTestUser)
	if !strings.Contains(string(body), expectedUser) {
		t.Errorf("response body does not contain expected x-maas-user %q", expectedUser)
	}
}

// TestFilesPathSpoofedHeader verifies that a request to /v1/files with a
// spoofed x-maas-user header still succeeds (200) because batch paths skip
// authorization. Mirrors TestBatchPathSpoofedHeader for the /v1/files prefix.
func TestFilesPathSpoofedHeader(t *testing.T) {
	t.Parallel()
	f := setupFixture(t)

	headers := map[string]string{
		"x-maas-user": "spoofed-user",
	}
	resp, _ := batchEnv.gatewayGet(t, "/v1/files", f.testUserToken, headers)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

// TestNoTokenReturns401 verifies that a request without an Authorization header
// is rejected with 401 by the authentication layer.
func TestNoTokenReturns401(t *testing.T) {
	t.Parallel()

	resp, _ := batchEnv.gatewayGet(t, "/v1/batches", "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

// TestInferencePathNoRBAC verifies that a request to an inference path
// with a valid token but no inference RBAC (get llminferenceservices) is
// rejected with 403.
func TestInferencePathNoRBAC(t *testing.T) {
	t.Parallel()

	ns := batchEnv.createNamespace(t, "e2e-batch", nil)
	batchEnv.deployEchoServer(t, ns)
	batchEnv.createHTTPRoute(t, ns, "echo-inference", fmt.Sprintf("/%s/echo-server", ns))

	// Create SA with no RBAC at all.
	batchEnv.createServiceAccount(t, ns, "no-rbac-user")
	token := batchEnv.requestToken(t, ns, "no-rbac-user")

	batchEnv.waitForGatewayRoute(t, fmt.Sprintf("/%s/echo-server/test", ns), token)

	path := fmt.Sprintf("/%s/echo-server/v1/chat/completions", ns)
	resp, _ := batchEnv.gatewayGet(t, path, token, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
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
	resp, _ := batchEnv.gatewayGet(t, path, f.testUserToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

// TestInferencePathDelegatedSAR verifies that a delegated request succeeds when
// the caller has `post-delegate llminferenceservices/delegate` (rule 2) and the
// forwarded user has `get llminferenceservices` (rule 1).
//
// Scenario 3 from BATCH.md: caller has post-delegate, forwarded user has get.
func TestInferencePathDelegatedSAR(t *testing.T) {
	t.Parallel()
	f := setupFixture(t)

	path := fmt.Sprintf("/%s/echo-server/v1/chat/completions", f.ns)
	headers := map[string]string{
		"x-maas-user": saIdentity(f.ns, saTestUser),
	}
	// Caller is test-user-delegate (has post-delegate), forwarded user is test-user (has get).
	resp, _ := batchEnv.gatewayGet(t, path, f.testDelegateToken, headers)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
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
	resp, _ := batchEnv.gatewayGet(t, "/v1/batches", f.testUserToken, headers)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

// TestInferencePathSpoofedNoRBAC verifies that a delegated request fails with
// 403 when the forwarded user has no RBAC (rule 1 fails: no get access).
// The caller has post-delegate (rule 2 would pass), isolating the rule 1 failure.
//
// Scenario 5 from BATCH.md: forwarded user has no RBAC.
func TestInferencePathSpoofedNoRBAC(t *testing.T) {
	t.Parallel()
	f := setupFixture(t)

	path := fmt.Sprintf("/%s/echo-server/v1/chat/completions", f.ns)
	headers := map[string]string{
		"x-maas-user": "nonexistent-user",
	}
	// Caller is test-user-delegate (has post-delegate), forwarded user has no RBAC.
	resp, _ := batchEnv.gatewayGet(t, path, f.testDelegateToken, headers)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}

// TestInferencePathDelegatedNoDelegate verifies that a delegated request fails
// with 403 when the caller lacks `post-delegate llminferenceservices/delegate`
// (rule 2 fails). The forwarded user has get access (rule 1 would pass),
// isolating the rule 2 failure. This validates that header spoofing by a
// regular user is blocked.
//
// Scenario 6 from BATCH.md: caller lacks post-delegate.
func TestInferencePathDelegatedNoDelegate(t *testing.T) {
	t.Parallel()
	f := setupFixture(t)

	path := fmt.Sprintf("/%s/echo-server/v1/chat/completions", f.ns)
	headers := map[string]string{
		"x-maas-user": saIdentity(f.ns, saTestDelegate),
	}
	// Caller is test-user (no post-delegate), forwarded user is test-user-delegate (has get).
	resp, _ := batchEnv.gatewayGet(t, path, f.testUserToken, headers)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}

// TestInferencePathDelegateVerbWrongResource verifies that a caller with
// post-delegate on llminferenceservices (without the /delegate subresource)
// does NOT pass rule 2. The SAR requires post-delegate on
// llminferenceservices/delegate specifically.
func TestInferencePathDelegateVerbWrongResource(t *testing.T) {
	t.Parallel()

	ns := batchEnv.createNamespace(t, "e2e-batch", nil)
	batchEnv.deployEchoServer(t, ns)
	batchEnv.createHTTPRoute(t, ns, "echo-inference", fmt.Sprintf("/%s/echo-server", ns))

	// Create caller SA with post-delegate on llminferenceservices (wrong resource —
	// should be llminferenceservices/delegate).
	const saCaller = "wrong-resource-caller"
	batchEnv.createServiceAccount(t, ns, saCaller)
	batchEnv.grantInferenceAccess(t, ns, ns, saCaller)
	batchEnv.grantAccess(t, ns, ns, saCaller, saCaller+"-wrong-resource", []rbacv1.PolicyRule{{
		APIGroups: []string{"serving.kserve.io"},
		Resources: []string{"llminferenceservices"},
		Verbs:     []string{"post-delegate"},
	}})
	callerToken := batchEnv.requestToken(t, ns, saCaller)

	// Create forwarded user SA with standard inference access (rule 1 passes).
	const saForwarded = "forwarded-user"
	batchEnv.createServiceAccount(t, ns, saForwarded)
	batchEnv.grantInferenceAccess(t, ns, ns, saForwarded)

	batchEnv.waitForGatewayRoute(t, fmt.Sprintf("/%s/echo-server/test", ns), callerToken)

	path := fmt.Sprintf("/%s/echo-server/v1/chat/completions", ns)
	headers := map[string]string{
		"x-maas-user": saIdentity(ns, saForwarded),
	}
	// Rule 1: forwarded-user has get → pass. Rule 2: caller has post-delegate on
	// wrong resource (llminferenceservices, not llminferenceservices/delegate) → fail.
	resp, _ := batchEnv.gatewayGet(t, path, callerToken, headers)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}

// TestInferencePathNoToken verifies that a request to an inference path
// without an Authorization header is rejected with 401.
func TestInferencePathNoToken(t *testing.T) {
	t.Parallel()
	f := setupFixture(t)

	path := fmt.Sprintf("/%s/echo-server/v1/chat/completions", f.ns)
	resp, _ := batchEnv.gatewayGet(t, path, "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

// TestDelegatedForwardedUserDelegateOnlyNoGet verifies that a delegated request
// fails when the forwarded user has post-delegate on llminferenceservices/delegate
// but lacks get on llminferenceservices. Rule 1 checks the forwarded user for get
// and should reject.
func TestDelegatedForwardedUserDelegateOnlyNoGet(t *testing.T) {
	t.Parallel()

	ns := batchEnv.createNamespace(t, "e2e-batch", nil)
	batchEnv.deployEchoServer(t, ns)
	batchEnv.createHTTPRoute(t, ns, "echo-inference", fmt.Sprintf("/%s/echo-server", ns))

	// Caller SA with both get and post-delegate (rule 2 passes).
	const saCaller = "delegate-caller"
	batchEnv.createServiceAccount(t, ns, saCaller)
	batchEnv.grantInferenceAccess(t, ns, ns, saCaller)
	batchEnv.grantDelegateAccess(t, ns, ns, saCaller)
	callerToken := batchEnv.requestToken(t, ns, saCaller)

	// Forwarded user SA with post-delegate but NO get (rule 1 fails).
	const saForwarded = "delegate-only-user"
	batchEnv.createServiceAccount(t, ns, saForwarded)
	batchEnv.grantDelegateAccess(t, ns, ns, saForwarded)

	batchEnv.waitForGatewayRoute(t, fmt.Sprintf("/%s/echo-server/test", ns), callerToken)

	path := fmt.Sprintf("/%s/echo-server/v1/chat/completions", ns)
	headers := map[string]string{
		"x-maas-user": saIdentity(ns, saForwarded),
	}
	resp, _ := batchEnv.gatewayGet(t, path, callerToken, headers)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}

// TestBatchSubpathAuthnOnly verifies that deeper batch subpaths like
// /v1/batches/batch_123 and /v1/files/file_abc/content also skip authorization
// (the when predicate uses startsWith).
func TestBatchSubpathAuthnOnly(t *testing.T) {
	t.Parallel()
	f := setupFixture(t)

	subpaths := []string{
		"/v1/batches/batch_123",
		"/v1/batches/batch_456/cancel",
		"/v1/files/file_abc",
		"/v1/files/file_abc/content",
	}
	for _, sp := range subpaths {
		t.Run(sp, func(t *testing.T) {
			resp, _ := batchEnv.gatewayGet(t, sp, f.testUserToken, nil)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("expected 200, got %d", resp.StatusCode)
			}
		})
	}
}

// TestDelegatedSelfDelegation verifies that a caller setting x-maas-user to
// their own identity requires both get (rule 1) and post-delegate (rule 2).
// test-user-delegate has both, so this should succeed.
func TestDelegatedSelfDelegation(t *testing.T) {
	t.Parallel()
	f := setupFixture(t)

	path := fmt.Sprintf("/%s/echo-server/v1/chat/completions", f.ns)
	headers := map[string]string{
		"x-maas-user": saIdentity(f.ns, saTestDelegate),
	}
	// Caller is test-user-delegate, forwarded user is also test-user-delegate.
	// Rule 1: forwarded user (test-user-delegate) has get → pass.
	// Rule 2: caller (test-user-delegate) has post-delegate → pass.
	resp, _ := batchEnv.gatewayGet(t, path, f.testDelegateToken, headers)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

// TestInferencePathNoBatchHeaders verifies that inference path responses do NOT
// include x-maas-user or x-maas-groups headers. These headers are only injected
// for batch paths (scoped by when predicates in the response section).
func TestInferencePathNoBatchHeaders(t *testing.T) {
	t.Parallel()
	f := setupFixture(t)

	path := fmt.Sprintf("/%s/echo-server/v1/chat/completions", f.ns)
	resp, body := batchEnv.gatewayGet(t, path, f.testUserToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// The echo server reflects all request headers in the response body.
	// x-maas-user and x-maas-groups should NOT appear because the response
	// header injection is scoped to batch paths only.
	bodyStr := string(body)
	if strings.Contains(bodyStr, "x-maas-user") {
		t.Errorf("inference path response should not contain x-maas-user header")
	}
	if strings.Contains(bodyStr, "x-maas-groups") {
		t.Errorf("inference path response should not contain x-maas-groups header")
	}
}
