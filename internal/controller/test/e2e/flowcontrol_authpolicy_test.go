//go:build e2e

package e2e

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// TestFlowControlHeadersSA verifies that the AuthPolicy injects the correct
// flow control headers for an authenticated ServiceAccount on an inference path.
//
// Expected: fairness-id = cluster issuer, objective = SA namespace.
func TestFlowControlHeadersSA(t *testing.T) {
	t.Parallel()

	ns := batchEnv.createNamespace(t, "e2e-fc", nil)
	batchEnv.deployEchoServer(t, ns)
	batchEnv.createHTTPRoute(t, ns, "echo-inference", fmt.Sprintf("/%s/echo-server", ns))
	batchEnv.createServiceAccount(t, ns, "fc-user")
	batchEnv.grantInferenceAccess(t, ns, ns, "fc-user")
	token := batchEnv.requestToken(t, ns, "fc-user")
	batchEnv.waitForGatewayRoute(t, fmt.Sprintf("/%s/echo-server/test", ns), token)

	path := fmt.Sprintf("/%s/echo-server/v1/chat/completions", ns)
	resp, body := batchEnv.gatewayGet(t, path, token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	bodyStr := string(body)

	if !strings.Contains(bodyStr, "x-gateway-inference-fairness-id") {
		t.Error("response does not contain x-gateway-inference-fairness-id header")
	}
	if !strings.Contains(bodyStr, "https://kubernetes.default.svc") {
		t.Error("fairness-id should be the cluster issuer (https://kubernetes.default.svc)")
	}

	if !strings.Contains(bodyStr, "x-gateway-inference-objective") {
		t.Error("response does not contain x-gateway-inference-objective header")
	}
	if !strings.Contains(bodyStr, ns) {
		t.Errorf("objective should be the SA namespace %q", ns)
	}
}

// TestFlowControlHeadersCrossNamespace verifies that two ServiceAccounts from
// different namespaces get different objective values (their respective
// namespace names), ensuring tenant isolation in flow control.
func TestFlowControlHeadersCrossNamespace(t *testing.T) {
	t.Parallel()

	nsA := batchEnv.createNamespace(t, "e2e-fc", nil)
	batchEnv.deployEchoServer(t, nsA)
	batchEnv.createHTTPRoute(t, nsA, "echo-inference", fmt.Sprintf("/%s/echo-server", nsA))
	batchEnv.createServiceAccount(t, nsA, "tenant-user")
	batchEnv.grantInferenceAccess(t, nsA, nsA, "tenant-user")
	tokenA := batchEnv.requestToken(t, nsA, "tenant-user")
	batchEnv.waitForGatewayRoute(t, fmt.Sprintf("/%s/echo-server/test", nsA), tokenA)

	nsB := batchEnv.createNamespace(t, "e2e-fc", nil)
	batchEnv.createServiceAccount(t, nsB, "tenant-user")
	batchEnv.grantInferenceAccess(t, nsA, nsB, "tenant-user")
	tokenB := batchEnv.requestToken(t, nsB, "tenant-user")

	path := fmt.Sprintf("/%s/echo-server/v1/chat/completions", nsA)

	respA, bodyA := batchEnv.gatewayGet(t, path, tokenA, nil)
	if respA.StatusCode != http.StatusOK {
		t.Fatalf("tenant A: expected 200, got %d", respA.StatusCode)
	}
	if !strings.Contains(string(bodyA), nsA) {
		t.Errorf("tenant A objective should contain namespace %q", nsA)
	}

	respB, bodyB := batchEnv.gatewayGet(t, path, tokenB, nil)
	if respB.StatusCode != http.StatusOK {
		t.Fatalf("tenant B: expected 200, got %d", respB.StatusCode)
	}
	if !strings.Contains(string(bodyB), nsB) {
		t.Errorf("tenant B objective should contain namespace %q", nsB)
	}
}
