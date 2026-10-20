#!/usr/bin/env bash
# Copyright 2026 Red Hat, Inc.
# Licensed under the Apache License, Version 2.0.
set -euo pipefail

project_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
: "${CONTROL_PLANE_NAME:=basic}"
: "${MESH_NAMESPACE:=istio-system}"
readonly authorino_operator_version=v0.25.4

# KServe's existing Serverless bootstrap creates this control plane and runtime.
oc get smcp "$CONTROL_PLANE_NAME" -n "$MESH_NAMESPACE" >/dev/null
oc get servingruntime kserve-sklearnserver -n kserve-ci-e2e-test >/dev/null

# Upgrade the operator installed by KServe's bootstrap to a pinned release.
oc apply --server-side --force-conflicts -f \
  "https://raw.githubusercontent.com/Kuadrant/authorino-operator/${authorino_operator_version}/config/deploy/manifests.yaml"
oc wait --for=condition=Established \
  crd/authorinos.operator.authorino.kuadrant.io crd/authconfigs.authorino.kuadrant.io --timeout=60s
oc rollout status deployment/authorino-operator -n authorino-operator --timeout=300s

oc create namespace authconfig-e2e --dry-run=client -o yaml | oc apply -f -
oc apply -f - <<EOF
apiVersion: maistra.io/v1
kind: ServiceMeshMember
metadata:
  name: default
  namespace: authconfig-e2e
spec:
  controlPlaneRef:
    namespace: ${MESH_NAMESPACE}
    name: ${CONTROL_PLANE_NAME}
EOF
oc wait servicemeshmember/default -n authconfig-e2e --for=condition=Ready --timeout=180s
oc apply -f "$project_root/test/config/authconfig-e2e/authorino.yaml"
oc wait authorino/authorino -n authconfig-e2e --for=condition=Ready --timeout=300s

oc patch smcp "$CONTROL_PLANE_NAME" -n "$MESH_NAMESPACE" --type=merge --patch-file - <<'EOF'
spec:
  techPreview:
    meshConfig:
      extensionProviders:
        - name: authconfig-e2e-authorino
          envoyExtAuthzGrpc:
            service: authorino-authorino-authorization.authconfig-e2e.svc.cluster.local
            port: 50051
EOF
mesh_generation=$(oc get smcp "$CONTROL_PLANE_NAME" -n "$MESH_NAMESPACE" -o jsonpath='{.metadata.generation}')
oc wait smcp/"$CONTROL_PLANE_NAME" -n "$MESH_NAMESPACE" \
  --for=jsonpath='{.status.observedGeneration}'="$mesh_generation" --timeout=600s
oc wait smcp/"$CONTROL_PLANE_NAME" -n "$MESH_NAMESPACE" --for=condition=Ready --timeout=600s
oc apply -n "$MESH_NAMESPACE" -f "$project_root/test/config/authconfig-e2e/envoyfilter.yaml"
oc apply -n "$MESH_NAMESPACE" -f "$project_root/test/config/authconfig-e2e/authorizationpolicies.yaml"
oc rollout status deployment/authorino -n authconfig-e2e --timeout=300s

# Register AuthConfig ownership watches after enabling mesh authorization.
oc rollout restart deployment/odh-model-controller -n opendatahub
oc rollout status deployment/odh-model-controller -n opendatahub --timeout=300s
