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

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"

	"github.com/go-logr/logr"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
	v1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/comparators"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/processors"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/resources"
)

var _ SubResourceReconciler = (*KserveRawMetricsServiceMonitorReconciler)(nil)

type KserveRawMetricsServiceMonitorReconciler struct {
	NoResourceRemoval
	client                client.Client
	serviceMonitorHandler resources.ServiceMonitorHandler
	deltaProcessor        processors.DeltaProcessor
}

func NewKServeRawMetricsServiceMonitorReconciler(client client.Client) *KserveRawMetricsServiceMonitorReconciler {
	return &KserveRawMetricsServiceMonitorReconciler{
		client:                client,
		serviceMonitorHandler: resources.NewServiceMonitorHandler(client),
		deltaProcessor:        processors.NewDeltaProcessor(),
	}
}

func (r *KserveRawMetricsServiceMonitorReconciler) Reconcile(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {
	log.V(1).Info("Reconciling Metrics ServiceMonitor for InferenceService")

	// Create Desired resource
	desiredResource, err := r.createDesiredResource(ctx, log, isvc)
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

func (r *KserveRawMetricsServiceMonitorReconciler) createDesiredResource(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) (*v1.ServiceMonitor, error) {
	isvcRuntime, err := utils.FindSupportingRuntimeForISvc(ctx, r.client, log, isvc)
	if err != nil {
		return nil, err
	}

	desiredServiceMonitor := &v1.ServiceMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      getMetricsServiceMonitorName(isvc),
			Namespace: isvc.Namespace,
			Labels: map[string]string{
				constants.RhoaiObservabilityLabel: "true",
			},
		},
		Spec: v1.ServiceMonitorSpec{
			Endpoints: []v1.Endpoint{
				{
					Port:   isvcRuntime.Name + "-metrics",
					Scheme: "http",
				},
			},
			NamespaceSelector: v1.NamespaceSelector{},
			Selector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					"name": getMetricsServiceMonitorName(isvc),
				},
			},
		},
	}
	if err := ctrl.SetControllerReference(isvc, desiredServiceMonitor, r.client.Scheme()); err != nil {
		return nil, err
	}
	return desiredServiceMonitor, nil
}

func (r *KserveRawMetricsServiceMonitorReconciler) getExistingResource(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) (*v1.ServiceMonitor, error) {
	return r.serviceMonitorHandler.FetchServiceMonitor(ctx, log, types.NamespacedName{Name: getMetricsServiceMonitorName(isvc), Namespace: isvc.Namespace})
}

func (r *KserveRawMetricsServiceMonitorReconciler) processDelta(ctx context.Context, log logr.Logger, desiredServiceMonitor *v1.ServiceMonitor, existingServiceMonitor *v1.ServiceMonitor) (err error) {
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

func getMetricsServiceMonitorName(isvc *kservev1beta1.InferenceService) string {
	return isvc.Name + "-metrics"
}
