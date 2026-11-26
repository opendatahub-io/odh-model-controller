# Konflux build validation

This repository uses `.github/workflows/pr-build-validation.yml` and
`scripts/validate-build.sh` to simulate the two Konflux image builds and the
manifest contracts the KServe module depends on. The workflow is intentionally
separate from the existing `Build images` workflow, which only builds the two
containers, and from `Validation`, which only renders Kustomize output.

The simulator covers build inputs, image runtime contracts, rendered RBAC and
service wiring, a KinD xKS smoke test, the pinned downstream variant, and
generated-artifact drift. It does not replace Konflux, the platform operator,
or the KServe module e2e suite.

## Build variants

Konflux builds two images from this repository. Both use the shared
`opendatahub-io/odh-konflux-central` pipeline
`pipeline/multi-arch-container-build.yaml` at `main`.

| Artifact | Pull-request PipelineRun | Dockerfile | Context | PR output | Push output |
|---|---|---|---|---|---|
| Controller | `.tekton/odh-model-controller-pull-request.yaml` | `Containerfile` | `.` | `quay.io/opendatahub/odh-model-controller:odh-pr` | `quay.io/opendatahub/odh-model-controller:odh-incubating` |
| Model serving API | `.tekton/odh-model-serving-api-ci-on-pull-request.yaml` | `Containerfile.server` | `.` | `quay.io/opendatahub/odh-model-serving-api:odh-pr` | `quay.io/opendatahub/odh-model-serving-api:odh-incubating` |

The simulator also records these upstream warnings rather than silently
assuming downstream behavior:

- Upstream PipelineRuns do not set `build-platforms`, so the shared pipeline
  default (`linux/x86_64`) applies.
- Upstream builds are non-hermetic and do not declare a `gomod` prefetch.
- `Containerfile.server` uses a floating `ubi-minimal:latest` runtime and
  installs RPMs during the build.
- The upstream controller build disables CGO; the downstream controller variant
  supplies FIPS flags.

The pinned downstream variant is
`red-hat-data-services/odh-model-controller@5ea9b19b7abecf3e9ec03e7395f8fc3aeb2fc559`.
Its contract is checked, but the downstream image itself is not built here:

- `Dockerfile.konflux` and `Containerfile.server.konflux`
- the final `ENTRYPOINT` traced through `COPY --from` to exactly one
  `go build -o` output, which must resolve `CGO_ENABLED=1` with
  `strictfipsruntime`
- digest-pinned `ubi-minimal-pqc:9.8` runtime
- hermetic `gomod` prefetch
- `linux/x86_64` and `arm64`
- both `config/overlays/odh` and `config/overlays/xks`
- both image keys in `config/base/params.env`

## What runs

