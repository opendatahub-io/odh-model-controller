package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/opendatahub-io/odh-model-controller/server/gateway"
	"github.com/opendatahub-io/odh-model-controller/server/middleware"
)

type mockDiscoverer struct {
	refs []gateway.GatewayRef
	err  error
}

func (m *mockDiscoverer) Discover(_ context.Context, _, _ string) ([]gateway.GatewayRef, error) {
	return m.refs, m.err
}

func requestWithToken(method, path string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	return req.WithContext(middleware.ContextWithToken(req.Context(), "test-token"))
}

func TestGatewayHandler_Success(t *testing.T) {
	h := &GatewayHandler{
		Discoverer: &mockDiscoverer{
			refs: []gateway.GatewayRef{
				{
					Name:        "gw1",
					Namespace:   "infra",
					Listener:    "https",
					Status:      "Ready",
					DisplayName: "Shared Gateway",
					Description: "Edge ingress",
				},
			},
		},
	}

	req := requestWithToken(http.MethodGet, "/api/v1/gateways?namespace=my-project")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp gateway.GatewaysResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if len(resp.Gateways) != 1 || resp.Gateways[0].Name != "gw1" {
		t.Errorf("unexpected response: %+v", resp)
	}
}

func TestGatewayHandler_EmptyResult(t *testing.T) {
	h := &GatewayHandler{
		Discoverer: &mockDiscoverer{refs: []gateway.GatewayRef{}},
	}

	req := requestWithToken(http.MethodGet, "/api/v1/gateways?namespace=my-project")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp gateway.GatewaysResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.Gateways == nil || len(resp.Gateways) != 0 {
		t.Errorf("expected empty gateways array, got: %+v", resp)
	}
}

func TestGatewayHandler_DiscoveryError(t *testing.T) {
	h := &GatewayHandler{
		Discoverer: &mockDiscoverer{err: errors.New("k8s api error")},
	}

	req := requestWithToken(http.MethodGet, "/api/v1/gateways?namespace=my-project")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestGatewayHandler_MissingNamespace(t *testing.T) {
	h := &GatewayHandler{Discoverer: &mockDiscoverer{}}

	req := requestWithToken(http.MethodGet, "/api/v1/gateways")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestGatewayHandler_InvalidNamespace(t *testing.T) {
	h := &GatewayHandler{Discoverer: &mockDiscoverer{}}

	req := requestWithToken(http.MethodGet, "/api/v1/gateways?namespace=INVALID_NS")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestGatewayHandler_WrongMethod(t *testing.T) {
	h := &GatewayHandler{Discoverer: &mockDiscoverer{}}

	req := requestWithToken(http.MethodPost, "/api/v1/gateways?namespace=my-project")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}