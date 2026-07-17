# Publisher-Path Authorization Rules

Extend `authpolicy_llm_isvc_userdefined.yaml` to handle publisher-path routing
with model-level SAR authorization and cross-tenant deny.

## Problem

The `inference-access` rule extracts namespace/name from `request.path.split("/")[1]`
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
| `deny-misrouted-model-header` | not publisher, not batch, valid model header | (always deny) | - | 0 |
| `model-access-path` | path ~ `^/publishers/[^/]+/models/` | `serving.opendatahub.io/models` | `post` | 1 |
| `model-access-path-delegate` | same + `x-maas-user` | `serving.opendatahub.io/models/delegate` | `post-delegate` | 1 |
| `inference-access` | depth >= 2, not `/v1/`, not publisher | `serving.kserve.io/llminferenceservices` | `get` | 1 |
| `inference-access-delegate` | same + `x-maas-user` | `serving.kserve.io/llminferenceservices/delegate` | `post-delegate` | 1 |

A `patternRef` (`has-ns-and-name`: regex `^/[^/]+/[^/]+`) guards against CEL
index-out-of-bounds on short paths like `/health`.

## Cross-tenant deny

The `deny-misrouted-model-header` rule fires when:
1. Path is not a publisher path
2. Path is not a batch path (`/v1/files`, `/v1/batches`)
3. A valid model routing header is present (`^publishers/[^/]+/models/.+$`)

This covers both per-participant paths (`/<ns>/<name>/...`) AND `/v1/` inference
endpoints (`/v1/chat/completions`, `/v1/completions`, etc.) when the model routing
header is present. Uses Authorino's `patternMatching` with `predicate: "false"` -
immediate local 403, no API server round-trip. Priority 0 ensures it fires before
SAR rules.

## Why inference-access excludes all /v1/ paths (not just batch)

The original template used a batch-only exclusion on `inference-access`, which meant
every `/v1/` path got a SAR check with garbage namespace/name extraction (ns=`v1`,
name=`chat` for `/v1/chat/completions`). This had two problems:

1. **Broke `/v1/models` discovery** - SAR for ns=v1/name=models denied legitimate
   model listing requests. A dedicated per-route AuthPolicy for `/v1/models`
   would be the proper solution for endpoints that need their own authorization
   posture.

2. **Broke non-inference services** - any service sharing the gateway with `/v1/`
   paths got SAR checks against `serving.kserve.io/llminferenceservices`.

The `/v1/` exclusion on `inference-access` avoids both issues. The
`deny-misrouted-model-header` rule handles the model routing header case explicitly,
scoped to inference traffic by the header format check rather than by path.

## Model name extraction

Model name is extracted from `request.path` using `split('/models/')[1].split('/v1/')[0]`.
This handles both single-segment (`llama-70b`) and multi-segment (`facebook/opt-125m`)
model names by capturing everything between `/models/` and the first `/v1/` path segment.

Model names containing a literal `/v1/` segment (e.g. `my-org/v1/model`) are not supported -
the extraction would truncate at the first `/v1/`. This is the same limitation as the kserve
HTTPRoute template, which uses `/v1/` as the API path delimiter after the model identity.

## Request flow

| Path | Header | Rule | Effect |
|---|---|---|---|
| `/publishers/<ns>/models/<m>/v1/...` | any | model-access-path | Model SAR |
| `/<ns>/<name>/...` | none | inference-access | Instance SAR |
| `/<ns>/<name>/...` | valid model header | deny-misrouted | 403 (cross-tenant) |
| `/v1/chat/completions` | valid model header | deny-misrouted | 403 (no BBR yet) |
| `/v1/chat/completions` | none | (no rule) | Authn-only (no route without header) |
| `/v1/files/...`, `/v1/batches/...` | any | (no rule) | Authn-only (batch bypass) |
| `/v1/models` | any | (no rule) | Authn-only (discovery) |
| `/health` | none | (no rule) | Authn-only (depth guard) |
| `/health` | valid model header | deny-misrouted | 403 |

## Model routing header

