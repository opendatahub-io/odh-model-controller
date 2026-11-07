/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package tls

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"

	configv1 "github.com/openshift/api/config/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestParseProfile(t *testing.T) {
	tests := []struct {
		name           string
		profile        *configv1.TLSSecurityProfile
		wantMinVersion uint16
		wantCiphers    []uint16
		wantCurves     []tls.CurveID
		wantErr        string
	}{
		{
			name:           "nil profile returns Intermediate defaults",
			profile:        nil,
			wantMinVersion: tls.VersionTLS12,
			wantCiphers:    IntermediateCiphers,
			wantCurves:     []tls.CurveID{tls.X25519MLKEM768, tls.X25519, tls.CurveP256, tls.CurveP384},
		},
		{
			name:           "empty profile returns Intermediate defaults",
			profile:        &configv1.TLSSecurityProfile{},
			wantMinVersion: tls.VersionTLS12,
			wantCiphers:    IntermediateCiphers,
			wantCurves:     []tls.CurveID{tls.X25519MLKEM768, tls.X25519, tls.CurveP256, tls.CurveP384},
		},
		{
			name:           "Intermediate type returns Intermediate defaults",
			profile:        &configv1.TLSSecurityProfile{Type: configv1.TLSProfileIntermediateType},
			wantMinVersion: tls.VersionTLS12,
			wantCiphers:    IntermediateCiphers,
			wantCurves:     []tls.CurveID{tls.X25519MLKEM768, tls.X25519, tls.CurveP256, tls.CurveP384},
		},
		{
			name:           "Modern returns TLS 1.3 with nil ciphers",
			profile:        &configv1.TLSSecurityProfile{Type: configv1.TLSProfileModernType},
			wantMinVersion: tls.VersionTLS13,
			wantCiphers:    nil,
			wantCurves:     []tls.CurveID{tls.X25519MLKEM768, tls.X25519, tls.CurveP256, tls.CurveP384},
		},
		{
			name:    "Custom with nil custom block returns error",
			profile: &configv1.TLSSecurityProfile{Type: configv1.TLSProfileCustomType},
			wantErr: "custom block is nil",
		},
		{
			name:    "Unknown type returns error",
			profile: &configv1.TLSSecurityProfile{Type: "SuperSecure"},
			wantErr: "unknown TLS profile type",
		},
		{
			name: "Custom with unknown minimum version returns error",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{
					TLSProfileSpec: configv1.TLSProfileSpec{
						MinTLSVersion: "VersionTLS99",
						Ciphers:       []string{"ECDHE-RSA-AES128-GCM-SHA256"},
					},
				},
			},
			wantErr: "unknown minimum TLS version",
		},
		{
			name: "Custom with empty minimum version returns error",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{
					TLSProfileSpec: configv1.TLSProfileSpec{
						Ciphers: []string{"ECDHE-RSA-AES128-GCM-SHA256"},
					},
				},
			},
			wantErr: "no minimum version",
		},
		{
			name: "Custom with valid ciphers",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{
					TLSProfileSpec: configv1.TLSProfileSpec{
						MinTLSVersion: "VersionTLS12",
						Ciphers:       []string{"ECDHE-ECDSA-AES128-GCM-SHA256", "ECDHE-RSA-AES256-GCM-SHA384"},
					},
				},
			},
			wantMinVersion: tls.VersionTLS12,
			wantCiphers: []uint16{
				tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			},
		},
		{
			name: "Custom with supported and unsupported groups keeps supported subset",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{TLSProfileSpec: configv1.TLSProfileSpec{
					MinTLSVersion: configv1.VersionTLS12,
					Groups:        []configv1.TLSGroup{configv1.TLSGroupX25519, "FutureGroup"},
				}},
			},
			wantMinVersion: tls.VersionTLS12,
			wantCurves:     []tls.CurveID{tls.X25519},
		},
		{
			name: "Custom with no groups leaves curve preferences unset",
			profile: &configv1.TLSSecurityProfile{
				Type:   configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{TLSProfileSpec: configv1.TLSProfileSpec{MinTLSVersion: configv1.VersionTLS12}},
			},
			wantMinVersion: tls.VersionTLS12,
		},
		{
			name: "Custom with all unsupported groups returns error",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{TLSProfileSpec: configv1.TLSProfileSpec{
					MinTLSVersion: configv1.VersionTLS12,
					Groups:        []configv1.TLSGroup{"FutureGroup", "AnotherFutureGroup"},
				}},
			},
			wantErr: "TLS profile has no usable groups",
		},
		{
			name: "Custom with unsupported cipher skips it",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{
					TLSProfileSpec: configv1.TLSProfileSpec{
						MinTLSVersion: "VersionTLS12",
						Ciphers:       []string{"ECDHE-ECDSA-AES128-GCM-SHA256", "UNSUPPORTED-CIPHER"},
					},
				},
			},
			wantMinVersion: tls.VersionTLS12,
			wantCiphers:    []uint16{tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256},
		},
		{
			name: "Custom with no ciphers keeps Go defaults",
			profile: &configv1.TLSSecurityProfile{
				Type:   configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{TLSProfileSpec: configv1.TLSProfileSpec{MinTLSVersion: "VersionTLS12"}},
			},
			wantMinVersion: tls.VersionTLS12,
			wantCiphers:    nil,
		},
		{
			name: "Custom TLS 1.2 with only TLS 1.3 suites returns error",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{
					TLSProfileSpec: configv1.TLSProfileSpec{
						MinTLSVersion: "VersionTLS12",
						Ciphers:       []string{"TLS_AES_128_GCM_SHA256", "TLS_CHACHA20_POLY1305_SHA256"},
					},
				},
			},
			wantErr: "no cipher suites usable",
		},
		{
			name: "Custom TLS 1.2 with mixed suites keeps TLS 1.2 subset",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{
					TLSProfileSpec: configv1.TLSProfileSpec{
						MinTLSVersion: "VersionTLS12",
						Ciphers:       []string{"TLS_AES_128_GCM_SHA256", "ECDHE-RSA-AES128-GCM-SHA256"},
					},
				},
			},
			wantMinVersion: tls.VersionTLS12,
			wantCiphers:    []uint16{tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256},
		},
		{
			name: "Custom with all unsupported ciphers returns error",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{
					TLSProfileSpec: configv1.TLSProfileSpec{
						MinTLSVersion: "VersionTLS12",
						Ciphers:       []string{"DHE-RSA-AES128-GCM-SHA256", "DHE-RSA-AES256-GCM-SHA384"},
					},
				},
			},
			wantErr: "no cipher suites usable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotMinVersion, gotCiphers, gotCurves, _, err := parseProfile(tt.profile)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("parseProfile() expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("parseProfile() error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseProfile() unexpected error: %v", err)
			}

			if gotMinVersion != tt.wantMinVersion {
				t.Errorf("parseProfile() minVersion = %d, want %d", gotMinVersion, tt.wantMinVersion)
			}
			if !reflect.DeepEqual(gotCurves, tt.wantCurves) {
				t.Errorf("parseProfile() curves = %v, want %v", gotCurves, tt.wantCurves)
			}

			if tt.wantCiphers == nil {
				if gotCiphers != nil {
					t.Errorf("parseProfile() ciphers = %v, want nil", gotCiphers)
				}
				return
			}

			if len(gotCiphers) != len(tt.wantCiphers) {
				t.Fatalf("parseProfile() ciphers length = %d, want %d (%v)", len(gotCiphers), len(tt.wantCiphers), gotCiphers)
			}
			for i, c := range gotCiphers {
				if c != tt.wantCiphers[i] {
					t.Errorf("parseProfile() ciphers[%d] = %d, want %d", i, c, tt.wantCiphers[i])
				}
			}
		})
	}
}

