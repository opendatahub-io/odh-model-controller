package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/opendatahub-io/odh-model-controller/server/gateway"
	"github.com/opendatahub-io/odh-model-controller/server/telemetry"
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

	discoverer := &gateway.KubeDiscoverer{
		SAClient:             saClient,
		AccessChecker:        accessChecker,
		GatewayLabelSelector: cfg.GatewayLabelSelector,
	}

	srv := NewServer(cfg, discoverer)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if cfg.TLSCertFile == "" || cfg.TLSKeyFile == "" {
		slog.Error("TLS is required: set TLS_CERT_FILE and TLS_KEY_FILE")
		os.Exit(1)
	}

	shutdownTelemetry, err := telemetry.Setup(ctx, telemetry.Config{
		MetricsAddr:  cfg.MetricsAddr,
		TLSCertFile:  cfg.TLSCertFile,
		TLSKeyFile:   cfg.TLSKeyFile,
		OTLPEndpoint: cfg.OTLPEndpoint,
	})
	if err != nil {
		slog.Error("failed to setup telemetry", "error", err)
		os.Exit(1)
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("server starting", "addr", cfg.ListenAddr, "tls", true)
		if err := srv.ListenAndServeTLS(cfg.TLSCertFile, cfg.TLSKeyFile); err != nil && err != http.ErrServerClosed {
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