| Mode | What it proves |
|---|---|
| `go-version` | Both Containerfiles use the same Go minor, and that minor matches `go.mod`, before the mode prints a version. |
| `static` | Tekton targets and image names agree with `params.env`; Containerfiles have exact final user IDs, entrypoints, COPY sources, and local import coverage; CRD revisions and overlay paths are present; base replacements target real Deployments. |
| `go-modules` | `go mod download`, `go mod verify`, and `go mod tidy` are clean. |
| `hermetic` | Both binaries compile inside the digest-pinned UBI Go builder with `--network none`. The module cache is populated once in a temporary copy, then the offline controller and FIPS server builds must succeed without changing `go.mod` or `go.sum`. |
| `manifests` | Renders `config/base`, `config/overlays/odh`, `config/overlays/xks`, and `config/default`; checks image injection, RBAC identity resolution, the OpenShift `apiservers` permission and `resourceNames`, webhook Service existence, ports, and selectors against rendered workload Pod labels, probes and service target ports, service-monitor TLS, manager flag spellings, CRD schema envelopes and storage versions, and kubeconform schemas. |
| `controller` | Builds the actual `Containerfile`, checks the declared UID is `65532`, executes `--help`, and verifies the metrics and health-probe flags. |
| `server` | Builds the actual `Containerfile.server`, checks UID `1000`, runs the binary with generated TLS material, verifies HTTPS `/healthz`, `/readyz`, embedded samples, unauthenticated gateway rejection, HTTPS metrics, five-second stability, startup logs, and fail-closed behavior without `TLS_CERT_DIR`. |
| `kind` | Deploys the xKS overlay with cert-manager and the KServe CRD, checks two mutating webhooks, CA injection, controller xKS logs, admission rejection paths, and an `LLMInferenceService` create. `SKIP_BUILD` defaults to `true` here so prebuilt images are used unless explicitly set to `false`. |
| `downstream` | Fetches the pinned downstream commit and traces each final-stage `ENTRYPOINT` through `COPY --from` (shell and JSON forms, `WORKDIR`-relative destinations) to the single `go build -o` that produced it, then checks that build's FIPS flags. Also checks the final runtime stage and digest (including stage aliases), PipelineRuns, overlays, and image keys. |
| `generated-drift` | Regenerates CRDs, RBAC, webhook manifests, and deepcopy code from `HEAD` and the base ref, labelling each snapshot by role. New drift introduced by the PR fails; drift already present on the base ref warns. Generator failure is always a failure. |
| `all` | Runs `static`, `go-modules`, `hermetic`, `manifests`, `controller`, and `server`. Use the individual modes for `kind`, `downstream`, and `generated-drift`. |

The manifest render is the KServe module contract: the module renders
`modelcontroller/overlays/odh` and `modelcontroller/overlays/xks`. The platform
operator injects `RELATED_IMAGE_ODH_MODEL_CONTROLLER_IMAGE` and
`RELATED_IMAGE_ODH_MODEL_SERVING_API_IMAGE` from `config/base/params.env`.
These repository overlays are the source paths that must stay compatible with
that module.

## Skipping and failure semantics

The workflow runs for pull requests targeting `incubating`, `main`, and
`stable-*`, plus manual dispatch. It skips only when:

- the PR title contains `[skip konflux-sim]`, or
- the `skip-konflux-sim` label is present, or
- no path in the workflow's relevant-path expression changed.

Label and title edits re-evaluate the skip decision. A new commit supersedes an
in-flight run; a label-only event does not cancel the existing build.

The summary job is fail-closed:

- If skip evaluation fails, the summary fails.
- On an intentional skip, every validation job must report `skipped`.
- On a normal run, every expected job must report `success`; a skipped job is a
  failure, not a pass.

## Local usage

Prerequisites: Bash, Go with the version from `go.mod`, Python 3 with PyYAML,
`jq`, Docker, `kubeconform`, and either standalone `kustomize` or
`kubectl kustomize`. `kind`, `kubectl`, and `openssl` are needed for the
runtime checks. `SKIP_SCHEMA=true` is an explicit local opt-out from kubeconform.

```bash
./scripts/validate-build.sh static
./scripts/validate-build.sh go-modules
./scripts/validate-build.sh hermetic
./scripts/validate-build.sh manifests
./scripts/validate-build.sh controller
./scripts/validate-build.sh server
```

`PLATFORM` defaults to `linux/amd64` to match the Konflux build target. On an
Apple Silicon host, Docker can exercise the amd64 server image, but a KinD node
is arm64. Use native images for the KinD check:

```bash
PLATFORM=linux/arm64 \
  CONTROLLER_IMAGE=quay.io/opendatahub/odh-model-controller:local-arm64 \
  ./scripts/validate-build.sh controller
PLATFORM=linux/arm64 \
  SERVER_IMAGE=quay.io/opendatahub/odh-model-serving-api:local-arm64 \
  ./scripts/validate-build.sh server
PLATFORM=linux/arm64 \
  CONTROLLER_IMAGE=quay.io/opendatahub/odh-model-controller:local-arm64 \
  KIND_SERVER_IMAGE=quay.io/opendatahub/odh-model-serving-api:local-arm64 \
  ./scripts/validate-build.sh kind
```

