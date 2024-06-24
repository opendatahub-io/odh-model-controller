# Build the manager binary
FROM registry.access.redhat.com/ubi9/go-toolset:1.21 as builder

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the go source
COPY main.go main.go
#COPY api/ api/
COPY controllers/ controllers/
COPY controllers/constants/ovms-metrics.json metrics_dashboards/ovms-metrics.json
COPY controllers/constants/tgis-metrics.json metrics_dashboards/tgis-metrics.json
COPY controllers/constants/vllm-metrics.json metrics_dashboards/vllm-metrics.json
COPY controllers/constants/caikit-metrics.json metrics_dashboards/caikit-metrics.json

# Build
USER root
RUN CGO_ENABLED=0 GOOS=linux go build -a -o manager main.go

# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM registry.access.redhat.com/ubi8/ubi-minimal:8.6
WORKDIR /
COPY --from=builder /workspace/manager .
COPY --from=builder /workspace/metrics_dashboards/ovms-metrics.json .
COPY --from=builder /workspace/metrics_dashboards/tgis-metrics.json .
COPY --from=builder /workspace/metrics_dashboards/vllm-metrics.json .
COPY --from=builder /workspace/metrics_dashboards/caikit-metrics.json .
USER 65532:65532

ENTRYPOINT ["/manager"]
