# ODH Model Controller

ODH Model Controller is a companion Kubernetes controller to [KServe](https://github.com/kserve/kserve) that augments KServe with OpenShift-specific and Open Data Hub platform features. It manages OpenShift routes, monitoring integration, NIM account lifecycle, multi-node Ray TLS certificates, Model Registry sync, LLMInferenceService auth policies, and Gateway API integration.

## Constraints

- **Generated files are read-only.** Never hand-edit files produced by `controller-gen`, `kustomize`, or codegen. These include:
  - `api/nim/v1/zz_generated.deepcopy.go`
  - `config/crd/bases/` CRD manifests
  - `config/rbac/role.yaml` (generated from `+kubebuilder:rbac` markers)
- **External CRDs are downloaded, not authored.** Files under `config/crd/external/` are fetched from upstream KServe via `make manifests-update`. The revision is tracked in `config/crd/external/.kserve_manifests_revision`.
- **Makefile is the source of truth.** Read `Makefile` before running or modifying build steps. Do not invent `go` commands when a Make target exists.
- **KServe dependency is replaced.** `go.mod` replaces `github.com/kserve/kserve` with the `opendatahub-io/kserve` fork. Several k8s libraries are pinned to `v0.34.x` and `controller-runtime` is pinned to `v0.19.7` (keda compatibility). Check `replace` directives before updating dependencies.
- **RawDeployment mode only.** This controller exclusively supports KServe's `RawDeployment` mode (no Knative/Serverless). Do not add Serverless-mode logic.
- **Feature gating via environment variables.** Controller behavior is toggled by env vars (`NIM_STATE`, `MODELREGISTRY_STATE`, `ENABLE_WEBHOOKS`, `MONITORING_NAMESPACE`, `POD_NAMESPACE`). Check `cmd/main.go` for the full list before adding new feature gates.

## Project Structure

```
cmd/main.go                          # Manager entrypoint — wires all controllers and webhooks
api/nim/v1/                          # NIM Account CRD types (the only CRD owned by this repo)
internal/controller/
  serving/
    inferenceservice_controller.go   # InferenceService reconciler (routes, certs, metrics, KEDA, Model Registry)
    servingruntime_controller.go     # ServingRuntime reconciler (monitoring RoleBindings, multi-node Ray TLS)
    inferencegraph_controller.go     # InferenceGraph reconciler (stub/placeholder)
    reconcilers/                     # Sub-reconcilers for ISVC (route, metrics, KEDA, clusterrolebinding, model registry)
    llm/
      llm_inferenceservice_controller.go  # LLMInferenceService reconciler (AuthPolicy for MaaS)
      gateway_controller.go               # Gateway reconciler (EnvoyFilter + AuthPolicy bootstrap)
      fixture/                            # envtest builders and helpers for LLM tests
      reconcilers/                        # LLM sub-reconcilers (AuthPolicy)
  core/
    configmap_controller.go          # Watches CA bundle ConfigMaps → aggregates into kserve trust bundle
    secret_controller.go             # Watches managed Secrets → triggers ISVC re-reconcile
    pod_controller.go                # Watches predictor Pods → emits metrics
  nim/
    account_controller.go            # NIM Account reconciler (API key validation, model config, templates, pull secrets)
    handlers/                        # NIM handler chain (validation → configmap → template → pull secret)
  comparators/                       # Resource comparators for delta processing
  constants/                         # Shared constants, labels, annotations
  processors/                        # DeltaProcessor for desired-vs-actual reconciliation
  resources/                         # Resource builders (AuthPolicy, EnvoyFilter, Route, NetworkPolicy, etc.)
  testing/                           # Shared envtest setup (Config, Client, Cleaner, WithCRDs, WithScheme)
  utils/                             # Shared utilities (CRD detection, cert generation, condition helpers)
internal/webhook/
  core/v1/pod_webhook.go             # Mutating webhook — injects Ray TLS init containers
  nim/v1/account_webhook.go          # Validating webhook — NIM Account validation
  serving/v1alpha1/                   # InferenceGraph validating webhook
  serving/v1beta1/                    # InferenceService validating + defaulting webhooks
server/                              # model-serving-api — standalone REST API for gateway discovery
config/
  base/                              # Kustomize base (params.env with image refs)
  crd/                               # CRD manifests (bases/ = generated, external/ = downloaded from KServe)
  rbac/                              # Generated RBAC from kubebuilder markers
  runtimes/                          # ServingRuntime templates (vLLM, OVMS, MLServer, etc.)
  server/                            # Kustomize overlay for model-serving-api deployment
  webhook/                           # Webhook configuration manifests
test/                                # E2E tests, test CRDs, test data, custom Gomega matchers
```

## Commands

Prerequisites: Go (version in `go.mod`), `make`, and optionally `podman`/`docker`.

```
make build                  # Build the controller binary (bin/manager)
make build-server           # Build the model-serving-api binary (bin/model-serving-api)
make test                   # Run all unit/integration tests with envtest (downloads assets automatically)
make manifests              # Regenerate CRDs, RBAC, and webhook manifests from markers
make generate               # Regenerate DeepCopy methods
make fmt                    # Run go fmt
make vet                    # Run go vet
make lint                   # Run golangci-lint (v2 config in .golangci.yml)
make lint-fix               # Run golangci-lint with --fix
make container-build        # Build controller container image
make container-build-server # Build model-serving-api container image
```

For faster iteration on a specific package, run `make envtest` once, then:

```
KUBEBUILDER_ASSETS="$(make -s print-envtest-assets)" POD_NAMESPACE=default \
  go test ./internal/controller/serving/... -run TestSpecificName -v
```

Or equivalently (the version is derived from `go.mod`, not hardcoded):

```
KUBEBUILDER_ASSETS="$(bin/setup-envtest use $(go list -m -f '{{ if .Replace }}{{ .Replace.Version }}{{ else }}{{ .Version }}{{ end }}' k8s.io/api | awk -F'[v.]' '{printf "1.%d", $3}') --bin-dir bin -p path)" POD_NAMESPACE=default \
  go test ./internal/controller/serving/... -run TestSpecificName -v
```

### E2E Tests

```
make test-e2e                   # Controller e2e on local Kind cluster
make test-e2e-server            # model-serving-api e2e (requires oc login + deployed server)
make test-e2e-controller        # Controller e2e (requires controller + gateway + Authorino deployed)
make test-e2e-kserve-ocp        # Full KServe e2e on OpenShift (clones opendatahub-io/kserve, runs its test suite)
```

## Testing

Tests live next to the code they test. The codebase uses two testing styles — match the style of the package you're modifying.

### Standard Go tests (`func Test...` + testify)

Use for **pure logic that doesn't need a running API server**: comparators, utility functions, resource builders, certificate helpers, gateway filtering logic.

Examples:
- `internal/controller/comparators/*_test.go` — struct comparison logic
- `internal/controller/utils/cert_test.go` — certificate generation
- `internal/controller/serving/llm/gateway_filter_test.go` — namespace/listener filtering
- `internal/controller/resources/unit_test.go` — resource builder output

These tests use `func Test...(t *testing.T)` with `assert`/`require` from testify. No envtest, no suite_test.go needed.

### Ginkgo + envtest

Use for **controller and webhook tests that need a real API server** (reconcile loops, watches, webhook admission). These tests create/read/update/delete Kubernetes resources against an in-process API server.

Examples:
- `internal/controller/serving/*_test.go` — ISVC and ServingRuntime reconciler tests
- `internal/controller/nim/account_controller_test.go` — NIM Account reconciler tests
- `internal/controller/serving/llm/*_test.go` — LLM and Gateway controller tests
- `internal/webhook/serving/v1beta1/*_test.go` — webhook admission tests

Key patterns:
- Each package has a `suite_test.go` that starts/stops envtest via `testing.Configure(WithCRDs(...), WithScheme(...)).WithControllers(...).Start(ctx)` — use this builder, not raw `envtest.Environment{}`
- The returned `*testing.Client` embeds `client.Client` and a `Cleaner` for resource cleanup
- Use `Eventually`/`Consistently` for assertions, never `time.Sleep`
- `POD_NAMESPACE=default` must be set (some controllers read it at runtime)
- envtest runs a real API server and etcd but NOT built-in controllers or garbage collection
- The LLM controller has its own `fixture/` package with fluent builders and assertion helpers
- Custom Gomega matchers live in `test/matchers/`
- Test CRDs for optional APIs (EnvoyFilter, AuthPolicy, Gateway API) are in `test/crds/`

## Pull Requests

Use the template in `.github/PULL_REQUEST_TEMPLATE.md`. Every PR must:
- Link JIRA ticket(s) in the description
- Squash commits with meaningful messages
- Include testing instructions
- Confirm manual testing was performed

## Gotchas

For detailed architecture, controller patterns, and design rationale, see [architecture.md](architecture.md).

- **Label-filtered caches.** The manager caches only Secrets with `opendatahub.io/managed=true` and Pods with `component=predictor` (configured in `cmd/main.go`). Resources without these labels are invisible to the cached client. Use `APIReader` if you need to read uncached resources.
- **Optional CRDs.** The controller runs on clusters where KEDA, Kuadrant, Istio, and Gateway API may not be installed. Always check `utils.IsCrdAvailable()` before setting up watches for these types.
- **`spec` vs `status` writes.** Never write both in a single API call. Guard status writes with deep-equal checks to avoid infinite reconcile loops.

## Two Binaries

This repo produces two binaries:
1. **controller** (`cmd/main.go`) — the main controller-runtime manager with all controllers and webhooks
2. **model-serving-api** (`server/main.go`) — a standalone REST API server for gateway/endpoint discovery, deployed as a separate pod

These share the same Go module but have independent entrypoints and container images.
