# Batch APIs Authn/z

Extend `authpolicy_llm_isvc_userdefined.yaml` to handle OpenAI-compatible batch API paths
with authentication and header injection.

## Batch API paths

```
/v1/files                      GET, POST
/v1/files/{file_id}            GET, DELETE
/v1/files/{file_id}/content    GET
/v1/batches                    GET, POST
/v1/batches/{batch_id}         GET
/v1/batches/{batch_id}/cancel  POST
```

## Authorization model

All batch API paths use **authentication only** at the gateway level (Kubernetes TokenReview).
No gateway-level authorization rule is needed — the batch service enforces access control
internally via tenant isolation (`TenantID` from `X-MaaS-User`):

- Every DB query filters by `TenantID`
- Accessing a file/batch belonging to another user returns 404
- Storage is isolated per tenant via `SHA256(tenantID)` folder names

## Header injection

For **all** batch API paths, inject these response headers:

| Header          | Source                        | Notes                                    |
|-----------------|-------------------------------|------------------------------------------|
| `X-MaaS-User`   | `auth.identity.user.username` | CEL expression in `plain`                |
| `X-MaaS-Groups` | `auth.identity.user.groups`   | Needs comma-separated output (see below) |

### X-MaaS-Groups: comma-separated list

`auth.identity.user.groups` is an array. To produce a comma-separated string, use a CEL
`join()` call:

```yaml
x-maas-groups:
  plain:
    expression: "auth.identity.user.groups.join(',')"
```

### Scoping headers to batch paths only

These headers must only be injected for batch API paths. Use `when` predicates on each
response header definition:

```yaml
x-maas-username:
  when:
    - predicate: "request.path.startsWith('/v1/files') || request.path.startsWith('/v1/batches')"
  plain:
    expression: auth.identity.user.username
```

## Impact on existing inference-access rule

The current `inference-access` rule applies to all paths and interprets
`request.path.split("/")[1]` as namespace and `[2]` as ISVC name. Batch paths would produce
nonsensical lookups (namespace=`v1`, name=`files`).

Add a `when` predicate to exclude batch paths:

```yaml
authorization:
  inference-access:
    when:
      - predicate: "!(request.path.startsWith('/v1/files') || request.path.startsWith('/v1/batches'))"
    kubernetesSubjectAccessReview:
    # ... existing spec unchanged ...
```

When the `inference-access` rule is skipped (batch paths), no authorization rules remain,
so Authorino implicitly allows the request — authentication-only behavior.

## Full template sketch

Below is the authorization + response section of the updated
`authpolicy_llm_isvc_userdefined.yaml` (authentication section unchanged):

```yaml
    authorization:
      inference-access:
        when:
          - predicate: "!(request.path.startsWith('/v1/files') || request.path.startsWith('/v1/batches'))"
        kubernetesSubjectAccessReview:
          user:
            expression: auth.identity.user.username
          authorizationGroups:
            expression: auth.identity.user.groups
          resourceAttributes:
            group:
              value: serving.kserve.io
            resource:
              value: llminferenceservices
            namespace:
              expression: request.path.split("/")[1]
            name:
              expression: request.path.split("/")[2]
            verb:
              value: get
        priority: 1
    response:
      success:
        headers:
          # Existing inference headers
          x-gateway-inference-fairness-id:
            when:
              - predicate: "!(request.path.startsWith('/v1/files') || request.path.startsWith('/v1/batches'))"
            metrics: false
            plain:
              expression: auth.identity.fairness
            priority: 0
          x-gateway-inference-objective:
            when:
              - predicate: "!(request.path.startsWith('/v1/files') || request.path.startsWith('/v1/batches'))"
            metrics: false
            plain:
              expression: auth.identity.objective
            priority: 0
          # Batch headers
          x-maas-username:
            when:
              - predicate: "request.path.startsWith('/v1/files') || request.path.startsWith('/v1/batches')"
            plain:
              expression: auth.identity.user.username
            priority: 0
          x-maas-groups:
            when:
              - predicate: "request.path.startsWith('/v1/files') || request.path.startsWith('/v1/batches')"
            plain:
              expression: "auth.identity.user.groups.join(',')"
            priority: 0
```

