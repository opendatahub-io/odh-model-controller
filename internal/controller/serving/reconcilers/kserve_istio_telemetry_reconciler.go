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
	"istio.io/api/telemetry/v1alpha1"
	istiotypes "istio.io/api/type/v1beta1"
	telemetryv1alpha1 "istio.io/client-go/pkg/apis/telemetry/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/comparators"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/processors"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/resources"
)

const (
	telemetryName = "enable-prometheus-metrics"
)

var _ SubResourceReconciler = (*KserveIstioTelemetryReconciler)(nil)

type KserveIstioTelemetryReconciler struct {
	SingleResourcePerNamespace
	client           client.Client
	telemetryHandler resources.TelemetryHandler
	deltaProcessor   processors.DeltaProcessor
}

func NewKServeIstioTelemetryReconciler(client client.Client) *KserveIstioTelemetryReconciler {
	return &KserveIstioTelemetryReconciler{
		client:           client,
		telemetryHandler: resources.NewTelemetryHandler(client),
		deltaProcessor:   processors.NewDeltaProcessor(),
	}
}

func (r *KserveIstioTelemetryReconciler) Reconcile(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {
	log.V(1).Info("Creating Istio Telemetry object for target namespace")

	// Create Desired resource
	desiredResource := r.createDesiredResource(isvc)

	// Get Existing resource
	existingResource, err := r.getExistingResource(ctx, log, isvc)
	if err != nil {
		return err
	}

	// Process Delta
	if err = r.processDelta(ctx, log, desiredResource, existingResource); err != nil {
		return err
	}
	return nil
}

func (r *KserveIstioTelemetryReconciler) Cleanup(ctx context.Context, log logr.Logger, isvcNs string) error {
	log.V(1).Info("Deleting Istio Telemetry object for target namespace")
	return r.telemetryHandler.DeleteTelemetry(ctx, types.NamespacedName{Name: telemetryName, Namespace: isvcNs})
}

func (r *KserveIstioTelemetryReconciler) createDesiredResource(isvc *kservev1beta1.InferenceService) *telemetryv1alpha1.Telemetry {
	desiredTelemetry := &telemetryv1alpha1.Telemetry{
		ObjectMeta: metav1.ObjectMeta{
			Name:      telemetryName,
			Namespace: isvc.Namespace,
		},
		Spec: v1alpha1.Telemetry{
			Selector: &istiotypes.WorkloadSelector{
				MatchLabels: map[string]string{
					"component": "predictor",
				},
			},
			Metrics: []*v1alpha1.Metrics{
				{
					Providers: []*v1alpha1.ProviderRef{
						{
							Name: "prometheus",
						},
					},
				},
			},
		},
	}
	return desiredTelemetry
}

func (r *KserveIstioTelemetryReconciler) getExistingResource(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) (*telemetryv1alpha1.Telemetry, error) {
	return r.telemetryHandler.FetchTelemetry(ctx, log, types.NamespacedName{Name: telemetryName, Namespace: isvc.Namespace})
}

func (r *KserveIstioTelemetryReconciler) processDelta(ctx context.Context, log logr.Logger, desiredTelemetry *telemetryv1alpha1.Telemetry, existingTelemetry *telemetryv1alpha1.Telemetry) (err error) {
	comparator := comparators.GetTelemetryComparator()
	delta := r.deltaProcessor.ComputeDelta(comparator, desiredTelemetry, existingTelemetry)

	if !delta.HasChanges() {
		log.V(1).Info("No delta found")
		return nil
	}

	if delta.IsAdded() {
		log.V(1).Info("Delta found", "create", desiredTelemetry.GetName())
		if err = r.client.Create(ctx, desiredTelemetry); err != nil {
			return err
		}
	}
	if delta.IsUpdated() {
		log.V(1).Info("Delta found", "update", existingTelemetry.GetName())
		rp := existingTelemetry.DeepCopy()
		rp.Spec.Selector = desiredTelemetry.Spec.Selector
		rp.Spec.Metrics = desiredTelemetry.Spec.Metrics

		if err = r.client.Update(ctx, rp); err != nil {
			return err
		}
	}
	if delta.IsRemoved() {
		log.V(1).Info("Delta found", "delete", existingTelemetry.GetName())
		if err = r.client.Delete(ctx, existingTelemetry); err != nil {
			return err
		}
	}
	return nil
}