func TestParseProfile_OldExplicitSuites(t *testing.T) {
	minVersion, ciphers, _, _, err := parseProfile(&configv1.TLSSecurityProfile{Type: configv1.TLSProfileOldType})
	if err != nil {
		t.Fatalf("parseProfile(Old) unexpected error: %v", err)
	}
	if minVersion != tls.VersionTLS10 {
		t.Errorf("parseProfile(Old) minVersion = %d, want TLS 1.0", minVersion)
	}
	if len(ciphers) == 0 {
		t.Fatal("parseProfile(Old) must resolve explicit suites, not nil/unrestricted defaults")
	}
	hasGCM, hasCBC, has3DES := false, false, false
	for _, id := range ciphers {
		switch id {
		case tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256, tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256:
			hasGCM = true
		case tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA, tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA:
			hasCBC = true
		case tls.TLS_RSA_WITH_3DES_EDE_CBC_SHA:
			has3DES = true
		}
	}
	if !hasGCM || !hasCBC || !has3DES {
		t.Errorf("parseProfile(Old) suites must include Old's AES-GCM, CBC and 3DES entries, got %v", ciphers)
	}
}

// Custom profiles naming weak suites must fail profile parsing, in every
// spelling.
func TestParseProfile_CustomRejectsWeakCiphers(t *testing.T) {
	tests := []struct {
		name    string
		ciphers []string
	}{
		{name: "IANA RC4", ciphers: []string{"TLS_RSA_WITH_RC4_128_SHA"}},
		{name: "OpenSSL RC4", ciphers: []string{"ECDHE-RSA-RC4-SHA"}},
		{name: "IANA 3DES", ciphers: []string{"TLS_RSA_WITH_3DES_EDE_CBC_SHA"}},
		{name: "OpenSSL 3DES", ciphers: []string{"DES-CBC3-SHA"}},
		{name: "IANA SHA-1 CBC", ciphers: []string{"TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA"}},
		{name: "OpenSSL SHA-1 CBC", ciphers: []string{"ECDHE-RSA-AES128-SHA"}},
		{
			name: "weak suite mixed with an approved suite",
			ciphers: []string{
				"ECDHE-RSA-AES128-GCM-SHA256",
				"ECDHE-RSA-AES128-SHA",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			profile := &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{TLSProfileSpec: configv1.TLSProfileSpec{
					MinTLSVersion: configv1.VersionTLS12,
					Ciphers:       tc.ciphers,
				}},
			}
			_, _, _, _, err := parseProfile(profile)
			if err == nil || !strings.Contains(err.Error(), "disallowed cipher suite") {
				t.Fatalf("parseProfile() error = %v, want disallowed cipher suite error", err)
			}
		})
	}
}

