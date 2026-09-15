# Post-release kserve-module smoke

OpenShift-only validation after an ODH release cut:

- `odh-model-controller` Running, `KServeReady=True`
- One `LLMInferenceService` reaches `Ready=True`

## Where tests live

Implementation is in [opendatahub-io/kserve](https://github.com/opendatahub-io/kserve):

- `kserve-module/tests/e2e/test_release_validation.py` (`post_release` marker)
- `make e2e-kserve-module-post-release`

## Where CI runs

Orchestration is **not** in this repository. Use Konflux integration testing:

- Pipeline: [odh-konflux-central/integration-tests/kserve/post-release-smoke-pipeline.yaml](https://github.com/opendatahub-io/odh-konflux-central/blob/main/integration-tests/kserve/post-release-smoke-pipeline.yaml)
- PipelineRun: [pipelineruns/kserve/kserve-post-release-smoke.yaml](https://github.com/opendatahub-io/odh-konflux-central/blob/main/pipelineruns/kserve/kserve-post-release-smoke.yaml)
- Docs: [integration-tests/kserve/post-release-smoke.md](https://github.com/opendatahub-io/odh-konflux-central/blob/main/integration-tests/kserve/post-release-smoke.md)

The pipeline provisions an ephemeral OpenShift (Hypershift) cluster, checks out kserve
at the release tag, and runs:

```bash
make e2e-setup-kserve-module PLATFORM=ocp E2E_IMG=quay.io/opendatahub/odh-kserve-module-operator:<release_tag>
make e2e-kserve-module-post-release
```

Trigger via Konflux (`/post-release-smoke` on kserve, or manual PipelineRun with
`release_tag` param). GitHub-hosted Actions cannot provision OpenShift and are not
used for this smoke.

JIRA: [RHOAIENG-85268](https://issues.redhat.com/browse/RHOAIENG-85268)
