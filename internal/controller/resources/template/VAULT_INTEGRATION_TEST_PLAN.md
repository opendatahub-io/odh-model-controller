# Vault OIDC Integration Test Plan

Validate external JWT authentication via the `security.opendatahub.io/authn-jwt-jwks`
Gateway annotation, using HashiCorp Vault as the OIDC provider.

This document is a companion to the "External JWT issuers" section of
`MULTI_CLUSTER_AUTH.md`. It covers Vault setup, cluster configuration, and a complete
set of test scenarios (functional, security, and edge cases) with copy-pasteable
commands.

**Validated**: All scenarios in this document have been tested on a live OCP cluster
with RHOAI 3.3, Kuadrant, and Authorino. See "Validated findings" at the end for
corrections discovered during testing.

## What the annotation does

When the controller sees `security.opendatahub.io/authn-jwt-jwks` on a Gateway, it
generates one `jwt-<prefix>` authentication rule in the AuthPolicy per issuer entry.
Each rule:

- Verifies the JWT signature against the configured JWKS URL
- Prefixes `username` and `groups` with `<prefix>:` to prevent collision with local
  Kubernetes identities (e.g., Vault user `alice` becomes `vault:alice`)
- Is disabled on `/api/v1/token` to prevent external JWTs from minting wristband tokens
- Has `priority: 2` (evaluated after `kubernetes-user` at 0 and `wristband-user` at 1)

## Prerequisites

- RHOAI or ODH installed with Kuadrant and Authorino on the cluster
- Gateway `openshift-ai-inference` in `openshift-ingress` namespace
- Controller-generated AuthPolicy active (or manual AuthPolicy with external issuer
  support)
- CLIs: `oc` (authenticated as cluster-admin), `jq`
- Network: Authorino must be able to reach the Vault JWKS endpoint (guaranteed by
  deploying Vault in-cluster)

## 1. Deploy Vault dev server (in-cluster)

Deploy a Vault instance in dev mode inside the cluster. This avoids external network
dependencies — Authorino can reach the JWKS URL via cluster DNS.

**OpenShift note**: Vault's default entrypoint requires `CAP_SETFCAP` and memory
locking (`mlock`), which are blocked by OpenShift's restricted SCC. The deployment
below uses `VAULT_DISABLE_MLOCK=true`, `SKIP_SETCAP=true`, and an explicit
`securityContext` to work within OpenShift's security constraints.

```bash
oc create namespace vault-oidc --dry-run=client -o yaml | oc apply -f -
```

```yaml
# vault-dev.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: vault
  namespace: vault-oidc
  labels:
    app.kubernetes.io/name: vault
spec:
  replicas: 1
  selector:
    matchLabels:
      app.kubernetes.io/name: vault
  template:
    metadata:
      labels:
        app.kubernetes.io/name: vault
    spec:
      containers:
        - name: vault
          image: hashicorp/vault:1.19
          command: ["vault"]
          args:
            - server
            - -dev
            - -dev-root-token-id=root
            - -dev-listen-address=0.0.0.0:8200
          ports:
            - containerPort: 8200
              protocol: TCP
          env:
            - name: VAULT_ADDR
              value: "http://127.0.0.1:8200"
            - name: VAULT_TOKEN
              value: "root"
            - name: SKIP_SETCAP
              value: "true"
            - name: VAULT_DISABLE_MLOCK
              value: "true"
          securityContext:
            allowPrivilegeEscalation: false
            capabilities:
              drop:
                - ALL
          readinessProbe:
            httpGet:
              path: /v1/sys/health
              port: 8200
            initialDelaySeconds: 5
            periodSeconds: 5
          resources:
            requests:
              cpu: 100m
              memory: 128Mi
            limits:
              cpu: 500m
              memory: 256Mi
---
apiVersion: v1
kind: Service
metadata:
  name: vault
  namespace: vault-oidc
  labels:
    app.kubernetes.io/name: vault
spec:
  selector:
    app.kubernetes.io/name: vault
  ports:
    - port: 8200
      targetPort: 8200
      protocol: TCP
```

```bash
oc apply -f vault-dev.yaml
oc wait -n vault-oidc deployment/vault --for=condition=Available --timeout=60s
```

Verify Vault is running:

```bash
oc exec -n vault-oidc deployment/vault -- vault status
```

## 2. Configure Vault OIDC provider

All commands run inside the Vault pod using `oc exec`. Vault dev mode pre-authenticates
with the root token.

### 2a. Create a named key for signing OIDC tokens

**Important**: Use `RS256`, not `ES256`. Vault's JWKS endpoint serves all keys
(including default RS256 keys). Authorino's go-jose library rejects ES256-signed JWTs
when the JWKS contains RS256 keys first (`unexpected signature algorithm "ES256";
expected ["RS256"]`). Using RS256 avoids this issue.

```bash
oc exec -n vault-oidc deployment/vault -- \
  vault write identity/oidc/key/inference-key \
    allowed_client_ids="*" \
    algorithm="RS256"
```

### 2b. Create an OIDC role with identity claims

The token template must include a `username` custom claim. Vault's default `sub` claim
is the entity UUID (e.g., `d875f26f-abb6-d959-...`), not the human-readable entity name.
The `username` claim provides the entity name for identity prefixing.

```bash
oc exec -n vault-oidc deployment/vault -- \
  vault write identity/oidc/role/inference-role \
    key="inference-key" \
    ttl="1h" \
    template='{"username": {{identity.entity.name}}, "groups": {{identity.entity.groups.names}}}'
```

The `sub` claim is automatically set to the entity ID (UUID) by Vault. The identity
normalization expression uses `auth.identity.username` (the custom claim), not
`auth.identity.sub` (the UUID).