func TestNextProtos(t *testing.T) {
	if got := nextProtos(tls.VersionTLS12, nil); !reflect.DeepEqual(got, []string{"h2", "http/1.1"}) {
		t.Errorf("nextProtos(TLS1.2, defaults) = %v, want h2+http/1.1", got)
	}
	if got := nextProtos(tls.VersionTLS12, IntermediateCiphers); !reflect.DeepEqual(got, []string{"h2", "http/1.1"}) {
		t.Errorf("nextProtos(TLS1.2, Intermediate) = %v, want h2+http/1.1", got)
	}
	// AES-256-GCM-only lists cannot serve HTTP/2 in Go: ALPN must drop h2.
	noH2 := []uint16{tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384, tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256}
	if got := nextProtos(tls.VersionTLS12, noH2); !reflect.DeepEqual(got, []string{"http/1.1"}) {
		t.Errorf("nextProtos(TLS1.2, no-AES128-GCM) = %v, want http/1.1 only", got)
	}
	// HTTP/2 requires TLS 1.2+, so the Old minimum never advertises h2.
	if got := nextProtos(tls.VersionTLS10, IntermediateCiphers); !reflect.DeepEqual(got, []string{"http/1.1"}) {
		t.Errorf("nextProtos(TLS1.0, ciphers) = %v, want http/1.1 only", got)
	}
}

func TestShouldApplyProfile(t *testing.T) {
	cases := []struct {
		adherence configv1.TLSAdherencePolicy
		applies   bool
		known     bool
	}{
		{configv1.TLSAdherencePolicyNoOpinion, false, true},
		{configv1.TLSAdherencePolicyLegacyAdheringComponentsOnly, false, true},
		{configv1.TLSAdherencePolicyStrictAllComponents, true, true},
		{configv1.TLSAdherencePolicy("Banana"), true, false},
	}
	for _, tc := range cases {
		applies, known := ShouldApplyProfile(tc.adherence)
		if applies != tc.applies || known != tc.known {
			t.Errorf("ShouldApplyProfile(%q) = (%v, %v), want (%v, %v)", tc.adherence, applies, known, tc.applies, tc.known)
		}
	}
}

