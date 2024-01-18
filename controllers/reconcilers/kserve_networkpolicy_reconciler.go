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
	"k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	networkPolicyName = "allow-from-openshift-monitoring-ns"
)

type KserveNetworkPolicyReconciler struct {
	client               client.Client
	scheme               *runtime.Scheme
	networkPolicyHandler resources.NetworkPolicyHandler
	deltaProcessor       processors.DeltaProcessor
}

func NewKServeNetworkPolicyReconciler(client client.Client, scheme *runtime.Scheme) *KserveNetworkPolicyReconciler {
	return &KserveNetworkPolicyReconciler{
		client:               client,
		scheme:               scheme,
		networkPolicyHandler: resources.NewNetworkPolicyHandler(client),
		deltaProcessor:       processors.NewDeltaProcessor(),
	}
}

func (r *KserveNetworkPolicyReconciler) Reconcile(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {

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

func (r *KserveNetworkPolicyReconciler) createDesiredResource(isvc *kservev1beta1.InferenceService) (*v1.NetworkPolicy, error) {
	desiredNetworkPolicy := &v1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      networkPolicyName,
			Namespace: isvc.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/version":               "release-v1.9",
				"networking.knative.dev/ingress-provider": "istio",
			},
		},
		Spec: v1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			Ingress: []v1.NetworkPolicyIngressRule{
				{
					From: []v1.NetworkPolicyPeer{
						{
							NamespaceSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"name": "openshift-user-workload-monitoring",
								},
							},
						},
					},
				},
			},
			PolicyTypes: []v1.PolicyType{v1.PolicyTypeIngress},
		},
	}
	return desiredNetworkPolicy, nil
}

func (r *KserveNetworkPolicyReconciler) getExistingResource(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) (*v1.NetworkPolicy, error) {
	return r.networkPolicyHandler.FetchNetworkPolicy(ctx, log, types.NamespacedName{Name: networkPolicyName, Namespace: isvc.Namespace})
}

func (r *KserveNetworkPolicyReconciler) processDelta(ctx context.Context, log logr.Logger, desiredNetworkPolicy *v1.NetworkPolicy, existingNetworkPolicy *v1.NetworkPolicy) (err error) {
	comparator := comparators.GetNetworkPolicyComparator()
	delta := r.deltaProcessor.ComputeDelta(comparator, desiredNetworkPolicy, existingNetworkPolicy)

	if !delta.HasChanges() {
		log.V(1).Info("No delta found")
		return nil
	}

	if delta.IsAdded() {
		log.V(1).Info("Delta found", "create", desiredNetworkPolicy.GetName())
		if err = r.client.Create(ctx, desiredNetworkPolicy); err != nil {
			return
		}
	}
	if delta.IsUpdated() {
		log.V(1).Info("Delta found", "update", existingNetworkPolicy.GetName())
		rp := existingNetworkPolicy.DeepCopy()
		rp.Labels = desiredNetworkPolicy.Labels
		rp.Spec = desiredNetworkPolicy.Spec

		if err = r.client.Update(ctx, rp); err != nil {
			return
		}
	}
	if delta.IsRemoved() {
		log.V(1).Info("Delta found", "delete", existingNetworkPolicy.GetName())
		if err = r.client.Delete(ctx, existingNetworkPolicy); err != nil {
			return
		}
	}
	return nil
}

func (r *KserveNetworkPolicyReconciler) DeleteNetworkPolicy(ctx context.Context, isvcNamespace string) error {
	return r.networkPolicyHandler.DeleteNetworkPolicy(ctx, types.NamespacedName{Name: networkPolicyName, Namespace: isvcNamespace})
}
