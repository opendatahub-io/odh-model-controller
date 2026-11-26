#!/usr/bin/env bash
set -euo pipefail

# Konflux build simulator for opendatahub-io/odh-model-controller.
#
# Konflux builds two images from this repository:
#   .tekton/odh-model-controller-pull-request.yaml          -> Containerfile
#   .tekton/odh-model-serving-api-ci-on-pull-request.yaml   -> Containerfile.server
#
# The simulator builds both images, checks the runtime contracts that the
# existing GitHub build workflow does not, and validates the manifests the
# KServe module renders in production. It does not deploy the KServe module or
# assert DataScienceCluster conditions; that belongs to the KServe onboarding.
#
# Usage:
#   scripts/validate-build.sh <go-version|static|go-modules|hermetic|manifests|controller|server|kind|downstream|generated-drift|all>
#
# Environment:
#   CONTROLLER_IMAGE       Local controller image tag
#   SERVER_IMAGE           Local server image tag
#   PLATFORM               Docker platform (default linux/amd64)
#   SERVER_HTTPS_PORT      Host port for the server HTTPS test (default 18443)
#   SERVER_METRICS_PORT    Host port for the server metrics test (default 18080)
#   KUSTOMIZE_BIN          kustomize binary; falls back to kubectl kustomize
#   KUBECONFORM_BIN        kubeconform binary (default kubeconform)
#   KUBERNETES_VERSION     Kubernetes schema version (default 1.33.0)
#   SKIP_SCHEMA=true       Skip kubeconform (local convenience only)
#   BASE_REF               Ref used for generated-artifact drift (default origin/incubating)
#   CONTROLLER_GEN         Prebuilt controller-gen binary for drift checks
#   DOWNSTREAM_REPO        Downstream git URL
#   DOWNSTREAM_REF         Pinned downstream commit
#   KEEP_CLUSTER=true      Keep the KinD cluster open for debugging
#   SKIP_BUILD=false       Build images inside the KinD smoke test (defaults to true)
#   KIND_SERVER_IMAGE      Optional prebuilt model-serving-api image to load
#                          into KinD instead of pulling the params.env image

MODE="${1:-all}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

# Host-side checks may switch to the go.mod toolchain. The hermetic check below
# explicitly pins GOTOOLCHAIN=local inside the UBI builder container.
export GOTOOLCHAIN="${GOTOOLCHAIN:-auto}"

CONTROLLER_IMAGE="${CONTROLLER_IMAGE:-quay.io/opendatahub/odh-model-controller:pr-validation}"
SERVER_IMAGE="${SERVER_IMAGE:-quay.io/opendatahub/odh-model-serving-api:pr-validation}"
PLATFORM="${PLATFORM:-linux/amd64}"
SERVER_HTTPS_PORT="${SERVER_HTTPS_PORT:-18443}"
SERVER_METRICS_PORT="${SERVER_METRICS_PORT:-18080}"
KUSTOMIZE_BIN="${KUSTOMIZE_BIN:-}"
KUBECONFORM_BIN="${KUBECONFORM_BIN:-kubeconform}"
KUBERNETES_VERSION="${KUBERNETES_VERSION:-1.33.0}"
SKIP_SCHEMA="${SKIP_SCHEMA:-false}"
BASE_REF="${BASE_REF:-origin/incubating}"
CONTROLLER_GEN="${CONTROLLER_GEN:-}"
DOWNSTREAM_REPO="${DOWNSTREAM_REPO:-https://github.com/red-hat-data-services/odh-model-controller.git}"
DOWNSTREAM_REF="${DOWNSTREAM_REF:-5ea9b19b7abecf3e9ec03e7395f8fc3aeb2fc559}"

info() { printf 'ℹ️  %s\n' "$*"; }
pass() { printf '✅ %s\n' "$*"; }
warn() { printf '⚠️  %s\n' "$*"; }
err() { printf '❌ %s\n' "$*" >&2; }

require_cmd() {
  local cmd="$1"
  if ! command -v "$cmd" >/dev/null 2>&1; then
    err "Required command not found: $cmd"
    return 1
  fi
}

