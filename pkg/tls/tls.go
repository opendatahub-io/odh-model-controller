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
	"fmt"
	"net"
	"strings"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	ocpcrypto "github.com/openshift/library-go/pkg/crypto"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	utilnet "k8s.io/apimachinery/pkg/util/net"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var log = ctrl.Log.WithName("tls")

// IntermediateCiphers is the Mozilla Intermediate cipher suite set.
var IntermediateCiphers = []uint16{
	tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
	tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
	tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
	tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
	tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
	tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
}

// Result holds the resolved TLS configuration.
type Result struct {
	TLSOpts      []func(*tls.Config)
	ProfileSpec  configv1.TLSProfileSpec
	APIAvailable bool
	// Adherence is the TLS adherence policy read from the same APIServer
	// object as the profile. It is NoOpinion (the zero value) whenever it
	// could not be determined.
	Adherence configv1.TLSAdherencePolicy
	// AdherenceFetched reports whether the adherence value was read
	// successfully. It mirrors APIAvailable.
	AdherenceFetched bool
	// RawProfile is the profile exactly as read from the APIServer object,
	// before normalization. Change detection compares it so transitions to
	// malformed shapes (which normalize to Intermediate) are not hidden.
	RawProfile *configv1.TLSSecurityProfile
	// ProfileErr is set when the profile is malformed (unknown type, nil
	// Custom block, unknown/missing minimum version, or no usable ciphers).
	// It is recorded instead of returned so adherence-aware callers can
	// ignore profiles they are documented not to apply: components that
	// enforce the profile (Strict/unknown adherence, or honor-always
	// consumers) must treat it as fatal; components preserving their own
	// defaults under NoOpinion/Legacy must not.
	ProfileErr error
}

// ShouldApplyProfile reports whether the adherence policy requires a
// component to enforce the cluster TLS profile, and whether the value is a
// known enum. Empty, NoOpinion and LegacyAdheringComponentsOnly preserve the
// component's own defaults; StrictAllComponents and unknown values enforce
// the profile (fail-closed, per the OCP centralized TLS config enhancement).
// The enhancement recommends library-go's ShouldHonorClusterTLSProfile; that
// helper is not present in the pinned library-go version, so the semantics
// are implemented here exactly as documented.
func ShouldApplyProfile(adherence configv1.TLSAdherencePolicy) (bool, bool) {
	switch adherence {
	case configv1.TLSAdherencePolicyNoOpinion, configv1.TLSAdherencePolicyLegacyAdheringComponentsOnly:
		return false, true
	case configv1.TLSAdherencePolicyStrictAllComponents:
		return true, true
	default:
		return true, false
	}
}

// ListenerOpts returns the TLS options for a component that gates on the
// adherence policy: nil under NoOpinion/Legacy (the component keeps its own
// serving defaults), the profile options under Strict and unknown values.
// A recorded profile error is returned as fatal only when the adherence
// policy actually applies the profile. The second return reports whether the
// adherence value was a known enum.
func (r Result) ListenerOpts() ([]func(*tls.Config), bool, error) {
	applies, known := ShouldApplyProfile(r.Adherence)
	if !applies {
		return nil, known, nil
	}
	if r.ProfileErr != nil {
		return nil, known, r.ProfileErr
	}
	return r.TLSOpts, known, nil
}

// Resolve reads the cluster TLS profile from apiservers.config.openshift.io/cluster
// and returns TLS option functions for controller-runtime.
// On non-OpenShift clusters or when the profile cannot be read, it returns
// hardened Intermediate defaults. Returns an error only on unexpected failures
// that should prevent startup (fail-closed), including malformed profiles.
func Resolve(ctx context.Context, cfg *rest.Config) (Result, error) {
	scheme := runtime.NewScheme()
	if err := configv1.Install(scheme); err != nil {
		return Result{}, fmt.Errorf("installing OpenShift config scheme: %w", err)
	}

	k8sClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return Result{}, fmt.Errorf("creating bootstrap client for TLS profile: %w", err)
	}

	resolveCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	return resolve(resolveCtx, k8sClient)
}

