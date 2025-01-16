/*

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package constants

const (
	// Caikit Standalone
	CaikitMetricsData = `{
        "config": [
			{
				"title": "Requests per 5 minutes",
				"type": "REQUEST_COUNT",
				"queries": [
					{
						"title": "Number of successful incoming requests",
						"query": "round(sum(increase(predict_rpc_count_total{namespace='${NAMESPACE}',code='OK',model_id='${MODEL_NAME}'}[${REQUEST_RATE_INTERVAL}])))"
					},
					{
						"title": "Number of failed incoming requests",
						"query": "round(sum(increase(predict_rpc_count_total{namespace='${NAMESPACE}',code!='OK',model_id='${MODEL_NAME}'}[${REQUEST_RATE_INTERVAL}])))"
					}
				]
			},
			{
				"title": "Average response time (ms)",
				"type": "MEAN_LATENCY",
				"queries": [
					{
						"title": "Average inference latency",
						"query": "sum by (model_id) (rate(predict_caikit_library_duration_seconds_sum{namespace='${NAMESPACE}',model_id='${MODEL_NAME}'}[1m])) / sum by (model_id) (rate(predict_caikit_library_duration_seconds_count{namespace='${NAMESPACE}',model_id='${MODEL_NAME}'}[${RATE_INTERVAL}]))"
					},
					{
						"title": "Average e2e latency",
						"query": "sum by (model_id) (rate(caikit_core_load_model_duration_seconds_sum{namespace='${NAMESPACE}',model_id='${MODEL_NAME}'}[1m]) + rate(predict_caikit_library_duration_seconds_sum{namespace='${NAMESPACE}',model_id='${MODEL_NAME}'}[1m])) / sum by (model_id) (rate(caikit_core_load_model_duration_seconds_count{namespace='${NAMESPACE}',model_id='${MODEL_NAME}'}[${RATE_INTERVAL}]) + rate(predict_caikit_library_duration_seconds_count{namespace='${NAMESPACE}',model_id='${MODEL_NAME}'}[${RATE_INTERVAL}]))"
					}
				]
			},
			{
				"title": "CPU utilization %",
				"type": "CPU_USAGE",
				"queries": [
					{
						"title": "CPU usage",
						"query":  "sum(pod:container_cpu_usage:sum{namespace='${NAMESPACE}', pod=~'${MODEL_NAME}-predictor-.*'})/sum(kube_pod_resource_limit{resource='cpu', pod=~'${MODEL_NAME}-predictor-.*', namespace='${NAMESPACE}'})"
					}
				]
			},
			{
				"title": "Memory utilization %",
				"type": "MEMORY_USAGE",
				"queries": [
					{
						"title": "Memory usage",
						"query":  "sum(container_memory_working_set_bytes{namespace='${NAMESPACE}', pod=~'${MODEL_NAME}-predictor-.*'})/sum(kube_pod_resource_limit{resource='memory', pod=~'${MODEL_NAME}-predictor-.*', namespace='${NAMESPACE}'})"
					}
				]
			}
		]
    }`

	// OpenVino Model Server
	OvmsMetricsData = `{
        "config": [
			{
				"title": "Requests per 5 minutes",
				"type": "REQUEST_COUNT",
				"queries": [
					{
						"title": "Number of successful incoming requests",
						"query": "round(sum(increase(ovms_requests_success{namespace='${NAMESPACE}',name='${MODEL_NAME}'}[${REQUEST_RATE_INTERVAL}])))"
					},
					{
						"title": "Number of failed incoming requests",
						"query": "round(sum(increase(ovms_requests_fail{namespace='${NAMESPACE}',name='${MODEL_NAME}'}[${REQUEST_RATE_INTERVAL}])))"
					}
				]
			},
			{
				"title": "Average response time (ms)",
				"type": "MEAN_LATENCY",
				"queries": [
					{
						"title": "Average inference latency",
						"query": "sum by (name) (rate(ovms_inference_time_us_sum{namespace='${NAMESPACE}', name='${MODEL_NAME}'}[1m])) / sum by (name) (rate(ovms_inference_time_us_count{namespace='${NAMESPACE}', name='${MODEL_NAME}'}[${RATE_INTERVAL}]))"
					},
					{
						"title": "Average e2e latency",
						"query": "sum by (name) (rate(ovms_request_time_us_sum{name='${MODEL_NAME}'}[1m])) / sum by (name) (rate(ovms_request_time_us_count{name='${MODEL_NAME}'}[${RATE_INTERVAL}]))"
					}
				]
			},
			{
				"title": "CPU utilization %",
				"type": "CPU_USAGE",
				"queries": [
					{
						"title": "CPU usage",
						"query":  "sum(pod:container_cpu_usage:sum{namespace='${NAMESPACE}', pod=~'${MODEL_NAME}-predictor-.*'})/sum(kube_pod_resource_limit{resource='cpu', pod=~'${MODEL_NAME}-predictor-.*', namespace='${NAMESPACE}'})"
					}
				]
			},
			{
				"title": "Memory utilization %",
				"type": "MEMORY_USAGE",
				"queries": [
					{
						"title": "Memory usage",
						"query":  "sum(container_memory_working_set_bytes{namespace='${NAMESPACE}', pod=~'${MODEL_NAME}-predictor-.*'})/sum(kube_pod_resource_limit{resource='memory', pod=~'${MODEL_NAME}-predictor-.*', namespace='${NAMESPACE}'})"
					}
				]
			}
		]
    }`

	// Caikit + TGIS
	TgisMetricsData = `{
        "config": [
			{
				"title": "Requests per 5 minutes",
				"type": "REQUEST_COUNT",
				"queries": [
					{
						"title": "Number of successful incoming requests",
						"query": "round(sum(increase(tgi_request_success{namespace='${NAMESPACE}', pod=~'${MODEL_NAME}-predictor-.*'}[${REQUEST_RATE_INTERVAL}])))"
					},
					{
						"title": "Number of failed incoming requests",
						"query": "round(sum(increase(tgi_request_failure{namespace='${NAMESPACE}', pod=~'${MODEL_NAME}-predictor-.*'}[${REQUEST_RATE_INTERVAL}])))"
					}
				]
			},
			{
				"title": "Average response time (ms)",
				"type": "MEAN_LATENCY",
				"queries": [
					{
						"title": "Average inference latency",
						"query": "sum by (pod) (rate(tgi_request_inference_duration_sum{namespace='${NAMESPACE}', pod=~'${MODEL_NAME}-predictor-.*'}[${RATE_INTERVAL}])) / sum by (pod) (rate(tgi_request_inference_duration_count{namespace='${NAMESPACE}', pod=~'${MODEL_NAME}-predictor-.*'}[${RATE_INTERVAL}]))  "
					},
					{
						"title": "Average e2e latency",
						"query": "sum by (pod) (rate(tgi_request_duration_sum{namespace='${NAMESPACE}', pod=~'${MODEL_NAME}-predictor-.*'}[${RATE_INTERVAL}])) / sum by (pod) (rate(tgi_request_duration_count{namespace='${NAMESPACE}', pod=~'${MODEL_NAME}-predictor-.*'}[${RATE_INTERVAL}]))"
					}
				]
			},
			{
				"title": "CPU utilization %",
				"type": "CPU_USAGE",
				"queries": [
					{
						"title": "CPU usage",
						"query":  "sum(pod:container_cpu_usage:sum{namespace='${NAMESPACE}', pod=~'${MODEL_NAME}-predictor-.*'})/sum(kube_pod_resource_limit{resource='cpu', pod=~'${MODEL_NAME}-predictor-.*', namespace='${NAMESPACE}'})"
					}
				]
			},
			{
				"title": "Memory utilization %",
				"type": "MEMORY_USAGE",
				"queries": [
					{
						"title": "Memory usage",
						"query":  "sum(container_memory_working_set_bytes{namespace='${NAMESPACE}', pod=~'${MODEL_NAME}-predictor-.*'})/sum(kube_pod_resource_limit{resource='memory', pod=~'${MODEL_NAME}-predictor-.*', namespace='${NAMESPACE}'})"
					}
				]
			}
		]
    }`

	// vLLM
	VllmMetricsData = `{
        "config": [
			{
				"title": "Requests per 5 minutes",
				"type": "REQUEST_COUNT",
				"queries": [
					{
						"title": "Number of successful incoming requests",
						"query": "round(sum(increase(vllm:request_success_total{namespace='${NAMESPACE}',model_name='${model_name}'}[${REQUEST_RATE_INTERVAL}])))"
					}
				]
			},
			{
				"title": "Average response time (ms)",
				"type": "MEAN_LATENCY",
				"queries": [
					{
						"title": "Average e2e latency",
						"query": "histogram_quantile(0.5, sum(rate(vllm:e2e_request_latency_seconds_bucket{namespace='${NAMESPACE}', model_name='${MODEL_NAME}'}[${RATE_INTERVAL}])) by (le, model_name))"
					}
				]
			},
			{
				"title": "CPU utilization %",
				"type": "CPU_USAGE",
				"queries": [
					{
						"title": "CPU usage",
						"query":  "sum(pod:container_cpu_usage:sum{namespace='${NAMESPACE}', pod=~'${MODEL_NAME}-predictor-.*'})/sum(kube_pod_resource_limit{resource='cpu', pod=~'${MODEL_NAME}-predictor-.*', namespace='${NAMESPACE}'})"
					}
				]
			},
			{
				"title": "Memory utilization %",
				"type": "MEMORY_USAGE",
				"queries": [
					{
						"title": "Memory usage",
						"query":  "sum(container_memory_working_set_bytes{namespace='${NAMESPACE}', pod=~'${MODEL_NAME}-predictor-.*'})/sum(kube_pod_resource_limit{resource='memory', pod=~'${MODEL_NAME}-predictor-.*', namespace='${NAMESPACE}'})"
					}
				]
			}
		]
    }`

	// NVIDIA NIM
	NIMMetricsData = `{
        "config": [
			{
				"title": "Requests per 5 minutes",
				"type": "REQUEST_COUNT",
				"queries": [
					{
						"title": "Number of successful incoming requests",
						"query": "round(sum(increase(request_success_total{namespace='${NAMESPACE}', pod=~'${MODEL_NAME}-predictor-.*'}[${REQUEST_RATE_INTERVAL}])))"
					},
					{
						"title": "Number of failed incoming requests",
						"query": "round(sum(increase(request_failure_total{namespace='${NAMESPACE}', pod=~'${MODEL_NAME}-predictor-.*'}[${REQUEST_RATE_INTERVAL}])))"
					}
				]
			},
			{
				"title": "Average response time (ms)",
				"type": "MEAN_LATENCY",
				"queries": [
					{
						"title": "Average e2e latency",
						"query": "(rate(e2e_request_latency_seconds_sum{namespace='${NAMESPACE}', pod=~'${MODEL_NAME}-predictor-.*'}[${RATE_INTERVAL}]) * 1000) / (rate(e2e_request_latency_seconds_count{namespace='${NAMESPACE}', pod=~'${MODEL_NAME}-predictor-.*'}[${RATE_INTERVAL}]) * 1000)"
					}
				]
			},
			{
				"title": "CPU utilization %",
				"type": "CPU_USAGE",
				"queries": [
					{
						"title": "CPU usage",
						"query":  "sum(pod:container_cpu_usage:sum{namespace='${NAMESPACE}', pod=~'${MODEL_NAME}-predictor-.*'})/sum(kube_pod_resource_limit{resource='cpu', pod=~'${MODEL_NAME}-predictor-.*', namespace='${NAMESPACE}'})"
					}
				]
			},
			{
				"title": "Memory utilization %",
				"type": "MEMORY_USAGE",
				"queries": [
					{
						"title": "Memory usage",
						"query":  "sum(container_memory_working_set_bytes{namespace='${NAMESPACE}', pod=~'${MODEL_NAME}-predictor-.*'})/sum(kube_pod_resource_limit{resource='memory', pod=~'${MODEL_NAME}-predictor-.*', namespace='${NAMESPACE}'})"
					}
				]
			},
			{
				"title": "GPU cache usage over time",
				"type": "KV_CACHE",
				"queries": [
					{
						"title": "GPU cache usage over time",
						"query": "sum_over_time(gpu_cache_usage_perc{namespace='${NAMESPACE}', pod=~'${MODEL_NAME}-predictor-.*'}[${KV_CACHE_SAMPLING_RATE}])"
					}
				]
			},
			{
				"title": "Current running, waiting, and max requests count",
				"type": "CURRENT_REQUESTS",
				"queries": [
					{
						"title": "Requests waiting",
						"query":  "num_requests_waiting{namespace='${NAMESPACE}', pod=~'${MODEL_NAME}-predictor-.*'}"
					},
					{
						"title": "Requests running",
						"query":  "num_requests_running{namespace='${NAMESPACE}', pod=~'${MODEL_NAME}-predictor-.*'}"
					},
					{
						"title": "Max requests",
						"query":  "num_request_max{namespace='${NAMESPACE}', pod=~'${MODEL_NAME}-predictor-.*'}"
					}
				]
			},
			{
				"title": "Tokens count",
				"type": "TOKENS_COUNT",
				"queries": [
					{
						"title": "Total prompts token",
						"query":  "round(rate(prompt_tokens_total{namespace='${NAMESPACE}', pod=~'${MODEL_NAME}-predictor-.*'}[${RATE_INTERVAL}]))"
					},
					{
						"title": "Total generation token",
						"query":  "round(rate(generation_tokens_total{namespace='${NAMESPACE}', pod=~'${MODEL_NAME}-predictor-.*'}[${RATE_INTERVAL}]))"
					}
				]
			},
			{
				"title": "Time to first token",
				"type": "TIME_TO_FIRST_TOKEN",
				"queries": [
					{
						"title": "Time to first token",
						"query": "rate(time_to_first_token_seconds_sum{namespace='${NAMESPACE}', pod=~'${MODEL_NAME}-predictor-.*'}[${RATE_INTERVAL}])"
					}
				]
			},
			{
				"title": "Time per output token",
				"type": "TIME_PER_OUTPUT_TOKEN",
				"queries": [
					{
						"title": "Time per output token",
						"query": "rate(time_per_output_token_seconds_sum{namespace='${NAMESPACE}', pod=~'${MODEL_NAME}-predictor-.*'}[${RATE_INTERVAL}])"
					}
				]
			},
			{
				"title": "Requests outcomes",
				"type": "REQUEST_OUTCOMES",
				"queries": [
					{
						"title": "Number of successful incoming requests",
						"query": "round(sum(increase(request_success_total{namespace='${NAMESPACE}', pod=~'${MODEL_NAME}-predictor-.*'}[${REQUEST_RATE_INTERVAL}])))"
					},
					{
						"title": "Number of failed incoming requests",
						"query": "round(sum(increase(request_failure_total{namespace='${NAMESPACE}', pod=~'${MODEL_NAME}-predictor-.*'}[${REQUEST_RATE_INTERVAL}])))"
					}
				]
			}
		]
    }`
)
