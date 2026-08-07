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
	"reflect"
	"testing"

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
	}{
		{
			name:           "nil profile returns Intermediate defaults",
			profile:        nil,
			wantMinVersion: tls.VersionTLS12,
			wantCiphers:    IntermediateCiphers,
		},
		{
			name:           "empty profile returns Intermediate defaults",
			profile:        &configv1.TLSSecurityProfile{},
			wantMinVersion: tls.VersionTLS12,
			wantCiphers:    IntermediateCiphers,
		},
		{
			name: "Intermediate type returns Intermediate defaults",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileIntermediateType,
			},
			wantMinVersion: tls.VersionTLS12,
			wantCiphers:    IntermediateCiphers,
		},
		{
			name: "Modern returns TLS 1.3 with nil ciphers",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileModernType,
			},
			wantMinVersion: tls.VersionTLS13,
			wantCiphers:    nil,
		},
		{
			name: "Old returns TLS 1.0 with nil ciphers",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileOldType,
			},
			wantMinVersion: tls.VersionTLS10,
			wantCiphers:    nil,
		},
		{
			name: "Custom with valid ciphers",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{
					TLSProfileSpec: configv1.TLSProfileSpec{
						MinTLSVersion: "VersionTLS12",
						Ciphers: []string{
							"ECDHE-ECDSA-AES128-GCM-SHA256",
							"ECDHE-RSA-AES256-GCM-SHA384",
						},
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
			name: "Custom with unsupported cipher skips it",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{
					TLSProfileSpec: configv1.TLSProfileSpec{
						MinTLSVersion: "VersionTLS12",
						Ciphers: []string{
							"ECDHE-ECDSA-AES128-GCM-SHA256",
							"UNSUPPORTED-CIPHER",
						},
					},
				},
			},
			wantMinVersion: tls.VersionTLS12,
			wantCiphers: []uint16{
				tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			},
		},
		{
			name: "Custom with nil custom block falls back to Intermediate",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
			},
			wantMinVersion: tls.VersionTLS12,
			wantCiphers:    IntermediateCiphers,
		},
		{
			name: "Unknown type falls back to Intermediate",
			profile: &configv1.TLSSecurityProfile{
				Type: "SuperSecure",
			},
			wantMinVersion: tls.VersionTLS12,
			wantCiphers:    IntermediateCiphers,
		},
		{
			name: "Custom with all unsupported ciphers returns empty slice",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{
					TLSProfileSpec: configv1.TLSProfileSpec{
						MinTLSVersion: "VersionTLS12",
						Ciphers: []string{
							"DHE-RSA-AES128-GCM-SHA256",
							"DHE-RSA-AES256-GCM-SHA384",
						},
					},
				},
			},
			wantMinVersion: tls.VersionTLS12,
			wantCiphers:    []uint16{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotMinVersion, gotCiphers := parseProfile(tt.profile)

			if gotMinVersion != tt.wantMinVersion {
				t.Errorf("parseProfile() minVersion = %d, want %d", gotMinVersion, tt.wantMinVersion)
			}

			if tt.wantCiphers == nil {
				if gotCiphers != nil {
					t.Errorf("parseProfile() ciphers = %v, want nil", gotCiphers)
				}
				return
			}

			if gotCiphers == nil {
				t.Fatal("expected non-nil empty slice, got nil (fail-closed guard needs non-nil)")
			}
			if len(gotCiphers) != len(tt.wantCiphers) {
				t.Errorf("parseProfile() ciphers length = %d, want %d", len(gotCiphers), len(tt.wantCiphers))
				return
			}

			for i, c := range gotCiphers {
				if c != tt.wantCiphers[i] {
					t.Errorf("parseProfile() ciphers[%d] = %d, want %d", i, c, tt.wantCiphers[i])
				}
			}
		})
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
	if !result.ProfileFetched {
		t.Error("expected ProfileFetched = true")
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
	if result.ProfileFetched {
		t.Error("expected ProfileFetched = false on NotFound")
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

func TestResolve_TransientError_SetsProfileFetched(t *testing.T) {
	fakeClient := &transientErrorClient{
		err: apierrors.NewServiceUnavailable("api down"),
	}

	result, err := resolve(context.Background(), fakeClient)
	if err != nil {
		t.Fatalf("resolve() error = %v, expected graceful fallback", err)
	}
	if !result.ProfileFetched {
		t.Error("expected ProfileFetched = true on transient error (for watcher self-healing)")
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
		OnProfileChange: func(_ context.Context, _, _ configv1.TLSProfileSpec) {
			called = true
		},
	}
	watcher.lastProfile = intermediateSpec

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

	_, err := resolve(context.Background(), fakeClient)
	if err == nil {
		t.Fatal("expected fail-closed error for all-unsupported ciphers")
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
