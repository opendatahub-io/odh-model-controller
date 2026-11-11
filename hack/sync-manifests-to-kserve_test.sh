#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")" && pwd)"
TMP_DIR="$(mktemp -d -t "sync-manifests-test.XXXXXXXXXX")"
trap 'rm -rf -- "$TMP_DIR"' EXIT

SOURCE_CONFIG="${TMP_DIR}/config"
DESTINATION="${TMP_DIR}/modelcontroller"
REVISION=0123456789abcdef0123456789abcdef01234567

mkdir -p "${SOURCE_CONFIG}/server" "${DESTINATION}"
printf 'current\n' > "${SOURCE_CONFIG}/server/clusterrole.yaml"
printf 'stale\n' > "${DESTINATION}/stale.yaml"

bash "${SCRIPT_DIR}/sync-manifests-to-kserve.sh" \
    "${SOURCE_CONFIG}" \
    "${DESTINATION}" \
    "https://github.com/opendatahub-io/odh-model-controller" \
    main \
    "${REVISION}"

test -f "${DESTINATION}/server/clusterrole.yaml"
test ! -e "${DESTINATION}/stale.yaml"
grep -qx "revision: ${REVISION}" "${DESTINATION}/.source.yaml"

if bash "${SCRIPT_DIR}/sync-manifests-to-kserve.sh" \
    "${SOURCE_CONFIG}" "${DESTINATION}" repository main short-sha; then
    echo "ERROR: Short revision was accepted" >&2
    exit 1
fi

if bash "${SCRIPT_DIR}/sync-manifests-to-kserve.sh" \
    "${SOURCE_CONFIG}" "" repository main "${REVISION}"; then
    echo "ERROR: Empty destination was accepted" >&2
    exit 1
fi

if bash "${SCRIPT_DIR}/sync-manifests-to-kserve.sh" \
    "${TMP_DIR}/missing" "${DESTINATION}" repository main "${REVISION}"; then
    echo "ERROR: Missing source directory was accepted" >&2
    exit 1
fi

echo "Manifest sync test passed."
