# **Deployment UI Flows for AI Engineers: Leveraging the Gateway API**

When designing the UI experience for AI Engineers deploying Large Language Models (LLMs) on OpenShift, particularly concerning network ingress using the Gateway API, we must balance ease of use with the principle of least privilege. AI Engineers typically focus on application deployment and may not possess cluster-admin permissions required to provision shared, cluster-wide `Gateway` resources.

The goal is to provide a smooth, self-service flow for development and staging environments, minimizing the need to engage platform administrators for basic networking setup.

Reference: [https://issues.redhat.com/browse/RHAIRFE-883](https://issues.redhat.com/browse/RHAIRFE-883) 

# **Proposed UI Workflow**

The UI flow should prioritize discovery and self-service creation of necessary components within the user's allowed scope.

## **Important Note**

The discussion and proposed UI workflows presented here focus primarily on the *high-level user experience* and the *conceptual integration* of the Kubernetes Gateway API within the OpenShift/AI deployment ecosystem. The specific implementation details, such as which backend component (UI, BFF, or custom operator) is responsible for querying resources, generating YAML, or managing the precise lifecycle are considered secondary for this conceptual phase.

The current priority is validating the user journey, the security model (namespace-scoped self-service vs. cluster-scoped administration), and the resultant architecture. The exact location and technology stack for generating the manifests (e.g., in the UI or an external service) can be refined once the core flows and resource definitions are validated.

## **Flow 1: Gateway Discovery and Selection (User-Namespace Scope)**

The initial step in the LLM deployment process, immediately following model artifact specification and deployment configuration (e.g., resource requests, replicas), focuses on identifying available ingress points.

**Objective:** Check for existing `Gateway` resources that the user can bind their `LLMInferenceService` to, within the confines of their current project/namespace.

1. **Search Logic:** The UI (or BFF or backend) automatically queries for `Gateway` resources that are either:  
   * Defined in the user's current namespace (e.g., `my-llm-project`).  
   * Defined in a shared namespace but explicitly configured to allow that particular namespace.  
2. **Display:**  
   * If one or more appropriate `Gateway` instances are found, present them in a dropdown list.  
   * Display a clear indication of which `Gateway` is *available* for the user to attach their `LLMInferenceService` to.  
3. **User Action:** The AI Engineer can select existing `Gateway(s)` and proceed to the next step (which involves creating/deploying the `LLMInferenceService` linked to the selected Gateway).

| Scenario | Action | Outcome |
| :---- | :---- | :---- |
| **Available Gateways Found** | AI Engineer selects one. | `LLMInferenceService` created, attached to the selected shared/pre-existing `Gateway`. |
| **No Available Gateways Found** | AI Engineer sees the option to create a new one. | Proceed to Flow 2\. |
| **User Chooses Not to Use Existing Gateway** | AI Engineer explicitly selects the 'Create New Gateway \+' option. | Proceed to Flow 2\. |

### **Pseudo-code of the Gateway discovery logic** 

```py
def get_usable_gateway_refs(target_namespace):
    # 1. Fetch all gateways in the cluster
    all_gateways = list_gateways() 
    
    # 2. Initialize the result list
    valid_gateway_refs = []

    # 3. Check RBAC: Can the user create the resource in this namespace?
    # (Checking this once upfront is more efficient if it applies to the whole NS)
    user_has_permission = check_user_rbac("create", "llmisvc", target_namespace)

    if not user_has_permission:
        return [] # Return empty if user has no rights

    # 4. Iterate through Gateways and Listeners
    for gw in all_gateways:
        for listener in gw.listeners:
            
            # Check if this specific listener permits the target namespace
            # (e.g., via AllowedRoutes or NamespaceSelector)
            if listener.allows_namespace(target_namespace):
                
                # Construct the reference object
                ref = {
                    "name": gw.name,
                    "namespace": gw.namespace,
                    "listener": listener.name,
                    "status": gw.status
                }
                
                valid_gateway_refs.append(ref)

    return valid_gateway_refs
```

## **Flow 2: On-Demand Gateway Creation (Dev/Staging Focus)**

If no suitable shared `Gateway` is available, or the user prefers a dedicated setup for a development workload, the UI offers a streamlined option to create a *namespace-scoped* `Gateway`.

This flow empowers the AI Engineer with self-service ingress, suitable for internal development and testing, without violating security policies, requiring manual intervention from platform administrators or infrastructure-specific limitations (on prem with limited or no Load Balancer services).

1. The UI minimizes required inputs.  
   * **Mandatory Field:**   
     * A name for the new `Gateway` (e.g., `<my-team>-gateway`).  
       * Note: Maximum name limits apply (considering also “companion resources” name limits)  
   * **Optional Field:**  
     * Expose LLM service externally (checkbox) \[verbiage TBD\]  
   * **Default Configuration:** The new `Gateway` should default to using a **ClusterIP** type service, meaning it is only accessible from *within* the cluster network initially.  
     * *Rationale:* This addresses the security and permission concern. It doesn't require cloud provider Load Balancer creation (which may require elevated permissions) and is sufficient for basic testing, internal consumption by other services, or if the platform team will expose the ClusterIP externally via a secondary mechanism (e.g., a shared edge router configured to handle traffic to these ClusterIPs).  
     * *Port Configuration:* Default to standard ports (e.g., port 80 for HTTP, port 443 for HTTPS).  
   * If *Expose LLM service externally* is checked, create a Route, in the same user’s namespace, pointing to the Gateway internal ClusterIP Service.  
     * In this case, the URL to show in the UI should be the URL of the LLMInferenceService status replacing the host with the OCP Route host  
2. **Execution:** Upon confirmation, the UI triggers the creation of:  
   * The `Gateway` resource in the user's namespace.  
   * (Optional) OCP Route  
   * The corresponding `LLMInferenceService` attached to this newly created `Gateway`.  
   * … waits for the `LLMInferenceService` to become ready

### **Latency and Developer Experience Trade-offs**

The architectural choice in Flow 2 (On-Demand Gateway Creation with Optional Route), where external exposure is achieved via an OpenShift `Route` pointing to the internal `Gateway`'s Service, introduces a necessary trade-off between simplicity and network performance.

**Potential Downside: Increased Network Hops and Latency**

When the optional `Route` is created, the ingress path for external traffic becomes:

* External Traffic \-\> OpenShift Router (via the `Route`) \-\> Gateway Pod \-\> LLMInferenceService Pod.

This sequence adds an extra layer of processing compared to a configuration where a shared, highly-optimized, cluster-external `Gateway` handles traffic directly. The primary drawback is a minor, but measurable, increase in network latency due to the additional hop through the OpenShift Router (the Route's implementation layer). For high-performance, low-latency AI inference workloads, every millisecond counts, and this added hop could slightly impact throughput or response time.

**Balancing Factor: Frictionless Developer Experience**

This complexity is accepted to provide a crucial benefit for the AI Engineer: **self-service ingress without requiring elevated cluster permissions.**

By creating a namespace-scoped `Gateway` (which is internally exposed via a ClusterIP) and then exposing that ClusterIP via a user-scoped `Route`, the AI Engineer gains immediate external access for testing and development workloads. This pattern:

1. **Eliminates administrative friction:** The engineer does not need to wait for a Platform Administrator to provision a shared, external-facing `Gateway` or allocate external IP resources.  
2. **Enables rapid iteration:** Developers can instantly deploy and test models, making the workflow significantly smoother for development and staging environments where a slight latency penalty is acceptable.  
3. **Promotes team autonomy:** Developers independently manage routes and policies (like rate limiting) within their own namespace, ensuring that configuration changes or errors remain isolated and do not impact other teams.

The additional latency is generally deemed a worthwhile trade-off for the ability to move quickly and own the entire ingress configuration for non-production environments. For production deployments, the recommended path remains Flow 1: utilizing a pre-existing, centrally managed, and optimized shared `Gateway`.

---

# **Implementation Details in OpenShift**

This tiered approach ensures that AI Engineers can quickly iterate on their deployments in development by managing their own ingress endpoints, while platform administrators retain control over the shared, external-facing ingress infrastructure via central `Gateways` configurations.

## **YAML Examples for UI Workflow Steps**

To provide clarity on what the UI workflow generates, here are conceptual YAML manifests corresponding to the resources created in Step 1 (Attaching to an existing Gateway via LLMInferenceService) and Step 2 (On-Demand creation of a Namespace-scoped Gateway and subsequent attachment).

### **Flow 1 YAML: LLMInferenceService attached to an Existing Gateway**

This example assumes a cluster administrator or platform team has already created a shared `Gateway` named `shared-edge-gateway` in a separate namespace (e.g., `openshift-ingress`) and configured it to allow attachment from the user's namespace (`my-llm-project`). The UI creates the `LLMInferenceService` to link to it.

```
apiVersion: serving.kserve.io/v1alpha1
kind: LLMInferenceService
metadata:
  name: my-llm-model-service
  namespace: <user-namespace>
spec:
  # ... 
  gateway:
    refs:
      - name: shared-edge-gateway
        namespace: openshift-ingress
```

### **Flow 2 YAML: On-Demand Namespace-Scoped Gateway and LLMInferenceService**

If the user opts to create a new Gateway (suitable for dev/staging), the UI (or BFF or backend) generates two primary resources in the user's namespace (`my-llm-project`).

#### **1\. Namespace-Scoped Gateway (Default Internal Exposure)**

This Gateway definition uses the minimal configuration, defaulting to a non-external service type (like `ClusterIP`) and a standard port.

```
apiVersion: v1
kind: ConfigMap
metadata:
  name: <gw-name>-config
  namespace: <user-namespace>
data:
  service: |
    metadata:
      annotations:
        service.beta.openshift.io/serving-cert-secret-name: "<gw-name>-gateway-service-tls"
    spec:
      type: ClusterIP
  deployment: |
    spec:
      template:
        spec:
          containers:
          - name: istio-proxy
            env:
            # This is for disconnected envs, can be injected through DSC
            - name: WASM_INSECURE_REGISTRIES
              value: <bastion-mirror-registry>
---
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: <gw-name>
  namespace: <user-namespace>
spec:
  gatewayClassName: data-science-gateway-class
  listeners:
  - name: https
    port: 443
    protocol: HTTPS
    allowedRoutes:
      namespaces:
        from: Same   # Only <user-namespace> can attach LLMs to this GW
    tls:
      mode: Terminate
      certificateRefs:
      - name: <gw-name>-gateway-service-tls
  infrastructure:
    parametersRef:
      group: ""
      kind: ConfigMap
      name: <gw-name>-config
```

#### **2\. LLMInferenceService linked to the New Gateway**

This deploys the LLM's service linked to the `Gateway`.

```
apiVersion: serving.kserve.io/v1alpha1
kind: LLMInferenceService
metadata:
  name: my-llm-model-service
  namespace: <user-namespace>
spec:
  # ... 
  gateway:
    refs:
      - name: <gw-name>
        namespace: <user-namespace>
```

**3\. (Optional) Route linked to the New Gateway Service**

If *Expose LLM service externally* is checked, create a Route, in the same user’s namespace, pointing to the Gateway internal ClusterIP Service.

```
apiVersion: route.openshift.io/v1
kind: Route
metadata:
  name: my-model-dev-route
  namespace: <user-namespace>
spec:
  to:
    kind: Service
    name: <gw-name>-data-science-gateway-class
    weight: 100
  port:
    targetPort: https 
  tls:
    termination: Edge
    insecureEdgeTerminationPolicy: Redirect
  wildcardPolicy: None
```

