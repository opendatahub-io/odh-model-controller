# Flow Control Configuration for AuthPolicy Templates

This document describes how the AuthPolicy templates configure flow control headers
for the Gateway API Inference Extension.

## Overview

Flow control is a system that manages request queuing, prioritization, and fairness
in multi-tenant inference serving environments. It prevents head-of-line blocking,
provides predictable latency guarantees, and ensures fair resource sharing among tenants.

The AuthPolicy templates inject two key headers that the flow control system uses:

| Header                            | Purpose                                                   |
|-----------------------------------|-----------------------------------------------------------|
| `x-gateway-inference-fairness-id` | Groups requests for fair queuing                          |
| `x-gateway-inference-objective`   | Determines request priority via InferenceObjective lookup |

## How Flow Control Works

### Request Lifecycle

1. **Submission**: Request arrives at the gateway with fairness/objective headers
2. **Flow Key Creation**: System creates a composite key from `(FairnessID, Priority)`
3. **Queuing**: Request is added to the appropriate flow queue
4. **Dispatch**: Requests are dispatched based on priority and fairness policies
5. **Completion**: Request proceeds to backend or is rejected/evicted

### Priority and Fairness

Flow control uses a 3-tier dispatch hierarchy:

1. **Priority Band Selection**: Higher priority requests are always processed first
2. **Inter-Flow Fairness**: Among flows at the same priority, fairness policy applies
3. **Intra-Flow Ordering**: Within a flow, requests are processed in order (FCFS by default)

## Default Behavior

### Authenticated Requests

For authenticated requests, the templates set:

| Header      | Default Expression    | Result                                 |
|-------------|-----------------------|----------------------------------------|
| `fairness`  | Static cluster issuer | e.g., `https://kubernetes.default.svc` |
| `objective` | CEL expression        | SA namespace or `"authenticated"`      |

**Default Objective Expression:**

```cel
auth.identity.user.username.startsWith('system:serviceaccount:')
  ? auth.identity.user.username.split(':')[2]
  : 'authenticated'
```

This means:

- **ServiceAccount tokens**: Objective is set to the SA's namespace (e.g., `my-namespace`)
- **User tokens**: Objective is set to `"authenticated"`

### Anonymous Requests

For anonymous (unauthenticated) requests:

| Header      | Value               |
|-------------|---------------------|
| `fairness`  | `"unauthenticated"` |
| `objective` | `"unauthenticated"` |

## Configuring Priority with InferenceObjective

The `x-gateway-inference-objective` header value is used to look up an `InferenceObjective`
resource, which defines the priority level for requests. Higher priority requests are
always served before lower priority requests.

### How Priority Lookup Works

1. The AuthPolicy extracts the objective value (e.g., `tenant-a` for a ServiceAccount in namespace `tenant-a`)
2. The flow control system looks for an `InferenceObjective` resource with that name
3. If found, the `spec.priority` value determines the request's priority
4. If not found, the request defaults to priority `0`

### Example: Setting Priority for a Tenant Namespace

To give requests from namespace `tenant-a` a higher priority when accessing models in
`my-model-namespace`:

```yaml
apiVersion: inference.networking.x-k8s.io/v1alpha2
kind: InferenceObjective
metadata:
  # Must match the objective header value.
  # By default, the namespace of the ServiceAccount accessing the model.
  name: tenant-a
  namespace: my-model-namespace     # Same namespace as the LLMInferenceService or InferencePool
spec:
  priority: 10                      # Higher value = higher priority
  poolRef:
    name: my-inference-pool         # Reference to the InferencePool
```

### Priority Values

| Priority               | Description                                         |
|------------------------|-----------------------------------------------------|
| Positive (e.g., `10`)  | Higher than default, served first                   |
| `0` (default)          | Standard priority when no InferenceObjective exists |
| Negative (e.g., `-10`) | Lower than default, served last                     |

### Example: Multi-Tenant Priority Configuration

```yaml
# Premium tenant - highest priority
apiVersion: inference.networking.x-k8s.io/v1alpha2
kind: InferenceObjective
metadata:
  name: premium-tenant
  namespace: my-model-namespace
spec:
  priority: 100
  poolRef:
    name: my-inference-pool
---
# Standard tenant - default priority
apiVersion: inference.networking.x-k8s.io/v1alpha2
kind: InferenceObjective
metadata:
  name: standard-tenant
  namespace: my-model-namespace
spec:
  priority: 0
  poolRef:
    name: my-inference-pool
---
# Best-effort tenant - lowest priority
apiVersion: inference.networking.x-k8s.io/v1alpha2
kind: InferenceObjective
metadata:
  name: best-effort-tenant
  namespace: my-model-namespace
spec:
  priority: -50
  poolRef:
    name: my-inference-pool
```

With this configuration:

- Requests from `premium-tenant` namespace are always served first
- Requests from `standard-tenant` namespace are served when no premium requests are pending
- Requests from `best-effort-tenant` namespace are only served when no other requests are pending

## Customization Options

### Custom Objective Expression

You can override the objective CEL expression by annotating your Gateway:

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: my-gateway
  namespace: openshift-ingress
  annotations:
    inference.opendatahub.io/objective-expression: "<your-cel-expression>"
spec:
# ...
```

**Example Expressions:**

| Use Case                 | Expression                                    |
|--------------------------|-----------------------------------------------|
| Use first group          | `auth.identity.user.groups[0]`                |
| Static value             | `'my-tenant'`                                 |
| Extract from extra field | `auth.identity.user.extra['custom-field'][0]` |

### CEL Expression Reference

The expression has access to `auth.identity.user` which contains:

| Field      | Type                | Description                                             |
|------------|---------------------|---------------------------------------------------------|
| `username` | string              | User or SA name (e.g., `system:serviceaccount:ns:name`) |
| `uid`      | string              | Unique user identifier                                  |
| `groups`   | []string            | User's group memberships                                |
| `extra`    | map[string][]string | Additional authenticator-provided data                  |

**CEL String Functions:**

- `startsWith(prefix)` - Check if string starts with prefix
- `endsWith(suffix)` - Check if string ends with suffix
- `contains(substr)` - Check if string contains substring
- `split(sep)` - Split string by separator, returns array
- `size()` - Get string length

**Ternary Syntax:**

```cel
condition ? value_if_true : value_if_false
```

## Template Files

| File                                   | Target    | Use Case                         |
|----------------------------------------|-----------|----------------------------------|
| `authpolicy_llm_isvc_userdefined.yaml` | Gateway   | Authenticated requests with RBAC |
| `authpolicy_anonymous.yaml`            | HTTPRoute | Anonymous/public access          |

## Flow Control Outcomes

When requests go through flow control, they can have these outcomes:

| Outcome             | Description                    |
|---------------------|--------------------------------|
| Dispatched          | Request successfully processed |
| Rejected (Capacity) | Queue at maximum capacity      |
| Evicted (TTL)       | Request time-to-live expired   |
| Evicted (Cancelled) | Client disconnected            |

## Related Resources

- **InferenceObjective**: Kubernetes resource that maps objective names to priority levels
- **Gateway**: Can be annotated to customize objective expression
- **AuthPolicy**: Kuadrant resource that configures authentication/authorization
