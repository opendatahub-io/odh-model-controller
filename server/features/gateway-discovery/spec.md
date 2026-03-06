# Gateway Discovery REST API — Implementation Specification

This document specifies the implementation of a standalone REST API backend for **Flow 1: Gateway Discovery and
Selection** as described in [Discover_Gateways.md](Discover_Gateways.md). The API returns usable Kubernetes Gateway
resources for a given user and target namespace, enabling UI-driven Gateway selection during LLMInferenceService
deployment.

## Overview

- **Scope:** Flow 1 only — discover existing Gateways, no on-demand creation
- **Language:** Go
- **HTTP framework:** stdlib `net/http` (no external HTTP dependencies)
- **Kubernetes client:** `client-go`, `controller-runtime`, `sigs.k8s.io/gateway-api` (already in `go.mod`)
- **Authentication:** User's Bearer token forwarded for RBAC checks
- **Permissions:** ServiceAccount with cluster-wide Gateway list and Namespace get

---

## File Structure

### Application Code (`server/`)

```
server/
├── main.go              # Server bootstrap, K8s client init, graceful shutdown
├── config.go            # Env var config loading (listen addr, TLS)
├── server.go            # HTTP mux, middleware chain
├── handlers/
│   ├── gateways.go      # GET /api/v1/gateways handler
│   └── health.go        # GET /healthz, GET /readyz
├── middleware/
│   ├── auth.go          # Bearer token extraction
│   ├── logging.go       # Request logging
│   └── recovery.go      # Panic recovery
└── gateway/
    ├── discovery.go     # Core discovery logic: RBAC check, list, filter
    ├── filter.go        # Listener namespace filtering (All/Same/Selector)
    ├── status.go        # Gateway condition extraction
    └── types.go         # GatewayRef, API response types
```

### Deployment Manifests (`config/server/`)

Following the same Kustomize patterns used by the controller in `config/manager/` and `config/rbac/`:

```
config/server/
├── kustomization.yaml           # Aggregates all server manifests
├── server.yaml                  # Deployment + Namespace definition
├── service.yaml                 # Service exposing the API
├── service_account.yaml         # ServiceAccount for the server pod
├── clusterrole.yaml             # ClusterRole (gateway list, namespace get)
└── clusterrolebinding.yaml      # Binds ClusterRole to ServiceAccount
```

### Application File Responsibilities

| File                     | Responsibility                                                                                                                                                                                        |
|--------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `main.go`                | Parse config, create K8s clients (`ctrl.GetConfigOrDie()`, `client.New()`, `kubernetes.NewForConfig()`), register Gateway API scheme, start HTTP server, graceful shutdown via `signal.NotifyContext` |
| `config.go`              | `Config` struct, load from env vars with defaults, parse `GATEWAY_LABEL_SELECTOR` into `map[string]string`                                                                                            |
| `server.go`              | Build `http.ServeMux`, register routes, apply middleware chain (recovery → logging → auth → handler)                                                                                                  |
| `handlers/gateways.go`   | Validate input, orchestrate RBAC check → list gateways → filter → respond                                                                                                                             |
| `handlers/health.go`     | Trivial health and readiness probes                                                                                                                                                                   |
| `middleware/auth.go`     | Extract `Authorization: Bearer <token>`, store in request context, reject if missing                                                                                                                  |
| `middleware/logging.go`  | Log method, path, status code, duration                                                                                                                                                               |
| `middleware/recovery.go` | Catch panics, return 500                                                                                                                                                                              |
| `gateway/discovery.go`   | `GatewayDiscoverer` interface + implementation wiring RBAC, listing, and filtering                                                                                                                    |
| `gateway/filter.go`      | `FilterListeners()`, `listenerAllowsNamespace()`, `matchesSelector()`                                                                                                                                 |
| `gateway/status.go`      | `ExtractStatus()` from Gateway conditions                                                                                                                                                             |
| `gateway/types.go`       | `GatewayRef`, `GatewaysResponse`, `ErrorResponse`                                                                                                                                                     |

### Manifest File Responsibilities