dockerfile_go_version() {
  local dockerfile="${1:-Containerfile}" image tag
  image=$(awk '
    tolower($1) == "from" {
      for (i = 2; i <= NF; i++) {
        if ($i ~ /^--/) continue
        if ($i ~ /(^|\/)(go-toolset|golang):[0-9]+\.[0-9]+/) { print $i; exit }
      }
    }' "$dockerfile")
  if [ -z "$image" ]; then
    err "$dockerfile: could not find a golang/go-toolset builder image"
    return 1
  fi
  image="${image%%@*}"
  tag="${image##*:}"
  if [[ ! "$tag" =~ ^([0-9]+\.[0-9]+) ]]; then
    err "$dockerfile: could not parse a Go version from $image"
    return 1
  fi
  printf '%s\n' "${BASH_REMATCH[1]}"
}

dockerfile_builder_image() {
  local dockerfile="${1:-Containerfile}" image
  image=$(awk '
    tolower($1) == "from" {
      for (i = 2; i <= NF; i++) {
        if ($i ~ /^--/) continue
        if ($i ~ /(^|\/)(go-toolset|golang):[0-9]+\.[0-9]+/) { print $i; exit }
      }
    }' "$dockerfile")
  if [ -z "$image" ]; then
    err "$dockerfile: could not find a golang/go-toolset builder image"
    return 1
  fi
  printf '%s\n' "$image"
}

go_mod_go_minor() {
  local version
  version=$(awk '$1 == "go" { print $2; exit }' go.mod)
  if [[ ! "$version" =~ ^([0-9]+\.[0-9]+) ]]; then
    err "go.mod: could not parse a Go minor from '${version:-missing}'"
    return 1
  fi
  printf '%s\n' "${BASH_REMATCH[1]}"
}

kustomize_build() {
  local dir="$1" out="$2"
  if [ -n "$KUSTOMIZE_BIN" ]; then
    "$KUSTOMIZE_BIN" build --load-restrictor LoadRestrictionsNone "$dir" > "$out"
  elif command -v kustomize >/dev/null 2>&1; then
    kustomize build --load-restrictor LoadRestrictionsNone "$dir" > "$out"
  elif command -v kubectl >/dev/null 2>&1; then
    kubectl kustomize "$dir" > "$out"
  else
    err "kustomize is required; install it or set KUSTOMIZE_BIN"
    return 1
  fi
}

run_static_checks() {
  require_cmd python3
  python3 - "$REPO_ROOT" <<'PY'
import json
import pathlib
import re
import shlex
import sys

import yaml

root = pathlib.Path(sys.argv[1])
issues = []
warnings = []


def fail(message):
    issues.append(message)
    print(f"❌ {message}")


def warn(message):
    warnings.append(message)
    print(f"⚠️  {message}")


def ok(message):
    print(f"✅ {message}")


def read(path):
    return pathlib.Path(path).read_text()


def load_yaml(path):
    with open(path, encoding="utf-8") as handle:
        return yaml.safe_load(handle)


def params_env(path):
    values = {}
    for raw in read(path).splitlines():
        line = raw.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        values[key] = value
    return values


def image_repo_tag(image):
    repo, tag = image.rsplit(":", 1)
    return repo, tag


def tekton_params(path):
    document = load_yaml(path)
    return {item["name"]: item.get("value") for item in document["spec"]["params"]}


def parse_froms(text):
    images = []
    for line in text.splitlines():
        match = re.match(r"^\s*FROM\s+(.+)$", line, re.I)
        if not match:
            continue
        tokens = [token for token in match.group(1).split() if not token.startswith("--")]
        if tokens:
            images.append(tokens[0])
    return images


def builder_version(image):
    image = image.split("@", 1)[0]
    tag = image.rsplit(":", 1)[-1]
    match = re.match(r"^([0-9]+\.[0-9]+)", tag)
    return match.group(1) if match else None


def copy_sources(text):
    sources = []
    for line in text.splitlines():
        match = re.match(r"^\s*COPY\s+(.+)$", line.strip(), re.I)
        if not match or "--from=" in match.group(1):
            continue
        argument = match.group(1).strip()
        while argument.startswith("--"):
            _, _, argument = argument.partition(" ")
            argument = argument.strip()
        if argument.startswith("["):
            try:
                tokens = json.loads(argument)
            except json.JSONDecodeError:
                fail("COPY has invalid JSON-form arguments")
                continue
            if not isinstance(tokens, list) or not all(isinstance(token, str) for token in tokens):
                fail("COPY JSON-form arguments must be a string array")
                continue
        else:
            tokens = shlex.split(argument)
        tokens = [token for token in tokens if not token.startswith("--")]
        if len(tokens) >= 2:
            sources.extend(tokens[:-1])
    return sources


def import_paths(path):
    text = read(path)
    paths = []
    for block in re.findall(r"(?ms)^\s*import\s*\((.*?)^\s*\)", text):
        paths.extend(re.findall(r'^\s*(?:[\w.]+\s+)?"([^"]+)"', block, re.M))
    for single in re.findall(r'(?m)^\s*import\s+(?:[\w.]+\s+)?"([^"]+)"', text):
        paths.append(single)
    return paths


def copied_prefixes(text):
    prefixes = []
    for source in copy_sources(text):
        cleaned = source[2:] if source.startswith("./") else source
        if cleaned.endswith("/"):
            cleaned = cleaned[:-1]
        prefixes.append(cleaned)
    return prefixes


def covered_by_copy(import_path, prefixes):
    return any(
        prefix == "."
        or import_path == prefix
        or import_path.startswith(prefix + "/")
        for prefix in prefixes
    )


def check_dockerfile(label, dockerfile, expected_user, require_fips):
    text = read(root / dockerfile)
    images = parse_froms(text)
    if len(images) < 2:
        fail(f"{dockerfile}: expected a builder and a runtime stage")
        return None

    builder = next((image for image in images if "go-toolset" in image or "golang" in image), None)
    if not builder:
        fail(f"{dockerfile}: no Go builder image")
        return None
    version = builder_version(builder)
    if not version:
        fail(f"{dockerfile}: could not parse the builder Go version from {builder}")

    runtime = images[-1]
    if runtime.endswith(":latest"):
        warn(f"{dockerfile}: runtime base {runtime} is floating; downstream uses a digest-pinned PQC base")
    if "microdnf" in text or "dnf install" in text:
        warn(f"{dockerfile}: installs RPMs during the build; upstream Konflux is non-hermetic, downstream must prefetch")

    if require_fips:
        if "CGO_ENABLED=1" not in text:
            fail(f"{dockerfile}: server build must set CGO_ENABLED=1")
        if "strictfipsruntime" not in text:
            fail(f"{dockerfile}: server build must set GOEXPERIMENT/strictfipsruntime")
    else:
        if "CGO_ENABLED=0" in text:
            warn(f"{dockerfile}: CGO is disabled upstream; the downstream Dockerfile.konflux must supply FIPS flags")
        if "strictfipsruntime" not in text:
            warn(f"{dockerfile}: upstream build does not use strictfipsruntime; downstream Dockerfile.konflux must")

    user_lines = [line.strip() for line in text.splitlines() if line.strip().upper().startswith("USER ")]
    if not user_lines:
        fail(f"{dockerfile}: no USER instruction")
    else:
        fields = shlex.split(user_lines[-1])
        user_spec = fields[1] if len(fields) > 1 else ""
        user = user_spec.split(":", 1)[0]
        if user != expected_user:
            fail(f"{dockerfile}: final USER is '{user_lines[-1]}', expected UID {expected_user}")
        else:
            ok(f"{dockerfile}: runtime USER is exactly {expected_user}")

    if not re.search(r"(?mi)^\s*ENTRYPOINT\s+", text):
        fail(f"{dockerfile}: no ENTRYPOINT")

    prefix_list = copied_prefixes(text)
    for source in copy_sources(text):
        cleaned = source[2:] if source.startswith("./") else source
        cleaned = cleaned.rstrip("/")
        if not (root / cleaned).exists():
            fail(f"{dockerfile}: COPY source does not exist: {source}")

    return version, prefix_list


def check_local_imports(label, entrypoints, dockerfile, prefixes):
    module_match = re.search(r"(?m)^module\s+(\S+)", read(root / "go.mod"))
    if not module_match:
        fail("go.mod: missing module directive")
        return
    module = module_match.group(1)
    missing = set()
    for entrypoint in entrypoints:
        for imported in import_paths(root / entrypoint):
            if not imported.startswith(module + "/"):
                continue
            relative = imported[len(module) + 1 :]
            if not covered_by_copy(relative, prefixes):
                missing.add((entrypoint, imported))
    if missing:
        for entrypoint, imported in sorted(missing):
            fail(f"{dockerfile}: {entrypoint} imports {imported}, but no COPY covers it")
    else:
        ok(f"{dockerfile}: COPY instructions cover every local import of {label}")


print("ℹ️  Checking Konflux build targets and image contracts...")
tekton_dir = root / ".tekton"
pr_files = sorted(tekton_dir.glob("*pull-request*.yaml"))
push_files = sorted(tekton_dir.glob("*push*.yaml"))

if len(pr_files) != 2:
    fail(f"expected 2 Konflux pull-request PipelineRuns, found {len(pr_files)}")
if len(push_files) != 2:
    fail(f"expected 2 Konflux push PipelineRuns, found {len(push_files)}")

pr_targets = {}
push_targets = {}
for path in pr_files + push_files:
    params = tekton_params(path)
    dockerfile = params.get("dockerfile")
    context = params.get("path-context", ".")
    output_image = params.get("output-image")
    if not dockerfile or not output_image:
        fail(f"{path.name}: missing dockerfile or output-image")
        continue
    if context != ".":
        fail(f"{path.name}: path-context is {context!r}; this simulator assumes the repository root")
    target_path = root / context / dockerfile
    if not target_path.is_file():
        fail(f"{path.name}: dockerfile not found at {target_path.relative_to(root)}")
    target = {
        "file": path.name,
        "dockerfile": dockerfile,
        "context": context,
        "output_image": output_image,
        "repo": image_repo_tag(output_image)[0],
        "tag": image_repo_tag(output_image)[1],
        "params": params,
    }
    if "pull-request" in path.name:
        pr_targets.setdefault(dockerfile, []).append(target)
    else:
        push_targets.setdefault(dockerfile, []).append(target)

for dockerfile in ("Containerfile", "Containerfile.server"):
    if len(pr_targets.get(dockerfile, [])) != 1:
        fail(f"expected exactly one PR PipelineRun for {dockerfile}")
    if len(push_targets.get(dockerfile, [])) != 1:
        fail(f"expected exactly one push PipelineRun for {dockerfile}")

params = params_env(root / "config/base/params.env")
for key, dockerfile in (("odh-model-controller", "Containerfile"), ("odh-model-serving-api", "Containerfile.server")):
    if key not in params:
        fail(f"config/base/params.env: missing {key}")
        continue
    param_repo, param_tag = image_repo_tag(params[key])
    if param_repo != pr_targets[dockerfile][0]["repo"]:
        fail(f"{key} points at {param_repo}, but Konflux builds {pr_targets[dockerfile][0]['repo']}")
    push_repo, push_tag = image_repo_tag(push_targets[dockerfile][0]["output_image"])
    if (param_repo, param_tag) != (push_repo, push_tag):
        fail(
            f"{key} points at {param_repo}:{param_tag}, but the push PipelineRun "
            f"publishes {push_repo}:{push_tag}"
        )
    else:
        ok(f"{key} matches the push image repository and tag")

for dockerfile in ("Containerfile", "Containerfile.server"):
    for target in pr_targets.get(dockerfile, []):
        if "build-platforms" not in target["params"]:
            warn(f"{target['file']}: no build-platforms; the shared pipeline defaults to linux/x86_64 only")
        hermetic = str(target["params"].get("hermetic", "false")).lower()
        if hermetic != "true":
            warn(f"{target['file']}: hermetic is not enabled; downstream builds are hermetic and use gomod prefetch")
        if "hermetic" not in target["params"]:
            warn(f"{target['file']}: no prefetch-input; this is expected only while upstream stays non-hermetic")

print("ℹ️  Checking Containerfiles...")
controller_version, controller_prefixes = check_dockerfile("controller", "Containerfile", "65532", False)
server_version, server_prefixes = check_dockerfile("server", "Containerfile.server", "1000", True)
if controller_version and server_version and controller_version != server_version:
    fail(f"Containerfile and Containerfile.server use different Go builders: {controller_version} vs {server_version}")

go_mod = read(root / "go.mod")
go_version_match = re.search(r"(?m)^go\s+([0-9]+\.[0-9]+(?:\.[0-9]+)?)", go_mod)
if not go_version_match:
    fail("go.mod: missing go directive")
else:
    go_mod_version = go_version_match.group(1)
    go_mod_minor = ".".join(go_mod_version.split(".")[:2])
    if controller_version and go_mod_minor != controller_version:
        fail(f"go.mod requires Go {go_mod_minor}, but the builder image is {controller_version}")
    else:
        ok(f"go.mod Go {go_mod_minor} matches the builder image")

if not (root / "go.sum").is_file():
    fail("go.sum is missing")

for line in go_mod.splitlines():
    if not line.startswith("replace "):
        continue
    left, _, right = line.partition("=>")
    right = right.strip()
    if right.startswith("./") or right.startswith("../"):
        target = (root / right.split()[0]).resolve()
        if not target.exists():
            fail(f"go.mod local replace target does not exist: {right}")
        else:
            ok(f"go.mod local replace target exists: {right}")

if controller_prefixes:
    check_local_imports("cmd/main.go", ["cmd/main.go"], "Containerfile", controller_prefixes)
if server_prefixes:
    server_entries = sorted(
        str(path.relative_to(root))
        for path in (root / "server").rglob("*.go")
        if not path.name.endswith("_test.go")
    )
    check_local_imports("server/", server_entries, "Containerfile.server", server_prefixes)

print("ℹ️  Checking downloaded and generated manifest inputs...")
makefile = read(root / "Makefile")
revision_match = re.search(r"(?m)^KSERVE_MANIFESTS_REVISION\s*\?=\s*(\S+)", makefile)
revision_file = root / "config/crd/external/.kserve_manifests_revision"
if not revision_match:
    fail("Makefile: KSERVE_MANIFESTS_REVISION is not pinned")
elif not revision_file.is_file():
    fail("config/crd/external/.kserve_manifests_revision is missing")
elif revision_match.group(1) != revision_file.read_text().strip():
    fail("Makefile KSERVE_MANIFESTS_REVISION does not match the stored revision")
else:
    ok(f"KServe CRD revision is pinned to {revision_match.group(1)}")

operator_revision = re.search(r"(?m)^OPERATOR_CRD_REVISION\s*\?=\s*([0-9a-f]{40})$", makefile)
if not operator_revision:
    fail("Makefile: OPERATOR_CRD_REVISION is not a full commit SHA")
else:
    ok(f"Operator CRD revision is pinned to {operator_revision.group(1)[:12]}")

external_kustomization = load_yaml(root / "config/crd/external/kustomization.yaml")
for resource in external_kustomization.get("resources", []):
    if not (root / "config/crd/external" / resource).is_file():
        fail(f"external CRD resource does not exist: {resource}")

for crd in (
    "serving.kserve.io_inferenceservices.yaml",
    "serving.kserve.io_servingruntimes.yaml",
    "serving.kserve.io_llminferenceservices.yaml",
):
    if not (root / "config/crd/external" / crd).is_file():
        fail(f"external CRD is missing: {crd}")

expected_crd_names = {
    "serving.kserve.io_inferenceservices.yaml": "inferenceservices.serving.kserve.io",
    "serving.kserve.io_servingruntimes.yaml": "servingruntimes.serving.kserve.io",
    "serving.kserve.io_llminferenceservices.yaml": "llminferenceservices.serving.kserve.io",
}
for filename, name in expected_crd_names.items():
    path = root / "config/crd/external" / filename
    if path.is_file() and name not in read(path):
        fail(f"external CRD {filename} does not define {name}")

for crd in (
    "datasciencecluster.opendatahub.io_datascienceclusters.yaml",
    "dscinitialization.opendatahub.io_dscinitializations.yaml",
):
    if not (root / "config/crd/external" / crd).is_file():
        fail(f"envtest DSC CRD is missing: {crd}")

print("ℹ️  Checking the KServe module manifest contract...")
for overlay in ("config/overlays/odh", "config/overlays/xks"):
    if not (root / overlay / "kustomization.yaml").is_file():
        fail(f"KServe module expects {overlay}/kustomization.yaml")
    else:
        ok(f"KServe module source path exists: {overlay}")

for overlay in ("config/overlays/odh", "config/overlays/xks"):
    text = read(root / overlay / "kustomization.yaml")
    if "../../base" not in text:
        fail(f"{overlay}/kustomization.yaml does not build from ../../base")

base_kustomization = load_yaml(root / "config/base/kustomization.yaml")
for replacement in base_kustomization.get("replacements", []):
    source = replacement.get("source", {})
    field_path = source.get("fieldPath", "")
    if field_path.startswith("data.") and field_path[len("data.") :] not in params:
        fail(f"config/base/kustomization.yaml references missing params.env key {field_path[len('data.') :]}")

required_targets = {"odh-model-controller", "model-serving-api"}
found_targets = set()
for replacement in base_kustomization.get("replacements", []):
    if replacement.get("source", {}).get("fieldPath", "") not in (
        "data.odh-model-controller",
        "data.odh-model-serving-api",
    ):
        continue
    for target in replacement.get("targets", []):
        select = target.get("select", {})
        if select.get("kind") == "Deployment" and select.get("name") in required_targets:
            found_targets.add(select["name"])
if found_targets != required_targets:
    fail(f"config/base replacements do not inject both images (found {sorted(found_targets)})")
else:
    ok("base replacements inject the controller and model-serving-api images")

if warnings:
    print(f"ℹ️  {len(warnings)} warning(s); see above")
if issues:
    print(f"❌ Static validation failed with {len(issues)} issue(s)")
    sys.exit(1)
print("✅ Static build-input contracts passed")
PY
}

check_go_modules() {
  require_cmd go
  info "Verifying Go module inputs..."
  go mod download
  go mod verify
  go mod tidy
  if ! git diff --exit-code -- go.mod go.sum; then
    err "go mod tidy changed go.mod or go.sum; commit the tidy result"
    return 1
  fi
  pass "Go modules download, verify, and tidy cleanly"
}

check_hermetic() (
  require_cmd docker
  require_cmd tar
  info "Preparing an isolated source tree and Go module cache..."
  local tmp controller_builder server_builder uid_gid
  tmp="$(mktemp -d "${TMPDIR:-/tmp}/omc-hermetic.XXXXXX")"
  cleanup_hermetic() {
    # Go writes module cache directories read-only. Restore owner write bits so
    # Linux CI can remove the temporary tree instead of failing a green build.
    chmod -R u+w "$tmp" 2>/dev/null || true
    rm -rf "$tmp" 2>/dev/null || true
  }
  trap cleanup_hermetic EXIT

  controller_builder=$(dockerfile_builder_image Containerfile)
  server_builder=$(dockerfile_builder_image Containerfile.server)
  uid_gid="$(id -u):$(id -g)"

  mkdir -p "$tmp/src" "$tmp/modcache" "$tmp/gocache" "$tmp/gopath" "$tmp/out"
  (cd "$REPO_ROOT" && tar --exclude='./.git' -cf - .) | tar -xf - -C "$tmp/src"
  cp "$tmp/src/go.mod" "$tmp/go.mod.before"
  cp "$tmp/src/go.sum" "$tmp/go.sum.before"

  # The builder image is the same one Konflux uses, so this proves the
  # prefetched module set against the real Go toolchain (including Red Hat's
  # strictfipsruntime). Populate once with network, then build with no network.
  info "Populating the isolated module cache in $controller_builder..."
  docker run --rm --platform "$PLATFORM" --user "$uid_gid" \
    -e HOME=/tmp \
    -e GOMODCACHE=/gomodcache \
    -e GOCACHE=/gocache \
    -e GOPATH=/gopath \
    -e GOTOOLCHAIN=local \
    -e GOFLAGS=-mod=mod \
    -v "$tmp/src:/workspace" \
    -v "$tmp/modcache:/gomodcache" \
    -v "$tmp/gocache:/gocache" \
    -v "$tmp/gopath:/gopath" \
    -w /workspace \
    "$controller_builder" go mod download
  pass "Go module cache populated"

  info "Compiling both binaries with the network disabled..."
  docker run --rm --platform "$PLATFORM" --network none --user "$uid_gid" \
    -e HOME=/tmp \
    -e GOMODCACHE=/gomodcache \
    -e GOCACHE=/gocache \
    -e GOPATH=/gopath \
    -e GOTOOLCHAIN=local \
    -e GOPROXY=off \
    -e GOSUMDB=off \
    -e CGO_ENABLED=0 \
    -e GOOS=linux \
    -v "$tmp/src:/workspace:ro" \
    -v "$tmp/modcache:/gomodcache:ro" \
    -v "$tmp/gocache:/gocache" \
    -v "$tmp/gopath:/gopath" \
    -v "$tmp/out:/out" \
    -w /workspace \
    "$controller_builder" go build -a -buildvcs=false -o /out/manager cmd/main.go
  pass "Controller compiles without network access"

  docker run --rm --platform "$PLATFORM" --network none --user "$uid_gid" \
    -e HOME=/tmp \
    -e GOMODCACHE=/gomodcache \
    -e GOCACHE=/gocache \
    -e GOPATH=/gopath \
    -e GOTOOLCHAIN=local \
    -e GOPROXY=off \
    -e GOSUMDB=off \
    -e CGO_ENABLED=1 \
    -e GOOS=linux \
    -e GOEXPERIMENT=strictfipsruntime \
    -v "$tmp/src:/workspace:ro" \
    -v "$tmp/modcache:/gomodcache:ro" \
    -v "$tmp/gocache:/gocache" \
    -v "$tmp/gopath:/gopath" \
    -v "$tmp/out:/out" \
    -w /workspace \
    "$server_builder" go build -tags strictfipsruntime -a -buildvcs=false \
      -o /out/model-serving-api ./server/
  pass "model-serving-api compiles without network access"

  if ! cmp -s "$tmp/go.mod.before" "$tmp/src/go.mod" || \
     ! cmp -s "$tmp/go.sum.before" "$tmp/src/go.sum"; then
    err "offline builds changed go.mod or go.sum; the prefetch set is incomplete"
    return 1
  fi
  pass "Offline builds used only the prefetched module set"

  cleanup_hermetic
  trap - EXIT
)

validate_manifests() (
  require_cmd python3
  local tmp
  tmp="$(mktemp -d "${TMPDIR:-/tmp}/omc-manifests.XXXXXX")"
  cleanup_manifests() {
    rm -rf "$tmp"
  }
  trap cleanup_manifests EXIT

  info "Rendering base, ODH, xKS, and default overlays..."
  kustomize_build "config/base" "$tmp/base.yaml"
  kustomize_build "config/overlays/odh" "$tmp/odh.yaml"
  kustomize_build "config/overlays/xks" "$tmp/xks.yaml"
  kustomize_build "config/default" "$tmp/default.yaml"

  python3 - "$REPO_ROOT" "$tmp" <<'PY'
import pathlib
import re
import sys

import yaml

root = pathlib.Path(sys.argv[1])
render_dir = pathlib.Path(sys.argv[2])
issues = []


def fail(message):
    issues.append(message)
    print(f"❌ {message}")


def ok(message):
    print(f"✅ {message}")


def params_env(path):
    values = {}
    for raw in pathlib.Path(path).read_text().splitlines():
        line = raw.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        values[key] = value
    return values


def load_docs(name):
    with open(render_dir / f"{name}.yaml", encoding="utf-8") as handle:
        return [doc for doc in yaml.safe_load_all(handle) if doc]


docs = {name: load_docs(name) for name in ("base", "odh", "xks", "default")}
params = params_env(root / "config/base/params.env")

# kubeconform does not supply a schema for CustomResourceDefinition itself. Do
# a structural check here so a malformed CRD is not silently skipped.
crd_seen = set()
crd_failed = False
for docs_name, documents in docs.items():
    for doc in documents:
        if doc.get("kind") != "CustomResourceDefinition":
            continue
        key = repr(doc)
        if key in crd_seen:
            continue
        crd_seen.add(key)
        name = doc.get("metadata", {}).get("name", "<unnamed>")
        spec = doc.get("spec") or {}
        names = spec.get("names") or {}
        versions = spec.get("versions") or []
        if doc.get("apiVersion") != "apiextensions.k8s.io/v1":
            fail(f"{docs_name}: CRD {name} must use apiextensions.k8s.io/v1")
            crd_failed = True
        if not spec.get("group") or not names.get("kind") or not names.get("plural"):
            fail(f"{docs_name}: CRD {name} is missing spec.group or spec.names")
            crd_failed = True
        if not versions or not all(
            version.get("name") and "served" in version and "storage" in version
            for version in versions
        ):
            fail(f"{docs_name}: CRD {name} has an incomplete spec.versions entry")
            crd_failed = True
        elif sum(1 for version in versions if version.get("storage") is True) != 1:
            fail(f"{docs_name}: CRD {name} must mark exactly one spec.versions entry as storage")
            crd_failed = True
if crd_seen and not crd_failed:
    ok("rendered CRDs have a valid v1 schema envelope")


def deployment(name, docs_name):
    for doc in docs[docs_name]:
        if doc.get("kind") == "Deployment" and doc.get("metadata", {}).get("name") == name:
            return doc
    return None


def service(name, docs_name, namespace=None):
    for doc in docs[docs_name]:
        metadata = doc.get("metadata") or {}
        if (
            doc.get("kind") == "Service"
            and metadata.get("name") == name
            and (namespace is None or metadata.get("namespace") == namespace)
        ):
            return doc
    return None


def container(deployment_doc, name=None):
    containers = deployment_doc["spec"]["template"]["spec"]["containers"]
    if name is None:
        return containers[0]
    return next(item for item in containers if item["name"] == name)


def args_map(container_doc):
    values = {}
    args = list(container_doc.get("command", [])) + list(container_doc.get("args", []))
    index = 0
    while index < len(args):
        token = args[index]
        if token == "--":
            break
        if not token.startswith("-") or token == "-":
            index += 1
            continue
        body = token[2:] if token.startswith("--") else token[1:]
        if not body:
            break
        if "=" in body:
            key, value = body.split("=", 1)
        else:
            key = body
            value = args[index + 1] if index + 1 < len(args) and not args[index + 1].startswith("-") else ""
            if value:
                index += 1
        values[key] = value
        index += 1
    return values


for overlay in ("base", "odh", "xks"):
    for name, key in (("odh-model-controller", "odh-model-controller"), ("model-serving-api", "odh-model-serving-api")):
        doc = deployment(name, overlay)
        if not doc:
            fail(f"{overlay}: Deployment/{name} is missing")
            continue
        actual = container(doc)["image"]
        if actual != params[key]:
            fail(f"{overlay}: Deployment/{name} image is {actual}, expected {params[key]}")
    for doc in docs[overlay]:
        for item in (doc.get("spec", {}).get("template", {}).get("spec", {}).get("containers") or []):
            if item.get("image") == "controller:latest":
                fail(f"{overlay}: placeholder image controller:latest remains")
    ok(f"{overlay}: workloads use the params.env images")

for overlay in ("base", "odh", "xks"):
    service_accounts = set()
    for doc in docs[overlay]:
        if doc.get("kind") != "ServiceAccount":
            continue
        metadata = doc.get("metadata") or {}
        service_accounts.add((metadata.get("namespace", ""), metadata.get("name")))
    for doc in docs[overlay]:
        kind = doc.get("kind")
        metadata = doc.get("metadata") or {}
        namespace = metadata.get("namespace", "")
        if kind in {"RoleBinding", "ClusterRoleBinding"}:
            for subject in doc.get("subjects") or []:
                if subject.get("kind") != "ServiceAccount":
                    continue
                account = (subject.get("namespace") or namespace, subject.get("name"))
                if account not in service_accounts:
                    fail(f"{overlay}: {kind}/{metadata.get('name')} binds {account[0]}/{account[1]}, which is not rendered")
        pod_spec = None
        if kind in {"Deployment", "StatefulSet", "DaemonSet", "Job"}:
            pod_spec = (doc.get("spec", {}).get("template", {}) or {}).get("spec")
        if pod_spec:
            account_name = pod_spec.get("serviceAccountName")
            if account_name and (namespace, account_name) not in service_accounts:
                fail(f"{overlay}: {kind}/{metadata.get('name')} runs as missing ServiceAccount {namespace}/{account_name}")
    ok(f"{overlay}: RBAC subjects and workload identities resolve")


odh_roles = {doc["metadata"]["name"]: doc for doc in docs["odh"] if doc.get("kind") == "ClusterRole"}
odh_bindings = {doc["metadata"]["name"]: doc for doc in docs["odh"] if doc.get("kind") == "ClusterRoleBinding"}
apiservers_roles = set()
for name, role in odh_roles.items():
    for rule in role.get("rules") or []:
        if "config.openshift.io" in (rule.get("apiGroups") or []) and "apiservers" in (rule.get("resources") or []):
            resource_names = rule.get("resourceNames") or []
            if "get" in (rule.get("verbs") or []) and (not resource_names or "cluster" in resource_names):
                apiservers_roles.add(name)
if not apiservers_roles:
    fail("odh overlay: no ClusterRole grants get on apiservers.config.openshift.io (RHOAIENG-82376)")
else:
    controller_doc = deployment("odh-model-controller", "odh")
    if controller_doc is None:
        fail("odh overlay: controller Deployment is missing; cannot verify its ServiceAccount")
    else:
        controller_namespace = (controller_doc.get("metadata") or {}).get("namespace", "")
        controller_pod_spec = controller_doc.get("spec", {}).get("template", {}).get("spec", {})
        controller_service_account = controller_pod_spec.get("serviceAccountName", "default")
        bound = False
        for binding in odh_bindings.values():
            if binding.get("roleRef", {}).get("name") not in apiservers_roles:
                continue
            for subject in binding.get("subjects") or []:
                if (
                    subject.get("kind") == "ServiceAccount"
                    and subject.get("namespace") == controller_namespace
                    and subject.get("name") == controller_service_account
                ):
                    bound = True
        if not bound:
            fail(
                "odh overlay: no ClusterRole granting apiservers.config.openshift.io "
                f"is bound to {controller_namespace}/{controller_service_account}"
            )
        else:
            ok("odh overlay grants apiservers.config.openshift.io to the controller")


def probe_port(container_doc, probe):
    return ((container_doc.get(probe) or {}).get("httpGet") or {}).get("port")


def probe_http_get(container_doc, probe):
    return (container_doc.get(probe) or {}).get("httpGet") or {}


manager = deployment("odh-model-controller", "odh")
server = deployment("model-serving-api", "odh")
if manager and server:
    manager_container = container(manager, "manager")
    manager_args = args_map(manager_container)
    metrics_port = manager_args.get("metrics-bind-address", "").lstrip(":")
    probe = manager_args.get("health-probe-bind-address", "").lstrip(":")
    if probe and probe_port(manager_container, "livenessProbe") != int(probe):
        fail(f"controller livenessProbe port does not match --health-probe-bind-address {probe}")
    if probe and probe_port(manager_container, "readinessProbe") != int(probe):
        fail(f"controller readinessProbe port does not match --health-probe-bind-address {probe}")
    metrics_service = service("odh-model-controller-metrics-service", "odh")
    if metrics_service and metrics_port:
        targets = {str(port.get("targetPort")) for port in metrics_service.get("spec", {}).get("ports", [])}
        if metrics_port not in targets:
            fail(f"controller metrics Service targets {sorted(targets)}, but the manager binds {metrics_port}")
    webhook_ports = {str(port.get("containerPort")) for port in manager_container.get("ports", [])}
    workload_pods = []
    for doc in docs["odh"]:
        if doc.get("kind") not in {"Deployment", "StatefulSet", "DaemonSet"}:
            continue
        workload_metadata = doc.get("metadata") or {}
        pod_template = (doc.get("spec") or {}).get("template") or {}
        workload_pods.append(
            (
                workload_metadata.get("namespace", ""),
                f"{doc.get('kind')}/{workload_metadata.get('name', '<unnamed>')}",
                (pod_template.get("metadata") or {}).get("labels") or {},
            )
        )
    webhook_refs = set()
    missing_webhook_refs = set()
    invalid_webhook_ports = set()
    for doc in docs["odh"]:
        kind = doc.get("kind")
        if kind not in {"MutatingWebhookConfiguration", "ValidatingWebhookConfiguration"}:
            continue
        metadata = doc.get("metadata") or {}
        config_name = metadata.get("name", "<unnamed>")
        config_namespace = metadata.get("namespace", "")
        for webhook in doc.get("webhooks") or []:
            service_ref = (webhook.get("clientConfig") or {}).get("service") or {}
            if not service_ref:
                continue
            ref_name = service_ref.get("name")
            ref_namespace = service_ref.get("namespace") or config_namespace
            if not ref_name:
                fail(f"odh: {kind}/{config_name} webhook {webhook.get('name')} has no clientConfig.service.name")
                continue
            ref = (ref_namespace, ref_name)
            referenced = service(ref_name, "odh", namespace=ref_namespace)
            if referenced is None:
                missing_webhook_refs.add(ref)
                continue
            webhook_refs.add(ref)
            ref_port = str(service_ref.get("port", 443))
            service_ports = {str(port.get("port")) for port in referenced.get("spec", {}).get("ports", [])}
            if ref_port not in service_ports:
                invalid_webhook_ports.add((ref_namespace, ref_name, ref_port))
    for ref_namespace, ref_name in sorted(missing_webhook_refs):
        fail(f"odh: admission webhook references missing Service {ref_namespace}/{ref_name}")
    for ref_namespace, ref_name, ref_port in sorted(invalid_webhook_ports):
        fail(f"odh: admission webhook targets port {ref_port}, but Service {ref_namespace}/{ref_name} does not expose it")
    for ref_namespace, ref_name in sorted(webhook_refs):
        referenced = service(ref_name, "odh", namespace=ref_namespace)
        targets = {str(port.get("targetPort")) for port in referenced.get("spec", {}).get("ports", [])}
        if not targets.issubset(webhook_ports):
            fail(f"webhook Service {ref_namespace}/{ref_name} targets {sorted(targets)}, but the container exposes {sorted(webhook_ports)}")
        selector = (referenced.get("spec") or {}).get("selector") or {}
        if not selector:
            fail(f"webhook Service {ref_namespace}/{ref_name} has no spec.selector, so it cannot route admission traffic")
            continue
        matching_workloads = [
            workload_name
            for workload_namespace, workload_name, pod_labels in workload_pods
            if (not ref_namespace or not workload_namespace or workload_namespace == ref_namespace)
            and all(pod_labels.get(key) == value for key, value in selector.items())
        ]
        if not matching_workloads:
            fail(
                f"webhook Service {ref_namespace}/{ref_name} selector {selector} "
                "matches no rendered workload Pod labels"
            )

    server_container = container(server)
    server_ports = {str(port.get("containerPort")) for port in server_container.get("ports", [])}
    server_service = service("model-serving-api", "odh")
    if server_service:
        for port in server_service.get("spec", {}).get("ports", []):
            if str(port.get("targetPort")) not in server_ports:
                fail(f"model-serving-api Service targetPort {port.get('targetPort')} is not exposed by the container")
    for probe_name in ("livenessProbe", "readinessProbe"):
        http_get = probe_http_get(server_container, probe_name)
        if str(http_get.get("port")) != "8443" or str(http_get.get("scheme", "HTTP")).upper() != "HTTPS":
            fail(f"model-serving-api {probe_name} must use HTTPS on port 8443")
    env = {item["name"]: item.get("value") for item in server_container.get("env") or []}
    if env.get("TLS_CERT_DIR") != "/tls":
        fail("model-serving-api must set TLS_CERT_DIR=/tls")
    mounts = {mount.get("mountPath") for mount in server_container.get("volumeMounts") or []}
    if "/tls" not in mounts:
        fail("model-serving-api must mount the TLS secret at /tls")

    for monitor in (doc for doc in docs["odh"] if doc.get("kind") == "ServiceMonitor"):
        selector = monitor.get("spec", {}).get("selector", {}).get("matchLabels", {})
        if selector.get("app") != "model-serving-api":
            continue
        for endpoint in monitor.get("spec", {}).get("endpoints", []):
            if endpoint.get("port") != "metrics":
                fail("model-serving-api ServiceMonitor must select the metrics port by name")
            if endpoint.get("scheme") != "https":
                fail("model-serving-api ServiceMonitor must use HTTPS")
    ok("controller and model-serving-api ports and probes agree")


manager_flags = set()
for line in (root / "cmd/main.go").read_text().splitlines():
    match = re.search(r'flag\.(?:String|Bool|Int|Int64|Duration|StringVar|BoolVar|IntVar|DurationVar)\(\s*&?[\w.]+,\s*"([^"]+)"', line)
    if match:
        manager_flags.add(match.group(1))
if not manager_flags:
    fail("cmd/main.go: no manager flags were discovered")
else:
    for overlay in ("base", "odh", "xks"):
        doc = deployment("odh-model-controller", overlay)
        if not doc:
            continue
        used = set(args_map(container(doc, "manager")))
        unknown = used - manager_flags
        if unknown:
            fail(f"{overlay}: manager passes undefined flag(s): {', '.join(sorted(unknown))}")
    ok("every rendered manager flag is defined by cmd/main.go")


if issues:
    print(f"❌ Manifest validation failed with {len(issues)} issue(s)")
    sys.exit(1)
print("✅ Manifest, image, RBAC, and port contracts passed")
PY

  if [[ "$SKIP_SCHEMA" =~ ^(true|1|yes)$ ]]; then
    warn "Skipping kubeconform because SKIP_SCHEMA=true"
    return 0
  fi
  require_cmd "$KUBECONFORM_BIN"
  require_cmd jq

  info "Validating rendered resources with kubeconform ${KUBERNETES_VERSION}..."
  local out json kc_err kc_status kc_finding invalid errored bad_skip skipped
  local schema_cache="$tmp/kubeconform-cache"
  mkdir -p "$schema_cache"
  kc_finding=false
  for out in "$tmp/base.yaml" "$tmp/odh.yaml" "$tmp/xks.yaml" "$tmp/default.yaml"; do
    json="$out.json"
    kc_err="$out.err"
    kc_status=0
    kc_finding=false
    set +e
    "$KUBECONFORM_BIN" -strict -ignore-missing-schemas \
      -kubernetes-version "$KUBERNETES_VERSION" -verbose \
      -cache "$schema_cache" -output json "$out" > "$json" 2> "$kc_err"
    kc_status=$?
    set -e

    if ! jq -e '.resources' "$json" >/dev/null 2>&1; then
      err "kubeconform produced no parseable output for $(basename "$out")"
      sed -n '1,20p' "$kc_err" >&2
      return 1
    fi

    invalid=$(jq -r '.resources[] | select(.status=="statusInvalid") | "\(.kind) \(.version): \(.msg)"' "$json" 2>/dev/null || true)
    if [ -n "$invalid" ]; then
      err "kubeconform found invalid resources in $(basename "$out"):"
      printf '%s\n' "$invalid" >&2
      kc_finding=true
    fi

    errored=$(jq -r '.resources[] | select(.status=="statusError") | "\(.kind) \(.version): \(.msg)"' "$json" 2>/dev/null || true)
    if [ -n "$errored" ]; then
      err "kubeconform could not validate resources in $(basename "$out"):"
      printf '%s\n' "$errored" >&2
      kc_finding=true
    fi

    bad_skip=$(jq -r '
      .resources[] | select(.status=="statusSkipped")
      | . as $r
      | select($r.kind != "CustomResourceDefinition")
      | (if ($r.version|contains("/")) then ($r.version|split("/")[0]) else "" end) as $g
      | select($g=="" or ($g|endswith(".k8s.io")) or ($g|IN("apps","batch","policy","autoscaling","extensions")))
      | "  \($r.kind) (\($r.version))"' "$json" 2>/dev/null || true)
    if [ -n "$bad_skip" ]; then
      err "kubeconform skipped built-in kind(s) in $(basename "$out") — likely an apiVersion/kind typo:"
      printf '%s\n' "$bad_skip" >&2
      kc_finding=true
    fi

    if [ "$kc_status" -ne 0 ] && [ "$kc_finding" = "false" ]; then
      err "kubeconform exited ${kc_status} for $(basename "$out") without a resource finding"
      sed -n '1,20p' "$kc_err" >&2
      return 1
    fi

    if [ "$kc_finding" = "true" ]; then
      return 1
    fi

    skipped=$(jq -r '[.resources[] | select(.status=="statusSkipped") | .kind] | unique | join(", ")' "$json" 2>/dev/null || true)
    if [ -n "$skipped" ]; then
      info "  $(basename "$out"): schema-skipped CRD-backed kinds: $skipped"
    fi
  done
  pass "All overlays rendered and passed Kubernetes schema checks"

  cleanup_manifests
  trap - EXIT
)

validate_controller() {
  require_cmd docker
  run_static_checks >/dev/null

  info "Building controller image $CONTROLLER_IMAGE for $PLATFORM..."
  docker build --platform "$PLATFORM" --tag "$CONTROLLER_IMAGE" -f Containerfile .
  pass "Controller image built"

  local user
  user=$(docker image inspect -f '{{.Config.User}}' "$CONTROLLER_IMAGE")
  user="${user%%:*}"
  if [[ "$user" != "65532" ]]; then
    err "Controller image runs as '${user:-unknown}', expected UID 65532"
    return 1
  fi
  pass "Controller image declares non-root UID 65532"

  local help
  help=$(docker run --rm --platform "$PLATFORM" "$CONTROLLER_IMAGE" --help 2>&1) || {
    err "Controller image failed to execute --help"
    printf '%s\n' "$help" >&2
    return 1
  }
  # Go's flag package prints single-dash flags in help output even though it
  # accepts both forms on the command line.
  if ! grep -Eq '^[[:space:]]+--?metrics-bind-address([[:space:]]|$)' <<< "$help" || \
     ! grep -Eq '^[[:space:]]+--?health-probe-bind-address([[:space:]]|$)' <<< "$help"; then
    err "Controller --help is missing the probe or metrics flags"
    printf '%s\n' "$help" >&2
    return 1
  fi
  pass "Controller binary starts and exposes its expected flags"
}

validate_server() {
  require_cmd docker
  require_cmd curl
  require_cmd openssl
  run_static_checks >/dev/null

  info "Building model-serving-api image $SERVER_IMAGE for $PLATFORM..."
  docker build --platform "$PLATFORM" --tag "$SERVER_IMAGE" -f Containerfile.server .
  pass "model-serving-api image built"

  local user
  user=$(docker image inspect -f '{{.Config.User}}' "$SERVER_IMAGE")
  user="${user%%:*}"
  if [[ "$user" != "1000" ]]; then
    err "model-serving-api image runs as '${user:-unknown}', expected UID 1000"
    return 1
  fi
  pass "model-serving-api image declares non-root UID 1000"

  local tmp container_name suffix
  suffix="${GITHUB_RUN_ID:-$$}-$RANDOM"
  container_name="omc-server-$suffix"
  tmp="$(mktemp -d "${TMPDIR:-/tmp}/omc-server.XXXXXX")"
  SERVER_CONTAINER_NAME="$container_name"
  SERVER_NO_TLS_CONTAINER_NAME=""
  SERVER_TMP="$tmp"
  chmod 755 "$tmp"
  cleanup_server() {
    if [ -n "${SERVER_CONTAINER_NAME:-}" ]; then
      docker rm -f "$SERVER_CONTAINER_NAME" >/dev/null 2>&1 || true
    fi
    if [ -n "${SERVER_NO_TLS_CONTAINER_NAME:-}" ]; then
      docker rm -f "$SERVER_NO_TLS_CONTAINER_NAME" >/dev/null 2>&1 || true
    fi
    if [ -n "${SERVER_TMP:-}" ]; then
      rm -rf "$SERVER_TMP"
    fi
  }
  trap cleanup_server EXIT

  openssl req -x509 -newkey rsa:2048 -nodes -days 1 \
    -keyout "$tmp/tls.key" -out "$tmp/tls.crt" \
    -subj "/CN=localhost" -addext "subjectAltName=DNS:localhost,IP:127.0.0.1" >/dev/null 2>&1
  chmod 644 "$tmp/tls.crt" "$tmp/tls.key"

  cat > "$tmp/kubeconfig" <<'KUBECONFIG'
apiVersion: v1
kind: Config
clusters:
  - name: dummy
    cluster:
      server: https://127.0.0.1:6443
      insecure-skip-tls-verify: true
contexts:
  - name: dummy
    context:
      cluster: dummy
      user: dummy
current-context: dummy
users:
  - name: dummy
    user:
      token: dummy
KUBECONFIG
  chmod 644 "$tmp/kubeconfig"
  local curl_tls=(--silent --cacert "$tmp/tls.crt" --connect-timeout 2 --max-time 5)

  info "Starting model-serving-api with a generated TLS certificate..."
  docker run -d --name "$container_name" --platform "$PLATFORM" \
    -p "127.0.0.1:${SERVER_HTTPS_PORT}:8443" \
    -p "127.0.0.1:${SERVER_METRICS_PORT}:8080" \
    -e TLS_CERT_DIR=/tls \
    -e LISTEN_ADDR=:8443 \
    -e METRICS_ADDR=:8080 \
    -e KUBECONFIG=/kubeconfig \
    -v "$tmp:/tls:ro" \
    -v "$tmp/kubeconfig:/kubeconfig:ro" \
    "$SERVER_IMAGE" >/dev/null

  local code=""
  for _ in $(seq 1 60); do
    code=$(curl "${curl_tls[@]}" -o /dev/null -w '%{http_code}' "https://127.0.0.1:${SERVER_HTTPS_PORT}/healthz" || true)
    [ "$code" = "200" ] && break
    if [ "$(docker inspect -f '{{.State.Running}}' "$container_name" 2>/dev/null || echo false)" != "true" ]; then
      break
    fi
    sleep 1
  done
  if [ "$code" != "200" ]; then
    err "model-serving-api /healthz did not return 200 (last: ${code:-no response})"
    docker logs "$container_name" || true
    return 1
  fi
  pass "model-serving-api /healthz returned 200 over HTTPS"

  code=$(curl "${curl_tls[@]}" -o /dev/null -w '%{http_code}' "https://127.0.0.1:${SERVER_HTTPS_PORT}/readyz" || true)
  if [ "$code" != "200" ]; then
    err "model-serving-api /readyz returned ${code:-no response}"
    docker logs "$container_name" || true
    return 1
  fi
  pass "model-serving-api /readyz returned 200 over HTTPS"

  local sample_status
  sample_status=$(curl "${curl_tls[@]}" -o "$tmp/sample.yaml" -w '%{http_code}' \
    "https://127.0.0.1:${SERVER_HTTPS_PORT}/api/v1/samples/llm-d?type=workload-single-node" || true)
  if [ "$sample_status" != "200" ] || \
     ! grep -q 'kind: LLMInferenceServiceConfig' "$tmp/sample.yaml" 2>/dev/null; then
    err "model-serving-api embedded sample endpoint did not return an LLMInferenceServiceConfig (status: ${sample_status:-no response})"
    sed -n '1,20p' "$tmp/sample.yaml" 2>/dev/null >&2 || true
    return 1
  fi
  pass "model-serving-api embedded samples are present"

  code=$(curl "${curl_tls[@]}" -o /dev/null -w '%{http_code}' "https://127.0.0.1:${SERVER_HTTPS_PORT}/api/v1/gateways?namespace=default" || true)
  if [ "$code" != "401" ]; then
    err "model-serving-api gateway endpoint returned ${code}, expected 401 without a bearer token"
    return 1
  fi
  pass "model-serving-api gateway endpoint rejects unauthenticated requests"

  local metrics
  metrics=$(curl "${curl_tls[@]}" --fail --show-error "https://127.0.0.1:${SERVER_METRICS_PORT}/metrics")
  if ! grep -q 'go_' <<< "$metrics"; then
    err "model-serving-api metrics endpoint has no Go runtime metrics"
    return 1
  fi
  pass "model-serving-api /metrics served Go runtime metrics over HTTPS"

  local uid
  uid=$(docker exec "$container_name" sh -c 'id -u' 2>/dev/null || true)
  if [ "$uid" != "1000" ]; then
    err "model-serving-api runs as UID ${uid:-unknown}, expected 1000"
    return 1
  fi
  pass "model-serving-api runs as UID 1000"

  sleep 5
  if [ "$(docker inspect -f '{{.State.Running}}' "$container_name" 2>/dev/null || echo false)" != "true" ]; then
    err "model-serving-api exited during the stability window"
    docker logs "$container_name" || true
    return 1
  fi

  local logs
  logs=$(docker logs "$container_name" 2>&1 || true)
  if grep -Eqi 'panic:|fatal error|segmentation fault' <<< "$logs"; then
    err "model-serving-api logs contain a fatal error"
    printf '%s\n' "$logs" | tail -50 >&2
    return 1
  fi
  if ! grep -q 'server starting' <<< "$logs" || ! grep -q 'metrics server starting' <<< "$logs"; then
    err "model-serving-api did not log both HTTPS servers starting"
    printf '%s\n' "$logs" | tail -50 >&2
    return 1
  fi
  pass "model-serving-api stayed running and logged both HTTPS servers"

  local no_tls_container="$container_name-no-tls" no_tls_running=true
  local no_tls_status no_tls_output
  SERVER_NO_TLS_CONTAINER_NAME="$no_tls_container"
  docker run -d --name "$no_tls_container" --platform "$PLATFORM" \
    -e KUBECONFIG=/kubeconfig \
    -v "$tmp/kubeconfig:/kubeconfig:ro" \
    "$SERVER_IMAGE" >/dev/null

  for _ in $(seq 1 15); do
    if [ "$(docker inspect -f '{{.State.Running}}' "$no_tls_container" 2>/dev/null || echo false)" != "true" ]; then
      no_tls_running=false
      break
    fi
    sleep 1
  done
  no_tls_output=$(docker logs "$no_tls_container" 2>&1 || true)
  if [ "$no_tls_running" = "true" ]; then
    err "model-serving-api did not exit within 15s when TLS_CERT_DIR was unset"
    printf '%s\n' "$no_tls_output" >&2
    return 1
  fi
  no_tls_status=$(docker inspect -f '{{.State.ExitCode}}' "$no_tls_container")
  docker rm -f "$no_tls_container" >/dev/null 2>&1 || true
  SERVER_NO_TLS_CONTAINER_NAME=""
  if [ "$no_tls_status" -eq 0 ] || ! grep -q 'TLS is required' <<< "$no_tls_output"; then
    err "model-serving-api did not fail closed when TLS_CERT_DIR was unset"
    printf '%s\n' "$no_tls_output" >&2
    return 1
  fi
  pass "model-serving-api fails closed without TLS configuration"

  cleanup_server
  trap - EXIT
  SERVER_CONTAINER_NAME=""
  SERVER_TMP=""
}

run_kind() {
  require_cmd docker
  require_cmd kind
  require_cmd kubectl
  info "Running the repository's xKS KinD smoke test against $CONTROLLER_IMAGE..."
  IMAGE="$CONTROLLER_IMAGE" SERVER_IMAGE="${KIND_SERVER_IMAGE:-}" \
    SKIP_BUILD="${SKIP_BUILD:-true}" KEEP_CLUSTER="${KEEP_CLUSTER:-false}" \
    ./test/scripts/kind/setup-odh-model-controller-xks.sh
}

validate_downstream() (
  require_cmd git
  require_cmd python3
  if [[ ! "$DOWNSTREAM_REF" =~ ^[0-9a-f]{40}$ ]]; then
    err "DOWNSTREAM_REF must be a full 40-character commit SHA, got '$DOWNSTREAM_REF'"
    return 1
  fi

  local workdir repo_dir
  workdir=$(mktemp -d "${TMPDIR:-/tmp}/omc-downstream.XXXXXX")
  repo_dir="$workdir/repo"
  trap 'rm -rf "$workdir"' EXIT

  info "Fetching downstream variant ${DOWNSTREAM_REF:0:12} from $DOWNSTREAM_REPO..."
  git init -q "$repo_dir"
  if ! git -C "$repo_dir" fetch -q --depth=1 "$DOWNSTREAM_REPO" "$DOWNSTREAM_REF"; then
    err "Could not fetch downstream ref $DOWNSTREAM_REF"
    return 1
  fi
  git -C "$repo_dir" checkout -q --detach FETCH_HEAD
  pass "Fetched pinned downstream commit"

  python3 - "$repo_dir" <<'PY'
import json
import pathlib
import posixpath
import re
import shlex
import sys

import yaml

repo = pathlib.Path(sys.argv[1])
issues = []


def fail(message):
    issues.append(message)
    print(f"❌ {message}")


def ok(message):
    print(f"✅ {message}")


def read(path):
    return (repo / path).read_text()


def params(path):
    document = yaml.safe_load(read(path))
    return {item["name"]: item.get("value") for item in document["spec"]["params"]}


def dockerfile_instructions(text):
    instructions = []
    current = ""
    for raw in text.splitlines():
        stripped = raw.strip()
        if not current and (not stripped or stripped.startswith("#")):
            continue
        if stripped.startswith("#"):
            continue
        current = f"{current} {stripped}".strip() if current else stripped
        if current.endswith("\\"):
            current = current[:-1].rstrip()
        else:
            instructions.append(current)
            current = ""
    if current:
        instructions.append(current)
    return instructions


def parse_stages(text):
    stages = []
    for line in dockerfile_instructions(text):
        fields = line.split(None, 1)
        keyword = fields[0].upper()
        rest = fields[1] if len(fields) > 1 else ""
        if keyword == "FROM":
            tokens = [token for token in shlex.split(rest) if not token.startswith("--")]
            if not tokens:
                continue
            alias = tokens[2].lower() if len(tokens) >= 3 and tokens[1].lower() == "as" else None
            stages.append({"target": tokens[0], "alias": alias, "instructions": []})
        elif stages:
            stages[-1]["instructions"].append((keyword, rest))
    return stages


def stage_index(stages, ref):
    ref = ref.lower()
    if ref.isdigit():
        index = int(ref)
        return index if 0 <= index < len(stages) else None
    return next((index for index, stage in enumerate(stages) if stage["alias"] == ref), None)


def resolved_base(stages, index, seen=None):
    seen = set() if seen is None else seen
    if index in seen:
        return stages[index]["target"]
    seen.add(index)
    dependency = stage_index(stages, stages[index]["target"])
    if dependency is None:
        return stages[index]["target"]
    return resolved_base(stages, dependency, seen)


def apply_build_environment(keyword, rest, environment):
    if keyword not in {"ENV", "ARG"}:
        return
    tokens = shlex.split(rest, comments=True)
    if not tokens:
        return
    if keyword == "ARG":
        for token in tokens:
            if "=" in token:
                key, value = token.split("=", 1)
                environment[key] = value
        return
    if "=" not in tokens[0]:
        environment[tokens[0]] = " ".join(tokens[1:])
        return
    for token in tokens:
        if "=" in token:
            key, value = token.split("=", 1)
            environment[key] = value


def normalize_path(workdir, path):
    if not path.startswith("/"):
        path = posixpath.join(workdir, path)
    return posixpath.normpath(path)


def parse_json_array(argument):
    try:
        tokens = json.loads(argument)
    except json.JSONDecodeError:
        return None
    if not isinstance(tokens, list) or not all(isinstance(token, str) for token in tokens):
        return None
    return tokens


def split_copy(rest):
    argument = rest.strip()
    flags = []
    while argument.startswith("--"):
        flag, _, remainder = argument.partition(" ")
        flags.append(flag)
        argument = remainder.strip()
    if argument.startswith("["):
        tokens = parse_json_array(argument)
        if tokens is None:
            return flags, None, None
    else:
        tokens = shlex.split(argument, comments=True)
    if len(tokens) < 2:
        return flags, None, None
    return flags, tokens[:-1], tokens[-1]


def copy_mappings(sources, destination, workdir, dockerfile, stage_name):
    destination_is_directory = destination.endswith("/") or destination in {".", ".."} or len(sources) > 1
    if len(sources) > 1 and not destination.endswith("/") and destination not in {".", ".."}:
        fail(f"downstream {dockerfile} {stage_name} COPY has multiple sources and a file destination")
        return []
    mappings = []
    for source in sources:
        if destination_is_directory:
            target = posixpath.join(workdir, destination, posixpath.basename(source.rstrip("/")))
        else:
            target = posixpath.join(workdir, destination)
        mappings.append((source, posixpath.normpath(target)))
    return mappings


def go_build_output(tokens):
    for position, token in enumerate(tokens):
        if token == "-o":
            return tokens[position + 1] if position + 1 < len(tokens) else None
        if token.startswith("-o=") and len(token) > 3:
            return token[3:]
    return None


def stage_builds(stage, dockerfile, stage_name):
    environment = {}
    workdir = "/"
    builds = []
    for keyword, rest in stage["instructions"]:
        if keyword in {"ENV", "ARG"}:
            apply_build_environment(keyword, rest, environment)
            continue
        if keyword == "WORKDIR":
            tokens = shlex.split(rest, comments=True)
            if tokens:
                workdir = normalize_path(workdir, tokens[0])
            continue
        if keyword != "RUN":
            continue
        for segment in re.split(r"\s*(?:&&|\|\||;)\s*", rest):
            tokens = shlex.split(segment, comments=True)
            effective = dict(environment)
            index = 1 if tokens and tokens[0] == "env" else 0
            while index < len(tokens):
                match = re.match(r"^([A-Za-z_][A-Za-z0-9_]*)=(.*)$", tokens[index])
                if not match:
                    break
                effective[match.group(1)] = match.group(2)
                index += 1
            if not re.search(r"\bgo\s+build\b", segment):
                continue
            output = go_build_output(tokens)
            if output is None:
                fail(f"downstream {dockerfile} {stage_name} has a go build without -o, so its output cannot be traced")
                continue
            output_is_directory = output.endswith("/") or output in {".", ".."}
            resolved = normalize_path(workdir, output)
            builds.append(
                {
                    "environment": effective,
                    "directory": resolved if output_is_directory else None,
                    "path": None if output_is_directory else resolved,
                }
            )
    return builds


def entrypoint_program(final_stage, dockerfile):
    entrypoint = None
    for keyword, rest in final_stage["instructions"]:
        if keyword == "ENTRYPOINT":
            entrypoint = rest.strip()
    if not entrypoint:
        fail(f"downstream {dockerfile} final stage has no ENTRYPOINT")
        return None
    if entrypoint.startswith("["):
        arguments = parse_json_array(entrypoint)
        if arguments is None:
            fail(f"downstream {dockerfile} ENTRYPOINT is not a JSON string array")
            return None
    else:
        arguments = shlex.split(entrypoint, comments=True)
    if not arguments:
        fail(f"downstream {dockerfile} ENTRYPOINT is empty")
        return None
    return arguments[0]


def proven_executable(stages, dockerfile):
    final_index = len(stages) - 1
    final_stage = stages[final_index]
    final_name = final_stage["alias"] or f"stage {final_index}"
    program = entrypoint_program(final_stage, dockerfile)
    if program is None:
        return None, None

    origins = []
    workdir = "/"
    for keyword, rest in final_stage["instructions"]:
        if keyword == "WORKDIR":
            tokens = shlex.split(rest, comments=True)
            if tokens:
                workdir = normalize_path(workdir, tokens[0])
            continue
        if keyword != "COPY":
            continue
        flags, sources, destination = split_copy(rest)
        if sources is None:
            fail(f"downstream {dockerfile} {final_name} has an unparseable COPY: {rest}")
            continue
        from_flags = [flag for flag in flags if flag.startswith("--from=")]
        if not from_flags:
            continue
        if len(from_flags) > 1:
            fail(f"downstream {dockerfile} {final_name} has a COPY with multiple --from stages")
            continue
        reference = from_flags[0].split("=", 1)[1]
        source_stage = stage_index(stages, reference)
        if source_stage is None:
            fail(f"downstream {dockerfile} copies {program} from {reference}, which is not a build stage")
            continue
        for source, target in copy_mappings(sources, destination, workdir, dockerfile, final_name):
            if target == program:
                origins.append((source_stage, source))

    if not origins:
        fail(f"downstream {dockerfile} does not copy its ENTRYPOINT {program} from a build stage")
        return None, None
    if len(origins) > 1:
        fail(f"downstream {dockerfile} copies its ENTRYPOINT {program} from more than one location; provenance is ambiguous")
        return None, None

    source_stage, source = origins[0]
    source_path = posixpath.normpath(source if source.startswith("/") else "/" + source)
    source_name = stages[source_stage]["alias"] or f"stage {source_stage}"
    candidates = [
        build
        for build in stage_builds(stages[source_stage], dockerfile, source_name)
        if build["path"] == source_path
        or (build["directory"] is not None and posixpath.dirname(source_path) == build["directory"])
    ]
    if len(candidates) != 1:
        fail(
            f"downstream {dockerfile} cannot prove that {program} ({source_path}) comes from exactly one "
            f"go build -o in {source_name}; found {len(candidates)} candidates"
        )
        return None, None
    return program, candidates[0]


pinned_runtime = re.compile(r"^registry\.redhat\.io/ubi9/ubi-minimal-pqc:9\.8@sha256:[0-9a-f]{64}$")
for dockerfile in ("Dockerfile.konflux", "Containerfile.server.konflux"):
    if not (repo / dockerfile).is_file():
        fail(f"downstream variant is missing {dockerfile}")
        continue
    text = read(dockerfile)
    stages = parse_stages(text)
    if not stages:
        fail(f"downstream {dockerfile} has no build stages")
        continue
    final_base = resolved_base(stages, len(stages) - 1)
    if not pinned_runtime.fullmatch(final_base):
        fail(f"downstream {dockerfile} does not use the digest-pinned PQC runtime base")
    issues_before = len(issues)
    program, build = proven_executable(stages, dockerfile)
    if build is None:
        continue
    environment = build["environment"]
    if environment.get("CGO_ENABLED") != "1":
        fail(f"downstream {dockerfile} does not resolve CGO_ENABLED=1 for the go build that produces {program}")
    experiments = {item.strip() for item in environment.get("GOEXPERIMENT", "").split(",") if item.strip()}
    if "strictfipsruntime" not in experiments:
        fail(f"downstream {dockerfile} does not resolve GOEXPERIMENT=strictfipsruntime for the go build that produces {program}")
    if len(issues) == issues_before:
        ok(f"downstream {dockerfile} runs {program} from a single FIPS go build and the pinned PQC base")

controller = params(".tekton/odh-model-controller-pull-request.yaml")
server = params(".tekton/odh-model-serving-api-pull-request.yaml")

for label, values, image, dockerfile in (
    ("controller", controller, "quay.io/rhoai/odh-model-controller-rhel9", "Dockerfile.konflux"),
    ("server", server, "quay.io/rhoai/odh-model-serving-api-rhel9", "Containerfile.server.konflux"),
):
    if not str(values.get("output-image", "")).startswith(image + ":"):
        fail(f"downstream {label} output-image is not under {image}")
    if values.get("dockerfile") != dockerfile:
        fail(f"downstream {label} does not build {dockerfile}")
    if str(values.get("hermetic", "")).lower() != "true":
        fail(f"downstream {label} does not enable hermetic builds")
    prefetch = str(values.get("prefetch-input", ""))
    if "gomod" not in prefetch:
        fail(f"downstream {label} does not prefetch Go modules")
    platforms = values.get("build-platforms") or []
    if not any("x86_64" in str(platform) for platform in platforms):
        fail(f"downstream {label} does not build linux/x86_64")
    if not any("arm64" in str(platform) for platform in platforms):
        fail(f"downstream {label} does not build arm64")
    ok(f"downstream {label} pipeline keeps hermetic gomod prefetch and multi-arch builds")

for overlay in ("config/overlays/odh", "config/overlays/xks"):
    if not (repo / overlay / "kustomization.yaml").is_file():
        fail(f"downstream variant is missing {overlay}/kustomization.yaml")
    else:
        ok(f"downstream variant keeps {overlay}")

downstream_params = {}
for raw in read("config/base/params.env").splitlines():
    line = raw.strip()
    if line and not line.startswith("#") and "=" in line:
        key, value = line.split("=", 1)
        downstream_params[key] = value

for key in ("odh-model-controller", "odh-model-serving-api"):
    if key not in downstream_params:
        fail(f"downstream params.env is missing {key}; the operator cannot inject the RHOAI image")
    else:
        ok(f"downstream params.env keeps the {key} key")

if issues:
    print(f"❌ Downstream variant contract failed with {len(issues)} issue(s)")
    sys.exit(1)
print("✅ Downstream variant contract passed; the downstream image itself is not built by this simulator")
PY

  pass "Downstream variant contract checked"
)

validate_generated_drift() {
  require_cmd git
  if [ -z "$CONTROLLER_GEN" ]; then
    CONTROLLER_GEN="$(go env GOPATH 2>/dev/null)/bin/controller-gen"
  fi
  if [ ! -x "$CONTROLLER_GEN" ]; then
    err "controller-gen is required; install it and set CONTROLLER_GEN"
    return 1
  fi
  if ! git rev-parse --verify "$BASE_REF^{commit}" >/dev/null 2>&1; then
    err "base ref $BASE_REF is not available; fetch it before checking generated drift"
    return 1
  fi

  local tmp
  tmp="$(mktemp -d "${TMPDIR:-/tmp}/omc-drift.XXXXXX")"
  DRIFT_TMP="$tmp"
  cleanup_drift() {
    if [ -n "${DRIFT_TMP:-}" ]; then
      rm -rf "$DRIFT_TMP"
    fi
  }
  trap cleanup_drift EXIT

  remove_generated_outputs() {
    local dir="$1"
    rm -rf "$dir/config/crd/bases" \
      "$dir/config/rbac/role.yaml" \
      "$dir/config/webhook/manifests.yaml"
    find "$dir/api" -name 'zz_generated.deepcopy.go' -type f -delete
  }

  file_digest() {
    local path="$1"
    if [ ! -e "$path" ]; then
      printf 'MISSING'
    elif command -v sha256sum >/dev/null 2>&1; then
      sha256sum "$path" | awk '{print $1}'
    else
      shasum -a 256 "$path" | awk '{print $1}'
    fi
  }

  collect_generated() {
    local dir="$1"
    {
      find "$dir/config/crd/bases" -type f 2>/dev/null || true
      [ -f "$dir/config/rbac/role.yaml" ] && printf '%s\n' "$dir/config/rbac/role.yaml"
      [ -f "$dir/config/webhook/manifests.yaml" ] && printf '%s\n' "$dir/config/webhook/manifests.yaml"
      find "$dir/api" -name 'zz_generated.deepcopy.go' -type f 2>/dev/null || true
    } | sed "s|^$dir/||" | LC_ALL=C sort -u
  }

  local ref label index
  local -a refs=(HEAD "$BASE_REF") labels=(head base)
  for index in 0 1; do
    ref="${refs[$index]}"
    label="${labels[$index]}"
    mkdir -p "$tmp/$label" "$tmp/$label-committed"
    git archive "$ref" | tar -x -C "$tmp/$label"

    while IFS= read -r rel; do
      mkdir -p "$tmp/$label-committed/$(dirname "$rel")"
      cp -a "$tmp/$label/$rel" "$tmp/$label-committed/$rel"
    done < <(collect_generated "$tmp/$label")

    mkdir -p "$tmp/$label/bin"
    cp "$CONTROLLER_GEN" "$tmp/$label/bin/controller-gen"
    chmod 0755 "$tmp/$label/bin/controller-gen"
    remove_generated_outputs "$tmp/$label"
    if ! (cd "$tmp/$label" && make manifests generate \
      CONTROLLER_GEN="$tmp/$label/bin/controller-gen" > "$tmp/make-$label.log" 2>&1); then
      cat "$tmp/make-$label.log" >&2
      err "generation failed on $ref; the generated files cannot be verified"
      return 1
    fi

    {
      collect_generated "$tmp/$label-committed"
      collect_generated "$tmp/$label"
    } | LC_ALL=C sort -u > "$tmp/$label.paths"

    : > "$tmp/$label.files"
    while IFS= read -r rel; do
      committed="$tmp/$label-committed/$rel"
      generated="$tmp/$label/$rel"
      committed_digest=$(file_digest "$committed")
      generated_digest=$(file_digest "$generated")
      if [ "$committed_digest" != "$generated_digest" ]; then
        printf '%s\t%s\t%s\n' "$rel" "$committed_digest" "$generated_digest" >> "$tmp/$label.files"
      fi
    done < "$tmp/$label.paths"
    LC_ALL=C sort -o "$tmp/$label.files" "$tmp/$label.files"
    diff -ru "$tmp/$label-committed" "$tmp/$label" > "$tmp/$label.diff" 2>&1 || true
  done

  local new_drift
  new_drift="$(LC_ALL=C comm -23 "$tmp/head.files" "$tmp/base.files")"
  if [ -n "$new_drift" ]; then
    sed -n '1,200p' "$tmp/head.diff" >&2
    err "generated-file drift introduced by this PR:"
    printf '%s\n' "$new_drift" | cut -f1 >&2
    return 1
  fi
  if [ -s "$tmp/head.files" ]; then
    warn "generated files are already drifted on $BASE_REF: $(cut -f1 "$tmp/head.files" | paste -sd, -)"
    sed -n '1,80p' "$tmp/head.diff"
  else
    pass "generated manifests and deepcopy code match their sources"
  fi

  cleanup_drift
  trap - EXIT
  DRIFT_TMP=""
}

case "$MODE" in
  go-version)
    version=$(dockerfile_go_version Containerfile)
    server_version=$(dockerfile_go_version Containerfile.server)
    if [ "$version" != "$server_version" ]; then
      err "builder images disagree: $version vs $server_version"
      exit 1
    fi
    go_mod_version=$(go_mod_go_minor)
    if [ "$version" != "$go_mod_version" ] || [ "$server_version" != "$go_mod_version" ]; then
      err "builder images use Go $version/$server_version, but go.mod requires Go $go_mod_version"
      exit 1
    fi
    printf '%s\n' "$version"
    ;;
  static)
    run_static_checks
    ;;
  go-modules)
    check_go_modules
    ;;
  hermetic)
    check_hermetic
    ;;
  manifests)
    validate_manifests
    ;;
  controller)
    validate_controller
    ;;
  server)
    validate_server
    ;;
  kind)
    run_kind
    ;;
  downstream)
    validate_downstream
    ;;
  generated-drift)
    validate_generated_drift
    ;;
  all)
    run_static_checks
    check_go_modules
    check_hermetic
    validate_manifests
    validate_controller
    validate_server
    ;;
  *)
    err "Usage: $0 <go-version|static|go-modules|hermetic|manifests|controller|server|kind|downstream|generated-drift|all>"
    exit 2
    ;;
esac
