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
	v1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type NetworkPolicyHandler interface {
	FetchNetworkPolicy(ctx context.Context, log logr.Logger, key types.NamespacedName) (*v1.NetworkPolicy, error)
	DeleteNetworkPolicy(ctx context.Context, key types.NamespacedName) error
}

type networkPolicyHandler struct {
	client client.Client
}

func NewNetworkPolicyHandler(client client.Client) NetworkPolicyHandler {
	return &networkPolicyHandler{
		client: client,
	}
}

func (r *networkPolicyHandler) FetchNetworkPolicy(ctx context.Context, log logr.Logger, key types.NamespacedName) (*v1.NetworkPolicy, error) {
	networkPolicy := &v1.NetworkPolicy{}
	err := r.client.Get(ctx, key, networkPolicy)
	if err != nil && errors.IsNotFound(err) {
		log.V(1).Info("NetworkPolicy not found.")
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	log.V(1).Info("Successfully fetch deployed NetworkPolicy")
	return networkPolicy, nil
}

func (r *networkPolicyHandler) DeleteNetworkPolicy(ctx context.Context, key types.NamespacedName) error {
	networkPolicy := &v1.NetworkPolicy{}
	err := r.client.Get(ctx, key, networkPolicy)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}
	if err = r.client.Delete(ctx, networkPolicy); err != nil {
		return fmt.Errorf("failed to delete NetworkPolicy: %w", err)
	}
	return nil
}
