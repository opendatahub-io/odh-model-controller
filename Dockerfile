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
FROM registry.access.redhat.com/ubi8/ubi-minimal:8.6
WORKDIR /
COPY --from=builder /workspace/manager .

# Set labels using build-time arguments
LABEL architecture=$ARCHITECTURE \
      build-date=$BUILD_DATE \
      version=$VERSION \
      com.redhat.build-host=$BUILD_HOST \
      com.redhat.component=$COMPONENT \
      com.redhat.license_terms=$LICENSE_TERMS \
      description=$DESCRIPTION \
      distribution-scope=$DISTRIBUTION_SCOPE \
      io.k8s.description=$K8S_DESCRIPTION \
      io.k8s.display-name=$K8S_DISPLAY_NAME \
      io.openshift.expose-services=$OPENSHIFT_EXPOSE_SERVICES \
      io.openshift.tags=$OPENSHIFT_TAGS \
      maintainer=$MAINTAINER \
      name=$NAME \
      release=$RELEASE \
      summary=$SUMMARY \
      url=$URL \
      vcs-ref=$VCS_REF \
      vcs-type=$VCS_TYPE \
      vendor=$VENDOR

USER 65532:65532

ENTRYPOINT ["/manager"]
