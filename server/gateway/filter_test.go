package gateway

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"
)

const expectedProtocolHTTPS = "HTTPS"

func ptr[T any](v T) *T { return &v }

func TestListenerAllowsNamespace(t *testing.T) {
	tests := []struct {
		name        string
		listener    gatewayapiv1.Listener
		gwNamespace string
		targetNS    string
		nsLabels    map[string]string
		want        bool
	}{
		{
			name:        "nil AllowedRoutes defaults to Same, matching namespace",
			listener:    gatewayapiv1.Listener{},
			gwNamespace: "my-ns",
			targetNS:    "my-ns",
			want:        true,
		},
		{
			name:        "nil AllowedRoutes defaults to Same, different namespace",
			listener:    gatewayapiv1.Listener{},
			gwNamespace: "gw-ns",
			targetNS:    "other-ns",
			want:        false,
		},
		{
			name: "nil From defaults to Same",
			listener: gatewayapiv1.Listener{
				AllowedRoutes: &gatewayapiv1.AllowedRoutes{
					Namespaces: &gatewayapiv1.RouteNamespaces{},
				},
			},
			gwNamespace: "gw-ns",
			targetNS:    "other-ns",
			want:        false,
		},
		{
			name: "From All allows any namespace",
			listener: gatewayapiv1.Listener{
				AllowedRoutes: &gatewayapiv1.AllowedRoutes{
					Namespaces: &gatewayapiv1.RouteNamespaces{
						From: ptr(gatewayapiv1.NamespacesFromAll),
					},
				},
			},
			gwNamespace: "gw-ns",
			targetNS:    "any-ns",
			want:        true,
		},
		{
			name: "From Same allows same namespace",
			listener: gatewayapiv1.Listener{
				AllowedRoutes: &gatewayapiv1.AllowedRoutes{
					Namespaces: &gatewayapiv1.RouteNamespaces{
						From: ptr(gatewayapiv1.NamespacesFromSame),
					},
				},
			},
			gwNamespace: "my-ns",
			targetNS:    "my-ns",
			want:        true,
		},
		{
			name: "From Same rejects different namespace",
			listener: gatewayapiv1.Listener{
				AllowedRoutes: &gatewayapiv1.AllowedRoutes{
					Namespaces: &gatewayapiv1.RouteNamespaces{
						From: ptr(gatewayapiv1.NamespacesFromSame),
					},
				},
			},
			gwNamespace: "gw-ns",
			targetNS:    "other-ns",
			want:        false,
		},
		{
			name: "From Selector with nil selector rejects",
			listener: gatewayapiv1.Listener{
				AllowedRoutes: &gatewayapiv1.AllowedRoutes{
					Namespaces: &gatewayapiv1.RouteNamespaces{
						From: ptr(gatewayapiv1.NamespacesFromSelector),
					},
				},
			},
			gwNamespace: "gw-ns",
			targetNS:    "target-ns",
			want:        false,
		},
		{
			name: "From Selector with matching labels",
			listener: gatewayapiv1.Listener{
				AllowedRoutes: &gatewayapiv1.AllowedRoutes{
					Namespaces: &gatewayapiv1.RouteNamespaces{
						From: ptr(gatewayapiv1.NamespacesFromSelector),
						Selector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"env": "prod"},
						},
					},
				},
			},
			gwNamespace: "gw-ns",
			targetNS:    "target-ns",
			nsLabels:    map[string]string{"env": "prod", "team": "ml"},
			want:        true,
		},
		{
			name: "From Selector with non-matching labels",
			listener: gatewayapiv1.Listener{
				AllowedRoutes: &gatewayapiv1.AllowedRoutes{
					Namespaces: &gatewayapiv1.RouteNamespaces{
						From: ptr(gatewayapiv1.NamespacesFromSelector),
						Selector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"env": "prod"},
						},
					},
				},
			},
			gwNamespace: "gw-ns",
			targetNS:    "target-ns",
			nsLabels:    map[string]string{"env": "staging"},
			want:        false,
		},
		{
			name: "From Selector with MatchExpressions In operator",
			listener: gatewayapiv1.Listener{
				AllowedRoutes: &gatewayapiv1.AllowedRoutes{
					Namespaces: &gatewayapiv1.RouteNamespaces{
						From: ptr(gatewayapiv1.NamespacesFromSelector),
						Selector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{Key: "env", Operator: metav1.LabelSelectorOpIn, Values: []string{"prod", "staging"}},
							},
						},
					},
				},
			},
			gwNamespace: "gw-ns",
			targetNS:    "target-ns",
			nsLabels:    map[string]string{"env": "prod"},
			want:        true,
		},
		{
			name: "From Selector with MatchExpressions Exists operator",
			listener: gatewayapiv1.Listener{
				AllowedRoutes: &gatewayapiv1.AllowedRoutes{
					Namespaces: &gatewayapiv1.RouteNamespaces{
						From: ptr(gatewayapiv1.NamespacesFromSelector),
						Selector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{Key: "env", Operator: metav1.LabelSelectorOpExists},
							},
						},
					},
				},
			},
			gwNamespace: "gw-ns",
			targetNS:    "target-ns",
			nsLabels:    map[string]string{"env": "anything"},
			want:        true,
		},
		{
			name: "From Selector with MatchExpressions DoesNotExist operator",
			listener: gatewayapiv1.Listener{
				AllowedRoutes: &gatewayapiv1.AllowedRoutes{
					Namespaces: &gatewayapiv1.RouteNamespaces{
						From: ptr(gatewayapiv1.NamespacesFromSelector),
						Selector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{Key: "restricted", Operator: metav1.LabelSelectorOpDoesNotExist},
							},
						},
					},
				},
			},
			gwNamespace: "gw-ns",
			targetNS:    "target-ns",
			nsLabels:    map[string]string{"env": "prod"},
			want:        true,
		},
		{
			name: "From Selector with empty selector matches all",
			listener: gatewayapiv1.Listener{
				AllowedRoutes: &gatewayapiv1.AllowedRoutes{
					Namespaces: &gatewayapiv1.RouteNamespaces{
						From:     ptr(gatewayapiv1.NamespacesFromSelector),
						Selector: &metav1.LabelSelector{},
					},
				},
			},
			gwNamespace: "gw-ns",
			targetNS:    "target-ns",
			nsLabels:    map[string]string{"any": "label"},
			want:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := listenerAllowsNamespace(tt.listener, tt.gwNamespace, tt.targetNS, tt.nsLabels)
			if got != tt.want {
				t.Errorf("listenerAllowsNamespace() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFilterListeners(t *testing.T) {
	readyGateway := gatewayapiv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gw1",
			Namespace: "infra",
			Annotations: map[string]string{
				AnnotationDisplayName: "Shared Edge Gateway",
				AnnotationDescription: "Production ingress gateway",
			},
		},
		Spec: gatewayapiv1.GatewaySpec{
			Listeners: []gatewayapiv1.Listener{
				{
					Name:     "https",
					Port:     443,
					Protocol: gatewayapiv1.HTTPSProtocolType,
					AllowedRoutes: &gatewayapiv1.AllowedRoutes{
						Namespaces: &gatewayapiv1.RouteNamespaces{
							From: ptr(gatewayapiv1.NamespacesFromAll),
						},
					},
				},
				{
					Name:     "internal",
					Port:     8080,
					Protocol: gatewayapiv1.HTTPProtocolType,
					AllowedRoutes: &gatewayapiv1.AllowedRoutes{
						Namespaces: &gatewayapiv1.RouteNamespaces{
							From: ptr(gatewayapiv1.NamespacesFromSame),
						},
					},
				},
			},
		},
		Status: gatewayapiv1.GatewayStatus{
			Conditions: []metav1.Condition{
				{Type: "Accepted", Status: metav1.ConditionTrue},
				{Type: "Programmed", Status: metav1.ConditionTrue},
			},
			Addresses: []gatewayapiv1.GatewayStatusAddress{
				{
					Type:  ptr(gatewayapiv1.HostnameAddressType),
					Value: "gw.example.com",
				},
			},
		},
	}

	refs := FilterListeners([]gatewayapiv1.Gateway{readyGateway}, "my-project", nil)

	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	if refs[0].Name != "gw1" || refs[0].Listener != "https" || refs[0].Status != "Ready" {
		t.Errorf("unexpected ref: %+v", refs[0])
	}
	if refs[0].DisplayName != "Shared Edge Gateway" {
		t.Errorf("displayName = %q, want %q", refs[0].DisplayName, "Shared Edge Gateway")
	}
	if refs[0].Description != "Production ingress gateway" {
		t.Errorf("description = %q, want %q", refs[0].Description, "Production ingress gateway")
	}
	if refs[0].Hostname != "gw.example.com" {
		t.Errorf("hostname = %q, want %q", refs[0].Hostname, "gw.example.com")
	}
	if refs[0].Protocol != expectedProtocolHTTPS {
		t.Errorf("protocol = %q, want %q", refs[0].Protocol, expectedProtocolHTTPS)
	}
	if refs[0].Port != 443 {
		t.Errorf("port = %d, want %d", refs[0].Port, 443)
	}
}

