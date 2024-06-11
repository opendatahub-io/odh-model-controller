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
	"time"

	"github.com/go-logr/logr"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"github.com/opendatahub-io/odh-model-controller/controllers/constants"
	"github.com/opendatahub-io/odh-model-controller/controllers/resources"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ SubResourceReconciler = (*KserveIsvcServiceReconciler)(nil)

type KserveIsvcServiceReconciler struct {
	client         client.Client
	serviceHandler resources.ServiceHandler
}

func NewKserveIsvcServiceReconciler(client client.Client) *KserveIsvcServiceReconciler {
	return &KserveIsvcServiceReconciler{
		client:         client,
		serviceHandler: resources.NewServiceHandler(client),
	}
}

func (r *KserveIsvcServiceReconciler) Delete(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {
	// NOOP - resources are deleted together with ISVCs
	return nil
}

func (r *KserveIsvcServiceReconciler) Cleanup(_ context.Context, _ logr.Logger, _ string) error {
	// NOOP - resources are deleted together with ISVCs
	return nil
}

func (r *KserveIsvcServiceReconciler) Reconcile(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {
	log.V(1).Info("Reconciling ISVC service serving cert for for Kserve InferenceService")

	// return if URL is not set
	if isvc.Status.URL == nil {
		log.V(1).Info("Inference Service not ready yet, waiting for URL")
		return nil
	}

	// Create Desired resource
	desiredResource, err := r.createDesiredResource(isvc, log)
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

func (r *KserveIsvcServiceReconciler) createDesiredResource(isvc *kservev1beta1.InferenceService, log logr.Logger) (*v1.Service, error) {
	service := &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: "isvc-service",
			Annotations: map[string]string{
				constants.ServingCertAnnotationKey: isvc.Name,
			},
		},
	}
	return service, nil
}

func (r *KserveIsvcServiceReconciler) getExistingResource(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) (*v1.Service, error) {
	// log.Info("Fetching existing KServe runtime Service")
	return r.serviceHandler.FetchWithRetryAndDelay(ctx, log, types.NamespacedName{Name: isvc.Name, Namespace: isvc.Namespace}, 1*time.Second, 10)
}

func (r *KserveIsvcServiceReconciler) processDelta(ctx context.Context, log logr.Logger, desiredService *v1.Service, existingService *v1.Service) (err error) {

	if isUpdated(desiredService, existingService, log) {
		log.V(1).Info("Delta found", "update", existingService.GetName())
		service := existingService.DeepCopy()
		if service.Annotations == nil {
			service.Annotations = make(map[string]string)
		}
		service.Annotations = desiredService.Annotations

		if err = r.client.Update(ctx, service); err != nil {
			return err
		}

		// log.V(1).Info("Add Serving cert annotation to kserve inferenceservice Service object", "update", existingService.GetName())
	}
	return nil
}

func isUpdated(desiredService *v1.Service, existingService *v1.Service, log logr.Logger) bool {
	if existingService == nil {
		log.Info("Service for a InferenceService is not created yet")
		return false
	}
	deployedAnnotations := existingService.GetAnnotations()

	if len(deployedAnnotations) != 0 {
		if val, exists := existingService.Annotations[constants.ServingCertAnnotationKey]; exists {
			if val == desiredService.Annotations[constants.ServingCertAnnotationKey] {
				return false
			}
		}
	}

	return true
}