func resolve(ctx context.Context, k8sClient client.Reader) (Result, error) {
	var result Result

	apiServer := &configv1.APIServer{}
	if err := k8sClient.Get(ctx, client.ObjectKey{Name: "cluster"}, apiServer); err != nil {
		switch {
		case meta.IsNoMatchError(err):
			log.Info("TLS profile not available, using hardened defaults (non-OpenShift cluster)")
		case apierrors.IsNotFound(err):
			log.Info("APIServer resource not found, using hardened defaults")
			// Bootstrap races can return NotFound even on OpenShift clusters:
			// mark the API available so the profile watcher registers and its
			// Create predicate catches the object's creation.
			result.APIAvailable = true
		case isTransientAPIServerError(err):
			log.Info("Transient error reading APIServer TLS profile, using hardened defaults", "error", err)
			result.APIAvailable = true
		default:
			return result, fmt.Errorf("failed to read APIServer TLS profile: %w", err)
		}
		result.ProfileSpec = *configv1.TLSProfiles[configv1.TLSProfileIntermediateType]
		result.TLSOpts = append(result.TLSOpts, intermediateWithALPN)
		return result, nil
	}

	result.APIAvailable = true
	result.AdherenceFetched = true
	result.Adherence = apiServer.Spec.TLSAdherence
	result.RawProfile = apiServer.Spec.TLSSecurityProfile
	result.ProfileSpec = *resolveProfileSpec(apiServer.Spec.TLSSecurityProfile)

	minVersion, ciphers, curves, unsupported, err := parseProfile(apiServer.Spec.TLSSecurityProfile)
	if err != nil {
		// Record the parse failure instead of returning it: callers that do
		// not apply the profile under the current adherence (the API server
		// binary under NoOpinion/Legacy) must keep serving their defaults.
		// Honor-always consumers and adherence-gated appliers treat the
		// recorded error as fatal.
		result.ProfileErr = fmt.Errorf("invalid TLS profile: %w", err)
		return result, nil
	}
	if len(unsupported) > 0 {
		log.Info("Dropping TLS profile ciphers not usable by this Go runtime", "dropped", unsupported)
	}

	result.TLSOpts = append(result.TLSOpts, func(c *tls.Config) {
		c.MinVersion = minVersion
		if len(ciphers) > 0 {
			c.CipherSuites = ciphers
		}
		if curves != nil {
			c.CurvePreferences = curves
		}
		c.NextProtos = nextProtos(minVersion, ciphers)
	})
	return result, nil
}

func isTransientAPIServerError(err error) bool {
	// Explicitly transient API statuses first.
	if apierrors.IsServiceUnavailable(err) ||
		apierrors.IsTimeout(err) ||
		apierrors.IsServerTimeout(err) ||
		apierrors.IsTooManyRequests(err) ||
		apierrors.IsInternalError(err) ||
		errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	// Reject other API-status errors before checking transport predicates.
	var statusErr *apierrors.StatusError
	if errors.As(err, &statusErr) {
		return false
	}
	// Transport-level errors for non-API-status failures.
	var netErr net.Error
	return utilnet.IsConnectionRefused(err) ||
		utilnet.IsConnectionReset(err) ||
		utilnet.IsProbableEOF(err) ||
		utilnet.IsHTTP2ConnectionLost(err) ||
		(errors.As(err, &netErr) && netErr.Timeout())
}

func intermediateWithALPN(c *tls.Config) {
	c.MinVersion = tls.VersionTLS12
	c.CipherSuites = IntermediateCiphers
	c.NextProtos = []string{"h2", "http/1.1"}
}

// EffectivePolicy returns the profile spec and adherence policy carried by
// an APIServer object, for change detection.
func EffectivePolicy(apiServer *configv1.APIServer) (configv1.TLSProfileSpec, configv1.TLSAdherencePolicy) {
	return *resolveProfileSpec(apiServer.Spec.TLSSecurityProfile), apiServer.Spec.TLSAdherence
}

// resolveProfileSpec resolves a TLSSecurityProfile to its effective spec,
// falling back to Intermediate for nil input or unrecognized shapes.
func resolveProfileSpec(profile *configv1.TLSSecurityProfile) *configv1.TLSProfileSpec {
	if profile == nil {
		return configv1.TLSProfiles[configv1.TLSProfileIntermediateType]
	}
	switch profile.Type {
	case configv1.TLSProfileModernType:
		return configv1.TLSProfiles[configv1.TLSProfileModernType]
	case configv1.TLSProfileOldType:
		return configv1.TLSProfiles[configv1.TLSProfileOldType]
	case configv1.TLSProfileCustomType:
		if profile.Custom != nil {
			return &profile.Custom.TLSProfileSpec
		}
		return configv1.TLSProfiles[configv1.TLSProfileIntermediateType]
	default:
		return configv1.TLSProfiles[configv1.TLSProfileIntermediateType]
	}
}

