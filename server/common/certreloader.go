package common

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
)

// debounceDelay waits for events to settle before reloading.
const debounceDelay = 250 * time.Millisecond

// CertReloader watches a directory for TLS certificate changes and atomically
// swaps the certificate when files are updated. This supports Kubernetes-style
// secret volume mounts where certificates are rotated via symlink swaps.
type CertReloader struct {
	cert *atomic.Pointer[tls.Certificate]
}

// NewCertReloader creates a CertReloader that watches the given directory for
// changes to tls.crt and tls.key files. The init parameter provides the
// initial certificate to serve until a reload occurs. The watcher goroutine
// runs until ctx is cancelled.
func NewCertReloader(ctx context.Context, path string, init *tls.Certificate) (*CertReloader, error) {
	certPtr := &atomic.Pointer[tls.Certificate]{}
	certPtr.Store(init)

	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create cert watcher: %w", err)
	}

	if err := w.Add(path); err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("failed to watch %q: %w", path, err)
	}

	go func() {
		defer func() {
			if err := w.Close(); err != nil {
				slog.Error("failed to close cert watcher", "path", path, "error", err)
			}
		}()

		var debounceTimer *time.Timer

		for {
			select {
			case ev := <-w.Events:
				slog.Debug("cert changed", "event", ev, "path", path)

				if ev.Op&(fsnotify.Write|fsnotify.Create) == 0 {
					continue
				}

				// Debounce: reset the timer if we get another event.
				if debounceTimer != nil {
					debounceTimer.Stop()
				}

				debounceTimer = time.AfterFunc(debounceDelay, func() {
					cert, err := tls.LoadX509KeyPair(filepath.Join(path, "tls.crt"), filepath.Join(path, "tls.key"))
					if err != nil {
						slog.Error("failed to reload TLS certificate", "path", path, "error", err)
						return
					}
					certPtr.Store(&cert)
					slog.Debug("reloaded TLS certificate", "path", path)
				})

			case err := <-w.Errors:
				if err != nil {
					slog.Error("cert watcher failed", "path", path, "error", err)
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	return &CertReloader{cert: certPtr}, nil
}

// Get returns the current TLS certificate. This is safe for concurrent use.
func (r *CertReloader) Get() *tls.Certificate {
	return r.cert.Load()
}

// NewTLSConfig returns a FIPS-compliant *tls.Config that uses the given
// GetCertificate callback for certificate selection. It restricts cipher
// suites to ECDHE+AES-GCM and curves to NIST P-256/P-384.
func NewTLSConfig(getCertificate func(*tls.ClientHelloInfo) (*tls.Certificate, error)) *tls.Config {
	return &tls.Config{
		MinVersion:     tls.VersionTLS12,
		GetCertificate: getCertificate,
		CipherSuites: []uint16{
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
		},
		CurvePreferences: []tls.CurveID{
			tls.CurveP256,
			tls.CurveP384,
		},
	}
}
