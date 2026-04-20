# Multi-Cluster Authentication with JWT Token Exchange

Extend `authpolicy_llm_isvc_userdefined.yaml` to support multi-cluster authentication
using signed JWT tokens for cross-cluster identity portability (implemented via
Kuadrant's wristband feature).

## Problem

Kubernetes tokens are cluster-scoped. A ServiceAccount token valid on cluster A cannot
authenticate on cluster B. In a multi-cluster topology where inference workloads are
distributed across clusters, users need a portable identity token to access models on
any cluster where they have been granted RBAC.

## Architecture: peer-to-peer

Every cluster is an equal peer — each can issue JWT tokens and consume tokens
issued by other peers. There is no hub/spoke distinction. The token proves the
user's **identity** (who they are), not their **authorization** (what they can do).
Each cluster enforces its own RBAC independently.

### Component diagram

```text
                                                 Client
                                                   │
                                           External LB / DNS
                                  ┌────────────────┴────────────────┐
                                  ▼                                 ▼
 ┌──────────────────────────────────────────────┐    ┌──────────────────────────────────────────────┐
 │  Cluster A                                   │    │  Cluster B                                   │
 │                                              │    │                                              │
 │  ┌────────────────────────────────────────┐  │    │  ┌────────────────────────────────────────┐  │
 │  │ Gateway                                │  │    │  │ Gateway                                │  │
 │  │                                        │  │    │  │                                        │  │
 │  │  Listener ──► Authorino                │  │    │  │  Listener ──► Authorino                │  │
 │  │                  │                     │  │    │  │                  │                     │  │
 │  │                  ├─ authn: k8s / JWT   │  │    │  │                  ├─ authn: k8s / JWT   │  │
 │  │                  ├─ authz: SAR (RBAC)  │  │    │  │                  ├─ authz: SAR (RBAC)  │  │
 │  └────────────────────────────────────────┘  │    │  └────────────────────────────────────────┘  │
 │                     │                        │    │                     │                        │
 │       ┌─────────────┴────────────┐           │    │       ┌─────────────┴────────────┐           │
 │       ▼                          ▼           │    │       ▼                          ▼           │
 │  ┌────────────────┐  ┌────────────────┐      │    │  ┌────────────────┐  ┌────────────────┐      │
 │  │model-serving-  │  │ Inference      │      │    │  │model-serving-  │  │ Inference      │      │
 │  │api (system ns) │  │ Backends       │      │    │  │api (system ns) │  │ Backends       │      │
 │  │ /api/v1/token  │  │ /<ns>/<model>/ │      │    │  │ /api/v1/token  │  │ /<ns>/<model>/ │      │
 │  └────────────────┘  └────────────────┘      │    │  └────────────────┘  └────────────────┘      │
 │                                              │    │                                              │
 │  ┌────────────────────────────────────────┐  │    │  ┌────────────────────────────────────────┐  │
 │  │ Authorino OIDC (:8083, TLS)            │  │    │  │ Authorino OIDC (:8083, TLS)            │  │
 │  │ JWKS: cluster-a-key + cluster-b-key    │  │    │  │ JWKS: cluster-b-key + cluster-a-key    │  │
 │  └────────────────────────────────────────┘  │    │  └────────────────────────────────────────┘  │
 │                                              │    │                                              │
 │  ┌────────────────────────────────────────┐  │    │  ┌────────────────────────────────────────┐  │
 │  │ Signing Key Secrets (kuadrant-system)  │  │    │  │ Signing Key Secrets (kuadrant-system)  │  │
 │  │ ├─ cluster-a-key (auto, signs)         │◄─┼────┼─►│ ├─ cluster-b-key (auto, signs)         │  │
 │  │ └─ cluster-b-key (peer, verify)        │  │    │  │ └─ cluster-a-key (peer, verify)        │  │
 │  └────────────────────────────────────────┘  │    │  └────────────────────────────────────────┘  │
 └──────────────────────────────────────────────┘    └──────────────────────────────────────────────┘
                                          ▲            ▲
                          Signing keys distributed via GitOps / ACM / HashiCorp Vault
```

### Token exchange flow

```text
Cluster A (or B) (issuing)                        Cluster B (or A) (consuming)

User ──► Gateway /api/v1/token                    Client ──► Gateway ──► Authorino ──► Inference Backend
           │                                                │
           ├─ 1. kubernetesTokenReview (authn)              ├─ 1. jwt verification (authn)
           ├─ 2. No authorization (token path)              ├─ 2. SubjectAccessReview (local RBAC)
           └─ 3. Issue signed JWT (response)                └─ 3. Set flow control headers (response)
                    │                                                  ▲
                    │              signed JWT                          │
                    └──────────────────────────────────────────────────┘
```

**Flow**:

1. User authenticates on cluster A with a local Kubernetes token via `GET /api/v1/token`
2. Cluster A skips authorization for the token path (authentication only)
3. Cluster A issues a signed JWT containing the user's identity (username, groups)
4. Client extracts the JWT from the response
5. Client sends the JWT to cluster B's inference endpoint
6. Cluster B verifies the JWT signature via JWKS
7. Cluster B extracts the username and groups from the JWT
8. Cluster B performs a **local** SubjectAccessReview — the user/SA must exist and have
   RBAC on cluster B

**Key property**: the token carries identity, not authorization. A stolen or leaked
token is useless on a cluster where the user has no RBAC. Each cluster is responsible
for its own access control.

### Assumptions and limitations

- **Identity consistency across clusters**: The JWT encodes the user's Kubernetes
  identity (e.g., `system:serviceaccount:ns:name`). For RBAC to succeed on a peer
  cluster, the same user or ServiceAccount (namespace + name) must exist on that cluster.
  The token does not create identities — it only carries them.
- **Kuadrant and Authorino installed on every cluster**: Each peer cluster must have
  Kuadrant with Authorino deployed and configured.
- **Token carries identity only**: The JWT does not encode authorization decisions.
  Each cluster enforces its own RBAC via SubjectAccessReview. A valid token from a
  peer is insufficient without local RoleBindings.
- **Zero-config single cluster**: The controller auto-generates a local signing key
  per Gateway. Token exchange via `/api/v1/token` works without any annotation or
  manual key creation. Multi-cluster requires only adding peer key names to the
  `security.opendatahub.io/token-exchange-signing-keys` annotation.
- **Signing key distribution**: Authorino's `signingKeyRefs` requires private keys —
  it cannot import public-key-only Secrets (rejects with `"invalid signing key
  algorithm"`). The local signing key is auto-generated; peer signing key pairs
  (private key Secrets) must be distributed to every cluster that needs to verify
  their tokens. Authorino publishes all `signingKeyRefs` in a single JWKS endpoint,
  so a single `jwt` authentication entry is sufficient to verify tokens from all
  peers whose keys are configured.

## Workflow overview

### Platform admin: multi-cluster setup

```text
Admin
  │
  ├─ 1. On each cluster: export the auto-generated signing key or generate a new one
  │     ├─ Export: oc get secret <gw>-wristband-signing-key -n kuadrant-system -o yaml
  │     └─ Generate: openssl ecparam -name prime256v1 -genkey -noout | oc create secret
  │        generic peer-key -n kuadrant-system --from-file=key.pem=/dev/stdin
  │
  ├─ 2. Distribute each cluster's signing key to every peer cluster
  │     └─ Via GitOps, RH Advanced Cluster Management (ACM), or manual `oc apply` (on each cluster)
  │
  ├─ 3. Annotate the Gateway on each cluster with peer key names
  │     └─ security.opendatahub.io/token-exchange-signing-keys: "peer-a-key,peer-b-key"
  │
  ├─ 4. Create RoleBindings for remote identities on each cluster
  │     └─ Grant remote SAs/users access to local LLMInferenceServices
  │
  └─ 5. (Optional) Configure token duration
        └─ security.opendatahub.io/token-exchange-duration: "31536000"

Controller (on annotation change)
  │
  └─ Update AuthPolicy signingKeyRefs: [auto-generated, peer-a-key, peer-b-key]
     └─ Authorino publishes all keys in a single JWKS endpoint
```

### Application developer: token exchange flow

```text
Developer / CI pipeline
  │
  ├─ 1. Authenticate on home cluster
  │     GET /api/v1/token
  │     Authorization: Bearer <k8s-token>
  │     └─ Response: {"token": "<jwt>"}
  │
  ├─ 2. Use JWT on any peer cluster
  │     GET /<ns>/<model>/v1/chat/completions
  │     Authorization: Bearer <jwt>
  │     │
  │     ├─ Authorino verifies JWT signature (JWKS)
  │     ├─ Extracts username/groups from claims
  │     ├─ SubjectAccessReview against local RBAC
  │     └─ 200 (authorized) or 403 (no local RBAC)
  │
  └─ 3. Same JWT works on any peer where user has RoleBindings
```

### Incident responder: token revocation

```text
Responder
  │
  ├─ Scope: single identity
  │  ├─ Remove RoleBindings on affected clusters → 403
  │  └─ (Optional) Add patternMatching deny rule → 401
  │
  ├─ Scope: single token
  │  └─ Add patternMatching deny rule on sub claim → 401
  │
  ├─ Scope: signing key compromised
  │  ├─ Rotate the signing key Secret
  │  ├─ Redistribute JWKS to peers
  │  └─ All existing tokens from that key → 401
  │
  └─ Scope: cluster compromised
     ├─ Remove peer's signing key Secret from all clusters
     ├─ Update Gateway annotation to exclude peer
     └─ All tokens from that peer → 401
```

## Backward compatibility

All changes are additive. The existing `kubernetesTokenReview` + `SubjectAccessReview`
flow remains the primary authentication/authorization path for local users.

On peer clusters, the `jwt` authentication method is configured alongside
`kubernetesTokenReview`. Authorino evaluates all authentication configs — at least one
must succeed. Local Kubernetes tokens continue to work unchanged.

## Token endpoint: `/api/v1/token`

A dedicated gateway path for wristband token exchange. The client authenticates with a
local Kubernetes token and receives a wristband JWT in the response.

### Routing

The `GatewayReconciler` (`gateway_controller.go`) automatically creates an HTTPRoute
for `/api/v1/token` for every managed Gateway. The HTTPRoute routes to the
`model-serving-api` server (`server/`, deployed via `config/server/`).

The server needs a `/api/v1/token` handler that returns the wristband token from the
`X-Wristband-Token` response header (injected by Authorino into the upstream request)
in the response body:

```go
// server/handlers/token.go
func Token(w http.ResponseWriter, r *http.Request) {
    token := r.Header.Get("X-Wristband-Token")
    w.Header().Set("Content-Type", "application/json")
    w.Header().Set("Cache-Control", "no-store")
    if token == "" {
        w.WriteHeader(http.StatusBadGateway)
        json.NewEncoder(w).Encode(map[string]string{"error": "missing wristband token"})
        return
    }
    json.NewEncoder(w).Encode(map[string]string{"token": token})
}
```

Note: `X-Wristband-Token` here is the **internal** response header that Authorino injects
into the upstream request (configured in `response.success.headers`). It is not the
client-facing header. The client sends and receives tokens via `Authorization: Bearer`.

Register in `server/server.go`:
```go
mux.HandleFunc("/api/v1/token", handlers.Token)
```

The controller creates the following HTTPRoute (owned by the Gateway for garbage collection):

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: <gateway-name>-token-endpoint
  namespace: <server-namespace>   # opendatahub or redhat-ods-applications
  labels:
    app.kubernetes.io/managed-by: odh-model-controller
spec:
  parentRefs:
    - name: openshift-ai-inference
      namespace: openshift-ingress
  rules:
    - matches:
        - path:
            type: Exact
            value: /api/v1/token
      backendRefs:
        - name: model-serving-api
          port: 443
```

The reconciler follows the same create/update/delete pattern as the existing AuthPolicy
and EnvoyFilter reconciliation.

The `opendatahub.io/managed: "false"` label/annotation on the Gateway disables all
controller-managed resources (AuthPolicy, EnvoyFilter, signing key Secret, and the
token endpoint HTTPRoute).

To disable only the token exchange automation while keeping other controller-managed
resources, set (on the Gateway):

```yaml
annotations:
  security.opendatahub.io/token-exchange: "false"
```

When set to `"false"`, the controller skips signing key generation, HTTPRoute creation,
and wristband configuration in the AuthPolicy. This allows operators to manage token
exchange manually or disable it entirely without affecting other controller behavior.

### Cross-namespace routing

The `model-serving-api` runs in the system namespace (`opendatahub` or
`redhat-ods-applications`), while the Gateway is in `openshift-ingress`. The HTTPRoute's
`parentRefs` is a cross-namespace reference.

Gateway API handles this via the Gateway listener's `allowedRoutes` setting:

- **`from: All`** (current default): Any namespace can attach HTTPRoutes — no additional
  resources needed.
- **`from: Same`**: Only HTTPRoutes in the gateway's own namespace are accepted. The
  system namespace would be rejected.
- **`from: Selector`**: Only namespaces matching a label selector are accepted. The system
  namespace must carry the required labels.

If the listener restricts namespaces (`Same` or `Selector`), either:

1. **Update the listener** to include the system namespace (preferred — the controller
   already manages the Gateway spec), or
2. **Deploy the HTTPRoute in the gateway namespace** and use a `backendRef` with a
   cross-namespace Service reference. This requires a `ReferenceGrant` in the server's
   namespace:

```yaml
apiVersion: gateway.networking.k8s.io/v1beta1
kind: ReferenceGrant
metadata:
  name: allow-gateway-to-model-serving-api
  namespace: <server-namespace>   # opendatahub or redhat-ods-applications
spec:
  from:
    - group: gateway.networking.k8s.io
      kind: HTTPRoute
      namespace: openshift-ingress
  to:
    - group: ""
      kind: Service
      name: model-serving-api
```

Note: `backendRefs` cross-namespace references are **not** controlled by `allowedRoutes`
— they require a `ReferenceGrant` in the target Service's namespace regardless of the
listener config.

Since the controller already manages the Gateway and its listeners (see
`gateway_controller.go`), option 1 is the natural choice — ensure the system namespace
is allowed in `allowedRoutes`.

### Authorization exclusion

The `/api/v1/token` path must be excluded from authorization rules (like batch paths).
Authentication is sufficient — if the user has a valid Kubernetes token, they can
request a wristband:

```yaml
authorization:
  inference-access:
    when:
      - predicate: >-
          !(request.path == '/api/v1/token' ||
            request.path == '/v1/files' || request.path.startsWith('/v1/files/') ||
            request.path == '/v1/batches' || request.path.startsWith('/v1/batches/'))
    # ... existing SAR config unchanged ...
  inference-access-delegate:
    when:
      - predicate: >-
          !(request.path == '/api/v1/token' ||
            request.path == '/v1/files' || request.path.startsWith('/v1/files/') ||
            request.path == '/v1/batches' || request.path.startsWith('/v1/batches/'))
      - predicate: "'x-maas-user' in request.headers"
    # ... existing SAR config unchanged ...
```

### Wristband response (scoped to `/api/v1/token`)

The wristband is only issued for the token endpoint — not on every request:

```yaml
response:
  success:
    headers:
      # ... existing headers (fairness, objective, maas-user, maas-groups) unchanged ...

      x-wristband-token:
        when:
          - predicate: "request.path == '/api/v1/token'"
        wristband:
          issuer: "https://authorino-authorino-oidc.kuadrant-system.svc.cluster.local:8083/<AUTHCONFIG_NS>/<AUTHCONFIG_NAME>/x-wristband-token"
          tokenDuration: 31536000   # configurable via Gateway annotation, default 1 year
          signingKeyRefs:
            - name: <gateway-name>-wristband-signing-key   # auto-generated
              algorithm: ES256
            # ... peer keys from annotation appended here ...
          customClaims:
            username:
              expression: auth.identity.user.username
            groups:
              expression: auth.identity.user.groups
        priority: 0
```

**Claims**: The wristband encodes identity only:

| Claim      | Source                        | Purpose                               |
|------------|-------------------------------|---------------------------------------|
| `username` | `auth.identity.user.username` | User or SA identity                   |
| `groups`   | `auth.identity.user.groups`   | Group memberships (stored as array)   |
| `iss`      | (standard)                    | Issuing cluster's Authorino endpoint  |
| `sub`      | (standard, auto-generated)    | Unique per token — usable as token ID |
| `exp`      | (standard)                    | Token expiration                      |
| `iat`      | (standard)                    | Token issued-at time                  |

Note: Authorino does not include `jti` by default, but `sub` is unique per issuance
(different hash each time, even for the same user). It can be used to identify specific
tokens in audit logs.

No authorization-scoped claims (namespace, model) — the wristband is not bound to a
specific resource. The same wristband works for any model on any peer cluster where the
user has RBAC.

## Signing keys

### Auto-generated local signing key

The controller automatically generates an ES256 signing key for each Gateway and creates
a Secret named `<gateway-name>-wristband-signing-key` in the AuthConfig namespace
(typically `kuadrant-system`). This key is always the **first entry** in `signingKeyRefs`
— it signs new wristband tokens. No user configuration is needed for single-cluster
token exchange.

The controller generates the key on first reconciliation if the Secret does not exist,
and never rotates it automatically (see "Key rotation" for manual rotation). The Secret
will need to be deleted by the controller when not needed anymore.

```yaml
# Auto-generated by the controller — no user action required
apiVersion: v1
kind: Secret
metadata:
  name: <gateway-name>-wristband-signing-key
  namespace: kuadrant-system
type: Opaque
data:
  key.pem: <auto-generated ES256 private key>
```

### Adding peer signing keys (multi-cluster)

For multi-cluster scenarios, operators add peer signing keys via the
`security.opendatahub.io/token-exchange-signing-keys` Gateway annotation. This is a
comma-separated list of Secret names that are **appended after** the auto-generated
local key in `signingKeyRefs`:

```yaml
annotations:
  # Peer keys for verification — local key is always prepended automatically
  security.opendatahub.io/token-exchange-signing-keys: "peer-cluster-a-key,peer-cluster-b-key"
```

The resulting `signingKeyRefs` order is:
1. `<gateway-name>-wristband-signing-key` (auto-generated, signs new tokens)
2. `peer-cluster-a-key` (from annotation, verification only)
3. `peer-cluster-b-key` (from annotation, verification only)

All keys are published in the JWKS. The first key signs; Authorino matches the JWT's
`kid` against all keys for verification.

Each Secret contains an ES256 private key (Authorino requires private keys in
`signingKeyRefs` — public-key-only Secrets are rejected). Each peer cluster's signing
key pair must be distributed to every cluster that needs to verify its tokens.

**Namespace**: All Secrets must be in the namespace where Kuadrant creates AuthConfigs —
this is the Kuadrant system namespace (typically `kuadrant-system`). Authorino's
`signingKeyRefs` resolves Secrets by name only (no namespace field), so it looks in the
AuthConfig's own namespace. Verify with `oc get authconfigs.authorino.kuadrant.io -A`.

Each peer Secret must have a `key.pem` entry:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: peer-cluster-a-key
  namespace: kuadrant-system
type: Opaque
stringData:
  key.pem: |
    -----BEGIN EC PRIVATE KEY----- # notsecret
    <ES256 private key>
    -----END EC PRIVATE KEY-----
```

Generate a key pair:

```bash
openssl ecparam -name prime256v1 -genkey -noout -out key.pem
openssl ec -in key.pem -pubout -out pub.pem
```

## Wristband issuer URL

The `issuer` field follows the format:

```
<scheme>://<host>:<port>/<authconfig-namespace>/<authconfig-name>/<wristband-config-name>
```

- `<host>:<port>`: Authorino OIDC service
  (`authorino-authorino-oidc.kuadrant-system.svc.cluster.local:8083`)
- `<authconfig-namespace>/<authconfig-name>`: The AuthConfig for the **token endpoint
  HTTPRoute rule**. Kuadrant creates separate AuthConfigs per topology path (one per
  HTTPRoute rule). The wristband OIDC/JWKS endpoint is served under the token endpoint's
  AuthConfig, not the inference endpoints'. AuthConfig names are SHA-256 hashes.
- `<wristband-config-name>`: The key in `response.success.headers` (`x-wristband-token`)

The issuer URL serves dual purpose:
1. It becomes the `iss` claim in the wristband JWT
2. Authorino serves OIDC discovery at `<issuer>/.well-known/openid-configuration` and JWKS
   at `<issuer>/.well-known/openid-connect/certs`

### OIDC server TLS

The Authorino OIDC server must have TLS enabled so that the `issuer` and `jwksUrl` use
`https://`. TLS is configured on the Authorino CR (`operator.authorino.kuadrant.io/v1beta1`):

```yaml
# kuadrant-system/authorino
spec:
  oidcServer:
    tls:
      enabled: true
      certSecretRef:
        name: authorino-oidc-tls   # Secret with tls.crt and tls.key
```

The controller should verify that `spec.oidcServer.tls.enabled` is `true` on the
Authorino CR. If TLS is disabled, the wristband issuer URL would use `http://`, which
means the `iss` claim in issued tokens would contain an `http://` URL. Consumers
configured with an `https://` issuer URL would reject these tokens due to issuer
mismatch.

### Computing the AuthConfig name

Kuadrant generates AuthConfig names as the SHA-256 of a **pathID** — a deterministic
string built from the full Gateway API topology path:

```
<gatewayClassName>|<gwNs>/<gwName>|<gwNs>/<gwName>#<listenerName>|<routeNs>/<routeName>|<routeNs>/<routeName>#<ruleSectionName>
```

For example, with GatewayClass `openshift-default`, Gateway `openshift-ingress/openshift-ai-inference`,
listener `http`, and HTTPRoute `echo-service/token-endpoint` rule `rule-1`:

```
openshift-default|openshift-ingress/openshift-ai-inference|openshift-ingress/openshift-ai-inference#http|echo-service/token-endpoint|echo-service/token-endpoint#rule-1
```

SHA-256 of this string → AuthConfig name.

**The controller can predict the AuthConfig name** at AuthPolicy creation time because
all inputs to the hash (GatewayClass, Gateway, Listener, HTTPRoute, Rule) are known.

Source: `kuadrant-operator` — `api/v1/merge_strategies.go:PathID()` and
`internal/controller/auth_workflow_helpers.go:AuthConfigNameForPath()`.

**Reusing Kuadrant utilities**: `PathID` is exported and importable from
`github.com/kuadrant/kuadrant-operator/api/v1` (already a dependency). It takes
`[]machinery.Targetable` from `github.com/kuadrant/policy-machinery` (already a transitive
dependency). `AuthConfigNameForPath` is in `internal/controller/` (not importable), but
it's trivial — SHA-256 of the pathID string:

```go
import (
    "crypto/sha256"
    "encoding/hex"

    kuadrantv1 "github.com/kuadrant/kuadrant-operator/api/v1"
    "github.com/kuadrant/policy-machinery/machinery"
)

// Build the topology path from known objects
path := []machinery.Targetable{gatewayClass, gateway, listener, httpRoute, httpRouteRule}
pathID := kuadrantv1.PathID(path)

// Compute the AuthConfig name (same as kuadrant's internal AuthConfigNameForPath)
hash := sha256.Sum256([]byte(pathID))
authConfigName := hex.EncodeToString(hash[:])
```

The `machinery.Targetable` objects can be constructed from the Gateway API objects the
controller already reconciles (Gateway, HTTPRoute, etc.) using `policy-machinery`'s
type adapters.

### Rule section name: `rule-<index>`

The rule section name in the pathID (e.g., `rule-1`) is auto-generated by
`policy-machinery` as `fmt.Sprintf("rule-%d", i+1)` — it's the 1-based index of the
rule in the HTTPRoute's `spec.rules` array. The HTTPRouteRule `name` field from
Gateway API is **not used yet** (tracked upstream:
`TODO: Use the 'name' field of the HTTPRouteRule once it's implemented`).

Since the controller auto-creates the `/api/v1/token` HTTPRoute with a single rule,
the rule index is always `rule-1` and the hash is stable. Other HTTPRoutes attached to
the same Gateway do not affect it — each HTTPRoute produces its own AuthConfig(s).

**Important**: The token endpoint HTTPRoute **must not** use named rules
(`spec.rules[].name`). The controller computes the AuthConfig hash using `rule-1`
(index-based). If `policy-machinery` adopts the `name` field in the future, using a
named rule would change the pathID (e.g., `rule-1` → `token-exchange`), producing a
different AuthConfig hash. This would invalidate the wristband issuer URL and break
all in-flight tokens. If Kuadrant changes the default behavior from index-based to
name-based, the controller's hash computation must be updated to match, therefore
we won't set names on the HTTPRoute rule for the token endpoint.

### Discovering the AuthConfig name (alternative)

If the controller doesn't compute the hash, discover it after deployment:

```bash
oc get authconfigs.authorino.kuadrant.io -A
# Example output:
# NAMESPACE         NAME                                                               READY
# kuadrant-system   41e136873d122b95c4212edfec0070d5e94fa1954a8dbbf54767f3b85cfde37a   true
```

The issuer URL would be:
```
https://authorino-authorino-oidc.kuadrant-system.svc.cluster.local:8083/kuadrant-system/41e136873d122b95c4212edfec0070d5e94fa1954a8dbbf54767f3b85cfde37a/x-wristband-token
```

## Peer cluster: consuming wristband tokens

### JWT authentication

Each peer cluster's AuthPolicy adds a `jwt` authentication method that verifies wristband
tokens from other peers:

```yaml
authentication:
  # Local cluster auth (unchanged)
  kubernetes-user:
    kubernetesTokenReview:
      audiences:
        - https://kubernetes.default.svc
    overrides:
      fairness:
        value: "https://kubernetes.default.svc"
      objective:
        expression: >-
          auth.identity.user.username.startsWith('system:serviceaccount:')
            ? auth.identity.user.username.split(':')[2]
            : 'authenticated'

  # JWT auth from peer clusters (disabled on /api/v1/token to prevent token escalation)
  wristband-user:
    when:
      - predicate: "request.path != '/api/v1/token'"
    jwt:
      jwksUrl: "https://authorino-authorino-oidc.kuadrant-system.svc.cluster.local:8083/<AUTHCONFIG_NS>/<AUTHCONFIG_NAME>/x-wristband-token/.well-known/openid-connect/certs"
    overrides:
      user:
        expression: "{'username': auth.identity.username, 'groups': auth.identity.groups}"
      fairness:
        expression: auth.identity.iss
      objective:
        expression: >-
          auth.identity.username.startsWith('system:serviceaccount:')
            ? auth.identity.username.split(':')[2]
            : 'authenticated'
    priority: 1
```

**Credential extraction**: Both `kubernetes-user` and `wristband-user` use the default
`Authorization: Bearer` header. Authorino evaluates all authentication methods — at
least one must succeed. A Kubernetes token passes `kubernetes-user`; a JWT passes
`wristband-user`. The client uses the same header regardless of token type.

**Identity normalization via overrides**: The `kubernetesTokenReview` produces an identity
with nested structure (`auth.identity.user.username`). The JWT produces a flat structure
(`auth.identity.username`). The `user` override on `wristband-user` reshapes the flat JWT
claims to match the nested structure so that authorization rules work without modification:

```
kubernetesTokenReview identity:        Wristband JWT identity (after override):
  auth.identity.user.username    →      auth.identity.user.username
  auth.identity.user.groups      →      auth.identity.user.groups
```

### Authorization: local RBAC enforcement

The existing `inference-access` SubjectAccessReview rule works unchanged for both
authentication methods. After identity normalization, `auth.identity.user.username`
and `auth.identity.user.groups` are available regardless of whether the user authenticated
via Kubernetes token or wristband:

```yaml
authorization:
  inference-access:
    when:
      - predicate: >-
          !(request.path == '/api/v1/token' ||
            request.path == '/v1/files' || request.path.startsWith('/v1/files/') ||
            request.path == '/v1/batches' || request.path.startsWith('/v1/batches/'))
    kubernetesSubjectAccessReview:
      user:
        expression: >-
          'x-maas-user' in request.headers
            ? request.headers['x-maas-user']
            : auth.identity.user.username
      authorizationGroups:
        expression: >-
          'x-maas-user' in request.headers
            ? ('x-maas-groups' in request.headers
                ? request.headers['x-maas-groups'].split(',')
                : [])
            : auth.identity.user.groups
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

The SAR checks the **local** cluster's RBAC. The user/SA identified by the wristband must
have the appropriate RoleBinding on the consuming cluster. If they don't, the request is
rejected with 403 — regardless of what permissions they have on the issuing cluster.

### Response headers

The `fairness` and `objective` overrides on `wristband-user` ensure that
`auth.identity.fairness` and `auth.identity.objective` are populated correctly for
both auth methods. The existing response header expressions work unchanged:

```yaml
response:
  success:
    headers:
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
```

## JWKS distribution

Authorino natively serves JWKS for each wristband config at:

```
https://authorino-authorino-oidc.<kuadrant-ns>.svc.cluster.local:8083/<authconfig-ns>/<authconfig-name>/<wristband-name>/.well-known/openid-connect/certs
```

The consumer AuthPolicy's `jwt.jwksUrl` points directly to this endpoint. Since the
AuthConfig name is deterministic (see "Computing the AuthConfig name" above), the
controller can compute the full JWKS URL at AuthPolicy creation time.

### Same-cluster wristband verification

When a cluster issues and consumes its own wristbands (e.g., self-testing or single-cluster
setups), the `jwksUrl` points to the local Authorino OIDC service. No additional
infrastructure is needed — Authorino serves the JWKS automatically when a wristband
`signingKeyRefs` is configured.

### Cross-cluster JWKS distribution

For multi-cluster scenarios, the JWKS must be pre-distributed to each peer cluster.
This is an operational concern — operators export the JWKS from the issuing cluster's
Authorino OIDC endpoint and make it available on each peer (e.g. via GitOps or RH ACM).

```bash
# On the issuing cluster: export the JWKS
JWKS=$(curl -s https://authorino-authorino-oidc.kuadrant-system.svc.cluster.local:8083/<authconfig-ns>/<authconfig-name>/x-wristband-token/.well-known/openid-connect/certs)

# Transfer $JWKS to each peer cluster and make it available at a URL
# that Authorino on the peer can reach via jwt.jwksUrl
```

The exact mechanism for serving the imported JWKS on the peer cluster (ConfigMap-backed
endpoint, shared object storage, external OIDC proxy, etc.) is outside the scope of
this design. The only requirement is that the `jwt.jwksUrl` in the peer's AuthPolicy
resolves to a valid JWKS JSON endpoint reachable from Authorino.

### Multiple peers

Since each peer's signing key (private key Secret) is distributed to every cluster,
all keys are configured as `signingKeyRefs` on the wristband config. Authorino publishes
all of them in a single JWKS endpoint. The consumer side needs only **one `jwt`
authentication entry** — its `jwksUrl` points to the local Authorino OIDC JWKS endpoint,
which already contains all peer keys:

```yaml
authentication:
  kubernetes-user:
    # ... local auth unchanged ...
  wristband-user:
    when:
      - predicate: "request.path != '/api/v1/token'"
    jwt:
      jwksUrl: "https://authorino-authorino-oidc.kuadrant-system.svc.cluster.local:8083/<AUTHCONFIG_NS>/<AUTHCONFIG_NAME>/x-wristband-token/.well-known/openid-connect/certs"
    overrides:
      user:
        expression: "{'username': auth.identity.username, 'groups': auth.identity.groups}"
      fairness:
        expression: auth.identity.iss
      objective:
        expression: "auth.identity.username.startsWith('system:serviceaccount:') ? auth.identity.username.split(':')[2] : 'authenticated'"
    priority: 1
```

Authorino matches the JWT's `kid` against the JWKS keys. The first key whose `kid`
matches verifies the signature. Revoking a peer means removing its signing key Secret
from `signingKeyRefs` on all clusters.

### Key rotation

1. Generate a new ES256 key pair on the peer cluster
2. Update `Secret/wristband-signing-key` on the peer — Authorino picks up the new key
3. The local JWKS endpoint updates automatically (Authorino watches the Secret)
4. For cross-cluster peers: export the updated JWKS and redistribute to each peer
5. Remove the old key from the peer after all in-flight wristbands expire

## Required Kubernetes resources (per cluster)

| Resource                            | Namespace                                | Purpose                                      | Created by        |
|-------------------------------------|------------------------------------------|----------------------------------------------|-------------------|
| `Secret/<gw>-wristband-signing-key` | AuthConfig namespace (`kuadrant-system`) | ES256 private key for signing wristband JWTs | Controller (auto) |
| HTTPRoute for `/api/v1/token`       | Server namespace                         | Routes token exchange requests               | Controller (auto) |
| Peer signing key Secrets            | AuthConfig namespace (`kuadrant-system`) | Peer keys for multi-cluster verification     | Operator (manual) |
| RoleBindings for remote users       | Model namespaces                         | RBAC for users from peer clusters            | Operator (manual) |

### RBAC for remote users

Users/SAs identified by wristbands must have RBAC on the consuming cluster. Since the
same SA name/namespace is used across clusters, the same RoleBinding works:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: remote-user-inference-access
  namespace: model-namespace
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: llminferenceservices-reader
subjects:
  - kind: ServiceAccount
    name: inference-client
    namespace: client-namespace
```

This grants `system:serviceaccount:client-namespace:inference-client` from **any** peer
cluster access to models in `model-namespace` on this cluster.

### Key rotation

1. Generate a new ES256 key pair
2. Update `Secret/wristband-signing-key` with the new `key.pem`
3. Authorino picks up the new key automatically (watches the Secret)
4. The JWKS endpoint updates to include the new key (identified by `kid`)
5. **Option A**: Peers auto-refresh JWKS — no action needed
6. **Option B**: Export updated JWKS and redistribute to all peers
7. Remove the old key after all in-flight wristbands expire

## Security considerations

- **Identity only, not authorization**: The wristband proves who the user is, not what
  they can do. Each cluster enforces its own RBAC via SubjectAccessReview. A stolen
  wristband is useless on a cluster where the user has no RoleBindings.
- **Asymmetric signing**: Use ES256 (ECDSA P-256). Peer clusters only need the public
  key (JWKS), never the private key. Compromising one cluster's JWKS does not compromise
  another cluster's signing key.
- **Token duration**: Default is 1 year (31536000s), configurable via
  `security.opendatahub.io/token-exchange-duration`. Long-lived tokens are acceptable
  because the wristband carries identity only — each cluster enforces its own RBAC.
  Revoking access is done by removing RoleBindings, not by expiring tokens.
- **No token re-minting**: The `wristband-user` authentication is disabled on
  `/api/v1/token` via a `when` predicate. This prevents using a wristband to mint a
  new wristband (token escalation). Only `kubernetesTokenReview` is accepted on the
  token endpoint.
- **SA name consistency**: The same SA name/namespace must exist across clusters for
  RBAC to work. This is a deployment constraint, not an Authorino constraint.
- **Issuer validation**: With `jwksUrl`, Authorino verifies the JWT signature but does
  not validate the `iss` claim. With `issuerUrl` (alternative), Authorino also validates
  the `iss` claim — but this does not mitigate a compromised peer, since the attacker
  controls the signing key and can set `iss` to any value.
- **Compromised peer trust boundary**: A compromised peer cluster has access to its
  signing key (private key Secret) and can mint wristband JWTs with arbitrary `username`
  and `groups` claims. The consuming cluster verifies the signature (valid, since the
  peer's key is in the JWKS) and performs a SubjectAccessReview using the asserted
  identity. If that identity has RoleBindings on the consuming cluster, access is
  granted. This is inherent to any federated trust model with shared signing keys —
  trusting a peer's key means trusting any token it signs. Neither `issuerUrl` validation
  nor additional JWT claims mitigate this, since the attacker controls the signing key
  and can set any claim. The mitigation is operational: rapid signing key revocation
  (see "Scenario 3: Peer cluster compromised") and the "Hardening: one JWKS per cluster"
  recommendation to enable surgical revocation per peer.

## Token revocation (incident response)

JWTs are stateless — there is no server-side session to invalidate. Revocation depends
on the severity of the incident. The table below summarizes the options, followed by
detailed procedures.

### Inspecting a token

Every wristband JWT contains a `kid` (key ID) in the header matching the signing key
Secret name, and identity claims in the payload. Decode a token to identify its origin
and the identity it carries:

```bash
# Decode JWT header (kid identifies the signing key / originating cluster)
echo "<token>" | cut -d. -f1 | tr '_-' '/+' | base64 -d | jq .
# {
#   "alg": "ES256",
#   "kid": "local-signing-key",    <-- Secret name that signed this token
#   "typ": "JWT"
# }

# Decode JWT payload (identity and expiration)
echo "<token>" | cut -d. -f2 | tr '_-' '/+' | base64 -d | jq .
# {
#   "username": "system:serviceaccount:echo-service:test-user",
#   "groups": ["system:serviceaccounts", "system:serviceaccounts:echo-service", "system:authenticated"],
#   "iss": "https://authorino-authorino-oidc...",
#   "exp": 1776674508,
#   "iat": 1776674208,
#   "sub": "f088720b..."
# }
```

The `kid` tells you which key id signed the token (each cluster's local signing key has
a unique name). Use this to scope revocation to the right key or cluster.

| Scenario                                 | Action                      | Scope                        | Effect                                  | Downtime                   |
|------------------------------------------|-----------------------------|------------------------------|-----------------------------------------|----------------------------|
| Single identity compromised              | Remove RoleBindings         | Per-identity, per-cluster    | 403 on authz, token still authenticates | None                       |
| Single identity compromised (full block) | Add deny rule to AuthPolicy | Per-identity, all clusters   | 403 before SAR                          | None                       |
| Signing key compromised                  | Rotate signing key Secret   | All tokens from that cluster | 401 for all existing tokens             | Token re-issuance required |
| Cluster compromised                      | Remove peer signing key     | Per-cluster                  | 401 for all tokens from that peer       | Token re-issuance required |

### Scenario 1: Single identity compromised

**Immediate response** — remove RoleBindings for the compromised identity on all
clusters where it has access:

```bash
# On each affected cluster: remove the RoleBinding
oc delete rolebinding <binding-name> -n <model-namespace>
```

This blocks authorization (403) immediately. The wristband still passes authentication
(the JWT is valid), but the SubjectAccessReview fails because the identity has no RBAC.

**Full block** — if the token must be rejected at the authentication layer (401 instead
of 403), add a `patternMatching` deny rule to the AuthPolicy. This uses Authorino's
CEL-based pattern matching to reject specific identities before authorization:

```yaml
authorization:
  deny-compromised-identity:
    patternMatching:
      patterns:
        - predicate: "auth.identity.user.username != 'system:serviceaccount:ns:compromised-sa'"
    priority: 0   # evaluate before inference-access
```

This denies the specific identity across all routes on the Gateway. Remove the rule
after the incident is resolved.

**Revoking a specific token** — if only a single token is compromised (not the entire
identity), deny it by its `sub` claim. Since `sub` is unique per issuance (see claims
table), it acts as a de facto token ID:

```yaml
authorization:
  deny-compromised-token:
    patternMatching:
      patterns:
        - predicate: "auth.identity.sub != 'f088720b...'"
    priority: 0
```

Identify the `sub` value by decoding the compromised token (see "Inspecting a token").
This is more surgical than blocking the entire identity — other tokens issued to the
same user remain valid. Note: Authorino does not include `jti` by default; `sub` serves
this purpose.

Both approaches (identity-level and token-level deny rules) can be automated via Gateway
annotations in the future (e.g., `security.opendatahub.io/denied-identities`,
`security.opendatahub.io/denied-tokens`) if frequent use is expected, but for incident
response a manual AuthPolicy patch is sufficient.

### Scenario 2: Signing key compromised

If the private signing key is compromised, all tokens signed by that key must be
considered compromised. Rotate the key immediately:

```bash
# 1. Generate a new key
openssl ecparam -name prime256v1 -genkey -noout -out /tmp/new-key.pem

# 2. Replace the signing key Secret
oc create secret generic wristband-signing-key \
  -n kuadrant-system \
  --from-file=key.pem=/tmp/new-key.pem \
  --dry-run=client -o yaml | oc apply -f -

# 3. Authorino picks up the new key automatically (watches the Secret)
# All existing tokens fail signature verification (401) immediately

# 4. Export the new JWKS and distribute to all peer clusters
NEW_JWKS=$(curl -s https://authorino-authorino-oidc.kuadrant-system.svc.cluster.local:8083/<authconfig-ns>/<authconfig-name>/x-wristband-token/.well-known/openid-connect/certs)

# 5. On each peer: update the JWKS
# (mechanism depends on deployment — Secret, ConfigMap, etc.)

# 6. Users must re-authenticate to obtain new tokens
```

**Impact**: all existing wristband tokens from this cluster are invalidated. Users
must re-authenticate via `/api/v1/token` to get new tokens signed with the new key.

### Scenario 3: Peer cluster compromised

If a peer cluster is fully compromised, remove trust by removing its signing key
from all other clusters:

```bash
# 1. Remove the compromised peer's signing key Secret from each cluster
oc delete secret <peer-signing-key-name> -n kuadrant-system

# 2. Remove the corresponding signingKeyRefs entry from the AuthPolicy on each cluster
# (or update the Gateway annotation to exclude the peer's key name)

# 3. Authorino updates the JWKS endpoint — the peer's public key is no longer published
# All existing tokens signed by that key fail verification (401)
```

No tokens from healthy peers are affected — their signing keys remain in
`signingKeyRefs` and their `kid` entries are still in the JWKS.

### Hardening: one JWKS per cluster

Maintain a separate JWKS artifact (Secret, file, etc.) for each trusted cluster —
including the local cluster. This enables surgical revocation: to revoke a compromised
cluster, remove only its JWKS from the distribution. Tokens from other clusters remain
valid because their signing keys are unaffected.

If JWKS from multiple clusters are merged into a single artifact, revoking one cluster
requires re-exporting and redistributing the merged JWKS without that cluster's keys —
a slower and more error-prone process during an incident.

### Recovery checklist

1. Identify the scope: single identity, single key, or full cluster compromise
2. Apply the immediate action from the table above
3. Audit access logs for unauthorized requests during the compromise window
4. For key rotation: redistribute JWKS to all peers
5. For identity compromise: verify RoleBindings are removed on all clusters
6. Remove any temporary deny rules added to AuthPolicies
7. Re-issue tokens to legitimate users if signing key was rotated

## Multi-Gateway isolation and multi-tenancy

When multiple Gateways exist on the same cluster (e.g., per-tenant Gateways), each
Gateway has its own AuthPolicy and can use a different signing key. This section
documents the isolation properties validated on a live cluster.

### Isolation properties

Each Gateway's AuthPolicy generates a separate AuthConfig. When different AuthPolicies
reference different `signingKeyRefs`, the resulting wristband endpoints serve **different
JWKS** — each containing only the public key for that Gateway's signing key.

| Property                | Behavior                                                                   |
|-------------------------|----------------------------------------------------------------------------|
| JWKS per Gateway        | Isolated — each AuthConfig serves its own JWKS with a unique `kid`         |
| Cross-gateway wristband | Rejected (401) — signature verification fails because the JWKS don't match |
| Same-gateway wristband  | Accepted for authn, then SAR enforced — 200 if RBAC exists, 403 otherwise  |
| Signing key per Gateway | Independent — each AuthPolicy can reference a different Secret             |
| AuthConfig naming       | SHA-256 hash per AuthPolicy — distinct per Gateway                         |

### Validated test matrix

Two Gateways (`openshift-ai-inference` in `openshift-ingress`, `tenant-b-gateway` in
`tenant-b`), each with its own signing key:

| Token issuer  | Target Gateway | Result     | Reason                                                     |
|---------------|----------------|------------|------------------------------------------------------------|
| GW1 wristband | GW1            | 200 or 403 | Authn passes (same JWKS), authz depends on RBAC            |
| GW2 wristband | GW2            | 200 or 403 | Authn passes (same JWKS), authz depends on RBAC            |
| GW1 wristband | GW2            | 401        | Signature verification fails — different signing key       |
| GW2 wristband | GW1            | 401        | Signature verification fails — different signing key       |
| K8s token     | GW1 or GW2     | 200 or 403 | `kubernetesTokenReview` is independent of wristband config |

### Within a single Gateway

All HTTPRoutes attached to the same Gateway share the same AuthPolicy. There is **no
per-route wristband isolation** within a single Gateway:

- All routes use the same signing key
- All routes serve the same JWKS
- A wristband obtained via one route's `/api/v1/token` is valid for any route on that
  Gateway (subject to SAR authorization)

This is by design — wristband tokens carry identity, not route-specific authorization.
The SAR check provides per-resource access control.

### OIDC endpoint accessibility

Authorino's OIDC server (`authorino-authorino-oidc:8083`) is a ClusterIP service
accessible from any namespace within the cluster:

- **No NetworkPolicies** restrict access by default
- **Not enumerable** — AuthConfig names are SHA-256 hashes; there is no directory listing
  endpoint. An attacker would need to know the exact hash to access a specific JWKS
- **Security through hashing, not secrecy** — the JWKS contains only public keys.
  Knowing the JWKS endpoint does not compromise signing capability

### Limitations

1. **Per-tenant isolation requires separate Gateways**: If tenants must have independent
   signing keys and isolated wristband verification, each tenant needs its own Gateway
   with its own AuthPolicy. A shared Gateway means shared wristband keys.

2. **Signing key Secret namespace**: The signing key Secret must be in the namespace where
   the AuthConfig is created (typically `kuadrant-system`). Multiple Gateways in different
   namespaces all reference Secrets in the same AuthConfig namespace, which means Secret
   names must be unique across all tenants.

3. **OIDC endpoint is cluster-wide**: Any pod in the cluster can reach the OIDC endpoint.
   While the paths are not enumerable (SHA-256 hashes), this is defense-in-depth only.
   The JWKS public keys are not sensitive, but operators should be aware of this surface.

4. **AuthConfig hash stability**: If routes are added/removed from a Gateway, the
   AuthConfig hash may change, invalidating the wristband issuer URL. This affects both
   the `iss` claim in wristbands and any peer `issuerUrl` configurations. Use `jwksUrl`
   (not `issuerUrl`) on the consumer side to avoid dependency on the issuer URL.

## Template changes (future implementation)

### Template data struct

Add optional fields to `authPolicyTemplateData` in `authpolicy.go`:

```go
type authPolicyTemplateData struct {
    Name       string
    Namespace  string
    TargetKind string
    TargetName string
    // Wristband token exchange
    WristbandIssuer      string   // Authorino OIDC endpoint for this wristband config
    SigningKeyRefs       []string // Secret names: first signs, all published in JWKS
    TokenDuration        int      // Seconds (default 31536000 = 1 year)
    WristbandJwksUrl     string   // Local Authorino OIDC JWKS URL (contains all peer keys)
}
```

Since all peer signing keys are distributed as Secrets and configured in `signingKeyRefs`,
the local Authorino OIDC JWKS endpoint publishes all keys. The consumer side needs only
a single `jwt.jwksUrl` pointing to the local JWKS — no separate peer URLs are needed.

### New ObjectOption functions

Add to `options.go`:

```go
// WithWristbandResponse configures wristband token issuance on /api/v1/token.
// signingKeyRefs: first key signs, all keys published in JWKS for verification.
func WithWristbandResponse(issuer string, signingKeyRefs []string, tokenDuration int) ObjectOption

// WithWristbandAuthentication adds JWT authentication for wristband tokens.
// jwksUrl points to the local Authorino OIDC JWKS endpoint (which contains all peer keys).
func WithWristbandAuthentication(jwksUrl string) ObjectOption
```

### Detection mechanism

Token exchange is enabled by default for every managed Gateway. The controller
auto-generates a local signing key and configures wristband issuance/consumption
without any annotation.

For multi-cluster, operators add peer signing keys via an optional annotation:

```yaml
metadata:
  annotations:
    # Optional: peer keys appended after auto-generated local key in signingKeyRefs
    security.opendatahub.io/token-exchange-signing-keys: "peer-cluster-a-key,peer-cluster-b-key"
    # Optional: token duration in seconds (default: 31536000 = 1 year)
    security.opendatahub.io/token-exchange-duration: "31536000"
```

The controller:
1. Auto-generates `<gateway-name>-wristband-signing-key` Secret if it doesn't exist
2. Parses the annotation (if present) for additional peer key names
3. Builds `signingKeyRefs` as: `[auto-generated, ...peer-keys]`
4. Computes the JWKS URL from the AuthConfig name (see "Computing the AuthConfig name")

## Testing

### Prerequisites

Deploy the echo service infrastructure from `BATCH.md` (gateway, echo server, HTTPRoutes,
ServiceAccounts with RBAC).

### Test setup

The controller auto-generates the signing key Secret and creates the HTTPRoute for
`/api/v1/token`. No manual setup is needed for single-cluster token exchange.

```bash
# Verify the auto-generated signing key exists
oc get secret openshift-ai-inference-wristband-signing-key -n kuadrant-system

# Verify the token endpoint HTTPRoute was created
oc get httproute -A -l app.kubernetes.io/managed-by=odh-model-controller

# Discover AuthConfig name
oc get authconfigs.authorino.kuadrant.io -A
```

### Test setup: AuthPolicy (controller-generated)

The controller generates this AuthPolicy automatically. Shown here for reference and
manual testing. Replace `<AUTHCONFIG_NS>/<AUTHCONFIG_NAME>` with actual values from
`oc get authconfigs`:

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
            expression: >-
              auth.identity.user.username.startsWith('system:serviceaccount:')
                ? auth.identity.user.username.split(':')[2]
                : 'authenticated'

      # Accept JWT tokens (disabled on /api/v1/token to prevent token escalation)
      wristband-user:
        when:
          - predicate: "request.path != '/api/v1/token'"
        jwt:
          issuerUrl: "https://authorino-authorino-oidc.kuadrant-system.svc.cluster.local:8083/<AUTHCONFIG_NS>/<AUTHCONFIG_NAME>/x-wristband-token"
        overrides:
          user:
            expression: "{'username': auth.identity.username, 'groups': auth.identity.groups}"
          fairness:
            expression: auth.identity.iss
          objective:
            expression: >-
              auth.identity.username.startsWith('system:serviceaccount:')
                ? auth.identity.username.split(':')[2]
                : 'authenticated'
        priority: 1

    authorization:
      inference-access:
        when:
          - predicate: >-
              !(request.path == '/api/v1/token' ||
                request.path == '/v1/files' || request.path.startsWith('/v1/files/') ||
                request.path == '/v1/batches' || request.path.startsWith('/v1/batches/'))
        kubernetesSubjectAccessReview:
          user:
            expression: >-
              'x-maas-user' in request.headers
                ? request.headers['x-maas-user']
                : auth.identity.user.username
          authorizationGroups:
            expression: >-
              'x-maas-user' in request.headers
                ? ('x-maas-groups' in request.headers
                    ? request.headers['x-maas-groups'].split(',')
                    : [])
                : auth.identity.user.groups
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
      inference-access-delegate:
        when:
          - predicate: >-
              !(request.path == '/api/v1/token' ||
                request.path == '/v1/files' || request.path.startsWith('/v1/files/') ||
                request.path == '/v1/batches' || request.path.startsWith('/v1/batches/'))
          - predicate: "'x-maas-user' in request.headers"
        kubernetesSubjectAccessReview:
          user:
            expression: "auth.identity.user.username"
          authorizationGroups:
            expression: "auth.identity.user.groups"
          resourceAttributes:
            group:
              value: serving.kserve.io
            resource:
              value: llminferenceservices/delegate
            namespace:
              expression: request.path.split("/")[1]
            name:
              expression: request.path.split("/")[2]
            verb:
              value: post-delegate
        priority: 1

    response:
      success:
        headers:
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
          x-maas-user:
            when:
              - predicate: >-
                  request.path == '/v1/files' || request.path.startsWith('/v1/files/') ||
                  request.path == '/v1/batches' || request.path.startsWith('/v1/batches/')
            plain:
              expression: auth.identity.user.username
            priority: 0
          x-maas-groups:
            when:
              - predicate: >-
                  request.path == '/v1/files' || request.path.startsWith('/v1/files/') ||
                  request.path == '/v1/batches' || request.path.startsWith('/v1/batches/')
            plain:
              expression: "auth.identity.user.groups.join(',')"
            priority: 0
          x-wristband-token:
            when:
              - predicate: "request.path == '/api/v1/token'"
            wristband:
              issuer: "https://authorino-authorino-oidc.kuadrant-system.svc.cluster.local:8083/<AUTHCONFIG_NS>/<AUTHCONFIG_NAME>/x-wristband-token"
              tokenDuration: 31536000
              signingKeyRefs:
                - name: openshift-ai-inference-wristband-signing-key   # auto-generated
                  algorithm: ES256
                # ... peer keys from annotation appended here ...
              customClaims:
                username:
                  expression: auth.identity.user.username
                groups:
                  expression: auth.identity.user.groups
            priority: 0
```

### Test scenarios

| # | Scenario                                      | Auth                   | Path                | Expected                            |
|---|-----------------------------------------------|------------------------|---------------------|-------------------------------------|
| 1 | Get JWT via token endpoint                    | `Bearer <k8s-token>`   | `/api/v1/token`     | 200, response body contains JWT     |
| 2 | Decode JWT, verify claims                     | —                      | —                   | JWT has `username`, `groups` claims |
| 3 | Verify JWKS endpoint                          | —                      | —                   | OIDC discovery returns public key   |
| 4 | Use JWT for inference (local RBAC exists)     | `Bearer <jwt>`         | `/<ns>/<model>/...` | 200, SAR passes                     |
| 5 | Use JWT for inference (no local RBAC)         | `Bearer <jwt>`         | `/<ns>/<model>/...` | 403, SAR fails                      |
| 6 | Expired JWT                                   | `Bearer <expired-jwt>` | `/<ns>/<model>/...` | 401                                 |
| 7 | Local k8s token still works (backward compat) | `Bearer <k8s-token>`   | `/<ns>/<model>/...` | 200                                 |
| 8 | JWT on token endpoint (no escalation)         | `Bearer <jwt>`         | `/api/v1/token`     | 401, JWT auth disabled on this path |

### Test commands

```bash
GW=http://openshift-ai-inference-openshift-default.openshift-ingress.svc.cluster.local
TEST_USER_TOKEN=$(oc create token test-user -n echo-service)

# Scenario 1: Get JWT via /api/v1/token
RESPONSE=$(oc exec -n default dnsutils -- curl -s \
  -H "Authorization: Bearer $TEST_USER_TOKEN" \
  $GW/api/v1/token)
JWT=$(echo "$RESPONSE" | jq -r .token)
echo "JWT: ${JWT:0:50}..."

# Scenario 2: Decode the JWT
echo "$JWT" | cut -d. -f2 | tr '_-' '/+' | base64 -d 2>/dev/null | jq .
# Expected: {"username": "system:serviceaccount:echo-service:test-user", "groups": [...], ...}

# Scenario 3: Verify JWKS endpoint
oc exec -n default dnsutils -- curl -s \
  https://authorino-authorino-oidc.kuadrant-system.svc.cluster.local:8083/<AUTHCONFIG_NS>/<AUTHCONFIG_NAME>/x-wristband-token/.well-known/openid-connect/certs | jq .

# Scenario 4: Use JWT for inference (test-user has RBAC in echo-service)
oc exec -n default dnsutils -- curl -s \
  -H "Authorization: Bearer $JWT" \
  $GW/echo-service/echo-server/v1/chat/completions | jq .request.headers
# Expected: 200, headers show x-gateway-inference-fairness-id and x-gateway-inference-objective

# Scenario 7: Local k8s token still works
oc exec -n default dnsutils -- curl -s \
  -H "Authorization: Bearer $TEST_USER_TOKEN" \
  $GW/echo-service/echo-server/v1/chat/completions | jq .request.headers
# Expected: 200, same as before

# Scenario 8: JWT on token endpoint (no escalation)
oc exec -n default dnsutils -- curl -s -o /dev/null -w "%{http_code}" \
  -H "Authorization: Bearer $JWT" \
  $GW/api/v1/token
# Expected: 401 (wristband-user auth is disabled on /api/v1/token via when predicate)
```

## Open questions

1. **Token endpoint backend**: The `model-serving-api` server (`server/`, `config/server/`)
   is the production backend. Add a `/api/v1/token` handler that reads the wristband token
   from the upstream request header (injected by Authorino) and returns it in the response
   body as JSON.

2. **Multiple signing keys**: For key rotation, Authorino supports multiple
   `signingKeyRefs`. The first key is used for signing; all keys are published in the JWKS
   for verification. Document the rotation procedure with overlapping keys.

3. **Cross-cluster JWKS delivery**: The Authorino OIDC JWKS endpoint is a ClusterIP
   service, not reachable from peer clusters. The mechanism for making a peer's JWKS
   available on the consuming cluster (external endpoint, shared storage, manual export)
   is an operational concern to be defined per deployment topology.