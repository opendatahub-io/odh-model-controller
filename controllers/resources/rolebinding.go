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
	"github.com/opendatahub-io/odh-model-controller/controllers/comparators"
	"github.com/opendatahub-io/odh-model-controller/controllers/processors"
	v1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type RoleBindingHandler interface {
	FetchRoleBinding(ctx context.Context, log logr.Logger, key types.NamespacedName) (*v1.RoleBinding, error)
	DeleteRoleBinding(ctx context.Context, key types.NamespacedName) error
	CreateDesiredRoleBinding(rbName string, serviceAccountName string, namespace string) *v1.RoleBinding
	ProcessDelta(ctx context.Context, log logr.Logger, desiredRB *v1.RoleBinding, existingRB *v1.RoleBinding, deltaProcessor processors.DeltaProcessor) (err error)
	GetRoleBindingName(isvcNamespace string, serviceAccountName string) string
}

type roleBindingHandler struct {
	client client.Client
}

func NewRoleBindingHandler(client client.Client) RoleBindingHandler {
	return &roleBindingHandler{
		client: client,
	}
}

func (r *roleBindingHandler) FetchRoleBinding(ctx context.Context, log logr.Logger, key types.NamespacedName) (*v1.RoleBinding, error) {
	roleBinding := &v1.RoleBinding{}
	err := r.client.Get(ctx, key, roleBinding)
	if err != nil && errors.IsNotFound(err) {
		log.V(1).Info("RoleBinding not found.")
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	log.V(1).Info("Successfully fetched deployed RoleBinding")
	return roleBinding, nil
}

func (r *roleBindingHandler) DeleteRoleBinding(ctx context.Context, key types.NamespacedName) error {
	roleBinding := &v1.RoleBinding{}
	err := r.client.Get(ctx, key, roleBinding)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}
	if err = r.client.Delete(ctx, roleBinding); err != nil {
		return fmt.Errorf("failed to delete RoleBinding: %w", err)
	}
	return nil
}

func (r *roleBindingHandler) CreateDesiredRoleBinding(rbName string, serviceAccountName string, namespace string) *v1.RoleBinding {
	desiredRoleBinding := &v1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rbName,
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
	return desiredRoleBinding
}

func (r *roleBindingHandler) ProcessDelta(ctx context.Context, log logr.Logger, desiredRB *v1.RoleBinding, existingRB *v1.RoleBinding, deltaProcessor processors.DeltaProcessor) (err error) {
	comparator := comparators.GetRoleBindingComparator()
	delta := deltaProcessor.ComputeDelta(comparator, desiredRB, existingRB)

	if !delta.HasChanges() {
		log.V(1).Info("No delta found")
		return nil
	}

	if delta.IsAdded() {
		log.V(1).Info("Delta found", "create", desiredRB.GetName())
		if err = r.client.Create(ctx, desiredRB); err != nil {
			if errors.IsAlreadyExists(err) {
				return nil
			}
			return err
		}
	}
	if delta.IsUpdated() {
		log.V(1).Info("Delta found", "update", existingRB.GetName())
		rp := existingRB.DeepCopy()
		rp.RoleRef = desiredRB.RoleRef
		rp.Subjects = desiredRB.Subjects

		if err = r.client.Update(ctx, rp); err != nil {
			return
		}
	}
	if delta.IsRemoved() {
		log.V(1).Info("Delta found", "delete", existingRB.GetName())
		if err = r.client.Delete(ctx, existingRB); err != nil {
			return
		}
	}
	return nil
}

func (r *roleBindingHandler) GetRoleBindingName(isvcNamespace string, serviceAccountName string) string {
	return isvcNamespace + "-" + serviceAccountName + "-auth-delegator"
}
