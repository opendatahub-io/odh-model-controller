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

package reconcilers

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-logr/logr"
	kservev1alpha2 "github.com/kserve/kserve/pkg/apis/serving/v1alpha2"
	kserveconstants "github.com/kserve/kserve/pkg/constants"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/comparators"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/processors"
	parentreconcilers "github.com/opendatahub-io/odh-model-controller/internal/controller/serving/reconcilers"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
)

var _ parentreconcilers.LLMSubResourceReconciler = (*KservePodMonitorReconciler)(nil)

type KservePodMonitorReconciler struct {
	client         client.Client
	scheme         *runtime.Scheme
	deltaProcessor processors.DeltaProcessor
}

func NewKservePodMonitorReconciler(client client.Client, scheme *runtime.Scheme) *KservePodMonitorReconciler {
	return &KservePodMonitorReconciler{
		client:         client,
		scheme:         scheme,
		deltaProcessor: processors.NewDeltaProcessor(),
	}
}

func (r *KservePodMonitorReconciler) Reconcile(ctx context.Context, log logr.Logger, llmisvc *kservev1alpha2.LLMInferenceService) error {
	log.V(1).Info("Reconciling PodMonitor for LLMInferenceService")

	if scrape := llmisvc.GetLabels()[constants.RhoaiObservabilityLabel]; scrape == "false" {
		log.V(1).Info("PodMonitor scraping disabled via label, cleaning up if exists", "name", llmisvc.Name)
		return r.Delete(ctx, log, llmisvc)
	}

	desired := DesiredPodMonitor(llmisvc)

	existing, err := r.getExisting(ctx, llmisvc)
	if err != nil {
		if isPodMonitorNoMatchError(err) {
			log.V(1).Info("PodMonitor CRD not available, skipping reconciliation")
			return nil
		}
		return fmt.Errorf("failed to get existing PodMonitor: %w", err)
	}

	if existing != nil && !utils.IsManagedByOpenDataHub(existing) {
		log.V(1).Info("Skipping PodMonitor reconciliation - not managed by odh-model-controller", "name", desired.Name)
		return nil
	}

	delta := r.deltaProcessor.ComputeDelta(comparators.GetPodMonitorComparator(), desired, existing)
	if !delta.HasChanges() {
		log.V(1).Info("No changes detected for PodMonitor", "name", desired.Name)
		return nil
	}

	if delta.IsAdded() {
		log.Info("Creating PodMonitor", "name", desired.Name)
		if err := controllerutil.SetControllerReference(llmisvc, desired, r.scheme); err != nil {
			return fmt.Errorf("failed to set controller reference for PodMonitor: %w", err)
		}
		if err := r.client.Create(ctx, desired); err != nil {
			return fmt.Errorf("failed to create PodMonitor: %w", err)
		}
	} else if delta.IsUpdated() {
		log.Info("Updating PodMonitor", "name", existing.Name)
		updated := existing.DeepCopy()
		updated.Labels = desired.Labels
		updated.Spec = desired.Spec
		if err := controllerutil.SetControllerReference(llmisvc, updated, r.scheme); err != nil {
			return fmt.Errorf("failed to set controller reference for PodMonitor: %w", err)
		}
		if err := r.client.Update(ctx, updated); err != nil {
			return fmt.Errorf("failed to update PodMonitor: %w", err)
		}
	}

	return nil
}

func (r *KservePodMonitorReconciler) Delete(ctx context.Context, log logr.Logger, llmisvc *kservev1alpha2.LLMInferenceService) error {
	log.V(1).Info("Deleting PodMonitor for LLMInferenceService")

	name := constants.GetLLMISvcPodMonitorName(llmisvc.Name)
	existing := &monitoringv1.PodMonitor{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: name, Namespace: llmisvc.Namespace}, existing); err != nil {
		if apierrors.IsNotFound(err) || isPodMonitorNoMatchError(err) {
			return nil
		}
		return fmt.Errorf("failed to get PodMonitor for deletion: %w", err)
	}

	if !utils.IsManagedByOpenDataHub(existing) {
		log.V(1).Info("Skipping PodMonitor deletion - not managed by odh-model-controller", "name", name)
		return nil
	}

	if err := r.client.Delete(ctx, existing); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete PodMonitor: %w", err)
	}

	log.Info("Deleted PodMonitor", "name", name)
	return nil
}

func (r *KservePodMonitorReconciler) Cleanup(_ context.Context, _ logr.Logger, _ string) error {
	return nil
}

func (r *KservePodMonitorReconciler) getExisting(ctx context.Context, llmisvc *kservev1alpha2.LLMInferenceService) (*monitoringv1.PodMonitor, error) {
	name := constants.GetLLMISvcPodMonitorName(llmisvc.Name)
	existing := &monitoringv1.PodMonitor{}
	err := r.client.Get(ctx, types.NamespacedName{Name: name, Namespace: llmisvc.Namespace}, existing)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return existing, nil
}