### 2c. Create test entities

Create entities for test scenarios. Each entity represents a user identity in Vault.

```bash
# Entity: alice (will have individual RBAC)
ALICE_ID=$(oc exec -n vault-oidc deployment/vault -- \
  vault write -field=id identity/entity \
    name="alice" \
    metadata=role="data-scientist")

# Entity: bob (will have group-based RBAC)
BOB_ID=$(oc exec -n vault-oidc deployment/vault -- \
  vault write -field=id identity/entity \
    name="bob" \
    metadata=role="ml-engineer")

# Entity: test-user (same name as k8s SA, for collision testing)
TEST_USER_ID=$(oc exec -n vault-oidc deployment/vault -- \
  vault write -field=id identity/entity \
    name="test-user" \
    metadata=role="collision-test")

# Entity: unauthorized-user (no RBAC anywhere)
UNAUTH_ID=$(oc exec -n vault-oidc deployment/vault -- \
  vault write -field=id identity/entity \
    name="unauthorized-user" \
    metadata=role="none")
```

### 2d. Create a group and add members

```bash
oc exec -n vault-oidc deployment/vault -- \
  vault write identity/group \
    name="ml-engineers" \
    member_entity_ids="$BOB_ID"
```

### 2e. Create entity aliases

Vault requires entity aliases linked to an auth method to generate tokens. Enable the
`userpass` auth method, create an OIDC reader policy, and create aliases:

```bash
# Enable userpass auth
oc exec -n vault-oidc deployment/vault -- vault auth enable userpass

# Get the accessor for userpass (jq runs locally, not in the container)
ACCESSOR=$(oc exec -n vault-oidc deployment/vault -- \
  vault auth list -format=json | jq -r '.["userpass/"].accessor')

# Create a policy allowing users to read OIDC tokens
# (the default policy does not grant this — without it, token generation fails with 403)
oc exec -n vault-oidc deployment/vault -- sh -c '
cat > /tmp/oidc-reader.hcl <<POLICY
path "identity/oidc/token/*" {
  capabilities = ["read"]
}
POLICY
vault policy write oidc-reader /tmp/oidc-reader.hcl'

# Create userpass users with the oidc-reader policy
for USER in alice bob test-user unauthorized-user; do
  oc exec -n vault-oidc deployment/vault -- \
    vault write auth/userpass/users/$USER password="testpassword" policies="oidc-reader"
done

# Create entity aliases
oc exec -n vault-oidc deployment/vault -- \
  vault write identity/entity-alias name="alice" canonical_id="$ALICE_ID" mount_accessor="$ACCESSOR"
oc exec -n vault-oidc deployment/vault -- \
  vault write identity/entity-alias name="bob" canonical_id="$BOB_ID" mount_accessor="$ACCESSOR"
oc exec -n vault-oidc deployment/vault -- \
  vault write identity/entity-alias name="test-user" canonical_id="$TEST_USER_ID" mount_accessor="$ACCESSOR"
oc exec -n vault-oidc deployment/vault -- \
  vault write identity/entity-alias name="unauthorized-user" canonical_id="$UNAUTH_ID" mount_accessor="$ACCESSOR"
```

### 2f. Generate test tokens

To generate a token for a specific entity, log in as that user and read the OIDC token.

**Important**: The container has `VAULT_TOKEN=root` set as an environment variable
(for `vault` CLI admin operations). This root token takes precedence over `vault login`.
The helper below uses `unset VAULT_TOKEN` before logging in so that the userpass
token (which carries entity context) is used for the OIDC read.

```bash
# Helper function: get a Vault JWT for a user
vault_jwt() {
  local user=$1
  oc exec -n vault-oidc deployment/vault -- sh -c "
    unset VAULT_TOKEN
    vault login -method=userpass username=$user password=testpassword >/dev/null 2>&1
    vault read -field=token identity/oidc/token/inference-role
  "
}

# Generate tokens
ALICE_JWT=$(vault_jwt alice)
BOB_JWT=$(vault_jwt bob)
TEST_USER_JWT=$(vault_jwt test-user)
UNAUTH_JWT=$(vault_jwt unauthorized-user)
```

### 2g. Verify the JWKS endpoint

```bash
oc exec -n vault-oidc deployment/vault -- \
  curl -s http://127.0.0.1:8200/v1/identity/oidc/.well-known/keys | jq .
```

Expected: JSON with `keys` array containing RS256 public keys.

### 2h. Decode a test token

```bash
echo "$ALICE_JWT" | cut -d. -f2 | tr '_-' '/+' | base64 -d 2>/dev/null | jq .
```

Expected payload structure:

```json
{
  "aud": "...",
  "exp": 1776677808,
  "groups": null,
  "iat": 1776674208,
  "iss": "http://0.0.0.0:8200/v1/identity/oidc",
  "namespace": "root",
  "sub": "d875f26f-abb6-d959-6cf7-dee7e4d52094",
  "username": "alice"
}
```