| File                                    | Responsibility                                                                           |
|-----------------------------------------|------------------------------------------------------------------------------------------|
| `config/server/kustomization.yaml`      | Kustomize aggregation of all server manifests, namespace override                        |
| `config/server/server.yaml`             | Deployment definition: container image, ports, probes, security context, resource limits |
| `config/server/service.yaml`            | Service exposing port 8443, selector targeting server pods                               |
| `config/server/service_account.yaml`    | ServiceAccount for the server pod                                                        |
| `config/server/clusterrole.yaml`        | ClusterRole: `list` gateways, `get` namespaces                                           |
| `config/server/clusterrolebinding.yaml` | Binds the ClusterRole to the server ServiceAccount                                       |

---

## API Contract

### Endpoint

```
GET /api/v1/gateways?namespace={target_namespace}
```

### Request

| Component       | Detail                                                                                              |
|-----------------|-----------------------------------------------------------------------------------------------------|
| Method          | `GET`                                                                                               |
| Path            | `/api/v1/gateways`                                                                                  |
| Query parameter | `namespace` (required) — target namespace where the user intends to create an `LLMInferenceService` |
| Header          | `Authorization: Bearer <user-token>` (required)                                                     |
| Body            | None                                                                                                |

### Success Response (200 OK)

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
    },
    {
      "name": "team-gateway",
      "namespace": "my-llm-project",
      "listener": "http",
      "status": "NotReady"
    }
  ]
}
```

When no gateways match (including when RBAC denies access):

```json
{
  "gateways": []
}
```

### Response Fields

| Field                    | Type   | Description                                                                                           |
|--------------------------|--------|-------------------------------------------------------------------------------------------------------|
| `gateways`               | array  | Always present, may be empty                                                                          |
| `gateways[].name`        | string | Gateway resource name                                                                                 |
| `gateways[].namespace`   | string | Gateway resource namespace                                                                            |
| `gateways[].listener`    | string | Name of the listener that permits the target namespace                                                |
| `gateways[].status`      | string | `"Ready"`, `"NotReady"`, or `"Unknown"` — derived from Gateway `Accepted` and `Programmed` conditions |
| `gateways[].displayName` | string | Human-readable name from `openshift.io/display-name` annotation (omitted if not set)                  |
| `gateways[].description` | string | Human-readable description from `openshift.io/description` annotation (omitted if not set)            |

### Error Responses

| Status | Condition                                | Body                                                       |
|--------|------------------------------------------|------------------------------------------------------------|
| 400    | Missing `namespace` param                | `{"error": "missing required query parameter: namespace"}` |
| 400    | Invalid namespace format                 | `{"error": "invalid namespace: must match RFC 1123"}`      |
| 401    | Missing/malformed `Authorization` header | `{"error": "missing or invalid authorization header"}`     |
| 405    | Non-GET method                           | `{"error": "method not allowed"}`                          |
| 500    | K8s API failure                          | `{"error": "internal server error"}`                       |

### Health Endpoints

```
GET /healthz → 200 {"status": "ok"}
GET /readyz  → 200 {"status": "ok"} (or 503 if K8s API unreachable)
```

---

## Request Flow

The following describes the complete sequence when a request arrives at `GET /api/v1/gateways?namespace=my-project`.

### Step 1: Middleware Chain

1. **Recovery** — wraps handler in `defer/recover`, returns 500 on panic
2. **Logging** — records start time, calls next, logs `method=GET path=/api/v1/gateways status=200 duration=45ms`
3. **Auth** — extracts `Authorization: Bearer <token>` header. If missing or not prefixed with `Bearer `, returns 401.
   Stores the raw token in request context via `context.WithValue`

### Step 2: Input Validation

1. Check `r.Method == http.MethodGet` — if not, return 405
2. Extract `namespace` from `r.URL.Query().Get("namespace")` — if empty, return 400
3. Validate namespace matches Kubernetes naming: `^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$` — if invalid, return 400

### Step 3: RBAC Check (User's Token)

Create a `SelfSubjectAccessReview` using the user's Bearer token to check if they can `create llminferenceservices` in
the target namespace.

```go
// Create a REST config with the user's token
userCfg := rest.CopyConfig(baseCfg)
userCfg.BearerToken = userToken
userCfg.BearerTokenFile = ""

userClient, err := kubernetes.NewForConfig(userCfg)
// ... error handling ...

review := &authorizationv1.SelfSubjectAccessReview{
    Spec: authorizationv1.SelfSubjectAccessReviewSpec{
        ResourceAttributes: &authorizationv1.ResourceAttributes{
            Namespace: targetNamespace,
            Verb:      "create",
            Group:     "serving.kserve.io",
            Resource:  "llminferenceservices",
        },
    },
}

