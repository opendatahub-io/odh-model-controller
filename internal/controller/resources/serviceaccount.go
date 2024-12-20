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
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ServiceAccountHandler interface {
	FetchServiceAccount(ctx context.Context, log logr.Logger, key types.NamespacedName) (*v1.ServiceAccount, error)
	DeleteServiceAccount(ctx context.Context, key types.NamespacedName) error
}

type serviceAccountHandler struct {
	client client.Client
}

func NewServiceAccountHandler(client client.Client) ServiceAccountHandler {
	return &serviceAccountHandler{
		client: client,
	}
}

func (r *serviceAccountHandler) FetchServiceAccount(ctx context.Context, log logr.Logger, key types.NamespacedName) (*v1.ServiceAccount, error) {
	serviceAccount := &v1.ServiceAccount{}
	err := r.client.Get(ctx, key, serviceAccount)
	if err != nil && errors.IsNotFound(err) {
		log.V(1).Info("Service account not found.")
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	log.V(1).Info("Successfully fetch deployed Service account")
	return serviceAccount, nil
}

func (r *serviceAccountHandler) DeleteServiceAccount(ctx context.Context, key types.NamespacedName) error {
	serviceAccount := &v1.ServiceAccount{}
	err := r.client.Get(ctx, key, serviceAccount)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}
	if err = r.client.Delete(ctx, serviceAccount); err != nil {
		return fmt.Errorf("failed to delete ServiceAccount: %w", err)
	}
	return nil
}
