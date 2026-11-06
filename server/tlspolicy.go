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
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"reflect"
	"sync"
	"time"

	opstls "github.com/opendatahub-io/odh-model-controller/pkg/tls"
	configv1 "github.com/openshift/api/config/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	utilnet "k8s.io/apimachinery/pkg/util/net"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// applyClusterTLSPolicy resolves the cluster TLS profile and adherence policy
// and applies the effective settings to the baseline TLS configuration when
// adherence requires it. NoOpinion and LegacyAdheringComponentsOnly keep the
// baseline unchanged (TLS 1.2 minimum, AES-GCM suites, PQC curve preferences,
// GetCertificate callback). StrictAllComponents and unknown adherence values
// apply the profile's minimum version and cipher suites on top of the
// baseline, preserving the certificate callback and curve preferences.
//
// When the cluster TLS API is available, a background watch reports policy
// changes through onChange so the process can shut down gracefully and
// restart with the new policy.
func applyClusterTLSPolicy(ctx context.Context, cfg *rest.Config, baseline *tls.Config, onChange func()) error {
	resolveCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	result, err := opstls.Resolve(resolveCtx, cfg)
	if err != nil {
		return fmt.Errorf("resolving cluster TLS policy: %w", err)
	}

	if _, err := applyTLSPolicy(baseline, result); err != nil {
		return fmt.Errorf("applying cluster TLS policy: %w", err)
	}

	if result.APIAvailable {
		go watchTLSPolicy(ctx, cfg, result, onChange)
	}
	return nil
}

// applyTLSPolicy applies the resolved policy to the baseline configuration
// according to the adherence gating, and reports whether the adherence value
// was a known enum. A malformed profile is fatal only when the adherence
// policy applies it; under NoOpinion/Legacy the baseline is preserved. It is
// a pure function of the result for testability.
func applyTLSPolicy(baseline *tls.Config, result opstls.Result) (bool, error) {
	opts, known, err := result.ListenerOpts()
	if !known {
		slog.Warn("unknown TLS adherence policy, treating as StrictAllComponents", "adherence", result.Adherence)
	}
	if err != nil {
		return known, err
	}
	for _, opt := range opts {
		opt(baseline)
	}
	return known, nil
}

// tlsPolicyWatchRetryInterval is the backoff before re-establishing the TLS
// policy watch after the stream closes or setup fails. Kubernetes watches are
// closed routinely by the API server (timeouts, restarts), so the watch must
// be re-established or later policy changes go undetected.
const tlsPolicyWatchRetryInterval = 30 * time.Second

// servesHTTP2 reports whether the effective TLS configuration serves HTTP/2:
// either no ALPN is configured (net/http enables h2 automatically with its
// default suites) or h2 is explicitly advertised.
func servesHTTP2(c *tls.Config) bool {
	if len(c.NextProtos) == 0 {
		return true
	}
	for _, p := range c.NextProtos {
		if p == "h2" {
			return true
		}
	}
	return false
}

// disableHTTP2 prevents net/http from configuring HTTP/2 on the server. Go's
// HTTP/2 setup requires an AES-128-GCM cipher suite, so for HTTP/1.1-only
// configurations (an Old minimum version, or a cipher list without
// AES-128-GCM) h2 must be disabled via TLSNextProto, not only through ALPN:
// ServeTLS runs the HTTP/2 setup before ALPN is applied and fails otherwise.
func disableHTTP2(srv *http.Server) {
	srv.TLSNextProto = map[string]func(*http.Server, *tls.Conn, http.Handler){}
}

// watchTLSPolicy watches the cluster APIServer object and invokes onChange
// when the effective TLS policy (profile or adherence) differs from the
// initial resolution. The watch is re-established when the stream closes or
// setup fails, re-checking the current policy first so changes that happened
// while the stream was down are still detected.
func watchTLSPolicy(ctx context.Context, cfg *rest.Config, initial opstls.Result, onChange func()) {
	scheme := runtime.NewScheme()
	if err := configv1.Install(scheme); err != nil {
		slog.Error("failed to install OpenShift config scheme for TLS policy watch", "error", err)
		return
	}

	watcher, err := createWatchClientWithRetry(ctx, tlsPolicyWatchRetryInterval, func() (client.WithWatch, error) {
		return client.NewWithWatch(cfg, client.Options{Scheme: scheme})
	})
	if err != nil {
		return
	}

	runTLSWatch(ctx, watcher, initial, onChange, tlsPolicyWatchRetryInterval)
}

