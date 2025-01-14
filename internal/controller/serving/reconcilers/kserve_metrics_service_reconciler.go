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
	"strconv"

	"github.com/go-logr/logr"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"github.com/kserve/kserve/pkg/constants"
	constants2 "github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/comparators"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/processors"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/resources"
)

var _ SubResourceReconciler = (*KserveRawMetricsServiceReconciler)(nil)

type KserveRawMetricsServiceReconciler struct {
	NoResourceRemoval
	client         client.Client
	serviceHandler resources.ServiceHandler
	deltaProcessor processors.DeltaProcessor
}

func NewKServeRawMetricsServiceReconciler(client client.Client) *KserveRawMetricsServiceReconciler {
	return &KserveRawMetricsServiceReconciler{
		client:         client,
		serviceHandler: resources.NewServiceHandler(client),
		deltaProcessor: processors.NewDeltaProcessor(),
	}
}

func (r *KserveRawMetricsServiceReconciler) Reconcile(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {
	log.V(1).Info("Reconciling Metrics Service for InferenceService, checking if there are resource for deletion")

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

func (r *KserveRawMetricsServiceReconciler) createDesiredResource(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) (*v1.Service, error) {
	isvcRuntime, err := utils.FindSupportingRuntimeForISvc(ctx, r.client, log, isvc)
	if err != nil {
		return nil, err
	}

	if isvcRuntime.Spec.Annotations == nil || isvcRuntime.Spec.Annotations[constants.PrometheusPortAnnotationKey] == "" {
		log.V(1).Info("No Prometheus annotations on ServingRuntime, skipping creation of metrics resources")
		return nil, nil
	}

	prometheusPortAnnotationValue := isvcRuntime.Spec.Annotations[constants.PrometheusPortAnnotationKey]
	prometheusPort, err := strconv.ParseInt(prometheusPortAnnotationValue, 10, 64)
	if err != nil {
		return nil, err
	}
	metricsService := &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      getMetricsServiceName(isvc),
			Namespace: isvc.Namespace,
			Labels: map[string]string{
				"name": getMetricsServiceName(isvc),
			},
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{
				{
					Name:       isvcRuntime.Name + "-metrics",
					Protocol:   v1.ProtocolTCP,
					Port:       int32(prometheusPort),
					TargetPort: intstr.FromInt32(int32(prometheusPort)),
				},
			},
			Type: v1.ServiceTypeClusterIP,
			Selector: map[string]string{
				constants2.KserveGroupAnnotation: isvc.Name,
			},
		},
	}
	if err := ctrl.SetControllerReference(isvc, metricsService, r.client.Scheme()); err != nil {
		log.Error(err, "Unable to add OwnerReference to the Metrics Service")
		return nil, err
	}
	return metricsService, nil
}

func (r *KserveRawMetricsServiceReconciler) getExistingResource(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) (*v1.Service, error) {
	return r.serviceHandler.FetchService(ctx, log, types.NamespacedName{Name: getMetricsServiceName(isvc), Namespace: isvc.Namespace})
}

func (r *KserveRawMetricsServiceReconciler) processDelta(ctx context.Context, log logr.Logger, desiredService *v1.Service, existingService *v1.Service) (err error) {
	comparator := comparators.GetServiceComparator()
	delta := r.deltaProcessor.ComputeDelta(comparator, desiredService, existingService)

	if !delta.HasChanges() {
		log.V(1).Info("No delta found")
		return nil
	}

	if delta.IsAdded() {
		log.V(1).Info("Delta found", "create", desiredService.GetName())
		if err = r.client.Create(ctx, desiredService); err != nil {
			return err
		}
	}
	if delta.IsUpdated() {
		log.V(1).Info("Delta found", "update", existingService.GetName())
		rp := existingService.DeepCopy()
		rp.Annotations = desiredService.Annotations
		rp.Labels = desiredService.Labels
		rp.Spec = desiredService.Spec

		if err = r.client.Update(ctx, rp); err != nil {
			return err
		}
	}
	if delta.IsRemoved() {
		log.V(1).Info("Delta found", "delete", existingService.GetName())
		if err = r.client.Delete(ctx, existingService); err != nil {
			return err
		}
	}
	return nil
}

func getMetricsServiceName(isvc *kservev1beta1.InferenceService) string {
	return isvc.Name + "-metrics"
}