The header name (`x-gateway-model-name` by default) is configurable via
`modelBasedRoutingHeaderName` in the `inferenceservice-config` ConfigMap. The value is
lowercased (Authorino normalizes header keys) and validated as an HTTP token to prevent
CEL injection. The GatewayReconciler watches the ConfigMap's `ingress` key for changes.

Currently used only by the `deny-misrouted-model-header` rule. BBR support (normalizing
`/v1/` + header into publisher format via `resolvedPath` override) is a planned follow-up
that will allow `model-access-path` to authorize BBR traffic instead of denying it.

## Anti-spoofing (delegation)

When `x-maas-user` is present, both the base rule and its delegate counterpart fire (AND
semantics in Authorino - all matched rules must pass). The base rule checks the forwarded
user's access via `resolvedUser`. The delegate rule checks the caller's own identity for
`post-delegate`. Regular users lack delegate permission and get 403 - only trusted callers
like the batch processor SA can forward requests on behalf of others.

## Known limitations

- **Namespace `v1`**: if namespace `v1` exists with real LLMInferenceServices, per-participant
  paths like `/v1/<name>/...` skip instance SAR (authn-only due to `/v1/` exclusion on
  inference-access). Mitigated by
  a ValidatingAdmissionPolicy
  blocking reserved namespace names (`v1`, `v2`, `publishers`).
- **`/v1/` without BBR**: `/v1/` inference endpoints without the model routing header are
  authn-only. Safe because no HTTPRoute matches those paths without the header (gateway
  returns 404). The BBR follow-up will add `resolvedPath` normalization to authorize them.

## Batch processing and dual RBAC domains

Publisher paths introduce a virtual resource (`models.serving.opendatahub.io`) for
model-level authorization. This creates two parallel RBAC domains:

| Access pattern | SAR resource | Verb |
|---|---|---|
| Per-participant (`/<ns>/<name>/...`) | `serving.kserve.io/llminferenceservices` | `get` |
| Publisher (`/publishers/<ns>/models/<m>/...`) | `serving.opendatahub.io/models` | `post` |

The batch processor (see [BATCH.md](BATCH.md) section 3) currently delegates via
per-participant paths using `post-delegate` on `llminferenceservices/delegate`. For
controlled deployment (canary), the batch processor should use the stable model identity
(publisher path or BBR) instead of version-specific instances. This requires new RBAC:
- Batch processor SA: `post-delegate` on `serving.opendatahub.io/models/delegate`
- Users: `post` on `serving.opendatahub.io/models` (via aggregated ClusterRole)

### Known tradeoff: virtual resource

The `models` resource has no backing CRD - it's a SAR-only authorization contract.
This means:
- `kubectl get models.serving.opendatahub.io` returns "not found"
- Operators can't discover what model resources exist by inspecting the cluster
- The `resourceNames` values are path-shaped strings (`publishers/ns/models/name`),
  not standard Kubernetes names
- Debugging authorization spans two API groups and two resource types

The virtual resource is a pragmatic workaround for Kubernetes RBAC not supporting
model-level grouping natively. In practice, most deployments use namespace-scoped
RBAC (`view`/`edit` roles) rather than per-model `resourceNames`, so the dual
domain is transparent - the
[aggregate ClusterRoles](https://github.com/opendatahub-io/kserve/pull/1744)
(`kserve-models-view`/`kserve-models-edit`) ensure namespace viewers/editors
automatically get model access alongside instance access.

For deployments that need per-model RBAC granularity, alternatives considered:
- **Namespace-scoped wildcard SAR**: check `get llminferenceservices` without
  `resourceName` - single call, existing RBAC, but grants access to all models
  in the namespace rather than a specific one. The aggregate ClusterRoles from
  [opendatahub-io/kserve#1744](https://github.com/opendatahub-io/kserve/pull/1744)
  already provide this level of granularity.
- **RBAC reconciler**: a controller watches routing groups and reconciles
  aggregated Roles covering all group members, making model-level SAR resolvable
  via a single call at request time. Moves the O(N) cost to reconciliation.
- **Informer-based ext-authz**: custom gRPC service with warm group membership
  cache (similar to EPP's model routing). O(1) lookup + N concurrent SAR calls.
  Overkill unless auth latency becomes a bottleneck.
