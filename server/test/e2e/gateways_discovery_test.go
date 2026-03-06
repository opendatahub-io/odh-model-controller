//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/opendatahub-io/odh-model-controller/server/gateway"
)

func TestSecurityHeaders(t *testing.T) {
	t.Parallel()

	resp, _ := env.HTTPGet(t, env.ServerURL+"/healthz", "")

	expected := map[string]string{
		"X-Content-Type-Options":    "nosniff",
		"X-Frame-Options":           "DENY",
		"Cache-Control":             "no-store",
		"Strict-Transport-Security": "max-age=63072000; includeSubDomains",
	}
	for header, want := range expected {
		if got := resp.Header.Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}

func TestMissingAuth(t *testing.T) {
	t.Parallel()

	resp, body := env.HTTPGet(t, env.ServerURL+"/api/v1/gateways?namespace=default", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d; body = %s", resp.StatusCode, http.StatusUnauthorized, body)
	}
}

func TestValidation(t *testing.T) {
	t.Parallel()

	ns := env.CreateNamespace(t, "e2e-gw-validation", nil)
	env.CreateServiceAccount(t, ns, "test-sa")
	token := env.RequestToken(t, ns, "test-sa")

	t.Run("missing namespace", func(t *testing.T) {
		t.Parallel()
		resp, body := env.HTTPGet(t, env.ServerURL+"/api/v1/gateways", token)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d; body = %s", resp.StatusCode, http.StatusBadRequest, body)
		}
	})

	t.Run("invalid namespace", func(t *testing.T) {
		t.Parallel()
		resp, body := env.HTTPGet(t, env.ServerURL+"/api/v1/gateways?namespace=INVALID_NS", token)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d; body = %s", resp.StatusCode, http.StatusBadRequest, body)
		}
	})

	t.Run("wrong method", func(t *testing.T) {
		t.Parallel()
		resp, body := env.HTTPDo(t, http.MethodPost, env.ServerURL+"/api/v1/gateways?namespace=default", token)
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want %d; body = %s", resp.StatusCode, http.StatusMethodNotAllowed, body)
		}
	})
}

func TestAuthorizedUserSeesGateways(t *testing.T) {
	t.Parallel()

	ns := env.CreateNamespace(t, "e2e-gw-authorized", nil)

	env.CreateGateway(t, ns, "test-gw", []gatewayapiv1.Listener{
		{
			Name:     "http",
			Port:     80,
			Protocol: gatewayapiv1.HTTPProtocolType,
			AllowedRoutes: &gatewayapiv1.AllowedRoutes{
				Namespaces: &gatewayapiv1.RouteNamespaces{
					From: ptr.To(gatewayapiv1.NamespacesFromAll),
				},
			},
		},
	}, nil)

	env.CreateServiceAccount(t, ns, "test-sa")
	env.GrantAccess(t, ns, ns, "test-sa")
	token := env.RequestToken(t, ns, "test-sa")

	result := env.WaitForGateway(t, ns, token, "test-gw", ns)

	for _, gw := range result.Gateways {
		if gw.Name == "test-gw" && gw.Namespace == ns {
			if gw.Listener != "http" {
				t.Errorf("listener = %q, want %q", gw.Listener, "http")
			}
			return
		}
	}
}

func TestUnauthorizedUserGetsEmptyList(t *testing.T) {
	t.Parallel()

	ns := env.CreateNamespace(t, "e2e-gw-unauth", nil)
	env.CreateServiceAccount(t, ns, "no-rbac-sa")
	token := env.RequestToken(t, ns, "no-rbac-sa")

	resp, body := env.HTTPGet(t, env.ServerURL+"/api/v1/gateways?namespace="+ns, token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", resp.StatusCode, http.StatusOK, body)
	}

	var result gateway.GatewaysResponse
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(result.Gateways) != 0 {
		t.Errorf("expected empty gateways, got: %+v", result.Gateways)
	}
}

