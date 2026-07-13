#!/usr/bin/env bash
# Smoke test: deploy OMC xKS overlay on Kind and verify webhook-only mode.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
CLUSTER_NAME="${CLUSTER_NAME:-odh-model-controller-xks}"
IMAGE="${IMAGE:-odh-model-controller:xks-smoke}"
NAMESPACE="${NAMESPACE:-opendatahub}"
KIND_CONFIG="${KIND_CONFIG:-${ROOT}/test/config/kind-xks-config.yaml}"
CERT_DIR="$(mktemp -d)"
KEEP_CLUSTER="${KEEP_CLUSTER:-false}"

if [[ "${KEEP_CLUSTER}" != "true" ]]; then
  trap 'rm -rf "${CERT_DIR}"; kind delete cluster --name "${CLUSTER_NAME}" >/dev/null 2>&1 || true' EXIT
else
  trap 'rm -rf "${CERT_DIR}"' EXIT
fi

log() { printf '==> %s\n' "$*"; }
fail() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

need() { command -v "$1" >/dev/null 2>&1 || fail "$1 is required"; }
need kind
need docker
need kubectl
need openssl

log "Creating single-node Kind cluster ${CLUSTER_NAME}"
kind delete cluster --name "${CLUSTER_NAME}" >/dev/null 2>&1 || true
kind create cluster --name "${CLUSTER_NAME}" --config "${KIND_CONFIG}"
kubectl wait --for=condition=Ready nodes --all --timeout=180s

log "Building controller image ${IMAGE}"
docker build -t "${IMAGE}" -f "${ROOT}/Containerfile" "${ROOT}"

log "Loading image into Kind"
kind load docker-image "${IMAGE}" --name "${CLUSTER_NAME}"

log "Installing minimal KServe CRDs"
kubectl apply --server-side -f "${ROOT}/config/crd/external/serving.kserve.io_inferenceservices.yaml"
kubectl apply --server-side -f "${ROOT}/test/crds/serving.kserve.io_llminferenceservices.yaml"

log "Generating webhook TLS material"
openssl req -x509 -newkey rsa:2048 \
  -keyout "${CERT_DIR}/tls.key" \
  -out "${CERT_DIR}/tls.crt" \
  -days 1 -nodes \
  -subj "/CN=odh-model-controller-webhook-service.${NAMESPACE}.svc" \
  -addext "subjectAltName=DNS:odh-model-controller-webhook-service.${NAMESPACE}.svc,DNS:odh-model-controller-webhook-service.${NAMESPACE}.svc.cluster.local" \
  >/dev/null 2>&1

CA_BUNDLE="$(base64 -w0 < "${CERT_DIR}/tls.crt")"

log "Ensuring target namespace exists"
kubectl create namespace "${NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f -

log "Rendering and applying xKS overlay"
kubectl kustomize "${ROOT}/config/overlays/xks" \
  | sed -E "s|(image: ).*odh-model-controller[^[:space:]]*|\\1${IMAGE}|" \
  | sed 's|imagePullPolicy: Always|imagePullPolicy: IfNotPresent|' \
  | kubectl apply -f -

kubectl -n "${NAMESPACE}" create secret tls odh-model-controller-webhook-cert \
  --cert="${CERT_DIR}/tls.crt" \
  --key="${CERT_DIR}/tls.key" \
  --dry-run=client -o yaml | kubectl apply -f -

kubectl patch deployment odh-model-controller -n "${NAMESPACE}" --type=json -p='[
  {"op":"replace","path":"/spec/template/spec/containers/0/args","value":["--health-probe-bind-address=:8081"]},
  {"op":"replace","path":"/spec/template/spec/containers/0/livenessProbe/initialDelaySeconds","value":30},
  {"op":"replace","path":"/spec/template/spec/containers/0/readinessProbe/initialDelaySeconds","value":15}
]'

log "Patching mutating webhook CA bundle"
kubectl patch mutatingwebhookconfiguration mutating.odh-model-controller.opendatahub.io \
  --type='json' \
  -p="[{\"op\":\"add\",\"path\":\"/webhooks/0/clientConfig/caBundle\",\"value\":\"${CA_BUNDLE}\"},{\"op\":\"add\",\"path\":\"/webhooks/1/clientConfig/caBundle\",\"value\":\"${CA_BUNDLE}\"},{\"op\":\"add\",\"path\":\"/webhooks/2/clientConfig/caBundle\",\"value\":\"${CA_BUNDLE}\"}]"

log "Waiting for controller deployment"
kubectl -n "${NAMESPACE}" rollout status deployment/odh-model-controller --timeout=180s

log "Checking controller logs for xKS mode"
CONTROLLER_LOGS="$(kubectl -n "${NAMESPACE}" logs deployment/odh-model-controller -c manager --tail=200 2>/dev/null || true)"
echo "${CONTROLLER_LOGS}" | grep -q 'xKS mode enabled' \
  || fail "controller did not log xKS mode enabled"

WEBHOOK_COUNT="$(kubectl get mutatingwebhookconfiguration mutating.odh-model-controller.opendatahub.io -o jsonpath='{.webhooks[*].name}' | wc -w)"
[[ "${WEBHOOK_COUNT}" -eq 3 ]] || fail "expected 3 mutating webhooks, got ${WEBHOOK_COUNT}"

if [[ -n "$(kubectl get validatingwebhookconfiguration -o name 2>/dev/null || true)" ]]; then
  fail "expected no validating webhooks on xKS"
fi

log "Creating InferenceService to verify mutating admission path"
cat <<EOF | kubectl apply -f -
apiVersion: serving.kserve.io/v1beta1
kind: InferenceService
metadata:
  name: xks-smoke-isvc
  namespace: ${NAMESPACE}
spec:
  predictor:
    model:
      modelFormat:
        name: sklearn
      storageUri: "s3://test-bucket/model"
EOF

kubectl -n "${NAMESPACE}" get inferenceservice xks-smoke-isvc >/dev/null 2>&1 \
  || fail "InferenceService was not created; webhook admission may have failed"

log "xKS Kind smoke test passed"