func TestFilterListeners_MultipleGateways(t *testing.T) {
	gateways := []gatewayapiv1.Gateway{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "gw1", Namespace: "infra"},
			Spec: gatewayapiv1.GatewaySpec{
				Listeners: []gatewayapiv1.Listener{
					{
						Name:     "https",
						Port:     443,
						Protocol: gatewayapiv1.HTTPSProtocolType,
						AllowedRoutes: &gatewayapiv1.AllowedRoutes{
							Namespaces: &gatewayapiv1.RouteNamespaces{
								From: ptr(gatewayapiv1.NamespacesFromAll),
							},
						},
					},
				},
			},
			Status: gatewayapiv1.GatewayStatus{
				Conditions: []metav1.Condition{
					{Type: "Accepted", Status: metav1.ConditionTrue},
					{Type: "Programmed", Status: metav1.ConditionTrue},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "gw2", Namespace: "my-project"},
			Spec: gatewayapiv1.GatewaySpec{
				Listeners: []gatewayapiv1.Listener{
					{
						Name:     "http",
						Port:     80,
						Protocol: gatewayapiv1.HTTPProtocolType,
					},
				},
			},
			Status: gatewayapiv1.GatewayStatus{
				Conditions: []metav1.Condition{
					{Type: "Accepted", Status: metav1.ConditionFalse},
				},
			},
		},
	}

	refs := FilterListeners(gateways, "my-project", nil)

	if len(refs) != 2 {
		t.Fatalf("expected 2 refs, got %d", len(refs))
	}
	if refs[0].Name != "gw1" || refs[0].Status != "Ready" {
		t.Errorf("unexpected first ref: %+v", refs[0])
	}
	if refs[1].Name != "gw2" || refs[1].Status != "NotReady" {
		t.Errorf("unexpected second ref: %+v", refs[1])
	}
}

