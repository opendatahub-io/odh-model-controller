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
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type RoleBindingHandler interface {
	FetchRoleBinding(ctx context.Context, log logr.Logger, key types.NamespacedName) (*v1.RoleBinding, error)
	DeleteRoleBinding(ctx context.Context, key types.NamespacedName) error
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
	log.V(1).Info("Successfully fetch deployed RoleBinding")
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
