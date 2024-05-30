#!/bin/bash

# Check if the OpenShift CLI tool is installed
if ! command -v oc &> /dev/null; then
    echo "OpenShift CLI (oc) could not be found. Please install it and configure your access to the cluster."
    exit 1
fi

# Log in to the OpenShift cluster if necessary
# oc login --server=<server> --token=<token>

# Total number of projects to delete
TOTAL_PROJECTS=2000

# Project name prefix
PROJECT_PREFIX="test-namespace"

# Number of parallel jobs
PARALLEL_JOBS=20

# Function to delete a project
delete_project() {
    PROJECT_NUMBER=$1
    PROJECT_NAME="${PROJECT_PREFIX}-${PROJECT_NUMBER}"

    echo "Deleting project: ${PROJECT_NAME}"

    oc delete project ${PROJECT_NAME}

    # Check if the project was deleted successfully
    if [ $? -ne 0 ]; then
        echo "Failed to delete project: ${PROJECT_NAME}."
        exit 1
    fi
}

export -f delete_project
export PROJECT_PREFIX

# Generate a list of project numbers and delete projects in parallel
seq 1 $TOTAL_PROJECTS | parallel -j $PARALLEL_JOBS delete_project {}

echo "Successfully deleted ${TOTAL_PROJECTS} projects."
