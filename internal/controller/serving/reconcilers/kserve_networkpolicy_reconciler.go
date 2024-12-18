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
	v1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/comparators"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/processors"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/resources"
)

const (
	monitoringNetworkPolicyName            = "allow-from-openshift-monitoring-ns"
	openshiftIngressNetworkPolicyName      = "allow-openshift-ingress"
	opendatahubNamespacesNetworkPolicyName = "allow-from-opendatahub-ns"
)

var definedNetworkPolicies = []string{monitoringNetworkPolicyName, openshiftIngressNetworkPolicyName, opendatahubNamespacesNetworkPolicyName}

var _ SubResourceReconciler = (*KserveNetworkPolicyReconciler)(nil)

type KserveNetworkPolicyReconciler struct {
	SingleResourcePerNamespace
	client               client.Client
	networkPolicyHandler resources.NetworkPolicyHandler
	deltaProcessor       processors.DeltaProcessor
}

func NewKServeNetworkPolicyReconciler(client client.Client) *KserveNetworkPolicyReconciler {
	return &KserveNetworkPolicyReconciler{
		client:               client,
		networkPolicyHandler: resources.NewNetworkPolicyHandler(client),
		deltaProcessor:       processors.NewDeltaProcessor(),
	}
}

func (r *KserveNetworkPolicyReconciler) Reconcile(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {
	log.V(1).Info("Reconciling NetworkPolicy for target namespace")

	desiredNetworkPolicies := []*v1.NetworkPolicy{
		r.allowTrafficFromMonitoringNamespace(isvc),
		r.allowOpenshiftIngressPolicy(isvc),
		r.allowTrafficFromApplicationNamespaces(isvc),
	}

	for _, desiredNetworkPolicy := range desiredNetworkPolicies {
		existingNetworkPolicy, err := r.getExistingResource(ctx, log, isvc, desiredNetworkPolicy.Name)
		if err != nil {
			return err
		}

		if err = r.processDelta(ctx, log, desiredNetworkPolicy, existingNetworkPolicy); err != nil {
			return err
		}

	}

	return nil
}

func (r *KserveNetworkPolicyReconciler) Cleanup(ctx context.Context, log logr.Logger, isvcNamespace string) error {
	log.V(1).Info("Deleting NetworkPolicy object for target namespace")
	for _, networkPolicy := range definedNetworkPolicies {
		if err := r.networkPolicyHandler.DeleteNetworkPolicy(ctx, types.NamespacedName{Name: networkPolicy, Namespace: isvcNamespace}); err != nil {
			return err
		}
	}

	return nil
}

func (r *KserveNetworkPolicyReconciler) allowTrafficFromMonitoringNamespace(isvc *kservev1beta1.InferenceService) *v1.NetworkPolicy {
	desiredNetworkPolicy := &v1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      monitoringNetworkPolicyName,
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
	return desiredNetworkPolicy
}

// allowOpenshiftIngressPolicy creates policy that grants ingress access through Openshift Routes to the services which
// are part of a namespace under Service Mesh, but are not under Service Mesh control.
func (r *KserveNetworkPolicyReconciler) allowOpenshiftIngressPolicy(isvc *kservev1beta1.InferenceService) *v1.NetworkPolicy {
	return &v1.NetworkPolicy{
		TypeMeta: metav1.TypeMeta{
			Kind:       "NetworkPolicy",
			APIVersion: "networking.k8s.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      openshiftIngressNetworkPolicyName,
			Namespace: isvc.Namespace,
			Labels:    map[string]string{"opendatahub.io/related-to": "RHOAIENG-1003"},
		},
		Spec: v1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			Ingress: []v1.NetworkPolicyIngressRule{
				{
					From: []v1.NetworkPolicyPeer{
						{
							NamespaceSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{"network.openshift.io/policy-group": "ingress"},
							},
						},
					},
				},
			},
			PolicyTypes: []v1.PolicyType{v1.PolicyTypeIngress},
		},
	}
}

// allowTrafficFromApplicationNamespaces creates combined network policy applied to pods in InferenceService namespace.
// This set of policies allow traffic from:
// - application namespace, where OpenDataHub and component services are deployed
// - namespaces created by OpenDataHub where components live
// - traffic from other DataScienceProjects (namespaces created through dashboard)
func (r *KserveNetworkPolicyReconciler) allowTrafficFromApplicationNamespaces(isvc *kservev1beta1.InferenceService) *v1.NetworkPolicy {
	return &v1.NetworkPolicy{
		TypeMeta: metav1.TypeMeta{
			Kind:       "NetworkPolicy",
			APIVersion: "networking.k8s.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      opendatahubNamespacesNetworkPolicyName,
			Namespace: isvc.Namespace,
			Labels:    map[string]string{"opendatahub.io/related-to": "RHOAIENG-1003"},
		},
		Spec: v1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			Ingress: []v1.NetworkPolicyIngressRule{
				{
					From: []v1.NetworkPolicyPeer{
						{
							NamespaceSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{"opendatahub.io/dashboard": "true"},
							},
						},
						{
							NamespaceSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{"kubernetes.io/metadata.name": constants.KServeNamespace},
							},
						},
						{
							NamespaceSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{"opendatahub.io/generated-namespace": "true"},
							},
						},
					},
				},
			},
			PolicyTypes: []v1.PolicyType{v1.PolicyTypeIngress},
		},
	}
}

func (r *KserveNetworkPolicyReconciler) getExistingResource(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService, policyName string) (*v1.NetworkPolicy, error) {
	return r.networkPolicyHandler.FetchNetworkPolicy(ctx, log, types.NamespacedName{Name: policyName, Namespace: isvc.Namespace})
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
			return err
		}
	}
	if delta.IsUpdated() {
		log.V(1).Info("Delta found", "update", existingNetworkPolicy.GetName())
		rp := existingNetworkPolicy.DeepCopy()
		rp.Labels = desiredNetworkPolicy.Labels
		rp.Spec = desiredNetworkPolicy.Spec

		if err = r.client.Update(ctx, rp); err != nil {
			return err
		}
	}
	if delta.IsRemoved() {
		log.V(1).Info("Delta found", "delete", existingNetworkPolicy.GetName())
		if err = r.client.Delete(ctx, existingNetworkPolicy); err != nil {
			return err
		}
	}
	return nil
}
