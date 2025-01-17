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
	v1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	clusterPrometheusAccessRoleBinding = "kserve-prometheus-k8s"
)

var _ SubResourceReconciler = (*KservePrometheusRoleBindingReconciler)(nil)

type KservePrometheusRoleBindingReconciler struct {
	SingleResourcePerNamespace
	client             client.Client
	roleBindingHandler resources.RoleBindingHandler
	deltaProcessor     processors.DeltaProcessor
}

func NewKServePrometheusRoleBindingReconciler(client client.Client) *KservePrometheusRoleBindingReconciler {
	return &KservePrometheusRoleBindingReconciler{
		client:             client,
		roleBindingHandler: resources.NewRoleBindingHandler(client),
		deltaProcessor:     processors.NewDeltaProcessor(),
	}
}

func (r *KservePrometheusRoleBindingReconciler) Reconcile(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {
	log.V(1).Info("Verifying that the role binding to enable prometheus access exists")

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

func (r *KservePrometheusRoleBindingReconciler) Cleanup(ctx context.Context, log logr.Logger, isvcNs string) error {
	log.V(1).Info("Deleting Prometheus RoleBinding object for target namespace")
	return r.roleBindingHandler.DeleteRoleBinding(ctx, types.NamespacedName{Name: clusterPrometheusAccessRoleBinding, Namespace: isvcNs})
}

func (r *KservePrometheusRoleBindingReconciler) createDesiredResource(isvc *kservev1beta1.InferenceService) (*v1.RoleBinding, error) {
	desiredRoleBinding := &v1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterPrometheusAccessRoleBinding,
			Namespace: isvc.Namespace,
		},
		RoleRef: v1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "kserve-prometheus-k8s",
		},
		Subjects: []v1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      "prometheus-k8s",
				Namespace: "openshift-monitoring",
			},
		},
	}
	return desiredRoleBinding, nil
}

func (r *KservePrometheusRoleBindingReconciler) getExistingResource(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) (*v1.RoleBinding, error) {
	return r.roleBindingHandler.FetchRoleBinding(ctx, log, types.NamespacedName{Name: clusterPrometheusAccessRoleBinding, Namespace: isvc.Namespace})
}

func (r *KservePrometheusRoleBindingReconciler) processDelta(ctx context.Context, log logr.Logger, desiredRoleBinding *v1.RoleBinding, existingRoleBinding *v1.RoleBinding) (err error) {
	comparator := comparators.GetRoleBindingComparator()
	delta := r.deltaProcessor.ComputeDelta(comparator, desiredRoleBinding, existingRoleBinding)

	if !delta.HasChanges() {
		log.V(1).Info("No delta found")
		return nil
	}

	if delta.IsAdded() {
		log.V(1).Info("Delta found", "create", desiredRoleBinding.GetName())
		if err = r.client.Create(ctx, desiredRoleBinding); err != nil {
			return
		}
	}
	if delta.IsUpdated() {
		log.V(1).Info("Delta found", "update", existingRoleBinding.GetName())
		rp := existingRoleBinding.DeepCopy()
		rp.RoleRef = desiredRoleBinding.RoleRef
		rp.Subjects = desiredRoleBinding.Subjects

		if err = r.client.Update(ctx, rp); err != nil {
			return
		}
	}
	if delta.IsRemoved() {
		log.V(1).Info("Delta found", "delete", existingRoleBinding.GetName())
		if err = r.client.Delete(ctx, existingRoleBinding); err != nil {
			return
		}
	}
	return nil
}
