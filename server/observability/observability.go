package observability

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	otelprometheus "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"

	otlpgrpc "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Config holds the observability configuration.
type Config struct {
	MetricsAddr  string      // HTTPS listen address for Prometheus /metrics endpoint (default ":9090")
	TLSConfig    *tls.Config // TLS configuration for the metrics server
	OTLPEndpoint string      // Optional OTLP collector endpoint; enables trace export when set
	ServiceName  string      // OTel service name (default "model-serving-api")
}

// Setup initializes OpenTelemetry metrics (Prometheus exporter) and optional
// tracing (OTLP gRPC exporter). It starts an HTTPS server on MetricsAddr
// serving the /metrics endpoint and returns a shutdown function that cleans
// up all resources.
func Setup(ctx context.Context, cfg Config) (shutdown func(context.Context) error, err error) {
	if cfg.ServiceName == "" {
		cfg.ServiceName = "model-serving-api"
	}

	var shutdownFuncs []func(context.Context) error

	res, err := resource.Merge(
		resource.Default(),
		resource.NewSchemaless(
			semconv.ServiceName(cfg.ServiceName),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create otel resource: %w", err)
	}

	// Prometheus exporter registers a collector with the default Prometheus registry.
	promExporter, err := otelprometheus.New()
	if err != nil {
		return nil, fmt.Errorf("failed to create prometheus exporter: %w", err)
	}

	meterProvider := metric.NewMeterProvider(
		metric.WithResource(res),
		metric.WithReader(promExporter),
	)
	otel.SetMeterProvider(meterProvider)
	shutdownFuncs = append(shutdownFuncs, meterProvider.Shutdown)

	// Optional OTLP trace exporter.
	// TLS is used by default; set OTEL_EXPORTER_OTLP_INSECURE=true to disable.
	if cfg.OTLPEndpoint != "" {
		traceExporter, err := otlpgrpc.New(ctx,
			otlpgrpc.WithEndpoint(cfg.OTLPEndpoint),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create otlp trace exporter: %w", err)
		}

		tracerProvider := sdktrace.NewTracerProvider(
			sdktrace.WithResource(res),
			sdktrace.WithBatcher(traceExporter),
		)
		otel.SetTracerProvider(tracerProvider)
		shutdownFuncs = append(shutdownFuncs, tracerProvider.Shutdown)

		slog.Info("otlp trace export enabled", "endpoint", cfg.OTLPEndpoint)
	}

	// HTTPS metrics server.
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

	metricsSrv := &http.Server{
		Addr:              cfg.MetricsAddr,
		Handler:           mux,
		TLSConfig:         cfg.TLSConfig,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		slog.Info("metrics server starting", "addr", cfg.MetricsAddr)
		if err := metricsSrv.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			slog.Error("metrics server error", "error", err)
		}
	}()

	// Prepend metrics server shutdown so it stops first.
	shutdownFuncs = append([]func(context.Context) error{metricsSrv.Shutdown}, shutdownFuncs...)

	return func(ctx context.Context) error {
		var errs []error
		for _, fn := range shutdownFuncs {
			if err := fn(ctx); err != nil {
				errs = append(errs, err)
			}
		}
		return errors.Join(errs...)
	}, nil
}
