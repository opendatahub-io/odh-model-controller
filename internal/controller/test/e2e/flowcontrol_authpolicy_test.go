//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// echoResponse captures the subset of the ealen/echo-server JSON response
// needed to inspect reflected request headers.
type echoResponse struct {
	Request struct {
		Headers map[string]json.RawMessage `json:"headers"`
	} `json:"request"`
}

// parseEchoHeader extracts a single header value from the echo server response.
// Handles both string ("value") and array (["value"]) formats.
func parseEchoHeader(t *testing.T, body []byte, header string) (string, bool) {
	t.Helper()
	var resp echoResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to parse echo server response: %v\nbody: %s", err, body)
	}
	raw, ok := resp.Request.Headers[header]
	if !ok {
		return "", false
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s, true
	}
	var arr []string
	if json.Unmarshal(raw, &arr) == nil && len(arr) > 0 {
		return arr[0], true
	}
	return "", false
}

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

	if val, ok := parseEchoHeader(t, body, "x-gateway-inference-fairness-id"); !ok {
		t.Error("x-gateway-inference-fairness-id header not injected")
	} else if val != "https://kubernetes.default.svc" {
		t.Errorf("fairness-id = %q, want %q", val, "https://kubernetes.default.svc")
	}

	if val, ok := parseEchoHeader(t, body, "x-gateway-inference-objective"); !ok {
		t.Error("x-gateway-inference-objective header not injected")
	} else if val != ns {
		t.Errorf("objective = %q, want namespace %q", val, ns)
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
	if val, ok := parseEchoHeader(t, bodyA, "x-gateway-inference-fairness-id"); !ok {
		t.Error("tenant A: x-gateway-inference-fairness-id header not injected")
	} else if val != "https://kubernetes.default.svc" {
		t.Errorf("tenant A: fairness-id = %q, want %q", val, "https://kubernetes.default.svc")
	}
	if val, ok := parseEchoHeader(t, bodyA, "x-gateway-inference-objective"); !ok {
		t.Error("tenant A: x-gateway-inference-objective header not injected")
	} else if val != nsA {
		t.Errorf("tenant A: objective = %q, want %q", val, nsA)
	}

	respB, bodyB := batchEnv.gatewayGet(t, path, tokenB, nil)
	if respB.StatusCode != http.StatusOK {
		t.Fatalf("tenant B: expected 200, got %d", respB.StatusCode)
	}
	if val, ok := parseEchoHeader(t, bodyB, "x-gateway-inference-fairness-id"); !ok {
		t.Error("tenant B: x-gateway-inference-fairness-id header not injected")
	} else if val != "https://kubernetes.default.svc" {
		t.Errorf("tenant B: fairness-id = %q, want %q", val, "https://kubernetes.default.svc")
	}
	if val, ok := parseEchoHeader(t, bodyB, "x-gateway-inference-objective"); !ok {
		t.Error("tenant B: x-gateway-inference-objective header not injected")
	} else if val != nsB {
		t.Errorf("tenant B: objective = %q, want %q", val, nsB)
	}
}

// TestNonInferencePathNoFlowControlHeaders verifies that flow control headers
// are NOT injected on non-inference paths. The AuthPolicy should scope header
// injection to inference paths only (e.g., /v1/chat/completions).
func TestNonInferencePathNoFlowControlHeaders(t *testing.T) {
	t.Parallel()

	ns := batchEnv.createNamespace(t, "e2e-fc", nil)
	batchEnv.deployEchoServer(t, ns)
	batchEnv.createHTTPRoute(t, ns, "echo-noninference", fmt.Sprintf("/%s/echo-server", ns))
	batchEnv.createServiceAccount(t, ns, "fc-user")
	batchEnv.grantInferenceAccess(t, ns, ns, "fc-user")
	token := batchEnv.requestToken(t, ns, "fc-user")
	batchEnv.waitForGatewayRoute(t, fmt.Sprintf("/%s/echo-server/test", ns), token)

	path := fmt.Sprintf("/%s/echo-server/v1/files", ns)
	resp, body := batchEnv.gatewayGet(t, path, token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	if _, ok := parseEchoHeader(t, body, "x-gateway-inference-fairness-id"); ok {
		t.Error("non-inference path should not have x-gateway-inference-fairness-id header")
	}
	if _, ok := parseEchoHeader(t, body, "x-gateway-inference-objective"); ok {
		t.Error("non-inference path should not have x-gateway-inference-objective header")
	}
}