result, err := userClient.AuthorizationV1().
    SelfSubjectAccessReviews().
    Create(ctx, review, metav1.CreateOptions{})
```

- If `result.Status.Allowed == false`: return `{"gateways": []}` with 200 OK (no information leak about gateway
  existence)
- If the K8s API call fails (network error, invalid token): return 500

### Step 4: List Gateways (ServiceAccount Token)

Use the ServiceAccount's `controller-runtime` client to list Gateways cluster-wide. When `GATEWAY_LABEL_SELECTOR` is
configured (e.g. `my-org/managed-by=ai-platform`), only Gateways carrying that label are returned — the filtering
happens server-side at the K8s API level via `client.MatchingLabels`.

```go
var gatewayList gatewayapiv1.GatewayList
listOpts := []client.ListOption{}
if cfg.GatewayLabelSelector != nil {
    listOpts = append(listOpts, client.MatchingLabels(cfg.GatewayLabelSelector))
}
err := saClient.List(ctx, &gatewayList, listOpts...)
```

When the env var is unset, all Gateways in the cluster are considered (no label filter applied).

### Step 5: Filter Listeners by Namespace

For each Gateway, for each Listener, determine if the listener allows the target namespace based on
`allowedRoutes.namespaces.from`:

- **`All`** — listener allows any namespace → include
- **`Same`** — listener allows only the Gateway's own namespace → include if `targetNamespace == gateway.Namespace`
- **`Selector`** — listener allows namespaces matching a label selector → fetch target namespace labels, evaluate
  selector
- **`nil` / absent** — default is `Same` per Gateway API spec

### Step 5a: Fetch Namespace Labels (Lazy)

Only performed if any listener uses `from: Selector`. At most one K8s API call per request:

```go
var ns corev1.Namespace
err := saClient.Get(ctx, types.NamespacedName{Name: targetNamespace}, &ns)
nsLabels := ns.Labels
```

### Step 6: Extract Status

Derive a human-readable status from Gateway conditions:

```go
func ExtractStatus(gateway *gatewayapiv1.Gateway) string {
    conditions := gateway.Status.Conditions
    accepted := findCondition(conditions, "Accepted")
    programmed := findCondition(conditions, "Programmed")

    if accepted != nil && accepted.Status == metav1.ConditionTrue &&
        programmed != nil && programmed.Status == metav1.ConditionTrue {
        return "Ready"
    }
    if (accepted != nil && accepted.Status == metav1.ConditionFalse) ||
        (programmed != nil && programmed.Status == metav1.ConditionFalse) {
        return "NotReady"
    }
    return "Unknown"
}
```

### Step 7: Build Response

Assemble the `[]GatewayRef` slice, serialize as JSON, write 200 OK with `Content-Type: application/json`.

---

## Listener Namespace Filtering Algorithm

```go
func FilterListeners(gateways []gatewayapiv1.Gateway, targetNS string, nsLabels map[string]string) []GatewayRef {
    var refs []GatewayRef
    for _, gw := range gateways {
        status := ExtractStatus(&gw)
        for _, listener := range gw.Spec.Listeners {
            if listenerAllowsNamespace(listener, gw.Namespace, targetNS, nsLabels) {
                refs = append(refs, GatewayRef{
                    Name:      gw.Name,
                    Namespace: gw.Namespace,
                    Listener:  string(listener.Name),
                    Status:    status,
                })
            }
        }
    }
    return refs
}

func listenerAllowsNamespace(l gatewayapiv1.Listener, gwNamespace, targetNS string, nsLabels map[string]string) bool {
    if l.AllowedRoutes == nil || l.AllowedRoutes.Namespaces == nil || l.AllowedRoutes.Namespaces.From == nil {
        // Default is "Same" per Gateway API spec
        return gwNamespace == targetNS
    }

    switch *l.AllowedRoutes.Namespaces.From {
    case gatewayapiv1.NamespacesFromAll:
        return true
    case gatewayapiv1.NamespacesFromSame:
        return gwNamespace == targetNS
    case gatewayapiv1.NamespacesFromSelector:
        if l.AllowedRoutes.Namespaces.Selector == nil {
            return false
        }
        return matchesSelector(*l.AllowedRoutes.Namespaces.Selector, nsLabels)
    default:
        return false
    }
}