Key observations:
- `iss` is `http://0.0.0.0:8200/...` (Vault's listen address), not the cluster DNS name.
  This is why `jwksUrl` must be used on the consumer side, not `issuerUrl` — issuer URL
  validation would fail because the `iss` claim doesn't match the cluster-accessible URL.
- `sub` is the entity UUID, not the entity name. The `username` custom claim carries the
  human-readable name.
- `groups` is `null` (not `[]`) when the entity has no group membership. The CEL identity
  normalization expression must handle null: `has(auth.identity.groups) &&
  auth.identity.groups != null ? ... : []`.

## 3. Cluster configuration

### 3a. Annotate the Gateway

Add the external issuer annotation to the Gateway. The JWKS URL uses the in-cluster
Vault Service DNS name:

```bash
oc annotate gateway openshift-ai-inference -n openshift-ingress \
  security.opendatahub.io/authn-jwt-jwks='[{"prefix":"vault","jwksUrl":"http://vault.vault-oidc.svc.cluster.local:8200/v1/identity/oidc/.well-known/keys"}]' \
  --overwrite
```

### 3b. Verify the AuthPolicy was updated

The controller should add a `jwt-vault` authentication rule to the AuthPolicy:

```bash
oc get authpolicy openshift-ai-inference-authn -n openshift-ingress -o yaml | grep -A 20 'jwt-vault'
```

Expected: a `jwt-vault` authentication section with:
- `when` predicate excluding `/api/v1/token`
- `jwt.jwksUrl` pointing to the Vault JWKS endpoint
- `overrides.user` expression prefixing identities with `vault:`
- `priority: 2`

If the controller has not yet implemented this feature, apply the AuthPolicy manually
(see "Manual AuthPolicy" appendix below).

### 3c. Create RBAC for prefixed Vault identities

```yaml
# vault-rbac.yaml

# User-based: vault:alice can access models in echo-service
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: vault-alice-inference-access
  namespace: echo-service
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: llminferenceservices-reader
subjects:
  - kind: User
    name: "vault:alice"
    apiGroup: rbac.authorization.k8s.io
---
# Group-based: vault:ml-engineers can access models in echo-service
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: vault-ml-engineers-inference-access
  namespace: echo-service
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: llminferenceservices-reader
subjects:
  - kind: Group
    name: "vault:ml-engineers"
    apiGroup: rbac.authorization.k8s.io
```

```bash
oc apply -f vault-rbac.yaml
```

Note: `vault:test-user` and `vault:unauthorized-user` intentionally have **no**
RoleBindings. This is required for the identity prefix isolation and negative tests.

## 4. Deploy test infrastructure

Reuse the echo-service infrastructure from `BATCH.md`. If it is already deployed, skip
to step 4c.

### 4a. Echo server

```bash
oc create namespace echo-service --dry-run=client -o yaml | oc apply -f -
```

```yaml
# echo-server.yaml
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
  labels:
    app.kubernetes.io/name: echo-server
spec:
  selector:
    app.kubernetes.io/name: echo-server
  ports:
    - port: 80
      targetPort: 8080
      protocol: TCP
```

```bash
oc apply -f echo-server.yaml
oc wait -n echo-service deployment/echo-server --for=condition=Available --timeout=60s
```

### 4b. HTTPRoute, ServiceAccount, and RBAC

```yaml
# echo-routes-and-rbac.yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: echo-inference
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
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: test-user
  namespace: echo-service
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: llminferenceservices-reader
rules:
  - apiGroups: ["serving.kserve.io"]
    resources: ["llminferenceservices"]
    verbs: ["get"]
---
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
```

```bash
oc apply -f echo-routes-and-rbac.yaml
```

### 4c. dnsutils pod

```bash
oc run dnsutils --image=registry.k8s.io/e2e-test-images/agnhost:2.39 \
  --restart=Never --command -- sleep infinity 2>/dev/null || true
oc wait pod dnsutils --for=condition=Ready --timeout=60s
```

### 4d. Set variables

```bash
GW="http://openshift-ai-inference-openshift-default.openshift-ingress.svc.cluster.local"
TEST_USER_TOKEN=$(oc create token test-user -n echo-service --audience=https://kubernetes.default.svc)
```

Refresh the Vault JWTs (they expire after 1 hour):

```bash
ALICE_JWT=$(vault_jwt alice)
BOB_JWT=$(vault_jwt bob)
TEST_USER_JWT=$(vault_jwt test-user)
UNAUTH_JWT=$(vault_jwt unauthorized-user)
```

## 5. Functional test scenarios

| # | Scenario                               | Token                | Path                                            | Expected                                       |
|---|----------------------------------------|----------------------|-------------------------------------------------|------------------------------------------------|
| 1 | Vault JWT, valid user RBAC             | `$ALICE_JWT`         | `/echo-service/echo-server/v1/chat/completions` | 200, identity = `vault:alice`                  |
| 2 | Vault JWT, group-based RBAC            | `$BOB_JWT`           | `/echo-service/echo-server/v1/chat/completions` | 200, identity = `vault:bob`                    |
| 3 | K8s token backward compatibility       | `$TEST_USER_TOKEN`   | `/echo-service/echo-server/v1/chat/completions` | 200, identity = `system:serviceaccount:...`    |
| 4 | Vault JWT on token endpoint (blocked)  | `$ALICE_JWT`         | `/api/v1/token`                                 | 401 (external JWT disabled on this path)       |

### Scenario 1: Vault JWT with valid user RBAC

`vault:alice` has a RoleBinding in `echo-service`. The Vault JWT should authenticate,
get prefixed to `vault:alice`, and pass the SubjectAccessReview.

```bash
oc exec dnsutils -- curl -s \
  -H "Authorization: Bearer $ALICE_JWT" \
  "$GW/echo-service/echo-server/v1/chat/completions" | jq .
```

Expected: 200. The echo server response should show the request was forwarded
(Authorino authenticated and authorized the request).

### Scenario 2: Vault JWT with group-based RBAC

`bob` is a member of the `ml-engineers` group in Vault. The JWT includes
`"groups": ["ml-engineers"]`, which the controller normalizes to
`["vault:ml-engineers"]`. The group RoleBinding in `echo-service` grants access.

```bash
oc exec dnsutils -- curl -s \
  -H "Authorization: Bearer $BOB_JWT" \
  "$GW/echo-service/echo-server/v1/chat/completions" | jq .
```

Expected: 200.

### Scenario 3: Kubernetes token backward compatibility

The existing `kubernetesTokenReview` authentication must continue to work unchanged.

```bash
oc exec dnsutils -- curl -s \
  -H "Authorization: Bearer $TEST_USER_TOKEN" \
  "$GW/echo-service/echo-server/v1/chat/completions" | jq .
```

Expected: 200.

### Scenario 4: Vault JWT on token endpoint (escalation prevention)

External JWT authentication is disabled on `/api/v1/token` via a `when` predicate.
Only `kubernetesTokenReview` can access the token endpoint (to prevent external JWTs
from minting wristband tokens).

```bash
oc exec dnsutils -- curl -s -o /dev/null -w "%{http_code}" \
  -H "Authorization: Bearer $ALICE_JWT" \
  "$GW/api/v1/token"
```

Expected: `401`.

## 6. Security test scenarios

| # | Scenario                        | Token                | Path                                            | Expected                |
|---|---------------------------------|----------------------|-------------------------------------------------|-------------------------|
| 5 | No RBAC for Vault identity      | `$UNAUTH_JWT`        | `/echo-service/echo-server/v1/chat/completions` | 403                     |
| 6 | Expired Vault JWT               | `$EXPIRED_JWT`       | `/echo-service/echo-server/v1/chat/completions` | 401                     |
| 7 | Tampered JWT payload            | `$TAMPERED_JWT`      | `/echo-service/echo-server/v1/chat/completions` | 401                     |
| 8 | Invalid JWT (garbage string)    | `not-a-jwt`          | `/echo-service/echo-server/v1/chat/completions` | 401                     |
| 9 | Identity prefix isolation       | `$TEST_USER_JWT`     | `/echo-service/echo-server/v1/chat/completions` | 403 (vault:test-user)   |

### Scenario 5: No RBAC for Vault identity

`vault:unauthorized-user` has no RoleBinding anywhere. Authentication passes (valid
JWT), but the SubjectAccessReview fails.

```bash
oc exec dnsutils -- curl -s -o /dev/null -w "%{http_code}" \
  -H "Authorization: Bearer $UNAUTH_JWT" \
  "$GW/echo-service/echo-server/v1/chat/completions"
```

Expected: `403`.

### Scenario 6: Expired Vault JWT

Create a Vault role with a very short TTL to produce an expired token:

```bash
# Create a short-lived role (2 seconds)
oc exec -n vault-oidc deployment/vault -- \
  vault write identity/oidc/role/expired-role \
    key="inference-key" \
    ttl="2s" \
    template='{"username": {{identity.entity.name}}, "groups": {{identity.entity.groups.names}}}'

# Get a token and wait for it to expire
EXPIRED_JWT=$(vault_jwt_with_role alice expired-role)
sleep 5

oc exec dnsutils -- curl -s -o /dev/null -w "%{http_code}" \
  -H "Authorization: Bearer $EXPIRED_JWT" \
  "$GW/echo-service/echo-server/v1/chat/completions"
```

Where `vault_jwt_with_role` is the same as `vault_jwt` but reads from the specified role:

```bash
vault_jwt_with_role() {
  local user=$1
  local role=$2
  oc exec -n vault-oidc deployment/vault -- sh -c "
    unset VAULT_TOKEN
    vault login -method=userpass username=$user password=testpassword >/dev/null 2>&1
    vault read -field=token identity/oidc/token/$role
  "
}
```

Expected: `401`.

### Scenario 7: Tampered JWT payload

Take a valid JWT and modify the payload to change the identity. The signature becomes
invalid:

```bash
# Split the JWT
HEADER=$(echo "$ALICE_JWT" | cut -d. -f1)
PAYLOAD=$(echo "$ALICE_JWT" | cut -d. -f2)
SIGNATURE=$(echo "$ALICE_JWT" | cut -d. -f3)

# Decode, modify, re-encode the payload
MODIFIED_PAYLOAD=$(echo "$PAYLOAD" | tr '_-' '/+' | base64 -d 2>/dev/null \
  | jq '.sub = "tampered-identity"' \
  | base64 | tr '/+' '_-' | tr -d '=')

TAMPERED_JWT="${HEADER}.${MODIFIED_PAYLOAD}.${SIGNATURE}"

oc exec dnsutils -- curl -s -o /dev/null -w "%{http_code}" \
  -H "Authorization: Bearer $TAMPERED_JWT" \
  "$GW/echo-service/echo-server/v1/chat/completions"
```

Expected: `401` (signature verification fails).

### Scenario 8: Invalid JWT (garbage string)

```bash
oc exec dnsutils -- curl -s -o /dev/null -w "%{http_code}" \
  -H "Authorization: Bearer not-a-jwt" \
  "$GW/echo-service/echo-server/v1/chat/completions"
```

Expected: `401`.

### Scenario 9: Identity prefix isolation

This is the critical security test. A Vault entity named `test-user` is prefixed to
`vault:test-user` by the identity normalization. The Kubernetes SA
`system:serviceaccount:echo-service:test-user` has RBAC (via the standard RoleBinding),
but `vault:test-user` has **no** RoleBinding. This proves the prefix prevents identity
collision.

```bash
# Vault JWT for test-user entity -> becomes vault:test-user -> no RBAC -> 403
oc exec dnsutils -- curl -s -o /dev/null -w "%{http_code}" \
  -H "Authorization: Bearer $TEST_USER_JWT" \
  "$GW/echo-service/echo-server/v1/chat/completions"
# Expected: 403

# K8s token for SA test-user -> system:serviceaccount:echo-service:test-user -> has RBAC -> 200
oc exec dnsutils -- curl -s -o /dev/null -w "%{http_code}" \
  -H "Authorization: Bearer $TEST_USER_TOKEN" \
  "$GW/echo-service/echo-server/v1/chat/completions"
# Expected: 200
```

This validates that Vault identity `test-user` and Kubernetes SA `test-user` are
distinct — the prefix mechanism prevents escalation.

## 7. Edge case scenarios

| #  | Scenario                   | Setup                                          | Expected                                     |
|----|----------------------------|-------------------------------------------------|----------------------------------------------|
| 10 | Unreachable JWKS URL       | Set jwksUrl to invalid endpoint                 | Vault JWT 401, k8s token 200                 |
| 11 | Remove annotation          | Remove `authn-jwt-jwks` annotation              | Vault JWT 401, k8s token 200                 |
| 12 | Re-add annotation          | Re-apply the annotation                         | Vault JWT works again                        |
| 13 | Multiple external issuers  | Add a second prefix entry                       | Both JWT types authenticate correctly        |
| 14 | Empty groups in JWT        | Entity with no group membership                 | User-level RBAC only, empty groups in SAR    |
| 15 | No token at all            | Omit Authorization header                       | 401                                          |

### Scenario 10: Unreachable JWKS URL

Set the JWKS URL to an endpoint that does not exist. Authorino cannot fetch the keys,
so Vault JWTs fail verification. Kubernetes tokens are unaffected.

```bash
# Set an invalid JWKS URL
oc annotate gateway openshift-ai-inference -n openshift-ingress \
  security.opendatahub.io/authn-jwt-jwks='[{"prefix":"vault","jwksUrl":"http://does-not-exist.svc:8200/v1/identity/oidc/.well-known/keys"}]' \
  --overwrite

# Wait for AuthPolicy to reconcile
sleep 5

# Vault JWT should fail
oc exec dnsutils -- curl -s -o /dev/null -w "%{http_code}" \
  -H "Authorization: Bearer $ALICE_JWT" \
  "$GW/echo-service/echo-server/v1/chat/completions"
# Expected: 401

# K8s token should still work
oc exec dnsutils -- curl -s -o /dev/null -w "%{http_code}" \
  -H "Authorization: Bearer $TEST_USER_TOKEN" \
  "$GW/echo-service/echo-server/v1/chat/completions"
# Expected: 200

# Restore the correct URL
oc annotate gateway openshift-ai-inference -n openshift-ingress \
  security.opendatahub.io/authn-jwt-jwks='[{"prefix":"vault","jwksUrl":"http://vault.vault-oidc.svc.cluster.local:8200/v1/identity/oidc/.well-known/keys"}]' \
  --overwrite
```

### Scenario 11: Remove annotation

Removing the annotation should cause the controller to regenerate the AuthPolicy
without the `jwt-vault` rule. Vault JWTs become unrecognized.

```bash
oc annotate gateway openshift-ai-inference -n openshift-ingress \
  security.opendatahub.io/authn-jwt-jwks- \

# Wait for AuthPolicy to reconcile
sleep 5

# Vault JWT should fail (no jwt-vault auth rule)
oc exec dnsutils -- curl -s -o /dev/null -w "%{http_code}" \
  -H "Authorization: Bearer $ALICE_JWT" \
  "$GW/echo-service/echo-server/v1/chat/completions"
# Expected: 401

# K8s token should still work
oc exec dnsutils -- curl -s -o /dev/null -w "%{http_code}" \
  -H "Authorization: Bearer $TEST_USER_TOKEN" \
  "$GW/echo-service/echo-server/v1/chat/completions"
# Expected: 200
```

### Scenario 12: Re-add annotation

```bash
oc annotate gateway openshift-ai-inference -n openshift-ingress \
  security.opendatahub.io/authn-jwt-jwks='[{"prefix":"vault","jwksUrl":"http://vault.vault-oidc.svc.cluster.local:8200/v1/identity/oidc/.well-known/keys"}]' \
  --overwrite

# Wait for AuthPolicy to reconcile
sleep 5

# Vault JWT should work again (need a fresh token if the old one expired)
ALICE_JWT=$(vault_jwt alice)
oc exec dnsutils -- curl -s -o /dev/null -w "%{http_code}" \
  -H "Authorization: Bearer $ALICE_JWT" \
  "$GW/echo-service/echo-server/v1/chat/completions"
# Expected: 200
```

### Scenario 13: Multiple external issuers

Add a second external issuer (simulated by a second Vault role with a different prefix):

```bash
oc annotate gateway openshift-ai-inference -n openshift-ingress \
  security.opendatahub.io/authn-jwt-jwks='[{"prefix":"vault","jwksUrl":"http://vault.vault-oidc.svc.cluster.local:8200/v1/identity/oidc/.well-known/keys"},{"prefix":"vault2","jwksUrl":"http://vault.vault-oidc.svc.cluster.local:8200/v1/identity/oidc/.well-known/keys"}]' \
  --overwrite

# Wait for AuthPolicy to reconcile
sleep 5

# Verify both jwt-vault and jwt-vault2 auth rules exist
oc get authpolicy openshift-ai-inference-authn -n openshift-ingress -o yaml | grep -E 'jwt-vault|jwt-vault2'
```

Create RBAC for the second prefix:

```bash
oc create rolebinding vault2-alice-inference-access -n echo-service \
  --clusterrole=llminferenceservices-reader \
  --user="vault2:alice"
```

Test that the same Vault JWT authenticates under the second prefix:

```bash
# vault2:alice should also work
oc exec dnsutils -- curl -s -o /dev/null -w "%{http_code}" \
  -H "Authorization: Bearer $ALICE_JWT" \
  "$GW/echo-service/echo-server/v1/chat/completions"
# Expected: 200 (matches either jwt-vault or jwt-vault2, both have valid JWKS)
```

Restore single issuer:

```bash
oc annotate gateway openshift-ai-inference -n openshift-ingress \
  security.opendatahub.io/authn-jwt-jwks='[{"prefix":"vault","jwksUrl":"http://vault.vault-oidc.svc.cluster.local:8200/v1/identity/oidc/.well-known/keys"}]' \
  --overwrite
oc delete rolebinding vault2-alice-inference-access -n echo-service
```

### Scenario 14: Empty groups in JWT

`alice` is not a member of any Vault group. The JWT's `groups` claim is `null` (not
`[]` — Vault uses null for absent group membership). The CEL expression handles null
and normalizes it to an empty list. The SAR uses an empty groups list, so only
user-level RBAC applies.

