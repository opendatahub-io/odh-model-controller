# Observability

Prometheus-compatible metrics and optional distributed tracing via OpenTelemetry.

## Metrics

The `otelhttp` middleware automatically records HTTP server metrics, exported via a Prometheus `/metrics` endpoint on a
dedicated HTTPS port (`:9090`, reusing the main server's TLS certs). A `ServiceMonitor` (
`config/server/servicemonitor.yaml`) enables automatic scraping by Prometheus Operator.

Metrics emitted (Prometheus exposition names):

| Metric                                  | Type      |
|-----------------------------------------|-----------|
| `http_server_duration_milliseconds`     | Histogram |
| `http_server_request_size_bytes_total`  | Counter   |
| `http_server_response_size_bytes_total` | Counter   |

All metrics include `http_method`, `http_status_code`, `http_scheme`, `net_host_name`, and `net_protocol_version`
attributes.

## Traces

Optional OTLP trace export via gRPC, enabled by setting `OTEL_EXPORTER_OTLP_ENDPOINT`. The `otelhttp` middleware creates
spans for each request automatically.

## Configuration

| Variable                      | Default | Description                                            |
|-------------------------------|---------|--------------------------------------------------------|
| `METRICS_ADDR`                | `:9090` | HTTPS listen address for `/metrics`                    |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | (none)  | OTLP collector endpoint; enables trace export when set |