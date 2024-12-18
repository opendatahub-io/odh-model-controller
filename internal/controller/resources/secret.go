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

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type SecretHandler interface {
	Get(ctx context.Context, key types.NamespacedName) (*v1.Secret, error)
}

type secretHandler struct {
	client client.Client
}

func NewSecretHandler(client client.Client) SecretHandler {
	return &secretHandler{
		client: client,
	}
}

func (s *secretHandler) Get(ctx context.Context, key types.NamespacedName) (*v1.Secret, error) {
	secret := &v1.Secret{}
	if err := s.client.Get(ctx, key, secret); err != nil {
		return nil, err
	}

	return secret, nil
}