## Upstream fix: Authorino response headers must overwrite, not append

Authorino's response header injection currently **appends** to existing request headers instead
of overwriting them. If a client sends `x-maas-user: attacker` and Authorino injects
`x-maas-user: real-user`, the backend receives `attacker, real-user`.

This affects **all** response headers — both batch headers (`x-maas-user`, `x-maas-groups`) and
flow control headers (`x-gateway-inference-fairness-id`, `x-gateway-inference-objective`).

### Root cause

In `authorino@v0.20.0/pkg/service/auth.go:420-435`, `buildResponseHeaders()` constructs
`HeaderValueOption` entries without setting the `AppendAction` field:

```go
package main

responseHeaders = append(responseHeaders, &envoy_core.HeaderValueOption{
    Header: &envoy_core.HeaderValue{
        Key:   key,
        Value: value,
    },
})
```

The Envoy `HeaderValueOption` has two fields controlling this behavior:

| Field          | Default (ext_authz context)        | Status     |
|----------------|------------------------------------|------------|
| `Append`       | `false` (overwrite)                | Deprecated |
| `AppendAction` | `APPEND_IF_EXISTS_OR_ADD` (append) | Current    |

Since Authorino sets neither, Envoy uses the `AppendAction` default: **append**.

### Required fix

Authorino must explicitly set `AppendAction: OVERWRITE_IF_EXISTS_OR_ADD` on every
`HeaderValueOption`:

```go
package main

responseHeaders = append(responseHeaders, &envoy_core.HeaderValueOption{
    Header: &envoy_core.HeaderValue{
        Key:   key,
        Value: value,
    },
    AppendAction: envoy_core.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD,
})
```

This ensures Authorino-injected headers replace any client-supplied values with the same name,
preventing header injection attacks and multi-value concatenation.

### Workaround: take the last header value

Until the upstream fix lands, consumers of Authorino-injected headers must always use the
**last** value in multi-value headers.

This is secure because Envoy's ext_authz pipeline guarantees ordering: client-supplied headers
come first, Authorino-injected values are appended last. A client cannot inject headers after
ext_authz processing, so the last value is always the trusted Authorino-injected one.

Example: a client sends `x-maas-user: attacker`. After Authorino appends, the backend receives
`attacker, real-user`. Taking the last comma-separated value yields `real-user`.

**Affected consumers**:

| Header                            | Consumer                 | Status                   |
|-----------------------------------|--------------------------|--------------------------|
| `x-maas-user`                     | Batch service middleware | Needs fix                |
| `x-maas-groups`                   | Batch service middleware | Needs fix                |
| `x-gateway-inference-fairness-id` | EPP                      | Already safe (see below) |
| `x-gateway-inference-objective`   | EPP                      | Already safe (see below) |

