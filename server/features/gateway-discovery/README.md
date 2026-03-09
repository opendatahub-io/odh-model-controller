# Gateway Discovery REST API

Discover Kubernetes Gateway resources usable by a given user and target namespace, enabling UI-driven Gateway selection
during LLMInferenceService deployment.

See [Discover_Gateways.md](Discover_Gateways.md) for the original design document.

## API

```
GET /api/v1/gateways?namespace={target_namespace}
Authorization: Bearer <user-token>
```

Returns Gateways whose listeners allow the target namespace, filtered by the user's RBAC permissions (
`create llminferenceservices`). Users without access get an empty list (200 OK) to avoid leaking gateway existence.

### Response

```json
{
  "gateways": [
    {
      "name": "shared-edge-gateway",
      "namespace": "openshift-ingress",
      "listener": "https",
      "status": "Ready",
      "displayName": "Shared Edge Gateway",
      "description": "Production ingress for all AI workloads"
    }
  ]
}
```

| Field         | Description                                                 |
|---------------|-------------------------------------------------------------|
| `name`        | Gateway resource name                                       |
| `namespace`   | Gateway resource namespace                                  |
| `listener`    | Listener name that permits the target namespace             |
| `status`      | `Ready`, `NotReady`, or `Unknown` (from Gateway conditions) |
| `displayName` | From `openshift.io/display-name` annotation (optional)      |
| `description` | From `openshift.io/description` annotation (optional)       |

### Errors

| Status | Condition                                   |
|--------|---------------------------------------------|
| 400    | Missing or invalid `namespace` parameter    |
| 401    | Missing or malformed `Authorization` header |
| 405    | Non-GET method                              |
| 500    | Internal error                              |

## Listener Filtering

Each Gateway listener is checked against the target namespace using `allowedRoutes.namespaces.from`:

- **All** — any namespace allowed
- **Same** — only the Gateway's own namespace (also the default when unset)
- **Selector** — namespaces matching the listener's label selector

## Security

- The user's token is used only for RBAC checks (`SelfSubjectAccessReview`), never for cluster-wide reads
- The ServiceAccount token handles Gateway listing and Namespace lookups
- Namespace input is validated against RFC 1123 before any K8s API call

## Configuration

| Variable                 | Default | Description                                          |
|--------------------------|---------|------------------------------------------------------|
| `LISTEN_ADDR`            | `:8443` | Server listen address                                |
| `TLS_CERT_DIR`           |         | Directory containing `tls.crt` and `tls.key` (required) |
| `LOG_LEVEL`              | `info`  | `debug`, `info`, `warn`, `error`                     |
| `GATEWAY_LABEL_SELECTOR` |         | Comma-separated `key=value` pairs to filter Gateways |

## Testing

```bash
# Unit tests
go test ./server/...

# E2E tests (requires deployed server + oc login)
make test-e2e-server
```