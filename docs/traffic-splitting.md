# Traffic splitting

The `odh-model-controller` adds to ModelMesh the capability to configure
percentage-based traffic splits targeting a group of models. Traffic 
splitting is the base feature that allows performing tasks like Canary 
rollouts, Blue/Green deployments, A/B testing, etc.

The models can be grouped together as long as all models in the group can 
accept the same payload structure. You are not limited to using the same 
model format, nor the same ServingRuntime.

## Dependencies

A Service Mesh is required as the supporting routing layer for traffic 
splitting. In OpenShift, it is possible to use [Maistra](https://maistra.io/),
or [OpenShift Service Mesh (OSSM)](https://docs.openshift.com/container-platform/4.12/service_mesh/v2x/ossm-about.html).
It is also possible to use [Istio](https://istio.io).

## Service Mesh preparation

At the moment, the Service Mesh control plane must be installed in the 
`istio-system` namespace. In this same namespace you must deploy an Ingress 
Gateway named `istio-ingressgateway`.

> :warning: NOTE: Most likely, the `istio-ingressgateway` hard names is going 
> to be configurable via some mechanism once the traffic splitting feature is
> stabilized.

Once you have the Service Mesh installed, you must create a Gateway. This is 
the Gateway that will be used to publicly expose models. The following is an 
example of a Gateway:

```yaml
apiVersion: networking.istio.io/v1beta1
kind: Gateway
metadata:
  name: odh-gateway
spec:
  selector:
    istio: ingressgateway
  servers:
  - hosts:
    - '*'
    port:
      name: http
      number: 80
      protocol: HTTP
```

> :bulb: NOTE: When installing the whole ODH platform, the Gateway creation 
> may be handled by the
> [odh-project-controller](https://github.com/maistra/odh-project-controller).

> :warning: If you are using OSSM, make sure that the namespace where the 
> Gateway is created is part of the Mesh; i.e. the namespace is included in 
> your `ServiceMeshMemberRoll` resource. This also may be handled by the 
> [odh-project-controller](https://github.com/maistra/odh-project-controller)
> when installing the whole ODH platform.

## ModelMesh preparation

As usual, you will need a namespace to host your ServingRuntimes, your
models and your applications. This namespace must be part of the Service
Mesh, although you should _not_ enable sidecar auto-injection.
In order to fully enable ODH-managed Service Mesh features,
you must add the `opendatahub.io/service-mesh=true` label to the namespace.
For example, if your namespace is named `modelmesh-apps` you can add the
label with the following command:

```shell
kubectl label ns modelmesh-apps opendatahub.io/service-mesh=true
```

Add the following annotations to your namespace to configure the Istio 
Gateway to use to expose the Inference Services created the namespace:

```shell
kubectl annotate ns modelmesh-apps maistra.io/gateway-namespace=gateway-namespace-name
kubectl annotate ns modelmesh-apps maistra.io/gateway-name=gateway-resource-name
```

> :bulb: When installing the whole ODH platform, these annotations should be 
> set automatically by the [odh-project-controller](https://github.com/maistra/odh-project-controller).

In this namespace, you can create your ServingRuntimes as normally. The only 
additional requirement is that all your ServingRuntimes **must** be 
annotated with `enable-route=true`; e.g.:

```shell
kubectl annotate servingruntime $my_serving_runtime enable-route=true
```

> NOTE: Making `enable-route=true` optional is still a work in progress.

## Configuring traffic splitting

Traffic splitting works by grouping together more than one InferenceService. 
So, start by creating the InferenceServices that you want to include in the 
group. Let's assume there are two InferenceServices named `ai-model-one` and 
`ai-model-two`.

Next, you need to choose the name of the group and label the 
InferenceServices to record the group name. Assuming we would use `ai-model` 
as the name of the group, the InferenceServices are labeled using the 
following commands:

```shell
ISVC_GROUP_NAME=ai-model
kubectl label isvc ai-model-one serving.kserve.io/model-tag=$ISVC_GROUP_NAME
kubectl label isvc ai-model-two serving.kserve.io/model-tag=$ISVC_GROUP_NAME
```

> :bulb: IMPORTANT: Make sure that the group name you choose does not 
> collide with the name of another group, or with the name of any existing 
> InferenceService.

Finally, you must set the percentages of the split using annotations. For 
example:

```shell
kubectl annotate isvc ai-model-one serving.kserve.io/canaryTrafficPercent=80
kubectl annotate isvc ai-model-two serving.kserve.io/canaryTrafficPercent=20
```

With this configuration, 80% of the requests are going to be routed to 
`ai-model-one` and the remaining 20% of the requests are going to be routed 
to `ai-model-two`.

> :warning: IMPORTANT: When configuring and re-configuring the group, you 
> must make sure that the percentages add up to 100%. The 
> `odh-model-controller` won't be able to create the split correctly until 
> split percentages are right.

## Querying the group of InferenceServices

Start a port-forwarding session to the Istio Ingress Gateway:

```shell
kubectl port-forward service/istio-ingressgateway 8080:80 -n istio-system
```

When using a Service Mesh as the routing layer, you use a single
`hostname:port` for both REST and GRPC requests. With the port-forward 
session opened with the previous command, you would use `localhost:8080`.

For REST requests, the endpoint has the format 
`http://$HOSTNAME:$PORT/modelmesh/$NAMESPACE/v2/models/$ISVC_GROUP_NAME/infer`.
So far, for our example, you could do an infer request with `curl` using the 
following script:

```shell
HOSTNAME=localhost
PORT=8080
NAMESPACE=modelmesh-apps
ISVC_GROUP_NAME=ai-model
ENDPOINT=http://$HOSTNAME:$PORT/modelmesh/$NAMESPACE/v2/models/$ISVC_GROUP_NAME/infer
curl -X POST $ENDPOINT -d @rest-input.json
```

> NOTE: `rest-input.json` is a file that should contain the payload of the 
> request.

For GRPC requests, you set the `mm-vmodel-id` header to the name of the 
group. For our example, you could do an infer request with `grpcurl` using the
following script:

```shell
HOSTNAME=localhost
PORT=8080
ISVC_GROUP_NAME=ai-model
grpcurl -plaintext -rpc-header "mm-vmodel-id: $ISVC_GROUP_NAME" -proto ./grpc_predict_v2.proto -d "$(cat grpc-input.json)" $HOSTNAME:$PORT inference.GRPCInferenceService.ModelInfer
```

> NOTE: `grpc-input.json` is a file that should contain the payload of the 
> request. The `grpc_predict_v2.proto` [can be found on KServe's repository](https://github.com/kserve/kserve/blob/master/docs/predict-api/v2/grpc_predict_v2.proto).