```bash
# alice has user-level RBAC (vault:alice RoleBinding exists) -> 200
oc exec dnsutils -- curl -s -o /dev/null -w "%{http_code}" \
  -H "Authorization: Bearer $ALICE_JWT" \
  "$GW/echo-service/echo-server/v1/chat/completions"
# Expected: 200
```

### Scenario 15: No token at all

```bash
oc exec dnsutils -- curl -s -o /dev/null -w "%{http_code}" \
  "$GW/echo-service/echo-server/v1/chat/completions"
# Expected: 401
```

## 8. Wristband token coexistence

If wristband token exchange is configured on the same cluster (see
`MULTI_CLUSTER_AUTH.md`), verify that all three authentication methods coexist.

### Scenario 16: Wristband token still works alongside Vault

```bash
# Get a wristband (requires token exchange to be configured)
WRISTBAND=$(oc exec dnsutils -- curl -s \
  -H "Authorization: Bearer $TEST_USER_TOKEN" \
  "$GW/api/v1/token" | jq -r '.request.headers["x-wristband-token"]')

# Use the wristband for inference
oc exec dnsutils -- curl -s -o /dev/null -w "%{http_code}" \
  -H "Authorization: Bearer $WRISTBAND" \
  "$GW/echo-service/echo-server/v1/chat/completions"
# Expected: 200 (wristband auth is independent of Vault auth)
```

