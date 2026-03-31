package llm

import (
	"testing"

	kservev1alpha2 "github.com/kserve/kserve/pkg/apis/serving/v1alpha2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func ref(name, namespace string) kservev1alpha2.UntypedObjectReference {
	return kservev1alpha2.UntypedObjectReference{
		Name:      gatewayapiv1.ObjectName(name),
		Namespace: gatewayapiv1.Namespace(namespace),
	}
}

func gateway(name, namespace string, listeners ...gatewayapiv1.Listener) *gatewayapiv1.Gateway {
	return &gatewayapiv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec:       gatewayapiv1.GatewaySpec{Listeners: listeners},
	}
}

func allListener(name string) gatewayapiv1.Listener {
	return gatewayapiv1.Listener{
		Name: gatewayapiv1.SectionName(name),
		AllowedRoutes: &gatewayapiv1.AllowedRoutes{
			Namespaces: &gatewayapiv1.RouteNamespaces{
				From: ptr.To(gatewayapiv1.NamespacesFromAll),
			},
		},
	}
}

func sameListener(name string) gatewayapiv1.Listener {
	return gatewayapiv1.Listener{
		Name: gatewayapiv1.SectionName(name),
		AllowedRoutes: &gatewayapiv1.AllowedRoutes{
			Namespaces: &gatewayapiv1.RouteNamespaces{
				From: ptr.To(gatewayapiv1.NamespacesFromSame),
			},
		},
	}
}

func selectorListener(name string) gatewayapiv1.Listener {
	return gatewayapiv1.Listener{
		Name: gatewayapiv1.SectionName(name),
		AllowedRoutes: &gatewayapiv1.AllowedRoutes{
			Namespaces: &gatewayapiv1.RouteNamespaces{
				From: ptr.To(gatewayapiv1.NamespacesFromSelector),
				Selector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"env": "prod"},
				},
			},
		},
	}
}

