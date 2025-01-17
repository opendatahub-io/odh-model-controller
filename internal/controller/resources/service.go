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

	"github.com/go-logr/logr"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ServiceHandler interface {
	FetchService(ctx context.Context, log logr.Logger, key types.NamespacedName) (*v1.Service, error)
}

type serviceHandler struct {
	client client.Client
}

func NewServiceHandler(client client.Client) ServiceHandler {
	return &serviceHandler{
		client: client,
	}
}

func (r *serviceHandler) FetchService(ctx context.Context, log logr.Logger, key types.NamespacedName) (*v1.Service, error) {
	svc := &v1.Service{}
	err := r.client.Get(ctx, key, svc)
	if err != nil && errors.IsNotFound(err) {
		log.V(1).Info("Service not found.")
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	log.V(1).Info("Successfully fetch deployed Service")
	return svc, nil
}
