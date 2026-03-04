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

#### 3. Delegated authorization in AuthPolicy

The gateway's existing `inference-access` rule uses the bearer token's identity for
SubjectAccessReview. When the batch processor calls, the bearer token belongs to the
processor's ServiceAccount — not the original user. The AuthPolicy needs a **delegated
authorization** rule that:

1. Recognizes the caller as the batch processor (by ServiceAccount name)
2. Uses the forwarded `X-MaaS-User` / `X-MaaS-Groups` headers for the SAR instead of
   the token identity

```yaml
authorization:
  # Existing rule — for direct user requests
  inference-access:
    when:
      - predicate: >-
          !(request.path.startsWith('/v1/files') || request.path.startsWith('/v1/batches'))
          && !auth.identity.user.username.startsWith('system:serviceaccount:{{.BatchProcessorNamespace}}:{{.BatchProcessorServiceAccount}}')
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

  # Delegated rule — for batch processor requests
  inference-access-delegated-batch:
    when:
      - predicate: >-
          !(request.path.startsWith('/v1/files') || request.path.startsWith('/v1/batches'))
          && auth.identity.user.username == 'system:serviceaccount:{{.BatchProcessorNamespace}}:{{.BatchProcessorServiceAccount}}'
    kubernetesSubjectAccessReview:
      user:
        expression: request.headers['x-maas-username']
      authorizationGroups:
        expression: "request.headers['x-maas-groups'].split(',')"
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
```

**Security**: the delegated rule is gated by `auth.identity.user.username == 'system:serviceaccount:...'`
— only requests authenticated as the batch processor's ServiceAccount can use forwarded
headers for authorization. Regular users always hit the `inference-access` rule which uses
their own token identity. This prevents header-injection attacks from external clients.

**Open items**:

- Verify that `request.headers['x-maas-username']` is accessible in Authorino CEL
  expressions for `kubernetesSubjectAccessReview.user`. The `request` context should be
  available, but this needs validation.
- Verify that `request.headers['x-maas-groups'].split(',')` produces a list that Authorino
  accepts for `authorizationGroups`. The `split()` CEL function returns `list(string)`.
- The processor ServiceAccount name/namespace must be known at template render time. Add
  `{{.BatchProcessorNamespace}}` and `{{.BatchProcessorServiceAccount}}` template variables.

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
      # Direct user requests — skip for batch paths and batch processor SA
      inference-access:
        when:
          - predicate: >-
              !(request.path.startsWith('/v1/files') || request.path.startsWith('/v1/batches'))
              && !auth.identity.user.username.startsWith('system:serviceaccount:batch-gateway:batch-processor')
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

      # Delegated rule — batch processor forwards original user identity via headers
      inference-access-delegated-batch:
        when:
          - predicate: >-
              !(request.path.startsWith('/v1/files') || request.path.startsWith('/v1/batches'))
              && auth.identity.user.username == 'system:serviceaccount:batch-gateway:batch-processor'
        kubernetesSubjectAccessReview:
          user:
            expression: request.headers['x-maas-user']
          authorizationGroups:
            expression: "request.headers['x-maas-groups'].split(',')"
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
| `{{.BatchProcessorNamespace}}`      | `batch-gateway`                                                                                                                  |
| `{{.BatchProcessorServiceAccount}}` | `batch-processor`                                                                                                                |

