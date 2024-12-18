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
	v1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/comparators"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/processors"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/resources"
)

const (
	istioServiceMonitorName = "istiod-monitor"
)

var _ SubResourceReconciler = (*KserveIstioServiceMonitorReconciler)(nil)

type KserveIstioServiceMonitorReconciler struct {
	SingleResourcePerNamespace
	client                client.Client
	serviceMonitorHandler resources.ServiceMonitorHandler
	deltaProcessor        processors.DeltaProcessor
}

func NewKServeIstioServiceMonitorReconciler(client client.Client) *KserveIstioServiceMonitorReconciler {
	return &KserveIstioServiceMonitorReconciler{
		client:                client,
		serviceMonitorHandler: resources.NewServiceMonitorHandler(client),
		deltaProcessor:        processors.NewDeltaProcessor(),
	}
}

func (r *KserveIstioServiceMonitorReconciler) Reconcile(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {
	log.V(1).Info("Creating Istio ServiceMonitor for target namespace")

	// Create Desired resource
	desiredResource, err := r.createDesiredResource(isvc)
	if err != nil {
		return err
	}

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

func (r *KserveIstioServiceMonitorReconciler) Cleanup(ctx context.Context, log logr.Logger, isvcNs string) error {
	log.V(1).Info("Deleting ServiceMonitor object for target namespace")
	return r.serviceMonitorHandler.DeleteServiceMonitor(ctx, types.NamespacedName{Name: istioServiceMonitorName, Namespace: isvcNs})
}

// TODO remove this reconcile loop in future versions
func (r *KserveIstioServiceMonitorReconciler) createDesiredResource(isvc *kservev1beta1.InferenceService) (*v1.ServiceMonitor, error) {
	return nil, nil
}

func (r *KserveIstioServiceMonitorReconciler) getExistingResource(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) (*v1.ServiceMonitor, error) {
	return r.serviceMonitorHandler.FetchServiceMonitor(ctx, log, types.NamespacedName{Name: istioServiceMonitorName, Namespace: isvc.Namespace})
}

func (r *KserveIstioServiceMonitorReconciler) processDelta(ctx context.Context, log logr.Logger, desiredServiceMonitor *v1.ServiceMonitor, existingServiceMonitor *v1.ServiceMonitor) (err error) {
	comparator := comparators.GetServiceMonitorComparator()
	delta := r.deltaProcessor.ComputeDelta(comparator, desiredServiceMonitor, existingServiceMonitor)

	if !delta.HasChanges() {
		log.V(1).Info("No delta found")
		return nil
	}

	if delta.IsAdded() {
		log.V(1).Info("Delta found", "create", desiredServiceMonitor.GetName())
		if err = r.client.Create(ctx, desiredServiceMonitor); err != nil {
			return err
		}
	}
	if delta.IsUpdated() {
		log.V(1).Info("Delta found", "update", existingServiceMonitor.GetName())
		rp := existingServiceMonitor.DeepCopy()
		rp.Annotations = desiredServiceMonitor.Annotations
		rp.Labels = desiredServiceMonitor.Labels
		rp.Spec = desiredServiceMonitor.Spec

		if err = r.client.Update(ctx, rp); err != nil {
			return err
		}
	}
	if delta.IsRemoved() {
		log.V(1).Info("Delta found", "delete", existingServiceMonitor.GetName())
		if err = r.client.Delete(ctx, existingServiceMonitor); err != nil {
			return err
		}
	}
	return nil
}