func TestListenerOpts_Gating(t *testing.T) {
	opts := []func(*tls.Config){func(c *tls.Config) { c.MinVersion = tls.VersionTLS13 }}
	var adherencePolicies = []configv1.TLSAdherencePolicy{
		configv1.TLSAdherencePolicyNoOpinion,
		configv1.TLSAdherencePolicyLegacyAdheringComponentsOnly,
	}
	for _, adherence := range adherencePolicies {
		got, known, err := Result{TLSOpts: opts, Adherence: adherence}.ListenerOpts()
		if got != nil || !known || err != nil {
			t.Errorf("ListenerOpts(%q) = (%v, %v, %v), want (nil, true, nil)", adherence, got, known, err)
		}
		// A malformed profile is ignored when it is not applied.
		got, known, err = Result{TLSOpts: opts, Adherence: adherence, ProfileErr: assert.AnError}.ListenerOpts()
		if got != nil || !known || err != nil {
			t.Errorf("ListenerOpts(%q, ProfileErr) = (%v, %v, %v), want (nil, true, nil)", adherence, got, known, err)
		}
	}
	var strictPolicies = []configv1.TLSAdherencePolicy{
		configv1.TLSAdherencePolicyStrictAllComponents,
		configv1.TLSAdherencePolicy("Banana"),
	}
	for _, adherence := range strictPolicies {
		got, known, err := Result{TLSOpts: opts, Adherence: adherence}.ListenerOpts()
		if len(got) != 1 || err != nil {
			t.Errorf("ListenerOpts(%q) must return the profile options, got %d opts, err %v", adherence, len(got), err)
		}
		wantKnown := adherence == configv1.TLSAdherencePolicyStrictAllComponents
		if known != wantKnown {
			t.Errorf("ListenerOpts(%q) known = %v, want %v", adherence, known, wantKnown)
		}
		// A malformed profile is fatal when it applies.
		if _, _, err := (Result{TLSOpts: opts, Adherence: adherence, ProfileErr: assert.AnError}).ListenerOpts(); err == nil {
			t.Errorf("ListenerOpts(%q, ProfileErr) must fail", adherence)
		}
	}
}

