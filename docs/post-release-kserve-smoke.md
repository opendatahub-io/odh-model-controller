# Post-release kserve-module smoke

OpenShift-only validation after an ODH Model Serving release cut:

- `odh-model-controller` Running (no restarts), `KServeReady=True`
- One `LLMInferenceService` reaches `Ready=True`

Tests live in [opendatahub-io/kserve](https://github.com/opendatahub-io/kserve)
(`post_release` pytest marker, `make e2e-kserve-module-post-release`).

## When to run

After cutting an ODH release tag and publishing the operator image on Quay.
The smoke does **not** run automatically on PR merges or tag pushes (OpenShift CI
cannot host a tag-regex postsubmit under the generated master jobs today).

Use this repo's **[ODH Release Workflow](https://github.com/opendatahub-io/odh-model-controller/actions/workflows/odh-release.yaml)**
(Actions → *ODH Release Workflow*) to cut component tags. For post-release smoke,
the relevant sequence is:

1. **Cut the kserve tag** — run the workflow with `repository: kserve` and
   `tag_name: odh-vX.Y` (or `-ea1`/`-ea2` suffix if applicable).
2. **Wait for the operator image** — confirm
   `quay.io/opendatahub/odh-kserve-module-operator:<tag>` exists on Quay.
3. **Trigger smoke**
   - Plain `odh-vX.Y`: on any open [opendatahub-io/kserve](https://github.com/opendatahub-io/kserve)
     PR, comment `/test e2e-kserve-module-post-release`.
   - Pre-release (`odh-vX.Y-eaN`, `-rc`, etc.): `/test` will not select that tag;
     run `export RELEASE_TAG=<tag> && bash hack/ci/post-release-smoke.sh` locally
     (same script and pytest flow as CI).
4. **Cut odh-model-controller** (and other components) — the release workflow
   requires the kserve tag to exist before bumping `go.mod` in this repo.

Run the smoke **after step 2** and **before treating the kserve release as validated**
for RHOAI downstream sync. Re-run with the same `/test` comment (or Prow UI) if the
operator image was rebuilt without creating a new tag.

## How to run (OpenShift CI)

Orchestration is an OpenShift CI **optional presubmit** on `opendatahub-io/kserve`:

- **Job:** `pull-ci-opendatahub-io-kserve-master-e2e-kserve-module-post-release`
- **Context:** `ci/prow/e2e-kserve-module-post-release`
- **Trigger:** `/test e2e-kserve-module-post-release` on a kserve PR

```text
# After operator image is on Quay and odh-vX.Y is pushed:
/test e2e-kserve-module-post-release
```

The job provisions an ephemeral Hypershift cluster, checks out the **newest
plain** `odh-vX.Y` tag, installs the published operator image, and runs
`hack/ci/post-release-smoke.sh`. (`/test` cannot pass a tag.)

**Early-access / `-ea` / `-rc`:** if the image/tag under test is e.g.
`odh-v3.6-ea1`, do **not** use `/test` for that tag — auto-resolve ignores
suffixes. Run the same script locally:

```bash
export RELEASE_TAG=odh-v3.6-ea1
bash hack/ci/post-release-smoke.sh
```

**Watch runs:** [OpenShift CI — opendatahub-io/kserve](https://prow.ci.openshift.org/?repo=opendatahub-io%2Fkserve)

**Re-run:** same `/test` comment, or Prow UI → Re-run.

### What the job validates

1. Fresh OpenShift install via kserve-module operator `.../odh-kserve-module-operator:<tag>`.
2. `odh-model-controller` Running, `KServeReady=True`.
3. One `LLMInferenceService` reaches `Ready=True`.

On failure, Prow artifacts include OMC logs, KServe CR, and LLMISVC state.

## Local dry run (optional)

To debug tests without OpenShift CI, on CRC or any OpenShift cluster with kubeconfig:

```bash
git clone https://github.com/opendatahub-io/kserve.git && cd kserve
git checkout odh-v3.6   # release tag under test

export RELEASE_TAG=odh-v3.6
bash hack/ci/post-release-smoke.sh
```

This does not validate Hypershift cluster provisioning.

## Further reading

- kserve tests: `kserve-module/tests/e2e/test_release_validation.py`
- kserve runbook: [docs/dev/post-release-smoke-openshift-ci.md](https://github.com/opendatahub-io/kserve/blob/master/docs/dev/post-release-smoke-openshift-ci.md)
- kserve E2E docs: [kserve-module/docs/tests/test.km-e2e.md](https://github.com/opendatahub-io/kserve/blob/master/kserve-module/docs/tests/test.km-e2e.md)
- OpenShift CI config: [openshift/release `opendatahub-io-kserve-master.yaml`](https://github.com/openshift/release/blob/master/ci-operator/config/opendatahub-io/kserve/opendatahub-io-kserve-master.yaml)
