#!/bin/bash

BASE_IMAGE="registry.access.redhat.com/ubi8/ubi-minimal:8.6"


# Extract metadata from the base image
ARCHITECTURE=$(docker inspect $BASE_IMAGE --format '{{.Architecture}}')
BUILD_DATE=$(docker inspect $BASE_IMAGE --format '{{.Created}}')
COMMIT_SHA=$(docker inspect $BASE_IMAGE --format '{{index .Config.Labels "vcs-ref"}}')
VERSION=$(docker inspect $BASE_IMAGE --format '{{index .Config.Labels "version"}}')

# Generate Dockerfile labels
cat <<EOF > Dockerfile.labels
LABEL architecture="${ARCHITECTURE}" \\
      build-date="${BUILD_DATE}" \\
      com.redhat.build-host="cpt-1008.osbs.prod.upshift.rdu2.redhat.com" \\
      com.redhat.component="ubi8-minimal-container" \\
      com.redhat.license_terms="https://www.redhat.com/en/about/red-hat-end-user-license-agreements#UBI" \\
      description="The Universal Base Image Minimal is a stripped down image that uses microdnf as a package manager. This base image is freely redistributable, but Red Hat only supports Red Hat technologies through subscriptions for Red Hat products. This image is maintained by Red Hat and updated regularly." \\
      distribution-scope="public" \\
      io.k8s.description="The Universal Base Image Minimal is a stripped down image that uses microdnf as a package manager. This base image is freely redistributable, but Red Hat only supports Red Hat technologies through subscriptions for Red Hat products. This image is maintained by Red Hat and updated regularly." \\
      io.k8s.display-name="Red Hat Universal Base Image 8 Minimal" \\
      io.openshift.expose-services="" \\
      io.openshift.tags="minimal rhel8" \\
      maintainer="Red Hat, Inc." \\
      name="ubi8-minimal" \\
      release="902" \\
      summary="Provides the latest release of the minimal Red Hat Universal Base Image 8." \\
      url="https://access.redhat.com/containers/#/registry.access.redhat.com/ubi8-minimal/images/8.6-902" \\
      vcs-ref="${COMMIT_SHA}" \\
      vcs-type="git" \\
      vendor="Red Hat, Inc." \\
      version="${VERSION}" \\
      supported-architectures="amd64, arm64, ppc64le, s390x"
EOF