func waitForTLSWatchRetryInterval(ctx context.Context, interval time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(interval):
		return true
	}
}

// watchTLSPolicyLoop consumes events from w until the policy changes or the
// context is done. It returns true when the stream closed and the caller
// should re-establish the watch, false when the loop is finished.
func watchTLSPolicyLoop(ctx context.Context, w watch.Interface, initial opstls.Result, onChange func()) bool {
	defer w.Stop()

	for {
		select {
		case <-ctx.Done():
			return false
		case ev, ok := <-w.ResultChan():
			if !ok {
				return true
			}
			apiServer, isAPIServer := ev.Object.(*configv1.APIServer)
			if !isAPIServer || apiServer.Name != "cluster" {
				continue
			}
			if policyChanged(initial, apiServer) {
				slog.Info("cluster TLS policy changed, initiating shutdown to reload")
				onChange()
				return false
			}
		}
	}
}

// policyChanged reports whether the effective policy carried by the observed
// APIServer object differs from the initial resolution. Profile changes are
// only relevant when the adherence policy applies the profile; under
// NoOpinion/Legacy only an adherence transition matters, so profile edits
// that do not affect serving do not trigger a restart.
//
// The raw profile is compared rather than the normalized spec:
// normalization maps malformed shapes (e.g. Custom with a nil block) to
// Intermediate, which would hide a transition from a valid profile to a
// malformed one under Strict. Detecting the raw change triggers a restart,
// and startup validation then fails closed on the malformed profile.

// runTLSWatch owns the retry loop so transient re-check and watch-setup
// recovery can be tested without constructing a controller-runtime Manager.
func runTLSWatch(
	ctx context.Context,
	watcher client.WithWatch,
	initial opstls.Result,
	onChange func(),
	retryInterval time.Duration,
) {
	for {
		w, err := watchWithTimeoutBound(ctx, watcher, watchSetupTimeout)
		if err != nil {
			slog.Error("failed to watch APIServer for TLS policy changes; retrying", "error", err)
		} else {
			// Re-check for changes between the initial Resolve snapshot and
			// the watch being established: the stream only replays events from
			// this point on, so a change in that window would otherwise be
			// missed until the next update.
			changed, retry := policyRecheck(ctx, watcher, initial, "before the watch was established", onChange)
			if changed {
				return
			}
			if retry {
				w.Stop()
				if !waitForTLSWatchRetryInterval(ctx, retryInterval) {
					return
				}
				continue
			}
			if !watchTLSPolicyLoop(ctx, w, initial, onChange) {
				return
			}
			slog.Info("TLS policy watch stream closed; re-establishing")
		}

		// Re-check for changes that happened while the stream was down.
		changed, retry := policyRecheck(ctx, watcher, initial, "while the watch was down", onChange)
		if changed {
			return
		}
		if retry {
			if !waitForTLSWatchRetryInterval(ctx, retryInterval) {
				return
			}
			continue
		}
		if !waitForTLSWatchRetryInterval(ctx, retryInterval) {
			return
		}
	}
}
func policyChanged(initial opstls.Result, apiServer *configv1.APIServer) bool {
	if initial.Adherence != apiServer.Spec.TLSAdherence {
		return true
	}
	if applies, _ := opstls.ShouldApplyProfile(apiServer.Spec.TLSAdherence); !applies {
		return false
	}
	return !reflect.DeepEqual(initial.RawProfile, apiServer.Spec.TLSSecurityProfile)
}

// createWatchClientWithRetry retries client construction failures while the
// manager remains running. Certificate files or other local REST configuration
// inputs can be transiently unavailable after startup resolution.
func createWatchClientWithRetry(
	ctx context.Context,
	retryInterval time.Duration,
	create func() (client.WithWatch, error),
) (client.WithWatch, error) {
	for {
		watcher, err := create()
		if err == nil {
			return watcher, nil
		}
		slog.Error("failed to create watching client for TLS policy; retrying", "error", err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(retryInterval):
		}
	}
}

// watchSetupTimeout bounds how long the watch request may take to receive
// response headers. A hung API server that accepts the connection but never
// responds would otherwise block the watch loop before the policy re-check
// runs, leaving the server on stale TLS settings.
const watchSetupTimeout = 30 * time.Second

