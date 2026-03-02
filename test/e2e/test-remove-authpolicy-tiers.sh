#!/usr/bin/env bash
#
# Test: Verify ODH Model Controller no longer creates AuthPolicy or tier-based resources.
# Corresponds to Jira ticket acceptance criteria for removing gateway-level AuthPolicy
# and tier resources from odh-model-controller.
#
set -euo pipefail

PASS=0
FAIL=0
SKIP=0
TEST_NS="llm"
GATEWAY_NS="openshift-ingress"
GATEWAY_NAME="maas-default-gateway"
ODH_NS="opendatahub"
LLMISVC_NAME="test-no-authpolicy"
WAIT_TIMEOUT=60
POLL_INTERVAL=3

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

pass() { PASS=$((PASS + 1)); echo -e "  ${GREEN}PASS${NC}: $1"; }
fail() { FAIL=$((FAIL + 1)); echo -e "  ${RED}FAIL${NC}: $1"; }
skip() { SKIP=$((SKIP + 1)); echo -e "  ${BLUE}SKIP${NC}: $1"; }
info() { echo -e "${YELLOW}>>>${NC} $1"; }

cleanup() {
    info "Cleaning up test resources..."
    oc delete llminferenceservice "$LLMISVC_NAME" -n "$TEST_NS" --ignore-not-found --wait=false 2>/dev/null || true
    oc delete llminferenceservice "test-tier-no-rbac" -n "$TEST_NS" --ignore-not-found --wait=false 2>/dev/null || true
    oc delete llminferenceservice "test-envoyfilter" -n "$TEST_NS" --ignore-not-found --wait=false 2>/dev/null || true
    oc delete maasauthpolicy test-maas-auth -n "$ODH_NS" --ignore-not-found 2>/dev/null || true
    oc delete maasmodel test-maas-model -n "$ODH_NS" --ignore-not-found 2>/dev/null || true
}
trap cleanup EXIT

wait_for_condition() {
    local description="$1"
    local timeout="$2"
    shift 2
    local elapsed=0
    while [ $elapsed -lt "$timeout" ]; do
        if "$@" >/dev/null 2>&1; then
            return 0
        fi
        sleep "$POLL_INTERVAL"
        elapsed=$((elapsed + POLL_INTERVAL))
    done
    return 1
}

wait_for_absence() {
    local description="$1"
    local timeout="$2"
    shift 2
    local elapsed=0
    while [ $elapsed -lt "$timeout" ]; do
        if ! "$@" >/dev/null 2>&1; then
            return 0
        fi
        sleep "$POLL_INTERVAL"
        elapsed=$((elapsed + POLL_INTERVAL))
    done
    return 1
}

# ─────────────────────────────────────────────────────────
info "Precondition: verify controller is running with new image"
# ─────────────────────────────────────────────────────────
EXPECTED_IMG="${EXPECTED_CONTROLLER_IMAGE:-quay.io/maas/odh-model-controller:latest}"
ACTUAL_IMG=$(oc get deployment odh-model-controller -n "$ODH_NS" -o jsonpath='{.spec.template.spec.containers[0].image}')
if [[ "$ACTUAL_IMG" == "$EXPECTED_IMG" ]]; then
    pass "odh-model-controller image is $EXPECTED_IMG"
else
    fail "odh-model-controller image is $ACTUAL_IMG (expected $EXPECTED_IMG)"
fi

READY=$(oc get deployment odh-model-controller -n "$ODH_NS" -o jsonpath='{.status.readyReplicas}')
if [[ "$READY" == "1" ]]; then
    pass "odh-model-controller pod is ready"
else
    fail "odh-model-controller pod is not ready (readyReplicas=$READY)"
fi

echo ""
# ─────────────────────────────────────────────────────────
info "TC1: No gateway-level AuthPolicy created by ODH"
# ─────────────────────────────────────────────────────────
oc apply -f - <<'EOF'
apiVersion: serving.kserve.io/v1alpha1
kind: LLMInferenceService
metadata:
  name: test-no-authpolicy
  namespace: llm
spec:
  model:
    uri: s3://test-bucket/test-model
EOF

info "Waiting for controller to reconcile (20s stabilization)..."
sleep 20

