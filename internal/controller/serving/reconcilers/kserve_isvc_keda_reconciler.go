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
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ SubResourceReconciler = (*KserveKEDAReconciler)(nil)

// KserveKEDAReconciler allows ISVCs to autoscale on custom Prometheus metrics via KEDA, with secure OpenShift Monitoring
// access. The reconciler automates KEDA/RBAC resource lifecycle.
//
// KServeKEDAReconciler manages KEDA-specific resources (ServiceAccount, Secret, Role, RoleBinding, TriggerAuthentication)
// for Prometheus-based autoscaling.
//   - Creates resources if InferenceService uses KEDA Prometheus external metric.
//   - Adds each InferenceService in a given namespace as non-controlling owner to shared namespaced resources.
//   - Removes InferenceService owner reference if KEDA Prometheus autoscaling is unused or InferenceService deleted.
//   - Cleans up KEDA resources from namespace if no InferenceServices (or LLMInferenceServices, see LLMKEDAReconciler)
//     use KEDA Prometheus autoscaling.
//
// The underlying resources are shared with LLMKEDAReconciler: an InferenceService and an LLMInferenceService in the
// same namespace that both use Prometheus-based KEDA autoscaling co-own the same ServiceAccount/Secret/Role/
// RoleBinding/TriggerAuthentication set, via kedaPrometheusAuthResources.
type KserveKEDAReconciler struct {
	auth *kedaPrometheusAuthResources
}

func NewKServeKEDAReconciler(client client.Client) *KserveKEDAReconciler {
	return &KserveKEDAReconciler{
		auth: &kedaPrometheusAuthResources{client: client},
	}
}

func (k *KserveKEDAReconciler) Reconcile(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {

	log = log.WithName("KserveKEDAReconciler")
	log.V(2).Info("Reconciling InferenceService", "InferenceService", isvc)

	if !hasPrometheusExternalAutoscalingMetric(isvc, log) {
		log.V(1).Info("No Prometheus external autoscaling metric found, KEDA resources not required by this InferenceService. Ensuring InferenceService is removed from owner references.")
		return k.auth.removeOwnerReferenceIfPresent(ctx, log, isvc.Namespace, AsIsvcOwnerRef(isvc))
	}

	log.Info("Reconciling resources")
	if err := k.auth.reconcile(ctx, log, isvc.Namespace, AsIsvcOwnerRef(isvc), isvc.Annotations); err != nil {
		return err
	}
	log.Info("Successfully reconciled KEDA resources")
	return nil
}

func (k *KserveKEDAReconciler) Delete(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {

	log = log.WithName("KserveKEDAReconciler")
	log.V(2).Info("KserveKEDAReconciler.Delete called")

	return k.auth.removeOwnerReferenceIfPresent(ctx, log, isvc.Namespace, AsIsvcOwnerRef(isvc))
}

func (k *KserveKEDAReconciler) Cleanup(ctx context.Context, log logr.Logger, isvcNs string) error {
	log = log.WithName("KserveKEDAReconciler")
	log.V(2).Info("KserveKEDAReconciler.Cleanup called.", "namespace", isvcNs)
	// NOTE: resources are shared with LLMInferenceService (see LLMKEDAReconciler), so even though this is called
	// when no InferenceService remains in the namespace, we must still check for LLMInferenceServices before
	// deleting anything.
	return k.auth.cleanupNamespaceIfUnused(ctx, log, isvcNs)
}

func hasPrometheusExternalAutoscalingMetric(isvc *kservev1beta1.InferenceService, log logr.Logger) bool {
	log.V(1).Info("hasPrometheusExternalAutoscalingMetric", "autoscaling", isvc.Spec.Predictor.AutoScaling)
	if isvc.Spec.Predictor.AutoScaling == nil {
		return false
	}
	for _, m := range isvc.Spec.Predictor.AutoScaling.Metrics {
		if m.External != nil && m.External.Metric.Backend == kservev1beta1.PrometheusBackend {
			return true
		}
	}
	return false
}
