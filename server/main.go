package main

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/opendatahub-io/odh-model-controller/server/common"
	"github.com/opendatahub-io/odh-model-controller/server/gateway"
	"github.com/opendatahub-io/odh-model-controller/server/observability"
)

func main() {
	cfg, err := LoadConfig()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	initLogging(cfg.LogLevel)

	scheme := runtime.NewScheme()
	utilruntime.Must(gatewayapiv1.Install(scheme))
	utilruntime.Must(corev1.AddToScheme(scheme))

	restCfg := ctrl.GetConfigOrDie()

	saClient, err := client.New(restCfg, client.Options{Scheme: scheme})
	if err != nil {
		slog.Error("failed to create kubernetes client", "error", err)
		os.Exit(1)
	}

	accessChecker, err := gateway.NewSelfSubjectAccessChecker(restCfg)
	if err != nil {
		slog.Error("failed to create access checker", "error", err)
		os.Exit(1)
	}

	discoverer, err := gateway.NewKubeDiscoverer(saClient, accessChecker, cfg.GatewayLabelSelector)
	if err != nil {
		slog.Error("failed to create gateway discoverer", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if cfg.TLSCertDir == "" {
		slog.Error("TLS is required: set TLS_CERT_DIR")
		os.Exit(1)
	}

	certFile := filepath.Join(cfg.TLSCertDir, "tls.crt")
	keyFile := filepath.Join(cfg.TLSCertDir, "tls.key")

	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		slog.Error("failed to load TLS key pair", "certFile", certFile, "keyFile", keyFile, "error", err)
		os.Exit(1)
	}

	reloader, err := common.NewCertReloader(ctx, cfg.TLSCertDir, &cert)
	if err != nil {
		slog.Error("failed to start certificate reloader", "error", err)
		os.Exit(1)
	}

	getCertificate := func(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
		return reloader.Get(), nil
	}

	tlsConfig := common.NewTLSConfig(getCertificate)

	srv := NewServer(cfg, discoverer, tlsConfig)

	shutdownTelemetry, err := observability.Setup(ctx, observability.Config{
		MetricsAddr:  cfg.MetricsAddr,
		TLSConfig:    tlsConfig,
		OTLPEndpoint: cfg.OTLPEndpoint,
	})
	if err != nil {
		slog.Error("failed to setup telemetry", "error", err)
		os.Exit(1)
	}

	ln, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		slog.Error("failed to listen", "addr", cfg.ListenAddr, "error", err)
		os.Exit(1)
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("server starting", "addr", cfg.ListenAddr, "tls", true)
		if err := srv.ServeTLS(ln, "", ""); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		slog.Error("server error", "error", err)
		os.Exit(1)
	case <-ctx.Done():
	}

	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := shutdownTelemetry(shutdownCtx); err != nil {
		slog.Error("telemetry shutdown error", "error", err)
	}

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown error", "error", err)
		os.Exit(1)
	}
}

func initLogging(level string) {
	var logLevel slog.Level
	switch level {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel})))
}