func TestFilterListeners_NoAnnotations(t *testing.T) {
	gw := gatewayapiv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: "gw-plain", Namespace: "infra"},
		Spec: gatewayapiv1.GatewaySpec{
			Listeners: []gatewayapiv1.Listener{
				{
					Name:     "https",
					Port:     443,
					Protocol: gatewayapiv1.HTTPSProtocolType,
					AllowedRoutes: &gatewayapiv1.AllowedRoutes{
						Namespaces: &gatewayapiv1.RouteNamespaces{
							From: ptr(gatewayapiv1.NamespacesFromAll),
						},
					},
				},
			},
		},
		Status: gatewayapiv1.GatewayStatus{
			Conditions: []metav1.Condition{
				{Type: "Accepted", Status: metav1.ConditionTrue},
				{Type: "Programmed", Status: metav1.ConditionTrue},
			},
		},
	}

	refs := FilterListeners([]gatewayapiv1.Gateway{gw}, "my-project", nil)

	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	if refs[0].DisplayName != "" {
		t.Errorf("displayName = %q, want empty", refs[0].DisplayName)
	}
	if refs[0].Description != "" {
		t.Errorf("description = %q, want empty", refs[0].Description)
	}
}

func TestFilterListeners_NoMatches(t *testing.T) {
	gateways := []gatewayapiv1.Gateway{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "gw1", Namespace: "infra"},
			Spec: gatewayapiv1.GatewaySpec{
				Listeners: []gatewayapiv1.Listener{
					{
						Name: "internal",
						AllowedRoutes: &gatewayapiv1.AllowedRoutes{
							Namespaces: &gatewayapiv1.RouteNamespaces{
								From: ptr(gatewayapiv1.NamespacesFromSame),
							},
						},
					},
				},
			},
		},
	}

	refs := FilterListeners(gateways, "other-ns", nil)

	if len(refs) != 0 {
		t.Fatalf("expected 0 refs, got %d", len(refs))
	}
}