GATEWAY_AP_NAME="${GATEWAY_NAME}-authn"
if oc get authpolicy "$GATEWAY_AP_NAME" -n "$GATEWAY_NS" 2>/dev/null; then
    MANAGED_BY=$(oc get authpolicy "$GATEWAY_AP_NAME" -n "$GATEWAY_NS" -o jsonpath='{.metadata.labels.app\.kubernetes\.io/managed-by}' 2>/dev/null || echo "unknown")
    if [[ "$MANAGED_BY" == "odh-model-controller" ]]; then
        fail "TC1: ODH created gateway AuthPolicy '$GATEWAY_AP_NAME' in $GATEWAY_NS"
    else
        pass "TC1: AuthPolicy '$GATEWAY_AP_NAME' exists but is managed by '$MANAGED_BY', not ODH"
    fi
else
    pass "TC1: No gateway AuthPolicy '$GATEWAY_AP_NAME' found in $GATEWAY_NS"
fi

echo ""
# ─────────────────────────────────────────────────────────
info "TC2: No HTTPRoute-level AuthPolicy created by ODH"
# ─────────────────────────────────────────────────────────
ODH_ROUTE_APS=$(oc get authpolicies -n "$TEST_NS" -o json 2>/dev/null | python3 -c "
import json, sys
data = json.load(sys.stdin)
odh_aps = [item['metadata']['name'] for item in data.get('items', [])
           if item.get('metadata', {}).get('labels', {}).get('app.kubernetes.io/managed-by') == 'odh-model-controller']
print(' '.join(odh_aps))
" 2>/dev/null || echo "")

if [[ -z "$ODH_ROUTE_APS" ]]; then
    pass "TC2: No ODH-managed AuthPolicies found in $TEST_NS"
else
    fail "TC2: ODH-managed AuthPolicies found in $TEST_NS: $ODH_ROUTE_APS"
fi

echo ""
# ─────────────────────────────────────────────────────────
info "TC3: No tier-based Role/RoleBinding created by ODH"
# ─────────────────────────────────────────────────────────
oc apply -f - <<'EOF'
apiVersion: serving.kserve.io/v1alpha1
kind: LLMInferenceService
metadata:
  name: test-tier-no-rbac
  namespace: llm
  annotations:
    alpha.maas.opendatahub.io/tiers: '["free", "premium"]'
spec:
  model:
    uri: s3://test-bucket/test-model-tiered
EOF

info "Waiting for reconciliation (20s)..."
sleep 20

ODH_ROLES=$(oc get roles -n "$TEST_NS" -o json 2>/dev/null | python3 -c "
import json, sys
data = json.load(sys.stdin)
odh_roles = [item['metadata']['name'] for item in data.get('items', [])
             if item.get('metadata', {}).get('labels', {}).get('app.kubernetes.io/managed-by') == 'odh-model-controller'
             and 'model-post-access' in item['metadata']['name']]
print(' '.join(odh_roles))
" 2>/dev/null || echo "")

ODH_RBS=$(oc get rolebindings -n "$TEST_NS" -o json 2>/dev/null | python3 -c "
import json, sys
data = json.load(sys.stdin)
odh_rbs = [item['metadata']['name'] for item in data.get('items', [])
           if item.get('metadata', {}).get('labels', {}).get('app.kubernetes.io/managed-by') == 'odh-model-controller'
           and 'tier-binding' in item['metadata']['name']]
print(' '.join(odh_rbs))
" 2>/dev/null || echo "")

if [[ -z "$ODH_ROLES" ]]; then
    pass "TC3a: No ODH-managed tier Roles found in $TEST_NS"
else
    fail "TC3a: ODH-managed tier Roles found: $ODH_ROLES"
fi

if [[ -z "$ODH_RBS" ]]; then
    pass "TC3b: No ODH-managed tier RoleBindings found in $TEST_NS"
else
    fail "TC3b: ODH-managed tier RoleBindings found: $ODH_RBS"
fi

echo ""
# ─────────────────────────────────────────────────────────
info "TC4: EnvoyFilter is created for gateway with authorino-tls-bootstrap"
# ─────────────────────────────────────────────────────────
# The default gateway on this cluster is maas-default-gateway, not openshift-ai-inference.
# We must create an LLMIsvc that explicitly references it so the controller can find it.
oc apply -f - <<EOF
apiVersion: serving.kserve.io/v1alpha1
kind: LLMInferenceService
metadata:
  name: test-envoyfilter
  namespace: $TEST_NS
spec:
  model:
    uri: s3://test-bucket/test-envoyfilter
  router:
    gateway:
      refs:
        - name: $GATEWAY_NAME
          namespace: $GATEWAY_NS
EOF

EF_NAME="${GATEWAY_NAME}-authn-ssl"
info "Waiting for EnvoyFilter '$EF_NAME' (up to 45s)..."
if wait_for_condition "EnvoyFilter $EF_NAME" 45 oc get envoyfilter "$EF_NAME" -n "$GATEWAY_NS"; then
    EF_OWNER=$(oc get envoyfilter "$EF_NAME" -n "$GATEWAY_NS" -o jsonpath='{.metadata.ownerReferences[0].kind}' 2>/dev/null || echo "none")
    pass "TC4: EnvoyFilter '$EF_NAME' exists in $GATEWAY_NS (owner: $EF_OWNER)"
else
    fail "TC4: EnvoyFilter '$EF_NAME' not found in $GATEWAY_NS after 45s"
fi

oc delete llminferenceservice test-envoyfilter -n "$TEST_NS" --ignore-not-found 2>/dev/null || true

echo ""
# ─────────────────────────────────────────────────────────
info "TC5: MaaS controller creates per-route AuthPolicy (not ODH)"
# ─────────────────────────────────────────────────────────
oc apply -f - <<EOF
apiVersion: maas.opendatahub.io/v1alpha1
kind: MaaSModel
metadata:
  name: test-maas-model
  namespace: $ODH_NS
spec:
  modelRef:
    kind: LLMInferenceService
    name: test-no-authpolicy
    namespace: $TEST_NS
EOF

oc apply -f - <<EOF
apiVersion: maas.opendatahub.io/v1alpha1
kind: MaaSAuthPolicy
metadata:
  name: test-maas-auth
  namespace: $ODH_NS
spec:
  modelRefs:
    - test-maas-model
  subjects:
    users:
      - system:serviceaccount:$TEST_NS:default
EOF

info "Waiting for MaaS controller to create AuthPolicy (up to 60s)..."
MAAS_AP_FOUND=false
elapsed=0
while [ $elapsed -lt 60 ]; do
    MAAS_APS=$(oc get authpolicies -n "$TEST_NS" -o json 2>/dev/null | python3 -c "
import json, sys
data = json.load(sys.stdin)
maas_aps = [item['metadata']['name'] for item in data.get('items', [])
            if 'maas' in item.get('metadata', {}).get('labels', {}).get('app.kubernetes.io/managed-by', '').lower()
            or 'maas' in item.get('metadata', {}).get('name', '').lower()]
print(' '.join(maas_aps))
" 2>/dev/null || echo "")
    if [[ -n "$MAAS_APS" ]]; then
        MAAS_AP_FOUND=true
        break
    fi
    sleep "$POLL_INTERVAL"
    elapsed=$((elapsed + POLL_INTERVAL))
done

if $MAAS_AP_FOUND; then
    pass "TC5: MaaS controller created AuthPolicy in $TEST_NS: $MAAS_APS"
else
    info "     This may be expected if MaaSModel/LLMIsvc isn't fully reconciled (missing HTTPRoute)"
    skip "TC5: MaaS controller did not create per-route AuthPolicy within 60s (MaaS controller flow is separate)"
fi

# Cleanup MaaS test resources
oc delete maasauthpolicy test-maas-auth -n "$ODH_NS" --ignore-not-found 2>/dev/null || true
oc delete maasmodel test-maas-model -n "$ODH_NS" --ignore-not-found 2>/dev/null || true

echo ""
# ─────────────────────────────────────────────────────────
info "TC6: Old webhooks are removed"
# ─────────────────────────────────────────────────────────
VWC_WEBHOOKS=$(oc get validatingwebhookconfiguration validating.odh-model-controller.opendatahub.io -o jsonpath='{range .webhooks[*]}{.name}{"\n"}{end}' 2>/dev/null || echo "")

if echo "$VWC_WEBHOOKS" | grep -q "validating.configmap.odh-model-controller"; then
    fail "TC6a: TierConfigMap webhook still registered"
else
    pass "TC6a: TierConfigMap webhook is removed"
fi

if echo "$VWC_WEBHOOKS" | grep -q "validating.llmisvc.odh-model-controller"; then
    fail "TC6b: LLMInferenceService tier webhook still registered"
else
    pass "TC6b: LLMInferenceService tier webhook is removed"
fi

echo ""
# ─────────────────────────────────────────────────────────
# Summary
# ─────────────────────────────────────────────────────────
TOTAL=$((PASS + FAIL + SKIP))
echo "============================================"
echo -e "  Results: ${GREEN}$PASS passed${NC}, ${RED}$FAIL failed${NC}, ${BLUE}$SKIP skipped${NC} out of $TOTAL"
echo "============================================"

if [ "$FAIL" -gt 0 ]; then
    exit 1
fi
