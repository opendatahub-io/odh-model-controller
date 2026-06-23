# Samples API

This document describes the API endpoint that serves `LLMInferenceServiceConfig` sample templates to the llm-d
deployment wizard UI.

**Status:** Implemented 
**Author:** [Pierangelo Di Pilato](https://github.com/pierDipi)  
**Date:** Jun 22, 2026

## Motivation

The UI deployment wizard needs to pre-populate YAML editors with `LLMInferenceServiceConfig` templates based on the
selected workload topology or router type. Rather than hardcoding templates in the dashboard, the odh-model-controller
server serves them as static, heavily commented YAML files embedded at build time.

Since `LLMInferenceServiceConfig` and `LLMInferenceService` share the same spec schema, the upstream kserve samples can
be adapted directly into config templates.

## API

```
GET /api/v1/llm-d/samples?type=<config-type>[&topology=<topology>]
```

### Query Parameters

| Parameter  | Required | Description                                                                                                                                                     |
|------------|----------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `type`     | Yes      | The `opendatahub.io/config-type` label value                                                                                                                    |
| `topology` | No       | Workload topology hint. Only meaningful when `type=router`. If the topology ends with `-pd`, the P/D disaggregated router sample is returned instead of the default. |

### Responses

| Status | Content-Type       | Body                                                  |
|--------|--------------------|-------------------------------------------------------|
| 200    | `application/yaml` | Single YAML document (`LLMInferenceServiceConfig` CR) |
| 400    | `application/json` | `{"error": "missing required query parameter: type"}` |
| 404    | `application/json` | `{"error": "no sample found for type: <type>"}`       |
| 405    | `application/json` | `{"error": "method not allowed"}`                     |

### Examples

```
GET /api/v1/llm-d/samples?type=workload-single-node-pd
```

```
GET /api/v1/llm-d/samples?type=router&topology=workload-single-node-pd
```

```yaml
# 200 OK
# Content-Type: application/yaml
apiVersion: serving.kserve.io/v1alpha2
kind: LLMInferenceServiceConfig
metadata:
  name: single-node-pd-roce
  labels:
    opendatahub.io/config-type: workload-single-node-pd
  annotations:
    description: Single node prefill/decode disaggregated serving with RoCE RDMA
spec:
# ...
```

### Authentication

No authentication required — these are static public templates.

## Available Sample Types

Each config-type maps to one embedded YAML file:

| Config Type                            | Description                                                         |
|----------------------------------------|---------------------------------------------------------------------|
| `workload-single-node`                 | Standard single-instance GPU deployment                             |
| `workload-single-node-pd`              | Prefill/Decode disaggregation on a single node with RDMA            |
| `workload-multi-node-data-parallel`    | Replicated model instances across multiple nodes                    |
| `workload-multi-node-data-parallel-pd` | P/D disaggregation with data-parallel scaling                       |
| `router`                               | Scheduler with endpoint picker plugins for routing and flow control |
| `router` + `topology=*-pd`            | P/D disaggregated scheduler with separate prefill/decode profiles   |

## Implementation

### Static YAML Embedding

Sample YAML files live under `server/samples/` as flat files. Filenames (minus `.yaml` extension) match the config-type
value. Go's `//go:embed` directive bundles them into the binary at build time.

```
server/samples/
├── embed.go
├── workload-single-node.yaml
├── workload-single-node-pd.yaml
├── workload-multi-node-data-parallel.yaml
├── workload-multi-node-data-parallel-pd.yaml
├── router.yaml
└── router-pd.yaml
```

The `samples.ByType(configType)` function reads the corresponding file from the embedded filesystem.

### Handler

`SamplesHandler` in `server/handlers/samples.go` follows the existing `GatewayHandler` pattern:

- Validates HTTP method (GET only)
- Extracts `type` query parameter
- Extracts optional `topology` query parameter — when `type=router` and topology ends with `-pd`, resolves to the P/D router sample
- Calls `samples.ByType()` to look up the embedded YAML
- Returns the raw YAML bytes with `Content-Type: application/yaml`

### Route Registration

In `server/server.go`, the handler is registered without auth middleware:

```go
samplesHandler := &handlers.SamplesHandler{}
mux.Handle("/api/v1/llm-d/samples", samplesHandler)
```

### Sample Content

Each YAML file is a complete `LLMInferenceServiceConfig` CR with:

- `opendatahub.io/config-type` label
- `annotations.description` field
- `opendatahub.io/supported-topologies` annotation on router samples (JSON array of compatible topology types)
- Extensive inline YAML comments on every field explaining purpose, valid alternatives, and how to adapt for different
  infrastructure

### Sample Sources

| Sample                                 | Upstream reference                                                                                                |
|----------------------------------------|-------------------------------------------------------------------------------------------------------------------|
| `workload-single-node`                 | `kserve/docs/samples/llmisvc/single-node-gpu/llm-inference-service-qwen2-7b-gpu.yaml`                             |
| `workload-single-node-pd`              | Design doc P/D example + `kserve/docs/samples/llmisvc/single-node-gpu/llm-inference-service-pd-qwen2-7b-gpu.yaml` |
| `workload-multi-node-data-parallel`    | `kserve/docs/samples/llmisvc/dp-ep/` (simplified)                                                                 |
| `workload-multi-node-data-parallel-pd` | `kserve/docs/samples/llmisvc/dp-ep/` P/D variant (simplified)                                                     |
| `router`                               | Design doc router example (simplified)                                                                            |
| `router-pd`                            | P/D disaggregated variant with prefill/decode scheduling profiles                                                 |

## Testing

### Unit Tests

`server/handlers/samples_test.go` — follows the `gateways_test.go` pattern using `httptest`:

- Valid type returns 200 with YAML content and correct `Content-Type` header
- Missing `type` parameter returns 400
- Unknown type returns 404
- Non-GET method returns 405

### E2E Tests

`server/test/e2e/samples_test.go` — follows the existing e2e patterns (build tag `e2e`, uses `testutil.Env`,
`t.Parallel()`):

- Fetches each known sample type and validates 200 + YAML response
- Verifies YAML starts with valid `LLMInferenceServiceConfig` structure
- Missing `type` parameter returns 400
- Unknown type returns 404
- Non-GET method returns 405

No auth, namespaces, or K8s resources needed — samples are static.

Run with: `make test-e2e-server` (defined in `Makefile:132`, requires the server deployed and `oc` logged in)