package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSamplesHandler_ValidType(t *testing.T) {
	h := &SamplesHandler{}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/samples/llm-d?type=workload-single-node", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/yaml" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/yaml")
	}
	body := rec.Body.String()
	if !strings.Contains(body, "kind: LLMInferenceServiceConfig") {
		t.Errorf("response body does not contain LLMInferenceServiceConfig kind")
	}
	if !strings.Contains(body, "opendatahub.io/config-type: workload-single-node") {
		t.Errorf("response body does not contain expected config-type label")
	}
}

func TestSamplesHandler_AllTypes(t *testing.T) {
	h := &SamplesHandler{}

	types := []string{
		"workload-single-node",
		"workload-single-node-pd",
		"workload-multi-node-data-parallel",
		"workload-multi-node-data-parallel-pd",
		"router",
	}
	for _, configType := range types {
		t.Run(configType, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/samples/llm-d?type="+configType, nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/yaml" {
				t.Errorf("Content-Type = %q, want %q", ct, "application/yaml")
			}
			if !strings.Contains(rec.Body.String(), "kind: LLMInferenceServiceConfig") {
				t.Error("response body does not contain LLMInferenceServiceConfig kind")
			}
		})
	}
}

func TestSamplesHandler_MissingType(t *testing.T) {
	h := &SamplesHandler{}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/samples/llm-d", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestSamplesHandler_UnknownType(t *testing.T) {
	h := &SamplesHandler{}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/samples/llm-d?type=nonexistent", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestSamplesHandler_TopologyPD(t *testing.T) {
	h := &SamplesHandler{}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/samples/llm-d?type=router&topology=workload-single-node-pd", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "disagg-profile-handler") {
		t.Error("P/D router should contain disagg-profile-handler")
	}
	if strings.Contains(body, "single-profile-handler") {
		t.Error("P/D router should not contain single-profile-handler")
	}
}

func TestSamplesHandler_TopologyNonPD(t *testing.T) {
	h := &SamplesHandler{}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/samples/llm-d?type=router&topology=workload-single-node", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "kind: LLMInferenceServiceConfig") {
		t.Error("response body does not contain LLMInferenceServiceConfig kind")
	}
}

func TestSamplesHandler_TopologyIgnoredForWorkload(t *testing.T) {
	h := &SamplesHandler{}

	url := "/api/v1/samples/llm-d?type=workload-single-node" +
		"&topology=workload-single-node-pd"
	req := httptest.NewRequest(http.MethodGet, url, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "opendatahub.io/config-type: workload-single-node") {
		t.Error("topology param should be ignored for non-router types")
	}
}

func TestSamplesHandler_WrongMethod(t *testing.T) {
	h := &SamplesHandler{}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/samples/llm-d?type=workload-single-node", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}
