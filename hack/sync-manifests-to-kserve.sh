#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 5 ]]; then
    echo "Usage: $0 <source-config> <destination> <repository> <branch> <revision>" >&2
    exit 2
fi

SOURCE_CONFIG=$1
DESTINATION=$2
SOURCE_REPOSITORY=$3
SOURCE_BRANCH=$4
SOURCE_REVISION=$5

if [[ ! -d "${SOURCE_CONFIG}" ]]; then
    echo "ERROR: Source config directory does not exist: ${SOURCE_CONFIG}" >&2
    exit 1
fi

if [[ ! "${SOURCE_REVISION}" =~ ^[0-9a-f]{40}$ ]]; then
    echo "ERROR: Source revision must be a full Git commit SHA" >&2
    exit 1
fi

if [[ -z "${DESTINATION}" || "${DESTINATION}" == "/" ]]; then
    echo "ERROR: Refusing unsafe destination: ${DESTINATION}" >&2
    exit 1
fi

rm -rf "${DESTINATION}"
mkdir -p "${DESTINATION}"
cp -a "${SOURCE_CONFIG}"/. "${DESTINATION}/"

cat > "${DESTINATION}/.source.yaml" <<EOF
repository: ${SOURCE_REPOSITORY}
branch: ${SOURCE_BRANCH}
revision: ${SOURCE_REVISION}
EOF
