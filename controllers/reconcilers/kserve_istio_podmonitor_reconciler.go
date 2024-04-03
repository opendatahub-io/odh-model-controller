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
	"github.com/opendatahub-io/odh-model-controller/controllers/comparators"
	"github.com/opendatahub-io/odh-model-controller/controllers/processors"
	"github.com/opendatahub-io/odh-model-controller/controllers/resources"
	v1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	istioPodMonitorName = "istio-proxies-monitor"
)

var _ SubResourceReconciler = (*KserveIstioPodMonitorReconciler)(nil)

type KserveIstioPodMonitorReconciler struct {
	SingleResourcePerNamespace
	client            client.Client
	scheme            *runtime.Scheme
	podMonitorHandler resources.PodMonitorHandler
	deltaProcessor    processors.DeltaProcessor
}

func NewKServeIstioPodMonitorReconciler(client client.Client, scheme *runtime.Scheme) *KserveIstioPodMonitorReconciler {
	return &KserveIstioPodMonitorReconciler{
		client:            client,
		scheme:            scheme,
		podMonitorHandler: resources.NewPodMonitorHandler(client),
		deltaProcessor:    processors.NewDeltaProcessor(),
	}
}

func (r *KserveIstioPodMonitorReconciler) Reconcile(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {
	log.V(1).Info("Creating Istio PodMonitor for target namespace")

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

func (r *KserveIstioPodMonitorReconciler) Cleanup(ctx context.Context, log logr.Logger, isvcNs string) error {
	log.V(1).Info("Deleting PodMonitor object for target namespace")
	return r.podMonitorHandler.DeletePodMonitor(ctx, types.NamespacedName{Name: istioPodMonitorName, Namespace: isvcNs})
}

func (r *KserveIstioPodMonitorReconciler) createDesiredResource(isvc *kservev1beta1.InferenceService) (*v1.PodMonitor, error) {
	desiredPodMonitor := &v1.PodMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      istioPodMonitorName,
			Namespace: isvc.Namespace,
		},
		Spec: v1.PodMonitorSpec{
			Selector: metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{
					{
						Key:      "istio-prometheus-ignore",
						Operator: metav1.LabelSelectorOpDoesNotExist,
					},
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
	return desiredPodMonitor, nil
}

func (r *KserveIstioPodMonitorReconciler) getExistingResource(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) (*v1.PodMonitor, error) {
	return r.podMonitorHandler.FetchPodMonitor(ctx, log, types.NamespacedName{Name: istioPodMonitorName, Namespace: isvc.Namespace})
}

func (r *KserveIstioPodMonitorReconciler) processDelta(ctx context.Context, log logr.Logger, desiredPodMonitor *v1.PodMonitor, existingPodMonitor *v1.PodMonitor) (err error) {
	comparator := comparators.GetPodMonitorComparator()
	delta := r.deltaProcessor.ComputeDelta(comparator, desiredPodMonitor, existingPodMonitor)

	if !delta.HasChanges() {
		log.V(1).Info("No delta found")
		return nil
	}

	if delta.IsAdded() {
		log.V(1).Info("Delta found", "create", desiredPodMonitor.GetName())
		if err = r.client.Create(ctx, desiredPodMonitor); err != nil {
			return
		}
	}
	if delta.IsUpdated() {
		log.V(1).Info("Delta found", "update", existingPodMonitor.GetName())
		rp := existingPodMonitor.DeepCopy()
		rp.Spec = desiredPodMonitor.Spec

		if err = r.client.Update(ctx, rp); err != nil {
			return
		}
	}
	if delta.IsRemoved() {
		log.V(1).Info("Delta found", "delete", existingPodMonitor.GetName())
		if err = r.client.Delete(ctx, existingPodMonitor); err != nil {
			return
		}
	}
	return nil
}