func TestFilterAllowedRefs(t *testing.T) {
	tests := []struct {
		name     string
		gateway  *gatewayapiv1.Gateway
		refs     []kservev1alpha2.UntypedObjectReference
		targetNS string
		wantLen  int
		wantRefs []kservev1alpha2.UntypedObjectReference
	}{
		{
			name:    "nil gateway returns all refs",
			gateway: nil,
			refs:    []kservev1alpha2.UntypedObjectReference{ref("gw", "ns")},
			wantLen: 1,
		},
		{
			name:     "nil refs returns nil",
			gateway:  gateway("gw", "ns", allListener("http")),
			refs:     nil,
			targetNS: "ns",
			wantLen:  0,
		},
		{
			name:     "ref to different gateway is kept",
			gateway:  gateway("gw-a", "ns", sameListener("http")),
			refs:     []kservev1alpha2.UntypedObjectReference{ref("gw-b", "ns")},
			targetNS: "other",
			wantLen:  1,
		},
		{
			name:     "From All allows ref from any namespace",
			gateway:  gateway("gw", "gw-ns", allListener("http")),
			refs:     []kservev1alpha2.UntypedObjectReference{ref("gw", "gw-ns")},
			targetNS: "other-ns",
			wantLen:  1,
		},
		{
			name:     "From Same allows ref from same namespace",
			gateway:  gateway("gw", "ns", sameListener("http")),
			refs:     []kservev1alpha2.UntypedObjectReference{ref("gw", "ns")},
			targetNS: "ns",
			wantLen:  1,
		},
		{
			name:     "From Same rejects ref from different namespace",
			gateway:  gateway("gw", "gw-ns", sameListener("http")),
			refs:     []kservev1alpha2.UntypedObjectReference{ref("gw", "gw-ns")},
			targetNS: "other-ns",
			wantLen:  0,
		},
		{
			name:     "nil allowedRoutes defaults to Same, same namespace",
			gateway:  gateway("gw", "ns", gatewayapiv1.Listener{Name: "http"}),
			refs:     []kservev1alpha2.UntypedObjectReference{ref("gw", "ns")},
			targetNS: "ns",
			wantLen:  1,
		},
		{
			name:     "nil allowedRoutes defaults to Same, different namespace",
			gateway:  gateway("gw", "gw-ns", gatewayapiv1.Listener{Name: "http"}),
			refs:     []kservev1alpha2.UntypedObjectReference{ref("gw", "gw-ns")},
			targetNS: "other-ns",
			wantLen:  0,
		},
		{
			name:     "From Selector is permissive",
			gateway:  gateway("gw", "gw-ns", selectorListener("http")),
			refs:     []kservev1alpha2.UntypedObjectReference{ref("gw", "gw-ns")},
			targetNS: "any-ns",
			wantLen:  1,
		},
		{
			name:    "empty ref namespace defaults to targetNS",
			gateway: gateway("gw", "app-ns", allListener("http")),
			refs:    []kservev1alpha2.UntypedObjectReference{ref("gw", "")},
			// ref ns is empty → defaults to targetNS ("app-ns") → matches gateway ns
			targetNS: "app-ns",
			wantLen:  1,
		},
		{
			name:     "empty ref namespace defaults to targetNS, no match",
			gateway:  gateway("gw", "gw-ns", sameListener("http")),
			refs:     []kservev1alpha2.UntypedObjectReference{ref("gw", "")},
			targetNS: "other-ns",
			// ref ns defaults to "other-ns" → doesn't match gateway ns "gw-ns" → kept as different gateway
			wantLen: 1,
		},
		{
			name:    "multiple listeners, one allows",
			gateway: gateway("gw", "gw-ns", sameListener("internal"), allListener("external")),
			refs:    []kservev1alpha2.UntypedObjectReference{ref("gw", "gw-ns")},
			// sameListener rejects "other-ns", but allListener allows it
			targetNS: "other-ns",
			wantLen:  1,
		},
		{
			name:     "gateway with no listeners rejects",
			gateway:  gateway("gw", "gw-ns"),
			refs:     []kservev1alpha2.UntypedObjectReference{ref("gw", "gw-ns")},
			targetNS: "gw-ns",
			wantLen:  0,
		},
		{
			name:    "mixed refs: matching and non-matching gateways",
			gateway: gateway("gw", "gw-ns", sameListener("http")),
			refs: []kservev1alpha2.UntypedObjectReference{
				ref("gw", "gw-ns"),       // matches gateway, Same rejects "other-ns"
				ref("other-gw", "gw-ns"),  // different gateway, kept
				ref("gw", "different-ns"), // different namespace, kept
			},
			targetNS: "other-ns",
			wantLen:  2,
			wantRefs: []kservev1alpha2.UntypedObjectReference{
				ref("other-gw", "gw-ns"),
				ref("gw", "different-ns"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterAllowedRefs(tt.gateway, tt.refs, tt.targetNS)
			if len(got) != tt.wantLen {
				t.Fatalf("filterAllowedRefs() returned %d refs, want %d; got %+v", len(got), tt.wantLen, got)
			}
			if tt.wantRefs != nil {
				for i, want := range tt.wantRefs {
					if got[i].Name != want.Name || got[i].Namespace != want.Namespace {
						t.Errorf("ref[%d] = {%s, %s}, want {%s, %s}", i, got[i].Name, got[i].Namespace, want.Name, want.Namespace)
					}
				}
			}
		})
	}
}

func TestListenerAllowsNamespace(t *testing.T) {
	tests := []struct {
		name        string
		listener    gatewayapiv1.Listener
		gwNamespace string
		targetNS    string
		want        bool
	}{
		{
			name:        "nil AllowedRoutes defaults to Same, match",
			listener:    gatewayapiv1.Listener{},
			gwNamespace: "ns",
			targetNS:    "ns",
			want:        true,
		},
		{
			name:        "nil AllowedRoutes defaults to Same, no match",
			listener:    gatewayapiv1.Listener{},
			gwNamespace: "gw-ns",
			targetNS:    "other-ns",
			want:        false,
		},
		{
			name: "nil Namespaces defaults to Same",
			listener: gatewayapiv1.Listener{
				AllowedRoutes: &gatewayapiv1.AllowedRoutes{},
			},
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
			gwNamespace: "ns",
			targetNS:    "ns",
			want:        true,
		},
		{
			name:        "From All allows any namespace",
			listener:    allListener("http"),
			gwNamespace: "gw-ns",
			targetNS:    "any-ns",
			want:        true,
		},
		{
			name:        "From Same allows same namespace",
			listener:    sameListener("http"),
			gwNamespace: "ns",
			targetNS:    "ns",
			want:        true,
		},
		{
			name:        "From Same rejects different namespace",
			listener:    sameListener("http"),
			gwNamespace: "gw-ns",
			targetNS:    "other-ns",
			want:        false,
		},
		{
			name:        "From Selector is permissive",
			listener:    selectorListener("http"),
			gwNamespace: "gw-ns",
			targetNS:    "any-ns",
			want:        true,
		},
		{
			name: "From Selector with nil selector is permissive",
			listener: gatewayapiv1.Listener{
				AllowedRoutes: &gatewayapiv1.AllowedRoutes{
					Namespaces: &gatewayapiv1.RouteNamespaces{
						From: ptr.To(gatewayapiv1.NamespacesFromSelector),
					},
				},
			},
			gwNamespace: "gw-ns",
			targetNS:    "any-ns",
			want:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := listenerAllowsNamespace(tt.listener, tt.gwNamespace, tt.targetNS)
			if got != tt.want {
				t.Errorf("listenerAllowsNamespace() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGatewayAllowsNamespace(t *testing.T) {
	tests := []struct {
		name     string
		gateway  *gatewayapiv1.Gateway
		targetNS string
		want     bool
	}{
		{
			name:     "no listeners rejects",
			gateway:  gateway("gw", "ns"),
			targetNS: "ns",
			want:     false,
		},
		{
			name:     "single All listener allows",
			gateway:  gateway("gw", "ns", allListener("http")),
			targetNS: "other",
			want:     true,
		},
		{
			name:     "single Same listener, same namespace",
			gateway:  gateway("gw", "ns", sameListener("http")),
			targetNS: "ns",
			want:     true,
		},
		{
			name:     "single Same listener, different namespace",
			gateway:  gateway("gw", "gw-ns", sameListener("http")),
			targetNS: "other-ns",
			want:     false,
		},
		{
			name:     "multiple listeners, one allows",
			gateway:  gateway("gw", "gw-ns", sameListener("internal"), allListener("external")),
			targetNS: "other-ns",
			want:     true,
		},
		{
			name:     "multiple Same listeners, all reject",
			gateway:  gateway("gw", "gw-ns", sameListener("a"), sameListener("b")),
			targetNS: "other-ns",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := gatewayAllowsNamespace(tt.gateway, tt.targetNS)
			if got != tt.want {
				t.Errorf("gatewayAllowsNamespace() = %v, want %v", got, tt.want)
			}
		})
	}
}