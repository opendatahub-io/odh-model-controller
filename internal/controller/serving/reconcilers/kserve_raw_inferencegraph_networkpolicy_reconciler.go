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
	"fmt"

	"github.com/go-logr/logr"
	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	kserveconstants "github.com/kserve/kserve/pkg/constants"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	knativekmeta "knative.dev/pkg/kmeta"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	inferenceGraphNetworkPolicyNameSuffix       = "-inferencegraph-network-policy"
	inferenceGraphNetworkPolicyManagedComponent = "kserve-inferencegraph-network-policy"
	inferenceGraphCallerLabel                   = "serving.kserve.io/inferencegraph-caller"
)

// KserveRawInferenceGraphNetworkPolicyReconciler owns the NetworkPolicy that
// protects a raw InferenceGraph router. KServe creates the router Deployment
// and Service; this controller reconciles the policy asynchronously so the
// KServe controller does not need to wait for an ODH-specific resource.
type KserveRawInferenceGraphNetworkPolicyReconciler struct {
	client client.Client
}

func NewKserveRawInferenceGraphNetworkPolicyReconciler(client client.Client) *KserveRawInferenceGraphNetworkPolicyReconciler {
	return &KserveRawInferenceGraphNetworkPolicyReconciler{client: client}
}

func (r *KserveRawInferenceGraphNetworkPolicyReconciler) Reconcile(
	ctx context.Context,
	log logr.Logger,
	graph *kservev1alpha1.InferenceGraph,
) error {
	desired, err := r.desiredNetworkPolicy(graph)
	if err != nil {
		return err
	}

	// KServe may create the router Pod and Service before this asynchronous
	// reconciliation creates the policy. During that interval a direct
	// Service call may be possible; subsequent retries and CNI policy
	// propagation converge the workload to the policy below.
	return r.reconcileNetworkPolicy(ctx, log, graph, desired)
}

func (r *KserveRawInferenceGraphNetworkPolicyReconciler) Delete(
	ctx context.Context,
	log logr.Logger,
	graph *kservev1alpha1.InferenceGraph,
) error {
	key := types.NamespacedName{Name: inferenceGraphNetworkPolicyName(graph), Namespace: graph.Namespace}
	policy := &networkingv1.NetworkPolicy{}
	if err := r.client.Get(ctx, key, policy); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to get NetworkPolicy %s for deletion: %w", key, err)
	}
	if !isOwnedByInferenceGraph(policy, graph) {
		log.V(1).Info("Leaving unowned raw InferenceGraph NetworkPolicy in place", "name", key)
		return nil
	}
	if err := r.client.Delete(ctx, policy); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete NetworkPolicy %s: %w", key, err)
	}
	return nil
}

func (r *KserveRawInferenceGraphNetworkPolicyReconciler) desiredNetworkPolicy(
	graph *kservev1alpha1.InferenceGraph,
) (*networkingv1.NetworkPolicy, error) {
	policy := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      inferenceGraphNetworkPolicyName(graph),
			Namespace: graph.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/component":       inferenceGraphNetworkPolicyManagedComponent,
				"app.kubernetes.io/part-of":         "kserve",
				managedByLabel:                      managedByValue,
				kserveconstants.InferenceGraphLabel: graph.Name,
			},
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{
				kserveconstants.InferenceGraphLabel: graph.Name,
			}},
			PolicyTypes: []networkingv1.PolicyType{
				networkingv1.PolicyTypeIngress,
				networkingv1.PolicyTypeEgress,
			},
			// Graph steps may use arbitrary user-configured URLs. Keep this an
			// explicit allow-all rule so enabling the policy does not break those
			// destinations or the optional Kubernetes API auth verifier.
			Egress: []networkingv1.NetworkPolicyEgressRule{{}},
		},
	}

	if isClusterLocalInferenceGraph(graph) {
		// A PodSelector without a NamespaceSelector is deliberately namespace
		// local. Callers opt in by labeling their Pod for this graph.
		policy.Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{{
			From: []networkingv1.NetworkPolicyPeer{{
				PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{
					inferenceGraphCallerLabel: graph.Name,
				}},
			}},
			Ports: []networkingv1.NetworkPolicyPort{networkPolicyPortTCP(kserveconstants.RouterPort)},
		}}
	} else {
		policy.Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{{
			From:  []networkingv1.NetworkPolicyPeer{routerPeer()},
			Ports: []networkingv1.NetworkPolicyPort{networkPolicyPortTCP(kserveconstants.RouterPort)},
		}}
	}

	if err := ctrl.SetControllerReference(graph, policy, r.client.Scheme()); err != nil {
		return nil, fmt.Errorf("set NetworkPolicy owner reference for %s: %w", policy.Name, err)
	}
	return policy, nil
}

