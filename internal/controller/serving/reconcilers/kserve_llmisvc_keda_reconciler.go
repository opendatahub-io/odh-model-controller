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

package reconcilers

import (
	"context"

	"github.com/go-logr/logr"
	kservev1alpha2 "github.com/kserve/kserve/pkg/apis/serving/v1alpha2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// prometheusKEDATriggerType is the KEDA ScaleTriggers.Type value for the Prometheus scaler, as used in
// LLMInferenceService's standalone/direct KEDA scaling (spec.scaling.keda.triggers[].type).
const prometheusKEDATriggerType = "prometheus"

var _ LLMSubResourceReconciler = (*LLMKEDAReconciler)(nil)

// LLMKEDAReconciler allows LLMInferenceServices to autoscale on user-defined Prometheus KEDA triggers
// (spec.scaling.keda), with secure OpenShift Monitoring access, mirroring KserveKEDAReconciler for
// InferenceService.
//
// It creates/removes the LLMInferenceService's owner reference on the same per-namespace KEDA Prometheus
// authentication resources (ServiceAccount, Secret, Role, RoleBinding, TriggerAuthentication) managed by
// kedaPrometheusAuthResources, which InferenceService also shares via KserveKEDAReconciler. Operators reference
// the resulting TriggerAuthentication by name (KEDAPrometheusAuthTriggerAuthName) in their own
// spec.scaling.keda.triggers[].authenticationRef.
//
// Scope: only the standalone/direct KEDA scaling path (spec.scaling.keda / spec.prefill.scaling.keda). The
// WVA-actuator KEDA path (spec.scaling.wva.keda) is configured separately via inferenceservice-config and a
// cluster-scoped ClusterTriggerAuthentication managed outside odh-model-controller.
type LLMKEDAReconciler struct {
	auth *kedaPrometheusAuthResources
}

func NewLLMKEDAReconciler(client client.Client) *LLMKEDAReconciler {
	return &LLMKEDAReconciler{
		auth: &kedaPrometheusAuthResources{client: client},
	}
}

func (k *LLMKEDAReconciler) Reconcile(ctx context.Context, log logr.Logger, llmisvc *kservev1alpha2.LLMInferenceService) error {
	log = log.WithName("LLMKEDAReconciler")
	log.V(2).Info("Reconciling LLMInferenceService", "LLMInferenceService", llmisvc)

	if !hasPrometheusKEDATrigger(llmisvc, log) {
		log.V(1).Info("No direct KEDA Prometheus trigger found, KEDA auth resources not required by this LLMInferenceService. Ensuring LLMInferenceService is removed from owner references.")
		return k.auth.removeOwnerReferenceIfPresent(ctx, log, llmisvc.Namespace, AsLLMIsvcOwnerRef(llmisvc))
	}

	log.Info("Reconciling resources")
	if err := k.auth.reconcile(ctx, log, llmisvc.Namespace, AsLLMIsvcOwnerRef(llmisvc), llmisvc.Annotations); err != nil {
		return err
	}
	log.Info("Successfully reconciled KEDA resources")
	return nil
}

func (k *LLMKEDAReconciler) Delete(ctx context.Context, log logr.Logger, llmisvc *kservev1alpha2.LLMInferenceService) error {
	log = log.WithName("LLMKEDAReconciler")
	log.V(2).Info("LLMKEDAReconciler.Delete called")

	return k.auth.removeOwnerReferenceIfPresent(ctx, log, llmisvc.Namespace, AsLLMIsvcOwnerRef(llmisvc))
}

func (k *LLMKEDAReconciler) Cleanup(ctx context.Context, log logr.Logger, llmIsvcNs string) error {
	log = log.WithName("LLMKEDAReconciler")
	log.V(2).Info("LLMKEDAReconciler.Cleanup called.", "namespace", llmIsvcNs)
	// NOTE: resources are shared with InferenceService (see KserveKEDAReconciler), so even though this is called
	// when no LLMInferenceService remains in the namespace, we must still check for InferenceServices before
	// deleting anything.
	return k.auth.cleanupNamespaceIfUnused(ctx, log, llmIsvcNs)
}

// hasPrometheusKEDATrigger reports whether llmisvc's standalone/direct KEDA scaling configuration (main workload
// and/or disaggregated prefill workload) declares at least one Prometheus-type trigger.
func hasPrometheusKEDATrigger(llmisvc *kservev1alpha2.LLMInferenceService, log logr.Logger) bool {
	log.V(1).Info("hasPrometheusKEDATrigger", "scaling", llmisvc.Spec.Scaling)
	if scalingHasPrometheusTrigger(llmisvc.Spec.Scaling) {
		return true
	}
	if llmisvc.Spec.Prefill != nil && scalingHasPrometheusTrigger(llmisvc.Spec.Prefill.Scaling) {
		return true
	}
	return false
}

func scalingHasPrometheusTrigger(scaling *kservev1alpha2.ScalingSpec) bool {
	if scaling == nil || scaling.KEDA == nil {
		return false
	}
	for _, trigger := range scaling.KEDA.Triggers {
		if trigger.Type == prometheusKEDATriggerType {
			return true
		}
	}
	return false
}
