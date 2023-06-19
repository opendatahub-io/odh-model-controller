# ODH Model Controller

The controller will watch the Predictor custom resource events to
extend the KServe modelmesh-serving controller behavior with the following
capabilities:

- Openshift ingress controller integration.

It has been developed using **Golang** and
**[Kubebuilder](https://book.kubebuilder.io/quick-start.html)**.

## Implementation detail



## Developer docs

Follow the instructions below if you want to extend the controller
functionality:

### Run unit tests

Unit tests have been developed using the [**Kubernetes envtest
framework**](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/envtest).

Run the following command to execute them:

```shell
make test
```

### Deploy local changes

Build a new image with your local changes and push it to `<YOUR_IMAGE>` (by
default `quay.io/${USER}/odh-model-controller:latest`).

```shell
make -e IMG=<YOUR_IMAGE> docker-build docker-push
```

Deploy the manager using the image in your registry:

```shell
make deploy -e K8S_NAMESPACE=<YOUR_NAMESPACE> -e IMG=<YOUR_IMAGE>
```