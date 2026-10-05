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
LLMInferenceService–Gateway pair in an allowed namespace with a usable reported
attachment and Grid configuration on that Gateway. It uses `grid.praxis.fast/v1alpha1`, following
[praxis-proxy/grid#238](https://github.com/praxis-proxy/grid/issues/238).
Install Grid's CRDs separately; the controller does not install them.

LLMInferenceService finalizers are changed using metadata merge patches with a
resource-version check. Spec and status fields introduced by newer installed
KServe schemas are preserved, as are other controllers' finalizers.

Enable Grid publication on the model **Namespace**, and configure Grid on each
model **Gateway**:

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: models
  labels:
    # Automatically publishes each attached LLMInferenceService in this namespace.
    grid.opendatahub.io/allowed: "true"
---
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: model-gateway
  namespace: infra
  annotations:
    # Configures provider network, credentials, and hosting site selector.
    grid.opendatahub.io/serving: '{"networkName":"grid-local","namespace":"grid-system","serviceAccount":"grid-model-client","tokenSecret":"grid-model-client-token","siteLabels":"grid.praxis.fast/provider-site=local"}'
spec:
  gatewayClassName: openshift-default
  listeners:
    - name: http
      protocol: HTTP
      port: 80
      allowedRoutes:
        namespaces:
          from: All
```

`networkName`, `namespace`, `serviceAccount`, and `tokenSecret` are required.
`siteLabels` is an optional comma-separated string of `key=value` labels;
omitting it matches every GridSite. The annotation must contain a JSON object
with valid resource names and no unknown fields. Restrict Gateway configuration
and namespace eligibility changes to administrators who may grant Grid access.

A Gateway has one authoritative Grid identity. Services in different namespaces
can share it without duplicating credential configuration. A service attached to
two configured Gateways gets two providers, each using its Gateway's network,
credentials, site labels, and reported endpoint. Several listeners or protocol
addresses on the same Gateway still produce only one provider for that service.

No Grid environment variables or per-service opt-in annotation are required.
Namespace label and Gateway annotation changes take effect without restarting
controllers. Removing the namespace label revokes every provider and generated
access grant for services in that namespace. Removing or invalidating a Gateway's
annotation revokes only the providers and grants associated with that Gateway;
providers through other configured Gateways remain. Invalid configuration reports
a reconciliation error and a Warning event on the source LLMInferenceService.

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
oc label namespace models grid.opendatahub.io/allowed=true
oc annotate gateway model-gateway -n infra grid.opendatahub.io/serving='{"networkName":"grid-local","namespace":"grid-system","serviceAccount":"grid-model-client","tokenSecret":"grid-model-client-token","siteLabels":"grid.praxis.fast/provider-site=local"}'
```

For each published service–Gateway pair, the controller creates a model-scoped
Role and RoleBinding granting that Gateway's configured service account access
to the model. Removing one pair's binding preserves grants from other pairs,
even if they use the same account. Managed Gateway AuthPolicies preserve caller
fairness and objective headers only for that Gateway's configured Grid identity.
Gateway annotation changes refresh its policy without affecting other Gateways.
Removing a namespace's eligibility leaves a shared Gateway's identity exception
in place for services in other namespaces; model-scoped access grants enforce
which models the identity can access.

Authentication and model authorization still apply to Grid requests.

Grid must forward the caller's trusted flow-control headers to the model Gateway.
Objective names are resolved using the destination's InferenceObjectives, so
participating sites must give those names consistent meanings. Removing the Gateway
configuration removes its Grid identity exception.

KServe must report `status.router.gateways` and a matching Gateway-origin entry
in `status.addresses`. These reported addresses are the routing source of truth;
the Grid provider controller reads the origin Gateway for configuration, but does
not reconstruct endpoints from Gateways, Services, or HTTPRoutes. It selects KServe's named `gateway-internal` and
`gateway-internal-model-routing` addresses with a matching reported Gateway origin
and a cluster-local HTTP or HTTPS URL. KServe owns validation of the Gateway
attachment and its backing Service. Public, unnamed, and other address types are
excluded. Cluster DNS names follow KServe's cluster-domain configuration.
When KServe withdraws an address or Gateway attachment from status, the associated
provider and authorization grants are cleaned up. Gateway configuration changes and deletion also trigger provider reconciliation.
Gateway references may come from `spec.router.gateway.refs`, inline
`spec.router.route.http.spec.parentRefs`, or referenced HTTPRoutes
(`spec.router.route.http.refs`). KServe reports the resulting attachments in
status; a spec reference alone does not establish a usable endpoint. Gateway
authentication discovery accounts for all three reference paths.

Within each Gateway attachment, the controller prefers shared publisher paths
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
| InferenceProvider `metadata.name` | `kmeta.ChildName("grid-<service-namespace>-<normalized-service-name>", "-<service-uid>-<gateway-digest>")` | Cluster-scoped; no namespace |
| Role `metadata.name` | Same generated name as the InferenceProvider | LLMInferenceService namespace |
| RoleBinding `metadata.name` | Same generated name as the InferenceProvider | LLMInferenceService namespace |
| RoleBinding `roleRef.name` | Same generated Role name | References the Role in the binding's namespace |
| Role `rules[].resourceNames[0]` | Original LLMInferenceService `metadata.name` | Full, unmodified service name |
| RoleBinding service account subject | `<namespace>/<serviceAccount>` | Namespace and name are separate subject fields |
| Provider `spec.auth.secretRef` | `<namespace>/<tokenSecret>` | Namespace and name are separate reference fields; the key is always `token` |
| Provider `spec.gridNetworkRef` | Exact value of `networkName` | Existing cluster-scoped GridNetwork name |
| Provider `spec.models[0].name` | Effective model name, or `publishers/<service-namespace>/models/<effective-model-name>` when the selected address requires it | Includes `baseRefs` inheritance; defaults to the original service name when absent or empty; no truncation or hash |
| LLMInferenceService finalizer | `grid.praxis.fast/inference-provider` | Fixed identifier on the source service |
| Controller name | `grid-inference-provider` | Fixed controller-runtime name used in logs and metrics |

The generated resource name uses Knative's `kmeta.ChildName` with:

- Parent: `grid-<service-namespace>-<normalized-service-name>`, replacing each
  `.` in the original service name with `-`.
- Suffix: `-<service-uid>-<gateway-digest>`, using the full service UID and
  the first 12 hex characters of SHA-256 of
  `<gateway-namespace>/<gateway-name>/<gateway-uid>`.

When the combined name fits within 63 characters, it is used directly. Longer
names are truncated and hashed by `kmeta.ChildName`; the namespace, service name,
and UID all contribute to that result. The provider, Role, and RoleBinding use
this same name.

The name is stable for the lifetime of both source objects. Recreating either
the service or Gateway with a new UID changes it. Different service namespaces,
Gateway namespaces, or Gateway names also produce different names.

Resources created by the previous one-provider-per-service implementation are
removed during reconciliation. Their optional administrative settings must be
reapplied to the new provider resources.

Configured names are used verbatim. `networkName`, `serviceAccount`,
and `tokenSecret` must be valid DNS subdomain names (lowercase letters,
digits, `-`, and `.`, up to 253 characters). `namespace` must be a DNS label
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
| `grid.opendatahub.io/gateway-name` | Attached Gateway name |
| `grid.opendatahub.io/gateway-namespace` | Attached Gateway namespace |
| `grid.opendatahub.io/gateway-uid` | Attached Gateway UID |

These source annotations identify ownership during reconciliation and cleanup.

With the Gateway configuration above, the example `models/qwen.2-7b` service
and UID produce the following InferenceProvider. This example assumes the
effective model name is `qwen2-7b` and KServe reports a shared publisher URL through
Gateway `infra/model-gateway` with UID `00000000-0000-0000-0000-000000000002`,
backed by Service `infra/inference-gateway`:

```yaml
apiVersion: grid.praxis.fast/v1alpha1
kind: InferenceProvider
metadata:
  name: grid-models-qwen-2-7b22b0d4cd55342d2c9a76cfeb523f975e-00000000
  labels:
    opendatahub.io/managed: 'true'
  annotations:
    grid.praxis.fast/source-name: qwen.2-7b
    grid.praxis.fast/source-namespace: models
    grid.praxis.fast/source-uid: '00000000-0000-0000-0000-000000000001'
    grid.opendatahub.io/gateway-name: model-gateway
    grid.opendatahub.io/gateway-namespace: infra
    grid.opendatahub.io/gateway-uid: '00000000-0000-0000-0000-000000000002'
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
      grid.praxis.fast/provider-site: local
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
oc get inferenceproviders.grid.praxis.fast grid-models-qwen-2-7b22b0d4cd55342d2c9a76cfeb523f975e-00000000 -o yaml
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
provider identities and paths, using the shared Gateway's credentials. Grid must reconcile additions,
removals, endpoint changes, and the new identity produced when a service or its Gateway is
recreated with a different UID.

Administrators supply backend CA and TLS server-identity declarations through
Grid's transport/trust configuration; an HTTPS endpoint alone does not specify
those values. ODH does not infer trust material or invent Grid transport fields.
Configure backend-specific health/metrics TLS and network access separately when
needed; the exported workload health probes are not inferred from service names.
Grid's inference transport must trust HTTPS Gateway certificates.

Removing a Gateway annotation or deleting that Gateway revokes its associated
RoleBindings/Roles and providers. Excluding the namespace, stopping the model,
withdrawing reported routing attachments, or deleting the LLMInferenceService
revokes the affected pairs. A finalizer holds service deletion
until uncached reads confirm the provider, Role, and RoleBinding are all absent,
including resources held by other finalizers. All deletions are requested even
when another resource is still terminating. Resources with a different recorded
source identity are never overwritten or deleted.
Publishing failures emit `Warning` events with reason
`InferenceProviderReconcileFailed` on the source LLMInferenceService, naming the
Gateway and error. This includes failures creating or updating a provider,
provisioning its access grants, or validating its Grid configuration/credentials.

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
above, provision credentials, label a model namespace, and annotate its Gateway.
Confirm the provider's endpoint/auth reference and model-scoped RoleBinding.
Confirm Grid generates the matching Praxis connection using the provider name
with `routingClusterRef` unset, then send an inference request through Grid.
Attach the service through two configured Gateways with different identities and
confirm two providers. Exercise both handy Gateway refs and HTTPRoute parentRefs,
including cross-namespace Gateways. Remove one Gateway annotation and confirm
only its provider and authorization disappear. Verify the other Gateway's policy
and provider remain intact. Repeat with namespace label removal, Gateway deletion,
service deletion, and service/Gateway recreation. Reject provider creation and
confirm a Warning event on the LLMInferenceService identifies the failed Gateway.
