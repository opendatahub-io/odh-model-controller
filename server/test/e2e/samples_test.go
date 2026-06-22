//go:build e2e

package e2e

import (
	"net/http"
	"strings"
	"testing"
)

func TestSamplesValidType(t *testing.T) {
	t.Parallel()

	types := []string{
		"workload-single-node",
		"workload-single-node-pd",
		"workload-multi-node-data-parallel",
		"workload-multi-node-data-parallel-pd",
		"router",
	}
	for _, configType := range types {
		t.Run(configType, func(t *testing.T) {
			t.Parallel()

			resp, body := env.HTTPGet(t, env.ServerURL+"/api/v1/llm-d/samples?type="+configType, "")
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want %d; body = %s", resp.StatusCode, http.StatusOK, body)
			}
			if ct := resp.Header.Get("Content-Type"); ct != "application/yaml" {
				t.Errorf("Content-Type = %q, want %q", ct, "application/yaml")
			}
			content := string(body)
			if !strings.Contains(content, "kind: LLMInferenceServiceConfig") {
				t.Error("response does not contain LLMInferenceServiceConfig kind")
			}
			if !strings.Contains(content, "opendatahub.io/config-type") {
				t.Error("response does not contain config-type label")
			}
		})
	}
}

func TestSamplesRouterTopologyPD(t *testing.T) {
	t.Parallel()

	resp, body := env.HTTPGet(t, env.ServerURL+"/api/v1/llm-d/samples?type=router&topology=workload-single-node-pd", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", resp.StatusCode, http.StatusOK, body)
	}
	content := string(body)
	if !strings.Contains(content, "disagg-profile-handler") {
		t.Error("P/D router should contain disagg-profile-handler")
	}
}

func TestSamplesRouterTopologyNonPD(t *testing.T) {
	t.Parallel()

	resp, body := env.HTTPGet(t, env.ServerURL+"/api/v1/llm-d/samples?type=router&topology=workload-single-node", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", resp.StatusCode, http.StatusOK, body)
	}
	content := string(body)
	if !strings.Contains(content, "kind: LLMInferenceServiceConfig") {
		t.Error("response does not contain LLMInferenceServiceConfig kind")
	}
}

func TestSamplesMissingType(t *testing.T) {
	t.Parallel()

	resp, body := env.HTTPGet(t, env.ServerURL+"/api/v1/llm-d/samples", "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body = %s", resp.StatusCode, http.StatusBadRequest, body)
	}
}

func TestSamplesUnknownType(t *testing.T) {
	t.Parallel()

	resp, body := env.HTTPGet(t, env.ServerURL+"/api/v1/llm-d/samples?type=nonexistent", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body = %s", resp.StatusCode, http.StatusNotFound, body)
	}
}

func TestSamplesWrongMethod(t *testing.T) {
	t.Parallel()

	resp, body := env.HTTPDo(t, http.MethodPost, env.ServerURL+"/api/v1/llm-d/samples?type=workload-single-node", "")
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d; body = %s", resp.StatusCode, http.StatusMethodNotAllowed, body)
	}
}
