/*
Copyright 2026.

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

package reconcilers_test

import (
	"testing"

	kservev1alpha2 "github.com/kserve/kserve/pkg/apis/serving/v1alpha2"
	kserveconstants "github.com/kserve/kserve/pkg/constants"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/serving/llm/reconcilers"
)

func TestDesiredPodMonitor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		llmisvc    *kservev1alpha2.LLMInferenceService
		assertFunc func(t *testing.T, pm *monitoringv1.PodMonitor)
	}{
		{
			name: "basic PodMonitor has correct name and namespace",
			llmisvc: &kservev1alpha2.LLMInferenceService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-llm-service",
					Namespace: "test-ns",
				},
			},
			assertFunc: func(t *testing.T, pm *monitoringv1.PodMonitor) {
				t.Helper()
				assert.Equal(t, constants.GetLLMISvcPodMonitorName("my-llm-service"), pm.Name)
				assert.Equal(t, "test-ns", pm.Namespace)
			},
		},
		{
			name: "PodMonitor has discovery label",
			llmisvc: &kservev1alpha2.LLMInferenceService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "svc",
					Namespace: "ns",
				},
			},
			assertFunc: func(t *testing.T, pm *monitoringv1.PodMonitor) {
				t.Helper()
				assert.Equal(t, "true", pm.Labels[constants.RhoaiObservabilityLabel])
			},
		},
		{
			name: "PodMonitor has standard app labels",
			llmisvc: &kservev1alpha2.LLMInferenceService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "svc",
					Namespace: "ns",
				},
			},
			assertFunc: func(t *testing.T, pm *monitoringv1.PodMonitor) {
				t.Helper()
				assert.Equal(t, "llm-monitoring", pm.Labels["app.kubernetes.io/component"])
				assert.Equal(t, "llminferenceservice", pm.Labels["app.kubernetes.io/part-of"])
				assert.Equal(t, "odh-model-controller", pm.Labels["app.kubernetes.io/managed-by"])
			},
		},
		{
			name: "PodMonitor selector targets single-node and multi-node workload pods",
			llmisvc: &kservev1alpha2.LLMInferenceService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-model",
					Namespace: "ns",
				},
			},
			assertFunc: func(t *testing.T, pm *monitoringv1.PodMonitor) {
				t.Helper()
				require.NotNil(t, pm.Spec.Selector.MatchLabels)
				assert.Equal(t, "my-model", pm.Spec.Selector.MatchLabels[kserveconstants.KubernetesAppNameLabelKey])
				assert.Equal(t, kserveconstants.LLMInferenceServicePartOfValue, pm.Spec.Selector.MatchLabels[kserveconstants.KubernetesPartOfLabelKey])

				require.Len(t, pm.Spec.Selector.MatchExpressions, 1)
				expr := pm.Spec.Selector.MatchExpressions[0]
				assert.Equal(t, kserveconstants.KubernetesComponentLabelKey, expr.Key)
				assert.Equal(t, metav1.LabelSelectorOpIn, expr.Operator)
				assert.ElementsMatch(t, []string{
					kserveconstants.LLMComponentWorkload,
					kserveconstants.LLMComponentWorkloadLeader,
					kserveconstants.LLMComponentWorkloadWorker,
				}, expr.Values)
			},
		},
		{
			name: "PodMetricsEndpoint targets vLLM port with 1m interval",
			llmisvc: &kservev1alpha2.LLMInferenceService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "svc",
					Namespace: "ns",
				},
			},
			assertFunc: func(t *testing.T, pm *monitoringv1.PodMonitor) {
				t.Helper()
				require.Len(t, pm.Spec.PodMetricsEndpoints, 1)
				ep := pm.Spec.PodMetricsEndpoints[0]

				require.NotNil(t, ep.Port)
				assert.Equal(t, "http", *ep.Port)
				assert.Equal(t, "/metrics", ep.Path)

				require.NotNil(t, ep.Scheme)
				assert.Equal(t, monitoringv1.Scheme("http"), *ep.Scheme)

				assert.Equal(t, monitoringv1.Duration("1m"), ep.Interval)
			},
		},
		{
			name: "relabeling injects namespace, pod, and service labels",
			llmisvc: &kservev1alpha2.LLMInferenceService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-svc",
					Namespace: "ns",
				},
			},
			assertFunc: func(t *testing.T, pm *monitoringv1.PodMonitor) {
				t.Helper()
				require.Len(t, pm.Spec.PodMetricsEndpoints, 1)
				relabelConfigs := pm.Spec.PodMetricsEndpoints[0].RelabelConfigs
				require.Len(t, relabelConfigs, 3)

				assert.Equal(t, []monitoringv1.LabelName{"__meta_kubernetes_namespace"}, relabelConfigs[0].SourceLabels)
				assert.Equal(t, "namespace", relabelConfigs[0].TargetLabel)
				assert.Equal(t, "replace", relabelConfigs[0].Action)

				assert.Equal(t, []monitoringv1.LabelName{"__meta_kubernetes_pod_name"}, relabelConfigs[1].SourceLabels)
				assert.Equal(t, "pod", relabelConfigs[1].TargetLabel)
				assert.Equal(t, "replace", relabelConfigs[1].Action)

				assert.Equal(t, "llm_inference_service", relabelConfigs[2].TargetLabel)
				require.NotNil(t, relabelConfigs[2].Replacement)
				assert.Equal(t, "my-svc", *relabelConfigs[2].Replacement)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pm := reconcilers.DesiredPodMonitor(tc.llmisvc)
			tc.assertFunc(t, pm)
		})
	}
}

func TestSingleNodePodSelector(t *testing.T) {
	t.Parallel()

	const llmisvcName = "my-model"
	selector := reconcilers.SingleNodePodSelector(llmisvcName)

	selectorObj, err := metav1.LabelSelectorAsSelector(&selector)
	require.NoError(t, err)

	tests := []struct {
		name      string
		podLabels map[string]string
		matches   bool
	}{
		{
			name: "matches single-node workload pod (role=both)",
			podLabels: map[string]string{
				kserveconstants.KubernetesAppNameLabelKey:   llmisvcName,
				kserveconstants.KubernetesPartOfLabelKey:    kserveconstants.LLMInferenceServicePartOfValue,
				kserveconstants.KubernetesComponentLabelKey: kserveconstants.LLMComponentWorkload,
				kserveconstants.KServeComponentLabelKey:     kserveconstants.KServeComponentWorkload,
				kserveconstants.LLMDRoleLabelKey:            kserveconstants.LLMDRoleBoth,
			},
			matches: true,
		},
		{
			name: "matches single-node decode pod (role=decode, prefill exists)",
			podLabels: map[string]string{
				kserveconstants.KubernetesAppNameLabelKey:   llmisvcName,
				kserveconstants.KubernetesPartOfLabelKey:    kserveconstants.LLMInferenceServicePartOfValue,
				kserveconstants.KubernetesComponentLabelKey: kserveconstants.LLMComponentWorkload,
				kserveconstants.KServeComponentLabelKey:     kserveconstants.KServeComponentWorkload,
				kserveconstants.LLMDRoleLabelKey:            kserveconstants.LLMDRoleDecode,
			},
			matches: true,
		},
		{
			name: "does not match disaggregated prefill pod",
			podLabels: map[string]string{
				kserveconstants.KubernetesAppNameLabelKey:   llmisvcName,
				kserveconstants.KubernetesPartOfLabelKey:    kserveconstants.LLMInferenceServicePartOfValue,
				kserveconstants.KubernetesComponentLabelKey: kserveconstants.LLMComponentWorkloadPrefill,
				kserveconstants.KServeComponentLabelKey:     kserveconstants.KServeComponentWorkload,
				kserveconstants.LLMDRoleLabelKey:            kserveconstants.LLMDRolePrefill,
			},
			matches: false,
		},
		{
			name: "does not match multi-node leader pod",
			podLabels: map[string]string{
				kserveconstants.KubernetesAppNameLabelKey:   llmisvcName,
				kserveconstants.KubernetesPartOfLabelKey:    kserveconstants.LLMInferenceServicePartOfValue,
				kserveconstants.KubernetesComponentLabelKey: kserveconstants.LLMComponentWorkloadLeader,
				kserveconstants.KServeComponentLabelKey:     kserveconstants.KServeComponentWorkload,
				kserveconstants.LLMDRoleLabelKey:            kserveconstants.LLMDRoleDecode,
			},
			matches: false,
		},
		{
			name: "does not match multi-node worker pod",
			podLabels: map[string]string{
				kserveconstants.KubernetesAppNameLabelKey:   llmisvcName,
				kserveconstants.KubernetesPartOfLabelKey:    kserveconstants.LLMInferenceServicePartOfValue,
				kserveconstants.KubernetesComponentLabelKey: kserveconstants.LLMComponentWorkloadWorker,
				kserveconstants.KServeComponentLabelKey:     kserveconstants.KServeComponentWorkload,
				kserveconstants.LLMDRoleLabelKey:            kserveconstants.LLMDRoleDecode,
			},
			matches: false,
		},
		{
			name: "does not match pod from different LLMInferenceService",
			podLabels: map[string]string{
				kserveconstants.KubernetesAppNameLabelKey:   "other-model",
				kserveconstants.KubernetesPartOfLabelKey:    kserveconstants.LLMInferenceServicePartOfValue,
				kserveconstants.KubernetesComponentLabelKey: kserveconstants.LLMComponentWorkload,
				kserveconstants.KServeComponentLabelKey:     kserveconstants.KServeComponentWorkload,
				kserveconstants.LLMDRoleLabelKey:            kserveconstants.LLMDRoleBoth,
			},
			matches: false,
		},
		{
			name: "does not match unrelated pod",
			podLabels: map[string]string{
				"app": "nginx",
			},
			matches: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.matches, selectorObj.Matches(labels.Set(tc.podLabels)))
		})
	}
}

func TestMultiNodeLWSPodSelector(t *testing.T) {
	t.Parallel()

	const llmisvcName = "my-model"
	selector := reconcilers.MultiNodeLWSPodSelector(llmisvcName)

	selectorObj, err := metav1.LabelSelectorAsSelector(&selector)
	require.NoError(t, err)

	tests := []struct {
		name      string
		podLabels map[string]string
		matches   bool
	}{
		{
			name: "matches multi-node leader pod (role=decode)",
			podLabels: map[string]string{
				kserveconstants.KubernetesAppNameLabelKey:   llmisvcName,
				kserveconstants.KubernetesPartOfLabelKey:    kserveconstants.LLMInferenceServicePartOfValue,
				kserveconstants.KubernetesComponentLabelKey: kserveconstants.LLMComponentWorkloadLeader,
				kserveconstants.KServeComponentLabelKey:     kserveconstants.KServeComponentWorkload,
				kserveconstants.LLMDRoleLabelKey:            kserveconstants.LLMDRoleDecode,
			},
			matches: true,
		},
		{
			name: "matches multi-node leader pod (role=both)",
			podLabels: map[string]string{
				kserveconstants.KubernetesAppNameLabelKey:   llmisvcName,
				kserveconstants.KubernetesPartOfLabelKey:    kserveconstants.LLMInferenceServicePartOfValue,
				kserveconstants.KubernetesComponentLabelKey: kserveconstants.LLMComponentWorkloadLeader,
				kserveconstants.KServeComponentLabelKey:     kserveconstants.KServeComponentWorkload,
				kserveconstants.LLMDRoleLabelKey:            kserveconstants.LLMDRoleBoth,
			},
			matches: true,
		},
		{
			name: "matches multi-node worker pod",
			podLabels: map[string]string{
				kserveconstants.KubernetesAppNameLabelKey:   llmisvcName,
				kserveconstants.KubernetesPartOfLabelKey:    kserveconstants.LLMInferenceServicePartOfValue,
				kserveconstants.KubernetesComponentLabelKey: kserveconstants.LLMComponentWorkloadWorker,
			},
			matches: true,
		},
		{
			name: "does not match single-node workload pod",
			podLabels: map[string]string{
				kserveconstants.KubernetesAppNameLabelKey:   llmisvcName,
				kserveconstants.KubernetesPartOfLabelKey:    kserveconstants.LLMInferenceServicePartOfValue,
				kserveconstants.KubernetesComponentLabelKey: kserveconstants.LLMComponentWorkload,
				kserveconstants.KServeComponentLabelKey:     kserveconstants.KServeComponentWorkload,
				kserveconstants.LLMDRoleLabelKey:            kserveconstants.LLMDRoleBoth,
			},
			matches: false,
		},
		{
			name: "does not match disaggregated prefill leader pod",
			podLabels: map[string]string{
				kserveconstants.KubernetesAppNameLabelKey:   llmisvcName,
				kserveconstants.KubernetesPartOfLabelKey:    kserveconstants.LLMInferenceServicePartOfValue,
				kserveconstants.KubernetesComponentLabelKey: kserveconstants.LLMComponentWorkloadLeaderPrefill,
				kserveconstants.KServeComponentLabelKey:     kserveconstants.KServeComponentWorkload,
				kserveconstants.LLMDRoleLabelKey:            kserveconstants.LLMDRolePrefill,
			},
			matches: false,
		},
		{
			name: "does not match disaggregated prefill worker pod",
			podLabels: map[string]string{
				kserveconstants.KubernetesAppNameLabelKey:   llmisvcName,
				kserveconstants.KubernetesPartOfLabelKey:    kserveconstants.LLMInferenceServicePartOfValue,
				kserveconstants.KubernetesComponentLabelKey: kserveconstants.LLMComponentWorkloadWorkerPrefill,
				kserveconstants.KServeComponentLabelKey:     kserveconstants.KServeComponentWorkload,
				kserveconstants.LLMDRoleLabelKey:            kserveconstants.LLMDRolePrefill,
			},
			matches: false,
		},
		{
			name: "does not match pod from different LLMInferenceService",
			podLabels: map[string]string{
				kserveconstants.KubernetesAppNameLabelKey:   "other-model",
				kserveconstants.KubernetesPartOfLabelKey:    kserveconstants.LLMInferenceServicePartOfValue,
				kserveconstants.KubernetesComponentLabelKey: kserveconstants.LLMComponentWorkloadLeader,
				kserveconstants.KServeComponentLabelKey:     kserveconstants.KServeComponentWorkload,
				kserveconstants.LLMDRoleLabelKey:            kserveconstants.LLMDRoleDecode,
			},
			matches: false,
		},
		{
			name: "does not match unrelated pod",
			podLabels: map[string]string{
				"app": "nginx",
			},
			matches: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.matches, selectorObj.Matches(labels.Set(tc.podLabels)))
		})
	}
}

func TestCombinedPodSelector(t *testing.T) {
	t.Parallel()

	const llmisvcName = "my-model"
	selector := reconcilers.CombinedPodSelector(llmisvcName)

	selectorObj, err := metav1.LabelSelectorAsSelector(&selector)
	require.NoError(t, err)

	tests := []struct {
		name      string
		podLabels map[string]string
		matches   bool
	}{
		{
			name: "matches single-node workload pod",
			podLabels: map[string]string{
				kserveconstants.KubernetesAppNameLabelKey:   llmisvcName,
				kserveconstants.KubernetesPartOfLabelKey:    kserveconstants.LLMInferenceServicePartOfValue,
				kserveconstants.KubernetesComponentLabelKey: kserveconstants.LLMComponentWorkload,
				kserveconstants.KServeComponentLabelKey:     kserveconstants.KServeComponentWorkload,
				kserveconstants.LLMDRoleLabelKey:            kserveconstants.LLMDRoleBoth,
			},
			matches: true,
		},
		{
			name: "matches multi-node leader pod",
			podLabels: map[string]string{
				kserveconstants.KubernetesAppNameLabelKey:   llmisvcName,
				kserveconstants.KubernetesPartOfLabelKey:    kserveconstants.LLMInferenceServicePartOfValue,
				kserveconstants.KubernetesComponentLabelKey: kserveconstants.LLMComponentWorkloadLeader,
				kserveconstants.KServeComponentLabelKey:     kserveconstants.KServeComponentWorkload,
				kserveconstants.LLMDRoleLabelKey:            kserveconstants.LLMDRoleDecode,
			},
			matches: true,
		},
		{
			name: "matches multi-node worker pod",
			podLabels: map[string]string{
				kserveconstants.KubernetesAppNameLabelKey:   llmisvcName,
				kserveconstants.KubernetesPartOfLabelKey:    kserveconstants.LLMInferenceServicePartOfValue,
				kserveconstants.KubernetesComponentLabelKey: kserveconstants.LLMComponentWorkloadWorker,
			},
			matches: true,
		},
		{
			name: "does not match disaggregated prefill pod",
			podLabels: map[string]string{
				kserveconstants.KubernetesAppNameLabelKey:   llmisvcName,
				kserveconstants.KubernetesPartOfLabelKey:    kserveconstants.LLMInferenceServicePartOfValue,
				kserveconstants.KubernetesComponentLabelKey: kserveconstants.LLMComponentWorkloadPrefill,
				kserveconstants.KServeComponentLabelKey:     kserveconstants.KServeComponentWorkload,
				kserveconstants.LLMDRoleLabelKey:            kserveconstants.LLMDRolePrefill,
			},
			matches: false,
		},
		{
			name: "does not match prefill leader pod",
			podLabels: map[string]string{
				kserveconstants.KubernetesAppNameLabelKey:   llmisvcName,
				kserveconstants.KubernetesPartOfLabelKey:    kserveconstants.LLMInferenceServicePartOfValue,
				kserveconstants.KubernetesComponentLabelKey: kserveconstants.LLMComponentWorkloadLeaderPrefill,
			},
			matches: false,
		},
		{
			name: "does not match prefill worker pod",
			podLabels: map[string]string{
				kserveconstants.KubernetesAppNameLabelKey:   llmisvcName,
				kserveconstants.KubernetesPartOfLabelKey:    kserveconstants.LLMInferenceServicePartOfValue,
				kserveconstants.KubernetesComponentLabelKey: kserveconstants.LLMComponentWorkloadWorkerPrefill,
			},
			matches: false,
		},
		{
			name: "does not match pod from different LLMInferenceService",
			podLabels: map[string]string{
				kserveconstants.KubernetesAppNameLabelKey:   "other-model",
				kserveconstants.KubernetesPartOfLabelKey:    kserveconstants.LLMInferenceServicePartOfValue,
				kserveconstants.KubernetesComponentLabelKey: kserveconstants.LLMComponentWorkload,
			},
			matches: false,
		},
		{
			name: "does not match unrelated pod",
			podLabels: map[string]string{
				"app": "nginx",
			},
			matches: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.matches, selectorObj.Matches(labels.Set(tc.podLabels)))
		})
	}
}