// parseProfile converts the security profile to an explicit minimum version,
// cipher-suite list, and curve preference list. Malformed input (unknown type,
// Custom with a nil block, unknown or missing minimum version) returns an
// error instead of a silent substitution. Old resolves to its explicit
// supported suites; requested cipher or group lists with no usable entries are
// rejected.
func parseProfile(profile *configv1.TLSSecurityProfile) (
	minVersion uint16, ciphers []uint16, curves []tls.CurveID, unsupported []string, err error,
) {
	if profile == nil {
		profile = &configv1.TLSSecurityProfile{Type: configv1.TLSProfileIntermediateType}
	}

	switch profile.Type {
	case "", configv1.TLSProfileIntermediateType:
		intermediate := configv1.TLSProfiles[configv1.TLSProfileIntermediateType]
		if intermediate == nil {
			return 0, nil, nil, nil, fmt.Errorf("Intermediate TLS profile is not defined by openshift/api")
		}
		return parseSuites(intermediate)
	case configv1.TLSProfileModernType:
		modern := configv1.TLSProfiles[configv1.TLSProfileModernType]
		if modern == nil {
			return 0, nil, nil, nil, fmt.Errorf("Modern TLS profile is not defined by openshift/api")
		}
		return parseSuites(modern)
	case configv1.TLSProfileOldType:
		old := configv1.TLSProfiles[configv1.TLSProfileOldType]
		if old == nil {
			return 0, nil, nil, nil, fmt.Errorf("Old TLS profile is not defined by openshift/api")
		}
		return parseSuites(old)
	case configv1.TLSProfileCustomType:
		if profile.Custom == nil {
			return 0, nil, nil, nil, fmt.Errorf("custom TLS profile type specified but custom block is nil")
		}
		if err := validateCustomCipherSuites(profile.Custom.Ciphers); err != nil {
			return 0, nil, nil, nil, err
		}
		return parseSuites(&profile.Custom.TLSProfileSpec)
	default:
		return 0, nil, nil, nil, fmt.Errorf("unknown TLS profile type %q", profile.Type)
	}
}

// validateCustomCipherSuites rejects Custom profiles that name RC4, 3DES, or
// SHA-1 CBC suites, accepting both IANA and OpenSSL spellings. Built-in
// profiles are exempt: Old serves those suites as documented.
func validateCustomCipherSuites(names []string) error {
	for _, name := range names {
		canonicalName := name
		if id, ok := cipherID(name); ok {
			canonicalName = tls.CipherSuiteName(id)
			if strings.HasPrefix(strings.ToLower(canonicalName), "0x") {
				if ianaNames := ocpcrypto.OpenSSLToIANACipherSuites([]string{name}); len(ianaNames) == 1 {
					canonicalName = ianaNames[0]
				}
			}
		}
		canonicalName = strings.ToUpper(canonicalName)
		if strings.Contains(canonicalName, "RC4") ||
			strings.Contains(canonicalName, "3DES") ||
			strings.Contains(canonicalName, "CBC3") ||
			strings.HasSuffix(canonicalName, "_CBC_SHA") {
			return fmt.Errorf("custom TLS profile includes disallowed cipher suite %q", name)
		}
	}
	return nil
}

// parseSuites validates a profile spec: the minimum version must be known,
// and non-empty cipher and group lists must contain at least one value usable
// by this Go runtime. TLS 1.3 cipher configuration is not possible in Go, so
// TLS 1.3 profiles carry no suites; TLS 1.3-only suites requested for lower
// minimums are reported as unsupported. An empty group list leaves curve
// selection to Go's defaults.
func parseSuites(spec *configv1.TLSProfileSpec) (uint16, []uint16, []tls.CurveID, []string, error) {
	minVersion, err := tlsVersion(spec.MinTLSVersion)
	if err != nil {
		return 0, nil, nil, nil, err
	}

	var curves []tls.CurveID
	if len(spec.Groups) > 0 {
		var unsupportedGroups []configv1.TLSGroup
		curves, unsupportedGroups = ocpcrypto.TLSGroupsToCurveIDs(spec.Groups)
		if len(curves) == 0 {
			return 0, nil, nil, nil, fmt.Errorf("TLS profile has no usable groups")
		}
		if len(unsupportedGroups) > 0 {
			log.Info("Dropping TLS profile groups not usable by this Go runtime", "dropped", unsupportedGroups)
		}
		// Hybrid ML-KEM groups only negotiate TLS 1.3; a group list with no
		// TLS 1.2-capable group below a TLS 1.3 minimum leaves TLS 1.2
		// clients unable to complete a handshake.
		if minVersion < tls.VersionTLS13 && !hasTLS12Group(curves) {
			return 0, nil, nil, nil, fmt.Errorf(
				"TLS profile with minimum %s has no groups usable for TLS 1.2: %v",
				spec.MinTLSVersion, spec.Groups)
		}
	}
	if minVersion == tls.VersionTLS13 {
		return minVersion, nil, curves, nil, nil
	}
	if len(spec.Ciphers) == 0 {
		return minVersion, nil, curves, nil, nil
	}

	ciphers, unsupported := usableCiphers(spec.Ciphers)
	if len(ciphers) == 0 {
		return 0, nil, nil, unsupported, fmt.Errorf(
			"TLS profile with minimum %s has no cipher suites usable by this Go runtime: %v",
			spec.MinTLSVersion, spec.Ciphers)
	}
	return minVersion, ciphers, curves, unsupported, nil
}

