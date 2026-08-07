# Publisher-Path Authorization Rules

Extend `authpolicy_llm_isvc_userdefined.yaml` to handle publisher-path routing
and model-based-routing with model-level SAR authorization.

## Problem

The `inference-access` rule extracts namespace/name from `request.path.split("/")[1]`
and `[2]`. This works for per-participant paths (`/<ns>/<name>/...`) but doesn't cover
[publisher paths](https://github.com/kserve/kserve/pull/5822)
(`/publishers/<ns>/models/<name>/...`), which need model-level authorization against
`serving.opendatahub.io/models`.

Additionally, the kserve HTTPRoute template's `v1-model-routing` and
`v1-catch-all-model-routing` rules dispatch requests by the model routing header
(`publishers/<ns>/models/<name>`) rather than the path - e.g. `/v1/chat/completions`
plus a header routes to that model's backend. Such requests must be authorized against
the model identity carried in the header, or a caller could reach a model they are not
authorized for (cross-tenant bypass).

## Authorization rules (6 total)

Two publisher-path rules, two model-header rules, two per-participant rules.

| Rule | Fires when | Resource | Verb | Priority |
|---|---|---|---|---|
| `model-access-path` | path ~ `^/publishers/[^/]+/models/` | `serving.opendatahub.io/models` | `post` | 1 |
| `model-access-path-delegate` | same + `x-maas-user` | `serving.opendatahub.io/models/delegate` | `post-delegate` | 1 |
| `model-access-header` | model-API path, not publisher, not batch, valid model header | `serving.opendatahub.io/models` | `post` | 1 |
| `model-access-header-delegate` | same + `x-maas-user` | `serving.opendatahub.io/models/delegate` | `post-delegate` | 1 |
| `inference-access` | depth >= 2, not model-API root, not publisher | `serving.kserve.io/llminferenceservices` | `get` | 1 |
| `inference-access-delegate` | same + `x-maas-user` | `serving.kserve.io/llminferenceservices/delegate` | `post-delegate` | 1 |

A `patternRef` (`has-ns-and-name`: regex `^/[^/]+/[^/]+`) guards against CEL
index-out-of-bounds on short paths like `/health`.

## Named patterns

The positive regexes reused across rules are single-sourced as named `patterns`
and referenced with `patternRef`:

| Pattern | Selector | Regex | Meaning |
|---|---|---|---|
| `has-ns-and-name` | path | `^/[^/]+/[^/]+` | at least 2 path segments |
| `is-short-path` | path | `^/[^/]*/?$` | fewer than 2 segments (root / single endpoint) |
| `is-publisher-path` | path | `^/publishers/[^/]+/models/` | publisher path |
| `is-model-api-root` | path | `^/(v1\|inference/v1)/` | model-API root at position 0 |
| `is-batch-path` | path | `^/v1/(files\|batches)($\|/)` | OpenAI batch bypass |
| `valid-model-header` | model header | `^publishers/[^/]+/models/.+$` | header carries a model id |
| `has-maas-user` | `x-maas-user` header | `.+` | delegation marker present (non-empty) |

**API constraint.** Kuadrant named patterns are an `allOf` of
`selector`/`operator`/`value` — positive `matches` only, no CEL, no OR, no
negation — and a `patternRef` in a `when` block cannot be negated. Consequently:

- Positive reused checks are patterns referenced by `patternRef`.
- The model-header path scope (an OR of "model-API root" and "short path") is
  expressed as `any: [patternRef is-model-api-root, patternRef is-short-path]`.
- Negations remain CEL `predicate`s, but each reuses the **same** regex value as
  its pattern (e.g. `!request.path.matches('^/publishers/[^/]+/models/')` mirrors
  `is-publisher-path`), so every regex is defined once.

`has-maas-user` uses `matches ".+"` (present and non-empty) rather than the CEL
`'x-maas-user' in request.headers` (present, possibly empty). The batch processor
always forwards a non-empty user, so this is parity in practice and marginally
stricter.

## Model-based-routing authorization

The `model-access-header` rule fires when:
1. Path is not a publisher path (those are covered by `model-access-path`)
2. Path is not a batch path (`/v1/files`, `/v1/batches`)
3. Path is a **model-API path**, not a per-participant path (see below)
4. A valid model routing header is present (`^publishers/[^/]+/models/.+$`)

**Model-API vs per-participant.** A model-routed request reaches a model backend by
header, on a model-API path. A per-participant request reaches a specific instance by
path (`/<ns>/<name>/...`). Gateway API path-prefix precedence beats header-only
routing, so on a per-participant path the header is inert - the request routes by path -
and `inference-access` (get on the LLMInferenceService) is the correct authorization.
The header rule must therefore *not* fire on per-participant paths, or it would AND a
model SAR on top of instance access and deny legitimate instance callers.

The discriminator (predicate 3) is:

```
request.path.startsWith('/v1/')
  || request.path.startsWith('/inference/v1/')
  || !request.path.matches('^/[^/]+/[^/]+')
```

- `/v1/...` and `/inference/v1/...` are model-API roots (the full vLLM/OpenAI surface:
  `/v1/chat/completions`, `/v1/completions`, `/v1/responses`, `/v1/messages`,
  `/inference/v1/generate`, ...).
- `!matches('^/[^/]+/[^/]+')` catches single-segment endpoints that also carry a model
  (`/tokenize`, `/detokenize`) and short paths (`/health`).
- Everything else with >=2 segments (`/<ns>/<name>/...`) is per-participant and falls to
  `inference-access`.

**Positional matching is a security boundary.** The model-API roots are matched
anchored at position 0 (`^/(v1|inference/v1)/`), not anywhere in the path. On a
per-participant or ops path — `/<ns>/<name>/v1/chat/completions`,
`/<ns>/<name>/scale_elastic_ep`, `/<ns>/<name>/tokenize` — the API portion (if any)
is a *suffix*, so `is-model-api-root` does not match and `model-access-header` does
not fire. A caller with access to *some* model therefore cannot set the header on
another tenant's `/<ns>/<name>/scale_elastic_ep` to have their model SAR stand in
for the instance `get` SAR: the header is inert, `inference-access` runs the instance
SAR, and a non-instance caller is denied. Direct root-level endpoints
(`/tokenize`, `/detokenize`) are single-segment (`is-short-path`), so with a valid
header they *do* route by header and are authorized by `model-access-header`.

When it fires, the rule authorizes against the SAME `publishers/<ns>/models/<name>`
identity the HTTPRoute routes on - namespace and model name are read from the header,
not the path - so authorization and routing cannot diverge. A caller without model RBAC
on the header's model gets 403; a caller with it is served.

The gateway EnvoyFilter (`envoyfilter_ssl.yaml`) populates this header by extracting
the `model` field from the JSON request body (`json_to_metadata` + `lua`), so clients
send the model in the body and the header is derived server-side.

## Why inference-access excludes model-API roots (not just batch)

The original template used a batch-only exclusion on `inference-access`, which meant
every `/v1/` path got a SAR check with garbage namespace/name extraction (ns=`v1`,
name=`chat` for `/v1/chat/completions`). This had two problems:

1. **Broke `/v1/models` discovery** - SAR for ns=v1/name=models denied legitimate
   model listing requests. A dedicated per-route AuthPolicy for `/v1/models`
   would be the proper solution for endpoints that need their own authorization
   posture.

2. **Broke non-inference services** - any service sharing the gateway with `/v1/`
   paths got SAR checks against `serving.kserve.io/llminferenceservices`.

`inference-access` therefore excludes the model-API roots `/v1/` **and**
`/inference/v1/`. The `/inference/v1/` exclusion matters for `/inference/v1/generate`:
it has 2+ segments and does not start with `/v1/`, so without the extra clause it would
be mis-read as a per-participant path (ns=`inference`, name=`v1`). Model-routed traffic
on these paths is authorized by `model-access-header` (scoped by header format), not by
path extraction.

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
| `/<ns>/<name>/...` | none | inference-access | Instance SAR (get) |
| `/<ns>/<name>/...` | valid model header | inference-access | Instance SAR (header inert; routes by path) |
| `/v1/chat/completions` | valid model header | model-access-header | Model SAR (header identity) |
| `/inference/v1/generate` | valid model header | model-access-header | Model SAR (header identity) |
| `/tokenize`, `/detokenize` | valid model header | model-access-header | Model SAR (single-segment, model in body) |
| `/v1/chat/completions` | none / invalid header | (no rule) | Authn-only (no header route match) |
| `/v1/files/...`, `/v1/batches/...` | any | (no rule) | Authn-only (batch bypass) |
| `/v1/models` | any | (no rule) | Authn-only (no model in body -> header invalid) |
| `/health`, `/metrics`, `/version`, `/ping` | none | (no rule) | Authn-only (depth guard / no model) |

## Model routing header

The header name (`x-gateway-model-name` by default) is configurable via
`modelBasedRoutingHeaderName` in the `inferenceservice-config` ConfigMap. The value is
lowercased (Authorino normalizes header keys) and validated as an HTTP token to prevent
CEL injection. The GatewayReconciler watches the ConfigMap's `ingress` key for changes.

Used by the `model-access-header` / `model-access-header-delegate` rules, which read the
routing namespace and model name from this header so authorization matches routing. The
gateway EnvoyFilter (`envoyfilter_ssl.yaml`) populates it by extracting the `model` field
from the JSON request body (`json_to_metadata` + `lua`); requests without a model in the
body get `unknown-model`, which fails the publisher-format check and stays authn-only.

**Filter ordering and authority.** The extractor and Lua injector are inserted
**before the Kuadrant auth WASM plugin** (Istio names it
`extensions.istio.io/wasmplugin/<gateway-namespace>.kuadrant-<gateway-name>`), not before
the router — the auth filter runs at the header phase and must see the derived header.
Because `json_to_metadata` writes its metadata during the request **data** phase while the
Lua and auth filters run at the **header** phase, the Lua calls `request_handle:body()` to
buffer the full body first, forcing the data through `json_to_metadata` (upstream in the
chain) before it reads `extracted_model`. The Lua uses `headers():replace(...)` (not
`add`): the body-derived value is authoritative and overwrites any client-supplied
`X-Gateway-Model-Name`, so a caller cannot spoof the routing/authorization identity and a
legitimate client that happens to send the header is not denied by a doubled (comma-joined)
value.

## Anti-spoofing (delegation)

When `x-maas-user` is present, both the base rule and its delegate counterpart fire (AND
semantics in Authorino - all matched rules must pass). The base rule checks the forwarded
user's access via `resolvedUser`. The delegate rule checks the caller's own identity for
`post-delegate`. Regular users lack delegate permission and get 403 - only trusted callers
like the batch processor SA can forward requests on behalf of others.

## Known limitations

- **Reserved namespaces `v1` / `inference`**: if namespace `v1` (or an `inference`
  namespace whose participant is named `v1`) exists with real LLMInferenceServices,
  per-participant paths like `/v1/<name>/...` or `/inference/v1/...` skip instance SAR
  (authn-only, because those prefixes are excluded from inference-access as model-API
  roots). Mitigated by a ValidatingAdmissionPolicy blocking reserved namespace names
  (`v1`, `v2`, `publishers`).
- **`/v1/` without BBR**: `/v1/` inference endpoints without the model routing header are
  authn-only. Safe because no HTTPRoute matches those paths without the header (gateway
  returns 404). The BBR follow-up will add `resolvedPath` normalization to authorize them.
- **Request-body buffering (large payloads)**: the gateway EnvoyFilter's Lua calls
  `request_handle:body()`, which buffers the entire request body before the request is
  forwarded and before auth runs. This is request-only — streamed *responses*
  (`stream: true` / SSE) are unaffected, since the filter defines no `envoy_on_response`
  and `json_to_metadata` has only `request_rules`. Normal inference clients POST a complete
  JSON body, so the latency cost is negligible; but a body larger than the listener's
  `per_connection_buffer_limit_bytes` (Envoy default 1 MiB) is rejected with 413. Raise that
  limit on the gateway if large multimodal or long-prompt requests are expected.
- **Future multi-segment model roots**: `is-model-api-root` enumerates the known roots
  (`/v1/`, `/inference/v1/`). A *new* multi-segment model root that does not start with one
  of these would be ns-shaped (>=2 segments) and fall to `inference-access` with garbage
  ns/name extraction — the same class as the `ns=v1` collision above. Mitigate by adding the
  new root to the `is-model-api-root` pattern (and the mirrored CEL negation in
  `inference-access`), and/or by extending the reserved-namespace ValidatingAdmissionPolicy.

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