func TestResolveProfileSpec(t *testing.T) {
	intermediateSpec := configv1.TLSProfiles[configv1.TLSProfileIntermediateType]
	modernSpec := configv1.TLSProfiles[configv1.TLSProfileModernType]
	oldSpec := configv1.TLSProfiles[configv1.TLSProfileOldType]

	tests := []struct {
		name    string
		profile *configv1.TLSSecurityProfile
		want    *configv1.TLSProfileSpec
	}{
		{
			name:    "nil profile returns Intermediate",
			profile: nil,
			want:    intermediateSpec,
		},
		{
			name:    "Modern profile",
			profile: &configv1.TLSSecurityProfile{Type: configv1.TLSProfileModernType},
			want:    modernSpec,
		},
		{
			name:    "Old profile",
			profile: &configv1.TLSSecurityProfile{Type: configv1.TLSProfileOldType},
			want:    oldSpec,
		},
		{
			name: "Custom profile returns custom spec",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{
					TLSProfileSpec: configv1.TLSProfileSpec{
						MinTLSVersion: "VersionTLS13",
						Ciphers:       []string{"TLS_AES_128_GCM_SHA256"},
					},
				},
			},
			want: &configv1.TLSProfileSpec{
				MinTLSVersion: "VersionTLS13",
				Ciphers:       []string{"TLS_AES_128_GCM_SHA256"},
			},
		},
		{
			name:    "Custom with nil block returns Intermediate",
			profile: &configv1.TLSSecurityProfile{Type: configv1.TLSProfileCustomType},
			want:    intermediateSpec,
		},
		{
			name:    "Unknown type returns Intermediate",
			profile: &configv1.TLSSecurityProfile{Type: "Future"},
			want:    intermediateSpec,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveProfileSpec(tt.profile)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("resolveProfileSpec() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func newTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = configv1.Install(s)
	return s
}

func TestResolve_Success(t *testing.T) {
	apiServer := &configv1.APIServer{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		Spec: configv1.APIServerSpec{
			TLSSecurityProfile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileModernType,
			},
		},
	}

	scheme := newTestScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(apiServer).Build()

	result, err := resolve(context.Background(), fakeClient)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if !result.APIAvailable {
		t.Error("expected APIAvailable = true")
	}
	if result.ProfileSpec.MinTLSVersion != configv1.TLSProfiles[configv1.TLSProfileModernType].MinTLSVersion {
		t.Errorf("expected Modern profile spec, got %+v", result.ProfileSpec)
	}
}

func TestResolve_NotFound(t *testing.T) {
	scheme := newTestScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	result, err := resolve(context.Background(), fakeClient)
	if err != nil {
		t.Fatalf("Resolve() error = %v, expected graceful fallback", err)
	}
	// Bootstrap race: defaults are applied, but the API is marked available so
	// the profile watcher registers and catches the object's creation.
	if !result.APIAvailable {
		t.Error("expected APIAvailable = true on NotFound (bootstrap race)")
	}
	if len(result.TLSOpts) == 0 {
		t.Error("expected TLSOpts with Intermediate defaults")
	}
	c := &tls.Config{}
	result.TLSOpts[0](c)
	if c.MinVersion != tls.VersionTLS12 {
		t.Errorf("expected TLS 1.2 fallback, got %d", c.MinVersion)
	}
}

func TestIsTransientAPIServerError(t *testing.T) {
	gr := schema.GroupResource{Group: "config.openshift.io", Resource: "apiservers"}
	for _, tc := range []struct {
		name      string
		err       error
		transient bool
	}{
		{name: "internal error", err: apierrors.NewInternalError(errors.New("etcd leader election")), transient: true},
		{name: "connection refused", err: &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}, transient: true},
		{name: "forbidden", err: apierrors.NewForbidden(gr, "cluster", nil), transient: false},
		{name: "bad request", err: apierrors.NewBadRequest("invalid request"), transient: false},
		{name: "invalid", err: apierrors.NewInvalid(
			schema.GroupKind{Group: gr.Group, Kind: "APIServer"}, "cluster", nil), transient: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTransientAPIServerError(tc.err); got != tc.transient {
				t.Fatalf("isTransientAPIServerError(%v) = %t, want %t", tc.err, got, tc.transient)
			}
		})
	}
}

func TestResolve_TransientError_SetsAPIAvailable(t *testing.T) {
	fakeClient := &transientErrorClient{
		err: apierrors.NewServiceUnavailable("api down"),
	}

	result, err := resolve(context.Background(), fakeClient)
	if err != nil {
		t.Fatalf("resolve() error = %v, expected graceful fallback", err)
	}
	if !result.APIAvailable {
		t.Error("expected APIAvailable = true on transient error (for watcher self-healing)")
	}
}

func TestResolve_FatalError_Returns(t *testing.T) {
	gr := schema.GroupResource{Group: "config.openshift.io", Resource: "apiservers"}
	fakeClient := &transientErrorClient{
		err: apierrors.NewForbidden(gr, "cluster", nil),
	}

	_, err := resolve(context.Background(), fakeClient)
	if err == nil {
		t.Fatal("expected error on Forbidden, got nil")
	}
}

func TestProfileWatcher_DetectsChange(t *testing.T) {
	initialProfile := *configv1.TLSProfiles[configv1.TLSProfileIntermediateType]
	scheme := newTestScheme()

	apiServer := &configv1.APIServer{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		Spec: configv1.APIServerSpec{
			TLSSecurityProfile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileModernType,
			},
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(apiServer).Build()

	changed := false
	watcher := &ProfileWatcher{
		Client:             fakeClient,
		InitialProfileSpec: initialProfile,
		OnProfileChange: func(_ context.Context, old, new configv1.TLSProfileSpec) {
			changed = true
		},
	}
	watcher.lastProfile = initialProfile

	_, err := watcher.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: client.ObjectKey{Name: "cluster"},
	})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if !changed {
		t.Error("expected OnProfileChange to be called when profile differs from initial")
	}
}

func TestProfileWatcher_IgnoresNonCluster(t *testing.T) {
	scheme := newTestScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	called := false
	watcher := &ProfileWatcher{
		Client: fakeClient,
		OnProfileChange: func(_ context.Context, _, _ configv1.TLSProfileSpec) {
			called = true
		},
	}

	_, err := watcher.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: client.ObjectKey{Name: "not-cluster"},
	})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if called {
		t.Error("OnProfileChange should not be called for non-cluster objects")
	}
}

