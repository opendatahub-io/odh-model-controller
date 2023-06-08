# Traffic splitting quick start

This `odh-model-controller` quick start demonstrates how to use the ModelMesh framework with traffic splitting. 

## Prerequisites

- Install ModelMesh as described in the [ModelMesh serving quickstart's installation section](https://github.com/opendatahub-io/modelmesh-serving/blob/main/docs/quickstart.md#1-install-modelmesh-serving). The `odh-model-controller` is a companion controller for [modelmesh-serving](https://github.com/opendatahub-io/modelmesh-serving).

- Install Service Mesh on OpenShift by using one of the following installation methods:

  - Install OpenShift Service Mesh (OSSM) by running the [`deploy.ossm.sh`](../scripts/deploy.ossm.sh) script. Note that on Red Hat OpenShift for AWS, you must uncomment a few lines in the `deploy.ossm.sh` script before you run it.

  - Alternatively, install [Istio](https://istio.io/) with the required `istiod` and `istio-ingressgateway` components. For example, if you [install by using the `istioctl` command](https://istio.io/latest/docs/setup/install/istioctl/), it includes the required `istiod` and `istio-ingressgateway` components.

## Procedure

1. Create a namespace named `opendatahub`:

    ~~~
    kubectl create namespace opendatahub
    ~~~

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
    
1. Install the ODH-Model-Controller:

    ```shell
    # Clone the odh-model-controller repository
    git clone https://github.com/opendatahub-io/odh-model-controller.git

    # Enter into the cloned code path
    cd odh-model-controller

    # Deploy odh-model-controller
    make deploy -e IMG=quay.io/edgarhz/odh-model-controller:service-mesh-integration
    ```

1. If you are using Istio, skip to the next step. 

    If you are using OSSM, enroll both the `opendatahub` and this `modelmesh-serving` namespaces in the mesh:

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

    ~~~
    kubectl annotate namespace modelmesh-serving opendatahub.io/service-mesh=true
    ~~~

1. Annotate the modelmesh-serving namespace to use the Istio Gateway:

   ~~~
   kubectl annotate namespace modelmesh-serving service-mesh.opendatahub.io/public-gateway-name=opendatahub/odh-gateway
   kubectl annotate namespace modelmesh-serving service-mesh.opendatahub.io/public-gateway-host-internal=istio-ingressgateway.istio-system.svc.cluster.local
   ~~~

1. To simulate the deployment of two models, you deploy the SKLearn MNIST sample model twice. 

    Note: The SKLearn MNIST sample model is also used in [the `modelmesh-serving` quick start](https://github.com/opendatahub-io/modelmesh-serving/blob/main/docs/quickstart.md#2-deploy-a-model). 
 
1. Allow route creation in the ServingRuntime:

    ~~~
    kubectl annotate servingruntime -n modelmesh-serving mlserver-0.x enable-route=true
    ~~~

1. Create two InferenceServices that deploy the SKLearn MNIST sample model:

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

> :bulb: To enable a split of 50% traffic to each model, both InferenceServices are labeled with `serving.kserve.io/model-tag: mnist` and annotated with `serving.kserve.io/canaryTrafficPercent: "50"`. 

## Perform an inference request

1. Start a port-forward session to the Ingress Gateway of the Service Mesh:

    ~~~
   kubectl port-forward service/istio-ingressgateway 8080:80 -n istio-system
    ~~~

1. Try a REST infer request:

   ```shell
   curl -X POST -k http://localhost:8080/modelmesh/modelmesh-serving/v2/models/mnist/infer -d '{"inputs": [{ "name": "predict", "shape": [1, 64], "datatype": "FP32", "data": [0.0, 0.0, 1.0, 11.0, 14.0, 15.0, 3.0, 0.0, 0.0, 1.0, 13.0, 16.0, 12.0, 16.0, 8.0, 0.0, 0.0, 8.0, 16.0, 4.0, 6.0, 16.0, 5.0, 0.0, 0.0, 5.0, 15.0, 11.0, 13.0, 14.0, 0.0, 0.0, 0.0, 0.0, 2.0, 12.0, 16.0, 13.0, 0.0, 0.0, 0.0, 0.0, 0.0, 13.0, 16.0, 16.0, 6.0, 0.0, 0.0, 0.0, 0.0, 16.0, 16.0, 16.0, 7.0, 0.0, 0.0, 0.0, 0.0, 11.0, 13.0, 12.0, 1.0, 0.0]}]}'
   ```

1. Try a GRPC infer request:

   ```shell
   grpcurl \
    -plaintext \
    -rpc-header "mm-vmodel-id: mnist" \
    -proto ./grpc_predict_v2.proto \
    -d '{ "inputs": [{ "name": "predict", "shape": [1, 64], "datatype": "FP32", "contents": { "fp32_contents": [0.0, 0.0, 1.0, 11.0, 14.0, 15.0, 3.0, 0.0, 0.0, 1.0, 13.0, 16.0, 12.0, 16.0, 8.0, 0.0, 0.0, 8.0, 16.0, 4.0, 6.0, 16.0, 5.0, 0.0, 0.0, 5.0, 15.0, 11.0, 13.0, 14.0, 0.0, 0.0, 0.0, 0.0, 2.0, 12.0, 16.0, 13.0, 0.0, 0.0, 0.0, 0.0, 0.0, 13.0, 16.0, 16.0, 6.0, 0.0, 0.0, 0.0, 0.0, 16.0, 16.0, 16.0, 7.0, 0.0, 0.0, 0.0, 0.0, 11.0, 13.0, 12.0, 1.0, 0.0] }}]}' \
    localhost:8080 \
    inference.GRPCInferenceService.ModelInfer
   ```

    > :bulb: You can download the `grpc_predict_v2.proto` file from 
    > [KServe's repository](https://github.com/kserve/kserve/blob/master/docs/predict-api/v2/grpc_predict_v2.proto).

1. View the JSON response and check the `model_name` attribute to see that, when you repeat the infer request, the response alternates between the `mnist-v1` and the `mnist-v2` models.