// hasTLS12Group reports whether at least one curve supports TLS 1.2
// negotiation. Hybrid ML-KEM groups (X25519MLKEM768, SecP256r1MLKEM768) are
// TLS 1.3-only; classical curves (X25519, P-256, P-384, P-521) work for
// both TLS 1.2 and TLS 1.3.
func hasTLS12Group(curves []tls.CurveID) bool {
	for _, c := range curves {
		switch c {
		case tls.X25519, tls.CurveP256, tls.CurveP384, tls.CurveP521:
			return true
		}
	}
	return false
}

func tlsVersion(v configv1.TLSProtocolVersion) (uint16, error) {
	switch v {
	case configv1.VersionTLS10:
		return tls.VersionTLS10, nil
	case configv1.VersionTLS11:
		return tls.VersionTLS11, nil
	case configv1.VersionTLS12:
		return tls.VersionTLS12, nil
	case configv1.VersionTLS13:
		return tls.VersionTLS13, nil
	case "":
		return 0, fmt.Errorf("TLS profile has no minimum version")
	default:
		return 0, fmt.Errorf("unknown minimum TLS version %q", v)
	}
}

// usableCiphers translates IANA or OpenSSL cipher names to the IDs this Go
// runtime can negotiate at TLS 1.2 or below. TLS 1.3-only suites, unknown
// names, and suites the runtime does not implement are reported as
// unsupported.
func usableCiphers(names []string) ([]uint16, []string) {
	implemented := make(map[uint16]struct{})
	for _, suite := range tls.CipherSuites() {
		implemented[suite.ID] = struct{}{}
	}
	for _, suite := range tls.InsecureCipherSuites() {
		implemented[suite.ID] = struct{}{}
	}

	ciphers := make([]uint16, 0, len(names))
	seen := make(map[uint16]struct{}, len(names))
	var unsupported []string
	for _, name := range names {
		id, ok := cipherID(name)
		if !ok {
			unsupported = append(unsupported, name)
			continue
		}
		// TLS 1.3 suite IDs are not configurable below TLS 1.3.
		if id >= 0x1301 && id <= 0x1303 {
			unsupported = append(unsupported, name)
			continue
		}
		if _, ok := implemented[id]; !ok {
			unsupported = append(unsupported, name)
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ciphers = append(ciphers, id)
	}
	return ciphers, unsupported
}

// cipherID resolves a cipher name, accepting both IANA and OpenSSL
// (library-go) spellings.
func cipherID(name string) (uint16, bool) {
	if id, err := ocpcrypto.CipherSuite(name); err == nil {
		return id, true
	}

	ianaNames := ocpcrypto.OpenSSLToIANACipherSuites([]string{name})
	if len(ianaNames) == 1 {
		if id, err := ocpcrypto.CipherSuite(ianaNames[0]); err == nil {
			return id, true
		}
	}

	return 0, false
}

// nextProtos returns the ALPN protocols for the effective configuration.
// Go's net/http requires TLS 1.2+ and an AES-128-GCM cipher suite to serve
// HTTP/2, so h2 is advertised only when the minimum version and cipher list
// can serve it; otherwise the listener is HTTP/1.1 only.
func nextProtos(minVersion uint16, ciphers []uint16) []string {
	if minVersion >= tls.VersionTLS12 && servesHTTP2(ciphers) {
		return []string{"h2", "http/1.1"}
	}
	return []string{"http/1.1"}
}

func servesHTTP2(ciphers []uint16) bool {
	if len(ciphers) == 0 {
		// Go's default suites include AES-128-GCM.
		return true
	}
	for _, id := range ciphers {
		if id == tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256 || id == tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256 {
			return true
		}
	}
	return false
}
