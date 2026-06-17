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

package resources

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	v1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/comparators"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/processors"
)

type ClusterRoleBindingHandler interface {
	FetchClusterRoleBinding(ctx context.Context, log logr.Logger, key types.NamespacedName) (*v1.ClusterRoleBinding, error)
	DeleteClusterRoleBinding(ctx context.Context, key types.NamespacedName) error
	CreateDesiredClusterRoleBinding(crbName string, serviceAccountName string, namespace string) *v1.ClusterRoleBinding
	ProcessDelta(ctx context.Context, log logr.Logger, desiredCRB *v1.ClusterRoleBinding, existingCRB *v1.ClusterRoleBinding, deltaProcessor processors.DeltaProcessor) (err error)
	GetClusterRoleBindingName(isvcNamespace string, serviceAccountName string) string
}

type clusterRoleBindingHandler struct {
	client client.Client
}

func NewClusterRoleBindingHandler(client client.Client) ClusterRoleBindingHandler {
	return &clusterRoleBindingHandler{
		client: client,
	}
}

func (r *clusterRoleBindingHandler) FetchClusterRoleBinding(ctx context.Context, log logr.Logger, key types.NamespacedName) (*v1.ClusterRoleBinding, error) {
	clusterRoleBinding := &v1.ClusterRoleBinding{}
	err := r.client.Get(ctx, key, clusterRoleBinding)
	if err != nil && errors.IsNotFound(err) {
		log.V(1).Info("ClusterRoleBinding not found.")
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	log.V(1).Info("Successfully fetch deployed ClusterRoleBinding")
	return clusterRoleBinding, nil
}

func (r *clusterRoleBindingHandler) DeleteClusterRoleBinding(ctx context.Context, key types.NamespacedName) error {
	clusterRoleBinding := &v1.ClusterRoleBinding{}
	err := r.client.Get(ctx, key, clusterRoleBinding)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}
	if err = r.client.Delete(ctx, clusterRoleBinding); err != nil {
		return fmt.Errorf("failed to delete ClusterRoleBinding: %w", err)
	}
	return nil
}

func (r *clusterRoleBindingHandler) CreateDesiredClusterRoleBinding(crbName string, serviceAccountName string, namespace string) *v1.ClusterRoleBinding {
	desiredClusterRoleBinding := &v1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      crbName,
			Namespace: namespace,
		},
		Subjects: []v1.Subject{
			{
				Kind:      "ServiceAccount",
				Namespace: namespace,
				Name:      serviceAccountName,
			},
		},
		RoleRef: v1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "system:auth-delegator",
		},
	}
	return desiredClusterRoleBinding
}

func (r *clusterRoleBindingHandler) ProcessDelta(ctx context.Context, log logr.Logger, desiredCRB *v1.ClusterRoleBinding, existingCRB *v1.ClusterRoleBinding, deltaProcessor processors.DeltaProcessor) (err error) {
	comparator := comparators.GetClusterRoleBindingComparator()
	delta := deltaProcessor.ComputeDelta(comparator, desiredCRB, existingCRB)

	if !delta.HasChanges() {
		log.V(1).Info("No delta found")
		return nil
	}

	if delta.IsAdded() {
		log.V(1).Info("Delta found", "create", desiredCRB.GetName())
		if err = r.client.Create(ctx, desiredCRB); err != nil {
			if errors.IsAlreadyExists(err) {
				return nil
			}
			return err
		}
	}
	if delta.IsUpdated() {
		log.V(1).Info("Delta found", "update", existingCRB.GetName())
		rp := existingCRB.DeepCopy()
		rp.RoleRef = desiredCRB.RoleRef
		rp.Subjects = desiredCRB.Subjects

		if err = r.client.Update(ctx, rp); err != nil {
			return err
		}
	}
	if delta.IsRemoved() {
		log.V(1).Info("Delta found", "delete", existingCRB.GetName())
		if err = r.client.Delete(ctx, existingCRB); err != nil {
			return err
		}
	}
	return nil
}

func (r *clusterRoleBindingHandler) GetClusterRoleBindingName(isvcNamespace string, serviceAccountName string) string {
	return isvcNamespace + "-" + serviceAccountName + "-auth-delegator"
}
