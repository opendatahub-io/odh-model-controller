# **New llm-d UI Flow Mechanics**

This document details the architectural transformation of the distributed inference (`llm-d`) user interface in OpenShift AI. By moving away from a rigid, flat template system toward a modular and composable framework, the UI now utilizes the LLMInferenceServiceConfig CRD to handle sophisticated deployment logic. This shift aims to minimize model deployment friction while facilitating the use of advanced routing strategies and infrastructure topologies.

**Status:** Approved 
**Author:** [Pierangelo Di Pilato](mailto:pdipilat@redhat.com) [Bartosz Majsak](mailto:bmajsak@redhat.com)  
**Date:** Jun 12, 2026

# **Architectural Overview**

The configuration surface for llm-d is substantial, involving approximately 46 inputs across 11 feature areas. To manage this complexity, the dashboard will now utilize a decoupled selection process where infrastructure topology is defined separately from operational scheduling policies.

## **Core Configuration Objects**

Platform Admins define reusable configuration templates via the LLMInferenceServiceConfig Custom Resource (CR). These resources are now classified using specific labels to drive the UI logic.

| Label Key | Values / Purpose |
| :---- | :---- |
| opendatahub.io/config-type | \<workload-topology-type\>: Defines the physical layout of the deployment [(see below)](#bookmark=id.7nn6x05qcc3). router: Defines routing, flow control, and prioritization policies. *\<any other type to add associated with other steps\>* |
| opendatahub.io/supported-topologies | Applied to router types or any other type that is tied with a topology.  A JSON array of topology types this router configuration is compatible with. |

# **Administrative Setup**

**Important**: The current framework operates on the premise that administrators possess the necessary expertise (there is no way around it until we can validate, discover and auto-assemble configs in a variety of environments and can be officially supported).

## **Workload Topology templates**

Admins must create `LLMInferenceServiceConfig` objects that represent the supported serving architectures. Each resource must include the `config-type: topology-type` label along with one of the following identifiers:

* **workload-single-node**: Standard single-instance deployment.  
* **workload-multi-node-data-parallel**: Replicated model instances across multiple nodes.  
* **workload-single-node-pd**: Prefill/Decode (P/D) disaggregation on a single node.  
* **workload-multi-node-data-parallel-pd**: P/D disaggregation with data-parallel scaling across a cluster.

**Important**: creating a topology template automatically enables that workload topology, workload-single-node is always enabled.

**Note**: the topology type can grow over time, *eventually*, there will be a workload-single-node-epd, etc;

An example `LLMInferenceServiceConfig` single node P/D disaggregation topology defaulting to 2 prefill replicas and 1 decode replica:

```
apiVersion: serving.kserve.io/v1alpha2
kind: LLMInferenceServiceConfig
metadata:
  name: roce-p2-pd-prefill-heavy
  labels:
    opendatahub.io/config-type: workload-single-node-pd
  annotations:
    description: Single node prefill/decode disaggregated serving 2p/1d on roce-p2 network
spec:
  router:
    route: { }
    gateway: { }
    scheduler: { }
  template:
    annotations:
      # RoCE network required for KV cache transfer via RDMA
      k8s.v1.cni.cncf.io/networks: roce-p2
    containers:
      - name: main
        env:
          # Enable RDMA for KV cache transfer
          - name: KSERVE_INFER_ROCE
            value: "true"
          # Pod IP for KV transfer side channel
          - name: VLLM_NIXL_SIDE_CHANNEL_HOST
            valueFrom:
              fieldRef:
                fieldPath: status.podIP
          # UCX configuration for RDMA transport
          - name: UCX_PROTO_INFO
            value: "y"
          - name: UCX_TLS
            value: "rc,sm,self,cuda_copy,cuda_ipc"
        args:
        # Enable KV cache transfer via NixlConnector (RDMA-based)
        - --kv_transfer_config
        - '{"kv_connector":"NixlConnector","kv_role":"kv_consumer"}'
        resources:
          limits:
            cpu: '4'
            memory: 32Gi
            nvidia.com/gpu: "1"
            rdma/roce_gdr: 1
          requests:
            cpu: '2'
            memory: 16Gi
            nvidia.com/gpu: "1"
            rdma/roce_gdr: 1
  prefill:
    # Prefill pool replica count (higher for concurrent prefill requests)
    replicas: 2
    annotations:
      # RoCE network required for KV cache transfer via RDMA
      k8s.v1.cni.cncf.io/networks: roce-p2
    template:
      containers:
        - name: main
          env:
            - name: KSERVE_INFER_ROCE
              value: "true"
            - name: VLLM_NIXL_SIDE_CHANNEL_HOST
              valueFrom:
                fieldRef:
                  fieldPath: status.podIP
            - name: UCX_PROTO_INFO
              value: "y"
            - name: UCX_TLS
              value: "rc,sm,self,cuda_copy,cuda_ipc"
          args:
          # Enable KV cache transfer via NixlConnector (RDMA-based)
          - --kv_transfer_config
          - '{"kv_connector":"NixlConnector","kv_role":"kv_producer"}'
          resources:
            limits:
              cpu: '4'
              memory: 32Gi
              nvidia.com/gpu: "1"
              rdma/roce_gdr: 1
            requests:
              cpu: '2'
              memory: 16Gi
              nvidia.com/gpu: "1"
              rdma/roce_gdr: 1
```

## **Routing templates**

Admins can create `LLMInferenceServiceConfig` objects that represent the admin (or potentially RHOAI) validated configs for routing and flow control.

An example `LLMInferenceServiceConfig` enabling precise-prefix-cache-scorer \+ queue-scorer \+ kv-cache-utilization-scorer which also ensures engine configuration consistency:

```
apiVersion: serving.kserve.io/v1alpha2
kind: LLMInferenceServiceConfig
metadata:
  name: precise-prefix-queue-kv-cache-utilization
  labels:
    opendatahub.io/config-type: router
    opendatahub.io/supported-topologies: '["workload-single-node"]'
  annotations:
    description: Routing profile with precise-prefix-cache-scorer + queue-scorer + kv-cache-utilization-scorer
spec:
  router:
    scheduler:
      config:
        inline:
          apiVersion: inference.networking.x-k8s.io/v1alpha1
          kind: EndpointPickerConfig
          plugins:
            - type: single-profile-handler
            - type: precise-prefix-cache-scorer
              parameters:
                tokenProcessorConfig:
                  blockSize: 64       # Must match vLLM --block-size (default is 16)
                  hashSeed: "42"      # Must match PYTHONHASHSEED in vLLM pods
                kvEventsConfig:
                  zmqEndpoint: "tcp://*:5557"
                indexerConfig:
                  kvBlockIndexConfig:
                    enableMetrics: true                   # Enable Prometheus metrics
                    metricsLoggingInterval: 60000000000   # Log metrics every 60 seconds
            - type: queue-scorer
            - type: kv-cache-utilization-scorer
            - type: max-score-picker
          schedulingProfiles:
            - name: default
              plugins:
                - pluginRef: queue-scorer
                  weight: 2
                - pluginRef: kv-cache-utilization-scorer
                  weight: 2
                - pluginRef: precise-prefix-cache-scorer
                  weight: 3
                - pluginRef: max-score-picker
  template:
    containers:
      - name: main
        args:
        - --prefix-caching-hash-algo
        - sha256_cbor
        - --block-size
        - 64 # must match tokenProcessorConfig
        # Configure vLLM for prefix caching with KV cache event publishing
        - --kv-events-config
        - '{\"enable_kv_cache_events\":true,\"publisher\":\"zmq\",\"endpoint\":\"tcp://{{ ChildName .ObjectMeta.Name `-epp-service` }}:5557\",\"topic\":\"kv@${POD_IP}@{{ .Spec.Model.Name }}\"}'
        env:
          # Pod IP used in KV cache event topic
          - name: POD_IP
            valueFrom:
              fieldRef:
                apiVersion: v1
                fieldPath: status.podIP
          # Hash seed must match scheduler configuration
          - name: PYTHONHASHSEED
            value: "42"

```

# **User Experience: The Deployment Wizard**

The Data Scientist/ML Engineer persona interacts with these configurations through a tiered visibility model designed to hide infrastructure complexity unless required (we could show the LLMInferenceServiceConfig YAML that will be referenced at each step below).

[Miro](https://miro.com/app/board/uXjVHHS7xHk=/?share_link_id=466708939960)

## **Step: Workload Topology Selection**

During the initial deployment phase (Essential Tier), users are presented with "Topology" options. These are populated by filtering LLMInferenceServiceConfig resources where the label is set to topology-type. Selecting a topology pre-fills critical hardware requirements such as replica counts for prefill and decode pools.

When there is no `LLMInferenceServiceConfig` with `opendatahub.io/topology-type` the wizard defaults to single-node topology.

**Important**: the usable `LLMInferenceServiceConfig` that the UI discovers can be in the “system namespace” (eg opendatahub) or in the user namespace

There could be multiple `LLMInferenceServiceConfig` for the same topology-type, the description and a YAML preview can help the user choose the right one.

## **Step: Advanced Router Selection**

In the Advanced Settings section, users can further refine their deployment by selecting a "router" configuration.

1. The UI filters available `LLMInferenceServiceConfig` resources marked as `config-type: router`.  
2. The wizard performs a **compatibility check**: only schedulers that list the user's currently selected `topology-type` in their `supported-topology` label will be available for selection.  
3. The selected scheduler applies operational parameters such as:  
   * Routing policies (e.g., least-loaded, prefix-cache-aware).  
   * Flow control (queue size, backpressure).  
   * Request prioritization levels.

## **Step xyz: Gateway selection, replicas etc**

# **Validation and Metadata**

* **Live Reference vs. Snapshot**: By default, the dashboard should treat these templates as snapshots at deploy time to ensure production stability, though **admins may choose to allow live references** for automated updates.  
  * Admin sets how the config is used opendatahub.io/config-usage: ref|clone   
  * Default to clone

# **Additional future UX improvements**

* Render the effective LLMInferenceService spec  
* Topology View (Graph view)  
* Guided scheduler plugins configuration

# **References**

* [Configuration Composition upstream docs](https://kserve.github.io/website/docs/next/model-serving/generative-inference/llmisvc/llmisvc-config-composition)  
* [UX Miro Board](https://miro.com/app/board/uXjVHHS7xHk=/?share_link_id=466708939960)