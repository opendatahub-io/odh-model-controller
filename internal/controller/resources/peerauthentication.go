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
	istiosecv1beta1 "istio.io/client-go/pkg/apis/security/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type PeerAuthenticationHandler interface {
	FetchPeerAuthentication(ctx context.Context, log logr.Logger, key types.NamespacedName) (*istiosecv1beta1.PeerAuthentication, error)
	DeletePeerAuthentication(ctx context.Context, key types.NamespacedName) error
}

type peerAuthenticationHandler struct {
	client client.Client
}

func NewPeerAuthenticationHandler(client client.Client) PeerAuthenticationHandler {
	return &peerAuthenticationHandler{
		client: client,
	}
}

func (r *peerAuthenticationHandler) FetchPeerAuthentication(ctx context.Context, log logr.Logger, key types.NamespacedName) (*istiosecv1beta1.PeerAuthentication, error) {
	peerAuthentication := &istiosecv1beta1.PeerAuthentication{}
	err := r.client.Get(ctx, key, peerAuthentication)
	if err != nil && errors.IsNotFound(err) {
		log.V(1).Info("PeerAuthentication not found.")
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	log.V(1).Info("Successfully fetch deployed PeerAuthentication")
	return peerAuthentication, nil
}

func (r *peerAuthenticationHandler) DeletePeerAuthentication(ctx context.Context, key types.NamespacedName) error {
	peerAuthentication := &istiosecv1beta1.PeerAuthentication{}
	err := r.client.Get(ctx, key, peerAuthentication)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}
	if err = r.client.Delete(ctx, peerAuthentication); err != nil {
		return fmt.Errorf("failed to delete PeerAuthentication: %w", err)
	}

	return nil
}
