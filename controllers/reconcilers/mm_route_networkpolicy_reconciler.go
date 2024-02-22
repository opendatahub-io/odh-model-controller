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
	modelMeshRouteNetworkPolicyName = "allow-modelmesh-route"
)

type MMRouteNetworkPolicyReconciler struct {
	client               client.Client
	scheme               *runtime.Scheme
	networkPolicyHandler resources.NetworkPolicyHandler
	deltaProcessor       processors.DeltaProcessor
}

func NewMMRouteNetworkPolicyReconciler(client client.Client, scheme *runtime.Scheme) *MMRouteNetworkPolicyReconciler {
	return &MMRouteNetworkPolicyReconciler{
		client:               client,
		scheme:               scheme,
		networkPolicyHandler: resources.NewNetworkPolicyHandler(client),
		deltaProcessor:       processors.NewDeltaProcessor(),
	}
}

func (r *MMRouteNetworkPolicyReconciler) Reconcile(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {

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

func (r *MMRouteNetworkPolicyReconciler) createDesiredResource(isvc *kservev1beta1.InferenceService) (*v1.NetworkPolicy, error) {
	desiredNetworkPolicy := &v1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      modelMeshRouteNetworkPolicyName,
			Namespace: isvc.Namespace,
		},
		Spec: v1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					"modelmesh-service": "modelmesh-serving",
				},
			},
			Ingress: []v1.NetworkPolicyIngressRule{
				{
					From: []v1.NetworkPolicyPeer{
						{
							NamespaceSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"network.openshift.io/policy-group": "ingress",
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

func (r *MMRouteNetworkPolicyReconciler) getExistingResource(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) (*v1.NetworkPolicy, error) {
	return r.networkPolicyHandler.FetchNetworkPolicy(ctx, log, types.NamespacedName{Name: modelMeshRouteNetworkPolicyName, Namespace: isvc.Namespace})
}

func (r *MMRouteNetworkPolicyReconciler) processDelta(ctx context.Context, log logr.Logger, desiredPod *v1.NetworkPolicy, existingPod *v1.NetworkPolicy) (err error) {
	comparator := comparators.GetNetworkPolicyComparator()
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
		rp.Labels = desiredPod.Labels
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

func (r *MMRouteNetworkPolicyReconciler) DeleteMMRouteNetworkPolicy(ctx context.Context, isvcNamespace string) error {
	return r.networkPolicyHandler.DeleteNetworkPolicy(ctx, types.NamespacedName{Name: modelMeshRouteNetworkPolicyName, Namespace: isvcNamespace})
}
