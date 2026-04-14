# model-serving-api

Standalone REST API server that provides gateway discovery for the Model Serving UI. It runs as a separate deployment
alongside the odh-model-controller.

## Features

- [Gateway Discovery](docs/features/gateway-discovery/README.md) — discover Kubernetes Gateway resources usable by a given
  user and target namespace
- [Observability](docs/features/observability/README.md) — Prometheus metrics and optional OTLP tracing via OpenTelemetry

## Project Structure

```
server/
├── main.go              # Server bootstrap, K8s client init, graceful shutdown
├── config.go            # Env var config loading (listen addr, TLS)
├── server.go            # HTTP mux, middleware chain
├── handlers/            # HTTP handlers (gateways, health probes)
├── gateway/             # Gateway discovery logic, filtering, types
├── middleware/           # Auth, logging, security headers, recovery
├── httputil/             # Shared HTTP response helpers
├── features/            # Feature specifications
└── test/e2e/            # End-to-end tests
```

## Development

### Building

```shell
make build-server
```

### Running Locally

The server requires a kubeconfig and TLS certificates. Environment variables:

| Variable                      | Default | Description                                                                    |
|-------------------------------|---------|--------------------------------------------------------------------------------|
| `LISTEN_ADDR`                 | `:8443` | Address to listen on                                                           |
| `TLS_CERT_DIR`                |         | Directory containing `tls.crt` and `tls.key` (required)                        |
| `LOG_LEVEL`                   | `info`  | Log level (`debug`, `info`, `warn`, `error`)                                   |
| `GATEWAY_LABEL_SELECTOR`      |         | Comma-separated `key=value` pairs to filter Gateways                           |
| `METRICS_ADDR`                | `:8080` | HTTPS address for Prometheus `/metrics` endpoint (reuses main TLS certs)       |
| `OTEL_EXPORTER_OTLP_ENDPOINT` |         | OTLP collector endpoint; enables trace export when set (e.g. `localhost:4317`) |

### Container Image

```shell
make container-build-server
make container-push-server
```

### Deploying

```shell
make deploy-server NAMESPACE=opendatahub
```

### Running Tests

Unit tests:

```shell
go test ./server/...
```

E2E tests (requires a deployed server and `oc` login):

```shell
make test-e2e-server
```

## Manual Testing

Query the gateway endpoint from inside the cluster using your own token:

```shell
QUERY_NAMESPACE="default"

SA_TOKEN=$(oc whoami -t) && \
  oc exec -n default dnsutils -- curl -sk \
    -H "Authorization: Bearer $SA_TOKEN" \
    "https://model-serving-api.opendatahub.svc.cluster.local/api/v1/gateways?namespace=${QUERY_NAMESPACE}" | jq
```

Or using a ServiceAccount token:

```shell
SA_NAME="default"
SA_NAMESPACE="default"
QUERY_NAMESPACE="default"

SA_TOKEN=$(oc create token "${SA_NAME}" -n "${SA_NAMESPACE}") && \
  oc exec -n default dnsutils -- curl -sk \
    -H "Authorization: Bearer $SA_TOKEN" \
    "https://model-serving-api.opendatahub.svc.cluster.local/api/v1/gateways?namespace=${QUERY_NAMESPACE}" | jq
```
