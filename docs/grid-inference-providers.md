# Grid providers for LLMInferenceServices

This integration assumes the following capabilities are implemented and enabled
in the deployed Grid version. These issue links define the expected integration
contract; they do not assert the issues' current release status.

| Grid capability | Responsibility |
| --- | --- |
| [#251: derive connection topology](https://github.com/praxis-proxy/grid/issues/251) | Generate local backend connections from provider endpoints and remote provider-hop connections from GridSite transport declarations |
| [#248: carry endpoint base paths](https://github.com/praxis-proxy/grid/issues/248) | Apply the selected provider's endpoint base path at the final backend hop, including shared publisher paths |
| [#266: generate provider-side configuration](https://github.com/praxis-proxy/grid/issues/266) | Generate receiving-side candidate-to-backend mappings and peer-to-provider authorization configuration |
| [#267: reconcile credential and CA mounts](https://github.com/praxis-proxy/grid/issues/267) | Coordinate generated file references, Secret mounts, and reload or rollout through the gateway owner |

The controller publishes one cluster-scoped Grid `InferenceProvider` for each
opted-in LLMInferenceService in an allowed namespace with a reported Gateway
attachment. It uses `grid.praxis.fast/v1alpha1`, following
[praxis-proxy/grid#238](https://github.com/praxis-proxy/grid/issues/238).
Install Grid's CRDs separately; the controller does not install them.

LLMInferenceService finalizers are changed using metadata merge patches with a
resource-version check. Spec and status fields introduced by newer installed
KServe schemas are preserved, as are other controllers' finalizers.

Configure these environment variables on the **odh-model-controller** deployment:

| Variable | Meaning | Requirement or behavior |
| --- | --- | --- |
| `GRID_NAMESPACE_SELECTOR` | Kubernetes `LabelSelector` object in JSON or YAML, with `matchLabels` and `matchExpressions`; `{}` allows all namespaces | Unset disables publishing |
| `GRID_NETWORK_NAME` | Existing GridNetwork name | Required when publishing is enabled |
| `GRID_NAMESPACE` | Namespace of the Grid credentials | Required when publishing is enabled |
| `GRID_SERVICE_ACCOUNT` | Existing service account used by Grid to call models | Required when publishing is enabled |
| `GRID_TOKEN_SECRET` | Populated service account token Secret for that account | Required when publishing is enabled |
| `GRID_SITE_LABELS` | Comma-separated `key=value` labels selecting the hosting GridSite | Empty matches all sites |

For example:

```yaml
env:
  - name: GRID_NAMESPACE_SELECTOR
    value: |
      matchLabels:
        grid.praxis.fast/allowed: "true"
      matchExpressions:
        - key: tenant
          operator: In
          values: [team-a, team-b]
        - key: restricted
          operator: DoesNotExist
  - name: GRID_NETWORK_NAME
    value: grid-local
  - name: GRID_NAMESPACE
    value: grid-system
  - name: GRID_SERVICE_ACCOUNT
    value: grid-model-client
  - name: GRID_TOKEN_SECRET
    value: grid-model-client-token
  - name: GRID_SITE_LABELS
    value: 'grid.praxis.fast/provider-site=rome'
```

`GRID_NAMESPACE_SELECTOR` uses the same object structure as Kubernetes
`spec.selector`. All `matchLabels` entries and `matchExpressions` requirements
must match. Expression operators are `In`, `NotIn`, `Exists`, and `DoesNotExist`.
`In` and `NotIn` require nonempty `values`; `Exists` and `DoesNotExist` must omit
`values` or use an empty list. JSON can also be used directly:

```yaml
- name: GRID_NAMESPACE_SELECTOR
  value: '{"matchLabels":{"grid.praxis.fast/allowed":"true"},"matchExpressions":[{"key":"tenant","operator":"In","values":["team-a","team-b"]}]}'
```

An explicit empty object (`value: '{}'`) matches every namespace. Unset or an
empty string disables publishing. Scalar selectors such as `tenant=team-a` and
`*` are unsupported; provide a LabelSelector object instead. Unknown fields,
duplicate fields, invalid operators, invalid labels, null, and whitespace-only
values fail startup rather than matching every namespace accidentally.

Restart the controller when changing environment variables. Removing
`GRID_NAMESPACE_SELECTOR` disables publishing and removes providers and grants
previously created by this controller.
The Grid CRD is optional; its watch is installed only when the API is available
at startup. Restart after installing that CRD to enable its watch.

Administrators provision the Grid identity and token Secret:

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: grid-model-client
  namespace: grid-system
automountServiceAccountToken: false
---
apiVersion: v1
kind: Secret
metadata:
  name: grid-model-client-token
  namespace: grid-system
  annotations:
    kubernetes.io/service-account.name: grid-model-client
type: kubernetes.io/service-account-token
```

Kubernetes populates the Secret's `token` key and service account UID annotation.
The controller checks both the account and its populated Secret using uncached
reads. It references the Secret through `spec.auth.strategy: bearer_token`, as in
the Grid exports. Configure Grid's gateway-owner integration to project this
credential into the gateway making the final backend call. The controller never
copies or logs the token.
The configured service account should have no other model grants: generated
bindings add permissions, and cannot revoke independently provisioned grants.
The shared account and Secret remain administrator-owned.

An administrator labels allowed namespaces:

```sh
oc label namespace models grid.praxis.fast/allowed=true tenant=team-a
```

The model owner opts in on the LLMInferenceService itself:

```yaml
metadata:
  annotations:
    grid.praxis.fast/enabled: 'true'
```

Only the exact string `true` enables publishing. The controller creates a Role
with `get` on that one `serving.kserve.io/llminferenceservices` resource and binds
it to the configured Grid service account. This matches the existing Gateway
AuthPolicy's Kubernetes TokenReview and model-specific SubjectAccessReview flow;
it does not grant namespace-wide model access or change AuthPolicies. Custom
AuthPolicies must accept the configured account and its model-scoped permission.
Publisher paths and root model-routing requests can use the Gateway policy's
`post` permission on `serving.opendatahub.io/models`, scoped to the qualified model
name. Provision that permission separately when using such policies; the generated
LLMInferenceService `get` grant covers participant paths. MaaS subscription or
delegation policies also require separately provisioned access.

For requests authenticated by Kubernetes TokenReview as exactly
`system:serviceaccount:<GRID_NAMESPACE>:<GRID_SERVICE_ACCOUNT>`, managed Gateway
AuthPolicies preserve the incoming `x-gateway-inference-fairness-id` and
`x-gateway-inference-objective` headers. Each missing header falls back independently
to the existing policy value: fairness uses the configured authentication audience,
and objective uses the Gateway's objective expression (including an annotation
override). Other authenticated callers keep the existing flow-control behavior.
Authentication and model authorization still apply to Grid requests.

Grid must forward the caller's trusted flow-control headers to the model Gateway.
Objective names are resolved using the destination's InferenceObjectives, so
participating sites must give those names consistent meanings. Disabling Grid
publishing removes the Grid identity exception when the controller restarts.

KServe must report `status.router.gateways` and a matching Gateway-origin entry
in `status.addresses`. These reported addresses are the routing source of truth;
the Grid controller does not read Gateways, Services, or HTTPRoutes to discover
endpoints. It selects KServe's named `gateway-internal` and
`gateway-internal-model-routing` addresses with a matching reported Gateway origin
and a cluster-local HTTP or HTTPS URL. KServe owns validation of the Gateway
attachment and its backing Service. Public, unnamed, and other address types are
excluded. Cluster DNS names follow KServe's cluster-domain configuration.
When KServe withdraws an address or Gateway attachment from status, the associated
provider and authorization grants are cleaned up. Gateway changes are handled
through KServe's status updates rather than a separate Grid Gateway watch.

Across eligible attachments, the controller prefers shared publisher paths
(`/publishers/<service-namespace>/models/<effective-model-name>`), then root
model-routing URLs when the service belongs to a routing group. These URLs retain
KServe's weighted traffic splitting. Participant paths
(`/<service-namespace>/<service-name>`) are the next fallback, followed by other
addresses supporting the unqualified model and remaining qualified-model addresses.
Within the same routing priority, HTTPS URLs are preferred over HTTP URLs;
remaining ties use the URL's lexical order. HTTP remains a fallback when that
routing priority has no HTTPS address. Only URLs actually reported in
`status.addresses` are used; the controller does not construct group URLs.

The configured model name (or service name if absent or empty) includes
inheritance through `baseRefs`. The provider advertises an identifier supported
by the selected address's `models` list; root model-routing addresses use
`publishers/<service-namespace>/models/<effective-model-name>`. For older status
addresses without a `models` list, KServe's path-dependent naming rules apply.

Names and references follow these formats:

| Resource or field | Name format | Scope or source |
| --- | --- | --- |
| InferenceProvider `metadata.name` | `kmeta.ChildName("grid-<service-namespace>-<normalized-service-name>", "-<service-uid>")` | Cluster-scoped; no namespace |
| Role `metadata.name` | Same generated name as the InferenceProvider | LLMInferenceService namespace |
| RoleBinding `metadata.name` | Same generated name as the InferenceProvider | LLMInferenceService namespace |
| RoleBinding `roleRef.name` | Same generated Role name | References the Role in the binding's namespace |
| Role `rules[].resourceNames[0]` | Original LLMInferenceService `metadata.name` | Full, unmodified service name |
| RoleBinding service account subject | `<GRID_NAMESPACE>/<GRID_SERVICE_ACCOUNT>` | Namespace and name are separate subject fields |
| Provider `spec.auth.secretRef` | `<GRID_NAMESPACE>/<GRID_TOKEN_SECRET>` | Namespace and name are separate reference fields; the key is always `token` |
| Provider `spec.gridNetworkRef` | Exact value of `GRID_NETWORK_NAME` | Existing cluster-scoped GridNetwork name |
| Provider `spec.models[0].name` | Effective model name, or `publishers/<service-namespace>/models/<effective-model-name>` when the selected address requires it | Includes `baseRefs` inheritance; defaults to the original service name when absent or empty; no truncation or hash |
| LLMInferenceService finalizer | `grid.praxis.fast/inference-provider` | Fixed identifier on the source service |
| Controller name | `grid-inference-provider` | Fixed controller-runtime name used in logs and metrics |

The generated resource name uses Knative's `kmeta.ChildName` with:

- Parent: `grid-<service-namespace>-<normalized-service-name>`, replacing each
  `.` in the original service name with `-`.
- Suffix: `-<service-uid>`, using the full UID of the LLMInferenceService.

When the combined name fits within 63 characters, it is used directly. Longer
names are truncated and hashed by `kmeta.ChildName`; the namespace, service name,
and UID all contribute to that result. The provider, Role, and RoleBinding use
this same name.

For example, a service named `qwen.2-7b` in namespace `models`, with UID
`00000000-0000-0000-0000-000000000001`, produces
`grid-models-qwen-2-7b-00000000-0000-0000-0000-000000000001`.
The name remains stable for that service's lifetime; recreating the service with
a new UID changes the name. Different namespaces also produce different names.

Configured names are used verbatim. `GRID_NETWORK_NAME`, `GRID_SERVICE_ACCOUNT`,
and `GRID_TOKEN_SECRET` must be valid DNS subdomain names (lowercase letters,
digits, `-`, and `.`, up to 253 characters). `GRID_NAMESPACE` must be a DNS label
(lowercase letters, digits, and `-`, up to 63 characters). Labels or subdomain
components must start and end with a letter or digit. All four names must be
configured explicitly when publishing is enabled; none has a default.

Every generated provider, Role, and RoleBinding records its source using these
fixed annotation keys and unmodified values:

| Annotation key | Value |
| --- | --- |
| `grid.praxis.fast/source-name` | Original LLMInferenceService name |
| `grid.praxis.fast/source-namespace` | Original LLMInferenceService namespace |
| `grid.praxis.fast/source-uid` | Original LLMInferenceService UID |

These source annotations identify ownership during reconciliation and cleanup.

With the environment configuration above, the example `models/qwen.2-7b` service
and UID produce the following InferenceProvider. This example assumes the
effective model name is `qwen2-7b` and KServe reports a shared publisher URL through
Gateway `infra/model-gateway`, backed by Service `infra/inference-gateway`:

```yaml
apiVersion: grid.praxis.fast/v1alpha1
kind: InferenceProvider
metadata:
  name: grid-models-qwen-2-7b-00000000-0000-0000-0000-000000000001
  labels:
    opendatahub.io/managed: 'true'
  annotations:
    grid.praxis.fast/source-name: qwen.2-7b
    grid.praxis.fast/source-namespace: models
    grid.praxis.fast/source-uid: '00000000-0000-0000-0000-000000000001'
spec:
  gridNetworkRef: grid-local
  providerKind: vllm
  backendKind: local_model
  endpoint: https://inference-gateway.infra.svc.cluster.local/publishers/models/models/qwen2-7b
  models:
    - name: qwen2-7b
      capabilities:
        - text_generation
  siteSelector:
    matchLabels:
      grid.praxis.fast/provider-site: rome
  auth:
    strategy: bearer_token
    manual: false
    secretRef:
      name: grid-model-client-token
      namespace: grid-system
      key: token
```

The resource is cluster-scoped. Server-generated metadata and Grid-managed status
are omitted from this example. Inspect the created resource with:

```sh
oc get inferenceproviders.grid.praxis.fast grid-models-qwen-2-7b-00000000-0000-0000-0000-000000000001 -o yaml
```

The controller manages discovery and auth fields while preserving optional Grid
settings such as `trafficPolicy`, `metricsConfig`, `healthCheck`, and `cost`.
It leaves `gatewayRef` unset on new providers; this optional Grid administrative
identity is independent of the LLMInferenceService's attached Gateway. Existing
administrator-configured `gatewayRef` and `routingClusterRef` values are preserved.

ODH publishes provider endpoints, models, hosting-site selectors, and
credential references. Grid generates Praxis routing and connection configuration;
the gateway owner consumes it and reconciles the required mounts and lifecycle.
ODH does not maintain a second list of Praxis clusters or modify Grid gateway
ConfigMaps and Deployments.

New providers omit `routingClusterRef`. Grid's routing identity is the explicit
`routingClusterRef` when configured, otherwise the InferenceProvider's
`metadata.name`. Grid must use that identity consistently for routing candidates
and generated Praxis connection mappings, including the receiving side of a
remote hop. No separate ODH routing-cluster annotation or mapping ConfigMap is
required. An explicit override remains available for existing Grid configurations.

A routing identity names a Praxis upstream connection configuration. The endpoint
base path and credential reference are separate properties of the selected
provider. Multiple models can share one KServe Gateway while retaining distinct
provider identities, paths, and credentials. Grid must reconcile additions,
removals, endpoint changes, and the new identity produced when a service is
recreated with a different UID.

Administrators supply backend CA and TLS server-identity declarations through
Grid's transport/trust configuration; an HTTPS endpoint alone does not specify
those values. ODH does not infer trust material or invent Grid transport fields.
Configure backend-specific health/metrics TLS and network access separately when
needed; the exported workload health probes are not inferred from service names.
Grid's inference transport must trust HTTPS Gateway certificates.

Removing the annotation, excluding the namespace, stopping the model, withdrawing
its reported routing addresses or Gateway attachment, or deleting the LLMInferenceService revokes
the RoleBinding/Role and deletes the provider. A finalizer holds service deletion
until uncached reads confirm the provider, Role, and RoleBinding are all absent,
including resources held by other finalizers. All deletions are requested even
when another resource is still terminating. Resources with a different recorded
source identity are never overwritten or deleted.
Credential validation repeats every minute and on service account events; a bad
or missing credential revokes generated access and removes the provider.

The envtest Grid CRD fixture is copied unchanged from
`praxis-grid/deploy/crds/inferenceprovider.yaml`, revision
`3aa4d9899f1b0ce5a722ebeba28d122b914e3b6e`. It is used only for testing.

Run the integration tests with:

```sh
make envtest
KUBEBUILDER_ASSETS="$(make -s print-envtest-assets)" POD_NAMESPACE=default \
  go test ./internal/controller/serving/llm -run TestLLMInferenceServiceController \
  -ginkgo.focus='Grid provider reconciliation' -count=1
```

For manual testing, enable the Grid generation and gateway-owner capabilities
above, configure the deployment, provision credentials, label a
namespace, and annotate a ready Gateway-attached service. Confirm its provider's
endpoint/auth reference and the model-scoped RoleBinding. Confirm Grid generates
the matching Praxis connection using the provider name with `routingClusterRef`
unset, then send an inference request through Grid. Remove the annotation and
confirm the provider, generated Grid configuration, and generated
authorization disappear. Repeat by removing the namespace label and deleting the
service. Also verify models sharing a Gateway and service recreation with a new UID.