**EPP is already safe**: the EPP reads headers via Envoy ext_proc, which delivers duplicate
headers as separate entries in the `Headers` slice. The EPP iterates and overwrites
(`reqCtx.Request.Headers[header.Key] = value`), so the last entry (Authorino's) wins
(`epp/handlers/request.go:57-67`).

**Batch service** needs to take the last value when reading `X-MaaS-User` and `X-MaaS-Groups`
from request headers (split by `, ` and take the last element, or equivalent).

Batch Gateway fixes

- https://github.com/llm-d-incubation/batch-gateway/pull/87
- https://github.com/llm-d-incubation/batch-gateway/pull/92

## Batch-gateway service compatibility

The batch-gateway service (`batch-gateway` repo) implements the OpenAI `/v1/files` and
`/v1/batches` endpoints. The following changes are needed to make it compatible with the
Authorino authn/z flow described above.

### 1. What already works — no changes needed

- **ID format**: file IDs (`file_<UUID>`) and batch IDs (`batch_<UUID>`) remain unchanged.
- **Storage isolation**: `GetFolderNameByTenantID()` (`com.go:40-55`) uses `SHA256(tenantID)`
  to derive folder names.
- **DB-level tenant isolation**: every query includes `TenantID` in the filter. A user
  accessing a file/batch belonging to another user gets 404.
- **Listing** (`GET /v1/files`, `GET /v1/batches`): `ListFiles()` and `ListBatches()` already
  filter by `TenantID` (from `X-MaaS-User`). Results are scoped to the calling user.
  Pagination uses integer cursors, not IDs.
- **Batch input file reference**: `POST /v1/batches` references an `input_file_id` in the
  request body. The batch service looks up the file by full ID with tenant isolation.

## Batch processor → gateway → inference backend authn/z

The batch processor (`cmd/batch-processor/main.go`) dequeues jobs and sends individual
inference requests to model serving backends. These requests must go **through the gateway**
so that Authorino handles per-model authorization using the original user's identity.

```
User ──► Gateway (Authorino) ──► Batch API server     (batch creation)
                                       │
                                       ▼
                                     Queue
                                       │
                                       ▼
                                 Batch Processor
                                       │
                                       ▼
                                 Gateway (Authorino) (per-model authz) ──► Inference Backend
```

### Current state

- **Routing**: processor sends requests directly to the backend, bypassing the gateway
  (`inference/client.go` — `gateway_url` config points to the backend, not the gateway).
- **Authentication**: shared API key (`inference/client.go:94`), not a Kubernetes token.
- **Authorization**: none — the processor does not check whether the original user has
  permission to access the models referenced in the batch.
- **User context**: the API server stores `TenantID` (username) with the batch record, but
  the processor does not forward `X-MaaS-User` or `X-MaaS-Groups` to the backend.

### Required changes

#### 1. Store user context at batch creation time

The API server must persist the user's identity with the batch job so the processor can
forward it later. Currently only `TenantID` (username) is stored.

**Needed fields** (in `BatchItem` / `BatchSpec` or a new `UserContext` struct):

| Field      | Source header   | Purpose                                |
|------------|-----------------|----------------------------------------|
| `Username` | `X-MaaS-User`   | Forwarded to gateway for delegated SAR |
| `Groups`   | `X-MaaS-Groups` | Forwarded to gateway for delegated SAR |

These headers are injected by Authorino at batch creation time and should be extracted by
the API server middleware and stored alongside the batch record.

**Files**: `batch_handler.go` (CreateBatch), `batch_item.go` (storage schema)

#### 2. Route inference requests through the gateway

Change the processor's target URL from the inference backend to the gateway.

```yaml
# cmd/batch-processor/config.yaml
inference_config:
  gateway_url: "https://<gateway-host>"   # was: http://localhost:8000 (direct to backend)
```

The processor authenticates to the gateway with its **own ServiceAccount token** (not the
original user's token, which may have expired). It forwards the original user's identity
via `X-MaaS-User` and `X-MaaS-Groups` headers.

```go
package inference

// inference/client.go – per-request setup
saToken := getServiceAccountToken() // mounted at /var/run/secrets/kubernetes.io/serviceaccount/token
req.Header.Set("Authorization", "Bearer " + saToken)
req.Header.Set("X-MaaS-User", job.UserContext.Username)
req.Header.Set("X-MaaS-Groups", job.UserContext.Groups)
```

**File**: `inference/client.go` (replace API key auth with SA token + forwarded headers)

#### 3. Single authorization rule with conditional resource attributes

The gateway's existing `inference-access` rule uses the bearer token's identity for
SubjectAccessReview. Instead of two separate rules (one for direct users, one for the
batch processor), a **single rule** uses conditional CEL expressions to vary the SAR
based on the presence of `x-maas-user` headers.

When the batch processor forwards a request through the gateway, it sets `X-MaaS-User`
and `X-MaaS-Groups` headers with the original user's identity. The single rule detects
this and switches to a delegated SAR:

| Condition                    | SAR user                         | SAR groups                                    | Resource                        | Verb   |
|------------------------------|----------------------------------|-----------------------------------------------|---------------------------------|--------|
| No `x-maas-user` header      | `auth.identity.user.username`    | `auth.identity.user.groups`                   | `llminferenceservices`          | `get`  |
| `x-maas-user` header present | `request.headers['x-maas-user']` | `request.headers['x-maas-groups'].split(',')` | `llminferenceservices/delegate` | `post` |

```yaml
authorization:
  inference-access:
    when:
      - predicate: "!(request.path.startsWith('/v1/files') || request.path.startsWith('/v1/batches'))"
    kubernetesSubjectAccessReview:
      user:
        expression: "'x-maas-user' in request.headers ? request.headers['x-maas-user'] : auth.identity.user.username"
      authorizationGroups:
        expression: "'x-maas-groups' in request.headers ? request.headers['x-maas-groups'].split(',') : auth.identity.user.groups"
      resourceAttributes:
        group:
          value: serving.kserve.io
        resource:
          expression: "'x-maas-user' in request.headers ? 'llminferenceservices/delegate' : 'llminferenceservices'"
        namespace:
          expression: request.path.split("/")[1]
        name:
          expression: request.path.split("/")[2]
        verb:
          expression: "'x-maas-user' in request.headers ? 'post' : 'get'"
    priority: 1
```

**Security model**: the delegated path uses a distinct resource (`llminferenceservices/delegate`)
and verb (`post`). This means RBAC controls which identities can perform delegated access:

- The batch processor SA needs a `ClusterRole` granting `post` on `llminferenceservices/delegate`.
  The SAR checks the **forwarded user** (from `x-maas-user`) for this permission, not the
  processor SA itself. So the users who should be eligible for batch inference need the
  `post llminferenceservices/delegate` permission (or the existing `get llminferenceservices`
  role can be extended to also grant `post` on the `delegate` subresource).
- A regular user spoofing `x-maas-user` headers on a direct request would trigger the delegated
  path, but the SAR would check the spoofed username for `post llminferenceservices/delegate`.
  This limits the blast radius to what the spoofed identity has access to, not the attacker's
  own permissions.

**Header spoofing mitigation**: to prevent a regular user from impersonating another user
via `x-maas-user` headers, the `post llminferenceservices/delegate` permission should **not**
be granted to regular users directly. Instead, only the batch processor ServiceAccount should
hold this permission. The SAR then needs to check the **authenticated caller** (processor SA)
for the `delegate` permission, and the **forwarded user** for the base `get llminferenceservices`
permission. This can be achieved with two approaches:

1. **Two SARs in one rule** (if Authorino supports it): check both the caller's `delegate`
   permission and the forwarded user's `get` permission.
2. **Accept the risk**: if all users who have `get llminferenceservices` should also be able
   to use batch, grant them `post llminferenceservices/delegate` too. Header spoofing then
   only allows accessing models the spoofed user has access to — which may be acceptable
   given the threat model.

**Open items**:

- ~~Verify that `has(request.headers['x-maas-user'])` works in Authorino CEL expressions.~~
  **Resolved**: `has()` with bracket-notation map access is invalid in Authorino's CEL.
  Use `'x-maas-user' in request.headers` instead (the `in` operator for map key existence).
- Determine whether `resource: 'llminferenceservices/delegate'` is treated as a resource
  with subresource in the SAR, or if separate `resource` and `subResource` fields are needed.
- Define the RBAC policy for `post llminferenceservices/delegate` — decide between
  granting it to all inference users vs. only the batch processor SA (see security discussion above).

#### 4. Flow control: Fairness and objective

Because the processor now routes through the gateway, Authorino injects
`x-gateway-inference-fairness-id` and `x-gateway-inference-objective` headers automatically
via the existing response rules. However, the current fairness/objective values are derived
from the processor's ServiceAccount token — not the original user.

The `fairness` override in the authentication section is set to `{{.Issuer}}` and the
`objective` override uses a CEL expression that extracts the namespace from ServiceAccount
usernames. For processor requests, this would produce the processor's namespace rather than
the original user's context.

**Options**:

- **Batch-class fairness**: accept this behavior — all batch requests get grouped under the
  processor's identity, forming a single fairness bucket separate from interactive traffic.
  This is the simplest approach and gives operators a clear knob to control batch vs
  interactive priority via `InferenceObjective`.
- **Per-user fairness**: override the fairness/objective values for delegated requests using
  the forwarded headers. This requires additional response header rules gated on the
  processor's SA identity, similar to the delegated authorization rule.
- ... a few others

## Full rendered AuthPolicy example

Below is the complete AuthPolicy as it would appear in the cluster, based on the existing
`openshift-ai-inference-authn` policy with all batch changes applied. Template variables are
rendered with example values.

```yaml
apiVersion: kuadrant.io/v1
kind: AuthPolicy
metadata:
  name: openshift-ai-inference-authn
  namespace: openshift-ingress
spec:
  targetRef:
    group: gateway.networking.k8s.io
    kind: Gateway
    name: openshift-ai-inference
  rules:
    authentication:
      kubernetes-user:
        kubernetesTokenReview:
          audiences:
            - https://kubernetes.default.svc
        overrides:
          fairness:
            value: "https://kubernetes.default.svc"
          objective:
            expression: "auth.identity.user.username.startsWith('system:serviceaccount:') ? auth.identity.user.username.split(':')[2] : 'authenticated'"

    authorization:
      # Single rule — conditional resource attributes based on x-maas-user header presence
      inference-access:
        when:
          - predicate: "!(request.path.startsWith('/v1/files') || request.path.startsWith('/v1/batches'))"
        kubernetesSubjectAccessReview:
          user:
            expression: "'x-maas-user' in request.headers ? request.headers['x-maas-user'] : auth.identity.user.username"
          authorizationGroups:
            expression: "'x-maas-groups' in request.headers ? request.headers['x-maas-groups'].split(',') : auth.identity.user.groups"
          resourceAttributes:
            group:
              value: serving.kserve.io
            resource:
              expression: "'x-maas-user' in request.headers ? 'llminferenceservices/delegate' : 'llminferenceservices'"
            namespace:
              expression: request.path.split("/")[1]
            name:
              expression: request.path.split("/")[2]
            verb:
              expression: "'x-maas-user' in request.headers ? 'post' : 'get'"
        priority: 1

    response:
      success:
        headers:
          # Flow control headers
          x-gateway-inference-fairness-id:
            metrics: false
            plain:
              expression: auth.identity.fairness
            priority: 0
          x-gateway-inference-objective:
            metrics: false
            plain:
              expression: auth.identity.objective
            priority: 0

          # Batch headers — only for batch paths
          x-maas-user:
            when:
              - predicate: "request.path.startsWith('/v1/files') || request.path.startsWith('/v1/batches')"
            plain:
              expression: auth.identity.user.username
            priority: 0
          x-maas-groups:
            when:
              - predicate: "request.path.startsWith('/v1/files') || request.path.startsWith('/v1/batches')"
            plain:
              expression: "auth.identity.user.groups.join(',')"
            priority: 0
```

**Example values used above**:

| Template variable                   | Rendered value                                                                                                                   |
|-------------------------------------|----------------------------------------------------------------------------------------------------------------------------------|
| `{{.Name}}`                         | `openshift-ai-inference-authn`                                                                                                   |
| `{{.GatewayNamespace}}`             | `openshift-ingress`                                                                                                              |
| `{{.GatewayName}}`                  | `openshift-ai-inference`                                                                                                         |
| `{{.AudiencesJSON}}`                | `["https://kubernetes.default.svc"]`                                                                                             |
| `{{.Issuer}}`                       | `https://kubernetes.default.svc`                                                                                                 |
| `{{.ObjectiveExpression}}`          | `auth.identity.user.username.startsWith('system:serviceaccount:') ? auth.identity.user.username.split(':')[2] : 'authenticated'` |

## Testing

### Test infrastructure: echo service

Deploy an echo server behind the gateway to verify Authorino behavior. The echo server
reflects request headers and body, making it easy to inspect what Authorino injected.

All test identities are ServiceAccounts with specific RBAC — no dependency on logged-in
user tokens or `oc whoami -t`.

#### ServiceAccounts

| SA                   | Namespace      | Purpose                                           | RBAC                                                   |
|----------------------|----------------|---------------------------------------------------|--------------------------------------------------------|
| `test-user`          | `echo-service` | Regular user with standard inference access only  | `get llminferenceservices` in `echo-service`           |
| `test-user-delegate` | `echo-service` | User eligible for batch (has delegate permission) | `post llminferenceservices/delegate` in `echo-service` |

#### Test scenarios

| # | Scenario                                                     | SA token             | Path                            | Extra headers                        | Expected SAR                                                  | Expected                                                 |
|---|--------------------------------------------------------------|----------------------|---------------------------------|--------------------------------------|---------------------------------------------------------------|----------------------------------------------------------|
| 1 | SA → batch path                                              | `test-user`          | `/v1/batches`                   | —                                    | Skipped (batch path)                                          | 200, `x-maas-user` and `x-maas-groups` injected          |
| 2 | SA → inference path (standard)                               | `test-user`          | `/echo-service/echo-server/...` | —                                    | `get llminferenceservices` for `test-user`                    | 200                                                      |
| 3 | SA → inference with `x-maas-user` (user has delegate RBAC)   | `test-user`          | `/echo-service/echo-server/...` | `x-maas-user: ...test-user-delegate` | `post llminferenceservices/delegate` for `test-user-delegate` | 200                                                      |
| 4 | SA → batch path with spoofed header                          | `test-user`          | `/v1/batches`                   | `x-maas-user: spoofed`               | Skipped (batch path)                                          | 200, Authorino appends real identity after spoofed value |
| 5 | SA → inference with `x-maas-user` (user has no RBAC)         | `test-user`          | `/echo-service/echo-server/...` | `x-maas-user: nonexistent-user`      | `post llminferenceservices/delegate` for `nonexistent-user`   | 403                                                      |
| 6 | SA → inference with `x-maas-user` (user lacks delegate RBAC) | `test-user-delegate` | `/echo-service/echo-server/...` | `x-maas-user: ...test-user`          | `post llminferenceservices/delegate` for `test-user`          | 403                                                      |

#### gateway.yaml

The GatewayClass and Gateway that the tests assume are already deployed. On OpenShift, the
`openshift-default` GatewayClass is provided by the platform. The Gateway must have Authorino
configured (AuthPolicy applied by odh-model-controller).

```yaml
# Gateway infrastructure for Authorino-based authn/z testing.
#
# On OpenShift the GatewayClass already exists — only apply the Gateway.
#
# Apply:
#   oc apply -f gateway.yaml
#
# --- GatewayClass (already exists on OCP — shown for reference) ---
apiVersion: gateway.networking.k8s.io/v1
kind: GatewayClass
metadata:
  name: openshift-default
spec:
  controllerName: openshift.io/gateway-controller/v1
---
# --- Gateway ---
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: openshift-ai-inference
  namespace: openshift-ingress
spec:
  gatewayClassName: openshift-default
  listeners:
    - name: http
      port: 80
      protocol: HTTP
      allowedRoutes:
        namespaces:
          from: All
```

The listener should set `allowedRoutes.namespaces.from: All` to accept HTTPRoutes from any
namespace. Without this, the default is `Same` (only the gateway's own namespace), and
cross-namespace HTTPRoutes will be rejected with `NotAllowedByListeners`.

The internal address of the gateway (for in-cluster testing) follows the OCP naming convention:

```
http://openshift-ai-inference-openshift-default.openshift-ingress.svc.cluster.local
```

The e2e tests discover this address automatically from the Gateway resource's
`.status.addresses[0].value`.

#### echo-service.yaml

```yaml
# Echo service + test ServiceAccounts for verifying Authorino batch authn/z.
#
# Apply:
#   oc apply -f echo-service.yaml
#
# --- Namespace ---
apiVersion: v1
kind: Namespace
metadata:
  name: echo-service
---
# --- ServiceAccounts ---
# Regular user — has standard inference access only
apiVersion: v1
kind: ServiceAccount
metadata:
  name: test-user
  namespace: echo-service
---
# User with delegate permission — eligible for batch inference
apiVersion: v1
kind: ServiceAccount
metadata:
  name: test-user-delegate
  namespace: echo-service
---
# --- Echo server ---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: echo-server
  namespace: echo-service
  labels:
    app.kubernetes.io/name: echo-server
spec:
  replicas: 1
  selector:
    matchLabels:
      app.kubernetes.io/name: echo-server
  template:
    metadata:
      labels:
        app.kubernetes.io/name: echo-server
    spec:
      containers:
        - name: echo-server
          image: ealen/echo-server:0.9.2
          ports:
            - containerPort: 8080
              protocol: TCP
          env:
            - name: PORT
              value: "8080"
          resources:
            requests:
              cpu: 50m
              memory: 64Mi
            limits:
              cpu: 200m
              memory: 128Mi
---
apiVersion: v1
kind: Service
metadata:
  name: echo-server
  namespace: echo-service
spec:
  selector:
    app.kubernetes.io/name: echo-server
  ports:
    - port: 80
      targetPort: 8080
      protocol: TCP
---
# --- HTTPRoutes ---
# Route batch API paths to the echo server
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: echo-server-batch
  namespace: echo-service
spec:
  parentRefs:
    - name: openshift-ai-inference
      namespace: openshift-ingress
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /v1/batches
      backendRefs:
        - name: echo-server
          port: 80
    - matches:
        - path:
            type: PathPrefix
            value: /v1/files
      backendRefs:
        - name: echo-server
          port: 80
---
# Route a fake inference path to the echo server for testing the
# conditional authorization rule (standard vs delegated SAR).
# Path format: /echo-service/echo-server/... so the SAR extracts
# namespace=echo-service, name=echo-server from the path.
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: echo-server-inference
  namespace: echo-service
spec:
  parentRefs:
    - name: openshift-ai-inference
      namespace: openshift-ingress
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /echo-service/echo-server
      backendRefs:
        - name: echo-server
          port: 80
```

#### echo-service-rbac.yaml

RBAC resources for the test ServiceAccounts. Two ClusterRoles define the two
permission levels; RoleBindings in `echo-service` namespace grant them to specific SAs.

```yaml
# RBAC for testing the conditional authorization rule.
#
# Apply:
#   oc apply -f echo-service-rbac.yaml
#
# --- ClusterRoles ---
# Standard inference access: get llminferenceservices
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: llminferenceservices-reader
rules:
  - apiGroups: ["serving.kserve.io"]
    resources: ["llminferenceservices"]
    verbs: ["get"]
---
# Delegated inference access: post llminferenceservices/delegate
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: llminferenceservices-delegate
rules:
  - apiGroups: ["serving.kserve.io"]
    resources: ["llminferenceservices/delegate"]
    verbs: ["post"]
---
# --- RoleBindings for test-user (standard access only) ---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: test-user-inference-access
  namespace: echo-service
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: llminferenceservices-reader
subjects:
  - kind: ServiceAccount
    name: test-user
    namespace: echo-service
---
# --- RoleBindings for test-user-delegate (standard + delegate access) ---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: test-user-delegate-inference-access
  namespace: echo-service
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: llminferenceservices-reader
subjects:
  - kind: ServiceAccount
    name: test-user-delegate
    namespace: echo-service
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: test-user-delegate-delegate-access
  namespace: echo-service
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: llminferenceservices-delegate
subjects:
  - kind: ServiceAccount
    name: test-user-delegate
    namespace: echo-service
```

#### Test commands

```bash
# Gateway internal address (from dnsutils pod in default namespace)
GW=http://openshift-ai-inference-openshift-default.openshift-ingress.svc.cluster.local

# --- Create tokens ---
TEST_USER_TOKEN=$(oc create token test-user -n echo-service)
TEST_USER_DELEGATE_TOKEN=$(oc create token test-user-delegate -n echo-service)

# Scenario 1: SA → batch path (authn only, headers injected)
# Expected: 200, echo shows x-maas-user = system:serviceaccount:echo-service:test-user
oc exec -n default dnsutils -- curl -vk -s \
  -H "Authorization: Bearer $TEST_USER_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"hello":"world"}' \
  $GW/v1/batches | jq

# Scenario 2: SA → inference path (standard SAR: get llminferenceservices)
# Expected: 200 (test-user has get llminferenceservices in echo-service)
oc exec -n default dnsutils -- curl -vk -s \
  -H "Authorization: Bearer $TEST_USER_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"hello":"world"}' \
  $GW/echo-service/echo-server/v1/chat/completions | jq

# Scenario 3: SA → inference with x-maas-user pointing to user WITH delegate RBAC
# Expected: 200 (test-user-delegate has post llminferenceservices/delegate in echo-service)
oc exec -n default dnsutils -- curl -vk -s \
  -H "Authorization: Bearer $TEST_USER_TOKEN" \
  -H "Content-Type: application/json" \
  -H "x-maas-user: system:serviceaccount:echo-service:test-user-delegate" \
  -H "x-maas-groups: system:serviceaccounts,system:serviceaccounts:echo-service,system:authenticated" \
  -d '{"hello":"world"}' \
  $GW/echo-service/echo-server/v1/chat/completions | jq

# Scenario 4: SA → batch path with spoofed x-maas-user (batch path — authz skipped)
# Expected: 200, Authorino appends real identity after spoofed value
oc exec -n default dnsutils -- curl -vk -s \
  -H "Authorization: Bearer $TEST_USER_TOKEN" \
  -H "Content-Type: application/json" \
  -H "x-maas-user: spoofed-user" \
  -d '{"hello":"world"}' \
  $GW/v1/batches | jq

# Scenario 5: SA → inference with x-maas-user pointing to nonexistent user (should fail)
# Expected: 403 (nonexistent-user has no RBAC)
oc exec -n default dnsutils -- curl -vk -s \
  -H "Authorization: Bearer $TEST_USER_TOKEN" \
  -H "Content-Type: application/json" \
  -H "x-maas-user: nonexistent-user" \
  -d '{"hello":"world"}' \
  $GW/echo-service/echo-server/v1/chat/completions | jq

# Scenario 6: SA → inference with x-maas-user pointing to user WITHOUT delegate RBAC (should fail)
# Expected: 403 (test-user has get but NOT post llminferenceservices/delegate)
oc exec -n default dnsutils -- curl -vk -s \
  -H "Authorization: Bearer $TEST_USER_DELEGATE_TOKEN" \
  -H "Content-Type: application/json" \
  -H "x-maas-user: system:serviceaccount:echo-service:test-user" \
  -H "x-maas-groups: system:serviceaccounts,system:serviceaccounts:echo-service,system:authenticated" \
  -d '{"hello":"world"}' \
  $GW/echo-service/echo-server/v1/chat/completions | jq
```


#### What to verify

**Scenario 1** (SA → batch path): Response should include injected headers.
Look for `x-maas-user: system:serviceaccount:echo-service:test-user` and `x-maas-groups`
with the SA's groups. No authorization check happens (batch path excluded).

**Scenario 2** (SA → inference path, standard): Standard SAR fires — checks
`system:serviceaccount:echo-service:test-user` for `get llminferenceservices` resource
`echo-server` in namespace `echo-service`. Should succeed (RBAC granted). Response should
include flow control headers but NOT `x-maas-user`/`x-maas-groups`.

**Scenario 3** (SA → inference with `x-maas-user`, user has delegate RBAC): Delegated SAR
fires — checks `system:serviceaccount:echo-service:test-user-delegate` (from `x-maas-user`
header) for `post llminferenceservices/delegate`. Should succeed (`test-user-delegate` has
delegate RBAC).

**Scenario 4** (SA → batch path with spoofed header): Batch path is excluded from
authorization, so the spoofed header has no effect on authz. Authorino appends the real
identity after the spoofed value (upstream append bug). Echo shows both values.

**Scenario 5** (SA → inference with spoofed header): The `x-maas-user` header triggers
the delegated SAR path. SAR checks `nonexistent-user` for `post llminferenceservices/delegate`
— fails with 403.

**Scenario 6** (SA → inference with `x-maas-user`, user lacks delegate RBAC): Delegated SAR
checks `system:serviceaccount:echo-service:test-user` for `post llminferenceservices/delegate`
— fails with 403 because `test-user` only has `get llminferenceservices`, not the delegate
permission. This validates that standard inference access does not automatically grant
delegated access.
