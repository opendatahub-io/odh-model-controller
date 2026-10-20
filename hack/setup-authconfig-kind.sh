#!/usr/bin/env bash
# Copyright 2026 Red Hat, Inc.
# Licensed under the Apache License, Version 2.0.
set -euo pipefail

project_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
: "${KUBECONFIG:?Set KUBECONFIG to a dedicated Kind kubeconfig}"
: "${KIND_CLUSTER_NAME:=authconfig-operator-e2e}"
readonly istio_version=1.20.8
readonly authorino_operator_version=v0.25.4
readonly kind_context="kind-${KIND_CLUSTER_NAME}"
kube=(kubectl --kubeconfig "$KUBECONFIG" --context "$kind_context")
helm_cmd=(helm --kubeconfig "$KUBECONFIG" --kube-context "$kind_context")

# Never fall back to the user's current OpenShift context.
if [[ $(kubectl --kubeconfig "$KUBECONFIG" config current-context) != "$kind_context" ]]; then
  echo "This setup requires the isolated ${kind_context} context." >&2
  exit 1
fi

"${kube[@]}" create namespace istio-system --dry-run=client -o yaml | "${kube[@]}" apply -f -
"${kube[@]}" create namespace authconfig-e2e --dry-run=client -o yaml | "${kube[@]}" apply -f -
"${helm_cmd[@]}" upgrade --install istio-base base --repo https://istio-release.storage.googleapis.com/charts \
  --namespace istio-system --version "$istio_version" --wait --timeout 180s
"${helm_cmd[@]}" upgrade --install istiod istiod --repo https://istio-release.storage.googleapis.com/charts \
  --namespace istio-system --version "$istio_version" \
  --values "$project_root/test/config/authconfig-e2e/kind-mesh.yaml" \
  --set resources.requests.cpu=100m --set resources.requests.memory=128Mi --wait --timeout 180s
"${helm_cmd[@]}" upgrade --install authconfig-gateway gateway --repo https://istio-release.storage.googleapis.com/charts \
  --namespace authconfig-e2e --version "$istio_version" --set service.type=ClusterIP \
  --set autoscaling.enabled=false --set resources.requests.cpu=10m \
  --set resources.requests.memory=64Mi --wait --timeout 180s

"${kube[@]}" apply --server-side --force-conflicts -f \
  "https://raw.githubusercontent.com/Kuadrant/authorino-operator/${authorino_operator_version}/config/deploy/manifests.yaml"
"${kube[@]}" wait --for=condition=Established \
  crd/authorinos.operator.authorino.kuadrant.io crd/authconfigs.authorino.kuadrant.io --timeout=60s
"${kube[@]}" rollout status deployment/authorino-operator -n authorino-operator --timeout=300s
"${kube[@]}" apply -f "$project_root/test/config/authconfig-e2e/authorino.yaml"
"${kube[@]}" wait authorino/authorino -n authconfig-e2e --for=condition=Ready --timeout=300s
"${kube[@]}" rollout status deployment/authorino -n authconfig-e2e --timeout=300s
"${kube[@]}" apply -n istio-system -f "$project_root/test/config/authconfig-e2e/envoyfilter.yaml"
"${kube[@]}" apply -n istio-system -f "$project_root/test/config/authconfig-e2e/authorizationpolicies.yaml"
"${kube[@]}" apply -f "$project_root/test/config/authconfig-e2e/kind-echo.yaml"
"${kube[@]}" rollout status deployment/echo -n authconfig-e2e --timeout=300s
