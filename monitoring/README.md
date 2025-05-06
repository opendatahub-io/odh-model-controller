# Monitoring

This directory contains the monitoring dashboards for Accelerators and vLLM performance metrics. This includes:

- [NVIDIA](grafana/nvidia/nvidia-vllm-dashboard.yaml) GPU metrics with vLLM Performance Metrics.
- [inputs.env](inputs.env): holds the parameters that need to be updated before deploying the Grafana dashboard.

The Grafana dashboard object is defined as a JSON string as this example:
```yaml
...
spec:
  instanceSelector:
    matchLabels:
      dashboards: grafana
  folder: "vLLM / GPU Metrics"
  json: |
    {
      "annotations": {
        "list": [
          {
            "builtIn": 1,
            "datasource": {
              "type": "$(DATASOURCE)",
              "uid": "grafana"
            },
            "enable": true,
            "hide": true
            "iconColor": "rgba(0, 211, 255, 1)",
            "name": "Annotations & Alerts",
            "type": "dashboard"
          }
        ]
      }
...
#remaining of the JSON definition
```

With this approach, it makes difficult to use kustomize to correctly override the needed parameters in the Dashboard object 
so it can correctly show the metrics deployed in your current namespace for the desired model. 

There are two parameters that needs to be updated to reflect it:

- **NAMESPACE**: the target namespace where the model will be deployed.
- **MODEL_NAME**: the model name as defined in your InferenceService, note that, it will also be used to filter the pod name
  in the Grafana dashboard, which is a filter using regex.

To correctly override it, we will use a helper called `envsubst`, it is available through the `gettext` GNU package.

We will use the `envsubst` command to replace the variables in the JSON string with the actual values provided in the 
[inputs.env](inputs.env) file, example:

```bash
# the following command reads the environment variables from the inputs.env file and exports them to the current shell
# so it can be used by the envsubst command
export $(cat config/grafana/nvidia/inputs.env | xargs)
envsubst '${NAMESPACE} ${MODEL_NAME}' < grafana/nvidia/nvidia-vllm-dashboard.yaml > /tmp/nvidia-vllm-dashboard-replaced.yaml
```

As example, the `inputs.env` file contains this values:
```bash
NAMESPACE=granite
MODEL_NAME=granite318b
```

These values will be replaced by its placeholders in the [dashboard](grafana/nvidia/nvidia-vllm-dashboard.yaml), in the end of
the generated file, pay attention to this section, where the `namespace` and `model_name` inputs are defined:

```json
       {
            "current": {
              "text": "granite",
              "value": "granite"
            },
            "description": "",
            "hide": 1,
            "name": "namespace",
            "options": [
              {
                "selected": true,
                "text": "granite",
                "value": "granite"
              }
            ],
            "query": "granite",
            "type": "textbox"
          },
          {
            "current": {
              "text": "granite318b",
              "value": "granite318b"
            },
            "hide": 2,
            "name": "model_name",
            "query": "granite318b",
            "skipUrlSync": true,
            "type": "constant"
          }
        ]
      },
```

Notice that the model_name and namespace dashboards variables were updated to the values from `inputs.env` file.

Now deploy the Dashboard:
```bash
oc create -f /tmp/nvidia-vllm-dashboard-replaced.yaml
```    


Note, it does not cover Grafana or Prometheus configurations, this is just the dashboard object that will be deployed in the Grafana instance.
For more information please refer to the OpenShift docs.