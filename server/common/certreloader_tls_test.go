package common

import (
	"crypto/tls"
	"testing"
)

func TestNewTLSConfigIncludesMLKEMCurve(t *testing.T) {
	// Keep this as a configuration-level check: runtime-specific FIPS filtering
	// is exercised by the live TLS handshake validation rather than this unit test.
	config := NewTLSConfig(func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		return nil, nil
	})

	for _, curve := range config.CurvePreferences {
		if curve == tls.X25519MLKEM768 {
			return
		}
	}

	t.Fatalf("expected TLS config to include %v, got %v", tls.X25519MLKEM768, config.CurvePreferences)
}
