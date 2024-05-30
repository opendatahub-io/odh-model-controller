#!/bin/bash

# Check if the OpenShift CLI tool is installed
if ! command -v oc &> /dev/null; then
    echo "OpenShift CLI (oc) could not be found. Please install it and configure your access to the cluster."
    exit 1
fi

# Log in to the OpenShift cluster if necessary
# oc login --server=<server> --token=<token>

# Total number of namespaces to create
TOTAL_NAMESPACES=2000

# Namespace name prefix
NAMESPACE_PREFIX="test-namespace"

# Number of parallel jobs
PARALLEL_JOBS=20

# Number of secrets and configmaps to create
NUM_SECRETS=20
NUM_CONFIGMAPS=10

# Function to create namespace, secrets, and configmaps
create_namespace_and_resources() {
    NAMESPACE_NUMBER=$1
    NAMESPACE_NAME="${NAMESPACE_PREFIX}-${NAMESPACE_NUMBER}"

#    echo "Creating namespace: ${NAMESPACE_NAME}"
#    oc new-project ${NAMESPACE_NAME} --skip-config-write=true
#
#    if [ $? -ne 0 ]; then
#        echo "Failed to create namespace: ${NAMESPACE_NAME}"
#        exit 1
#    fi

    echo "Creating resources in namespace: ${NAMESPACE_NAME}"

    for ((i=1; i<=NUM_SECRETS; i++)); do
        oc create secret generic "secret-big-${i}" --from-file=1mbfile --namespace=${NAMESPACE_NAME} || { echo "Failed to create secret-${i} in ${NAMESPACE_NAME}"; exit 1; }
    done

#    for ((i=1; i<=NUM_CONFIGMAPS; i++)); do
#        oc create configmap "configmap-${i}" --namespace=${NAMESPACE_NAME} || { echo "Failed to create configmap-${i} in ${NAMESPACE_NAME}"; exit 1; }
#    done

    echo "Namespace and resources created: ${NAMESPACE_NAME}"
}

export -f create_namespace_and_resources
export NAMESPACE_PREFIX
export NUM_SECRETS
export NUM_CONFIGMAPS

# Generate namespace numbers and create namespaces and resources in parallel
seq 1 $TOTAL_NAMESPACES | parallel -j $PARALLEL_JOBS create_namespace_and_resources {}

echo "Successfully created namespaces and resources in ${TOTAL_NAMESPACES} namespaces."