func TestListenerFromSame(t *testing.T) {
	t.Parallel()

	gwNS := env.CreateNamespace(t, "e2e-gw-same", nil)
	otherNS := env.CreateNamespace(t, "e2e-gw-same-other", nil)

	env.CreateGateway(t, gwNS, "same-gw", []gatewayapiv1.Listener{
		{
			Name:     "http",
			Port:     80,
			Protocol: gatewayapiv1.HTTPProtocolType,
			AllowedRoutes: &gatewayapiv1.AllowedRoutes{
				Namespaces: &gatewayapiv1.RouteNamespaces{
					From: ptr.To(gatewayapiv1.NamespacesFromSame),
				},
			},
		},
	}, nil)

	env.CreateServiceAccount(t, gwNS, "test-sa")
	env.GrantAccess(t, gwNS, gwNS, "test-sa")
	env.GrantAccess(t, otherNS, gwNS, "test-sa")
	token := env.RequestToken(t, gwNS, "test-sa")

	t.Run("same namespace sees gateway", func(t *testing.T) {
		t.Parallel()
		env.WaitForGateway(t, gwNS, token, "same-gw", gwNS)
	})

	t.Run("different namespace does not see gateway", func(t *testing.T) {
		t.Parallel()
		resp, body := env.HTTPGet(t, env.ServerURL+"/api/v1/gateways?namespace="+otherNS, token)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d; body = %s", resp.StatusCode, body)
		}
		var result gateway.GatewaysResponse
		if err := json.Unmarshal(body, &result); err != nil {
			t.Fatalf("decode: %v", err)
		}
		for _, gw := range result.Gateways {
			if gw.Name == "same-gw" && gw.Namespace == gwNS {
				t.Errorf("should not see same-gw from different namespace, got: %+v", result.Gateways)
				break
			}
		}
	})
}

func TestListenerFromSelector(t *testing.T) {
	t.Parallel()

	gwNS := env.CreateNamespace(t, "e2e-gw-selector", nil)
	matchNS := env.CreateNamespace(t, "e2e-gw-sel-match", map[string]string{"e2e-test": "match"})
	noMatchNS := env.CreateNamespace(t, "e2e-gw-sel-nomatch", nil)

	env.CreateGateway(t, gwNS, "sel-gw", []gatewayapiv1.Listener{
		{
			Name:     "http",
			Port:     80,
			Protocol: gatewayapiv1.HTTPProtocolType,
			AllowedRoutes: &gatewayapiv1.AllowedRoutes{
				Namespaces: &gatewayapiv1.RouteNamespaces{
					From: ptr.To(gatewayapiv1.NamespacesFromSelector),
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"e2e-test": "match"},
					},
				},
			},
		},
	}, nil)

	env.CreateServiceAccount(t, gwNS, "test-sa")
	env.GrantAccess(t, matchNS, gwNS, "test-sa")
	env.GrantAccess(t, noMatchNS, gwNS, "test-sa")
	token := env.RequestToken(t, gwNS, "test-sa")

	t.Run("matching labels sees gateway", func(t *testing.T) {
		t.Parallel()
		env.WaitForGateway(t, matchNS, token, "sel-gw", gwNS)
	})

	t.Run("non-matching labels does not see gateway", func(t *testing.T) {
		t.Parallel()
		resp, body := env.HTTPGet(t, env.ServerURL+"/api/v1/gateways?namespace="+noMatchNS, token)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d; body = %s", resp.StatusCode, body)
		}
		var result gateway.GatewaysResponse
		if err := json.Unmarshal(body, &result); err != nil {
			t.Fatalf("decode: %v", err)
		}
		for _, gw := range result.Gateways {
			if gw.Name == "sel-gw" && gw.Namespace == gwNS {
				t.Errorf("should not see sel-gw for non-matching namespace, got: %+v", result.Gateways)
				break
			}
		}
	})
}

func TestGatewayMetadata(t *testing.T) {
	t.Parallel()

	ns := env.CreateNamespace(t, "e2e-gw-metadata", nil)

	env.CreateGateway(t, ns, "meta-gw", []gatewayapiv1.Listener{
		{
			Name:     "http",
			Port:     80,
			Protocol: gatewayapiv1.HTTPProtocolType,
			AllowedRoutes: &gatewayapiv1.AllowedRoutes{
				Namespaces: &gatewayapiv1.RouteNamespaces{
					From: ptr.To(gatewayapiv1.NamespacesFromAll),
				},
			},
		},
	}, map[string]string{
		"openshift.io/display-name": "My Gateway",
		"openshift.io/description":  "A test gateway for e2e",
	})

	env.CreateServiceAccount(t, ns, "test-sa")
	env.GrantAccess(t, ns, ns, "test-sa")
	token := env.RequestToken(t, ns, "test-sa")

	result := env.WaitForGateway(t, ns, token, "meta-gw", ns)

	for _, gw := range result.Gateways {
		if gw.Name == "meta-gw" && gw.Namespace == ns {
			if gw.DisplayName != "My Gateway" {
				t.Errorf("displayName = %q, want %q", gw.DisplayName, "My Gateway")
			}
			if gw.Description != "A test gateway for e2e" {
				t.Errorf("description = %q, want %q", gw.Description, "A test gateway for e2e")
			}
			return
		}
	}
}