### Scenario 17: All three auth methods in one session

```bash
echo "K8s token:"
oc exec dnsutils -- curl -s -o /dev/null -w "%{http_code}" \
  -H "Authorization: Bearer $TEST_USER_TOKEN" \
  "$GW/echo-service/echo-server/v1/chat/completions"
# Expected: 200

echo "Vault JWT:"
oc exec dnsutils -- curl -s -o /dev/null -w "%{http_code}" \
  -H "Authorization: Bearer $ALICE_JWT" \
  "$GW/echo-service/echo-server/v1/chat/completions"
# Expected: 200

echo "Wristband:"
oc exec dnsutils -- curl -s -o /dev/null -w "%{http_code}" \
  -H "Authorization: Bearer $WRISTBAND" \
  "$GW/echo-service/echo-server/v1/chat/completions"
# Expected: 200
```

## 9. Automated e2e test design

The manual scenarios above map to Go e2e tests following the patterns in
`internal/controller/test/e2e/batch_authpolicy_test.go`.

### Proposed file structure

```
internal/controller/test/e2e/
  vault_jwt_test.go          # Test functions (//go:build e2e)
  vault_jwt_test_env.go      # vaultTestEnv struct, setup/teardown helpers
```

### Test environment

```go
type vaultTestEnv struct {
    gatewayURL       string
    gatewayName      string
    gatewayNamespace string
    vaultNamespace   string
    echoNamespace    string
    clientset        *kubernetes.Clientset
    k8sClient        client.Client
    httpClient       *http.Client
    portForwardStop  chan struct{}
}
```

