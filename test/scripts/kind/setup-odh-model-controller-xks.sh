#!/usr/bin/env bash
# Smoke test: deploy OMC xKS overlay on Kind and verify webhook-only mode.
# Webhook TLS follows the KServe odh-xks pattern (cert-manager Certificate + inject-ca-from).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
CLUSTER_NAME="${CLUSTER_NAME:-odh-model-controller-xks}"
IMAGE="${IMAGE:-odh-model-controller:xks-smoke}"
NAMESPACE="${NAMESPACE:-opendatahub}"
KIND_CONFIG="${KIND_CONFIG:-${ROOT}/test/config/kind-xks-config.yaml}"
CERT_MANAGER_VERSION="${CERT_MANAGER_VERSION:-v1.16.0}"
KEEP_CLUSTER="${KEEP_CLUSTER:-false}"

if [[ "${KEEP_CLUSTER}" != "true" ]]; then
  trap 'kind delete cluster --name "${CLUSTER_NAME}" >/dev/null 2>&1 || true' EXIT
fi

log() { printf '==> %s\n' "$*"; }
fail() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

need() { command -v "$1" >/dev/null 2>&1 || fail "$1 is required"; }
need kind
need docker
need kubectl

log "Creating single-node Kind cluster ${CLUSTER_NAME}"
kind delete cluster --name "${CLUSTER_NAME}" >/dev/null 2>&1 || true
kind create cluster --name "${CLUSTER_NAME}" --config "${KIND_CONFIG}"
kubectl wait --for=condition=Ready nodes --all --timeout=180s

log "Building controller image ${IMAGE}"
docker build -t "${IMAGE}" -f "${ROOT}/Containerfile" "${ROOT}"

log "Loading image into Kind"
kind load docker-image "${IMAGE}" --name "${CLUSTER_NAME}"

log "Installing cert-manager ${CERT_MANAGER_VERSION}"
kubectl create namespace cert-manager --dry-run=client -o yaml | kubectl apply -f -
kubectl apply -f "https://github.com/cert-manager/cert-manager/releases/download/${CERT_MANAGER_VERSION}/cert-manager.yaml"
kubectl wait --for=condition=Available deployment/cert-manager-webhook \
  -n cert-manager --timeout=300s
# Give the webhook a moment after the Deployment reports Available.
sleep 5

log "Bootstrapping OpenDataHub CA ClusterIssuer (Kind PKI)"
kubectl apply -k "${ROOT}/test/config/kind-xks-cert-manager"
kubectl wait --for=condition=Ready clusterissuer/opendatahub-selfsigned-issuer --timeout=60s
kubectl wait --for=condition=Ready certificate/opendatahub-ca -n cert-manager --timeout=120s
kubectl wait --for=condition=Ready clusterissuer/opendatahub-ca-issuer --timeout=60s

log "Installing minimal KServe CRDs"
kubectl apply --server-side -f "${ROOT}/test/crds/serving.kserve.io_llminferenceservices.yaml"

log "Ensuring target namespace exists"
kubectl create namespace "${NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f -

log "Rendering and applying xKS overlay"
kubectl kustomize "${ROOT}/config/overlays/xks" \
  | sed -E "s|(image: ).*odh-model-controller[^[:space:]]*|\\1${IMAGE}|" \
  | sed 's|imagePullPolicy: Always|imagePullPolicy: IfNotPresent|' \
  | kubectl apply -f -

kubectl patch deployment odh-model-controller -n "${NAMESPACE}" --type=json -p='[
  {"op":"replace","path":"/spec/template/spec/containers/0/args","value":["--health-probe-bind-address=:8081"]},
  {"op":"replace","path":"/spec/template/spec/containers/0/livenessProbe/initialDelaySeconds","value":30},
  {"op":"replace","path":"/spec/template/spec/containers/0/readinessProbe/initialDelaySeconds","value":15}
]'

log "Waiting for webhook Certificate to be Ready"
kubectl wait --for=condition=Ready \
  certificate/odh-model-controller-webhook \
  -n "${NAMESPACE}" --timeout=120s

log "Waiting for cert-manager to inject webhook CA bundle"
deadline=$((SECONDS + 120))
while true; do
  ca_bundle="$(kubectl get mutatingwebhookconfiguration mutating.odh-model-controller.opendatahub.io \
    -o jsonpath='{.webhooks[0].clientConfig.caBundle}' 2>/dev/null || true)"
  if [[ -n "${ca_bundle}" ]]; then
    break
  fi
  if (( SECONDS >= deadline )); then
    fail "timed out waiting for cert-manager CA injection on MutatingWebhookConfiguration"
  fi
  sleep 2
done

log "Waiting for controller deployment"
kubectl -n "${NAMESPACE}" rollout status deployment/odh-model-controller --timeout=180s

log "Checking controller logs for xKS mode"
CONTROLLER_LOGS="$(kubectl -n "${NAMESPACE}" logs deployment/odh-model-controller -c manager --tail=200 2>/dev/null || true)"
echo "${CONTROLLER_LOGS}" | grep -q 'xKS mode enabled' \
  || fail "controller did not log xKS mode enabled"

WEBHOOK_COUNT="$(kubectl get mutatingwebhookconfiguration mutating.odh-model-controller.opendatahub.io -o jsonpath='{.webhooks[*].name}' | wc -w)"
[[ "${WEBHOOK_COUNT}" -eq 2 ]] || fail "expected 2 mutating webhooks, got ${WEBHOOK_COUNT}"

ANNOTATION="$(kubectl get mutatingwebhookconfiguration mutating.odh-model-controller.opendatahub.io \
  -o jsonpath='{.metadata.annotations.cert-manager\.io/inject-ca-from}')"
[[ "${ANNOTATION}" == "${NAMESPACE}/odh-model-controller-webhook" ]] \
  || fail "expected cert-manager.io/inject-ca-from=${NAMESPACE}/odh-model-controller-webhook, got '${ANNOTATION}'"

# cert-manager installs its own ValidatingWebhookConfiguration; only OMC's must be absent.
if kubectl get validatingwebhookconfiguration validating.odh-model-controller.opendatahub.io >/dev/null 2>&1; then
  fail "expected no OMC validating webhook on xKS"
fi

log "Creating LLMInferenceService to verify mutating admission path"
cat <<EOF | kubectl apply -f -
apiVersion: serving.kserve.io/v1alpha2
kind: LLMInferenceService
metadata:
  name: xks-smoke-llmisvc
  namespace: ${NAMESPACE}
spec:
  model:
    name: test-model
    uri: s3://test-bucket/test-model
EOF

kubectl -n "${NAMESPACE}" get llminferenceservice xks-smoke-llmisvc >/dev/null 2>&1 \
  || fail "LLMInferenceService was not created; webhook admission may have failed"

log "xKS Kind smoke test passed"
