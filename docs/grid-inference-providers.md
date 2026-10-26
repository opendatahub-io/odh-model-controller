# Grid providers for LLMInferenceServices

The controller publishes LLMInferenceServices to Grid as `InferenceProvider`
resources (`grid.praxis.fast/v1alpha1`), so Grid can route inference to them from
other sites. Install Grid and its CRDs separately. If the `InferenceProvider` CRD
is installed after the controller starts, restart the controller.

## What the controller creates

For each LLMInferenceService and Gateway pair, the controller creates one
cluster-scoped `InferenceProvider`, plus a Role and RoleBinding with the same name
in the service's namespace. The Role grants the Gateway's Grid service account
`get` on that one LLMInferenceService.

A pair is published when all of the following hold:

- the service's namespace has the label `grid.opendatahub.io/allowed: "true"`
- the Gateway has a valid `grid.opendatahub.io/serving` annotation and is in an
  allowed namespace
- KServe reports the Gateway in `status.router.gateways` and a matching
  cluster-local `gateway-internal` or `gateway-internal-model-routing` address in
  `status.addresses`
- the service is not stopped or deleting
- the configured service account and its populated token Secret exist

A service attached to two configured Gateways gets two providers. When KServe
reports several addresses for one Gateway, the controller prefers the shared
publisher path, then group model routing, then the service's own path, and HTTPS
over HTTP.

## Turn it on

Label the model namespace and annotate the Gateway:

```sh
oc label namespace models grid.opendatahub.io/allowed=true
oc annotate gateway model-gateway -n openshift-ingress grid.opendatahub.io/serving='{"networkName":"grid-local","namespace":"grid-system","serviceAccount":"grid-model-client","tokenSecret":"grid-model-client-token","siteLabels":"grid.praxis.fast/provider-site=local","publishMetrics":true,"metricsAllowPlaintext":true}'
```

The EPP serves metrics over plain HTTP, so Grid needs `metricsAllowPlaintext` to send its
token there. It defaults to false, and without it Grid reports `MetricsAvailable=False`.

Provision the service account and its token Secret in the configured namespace:

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

Give this service account no other model grants. Changes to the label and the
annotation take effect without a restart.

## Configuration

The annotation value is a JSON object. Unknown fields are rejected.

| Key | Required | Meaning |
| --- | --- | --- |
| `networkName` | yes | GridNetwork the provider joins |
| `namespace` | yes | Namespace of the service account and token Secret |
| `serviceAccount` | yes | Grid identity granted access to the model |
| `tokenSecret` | yes | `kubernetes.io/service-account-token` Secret for that account |
| `siteLabels` | no | Comma-separated `key=value` GridSite selector. Empty matches every site |
| `publishMetrics` | no | Publish the EPP metrics endpoint. Default `false` |
| `metricsAllowPlaintext` | no | Allow Grid to scrape an `http` metrics endpoint with its token. Default `false` |

Only Gateways in `openshift-ingress`, `redhat-ods-applications`, or `opendatahub`
may configure Grid. To change the list, set the controller environment variable
`GRID_GATEWAY_NAMESPACES` to a comma-separated list and restart the controller. An
empty value allows no namespace. Limit write access to Gateways in these
namespaces to administrators who may grant Grid access.

## Turn it off

- Remove the namespace label to revoke every provider for services in that
  namespace.
- Remove the Gateway annotation, or delete the Gateway, to revoke only that
  Gateway's providers.
- Stopping or deleting the service, or KServe withdrawing the attachment, revokes
  the affected pairs.

Revoking a pair deletes its provider, Role, RoleBinding, and NetworkPolicy. A
finalizer on the LLMInferenceService holds its deletion until all of them are gone.

## Ownership

| The controller manages | The administrator owns |
| --- | --- |
| Provider `endpoint`, `models`, `siteSelector`, `auth`, `gridNetworkRef`, `providerKind`, `backendKind` | Grid, its CRDs, and the GridNetwork |
| Per-pair Role, RoleBinding, and metrics NetworkPolicy | The service account and token Secret |
| `metricsConfig` while it carries the generated endpoint | Provider `trafficPolicy`, `healthCheck`, `cost`, `gatewayRef`, `routingClusterRef` |
| | Backend TLS trust for HTTPS Gateways |

The provider name is derived from the service and Gateway names and UIDs, so it
changes when either is recreated. Each resource records its source in the
`grid.praxis.fast/source-*` and `grid.opendatahub.io/gateway-*` annotations, and the
controller never overwrites or deletes a resource whose annotations name another
source.

## Metrics publication

With `publishMetrics: true`, the controller reads the EPP Service that KServe
reports in `status.router.scheduler.service`. It uses the Service only if the
LLMInferenceService controls it and it has a port named `metrics`. Otherwise no
`metricsConfig` is published.

The scheme comes from the port's `appProtocol`. A port with `appProtocol: https`
is published as HTTPS, with trust from the `openshift-service-ca.crt` ConfigMap in
the configured `namespace`. Any other value, or none, is published as HTTP, which
is how the EPP serves metrics by default. The resulting field looks like this:

```yaml
spec:
  metricsConfig:
    endpoint: http://qwen2-7b-epp-service.models.svc.cluster.local:9090
    auth:
      type: serviceAccountToken
      allowPlaintext: true
```

Grid refuses to send its token to an `http` endpoint unless `auth.allowPlaintext`
is `true`. The controller sets it only for an HTTP endpoint on a Gateway with
`metricsAllowPlaintext: true`, and removes a `true` value otherwise. It never
changes an `allowPlaintext: false` that an administrator set.

The controller records the endpoint it wrote in the provider annotation
`grid.opendatahub.io/metrics-endpoint`. To take over `metricsConfig`, set a
different `endpoint`. To hand it back, delete `metricsConfig`. Other fields you
add, such as `timeout` or `signalNames`, are preserved.

While it publishes `metricsConfig`, the controller also owns a NetworkPolicy with
the provider's name. It admits pods in the configured `namespace` to the EPP pods'
metrics target port, and is not created for an EPP Service without a selector.

## Troubleshooting

- Publishing failures appear as `Warning` events with reason `InferenceProviderReconcileFailed` on the LLMInferenceService, naming the Gateway.
- No provider: check the namespace label, the Gateway namespace allowlist, and KServe's `status.addresses`.
- Provider removed: the token Secret is missing, unpopulated, or bound to another account. It is rechecked every minute.
- Grid reports `MetricsAvailable=False` on an HTTP endpoint: set `metricsAllowPlaintext: true` or serve metrics over HTTPS.
