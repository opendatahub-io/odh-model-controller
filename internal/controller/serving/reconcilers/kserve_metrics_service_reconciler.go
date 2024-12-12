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
	"github.com/opendatahub-io/odh-model-controller/internal/controller/comparators"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/processors"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/resources"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	inferenceServiceLabelName = "serving.kserve.io/inferenceservice"
)

var _ SubResourceReconciler = (*KserveMetricsServiceReconciler)(nil)

type KserveMetricsServiceReconciler struct {
	NoResourceRemoval
	client         client.Client
	serviceHandler resources.ServiceHandler
	deltaProcessor processors.DeltaProcessor
}

func NewKServeMetricsServiceReconciler(client client.Client) *KserveMetricsServiceReconciler {
	return &KserveMetricsServiceReconciler{
		client:         client,
		serviceHandler: resources.NewServiceHandler(client),
		deltaProcessor: processors.NewDeltaProcessor(),
	}
}

// TODO remove this reconcile loop in future versions
func (r *KserveMetricsServiceReconciler) Reconcile(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {
	log.V(1).Info("Reconciling Metrics Service for InferenceService, checking if there are resource for deletion")

	// Create Desired resource
	desiredResource, err := r.createDesiredResource(log, isvc)
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

func (r *KserveMetricsServiceReconciler) createDesiredResource(log logr.Logger, isvc *kservev1beta1.InferenceService) (*v1.Service, error) {
	return nil, nil
}

func (r *KserveMetricsServiceReconciler) getExistingResource(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) (*v1.Service, error) {
	return r.serviceHandler.FetchService(ctx, log, types.NamespacedName{Name: getMetricsServiceName(isvc), Namespace: isvc.Namespace})
}

func (r *KserveMetricsServiceReconciler) processDelta(ctx context.Context, log logr.Logger, desiredService *v1.Service, existingService *v1.Service) (err error) {
	comparator := comparators.GetServiceComparator()
	delta := r.deltaProcessor.ComputeDelta(comparator, desiredService, existingService)

	if !delta.HasChanges() {
		log.V(1).Info("No delta found")
		return nil
	}

	if delta.IsAdded() {
		log.V(1).Info("Delta found", "create", desiredService.GetName())
		if err = r.client.Create(ctx, desiredService); err != nil {
			return
		}
	}
	if delta.IsUpdated() {
		log.V(1).Info("Delta found", "update", existingService.GetName())
		rp := existingService.DeepCopy()
		rp.Annotations = desiredService.Annotations
		rp.Labels = desiredService.Labels
		rp.Spec = desiredService.Spec

		if err = r.client.Update(ctx, rp); err != nil {
			return
		}
	}
	if delta.IsRemoved() {
		log.V(1).Info("Delta found", "delete", existingService.GetName())
		if err = r.client.Delete(ctx, existingService); err != nil {
			return
		}
	}
	return nil
}

func getMetricsServiceName(isvc *kservev1beta1.InferenceService) string {
	return isvc.Name + "-metrics"
}