Key helper methods:

| Method | Purpose |
|--------|---------|
| `deployVault(t)` | Deploy Vault dev server pod + service |
| `configureVaultOIDC(t)` | Create named key, OIDC role, entities, groups, aliases |
| `getVaultJWT(t, user string) string` | Log in as user, read OIDC token |
| `annotateGateway(t, issuers string)` | Set `authn-jwt-jwks` annotation |
| `removeGatewayAnnotation(t)` | Remove `authn-jwt-jwks` annotation |
| `createVaultRBAC(t, prefix, name, ns string)` | Create RoleBinding for prefixed identity |
| `waitForAuthPolicyRule(t, ruleName string)` | Poll until the auth rule appears in the AuthPolicy |

### Test function mapping

| Test function | Scenario # | What it validates |
|---------------|------------|-------------------|
| `TestVaultJWTUserRBAC` | 1 | Vault JWT + user RoleBinding = 200 |
| `TestVaultJWTGroupRBAC` | 2 | Vault JWT + group RoleBinding = 200 |
| `TestKubernetesTokenBackwardCompat` | 3 | K8s token still works = 200 |
| `TestVaultJWTTokenEndpointBlocked` | 4 | Vault JWT on /api/v1/token = 401 |
| `TestVaultJWTNoRBAC` | 5 | Vault JWT without RBAC = 403 |
| `TestVaultJWTExpired` | 6 | Expired JWT = 401 |
| `TestVaultJWTTampered` | 7 | Modified payload = 401 |
| `TestVaultJWTInvalid` | 8 | Garbage string = 401 |
| `TestVaultJWTIdentityPrefixIsolation` | 9 | vault:test-user 403, k8s test-user 200 |
| `TestVaultJWTUnreachableJWKS` | 10 | Bad URL = vault 401, k8s 200 |
| `TestVaultJWTAnnotationRemoval` | 11 | Remove annotation = vault 401, k8s 200 |
| `TestVaultJWTAnnotationRestore` | 12 | Re-add annotation = vault 200 |
| `TestMultipleExternalIssuers` | 13 | Two prefixes, both authenticate |
| `TestVaultJWTEmptyGroups` | 14 | No groups = user RBAC only |
| `TestNoToken` | 15 | Missing auth header = 401 |

