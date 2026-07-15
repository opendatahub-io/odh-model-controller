# Publisher-Path Authorization Rules

Extend `authpolicy_llm_isvc_userdefined.yaml` to handle publisher-path routing
with model-level SAR authorization and cross-tenant deny.

## Problem

The current `inference-access` rule extracts namespace/name from `request.path.split("/")[1]`
and `[2]`. This works for per-participant paths (`/<ns>/<name>/...`) but doesn't cover
[publisher paths](https://github.com/kserve/kserve/pull/5822)
(`/publishers/<ns>/models/<name>/...`), which need model-level authorization against
`serving.opendatahub.io/models`.

Additionally, the kserve HTTPRoute template's `v1-catch-all-model-routing` rule matches
on the model routing header alone (no path constraint). A per-participant path with a
valid model header would be routed by the header (tenant-B) but authorized by the path
(tenant-A) - a cross-tenant authorization bypass.

## Authorization rules (5 total)

Two new model-access rules, one deny rule, two existing rules updated.

| Rule | Fires when | Resource | Verb | Priority |
|---|---|---|---|---|
| `deny-misrouted-model-header` | not `/v1/`, not publisher, valid model header present | (always deny) | - | 0 |
| `model-access-path` | path ~ `^/publishers/[^/]+/models/` | `serving.opendatahub.io/models` | `post` | 1 |
| `model-access-path-delegate` | same + `x-maas-user` | `serving.opendatahub.io/models/delegate` | `post-delegate` | 1 |
| `inference-access` | depth >= 2, not `/v1/`, not publisher | `serving.kserve.io/llminferenceservices` | `get` | 1 |
| `inference-access-delegate` | same + `x-maas-user` | `serving.kserve.io/llminferenceservices/delegate` | `post-delegate` | 1 |

A `patternRef` (`has-ns-and-name`: regex `^/[^/]+/[^/]+`) guards against CEL
index-out-of-bounds on short paths like `/health`.

## Cross-tenant deny

The `deny-misrouted-model-header` rule fires when:
1. Path is not `/v1/` (not a BBR endpoint)
2. Path is not a publisher path
3. A valid model routing header is present (`^publishers/[^/]+/models/.+$`)

Uses Authorino's `patternMatching` with `predicate: "false"` - immediate local 403,
no API server round-trip. Priority 0 ensures it short-circuits before SAR rules.

This closes the routing/authorization identity mismatch: the request is denied before
`inference-access` can authorize it against the wrong tenant's path segments.

## Model name extraction

Model name is extracted from `request.path` using `split('/models/')[1].split('/v1/')[0]`.
This handles both single-segment (`llama-70b`) and multi-segment (`facebook/opt-125m`)
model names by capturing everything between `/models/` and the first `/v1/` path segment.

Model names containing a literal `/v1/` segment (e.g. `my-org/v1/model`) are not supported -
the extraction would truncate at the first `/v1/`. This is the same limitation as the kserve
HTTPRoute template, which uses `/v1/` as the API path delimiter after the model identity.
The ambiguity is structural, not specific to the AuthPolicy.

## Request flow

| Path | Header | Rule | Effect |
|---|---|---|---|
| `/publishers/<ns>/models/<m>/v1/...` | any | model-access-path | Model SAR |
| `/<ns>/<name>/...` | none | inference-access | Instance SAR |
| `/<ns>/<name>/...` | valid model header | deny-misrouted | 403 (cross-tenant) |
| `/v1/...` | any | (no rule fires) | Authn-only |
| `/v1/files/...`, `/v1/batches/...` | any | (no rule fires) | Authn-only (batch) |
| `/health`, `/` | any | (no rule fires) | Authn-only (short path) |

## Model routing header

The header name (`x-gateway-model-name` by default) is configurable via
`modelBasedRoutingHeaderName` in the `inferenceservice-config` ConfigMap. The value is
lowercased (Authorino normalizes header keys) and validated as an HTTP token to prevent
CEL injection. The GatewayReconciler watches the ConfigMap's `ingress` key for changes.

Currently used only by the `deny-misrouted-model-header` rule. BBR support (normalizing
`/v1/` + header into publisher format via `resolvedPath` override) is a planned follow-up.

## Backward compatibility

- Per-participant paths (`/<ns>/<name>/...`) use `inference-access` (instance SAR), unchanged.
- Batch paths (`/v1/files`, `/v1/batches`) remain authn-only.
- Existing `get llminferenceservices` RBAC continues to work.
- `/v1/` paths without model header remain authn-only.

## Anti-spoofing

When `x-maas-user` is present, both the base rule and its delegate counterpart fire (AND
semantics). The delegate rule checks the caller's identity for `post-delegate` or
`post-delegate`. Regular users lack this and get 403.

## Testing

### Unit tests

- 5 authorization rules present in rendered policy
- `resolvedUser`/`resolvedGroups` overrides rendered
- Custom model routing header substituted in deny rule

### E2E tests (13 publisher-path + 20 batch = 33 total)

| Category | Tests |
|---|---|
| Model-access rules | publisher path with/without RBAC, multi-segment model name |
| Cross-RBAC type | model-user on participant path (wrong RBAC type) |
| Namespace collision | ns=publishers participant path, ns=publishers publisher route |
| Cross-tenant deny | participant path + valid model header -> 403 |
| Exclusion boundaries | /v1/models authn-only, /v1/ excluded, /health depth guard, empty header |
| Delegation | delegated model-access (post-delegate), spoofed (lacks post-delegate) |

### CI workflow

Kind-based CI (`.github/workflows/test-authpolicy-e2e.yml`): kind + MetalLB + cert-manager +
Gateway API CRDs + Istio + Kuadrant. Renders the template via `sed` with default values.
Triggered on PR when auth-related files change.
