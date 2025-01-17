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

type ClusterRoleBindingHandler interface {
	FetchClusterRoleBinding(ctx context.Context, log logr.Logger, key types.NamespacedName) (*v1.ClusterRoleBinding, error)
	DeleteClusterRoleBinding(ctx context.Context, key types.NamespacedName) error
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
