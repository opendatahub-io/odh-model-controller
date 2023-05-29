# Traffic splitting quickstart

This quickstart guide enumerates the steps needed for using ModelMesh with
traffic splitting. Please, note that this results in a demo/experimental setup.

## Prerequisites

Since `odh-model-controller` is a companion controller
for [modelmesh-serving](https://github.com/opendatahub-io/modelmesh-serving),
you must first install it. Please, follow
[ModelMesh serving quickstart's installation section](https://github.com/opendatahub-io/modelmesh-serving/blob/main/docs/quickstart.md#1-install-modelmesh-serving).

## Install a Service Mesh

If you are using OpenShift, you can use
the [deploy.ossm.sh](../scripts/deploy.ossm.sh) script to quickly install
OpenShift Service Mesh (OSSM). If you are using ROSA, there are a few lines in
the script that you will need to uncomment.

If you are using another Kubernetes flavor (or also OpenShift), you can
use [Istio](https://istio.io/). Use the installation method that better suits
your needs. You must install `istiod` and `istio-ingressgateway` components. If
you are
[installing with `istioctl`](https://istio.io/latest/docs/setup/install/istioctl/),
installing with the command `istioctl install` will provide a setup with these
components.

Once the Service Mesh is installed:

1. Create a namespace named `opendatahub`:

    `kubectl create namespace opendatahub`

1. Create the `odh-gateway` in the `opendatahub` namespace:

    ```shell
    kubectl apply -f - <<EOF
    apiVersion: networking.istio.io/v1beta1
    kind: Gateway
    metadata:
      name: odh-gateway
      namespace: opendatahub
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
    EOF
    ```

## Install ODH-Model-Controller

As noted in the prerequisites, make sure you install `modelmesh-serving` first.
Then, install `odh-model-controller`:

```shell
# Clone the odh-model-controller repository
git clone https://github.com/opendatahub-io/odh-model-controller.git
# Enter into the cloned code path
cd odh-model-controller
# Deploy odh-model-controller
make deploy -e IMG=quay.io/edgarhz/odh-model-controller:service-mesh-integration
```

## Enable ODH Service Mesh features

If
you [installed ModelMesh with the quickstart instructions](https://github.com/opendatahub-io/modelmesh-serving/blob/main/docs/quickstart.md#1-install-modelmesh-serving)
(as mentioned in the [Prerequisites](#prerequisites)) you will deploy models in
the `modelmesh-serving` namespace, because this is the namespace where the
ServingRuntimes are available.

1. If you are using OSSM, enrol both the `opendatahub` and
   this `modelmesh-serving` namespaces in the mesh:

    ```shell
    kubectl apply -f - <<EOF
    apiVersion: maistra.io/v1
    kind: ServiceMeshMemberRoll
    metadata:
      name: default
      namespace: istio-system
    spec:
      members:
      - modelmesh-serving
      - opendatahub
    EOF
    ```

1. Enable ODH Service Mesh features in the `modelmesh-serving` namespace:

    `kubectl label namespace modelmesh-serving opendatahub.io/service-mesh=true`

## Deploy two models

For this quickstart, the same SKLearn MNIST sample
model [as in `modelmesh-serving` quickstart](https://github.com/opendatahub-io/modelmesh-serving/blob/main/docs/quickstart.md#2-deploy-a-model)
is going to be used. The same model is going to be deployed twice, to simulate
two versions of the model

1. Allow route creation in the ServingRuntime

   `kubectl annotate servingruntime -n modelmesh-serving mlserver-0.x enable-route=true`

1. Create two InferenceServices deploying the SKLearn MNIST sample model.

  ```shell
  kubectl apply -n modelmesh-serving -f - <<EOF
  apiVersion: serving.kserve.io/v1beta1
  kind: InferenceService
  metadata:
    name: mnist-v1
    annotations:
      serving.kserve.io/deploymentMode: ModelMesh
      serving.kserve.io/canaryTrafficPercent: "50"
    labels:
      serving.kserve.io/model-tag: mnist
  spec:
    predictor:
      model:
        modelFormat:
          name: sklearn
        storage:
          key: localMinIO
          path: sklearn/mnist-svm.joblib
  ---
  apiVersion: serving.kserve.io/v1beta1
  kind: InferenceService
  metadata:
    name: mnist-v2
    annotations:
      serving.kserve.io/deploymentMode: ModelMesh
      serving.kserve.io/canaryTrafficPercent: "50"
    labels:
      serving.kserve.io/model-tag: mnist
  spec:
    predictor:
      model:
        modelFormat:
          name: sklearn
        storage:
          key: localMinIO
          path: sklearn/mnist-svm.joblib
  EOF
  ```

> :bulb: Notice that both InferenceServices are labeled
> with `serving.kserve.io/model-tag: mnist`, and annotated
> with `serving.kserve.io/canaryTrafficPercent: "50"`. This is what enables a
> split of 50% traffic to each model.

## Perform an inference request

1. Start a port-forward session to the Ingress Gateway of the Service Mesh:

   `kubectl port-forward service/istio-ingressgateway 8080:80 -n istio-system`

1. Try a REST infer request:

   ```shell
   curl -X POST -k http://localhost:8080/modelmesh/modelmesh-serving/v2/models/mnist/infer -d '{"inputs": [{ "name": "predict", "shape": [1, 64], "datatype": "FP32", "data": [0.0, 0.0, 1.0, 11.0, 14.0, 15.0, 3.0, 0.0, 0.0, 1.0, 13.0, 16.0, 12.0, 16.0, 8.0, 0.0, 0.0, 8.0, 16.0, 4.0, 6.0, 16.0, 5.0, 0.0, 0.0, 5.0, 15.0, 11.0, 13.0, 14.0, 0.0, 0.0, 0.0, 0.0, 2.0, 12.0, 16.0, 13.0, 0.0, 0.0, 0.0, 0.0, 0.0, 13.0, 16.0, 16.0, 6.0, 0.0, 0.0, 0.0, 0.0, 16.0, 16.0, 16.0, 7.0, 0.0, 0.0, 0.0, 0.0, 11.0, 13.0, 12.0, 1.0, 0.0]}]}'
   ```

1. Try a GRPC infer request.

   ```shell
   grpcurl \
    -plaintext \
    -rpc-header "mm-vmodel-id: mnist" \
    -proto ./grpc_predict_v2.proto \
    -d '{ "inputs": [{ "name": "predict", "shape": [1, 64], "datatype": "FP32", "contents": { "fp32_contents": [0.0, 0.0, 1.0, 11.0, 14.0, 15.0, 3.0, 0.0, 0.0, 1.0, 13.0, 16.0, 12.0, 16.0, 8.0, 0.0, 0.0, 8.0, 16.0, 4.0, 6.0, 16.0, 5.0, 0.0, 0.0, 5.0, 15.0, 11.0, 13.0, 14.0, 0.0, 0.0, 0.0, 0.0, 2.0, 12.0, 16.0, 13.0, 0.0, 0.0, 0.0, 0.0, 0.0, 13.0, 16.0, 16.0, 6.0, 0.0, 0.0, 0.0, 0.0, 16.0, 16.0, 16.0, 7.0, 0.0, 0.0, 0.0, 0.0, 11.0, 13.0, 12.0, 1.0, 0.0] }}]}' \
    localhost:8080 \
    inference.GRPCInferenceService.ModelInfer
   ```

   > :bulb: Note: The `grpc_predict_v2.proto` file
   > [can be downloaded from KServe's repository](https://github.com/kserve/kserve/blob/master/docs/predict-api/v2/grpc_predict_v2.proto)

Notice that both in the REST and the GRPC case, the request goes to the `mnist`
model, which is the `model-tag` specified in both InferenceServices as a label.
This is what triggers the traffic split routing. By doing the infer request
repeatedly, the response should be alternating between the `mnist-v1` and
the `mnist-v2` models. You can notice this by looking at the `model_name`
attribute in the JSON response.