func TestProfileWatcher_NoChangeNoCallback(t *testing.T) {
	intermediateSpec := *configv1.TLSProfiles[configv1.TLSProfileIntermediateType]
	scheme := newTestScheme()

	apiServer := &configv1.APIServer{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		Spec: configv1.APIServerSpec{
			TLSSecurityProfile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileIntermediateType,
			},
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(apiServer).Build()

	called := false
	watcher := &ProfileWatcher{
		Client:             fakeClient,
		InitialProfileSpec: intermediateSpec,
		InitialRawProfile:  apiServer.Spec.TLSSecurityProfile,
		OnProfileChange: func(_ context.Context, _, _ configv1.TLSProfileSpec) {
			called = true
		},
	}
	watcher.lastProfile = intermediateSpec
	watcher.lastRaw = apiServer.Spec.TLSSecurityProfile

	_, err := watcher.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: client.ObjectKey{Name: "cluster"},
	})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if called {
		t.Error("OnProfileChange should not be called when profile hasn't changed")
	}
}

func TestResolve_UnsupportedCiphers_FailsClosed(t *testing.T) {
	apiServer := &configv1.APIServer{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		Spec: configv1.APIServerSpec{
			TLSSecurityProfile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{
					TLSProfileSpec: configv1.TLSProfileSpec{
						MinTLSVersion: "VersionTLS12",
						Ciphers:       []string{"UNSUPPORTED-ONLY"},
					},
				},
			},
		},
	}

	scheme := newTestScheme()
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).WithObjects(apiServer).Build()

	result, err := resolve(context.Background(), fakeClient)
	if err != nil {
		t.Fatalf("resolve() must record parse failures, not return them: %v", err)
	}
	if result.ProfileErr == nil {
		t.Fatal("expected recorded ProfileErr for all-unsupported ciphers")
	}
	if len(result.TLSOpts) != 0 {
		t.Error("no TLS options may be installed for a malformed profile")
	}
}

// transientErrorClient is a fake client that returns a specific error on Get.
type transientErrorClient struct {
	client.Client
	err error
}

func (c *transientErrorClient) Get(
	_ context.Context, _ client.ObjectKey, _ client.Object, _ ...client.GetOption,
) error {
	return c.err
}

func applyOpts(opts []func(*tls.Config)) *tls.Config {
	cfg := &tls.Config{}
	for _, fn := range opts {
		fn(cfg)
	}
	return cfg
}

// TestResolve_AppliesProfileUnderAllAdherences pins the honor-always
// behavior of the controller manager path: the resolved profile is applied
// regardless of the adherence policy.
func TestResolve_AppliesProfileUnderAllAdherences(t *testing.T) {
	for _, adherence := range []configv1.TLSAdherencePolicy{
		configv1.TLSAdherencePolicyNoOpinion,
		configv1.TLSAdherencePolicyLegacyAdheringComponentsOnly,
		configv1.TLSAdherencePolicyStrictAllComponents,
		configv1.TLSAdherencePolicy("Banana"),
	} {
		apiServer := &configv1.APIServer{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
			Spec: configv1.APIServerSpec{
				TLSSecurityProfile: &configv1.TLSSecurityProfile{Type: configv1.TLSProfileModernType},
				TLSAdherence:       adherence,
			},
		}
		fakeClient := fake.NewClientBuilder().WithScheme(newTestScheme()).WithObjects(apiServer).Build()

		result, err := resolve(context.Background(), fakeClient)
		if err != nil {
			t.Fatalf("resolve(%q) unexpected error: %v", adherence, err)
		}
		if len(result.TLSOpts) == 0 {
			t.Fatalf("resolve(%q) must apply the profile (honor-always)", adherence)
		}
		cfg := applyOpts(result.TLSOpts)
		if cfg.MinVersion != tls.VersionTLS13 {
			t.Errorf("resolve(%q) applied MinVersion = %d, want TLS 1.3", adherence, cfg.MinVersion)
		}
		wantCurves := []tls.CurveID{tls.X25519MLKEM768, tls.X25519, tls.CurveP256, tls.CurveP384}
		if !reflect.DeepEqual(cfg.CurvePreferences, wantCurves) {
			t.Errorf("resolve(%q) applied CurvePreferences = %v, want %v", adherence, cfg.CurvePreferences, wantCurves)
		}
		if result.Adherence != adherence {
			t.Errorf("resolve(%q) Adherence = %q, want %q", adherence, result.Adherence, adherence)
		}
		if !result.AdherenceFetched {
			t.Errorf("resolve(%q) AdherenceFetched must be true on a successful read", adherence)
		}
	}
}