func TestNeedsNamespaceLabels(t *testing.T) {
	tests := []struct {
		name     string
		gateways []gatewayapiv1.Gateway
		want     bool
	}{
		{
			name:     "empty list",
			gateways: nil,
			want:     false,
		},
		{
			name: "no selector listeners",
			gateways: []gatewayapiv1.Gateway{
				{
					Spec: gatewayapiv1.GatewaySpec{
						Listeners: []gatewayapiv1.Listener{
							{AllowedRoutes: &gatewayapiv1.AllowedRoutes{
								Namespaces: &gatewayapiv1.RouteNamespaces{
									From: ptr(gatewayapiv1.NamespacesFromAll),
								},
							}},
						},
					},
				},
			},
			want: false,
		},
		{
			name: "has selector listener",
			gateways: []gatewayapiv1.Gateway{
				{
					Spec: gatewayapiv1.GatewaySpec{
						Listeners: []gatewayapiv1.Listener{
							{AllowedRoutes: &gatewayapiv1.AllowedRoutes{
								Namespaces: &gatewayapiv1.RouteNamespaces{
									From: ptr(gatewayapiv1.NamespacesFromSelector),
								},
							}},
						},
					},
				},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NeedsNamespaceLabels(tt.gateways)
			if got != tt.want {
				t.Errorf("NeedsNamespaceLabels() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExtractHostname(t *testing.T) {
	tests := []struct {
		name      string
		addresses []gatewayapiv1.GatewayStatusAddress
		want      string
	}{
		{
			name:      "no addresses returns empty",
			addresses: nil,
			want:      "",
		},
		{
			name: "hostname address type preferred",
			addresses: []gatewayapiv1.GatewayStatusAddress{
				{Type: ptr(gatewayapiv1.IPAddressType), Value: "10.0.0.1"},
				{Type: ptr(gatewayapiv1.HostnameAddressType), Value: "gw.example.com"},
			},
			want: "gw.example.com",
		},
		{
			name: "IP address used as fallback",
			addresses: []gatewayapiv1.GatewayStatusAddress{
				{Type: ptr(gatewayapiv1.IPAddressType), Value: "10.0.0.1"},
			},
			want: "10.0.0.1",
		},
		{
			name: "nil type falls back to value",
			addresses: []gatewayapiv1.GatewayStatusAddress{
				{Value: "192.168.1.1"},
			},
			want: "192.168.1.1",
		},
		{
			name: "first hostname wins when multiple hostname addresses exist",
			addresses: []gatewayapiv1.GatewayStatusAddress{
				{Type: ptr(gatewayapiv1.HostnameAddressType), Value: "first.example.com"},
				{Type: ptr(gatewayapiv1.HostnameAddressType), Value: "second.example.com"},
			},
			want: "first.example.com",
		},
		{
			name:      "nil gateway returns empty",
			addresses: nil,
			want:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gw := &gatewayapiv1.Gateway{
				Status: gatewayapiv1.GatewayStatus{
					Addresses: tt.addresses,
				},
			}
			got := extractHostname(gw)
			if got != tt.want {
				t.Errorf("extractHostname() = %q, want %q", got, tt.want)
			}
		})
	}

	t.Run("nil gateway", func(t *testing.T) {
		got := extractHostname(nil)
		if got != "" {
			t.Errorf("extractHostname(nil) = %q, want empty", got)
		}
	})

	t.Run("invalid address values are skipped", func(t *testing.T) {
		gw := &gatewayapiv1.Gateway{
			Status: gatewayapiv1.GatewayStatus{
				Addresses: []gatewayapiv1.GatewayStatusAddress{
					{Type: ptr(gatewayapiv1.HostnameAddressType), Value: "not a valid hostname!"},
					{Type: ptr(gatewayapiv1.HostnameAddressType), Value: "valid.example.com"},
				},
			},
		}
		got := extractHostname(gw)
		if got != "valid.example.com" {
			t.Errorf("extractHostname() = %q, want %q", got, "valid.example.com")
		}
	})

	t.Run("all invalid addresses return empty", func(t *testing.T) {
		gw := &gatewayapiv1.Gateway{
			Status: gatewayapiv1.GatewayStatus{
				Addresses: []gatewayapiv1.GatewayStatusAddress{
					{Value: "http://evil.com/redirect"},
					{Value: "spaces in hostname"},
				},
			},
		}
		got := extractHostname(gw)
		if got != "" {
			t.Errorf("extractHostname() = %q, want empty", got)
		}
	})
}

func TestIsValidHostnameOrIP(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{"", false},
		{"gw.example.com", true},
		{"10.0.0.1", true},
		{"::1", true},
		{"fe80::1", true},
		{"my-gateway", true},
		{"a", true},
		{"not a hostname", false},
		{"http://evil.com", false},
		{"-starts-with-dash.com", false},
		{"ends-with-dash-.com", false},
		{"valid-host.example.com", true},
		{"123.456.789.0", false},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			got := isValidHostnameOrIP(tt.value)
			if got != tt.want {
				t.Errorf("isValidHostnameOrIP(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

func TestFilterListeners_ConnectionMetadata(t *testing.T) {
	t.Run("listener hostname overrides gateway status address", func(t *testing.T) {
		listenerHost := gatewayapiv1.Hostname("listener.example.com")
		gw := gatewayapiv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{Name: "gw1", Namespace: "infra"},
			Spec: gatewayapiv1.GatewaySpec{
				Listeners: []gatewayapiv1.Listener{
					{
						Name:     "https",
						Port:     443,
						Protocol: gatewayapiv1.HTTPSProtocolType,
						Hostname: &listenerHost,
						AllowedRoutes: &gatewayapiv1.AllowedRoutes{
							Namespaces: &gatewayapiv1.RouteNamespaces{
								From: ptr(gatewayapiv1.NamespacesFromAll),
							},
						},
					},
				},
			},
			Status: gatewayapiv1.GatewayStatus{
				Conditions: []metav1.Condition{
					{Type: "Accepted", Status: metav1.ConditionTrue},
					{Type: "Programmed", Status: metav1.ConditionTrue},
				},
				Addresses: []gatewayapiv1.GatewayStatusAddress{
					{Type: ptr(gatewayapiv1.HostnameAddressType), Value: "gateway-status.example.com"},
				},
			},
		}

		refs := FilterListeners([]gatewayapiv1.Gateway{gw}, "my-project", nil)

		if len(refs) != 1 {
			t.Fatalf("expected 1 ref, got %d", len(refs))
		}
		if refs[0].Hostname != "listener.example.com" {
			t.Errorf("hostname = %q, want %q", refs[0].Hostname, "listener.example.com")
		}
		if refs[0].Protocol != expectedProtocolHTTPS {
			t.Errorf("protocol = %q, want %q", refs[0].Protocol, expectedProtocolHTTPS)
		}
		if refs[0].Port != 443 {
			t.Errorf("port = %d, want %d", refs[0].Port, 443)
		}
	})

	t.Run("gateway status address used when listener has no hostname", func(t *testing.T) {
		gw := gatewayapiv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{Name: "gw1", Namespace: "infra"},
			Spec: gatewayapiv1.GatewaySpec{
				Listeners: []gatewayapiv1.Listener{
					{
						Name:     "http",
						Port:     80,
						Protocol: gatewayapiv1.HTTPProtocolType,
						AllowedRoutes: &gatewayapiv1.AllowedRoutes{
							Namespaces: &gatewayapiv1.RouteNamespaces{
								From: ptr(gatewayapiv1.NamespacesFromAll),
							},
						},
					},
				},
			},
			Status: gatewayapiv1.GatewayStatus{
				Conditions: []metav1.Condition{
					{Type: "Accepted", Status: metav1.ConditionTrue},
					{Type: "Programmed", Status: metav1.ConditionTrue},
				},
				Addresses: []gatewayapiv1.GatewayStatusAddress{
					{Type: ptr(gatewayapiv1.HostnameAddressType), Value: "external.example.com"},
				},
			},
		}

		refs := FilterListeners([]gatewayapiv1.Gateway{gw}, "my-project", nil)

		if len(refs) != 1 {
			t.Fatalf("expected 1 ref, got %d", len(refs))
		}
		if refs[0].Hostname != "external.example.com" {
			t.Errorf("hostname = %q, want %q", refs[0].Hostname, "external.example.com")
		}
		if refs[0].Protocol != "HTTP" {
			t.Errorf("protocol = %q, want %q", refs[0].Protocol, "HTTP")
		}
		if refs[0].Port != 80 {
			t.Errorf("port = %d, want %d", refs[0].Port, 80)
		}
	})

	t.Run("IP address fallback when no hostname address", func(t *testing.T) {
		gw := gatewayapiv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{Name: "gw1", Namespace: "infra"},
			Spec: gatewayapiv1.GatewaySpec{
				Listeners: []gatewayapiv1.Listener{
					{
						Name:     "https",
						Port:     8443,
						Protocol: gatewayapiv1.HTTPSProtocolType,
						AllowedRoutes: &gatewayapiv1.AllowedRoutes{
							Namespaces: &gatewayapiv1.RouteNamespaces{
								From: ptr(gatewayapiv1.NamespacesFromAll),
							},
						},
					},
				},
			},
			Status: gatewayapiv1.GatewayStatus{
				Conditions: []metav1.Condition{
					{Type: "Accepted", Status: metav1.ConditionTrue},
					{Type: "Programmed", Status: metav1.ConditionTrue},
				},
				Addresses: []gatewayapiv1.GatewayStatusAddress{
					{Type: ptr(gatewayapiv1.IPAddressType), Value: "10.0.0.1"},
				},
			},
		}

		refs := FilterListeners([]gatewayapiv1.Gateway{gw}, "my-project", nil)

		if len(refs) != 1 {
			t.Fatalf("expected 1 ref, got %d", len(refs))
		}
		if refs[0].Hostname != "10.0.0.1" {
			t.Errorf("hostname = %q, want %q", refs[0].Hostname, "10.0.0.1")
		}
		if refs[0].Port != 8443 {
			t.Errorf("port = %d, want %d", refs[0].Port, 8443)
		}
	})

	t.Run("no addresses and no listener hostname returns empty hostname", func(t *testing.T) {
		gw := gatewayapiv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{Name: "gw1", Namespace: "infra"},
			Spec: gatewayapiv1.GatewaySpec{
				Listeners: []gatewayapiv1.Listener{
					{
						Name:     "https",
						Port:     443,
						Protocol: gatewayapiv1.HTTPSProtocolType,
						AllowedRoutes: &gatewayapiv1.AllowedRoutes{
							Namespaces: &gatewayapiv1.RouteNamespaces{
								From: ptr(gatewayapiv1.NamespacesFromAll),
							},
						},
					},
				},
			},
			Status: gatewayapiv1.GatewayStatus{
				Conditions: []metav1.Condition{
					{Type: "Accepted", Status: metav1.ConditionTrue},
					{Type: "Programmed", Status: metav1.ConditionTrue},
				},
			},
		}

		refs := FilterListeners([]gatewayapiv1.Gateway{gw}, "my-project", nil)

		if len(refs) != 1 {
			t.Fatalf("expected 1 ref, got %d", len(refs))
		}
		if refs[0].Hostname != "" {
			t.Errorf("hostname = %q, want empty", refs[0].Hostname)
		}
		if refs[0].Protocol != expectedProtocolHTTPS {
			t.Errorf("protocol = %q, want %q", refs[0].Protocol, expectedProtocolHTTPS)
		}
		if refs[0].Port != 443 {
			t.Errorf("port = %d, want %d", refs[0].Port, 443)
		}
	})
}
