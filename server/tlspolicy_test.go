/*
Copyright 2026.

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

package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	opstls "github.com/opendatahub-io/odh-model-controller/pkg/tls"
	"github.com/opendatahub-io/odh-model-controller/server/common"
	"github.com/stretchr/testify/assert"

	configv1 "github.com/openshift/api/config/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// baselineConfig returns the server's baseline TLS configuration with a
// sentinel certificate callback.
func baselineConfig() *tls.Config {
	getCert := func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return nil, nil }
	return common.NewTLSConfig(getCert)
}

// profileOpts mirrors the options resolve() installs for a resolved profile.
func profileOpts(minVersion uint16, ciphers []uint16, protos []string, curves ...tls.CurveID) []func(*tls.Config) {
	return []func(*tls.Config){func(c *tls.Config) {
		c.MinVersion = minVersion
		if len(ciphers) > 0 {
			c.CipherSuites = ciphers
		}
		if curves != nil {
			c.CurvePreferences = curves
		}
		c.NextProtos = protos
	}}
}

func TestApplyTLSPolicy_NoOpinionAndLegacy_KeepBaseline(t *testing.T) {
	var adherencePolicies = []configv1.TLSAdherencePolicy{
		configv1.TLSAdherencePolicyNoOpinion,
		configv1.TLSAdherencePolicyLegacyAdheringComponentsOnly,
	}
	for _, adherence := range adherencePolicies {
		baseline := baselineConfig()
		wantMin, wantCiphers, wantCurves := baseline.MinVersion, baseline.CipherSuites, baseline.CurvePreferences
		wantCertPtr := reflect.ValueOf(baseline.GetCertificate).Pointer()

		known, err := applyTLSPolicy(baseline, opstls.Result{
			TLSOpts:   profileOpts(tls.VersionTLS13, nil, []string{"h2", "http/1.1"}, tls.CurveP256),
			Adherence: adherence,
		})
		if err != nil {
			t.Errorf("applyTLSPolicy(%q) unexpected error: %v", adherence, err)
		}

		if !known {
			t.Errorf("applyTLSPolicy(%q) should report a known adherence value", adherence)
		}
		if baseline.MinVersion != wantMin || !reflect.DeepEqual(baseline.CipherSuites, wantCiphers) ||
			!reflect.DeepEqual(baseline.CurvePreferences, wantCurves) || baseline.NextProtos != nil {
			t.Errorf("applyTLSPolicy(%q) must not modify the baseline: got %+v", adherence, baseline)
		}
		if reflect.ValueOf(baseline.GetCertificate).Pointer() != wantCertPtr {
			t.Errorf("applyTLSPolicy(%q) must preserve the GetCertificate callback", adherence)
		}
	}
}

func TestApplyTLSPolicy_StrictAndUnknown_ApplyProfile(t *testing.T) {
	custom := []uint16{tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256}
	var adherencePolicies = []configv1.TLSAdherencePolicy{
		configv1.TLSAdherencePolicyStrictAllComponents,
		configv1.TLSAdherencePolicy("Banana"),
	}
	for _, adherence := range adherencePolicies {
		baseline := baselineConfig()
		getCert := baseline.GetCertificate
		profileCurves := []tls.CurveID{tls.CurveP256}

		known, err := applyTLSPolicy(baseline, opstls.Result{
			TLSOpts:   profileOpts(tls.VersionTLS12, custom, []string{"h2", "http/1.1"}, profileCurves...),
			Adherence: adherence,
		})
		if err != nil {
			t.Errorf("applyTLSPolicy(%q) unexpected error: %v", adherence, err)
		}

		if adherence == configv1.TLSAdherencePolicyStrictAllComponents && !known {
			t.Error("applyTLSPolicy(Strict) should report a known adherence value")
		}
		if adherence == configv1.TLSAdherencePolicy("Banana") && known {
			t.Error("applyTLSPolicy(unknown) should report an unknown adherence value")
		}
		if baseline.MinVersion != tls.VersionTLS12 {
			t.Errorf("applyTLSPolicy(%q) MinVersion = %d, want TLS 1.2", adherence, baseline.MinVersion)
		}
		if !reflect.DeepEqual(baseline.CipherSuites, custom) {
			t.Errorf("applyTLSPolicy(%q) CipherSuites = %v, want %v", adherence, baseline.CipherSuites, custom)
		}
		certPtr := reflect.ValueOf(baseline.GetCertificate).Pointer()
		if baseline.GetCertificate == nil || certPtr != reflect.ValueOf(getCert).Pointer() {
			t.Errorf("applyTLSPolicy(%q) must preserve the GetCertificate callback", adherence)
		}
		if !reflect.DeepEqual(baseline.CurvePreferences, profileCurves) {
			t.Errorf("applyTLSPolicy(%q) CurvePreferences = %v, want %v", adherence, baseline.CurvePreferences, profileCurves)
		}
	}
}

func TestApplyTLSPolicy_StrictModern_TLS13(t *testing.T) {
	baseline := baselineConfig()
	if _, err := applyTLSPolicy(baseline, opstls.Result{
		TLSOpts:   profileOpts(tls.VersionTLS13, nil, nil),
		Adherence: configv1.TLSAdherencePolicyStrictAllComponents,
	}); err != nil {
		t.Errorf("applyTLSPolicy(Strict Modern) unexpected error: %v", err)
	}
	if baseline.MinVersion != tls.VersionTLS13 {
		t.Errorf("MinVersion = %d, want TLS 1.3", baseline.MinVersion)
	}
}

func TestPolicyChanged(t *testing.T) {
	same := &configv1.APIServer{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		Spec: configv1.APIServerSpec{
			TLSSecurityProfile: &configv1.TLSSecurityProfile{Type: configv1.TLSProfileIntermediateType},
			TLSAdherence:       configv1.TLSAdherencePolicyStrictAllComponents,
		},
	}
	initial := opstls.Result{
		ProfileSpec: *configv1.TLSProfiles[configv1.TLSProfileIntermediateType],
		RawProfile:  same.Spec.TLSSecurityProfile,
		Adherence:   configv1.TLSAdherencePolicyStrictAllComponents,
	}
	if policyChanged(initial, same) {
		t.Error("policyChanged must be false for an unchanged policy")
	}

	changedProfile := same.DeepCopy()
	changedProfile.Spec.TLSSecurityProfile = &configv1.TLSSecurityProfile{Type: configv1.TLSProfileModernType}
	if !policyChanged(initial, changedProfile) {
		t.Error("policyChanged must be true for a profile change")
	}

	changedAdherence := same.DeepCopy()
	changedAdherence.Spec.TLSAdherence = configv1.TLSAdherencePolicyNoOpinion
	if !policyChanged(initial, changedAdherence) {
		t.Error("policyChanged must be true for an adherence change")
	}

	// Under NoOpinion the profile is not applied: profile edits must not
	// trigger restarts, only adherence transitions must.
	lenientInitial := opstls.Result{
		ProfileSpec: *configv1.TLSProfiles[configv1.TLSProfileIntermediateType],
		Adherence:   configv1.TLSAdherencePolicyNoOpinion,
	}
	lenientProfileEdit := same.DeepCopy()
	lenientProfileEdit.Spec.TLSAdherence = configv1.TLSAdherencePolicyNoOpinion
	lenientProfileEdit.Spec.TLSSecurityProfile = &configv1.TLSSecurityProfile{Type: configv1.TLSProfileModernType}
	if policyChanged(lenientInitial, lenientProfileEdit) {
		t.Error("policyChanged must be false for a profile edit that is not applied under NoOpinion")
	}
	lenientToStrict := same.DeepCopy()
	lenientToStrict.Spec.TLSAdherence = configv1.TLSAdherencePolicyStrictAllComponents
	if !policyChanged(lenientInitial, lenientToStrict) {
		t.Error("policyChanged must be true for a NoOpinion-to-Strict transition")
	}

	// A transition from a valid profile to a malformed one under Strict must
	// be detected even though normalization maps the malformed shape to
	// Intermediate (same as the initial valid profile).
	malformedEdit := same.DeepCopy()
	malformedEdit.Spec.TLSSecurityProfile = &configv1.TLSSecurityProfile{Type: configv1.TLSProfileCustomType}
	if !policyChanged(initial, malformedEdit) {
		t.Error("policyChanged must be true for a valid-to-malformed profile transition under Strict")
	}
}

func TestWatchTLSPolicyLoop_TriggersOnChange(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := configv1.Install(scheme); err != nil {
		t.Fatal(err)
	}

	apiServer := &configv1.APIServer{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		Spec: configv1.APIServerSpec{
			TLSSecurityProfile: &configv1.TLSSecurityProfile{Type: configv1.TLSProfileIntermediateType},
			TLSAdherence:       configv1.TLSAdherencePolicyStrictAllComponents,
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(apiServer).Build()

	initial := opstls.Result{
		ProfileSpec: *configv1.TLSProfiles[configv1.TLSProfileIntermediateType],
		Adherence:   configv1.TLSAdherencePolicyStrictAllComponents,
		RawProfile:  apiServer.Spec.TLSSecurityProfile,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Register the watch synchronously so no event can be missed, then
	// consume it in a goroutine through the production loop.
	w, err := fakeClient.Watch(ctx, &configv1.APIServerList{}, &client.ListOptions{})
	if err != nil {
		t.Fatalf("watch failed: %v", err)
	}

	onChange := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		watchTLSPolicyLoop(ctx, w, initial, func() { onChange <- struct{}{} })
	}()

	// An adherence change must trigger. Benign profile edits are covered by
	// TestPolicyChanged, keeping this event-delivery test deterministic.
	apiServer.Spec.TLSAdherence = configv1.TLSAdherencePolicyNoOpinion
	if err := fakeClient.Update(ctx, apiServer); err != nil {
		t.Fatal(err)
	}

	select {
	case <-onChange:
	case <-time.After(5 * time.Second):
		t.Fatal("watch loop did not report the policy change")
	}
	<-done
}

func TestWatchTLSPolicyLoop_StreamCloseRequestsReestablishment(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := configv1.Install(scheme); err != nil {
		t.Fatal(err)
	}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(&configv1.APIServer{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}}).Build()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	w, err := fakeClient.Watch(ctx, &configv1.APIServerList{}, &client.ListOptions{})
	if err != nil {
		t.Fatalf("watch failed: %v", err)
	}

	initial := opstls.Result{ProfileSpec: *configv1.TLSProfiles[configv1.TLSProfileIntermediateType]}

	called := false
	// Closing the stream (as the API server does routinely) must make the
	// loop return true so the caller re-establishes the watch.
	w.Stop()
	if reestablish := watchTLSPolicyLoop(ctx, w, initial, func() { called = true }); !reestablish {
		t.Error("a closed watch stream must request re-establishment")
	}
	if called {
		t.Error("onChange must not fire for a stream close")
	}
}

func TestServesHTTP2(t *testing.T) {
	if !servesHTTP2(&tls.Config{}) {
		t.Error("no ALPN means net/http auto-h2, so servesHTTP2 must be true")
	}
	if !servesHTTP2(&tls.Config{NextProtos: []string{"h2", "http/1.1"}}) {
		t.Error("explicit h2 must serve HTTP/2")
	}
	if servesHTTP2(&tls.Config{NextProtos: []string{"http/1.1"}}) {
		t.Error("http/1.1-only ALPN must not serve HTTP/2")
	}
}

func TestDisableHTTP2(t *testing.T) {
	srv := &http.Server{}
	disableHTTP2(srv)
	if srv.TLSNextProto == nil {
		t.Error("TLSNextProto must be a non-nil empty map to disable HTTP/2")
	}
	if len(srv.TLSNextProto) != 0 {
		t.Error("TLSNextProto must be empty to disable HTTP/2")
	}
}

// TestApplyTLSPolicy_UnknownAdherenceLogsWarning pins clause 3's logging
// requirement for unknown adherence values.
func TestApplyTLSPolicy_UnknownAdherenceLogsWarning(t *testing.T) {
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(previous)

	if _, err := applyTLSPolicy(baselineConfig(), opstls.Result{
		TLSOpts:   profileOpts(tls.VersionTLS12, nil, nil),
		Adherence: configv1.TLSAdherencePolicy("Banana"),
	}); err != nil {
		t.Errorf("applyTLSPolicy(unknown) unexpected error: %v", err)
	}

	if !strings.Contains(buf.String(), "unknown TLS adherence policy") {
		t.Errorf("expected a warning for the unknown adherence value, got log: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "Banana") {
		t.Errorf("the warning should include the observed value, got log: %q", buf.String())
	}
}

// TestApplyTLSPolicy_MalformedProfile_OnlyFatalWhenApplied pins the clause-4
// keying: a malformed profile is fatal under Strict/unknown adherence, but
// under NoOpinion/Legacy the server keeps its baseline over a profile it is
// documented to ignore.
func TestApplyTLSPolicy_MalformedProfile_OnlyFatalWhenApplied(t *testing.T) {
	var adherencePolicies = []configv1.TLSAdherencePolicy{
		configv1.TLSAdherencePolicyNoOpinion,
		configv1.TLSAdherencePolicyLegacyAdheringComponentsOnly,
	}
	for _, adherence := range adherencePolicies {
		baseline := baselineConfig()
		_, err := applyTLSPolicy(baseline, opstls.Result{
			ProfileErr: assert.AnError,
			Adherence:  adherence,
		})
		if err != nil {
			t.Errorf("applyTLSPolicy(%q) must not fail on a profile it does not apply: %v", adherence, err)
		}
		if baseline.MinVersion != tls.VersionTLS12 {
			t.Errorf("applyTLSPolicy(%q) must keep the baseline", adherence)
		}
	}

	var strictPolicies = []configv1.TLSAdherencePolicy{
		configv1.TLSAdherencePolicyStrictAllComponents,
		configv1.TLSAdherencePolicy("Banana"),
	}
	for _, adherence := range strictPolicies {
		if _, err := applyTLSPolicy(baselineConfig(), opstls.Result{
			ProfileErr: assert.AnError,
			Adherence:  adherence,
		}); err == nil {
			t.Errorf("applyTLSPolicy(%q) must fail on a malformed profile it applies", adherence)
		}
	}
}

// TestPolicyRecheck covers the recheck decision table: graceful NotFound and
// NoMatch, fail-closed authorization and wrapped certificate errors, transient
// retry without onChange, parent cancellation, and policy-change reload.
func TestPolicyRecheck(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := configv1.Install(scheme); err != nil {
		t.Fatal(err)
	}
	apiServer := &configv1.APIServer{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		Spec: configv1.APIServerSpec{
			TLSSecurityProfile: &configv1.TLSSecurityProfile{Type: configv1.TLSProfileIntermediateType},
			TLSAdherence:       configv1.TLSAdherencePolicyStrictAllComponents,
		},
	}
	initial := opstls.Result{
		ProfileSpec: *configv1.TLSProfiles[configv1.TLSProfileIntermediateType],
		RawProfile:  apiServer.Spec.TLSSecurityProfile,
		Adherence:   configv1.TLSAdherencePolicyStrictAllComponents,
	}

	gr := schema.GroupResource{Group: "config.openshift.io", Resource: "apiservers"}
	notFound := apierrors.NewNotFound(gr, "cluster")
	noMatch := &meta.NoKindMatchError{GroupKind: schema.GroupKind{Group: "config.openshift.io", Kind: "APIServer"}}
	forbidden := apierrors.NewForbidden(gr, "cluster", errors.New("denied"))
	serviceUnavailable := apierrors.NewServiceUnavailable("apiserver unavailable")

	changed := apiServer.DeepCopy()
	changed.Spec.TLSAdherence = configv1.TLSAdherencePolicyNoOpinion

	for _, tc := range []struct {
		name         string
		getErr       error
		obj          *configv1.APIServer
		wantRet      bool
		wantRetry    bool
		wantChange   bool
		cancelParent bool
	}{
		{name: "NotFound is graceful", getErr: notFound, wantRet: false},
		{name: "NoMatch is graceful", getErr: noMatch, wantRet: false},
		{name: "Forbidden fails closed", getErr: forbidden, wantRet: true, wantChange: true},
		{name: "transient error retries with a fresh watch", getErr: serviceUnavailable, wantRetry: true},
		{name: "context deadline exceeded requests a retry", getErr: context.DeadlineExceeded, wantRetry: true},
		{name: "connection refused requests a retry",
			getErr: &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}, wantRetry: true},
		{name: "wrapped connection refused requests a retry",
			getErr: &url.Error{Op: "Get", URL: "https://kubernetes.default.svc",
				Err: &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}}, wantRetry: true},
		{name: "wrapped certificate verification error fails closed",
			getErr:     &url.Error{Err: x509.UnknownAuthorityError{Cert: &x509.Certificate{}}},
			wantRet:    true,
			wantChange: true},
		{name: "parent context cancellation is not a policy failure",
			getErr: forbidden, cancelParent: true},
		{name: "unchanged policy continues", obj: apiServer, wantRet: false},
		{name: "changed policy triggers reload", obj: changed, wantRet: true, wantChange: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			if tc.cancelParent {
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			}
			builder := fake.NewClientBuilder().WithScheme(scheme)
			if tc.obj != nil {
				builder = builder.WithObjects(tc.obj)
			}
			if tc.getErr != nil {
				builder = builder.WithInterceptorFuncs(interceptor.Funcs{
					Get: func(ctx context.Context, c client.WithWatch,
						key client.ObjectKey, obj client.Object, opts ...client.GetOption,
					) error {
						return tc.getErr
					},
				})
			}
			c := builder.Build()

			onChange := make(chan struct{}, 1)
			fired, retry := policyRecheck(ctx, c, initial, "test", func() { onChange <- struct{}{} })

			if fired != tc.wantRet {
				t.Fatalf("policyRecheck() changed = %v, want %v", fired, tc.wantRet)
			}
			if retry != tc.wantRetry {
				t.Fatalf("policyRecheck() retry = %v, want %v", retry, tc.wantRetry)
			}
			if tc.wantChange {
				select {
				case <-onChange:
				default:
					t.Fatal("expected onChange to fire")
				}
			} else {
				select {
				case <-onChange:
					t.Fatal("onChange must not fire")
				default:
				}
			}
		})
	}
}

type stubWatchClient struct {
	client.WithWatch
	getFunc   func(context.Context, client.ObjectKey, client.Object, ...client.GetOption) error
	watchFunc func(context.Context, client.ObjectList, ...client.ListOption) (watch.Interface, error)
}

func (c *stubWatchClient) Get(
	ctx context.Context,
	key client.ObjectKey,
	obj client.Object,
	opts ...client.GetOption,
) error {
	if c.getFunc == nil {
		return errors.New("unexpected Get")
	}
	return c.getFunc(ctx, key, obj, opts...)
}

func TestCreateWatchClientWithRetry(t *testing.T) {
	attempts := 0
	want := &stubWatchClient{}
	got, err := createWatchClientWithRetry(context.Background(), time.Millisecond, func() (client.WithWatch, error) {
		attempts++
		if attempts == 1 {
			return nil, errors.New("temporary certificate file read error")
		}
		return want, nil
	})
	if err != nil {
		t.Fatalf("createWatchClientWithRetry() error = %v", err)
	}
	if got != want || attempts != 2 {
		t.Fatalf("watcher=%v attempts=%d, want watcher returned on second attempt", got, attempts)
	}
}

func (c *stubWatchClient) Watch(
	ctx context.Context,
	obj client.ObjectList,
	opts ...client.ListOption,
) (watch.Interface, error) {
	return c.watchFunc(ctx, obj, opts...)
}

type lifecycleTestWatch struct {
	result  chan watch.Event
	stopped chan struct{}
	once    sync.Once
}

func newLifecycleTestWatch() *lifecycleTestWatch {
	return &lifecycleTestWatch{result: make(chan watch.Event), stopped: make(chan struct{})}
}

func (w *lifecycleTestWatch) Stop() {
	w.once.Do(func() { close(w.stopped) })
}

func (w *lifecycleTestWatch) ResultChan() <-chan watch.Event { return w.result }

func TestWatchWithTimeoutContextLifecycle(t *testing.T) {
	t.Run("successful watch lives until Stop", func(t *testing.T) {
		attemptCtxCh := make(chan context.Context, 1)
		stream := newLifecycleTestWatch()
		stub := &stubWatchClient{
			watchFunc: func(ctx context.Context, _ client.ObjectList, _ ...client.ListOption) (watch.Interface, error) {
				attemptCtxCh <- ctx
				return stream, nil
			},
		}
		w, err := watchWithTimeoutBound(context.Background(), stub, time.Second)
		if err != nil {
			t.Fatalf("watchWithTimeoutBound() error = %v", err)
		}
		attemptCtx := <-attemptCtxCh
		select {
		case <-attemptCtx.Done():
			t.Fatal("watch setup context was cancelled before the successful watch stopped")
		default:
		}
		w.Stop()
		select {
		case <-attemptCtx.Done():
		case <-time.After(time.Second):
			t.Fatal("watch Stop did not cancel its setup context")
		}
	})

	t.Run("timed out watch is cancelled and stopped when it returns", func(t *testing.T) {
		stream := newLifecycleTestWatch()
		watchReturned := make(chan context.Context, 1)
		stub := &stubWatchClient{
			watchFunc: func(ctx context.Context, _ client.ObjectList, _ ...client.ListOption) (watch.Interface, error) {
				watchReturned <- ctx
				<-ctx.Done()
				return stream, nil
			},
		}
		w, err := watchWithTimeoutBound(context.Background(), stub, 10*time.Millisecond)
		if err == nil || w != nil {
			t.Fatalf("watchWithTimeoutBound() = (%v, %v), want timeout error and nil watch", w, err)
		}
		attemptCtx := <-watchReturned
		select {
		case <-attemptCtx.Done():
		case <-time.After(time.Second):
			t.Fatal("timed-out watch request context was not cancelled")
		}
		select {
		case <-stream.stopped:
		case <-time.After(time.Second):
			t.Fatal("watch returned after timeout was not stopped")
		}
	})
}

func TestIsTransientAPIServerReadError(t *testing.T) {
	certificateErr := &url.Error{
		Op:  "Get",
		URL: "https://kubernetes.default.svc",
		Err: x509.UnknownAuthorityError{Cert: &x509.Certificate{}},
	}
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "internal error", err: apierrors.NewInternalError(errors.New("temporary internal error")), want: true},
		{name: "deadline exceeded", err: context.DeadlineExceeded, want: true},
		{name: "connection refused", err: &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}, want: true},
		{name: "wrapped certificate verification failure", err: certificateErr, want: false},
		{name: "bad request with transport-like text", err: apierrors.NewBadRequest("connection reset by peer"), want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTransientAPIServerReadError(tc.err); got != tc.want {
				t.Errorf("isTransientAPIServerReadError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestRunTLSWatchRetriesRecheckAfterTransientError(t *testing.T) {
	apiServer := &configv1.APIServer{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		Spec: configv1.APIServerSpec{
			TLSSecurityProfile: &configv1.TLSSecurityProfile{Type: configv1.TLSProfileIntermediateType},
			TLSAdherence:       configv1.TLSAdherencePolicyStrictAllComponents,
		},
	}
	initial := opstls.Result{
		ProfileSpec: *configv1.TLSProfiles[configv1.TLSProfileIntermediateType],
		RawProfile:  apiServer.Spec.TLSSecurityProfile,
		Adherence:   configv1.TLSAdherencePolicyStrictAllComponents,
	}
	firstWatch, secondWatch := newLifecycleTestWatch(), newLifecycleTestWatch()
	watchResults := make(chan *lifecycleTestWatch, 2)
	var getCount atomic.Int32
	var watchCount atomic.Int32
	unavailable := apierrors.NewServiceUnavailable("apiserver restarting")
	stub := &stubWatchClient{
		getFunc: func(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
			if getCount.Add(1) == 1 {
				return unavailable
			}
			*obj.(*configv1.APIServer) = *apiServer.DeepCopy()
			return nil
		},
		watchFunc: func(_ context.Context, _ client.ObjectList, _ ...client.ListOption) (watch.Interface, error) {
			if watchCount.Add(1) == 1 {
				watchResults <- firstWatch
				return firstWatch, nil
			}
			watchResults <- secondWatch
			return secondWatch, nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	changed := make(chan struct{}, 1)
	go func() {
		defer close(done)
		runTLSWatch(ctx, stub, initial, func() { changed <- struct{}{} }, time.Millisecond)
	}()

	if got := <-watchResults; got != firstWatch {
		t.Fatal("first watch was not created")
	}
	if got := <-watchResults; got != secondWatch {
		t.Fatal("transient re-check did not replace the watch")
	}
	select {
	case <-firstWatch.stopped:
	case <-time.After(time.Second):
		t.Fatal("old watch was not stopped before retry")
	}
	select {
	case <-secondWatch.stopped:
		t.Fatal("replacement watch stopped before shutdown")
	default:
	}
	select {
	case <-changed:
		t.Fatal("transient re-check error must not trigger a restart")
	default:
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("watch loop did not stop after context cancellation")
	}
	select {
	case <-secondWatch.stopped:
	case <-time.After(time.Second):
		t.Fatal("replacement watch was not stopped on shutdown")
	}
}
