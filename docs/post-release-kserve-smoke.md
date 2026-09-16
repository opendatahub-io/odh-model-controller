# Post-release kserve-module smoke

OpenShift-only validation after an ODH Model Serving release cut:

- `odh-model-controller` Running, `KServeReady=True`
- One `LLMInferenceService` reaches `Ready=True`

Tests live in [opendatahub-io/kserve](https://github.com/opendatahub-io/kserve)
(`post_release` pytest marker). Konflux runs them on a fresh ephemeral cluster.

JIRA: [RHOAIENG-85268](https://issues.redhat.com/browse/RHOAIENG-85268)

## When to run

This smoke is **on-demand only**. It does **not** run automatically when you cut a
release, merge a PR, or push a tag. Someone must trigger it explicitly after each
ODH kserve release.

Use this repo's **[ODH Release Workflow](https://github.com/opendatahub-io/odh-model-controller/actions/workflows/odh-release.yaml)**
(Actions → *ODH Release Workflow*) to cut component tags. For post-release smoke,
the relevant sequence is:

1. **Cut the kserve tag** — run the workflow with `repository: kserve` and
   `tag_name: odh-vX.Y` (or `-ea1`/`-ea2` suffix if applicable).
2. **Wait for the operator image** — tag push triggers Konflux builds; confirm
   `quay.io/opendatahub/odh-kserve-module-operator:<tag>` exists on Quay.
3. **Run post-release smoke** (this doc) with that same `<tag>`.
4. **Cut odh-model-controller** (and other components) — the release workflow
   requires the kserve tag to exist before bumping `go.mod` in this repo.

Run the smoke **after step 2** and **before treating the kserve release as validated**
for RHOAI downstream sync. Re-run after a tag rebuild if the operator image changed.

Do **not** expect this to gate the GitHub release workflow itself — it is a separate
manual Konflux check.

## How to run

Orchestration lives in
[odh-konflux-central](https://github.com/opendatahub-io/odh-konflux-central)
(`integration-tests/kserve/post-release-smoke-pipeline.yaml`). Pick one trigger:

### Option A — PAC comment (usual)

Comment on any open PR in **opendatahub-io/kserve** (the PR content does not matter;
the pipeline checks out the **release tag**, not the PR branch):

```
/post-release-smoke odh-v3.6
```

Replace `odh-v3.6` with the tag you just cut. The tag is required — there is no
default. Comment author must meet PAC policy for the kserve repo (owner, collaborator,
or listed in `OWNERS`).

Watch the run: [Konflux UI — open-data-hub-tenant](https://konflux-ui.apps.stone-prd-rh01.pg1f.p1.openshiftapps.com/ns/open-data-hub-tenant)
(PipelineRun name prefix `kserve-post-release-smoke`).

### Option B — Manual PipelineRun

In Konflux UI (same namespace), create a PipelineRun from
`pipelineruns/kserve/kserve-post-release-smoke.yaml` and set:

| Param | Value |
|-------|-------|
| `release_tag` | `odh-v3.6` (the tag under test) |
| `trigger_comment` | leave empty |
| `operator_image` | leave empty unless overriding the Quay ref |

Use this when no suitable kserve PR is open or when debugging the pipeline.

### What the pipeline does

1. Provisions an ephemeral OpenShift cluster (EaaS / Hypershift).
2. Clones `opendatahub-io/kserve` at `refs/tags/<release_tag>`.
3. Runs:

```bash
make e2e-setup-kserve-module \
  PLATFORM=ocp \
  E2E_IMG=quay.io/opendatahub/odh-kserve-module-operator:<release_tag>
make e2e-kserve-module-post-release
```

On failure, Konflux logs include OMC, KServe, and LLMISVC state.

## Local dry run (optional)

To debug tests without Konflux, on CRC or any OpenShift cluster with kubeconfig:

```bash
git clone https://github.com/opendatahub-io/kserve.git && cd kserve
git checkout odh-v3.6   # release tag under test

make e2e-setup-kserve-module \
  PLATFORM=ocp \
  E2E_IMG=quay.io/opendatahub/odh-kserve-module-operator:odh-v3.6
make e2e-kserve-module-post-release
```

This does not validate Konflux cluster provisioning.

## Further reading

- kserve tests: `kserve-module/tests/e2e/test_release_validation.py`, `make e2e-kserve-module-post-release`
- Konflux runbook: [integration-tests/kserve/post-release-smoke.md](https://github.com/opendatahub-io/odh-konflux-central/blob/main/integration-tests/kserve/post-release-smoke.md)
- kserve E2E docs: [kserve-module/docs/tests/test.km-e2e.md](https://github.com/opendatahub-io/kserve/blob/master/kserve-module/docs/tests/test.km-e2e.md)