// TestResolve_AppliedConfig pins the effective tls.Config for the profile
// shapes that have silent-failure histories: TLS 1.3 leaves CipherSuites to
// Go (clause 9), a Custom profile with no ciphers leaves CipherSuites nil
// (clause 13), and a list without AES-128-GCM drops h2 from ALPN (clause 14).
func TestResolve_AppliedConfig(t *testing.T) {
	cases := []struct {
		name          string
		profile       *configv1.TLSSecurityProfile
		wantMin       uint16
		wantCiphers   []uint16
		wantCurves    []tls.CurveID
		wantNextProto []string
	}{
		{
			name:          "Modern leaves cipher selection to Go",
			profile:       &configv1.TLSSecurityProfile{Type: configv1.TLSProfileModernType},
			wantMin:       tls.VersionTLS13,
			wantCiphers:   nil,
			wantCurves:    []tls.CurveID{tls.X25519MLKEM768, tls.X25519, tls.CurveP256, tls.CurveP384},
			wantNextProto: []string{"h2", "http/1.1"},
		},
		{
			name: "Custom TLS 1.2 without ciphers keeps Go defaults",
			profile: &configv1.TLSSecurityProfile{
				Type:   configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{TLSProfileSpec: configv1.TLSProfileSpec{MinTLSVersion: configv1.VersionTLS12}},
			},
			wantMin:       tls.VersionTLS12,
			wantCiphers:   nil,
			wantNextProto: []string{"h2", "http/1.1"},
		},
		{
			name: "Custom TLS 1.2 AES256-only list drops h2 from ALPN",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{TLSProfileSpec: configv1.TLSProfileSpec{
					MinTLSVersion: configv1.VersionTLS12,
					Ciphers:       []string{"ECDHE-RSA-AES256-GCM-SHA384"},
				}},
			},
			wantMin:       tls.VersionTLS12,
			wantCiphers:   []uint16{tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384},
			wantNextProto: []string{"http/1.1"},
		},
		{
			name: "Custom TLS 1.2 applies recognized curve preferences",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{TLSProfileSpec: configv1.TLSProfileSpec{
					MinTLSVersion: configv1.VersionTLS12,
					Groups:        []configv1.TLSGroup{configv1.TLSGroupSecP256r1},
				}},
			},
			wantMin:       tls.VersionTLS12,
			wantCurves:    []tls.CurveID{tls.CurveP256},
			wantNextProto: []string{"h2", "http/1.1"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			apiServer := &configv1.APIServer{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
				Spec:       configv1.APIServerSpec{TLSSecurityProfile: tc.profile},
			}
			fakeClient := fake.NewClientBuilder().WithScheme(newTestScheme()).WithObjects(apiServer).Build()

			result, err := resolve(context.Background(), fakeClient)
			if err != nil {
				t.Fatalf("resolve() unexpected error: %v", err)
			}
			cfg := applyOpts(result.TLSOpts)
			if cfg.MinVersion != tc.wantMin {
				t.Errorf("applied MinVersion = %d, want %d", cfg.MinVersion, tc.wantMin)
			}
			if !reflect.DeepEqual(cfg.CipherSuites, tc.wantCiphers) {
				t.Errorf("applied CipherSuites = %v, want %v", cfg.CipherSuites, tc.wantCiphers)
			}
			if !reflect.DeepEqual(cfg.CurvePreferences, tc.wantCurves) {
				t.Errorf("applied CurvePreferences = %v, want %v", cfg.CurvePreferences, tc.wantCurves)
			}
			if !reflect.DeepEqual(cfg.NextProtos, tc.wantNextProto) {
				t.Errorf("applied NextProtos = %v, want %v", cfg.NextProtos, tc.wantNextProto)
			}
		})
	}
}

