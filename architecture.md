# Architecture

## Purpose

ODH Model Controller is a **companion controller** to [KServe](https://github.com/kserve/kserve) within the [Open Data Hub](https://opendatahub.io/) (ODH) and Red Hat OpenShift AI platforms. KServe provides the core model serving primitives (InferenceService, ServingRuntime, InferenceGraph, LLMInferenceService). ODH Model Controller layers on platform-specific capabilities that don't belong in upstream KServe:

- **OpenShift route management** — Creates and manages OpenShift Routes for model endpoints
- **Certificate trust aggregation** — Watches platform CA bundle ConfigMaps and aggregates them into KServe's trust bundle
- **Monitoring integration** — Creates ServiceMonitors, PodMonitors, metrics dashboards, and Prometheus RoleBindings
- **NVIDIA NIM integration** — Manages NIM Account lifecycle (API key validation, model catalog sync, pull secrets, ServingRuntime templates)
- **Multi-node Ray TLS** — Generates and distributes CA certificates for Ray head/worker TLS in multi-node serving
- **Model Registry sync** — Bidirectional sync between InferenceServices and Model Registry metadata
- **KEDA autoscaling** — Reconciles TriggerAuthentication resources for KEDA-based autoscaling
- **LLMInferenceService auth** — Creates Kuadrant AuthPolicies and Istio EnvoyFilters for MaaS (Model-as-a-Service) gateway authentication
- **Gateway API bootstrap** — Reconciles EnvoyFilter and AuthPolicy resources on Gateways independent of model lifecycle
- **Webhook enforcement** — Validates InferenceService naming constraints, protects system namespaces, injects Ray TLS init containers

## System Context

```
┌──────────────────────────────────────────────────────────────────────┐
│                        OpenShift / Kubernetes Cluster                │
│                                                                      │
│  ┌─────────────┐    watches     ┌──────────────────────────────┐    │
│  │   KServe     │──────────────►│  InferenceService (ISVC)      │    │
│  │  Controller  │    reconciles │  ServingRuntime (SR)           │    │
│  └─────────────┘               │  InferenceGraph (IG)           │    │
│                                 │  LLMInferenceService (LLMISVC)│    │
│  ┌─────────────┐    watches     │  LLMInferenceServiceConfig    │    │
│  │  ODH Model  │──────────────►│                                │    │
│  │  Controller  │    augments   └──────────────────────────────┘    │
│  │             │                                                     │
│  │             │    creates/manages                                   │
│  │             │──────────────► Routes, NetworkPolicies,             │
│  │             │                ServiceMonitors, PodMonitors,        │
│  │             │                Secrets, ConfigMaps, RoleBindings,   │
│  │             │                ClusterRoleBindings, ServiceAccounts,│
│  │             │                EnvoyFilters, AuthPolicies,          │
│  │             │                TriggerAuthentications, Templates    │
│  └─────────────┘                                                     │
│                                                                      │
│  ┌─────────────┐                                                     │
│  │ model-      │    REST API for gateway/endpoint discovery          │
│  │ serving-api │    (standalone deployment, separate binary)         │
│  └─────────────┘                                                     │
│                                                                      │
│  External Dependencies (optional, detected at startup via CRD check):│
│  • Kuadrant / Authorino — auth policies                              │
│  • Istio — EnvoyFilters                                              │
│  • KEDA — autoscaler TriggerAuthentications                          │
│  • Gateway API — Gateway, HTTPRoute                                  │
│  • Prometheus Operator — ServiceMonitor, PodMonitor                  │
│  • cert-manager — (upstream KServe dependency)                       │
└──────────────────────────────────────────────────────────────────────┘
```

## Two Binaries

The repository produces two independent binaries from a single Go module:

### Controller (`cmd/main.go`)

The primary controller-runtime manager. It registers all controllers and webhooks, starts the manager, and runs the NIM account reconciler in a separate goroutine. The manager uses label-filtered caches:
- **Secrets**: only `opendatahub.io/managed=true`
- **Pods**: only `component=predictor`

### Model Serving API (`server/main.go`)

A standalone REST API server for querying gateway and endpoint information. It is deployed as a separate pod with its own Deployment, Service, and container image. It provides:
- Gateway discovery and status endpoints
- Health and metrics endpoints
- TLS with cert reloading
- OpenShift auth middleware (TokenReview/SubjectAccessReview)
- OpenTelemetry instrumentation

## Controllers

### InferenceService Controller (`internal/controller/serving/`)

**Trigger:** InferenceService create/update/delete, plus watched ServingRuntimes and Secrets.

**Responsibilities:**
1. Adds/removes an ODH finalizer for cross-namespace cleanup
2. Delegates to `KserveRawInferenceServiceReconciler` which fans out to sub-reconcilers:
   - **Route reconciler** — creates OpenShift Routes with TLS passthrough for model endpoints
   - **Metrics service reconciler** — creates a Service for scraping runtime metrics
   - **Metrics ServiceMonitor reconciler** — creates ServiceMonitor/PodMonitor for Prometheus
   - **Metrics dashboard reconciler** — creates ConfigMaps with Grafana dashboard JSON
   - **ClusterRoleBinding reconciler** — grants auth-delegator for secure metrics
   - **ServiceAccount reconciler** — ensures service accounts with proper image pull secrets
   - **KEDA reconciler** — creates TriggerAuthentication for KEDA autoscaling
3. Optionally runs Model Registry reconciliation (controlled by `MODELREGISTRY_STATE=managed`)
4. Cleans up shared namespace resources when the last ISVC is deleted

### ServingRuntime Controller (`internal/controller/serving/`)

**Trigger:** ServingRuntime create/update/delete, plus watched Namespaces, RoleBindings, and Ray TLS Secrets.

**Responsibilities:**
1. **Monitoring RoleBindings** — creates/updates `prometheus-ns-access` RoleBindings in monitoring-configured namespaces
2. **Multi-node Ray TLS** — when a ServingRuntime has `spec.workerSpec` (multi-node):
   - Creates a self-signed CA certificate in the controller namespace (`ray-ca-tls`)
   - Distributes the CA cert to user namespaces as `ray-tls` Secret
   - Syncs CA updates across all namespaces with multi-node runtimes

### InferenceGraph Controller (`internal/controller/serving/`)

Placeholder controller that watches InferenceGraph resources. Currently a no-op stub — reconciliation logic has not been implemented yet.

### LLMInferenceService Controller (`internal/controller/serving/llm/`)

**Trigger:** LLMInferenceService create/update/delete, plus AuthPolicy changes on managed resources and global Kuadrant/Authorino availability changes.

**Responsibilities:**
1. Resolves BaseRef configs (LLMInferenceServiceConfig) and merges specs using KServe's `MergeSpecs`
2. Runs sub-reconcilers — currently the **AuthPolicy reconciler** which creates Kuadrant AuthPolicies per-service
3. Cleans up namespace-scoped resources when the last LLMInferenceService is deleted
4. Triggers global re-reconciliation when Kuadrant or Authorino instances are created/deleted

### Gateway Controller (`internal/controller/serving/llm/`)

**Trigger:** Gateway create/update (with specific predicate filtering), plus changes to LLMInferenceService, LLMInferenceServiceConfig, and Namespace labels.

**Responsibilities:**
1. **EnvoyFilter reconciliation** — creates Istio EnvoyFilters for Authorino TLS bootstrap on gateways referenced by LLMInferenceServices
2. **AuthPolicy reconciliation** — creates gateway-level Kuadrant AuthPolicies with configurable CEL expressions for access control
3. Respects `opendatahub.io/managed` labels/annotations and `security.opendatahub.io/authorino-tls-bootstrap` annotation
4. Uses delta processing (desired-vs-actual comparison) to minimize API writes
5. Handles Gateway API allowedRoutes/namespace selectors to determine which namespaces can reference a gateway

### ConfigMap Controller (`internal/controller/core/`)

**Trigger:** ConfigMap create/update/delete for specific CA bundle ConfigMaps.

**Responsibilities:**
Watches `odh-trusted-ca-bundle` and `openshift-service-ca.crt` ConfigMaps. Aggregates their certificate contents into the KServe CA bundle ConfigMap (`odh-kserve-custom-ca-bundle`) so that model servers trust both platform and custom CAs.

### Secret Controller (`internal/controller/core/`)

**Trigger:** Secret create/update/delete for Secrets with label `opendatahub.io/managed=true`.

**Responsibilities:**
Triggers re-reconciliation of associated InferenceServices when managed Secrets change (e.g., storage credentials, serving certs).

### Pod Controller (`internal/controller/core/`)

**Trigger:** Pod events for Pods with label `component=predictor`.

**Responsibilities:**
Emits metrics about predictor pod lifecycle for observability dashboards.

### NIM Account Controller (`internal/controller/nim/`)

**Trigger:** NIM Account create/update/delete, plus watched Secrets (API key) and ConfigMaps (model list) via field indexers.

**Responsibilities:**
Manages the lifecycle of NVIDIA NIM integration through a handler chain:
1. **ValidationHandler** — validates the NGC API key against NVIDIA's API, respects refresh rates and force-validation annotation
2. **ConfigMapHandler** — fetches/syncs the NIM model catalog into a ConfigMap (supports air-gapped mode with user-provided model lists)
3. **TemplateHandler** — creates/updates an OpenShift Template for NIM ServingRuntimes
4. **PullSecretHandler** — creates/updates image pull secrets for NGC container registry

The controller uses a finalizer (`runtimes.opendatahub.io/nim-cleanup-finalizer`) for cleanup. When `NIM_STATE=removed`, it runs a cleanup runner instead of the full controller.

## Webhooks

All webhooks are registered in `cmd/main.go` and can be disabled via `ENABLE_WEBHOOKS=false`.

| Webhook | Type | Purpose |
|---------|------|---------|
| **InferenceService** (v1beta1) | Validating + Defaulting | Validates name length (max 53 chars), blocks creation in protected (application) namespaces |
| **InferenceGraph** (v1alpha1) | Validating + Defaulting | Validates InferenceGraph resources, selects appropriate runtime |
| **NIM Account** (nim/v1) | Validating | Validates NIM Account spec |
| **Pod** (core/v1) | Mutating | Injects Ray TLS generator init containers and volume mounts into multi-node predictor pods |

## Key Patterns

### Handler Chain (NIM)

The NIM Account controller uses a sequential handler chain pattern where each handler returns a `Response` with `{Error, Requeue, Continue}`. If a handler returns `Continue=false`, subsequent handlers are skipped. This allows early-exit for validation failures or pending async operations.

### Sub-Reconciler Pattern

Both the InferenceService and LLMInferenceService controllers decompose reconciliation into sub-reconcilers, each responsible for one resource type or concern. Sub-reconcilers implement a common interface (`Reconcile`, `Delete`, `Cleanup` methods) and are iterated in the parent reconciler.

### Delta Processing

The `processors.DeltaProcessor` and type-specific comparators (in `comparators/`) compute a delta between desired and actual resource state. The delta reports whether a resource needs to be added, updated, or is unchanged. This avoids unnecessary API writes and provides a consistent create/update pattern across resource types.

### Optional CRD Detection

At controller setup time, `utils.IsCrdAvailable()` probes the API server for optional CRDs (KEDA, Kuadrant, Istio, Gateway API). Controllers conditionally register watches based on CRD availability, allowing the controller to run on clusters without these operators installed.

### Resource Builders with Functional Options

The `resources/` package provides builder functions for Kubernetes resources (AuthPolicy, EnvoyFilter, Route, NetworkPolicy, etc.) that accept functional options (`WithLabels`, `WithAudiences`, etc.) for customization. This keeps resource construction centralized and testable.

## Integration with KServe

ODH Model Controller depends on the **opendatahub-io/kserve** fork (not upstream `kserve/kserve`). The fork is pinned via a `replace` directive in `go.mod`. Key integration points:

- **API types**: Imports `kserve/pkg/apis/serving/v1beta1` (InferenceService), `v1alpha1` (ServingRuntime, InferenceGraph), and `v1alpha2` (LLMInferenceService, LLMInferenceServiceConfig)
- **Spec merging**: Uses `kserve/pkg/controller/v1alpha2/llmisvc.MergeSpecs` for LLMInferenceService config inheritance
- **Deployment mode**: Exclusively operates in RawDeployment mode — the controller checks for and manages raw Deployment/Service resources, not Knative Services
- **Shared CRDs**: KServe CRD manifests are downloaded into `config/crd/external/` for envtest and kustomize overlays

## Runtime Templates

Pre-built ServingRuntime templates for supported model servers live in `config/runtimes/`:

| Template | Model Server |
|----------|-------------|
| `vllm-cuda-template.yaml` | vLLM (NVIDIA GPU) |
| `vllm-rocm-template.yaml` | vLLM (AMD ROCm) |
| `vllm-cpu-template.yaml` | vLLM (CPU) |
| `vllm-gaudi-template.yaml` | vLLM (Intel Gaudi) |
| `vllm-spyre-*.yaml` | vLLM (IBM Spyre accelerator) |
| `vllm-multinode-template.yaml` | vLLM multi-node (Ray) |
| `vllm-omni-cuda-template.yaml` | vLLM-Omni (NVIDIA GPU, multimodal) |
| `ovms-kserve-template.yaml` | OpenVINO Model Server |
| `mlserver-template.yaml` | Seldon MLServer |
| `hf-detector-template.yaml` | HuggingFace detector |

These are kustomized into the deployment manifests and are the basis for the NIM Template handler's output.

## Deployment

The controller is deployed via kustomize overlays in `config/`. The base overlay in `config/base/` references a `params.env` file containing container image references. The ODH operator (opendatahub-io/opendatahub-operator) manages the actual deployment — this repo provides the manifests and container images, not a standalone installer.

The model-serving-api is deployed separately via `config/server/`.
