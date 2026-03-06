# Observability — Implementation Specification

This document specifies the observability setup for the model-serving-api server, covering Prometheus-compatible metrics and optional distributed tracing via OpenTelemetry.

## Overview

The server uses the OpenTelemetry SDK to provide:

- **Metrics**: Automatic HTTP server metrics via `otelhttp` middleware, exported through a Prometheus-compatible `/metrics` endpoint on a dedicated HTTPS port (`:9090`)
- **Traces**: Optional OTLP trace export, enabled by setting the `OTEL_EXPORTER_OTLP_ENDPOINT` environment variable

## Architecture

```
                                 ┌──────────────────────┐
                                 │  model-serving-api   │
                                 │                      │
  HTTP request ──────────────────►  :8443 (main server) │
                                 │    otelhttp middleware│
                                 │      ▼               │
                                 │  MeterProvider       │
                                 │  (Prometheus reader) │
                                 │      │               │
  Prometheus ◄───── /metrics ────┤  :9090 (metrics HTTPS)│
                                 │                      │
                                 │  TracerProvider      │
                                 │  (optional)          │
                                 │      │               │
  OTLP Collector ◄──── gRPC ────┤  (if configured)     │
                                 └──────────────────────┘
```

### Components

1. **`otelhttp` middleware** wraps the HTTP handler chain, automatically recording request metrics and creating trace spans
2. **Prometheus exporter** registers an OTel metric reader with the default Prometheus registry
3. **Metrics HTTPS server** on `:9090` serves `promhttp.Handler()` using the same TLS certificates as the main server
4. **OTLP trace exporter** (optional) sends spans to a collector via gRPC when `OTEL_EXPORTER_OTLP_ENDPOINT` is set

## Metrics Emitted

The `otelhttp` middleware automatically emits the following metrics, shown here with their Prometheus exposition names:

| Prometheus Metric                       | Type      | Attributes                                                                                |
|-----------------------------------------|-----------|-------------------------------------------------------------------------------------------|
| `http_server_duration_milliseconds`     | Histogram | `http_method`, `http_status_code`, `http_scheme`, `net_host_name`, `net_protocol_version` |
| `http_server_request_size_bytes_total`  | Counter   | `http_method`, `http_status_code`, `http_scheme`, `net_host_name`, `net_protocol_version` |
| `http_server_response_size_bytes_total` | Counter   | `http_method`, `http_status_code`, `http_scheme`, `net_host_name`, `net_protocol_version` |

## Environment Variables

| Variable                      | Default | Description                                                                         |
|-------------------------------|---------|-------------------------------------------------------------------------------------|
| `METRICS_ADDR`                | `:9090` | HTTPS listen address for the Prometheus `/metrics` endpoint (reuses main TLS certs) |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | (none)  | OTLP collector endpoint (e.g. `localhost:4317`); enables trace export when set      |

## Kubernetes Integration

A `ServiceMonitor` resource (`config/server/servicemonitor.yaml`) is included for Prometheus Operator to automatically discover and scrape the metrics endpoint:

```yaml
spec:
  endpoints:
    - path: /metrics
      port: metrics
      scheme: https
      tlsConfig:
        insecureSkipVerify: true
  selector:
    matchLabels:
      app: model-serving-api
```

The Service exposes the metrics port at `9090`, and the Deployment declares the container port `9090` named `metrics`.

## Verification

### Local

```bash
# Build and run
make build-server
TLS_CERT_FILE=... TLS_KEY_FILE=... ./bin/model-serving-api

# Check metrics endpoint
curl -sk https://localhost:9090/metrics

# Generate some traffic, then check for HTTP metrics
curl -sk https://localhost:8443/healthz
curl -sk https://localhost:9090/metrics | grep http_server
```

### In-cluster

```bash
# Port-forward the metrics port
kubectl port-forward -n opendatahub svc/model-serving-api 9090:9090

# Check metrics
curl -sk https://localhost:9090/metrics | grep http_server
```

## Telemetry Lifecycle

1. **Startup**: `telemetry.Setup()` is called after config loading and TLS validation, before the main HTTP server starts
2. **Runtime**: The `otelhttp` middleware records metrics and (optionally) creates trace spans for every request
3. **Shutdown**: The combined shutdown function is called during graceful shutdown, stopping the metrics server, flushing the MeterProvider, and (if configured) flushing the TracerProvider