func (r *KserveRawInferenceGraphNetworkPolicyReconciler) reconcileNetworkPolicy(
	ctx context.Context,
	log logr.Logger,
	graph *kservev1alpha1.InferenceGraph,
	desired *networkingv1.NetworkPolicy,
) error {
	key := client.ObjectKeyFromObject(desired)
	existing := &networkingv1.NetworkPolicy{}
	if err := r.client.Get(ctx, key, existing); err != nil {
		if apierrors.IsNotFound(err) {
			log.V(1).Info("Creating raw InferenceGraph NetworkPolicy", "name", desired.Name)
			return r.client.Create(ctx, desired)
		}
		return fmt.Errorf("failed to get NetworkPolicy %s: %w", key, err)
	}

	if !isOwnedByInferenceGraph(existing, graph) {
		return fmt.Errorf("refusing to overwrite NetworkPolicy %s: it is not owned by InferenceGraph %s/%s", key, graph.Namespace, graph.Name)
	}
	if !existing.GetDeletionTimestamp().IsZero() {
		return fmt.Errorf("NetworkPolicy %s is still deleting", key)
	}

	if equality.Semantic.DeepEqual(existing.Spec, desired.Spec) &&
		equality.Semantic.DeepEqual(existing.Labels, desired.Labels) &&
		equality.Semantic.DeepEqual(existing.Annotations, desired.Annotations) &&
		equality.Semantic.DeepEqual(existing.OwnerReferences, desired.OwnerReferences) {
		return nil
	}

	updated := existing.DeepCopy()
	updated.Labels = desired.Labels
	updated.Annotations = desired.Annotations
	updated.OwnerReferences = desired.OwnerReferences
	updated.Spec = *desired.Spec.DeepCopy()
	log.V(1).Info("Updating raw InferenceGraph NetworkPolicy", "name", desired.Name)
	if err := r.client.Update(ctx, updated); err != nil {
		return fmt.Errorf("failed to update NetworkPolicy %s: %w", key, err)
	}
	return nil
}

func inferenceGraphNetworkPolicyName(graph *kservev1alpha1.InferenceGraph) string {
	return knativekmeta.ChildName(graph.Name, inferenceGraphNetworkPolicyNameSuffix)
}

func isOwnedByInferenceGraph(policy *networkingv1.NetworkPolicy, graph *kservev1alpha1.InferenceGraph) bool {
	for _, ref := range policy.OwnerReferences {
		if ref.APIVersion != kservev1alpha1.SchemeGroupVersion.String() ||
			ref.Kind != "InferenceGraph" ||
			ref.Name != graph.Name ||
			ref.Controller == nil ||
			!*ref.Controller {
			continue
		}
		if ref.UID == graph.UID {
			return true
		}
	}
	return false
}

func isClusterLocalInferenceGraph(graph *kservev1alpha1.InferenceGraph) bool {
	mode := graph.Status.DeploymentMode
	if mode == "" && graph.Annotations != nil {
		mode = graph.Annotations[kserveconstants.DeploymentMode]
	}
	if mode != "" && kserveconstants.ParseDeploymentMode(mode) != kserveconstants.Standard {
		return false
	}
	return graph.Labels[kserveconstants.NetworkVisibility] == kserveconstants.ClusterLocalVisibility
}

// IsRawInferenceGraph reports whether an InferenceGraph resolves to KServe's
// Standard (raw) deployment mode. An empty mode uses KServe's Standard default
// in the ODH deployment.
func IsRawInferenceGraph(graph *kservev1alpha1.InferenceGraph) bool {
	mode := graph.Status.DeploymentMode
	if mode == "" && graph.Annotations != nil {
		mode = graph.Annotations[kserveconstants.DeploymentMode]
	}
	return mode == "" || kserveconstants.ParseDeploymentMode(mode) == kserveconstants.Standard
}

func IsRawInferenceService(isvc *kservev1beta1.InferenceService) bool {
	mode := isvc.Status.DeploymentMode
	if mode == "" && isvc.Annotations != nil {
		mode = isvc.Annotations[kserveconstants.DeploymentMode]
	}
	return mode == "" || kserveconstants.ParseDeploymentMode(mode) == kserveconstants.Standard
}

func networkPolicyPortTCP(port int) networkingv1.NetworkPolicyPort {
	return networkingv1.NetworkPolicyPort{
		Protocol: ptr.To(corev1.ProtocolTCP),
		Port:     ptr.To(intstr.FromInt32(int32(port))),
	}
}
