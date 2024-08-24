# Define build-time arguments
ARG VERSION
ARG BUILD_DATE
ARG ARCHITECTURE
ARG BUILD_HOST
ARG COMPONENT
ARG LICENSE_TERMS
ARG DESCRIPTION
ARG DISTRIBUTION_SCOPE
ARG K8S_DESCRIPTION
ARG K8S_DISPLAY_NAME
ARG OPENSHIFT_EXPOSE_SERVICES
ARG OPENSHIFT_TAGS
ARG MAINTAINER
ARG NAME
ARG RELEASE
ARG SUMMARY
ARG URL
ARG VCS_REF
ARG VCS_TYPE
ARG VENDOR


# Build the manager binary
FROM registry.access.redhat.com/ubi9/go-toolset:1.21 as builder

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy and include dynamic labels
#COPY Dockerfile.labels /labels
# RUN cat /labels >> /etc/image-info

# Copy the go source
COPY main.go main.go
#COPY api/ api/
COPY controllers/ controllers/

# Build
USER root
#RUN cat /labels >> /etc/image-info
RUN CGO_ENABLED=0 GOOS=linux go build -a -o manager main.go

# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM registry.access.redhat.com/ubi8/ubi-minimal:latest
# FROM docker.io/redhat/ubi8-minimal:latest
WORKDIR /
COPY --from=builder /workspace/manager .

# Install the latest security patches
#RUN yum update -y && \
#    yum install -y \
#    glibc-2.28-251.el8_10.2 \
#    glibc-minimal-langpack-2.28-251.el8_10.2 \
#    glibc-common-2.28-251.el8_10.2 \
#    libnghttp2-1.33.0-6.el8_10.1 \
#    krb5-libs-1.18.2-29.el8_10 \
#    openssl-libs-1.1.1k-12.el8_9 \
#    libksba-1.3.5-9.el8_7 \
#    libcap-2.48-5.el8_8 \
#    libssh-0.9.6-13.el8_9 \
#    libssh-config-0.9.6-14.el8 \
#    systemd-libs-239-82.el8 \
#    gmp-1:6.1.2-11.el8 \
#    libtasn1-4.13-4.el8_7 \
#    rpm-libs-4.14.3-28.el8_9 \
#    ncurses-base-6.1-9.20180224.el8_8.1 \
#    libxml2-2.9.7-18.el8_9 \
#    curl-7.61.1-34.el8_10.2 \
#    sqlite-libs-3.26.0-19.el8_9 \
#    libarchive-3.3.3-5.el8 \
#    gnutls-3.6.16-8.el8_9 \
#    openldap-2.4.46-19.el8_10 && \
#    yum clean all

# Set labels using build-time arguments
LABEL version="${VERSION}" \
      build-date="${BUILD_DATE}" \
      architecture="${ARCHITECTURE}" \
      build-host="${BUILD_HOST}" \
      component="${COMPONENT}" \
      license-terms="${LICENSE_TERMS}" \
      description="${DESCRIPTION}" \
      distribution-scope="${DISTRIBUTION_SCOPE}" \
      io.k8s.description="${K8S_DESCRIPTION}" \
      io.k8s.display-name="${K8S_DISPLAY_NAME}" \
      io.openshift.expose-services="${OPENSHIFT_EXPOSE_SERVICES}" \
      io.openshift.tags="${OPENSHIFT_TAGS}" \
      maintainer="${MAINTAINER}" \
      name="${NAME}" \
      release="${RELEASE}" \
      summary="${SUMMARY}" \
      url="${URL}" \
      vcs-ref="${VCS_REF}" \
      vcs-type="${VCS_TYPE}" \
      vendor="${VENDOR}"


USER 65532:65532

ENTRYPOINT ["/manager"]
