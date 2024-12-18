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
	v1 "github.com/openshift/api/route/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// RouteHandler to provide route specific implementation.
type RouteHandler interface {
	FetchRoute(ctx context.Context, log logr.Logger, key types.NamespacedName) (*v1.Route, error)
	DeleteRoute(ctx context.Context, key types.NamespacedName) error
}

type routeHandler struct {
	client client.Client
}

func NewRouteHandler(client client.Client) RouteHandler {
	return &routeHandler{
		client: client,
	}
}

func (r *routeHandler) FetchRoute(ctx context.Context, log logr.Logger, key types.NamespacedName) (*v1.Route, error) {
	route := &v1.Route{}
	err := r.client.Get(ctx, key, route)
	if err != nil && errors.IsNotFound(err) {
		log.V(1).Info("Openshift Route not found.")
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	log.V(1).Info("Successfully fetch deployed Openshift Route")
	return route, nil
}

func (r *routeHandler) DeleteRoute(ctx context.Context, key types.NamespacedName) error {
	route := &v1.Route{}
	err := r.client.Get(ctx, key, route)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}
	if err = r.client.Delete(ctx, route); err != nil {
		return fmt.Errorf("failed to delete route: %w", err)
	}
	return nil
}