`KIND_SERVER_IMAGE` makes the smoke test replace and load the model-serving-api
image instead of pulling the `params.env` image. Without it, the smoke test
keeps the overlay's published image. Set `KEEP_CLUSTER=true` to retain the KinD
cluster for debugging.

Use the same tool versions as CI when validating manifest changes:

```bash
KUSTOMIZE_BIN=/path/to/kustomize \
KUBECONFORM_BIN=/path/to/kubeconform \
  ./scripts/validate-build.sh manifests
```

The workflow verifies kustomize `5.7.1` and kubeconform `0.7.0` against pinned
SHA-256 digests before using them. `SKIP_SCHEMA=true` is a local convenience
only; it is not used by CI.

For generated-artifact drift:

```bash
GOBIN=/tmp/omc-bin go install \
  sigs.k8s.io/controller-tools/cmd/controller-gen@v0.16.4
CONTROLLER_GEN=/tmp/omc-bin/controller-gen \
BASE_REF=origin/incubating \
  ./scripts/validate-build.sh generated-drift
```

## Evidence

The checks are designed from failure modes reported against this component and
the KServe module.

| Ticket | Failure | Coverage |
|---|---|---|
| RHOAIENG-55878 / RHOAIENG-55730 | `model-serving-api` rendered with placeholder `controller:latest` | `static` image/tag contract and rendered-image checks |
| RHOAIENG-89454 | Missing packaged `modelcontroller` `params.env` blocked model deployment | Overlay path and `params.env` contract; external operator packaging is out of reach |
| RHOAIENG-55188 | Missing `/opt/manifests/wva` directory caused DSC NotReady | Not caught; external image packaging |
| RHOAIENG-82376 | Missing RBAC on `apiservers.config.openshift.io` blocked KserveReady | `manifests` explicit RBAC assertion |
| RHOAIENG-58209 | Metrics port mismatch against NetworkPolicy | Service target-port, probe, and ServiceMonitor checks |
| RHOAIENG-57257 | Image tag instead of digest broke disconnected installs | Downstream PQC digest/base and image-key contract; release digest injection is external |
| RHOAIENG-101182 | Hardcoded DSC v1 GVK broke on a newer platform | Not caught; platform operator/provider integration |
| RHOAIENG-55047 | Invalid `LLMInferenceServiceConfig` CRD | Overlay render and CRD structural/schema checks; upstream CRD validity only partly covered |
| RHOAIENG-18974 | Controller restart after CRD availability | KinD startup and rollout checks; full timing race not covered |
| RHOAIENG-102401 | Local-model controller crash left DSC NotReady | Not caught; operator reconcile and e2e |
| RHOAIENG-82074 | Finalizers blocked namespace uninstall | Not caught; operator/e2e |
| RHOAIENG-94842 | Authorino TLS race | Not caught; live reconcile behavior |

The `static`, `manifests`, `downstream`, and runtime checks were fault-injected
during onboarding: a missing COPY source, a wrong `params.env` tag, removal of
the `apiservers` RBAC rule, a wrong model-serving-api service target port, a
webhook Service selector that matches no workload Pod labels, and a downstream
binary copied from a non-FIPS `go build -o` while a separate FIPS build sat
unused all failed the expected gate and were restored afterward.

## Out of scope and handoff

This repository has no operator of its own. The KServe module owns the
`odh`/`xks` render and the platform operator owns `DataScienceCluster` and
`DSCInitialization` reconciliation. Therefore the simulator does not assert:

- `DataScienceCluster` or `DSCInitialization` conditions
- platform-operator image injection or upgrade behavior
- KServe module operator e2e
- disconnected upgrade and mirroring
- multi-architecture execution (only the downstream build contract is checked)
- full controller/webhook reconcile behavior on a live OpenShift cluster
- external manifest packaging, finalizer cleanup, ModelCache crashes, or the
  Authorino TLS race

Those belong to the KServe module and platform-operator onboarding. The KinD
test is an xKS startup and admission-path smoke test, not a substitute for
those suites.
