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
	"github.com/kserve/kserve/pkg/constants"
	"github.com/opendatahub-io/odh-model-controller/controllers/comparators"
	"github.com/opendatahub-io/odh-model-controller/controllers/processors"
	"github.com/opendatahub-io/odh-model-controller/controllers/resources"
	v1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type KserveMetricsPodMonitorReconciler struct {
	client            client.Client
	scheme            *runtime.Scheme
	podMonitorHandler resources.PodMonitorHandler
	deltaProcessor    processors.DeltaProcessor
}

func NewKServeMetricsPodMonitorReconciler(client client.Client, scheme *runtime.Scheme) *KserveMetricsPodMonitorReconciler {
	return &KserveMetricsPodMonitorReconciler{
		client:            client,
		scheme:            scheme,
		podMonitorHandler: resources.NewPodMonitorHandler(client),
		deltaProcessor:    processors.NewDeltaProcessor(),
	}
}

func (r *KserveMetricsPodMonitorReconciler) Reconcile(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {

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

func (r *KserveMetricsPodMonitorReconciler) createDesiredResource(isvc *kservev1beta1.InferenceService) (*v1.PodMonitor, error) {
	desiredPodMonitor := &v1.PodMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      getMetricsPodMonitorName(isvc),
			Namespace: isvc.Namespace,
		},
		Spec: v1.PodMonitorSpec{
			Selector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					constants.InferenceServicePodLabelKey: isvc.Name,
				},
			},
			PodMetricsEndpoints: []v1.PodMetricsEndpoint{
				{
					Path:     "/stats/prometheus",
					Interval: "30s",
				},
			},
		},
	}
	if err := ctrl.SetControllerReference(isvc, desiredPodMonitor, r.scheme); err != nil {
		return nil, err
	}
	return desiredPodMonitor, nil
}

func (r *KserveMetricsPodMonitorReconciler) getExistingResource(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) (*v1.PodMonitor, error) {
	return r.podMonitorHandler.FetchPodMonitor(ctx, log, types.NamespacedName{Name: getMetricsPodMonitorName(isvc), Namespace: isvc.Namespace})
}

func (r *KserveMetricsPodMonitorReconciler) processDelta(ctx context.Context, log logr.Logger, desiredPod *v1.PodMonitor, existingPod *v1.PodMonitor) (err error) {
	comparator := comparators.GetPodMonitorComparator()
	delta := r.deltaProcessor.ComputeDelta(comparator, desiredPod, existingPod)

	if !delta.HasChanges() {
		log.V(1).Info("No delta found")
		return nil
	}

	if delta.IsAdded() {
		log.V(1).Info("Delta found", "create", desiredPod.GetName())
		if err = r.client.Create(ctx, desiredPod); err != nil {
			return
		}
	}
	if delta.IsUpdated() {
		log.V(1).Info("Delta found", "update", existingPod.GetName())
		rp := existingPod.DeepCopy()
		rp.Spec = desiredPod.Spec

		if err = r.client.Update(ctx, rp); err != nil {
			return
		}
	}
	if delta.IsRemoved() {
		log.V(1).Info("Delta found", "delete", existingPod.GetName())
		if err = r.client.Delete(ctx, existingPod); err != nil {
			return
		}
	}
	return nil
}

func getMetricsPodMonitorName(isvc *kservev1beta1.InferenceService) string {
	return isvc.Name + "-monitor"
}