// TestResolve_TransientError_InstallsIntermediateFallback pins clause 11:
// a transient read installs the hardened Intermediate fallback options (not
// an empty result) and keeps the API available for watcher self-heal.
func TestResolve_TransientError_InstallsIntermediateFallback(t *testing.T) {
	failing := &transientErrorClient{err: apierrors.NewServiceUnavailable("apiserver restarting")}
	result, err := resolve(context.Background(), failing)
	if err != nil {
		t.Fatalf("resolve() unexpected error: %v", err)
	}
	if !result.APIAvailable {
		t.Error("APIAvailable must be true on transient errors so the watcher self-heals")
	}
	if len(result.TLSOpts) == 0 {
		t.Fatal("transient errors must install the Intermediate fallback options")
	}
	cfg := applyOpts(result.TLSOpts)
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("fallback MinVersion = %d, want TLS 1.2", cfg.MinVersion)
	}
	if !reflect.DeepEqual(cfg.CipherSuites, IntermediateCiphers) {
		t.Errorf("fallback CipherSuites = %v, want Intermediate set", cfg.CipherSuites)
	}
}

// TestResolve_MalformedProfile_RecordedForHonorAlways pins the controller
// contract: the manager applies the profile under every adherence mode, so
// callers treat the recorded ProfileErr as fatal regardless of adherence.
func TestResolve_MalformedProfile_RecordedForHonorAlways(t *testing.T) {
	apiServer := &configv1.APIServer{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		Spec: configv1.APIServerSpec{
			TLSSecurityProfile: &configv1.TLSSecurityProfile{
				Type:   configv1.TLSProfileCustomType,
				Custom: nil,
			},
			TLSAdherence: configv1.TLSAdherencePolicyNoOpinion,
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(newTestScheme()).WithObjects(apiServer).Build()

	result, err := resolve(context.Background(), fakeClient)
	if err != nil {
		t.Fatalf("resolve() must record parse failures, not return them: %v", err)
	}
	if result.ProfileErr == nil {
		t.Fatal("expected recorded ProfileErr for a nil Custom block")
	}
	if len(result.TLSOpts) != 0 {
		t.Error("no TLS options may be installed for a malformed profile")
	}
}

// TestParseSuites_TLS12OnlyGroupsRejected pins the group-version
// compatibility rule: a Custom profile below TLS 1.3 whose group list has
// no TLS 1.2-capable group is rejected, because TLS 1.2 clients would be
// unable to complete a handshake.
func TestParseSuites_TLS12OnlyGroupsRejected(t *testing.T) {
	spec := &configv1.TLSProfileSpec{
		MinTLSVersion: configv1.VersionTLS12,
		Ciphers:       []string{"ECDHE-RSA-AES128-GCM-SHA256"},
		Groups:        []configv1.TLSGroup{configv1.TLSGroupX25519MLKEM768},
	}
	_, _, _, _, err := parseSuites(spec)
	if err == nil {
		t.Fatal("TLS 1.2 profile with only TLS 1.3-capable groups must be rejected")
	}
	if !strings.Contains(err.Error(), "no groups usable for TLS 1.2") {
		t.Errorf("error = %v, want containing 'no groups usable for TLS 1.2'", err)
	}

	// Mixed list with a classical group is accepted.
	spec.Groups = []configv1.TLSGroup{configv1.TLSGroupX25519MLKEM768, configv1.TLSGroupX25519}
	_, _, curves, _, err := parseSuites(spec)
	if err != nil {
		t.Fatalf("mixed group list with a TLS 1.2-capable group must be accepted: %v", err)
	}
	if len(curves) == 0 {
		t.Fatal("expected non-empty curve list")
	}

	// TLS 1.3 profile with only hybrid groups is fine (TLS 1.2 is not permitted).
	spec.MinTLSVersion = configv1.VersionTLS13
	spec.Groups = []configv1.TLSGroup{configv1.TLSGroupX25519MLKEM768}
	_, _, _, _, err = parseSuites(spec)
	if err != nil {
		t.Fatalf("TLS 1.3 profile with hybrid-only groups must be accepted: %v", err)
	}
}
