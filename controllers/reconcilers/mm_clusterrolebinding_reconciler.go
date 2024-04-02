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
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ Reconciler = (*ModelMeshClusterRoleBindingReconciler)(nil)

type ModelMeshClusterRoleBindingReconciler struct {
	client                    client.Client
	scheme                    *runtime.Scheme
	clusterRoleBindingHandler resources.ClusterRoleBindingHandler
	deltaProcessor            processors.DeltaProcessor
}

func NewModelMeshClusterRoleBindingReconciler(client client.Client, scheme *runtime.Scheme) *ModelMeshClusterRoleBindingReconciler {
	return &ModelMeshClusterRoleBindingReconciler{
		client:                    client,
		scheme:                    scheme,
		clusterRoleBindingHandler: resources.NewClusterRoleBindingHandler(client),
		deltaProcessor:            processors.NewDeltaProcessor(),
	}
}

func (r *ModelMeshClusterRoleBindingReconciler) Reconcile(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {
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

func (r *ModelMeshClusterRoleBindingReconciler) createDesiredResource(isvc *kservev1beta1.InferenceService) (*v1.ClusterRoleBinding, error) {
	desiredClusterRoleBinding := &v1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      getClusterRoleBindingName(isvc.Namespace),
			Namespace: isvc.Namespace,
		},
		Subjects: []v1.Subject{
			{
				Kind:      "ServiceAccount",
				Namespace: isvc.Namespace,
				Name:      modelMeshServiceAccountName,
			},
		},
		RoleRef: v1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "system:auth-delegator",
		},
	}
	return desiredClusterRoleBinding, nil
}

func (r *ModelMeshClusterRoleBindingReconciler) getExistingResource(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) (*v1.ClusterRoleBinding, error) {
	return r.clusterRoleBindingHandler.FetchClusterRoleBinding(ctx, log, types.NamespacedName{Name: getClusterRoleBindingName(isvc.Namespace), Namespace: isvc.Namespace})
}

func (r *ModelMeshClusterRoleBindingReconciler) processDelta(ctx context.Context, log logr.Logger, desiredCRB *v1.ClusterRoleBinding, existingCRB *v1.ClusterRoleBinding) (err error) {
	comparator := comparators.GetClusterRoleBindingComparator()
	delta := r.deltaProcessor.ComputeDelta(comparator, desiredCRB, existingCRB)

	if !delta.HasChanges() {
		log.V(1).Info("No delta found")
		return nil
	}

	if delta.IsAdded() {
		log.V(1).Info("Delta found", "create", desiredCRB.GetName())
		if err = r.client.Create(ctx, desiredCRB); err != nil {
			return
		}
	}
	if delta.IsUpdated() {
		log.V(1).Info("Delta found", "update", existingCRB.GetName())
		rp := existingCRB.DeepCopy()
		rp.RoleRef = desiredCRB.RoleRef
		rp.Subjects = desiredCRB.Subjects

		if err = r.client.Update(ctx, rp); err != nil {
			return
		}
	}
	if delta.IsRemoved() {
		log.V(1).Info("Delta found", "delete", existingCRB.GetName())
		if err = r.client.Delete(ctx, existingCRB); err != nil {
			return
		}
	}
	return nil
}

func getClusterRoleBindingName(isvcNamespace string) string {
	return isvcNamespace + "-" + modelMeshServiceAccountName + "-auth-delegator"
}

func (r *ModelMeshClusterRoleBindingReconciler) DeleteClusterRoleBinding(ctx context.Context, isvcNamespace string) error {
	return r.clusterRoleBindingHandler.DeleteClusterRoleBinding(ctx, types.NamespacedName{Name: getClusterRoleBindingName(isvcNamespace), Namespace: isvcNamespace})
}