### TestMain

```go
func TestMain(m *testing.M) {
    env, err := newVaultTestEnv()  // gateway discovery, port-forward
    if err != nil {
        fmt.Fprintf(os.Stderr, "setup: %v\n", err)
        os.Exit(1)
    }
    env.deployVault(nil)              // deploy Vault pod + service
    env.configureVaultOIDC(nil)       // create OIDC provider, entities, groups
    env.deployEchoServer(nil)         // deploy echo server + HTTPRoute
    env.annotateGateway(nil, `[...]`) // set the external issuer annotation
    env.createVaultRBAC(nil, ...)     // create RoleBindings for prefixed identities

    code := m.Run()
    env.close()
    os.Exit(code)
}
```

### Token generation in Go

Instead of shelling out to `vault`, call the Vault HTTP API directly:

```go
func (e *vaultTestEnv) getVaultJWT(t *testing.T, user string) string {
    // POST /v1/auth/userpass/login/<user> -> client_token
    // GET  /v1/identity/oidc/token/inference-role with X-Vault-Token -> token
}
```

### Makefile target

```makefile
test-e2e-vault:
	go test -v -tags=e2e -timeout=10m -parallel=8 \
	  ./internal/controller/test/e2e/ -run TestVault -count=1
```

## 10. Verification checklist

| # | Check                                                       | Pass |
|---|-------------------------------------------------------------|------|
| 1 | Vault JWT authenticates with valid user RBAC (200)          | [ ]  |
| 2 | Group-based RBAC works with prefixed groups (200)           | [ ]  |
| 3 | K8s token backward compatibility (200)                      | [ ]  |
| 4 | Vault JWT blocked on /api/v1/token (401)                    | [ ]  |
| 5 | No RBAC for Vault identity (403)                            | [ ]  |
| 6 | Expired Vault JWT (401)                                     | [ ]  |
| 7 | Tampered JWT rejected (401)                                 | [ ]  |
| 8 | Invalid token rejected (401)                                | [ ]  |
| 9 | Identity prefix prevents collision (vault:X != k8s X)       | [ ]  |
| 10 | Unreachable JWKS: vault 401, k8s 200                       | [ ]  |
| 11 | Annotation removal: vault 401, k8s 200                     | [ ]  |
| 12 | Annotation restore: vault works again                      | [ ]  |
| 13 | Multiple external issuers coexist                          | [ ]  |
| 14 | Empty groups: user-level RBAC only                         | [ ]  |
| 15 | Missing auth header (401)                                  | [ ]  |
| 16 | Wristband token works alongside Vault (200)                | [ ]  |
| 17 | All three auth methods coexist                             | [ ]  |

## 11. Cleanup

```bash
# Remove Vault RBAC
oc delete rolebinding vault-alice-inference-access -n echo-service
oc delete rolebinding vault-ml-engineers-inference-access -n echo-service

# Remove Gateway annotation
oc annotate gateway openshift-ai-inference -n openshift-ingress \
  security.opendatahub.io/authn-jwt-jwks-

# Remove Vault
oc delete namespace vault-oidc

# Remove echo server (if created for this test)
oc delete httproute echo-inference -n echo-service
oc delete deployment echo-server -n echo-service
oc delete service echo-server -n echo-service
oc delete rolebinding test-user-inference-access -n echo-service
oc delete serviceaccount test-user -n echo-service

# Remove dnsutils
oc delete pod dnsutils

# Remove namespace (if no longer needed)
oc delete namespace echo-service

# Remove ClusterRole (shared, check if other tests need it)
# oc delete clusterrole llminferenceservices-reader
```

## 12. Known limitations

- **Vault dev mode**: Uses HTTP without TLS. Suitable for testing only. The JWKS URL
  uses `http://` which Authorino accepts. In production, Vault should use TLS and the
  JWKS URL should use `https://`.
- **JWKS cache refresh**: Authorino caches JWKS and refreshes periodically. Immediate
  changes to the JWKS (e.g., key rotation in Vault) may not be reflected for a few
  seconds. Allow a brief delay in tests after JWKS changes.
- **`jwksUrl` vs `issuerUrl`**: The annotation uses `jwksUrl`, which means Authorino
  does **not** validate the `iss` claim in the JWT. This is required because Vault dev
  mode's `iss` claim uses the internal listen address (`http://0.0.0.0:8200/...`), not
  the cluster DNS name. Even in production, `jwksUrl` is preferred for flexibility.
- **Priority ordering**: External issuers use `priority: 2`. Authorino evaluates
  lower-priority methods first (`kubernetes-user` at 0, `wristband-user` at 1). If a
  token matches a higher-priority method, the external issuer is not evaluated.
- **Vault token TTL**: Vault OIDC tokens have a TTL configured on the role (default
  `1h` in this test plan). Refresh tokens before they expire when running tests
  manually.

## Validated findings

The following issues were discovered during live cluster validation and are reflected
in the document above. They are listed here as a reference for the design discussion.

### 1. OIDC key algorithm must be RS256

Vault's OIDC JWKS endpoint serves all named keys alongside default RS256 keys. When
the inference key uses ES256, the JWKS contains both RS256 and ES256 entries. Authorino's
go-jose library selects RS256 as the expected algorithm (based on JWKS ordering) and
rejects the ES256-signed JWT with:

```
oidc: malformed jwt: go-jose/go-jose: unexpected signature algorithm "ES256"; expected ["RS256"]
```

**Resolution**: Use `algorithm="RS256"` for the Vault OIDC named key. This aligns with
the default keys already in the JWKS.