// DesiredPodMonitor builds a PodMonitor targeting the vLLM metrics endpoint (named port "http", /metrics)
// for a given LLMInferenceService. The pod selector covers single-node Deployment pods and
// multi-node LeaderWorkerSet pods (leader + workers) using a combined label selector.
func DesiredPodMonitor(llmisvc *kservev1alpha2.LLMInferenceService) *monitoringv1.PodMonitor {
	vllmPort := "http"
	scrapeInterval := monitoringv1.Duration(constants.IntervalValue)
	selector := CombinedPodSelector(llmisvc.Name)

	return &monitoringv1.PodMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      constants.GetLLMISvcPodMonitorName(llmisvc.Name),
			Namespace: llmisvc.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/component":     "llm-monitoring",
				"app.kubernetes.io/part-of":       "llminferenceservice",
				"app.kubernetes.io/managed-by":    "odh-model-controller",
				constants.RhoaiObservabilityLabel: "true",
			},
		},
		Spec: monitoringv1.PodMonitorSpec{
			Selector: selector,
			PodMetricsEndpoints: []monitoringv1.PodMetricsEndpoint{
				{
					Port:     &vllmPort,
					Path:     "/metrics",
					Scheme:   ptr.To(monitoringv1.Scheme("http")),
					Interval: scrapeInterval,
					RelabelConfigs: []monitoringv1.RelabelConfig{
						{
							SourceLabels: []monitoringv1.LabelName{"__meta_kubernetes_namespace"},
							Action:       "replace",
							TargetLabel:  "namespace",
						},
						{
							SourceLabels: []monitoringv1.LabelName{"__meta_kubernetes_pod_name"},
							Action:       "replace",
							TargetLabel:  "pod",
						},
						{
							Replacement: ptr.To(llmisvc.Name),
							TargetLabel: "llm_inference_service",
						},
					},
				},
			},
		},
	}
}

// SingleNodePodSelector returns a label selector that targets vLLM pods created by a standard
// single-node Deployment for the given LLMInferenceService. It selects pods with the
// "llminferenceservice-workload" component label, which excludes multi-node leader/worker pods
// and disaggregated prefill pods.
func SingleNodePodSelector(llmisvcName string) metav1.LabelSelector {
	return metav1.LabelSelector{
		MatchLabels: map[string]string{
			kserveconstants.KubernetesAppNameLabelKey:   llmisvcName,
			kserveconstants.KubernetesPartOfLabelKey:    kserveconstants.LLMInferenceServicePartOfValue,
			kserveconstants.KubernetesComponentLabelKey: kserveconstants.LLMComponentWorkload,
		},
	}
}

// MultiNodeLWSPodSelector returns a label selector that targets all pods (leader + workers)
// belonging to a LeaderWorkerSet-based multi-node vLLM deployment for the given
// LLMInferenceService. It uses a MatchExpressions In operator on the component label to
// select both leader and worker pods while excluding single-node and disaggregated prefill pods.
func MultiNodeLWSPodSelector(llmisvcName string) metav1.LabelSelector {
	return metav1.LabelSelector{
		MatchLabels: map[string]string{
			kserveconstants.KubernetesAppNameLabelKey: llmisvcName,
			kserveconstants.KubernetesPartOfLabelKey:  kserveconstants.LLMInferenceServicePartOfValue,
		},
		MatchExpressions: []metav1.LabelSelectorRequirement{
			{
				Key:      kserveconstants.KubernetesComponentLabelKey,
				Operator: metav1.LabelSelectorOpIn,
				Values: []string{
					kserveconstants.LLMComponentWorkloadLeader,
					kserveconstants.LLMComponentWorkloadWorker,
				},
			},
		},
	}
}

// CombinedPodSelector returns a label selector that targets vLLM pods across both single-node
// Deployment and multi-node LeaderWorkerSet topologies. It selects pods with component labels
// matching workload, workload-leader, or workload-worker while excluding disaggregated prefill pods.
func CombinedPodSelector(llmisvcName string) metav1.LabelSelector {
	return metav1.LabelSelector{
		MatchLabels: map[string]string{
			kserveconstants.KubernetesAppNameLabelKey: llmisvcName,
			kserveconstants.KubernetesPartOfLabelKey:  kserveconstants.LLMInferenceServicePartOfValue,
		},
		MatchExpressions: []metav1.LabelSelectorRequirement{
			{
				Key:      kserveconstants.KubernetesComponentLabelKey,
				Operator: metav1.LabelSelectorOpIn,
				Values: []string{
					kserveconstants.LLMComponentWorkload,
					kserveconstants.LLMComponentWorkloadLeader,
					kserveconstants.LLMComponentWorkloadWorker,
				},
			},
		},
	}
}

func isPodMonitorNoMatchError(err error) bool {
	var noKind *meta.NoKindMatchError
	if errors.As(err, &noKind) {
		return noKind.GroupKind.Group == monitoringv1.SchemeGroupVersion.Group &&
			noKind.GroupKind.Kind == "PodMonitor"
	}

	var noRes *meta.NoResourceMatchError
	if errors.As(err, &noRes) {
		return noRes.PartialResource.Group == monitoringv1.SchemeGroupVersion.Group &&
			noRes.PartialResource.Resource == "podmonitors"
	}

	return false
}