// watchWithTimeout establishes the APIServer watch with a bounded setup. If
// the watch request does not complete within watchSetupTimeout, the setup is
// abandoned and the loop retries (the re-check then verifies the policy). A
// successfully established stream is not limited by the setup bound.
type cancelOnStopWatch struct {
	watch.Interface
	cancel context.CancelFunc
	once   sync.Once
}

type watchSetupResult struct {
	w   watch.Interface
	err error
}

func (w *cancelOnStopWatch) Stop() {
	w.once.Do(func() {
		w.Interface.Stop()
		w.cancel()
	})
}

func watchWithTimeoutBound(
	ctx context.Context, watcher client.WithWatch, timeout time.Duration,
) (watch.Interface, error) {
	ch := make(chan watchSetupResult, 1)
	// The attempt context stays alive for the lifetime of a successful watch;
	// Stop cancels it. On setup timeout the request is cancelled and any late
	// watch is stopped so repeated timeouts cannot leak watch resources.
	attemptCtx, cancel := context.WithCancel(ctx)
	go func() {
		w, err := watcher.Watch(attemptCtx, &configv1.APIServerList{}, &client.ListOptions{})
		ch <- watchSetupResult{w, err}
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case res := <-ch:
		if res.err != nil {
			cancel()
			return nil, res.err
		}
		return &cancelOnStopWatch{Interface: res.w, cancel: cancel}, nil
	case <-ctx.Done():
		cancel()
		go stopLateWatch(ch)
		return nil, ctx.Err()
	case <-timer.C:
		cancel()
		go stopLateWatch(ch)
		return nil, fmt.Errorf("watch setup timed out after %s", timeout)
	}
}

func stopLateWatch(ch <-chan watchSetupResult) {
	if res := <-ch; res.w != nil {
		res.w.Stop()
	}
}

// isTransientAPIServerReadError classifies retryable API statuses and
// transport errors while leaving unexpected API statuses fail-closed.
func isTransientAPIServerReadError(err error) bool {
	if apierrors.IsServiceUnavailable(err) || apierrors.IsTimeout(err) ||
		apierrors.IsServerTimeout(err) || apierrors.IsTooManyRequests(err) ||
		apierrors.IsInternalError(err) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var statusErr *apierrors.StatusError
	if errors.As(err, &statusErr) {
		return false
	}
	var netErr net.Error
	return utilnet.IsConnectionRefused(err) || utilnet.IsConnectionReset(err) ||
		utilnet.IsProbableEOF(err) || utilnet.IsHTTP2ConnectionLost(err) ||
		(errors.As(err, &netErr) && netErr.Timeout())
}

// policyRecheck reads the current APIServer policy and reports whether the
// process must shut down: a changed policy triggers onChange, and a read
// error that is not NotFound or NoMatch fails closed because the re-check
// cannot verify the policy. NotFound and NoMatch are graceful (the watch
// will catch creation), and the restart re-resolves with retries so
// transient conditions recover.
func policyRecheck(
	ctx context.Context, watcher client.WithWatch, initial opstls.Result, window string, onChange func(),
) (changed, retry bool) {
	apiServer := &configv1.APIServer{}
	recheckCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := watcher.Get(recheckCtx, client.ObjectKey{Name: "cluster"}, apiServer); err != nil {
		// Shutdown cancellation is not a policy failure; don't trigger another
		// shutdown or retry after the parent context has already ended.
		if ctx.Err() != nil {
			return false, false
		}
		if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
			slog.Warn("TLS policy re-check could not find the APIServer object; continuing on the watch", "window", window)
			return false, false
		}
		// Retry transient read errors with a fresh watch so changes during
		// the re-check gap are not missed. Forbidden and unexpected errors
		// fail closed because the policy cannot be verified.
		if isTransientAPIServerReadError(err) {
			slog.Warn("TLS policy re-check hit a transient error; retrying the watch", "window", window, "error", err)
			return false, true
		}
		slog.Error("TLS policy re-check failed; initiating shutdown to reload", "window", window, "error", err)
		onChange()
		return true, false
	}
	if policyChanged(initial, apiServer) {
		slog.Info("cluster TLS policy changed " + window + "; initiating shutdown to reload")
		onChange()
		return true, false
	}
	return false, false
}