**Impact on MULTI_CLUSTER_AUTH.md**: The design doc's Vault example should note that
the OIDC key algorithm must match what Authorino expects from the JWKS. If the JWKS
serves multiple algorithms, Authorino may not handle mixed-algorithm JWKS correctly.

### 2. `sub` claim is entity UUID, not entity name

Vault's OIDC `sub` claim contains the entity ID (UUID, e.g., `d875f26f-abb6-d959-...`),
not the human-readable entity name. The original design expression
`'vault:' + auth.identity.sub` would produce `vault:d875f26f-abb6-...`, which is
unusable for RoleBinding subjects.

**Resolution**: Add a `username` custom claim to the OIDC role template
(`{{identity.entity.name}}`), and use `auth.identity.username` in the identity
normalization expression.

**Impact on MULTI_CLUSTER_AUTH.md**: The external issuer section should document that
Vault requires a custom `username` claim and the identity expression should reference
`auth.identity.username`, not `auth.identity.sub`.

### 3. `iss` claim uses Vault's listen address

Vault sets the `iss` claim to its internal listen address (`http://0.0.0.0:8200/...`),
not the externally reachable service URL. This makes `issuerUrl`-based validation
impossible — Authorino would try to discover OIDC at `http://0.0.0.0:8200/...` which
is not reachable from the Authorino pod.

**Resolution**: Use `jwksUrl` (not `issuerUrl`) for external issuers. Authorino fetches
keys from the configured URL and skips `iss` validation.

### 4. `groups` claim is `null` when empty

Vault returns `"groups": null` (not `[]`) when an entity has no group membership. The
CEL expression must handle both `null` and `[]`:

```
has(auth.identity.groups) && auth.identity.groups != null
  ? auth.identity.groups.map(g, 'vault:' + g)
  : []
```

### 5. OpenShift SCC requires special container configuration

Vault's default entrypoint calls `setcap` and `mlock`, which OpenShift's restricted SCC
blocks. The container requires:
- `VAULT_DISABLE_MLOCK=true` and `SKIP_SETCAP=true` env vars
- Explicit `command: ["vault"]` (bypasses the default entrypoint)
- `securityContext.allowPrivilegeEscalation: false` and `capabilities.drop: [ALL]`

### 6. `VAULT_TOKEN` env var overrides `vault login`

The container's `VAULT_TOKEN=root` environment variable takes precedence over
`vault login`. When generating user-specific OIDC tokens, `VAULT_TOKEN` must be unset
first — otherwise the root token is used, which has no entity association, and the OIDC
read fails with "no entity associated with the request's token".

### 7. Vault userpass requires explicit OIDC policy

The default Vault policy does not grant permission to read OIDC tokens. Without an
explicit policy, users get a 403 when calling
`vault read identity/oidc/token/<role>`. An `oidc-reader` policy must be created and
attached to userpass users.

### Test results

All scenarios validated on OCP cluster with RHOAI 3.3, Kuadrant, and Authorino:

| # | Scenario                                    | Expected | Actual | Status    |
|---|---------------------------------------------|----------|--------|-----------|
| 1 | Vault JWT alice (user RBAC)                 | 200      | 200    | Validated |
| 2 | Vault JWT bob (group RBAC)                  | 200      | 200    | Validated |
| 3 | K8s token backward compatibility            | 200      | 200    | Validated |
| 4 | Vault JWT on /api/v1/token (blocked)        | 401      | 401    | Validated |
| 5 | No RBAC for Vault identity                  | 403      | 403    | Validated |
| 6 | Expired Vault JWT                           | 401      | 401    | Validated |
| 7 | Tampered JWT payload                        | 401      | 401    | Validated |
| 8 | Invalid JWT (garbage)                       | 401      | 401    | Validated |
| 9 | Identity prefix isolation                   | 403/200  | 403/200| Validated |
| 11| Remove jwt-vault rule                       | 401/200  | 401/200| Validated |
| 12| Re-add jwt-vault rule                       | 200      | 200    | Validated |
| 15| No token at all                             | 401      | 401    | Validated |
| 16| Wristband coexistence                       | 200      | 200    | Validated |

## Appendix: Manual AuthPolicy with external issuer

If the controller has not yet implemented `security.opendatahub.io/authn-jwt-jwks`
parsing, apply this AuthPolicy manually. It extends the standard
`authpolicy_llm_isvc_userdefined.yaml` template with a `jwt-vault` authentication
rule.

Label the existing AuthPolicy as unmanaged first:

```bash
oc label authpolicy openshift-ai-inference-authn -n openshift-ingress \
  opendatahub.io/managed=false
```

Then apply:

```yaml
apiVersion: kuadrant.io/v1
kind: AuthPolicy
metadata:
  name: openshift-ai-inference-authn
  namespace: openshift-ingress
  labels:
    app.kubernetes.io/name: llminferenceservice-auth
    app.kubernetes.io/component: llminferenceservice-policies
    app.kubernetes.io/part-of: llminferenceservice
    app.kubernetes.io/managed-by: manual
    opendatahub.io/managed: "false"
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

      jwt-vault:
        when:
          - predicate: "request.path != '/api/v1/token'"
        jwt:
          jwksUrl: "http://vault.vault-oidc.svc.cluster.local:8200/v1/identity/oidc/.well-known/keys"
        overrides:
          user:
            expression: "{'username': 'vault:' + auth.identity.username, 'groups': has(auth.identity.groups) && auth.identity.groups != null ? auth.identity.groups.map(g, 'vault:' + g) : []}"
          fairness:
            expression: auth.identity.iss
          objective:
            expression: "'authenticated'"
        priority: 2

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
```
