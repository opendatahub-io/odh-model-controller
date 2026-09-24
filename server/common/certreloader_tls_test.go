package common

import (
	"context"
	"crypto/tls"
	"errors"
	"testing"
)

func TestNewTLSConfigIncludesMLKEMCurve(t *testing.T) {
	t.Parallel()

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

func TestTLSConfigNegotiatesMLKEMCurve(t *testing.T) {
	t.Parallel()

	certPEM, keyPEM, err := createTestCertificate(1)
	if err != nil {
		t.Fatalf("failed to create test certificate: %v", err)
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("failed to load test certificate: %v", err)
	}

	serverConfig := NewTLSConfig(func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		return &cert, nil
	})
	listener, err := tls.Listen("tcp", "127.0.0.1:0", serverConfig)
	if err != nil {
		t.Fatalf("failed to start TLS listener: %v", err)
	}
	defer func() {
		if err := listener.Close(); err != nil {
			t.Errorf("failed to close TLS listener: %v", err)
		}
	}()

	serverErr := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		hsErr := conn.(*tls.Conn).Handshake()
		serverErr <- errors.Join(hsErr, conn.Close())
	}()

	clientConfig := &tls.Config{
		MinVersion:         tls.VersionTLS13,
		MaxVersion:         tls.VersionTLS13,
		InsecureSkipVerify: true, //nolint:gosec // test certificate is self-signed
		CurvePreferences:   []tls.CurveID{tls.X25519MLKEM768},
	}
	client := tls.Dialer{Config: clientConfig}
	conn, err := client.DialContext(context.Background(), "tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("ML-KEM TLS handshake failed: %v", err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			t.Errorf("failed to close client connection: %v", err)
		}
	}()

	if err := <-serverErr; err != nil {
		t.Fatalf("server TLS handshake failed: %v", err)
	}
	if got := conn.(*tls.Conn).ConnectionState().CurveID; got != tls.X25519MLKEM768 {
		t.Fatalf("expected negotiated curve %v, got %v", tls.X25519MLKEM768, got)
	}
}