func matchesSelector(selector metav1.LabelSelector, labels map[string]string) bool {
    for k, v := range selector.MatchLabels {
        if labels[k] != v {
            return false
        }
    }
    for _, expr := range selector.MatchExpressions {
        val, exists := labels[expr.Key]
        switch expr.Operator {
        case metav1.LabelSelectorOpIn:
            if !slices.Contains(expr.Values, val) {
                return false
            }
        case metav1.LabelSelectorOpNotIn:
            if exists && slices.Contains(expr.Values, val) {
                return false
            }
        case metav1.LabelSelectorOpExists:
            if !exists {
                return false
            }
        case metav1.LabelSelectorOpDoesNotExist:
            if exists {
                return false
            }
        }
    }
    return true
}
```

---

## Security Model

### Authentication

- Every request to `/api/v1/gateways` must include `Authorization: Bearer <token>`
- The token is the user's OpenShift/Kubernetes token, passed through from the UI/BFF
- Token validity is verified implicitly when the `SelfSubjectAccessReview` call is made to the K8s API server — if the
  token is invalid, K8s returns 401, and the service returns 401 to the caller

### Authorization

- The user's token is used via `SelfSubjectAccessReview` to check if the user can `create llminferenceservices` in the
  target namespace
- If denied, the API returns an empty gateway list (200 OK) — this prevents information leakage about gateway existence
  to unauthorized users
- The ServiceAccount token is used only for privileged reads (list gateways cluster-wide, get namespace labels)

### Token Isolation

- The user's token is **never** used for cluster-wide Gateway listing (the user may lack cluster-wide permissions)
- The ServiceAccount token is **never** sent back to the client or logged
- Each RBAC check creates a short-lived K8s client with the user's token — no token caching across requests

### Input Validation

- The `namespace` parameter is validated against RFC 1123 before any K8s API call
- Only `GET` is accepted on the gateway endpoint

### TLS

- The server supports TLS termination via configurable cert/key paths
- In production, TLS is typically terminated by the OpenShift Route or service mesh
- All communication to the K8s API uses HTTPS with the in-cluster CA (handled by `ctrl.GetConfigOrDie()`)

---

## Kubernetes Client Setup

Use existing project patterns from `cmd/main.go` and `internal/controller/utils/init.go`:

```go
import (
    "k8s.io/apimachinery/pkg/runtime"
    utilruntime "k8s.io/apimachinery/pkg/util/runtime"
    "k8s.io/client-go/kubernetes"
    "k8s.io/client-go/rest"
    ctrl "sigs.k8s.io/controller-runtime"
    "sigs.k8s.io/controller-runtime/pkg/client"
    gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// Scheme registration (same pattern as internal/controller/utils/init.go)
scheme := runtime.NewScheme()
utilruntime.Must(gatewayapiv1.Install(scheme))
utilruntime.Must(corev1.AddToScheme(scheme))

// In-cluster config (same pattern as cmd/main.go)
cfg := ctrl.GetConfigOrDie()

// ServiceAccount client for listing Gateways and getting Namespaces
saClient, err := client.New(cfg, client.Options{Scheme: scheme})

// Base config for creating per-request user clients (RBAC checks)
// The base config is copied per request with the user's token injected
baseCfg := rest.CopyConfig(cfg)
```

---

## Configuration

| Env Var                  | Default | Description                                                                            |
|--------------------------|---------|----------------------------------------------------------------------------------------|
| `LISTEN_ADDR`            | `:8443` | HTTP server listen address                                                             |
| `TLS_CERT_FILE`          | `""`    | Path to TLS certificate (empty = no TLS)                                               |
| `TLS_KEY_FILE`           | `""`    | Path to TLS private key                                                                |
| `LOG_LEVEL`              | `info`  | Log verbosity: `debug`, `info`, `error`                                                |
| `READ_TIMEOUT`           | `10s`   | HTTP server read timeout                                                               |
| `WRITE_TIMEOUT`          | `10s`   | HTTP server write timeout                                                              |
| `GATEWAY_LABEL_SELECTOR` | `""`    | Only consider Gateways with this label. Format: `key=value`. Empty means all Gateways. |

No K8s API host, CA, or token configuration is needed — `ctrl.GetConfigOrDie()` handles in-cluster setup automatically.

The `GATEWAY_LABEL_SELECTOR` feature flag allows operators to restrict which Gateways are discoverable. For example,
setting `GATEWAY_LABEL_SELECTOR=ai-platform.opendatahub.io/managed=true` ensures only explicitly tagged Gateways
appear in the discovery results. The label is parsed as `key=value` at startup and passed as
`client.MatchingLabels{key: value}` to the K8s List call, so filtering happens server-side.

---

## ServiceAccount RBAC

The API server's ServiceAccount needs a minimal ClusterRole:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: gateway-discovery-api
rules:
  - apiGroups: [ "gateway.networking.k8s.io" ]
    resources: [ "gateways" ]
    verbs: [ "list" ]
  - apiGroups: [ "" ]
    resources: [ "namespaces" ]
    verbs: [ "get" ]
```

The `SelfSubjectAccessReview` create permission is implicitly available to all authenticated users via the K8s API
server — no additional RBAC is needed for that.

---

## Deployment Manifests

All manifests live in `config/server/` and follow the same patterns as `config/manager/` and `config/rbac/`.

### config/server/kustomization.yaml

```yaml
resources:
  - server.yaml
  - service.yaml
  - service_account.yaml
  - clusterrole.yaml
  - clusterrolebinding.yaml
```

### config/server/server.yaml

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: gateway-discovery-api
  namespace: system
  labels:
    control-plane: gateway-discovery-api
spec:
  replicas: 1
  selector:
    matchLabels:
      control-plane: gateway-discovery-api
  template:
    metadata:
      labels:
        control-plane: gateway-discovery-api
    spec:
      serviceAccountName: gateway-discovery-api
      securityContext:
        runAsNonRoot: true
      containers:
        - name: server
          image: controller:latest
          env:
            - name: GATEWAY_LABEL_SELECTOR
              value: ""  # e.g. "ai-platform.opendatahub.io/managed=true"
          ports:
            - containerPort: 8443
              name: https
              protocol: TCP
          livenessProbe:
            httpGet:
              path: /healthz
              port: 8443
            initialDelaySeconds: 5
            periodSeconds: 10
          readinessProbe:
            httpGet:
              path: /readyz
              port: 8443
            initialDelaySeconds: 5
            periodSeconds: 10
          resources:
            limits:
              cpu: 200m
              memory: 128Mi
            requests:
              cpu: 50m
              memory: 64Mi
          securityContext:
            allowPrivilegeEscalation: false
            capabilities:
              drop:
                - ALL
```

### config/server/service.yaml

```yaml
apiVersion: v1
kind: Service
metadata:
  name: gateway-discovery-api
  namespace: system
  labels:
    control-plane: gateway-discovery-api
spec:
  ports:
    - name: https
      port: 8443
      protocol: TCP
      targetPort: 8443
  selector:
    control-plane: gateway-discovery-api
```

### config/server/service_account.yaml

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: gateway-discovery-api
  namespace: system
```

### config/server/clusterrole.yaml

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: gateway-discovery-api
rules:
  - apiGroups: [ "gateway.networking.k8s.io" ]
    resources: [ "gateways" ]
    verbs: [ "list" ]
  - apiGroups: [ "" ]
    resources: [ "namespaces" ]
    verbs: [ "get" ]
```

### config/server/clusterrolebinding.yaml

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: gateway-discovery-api
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: gateway-discovery-api
subjects:
  - kind: ServiceAccount
    name: gateway-discovery-api
    namespace: system
```

---

## API Response Types

```go
// GatewayRef represents a single usable gateway+listener pair
type GatewayRef struct {
    Name        string `json:"name"`
    Namespace   string `json:"namespace"`
    Listener    string `json:"listener"`
    Status      string `json:"status"`
    DisplayName string `json:"displayName,omitempty"`
    Description string `json:"description,omitempty"`
}

// GatewaysResponse is the API response envelope
type GatewaysResponse struct {
    Gateways []GatewayRef `json:"gateways"`
}

// ErrorResponse is returned for 4xx/5xx errors
type ErrorResponse struct {
    Error string `json:"error"`
}
```

---

## Handler Pattern

```go
type GatewayHandler struct {
    saClient client.Client    // ServiceAccount client for K8s reads
    baseCfg  *rest.Config     // Base config for per-request user clients
    nsRegex  *regexp.Regexp   // Compiled RFC 1123 validator
}

func (h *GatewayHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodGet {
        writeError(w, http.StatusMethodNotAllowed, "method not allowed")
        return
    }

    namespace := r.URL.Query().Get("namespace")
    if namespace == "" {
        writeError(w, http.StatusBadRequest, "missing required query parameter: namespace")
        return
    }
    if !h.nsRegex.MatchString(namespace) {
        writeError(w, http.StatusBadRequest, "invalid namespace: must match RFC 1123")
        return
    }

    userToken := middleware.TokenFromContext(r.Context())

    // RBAC check with user's token
    allowed, err := h.checkAccess(r.Context(), userToken, namespace)
    if err != nil {
        writeError(w, http.StatusInternalServerError, "internal server error")
        return
    }
    if !allowed {
        writeJSON(w, http.StatusOK, GatewaysResponse{Gateways: []GatewayRef{}})
        return
    }

    // List gateways with ServiceAccount
    var gatewayList gatewayapiv1.GatewayList
    if err := h.saClient.List(r.Context(), &gatewayList); err != nil {
        writeError(w, http.StatusInternalServerError, "internal server error")
        return
    }

    // Lazily fetch namespace labels only if any listener uses Selector
    var nsLabels map[string]string
    if needsNamespaceLabels(gatewayList.Items) {
        var ns corev1.Namespace
        if err := h.saClient.Get(r.Context(), types.NamespacedName{Name: namespace}, &ns); err != nil {
            writeError(w, http.StatusInternalServerError, "internal server error")
            return
        }
        nsLabels = ns.Labels
    }

    refs := FilterListeners(gatewayList.Items, namespace, nsLabels)
    writeJSON(w, http.StatusOK, GatewaysResponse{Gateways: refs})
}

func (h *GatewayHandler) checkAccess(ctx context.Context, userToken, namespace string) (bool, error) {
    userCfg := rest.CopyConfig(h.baseCfg)
    userCfg.BearerToken = userToken
    userCfg.BearerTokenFile = ""

    userClient, err := kubernetes.NewForConfig(userCfg)
    if err != nil {
        return false, err
    }

    review := &authorizationv1.SelfSubjectAccessReview{
        Spec: authorizationv1.SelfSubjectAccessReviewSpec{
            ResourceAttributes: &authorizationv1.ResourceAttributes{
                Namespace: namespace,
                Verb:      "create",
                Group:     "serving.kserve.io",
                Resource:  "llminferenceservices",
            },
        },
    }

    result, err := userClient.AuthorizationV1().
        SelfSubjectAccessReviews().
        Create(ctx, review, metav1.CreateOptions{})
    if err != nil {
        return false, err
    }

    return result.Status.Allowed, nil
}
```

---

## Server Bootstrap

```go
func main() {
cfg := LoadConfig()

// K8s setup
scheme := runtime.NewScheme()
utilruntime.Must(gatewayapiv1.Install(scheme))
utilruntime.Must(corev1.AddToScheme(scheme))

restCfg := ctrl.GetConfigOrDie()
saClient, err := client.New(restCfg, client.Options{Scheme: scheme})
if err != nil {
log.Fatalf("failed to create kubernetes client: %v", err)
}

srv := NewServer(cfg, saClient, restCfg)

// Graceful shutdown
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()

    go func() {
        log.Printf("listening on %s", cfg.ListenAddr)
        if cfg.TLSCertFile != "" && cfg.TLSKeyFile != "" {
            err = srv.ListenAndServeTLS(cfg.TLSCertFile, cfg.TLSKeyFile)
        } else {
            err = srv.ListenAndServe()
        }
        if err != nil && err != http.ErrServerClosed {
            log.Fatalf("server error: %v", err)
        }
    }()

    <-ctx.Done()
    log.Println("shutting down...")

    shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    if err := srv.Shutdown(shutdownCtx); err != nil {
        log.Fatalf("shutdown error: %v", err)
    }
}
```

---

## Testing Strategy

### Unit Tests

| File                      | Tests                                                                                                                                                                                       |
|---------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `gateway/filter_test.go`  | `listenerAllowsNamespace` for all `From` values: `All`, `Same` (match/no match), `Selector` (matchLabels, matchExpressions, all operators), `nil` defaults. Multiple listeners per gateway. |
| `gateway/filter_test.go`  | `matchesSelector` edge cases: empty selector matches all, `In` with missing key, `DoesNotExist` with missing key, combined matchLabels + matchExpressions.                                  |
| `gateway/status_test.go`  | `ExtractStatus` with: both Accepted+Programmed true → "Ready", Programmed false → "NotReady", no conditions → "Unknown", partial conditions.                                                |
| `middleware/auth_test.go` | Bearer token extraction: valid token, missing header, "Basic" scheme, empty token, whitespace.                                                                                              |

### Handler Tests

Test `GatewayHandler` with a mock `GatewayDiscoverer` interface:

- Successful flow returns matching gateways
- RBAC denied returns empty array (200 OK)
- K8s API error returns 500
- Missing namespace returns 400
- Invalid namespace returns 400
- Wrong HTTP method returns 405

### Integration Tests

Use `httptest.Server` to test the full middleware → handler chain, or use `envtest` for tests that need a real K8s API
server.

### E2E Tests

**File:** `server/test/e2e/e2e_test.go`

End-to-end tests run against a live Kubernetes cluster with the model-serving-api already deployed. Tests use Go k8s
clients (`client-go`, `controller-runtime`) to create real resources per scenario and clean up via `t.Cleanup`.

**Prerequisites:**

- Kubernetes cluster with Gateway API CRDs installed and a running gateway controller
- model-serving-api deployed (via `make deploy-server` or equivalent)
- `MODEL_SERVING_API_URL` env var set to the server endpoint (e.g.
  `https://model-serving-api.opendatahub.svc.cluster.local`)

**Environment variables:**

| Variable                          | Required | Default             | Description                           |
|-----------------------------------|----------|---------------------|---------------------------------------|
| `MODEL_SERVING_API_URL`           | Yes      | —                   | Base URL of the deployed server       |
| `MODEL_SERVING_API_GATEWAY_CLASS` | No       | `openshift-default` | GatewayClass to use for test Gateways |

**Test scenarios:**

| Test                                | Description                                                                                        |
|-------------------------------------|----------------------------------------------------------------------------------------------------|
| `TestHealthEndpoints`               | `/healthz` and `/readyz` return 200 with `{"status":"ok"}`                                         |
| `TestSecurityHeaders`               | Response includes `X-Content-Type-Options`, `X-Frame-Options`, `Cache-Control`, `HSTS` headers     |
| `TestMissingAuth`                   | Request without `Authorization` header returns 401                                                 |
| `TestValidation/missing_ns`         | Missing `namespace` query parameter returns 400                                                    |
| `TestValidation/invalid_ns`         | Invalid namespace format returns 400                                                               |
| `TestValidation/wrong_method`       | POST request returns 405                                                                           |
| `TestAuthorizedUserSeesGateways`    | SA with `create llminferenceservices` RBAC sees Gateway with `From: All` listener                  |
| `TestUnauthorizedUserGetsEmptyList` | SA without RBAC gets empty `{"gateways": []}`                                                      |
| `TestListenerFromSame`              | `From: Same` — same namespace sees gateway, different namespace does not                           |
| `TestListenerFromSelector`          | `From: Selector` — namespace with matching labels sees gateway, without does not                   |
| `TestGatewayMetadata`               | Gateway annotations (`openshift.io/display-name`, `openshift.io/description`) returned in response |

Tests use the `//go:build e2e` build tag and are excluded from normal `go test ./...` runs.

**Running:**

```bash
# Via Makefile (creates a passthrough route, runs tests, cleans up):
make test-e2e-server

# Manually:
export MODEL_SERVING_API_URL=https://model-serving-api.opendatahub.svc.cluster.local
go test -tags e2e ./server/test/e2e/ -v -count=1
```

### Testability Interface

```go
type GatewayDiscoverer interface {
    Discover(ctx context.Context, userToken, targetNamespace string) ([]GatewayRef, error)
}
```

The production implementation wires together the K8s client calls. Tests inject a mock.

---

## Implementation Sequence

1. `gateway/types.go` — response types
2. `gateway/status.go` — status extraction
3. `gateway/filter.go` — listener filtering algorithm
4. `middleware/auth.go` — Bearer token extraction
5. `middleware/logging.go` — request logging
6. `middleware/recovery.go` — panic recovery
7. `handlers/health.go` — health probes
8. `gateway/discovery.go` — `GatewayDiscoverer` implementation wiring RBAC + list + filter
9. `handlers/gateways.go` — HTTP handler
10. `config.go` — configuration loading
11. `server.go` — mux setup and middleware chain
12. `main.go` — server bootstrap with graceful shutdown
13. `config/server/*.yaml` — deployment manifests
14. Unit tests for filter, status, auth middleware
15. Handler tests with mock discoverer