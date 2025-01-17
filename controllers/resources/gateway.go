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

	istionetworkclientv1beta1 "istio.io/client-go/pkg/apis/networking/v1beta1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type GatewayHandler interface {
	Get(ctx context.Context, key types.NamespacedName) (*istionetworkclientv1beta1.Gateway, error)
	Update(ctx context.Context, gateway *istionetworkclientv1beta1.Gateway) error
}

type gatewayHandler struct {
	client client.Client
}

func NewGatewayHandler(client client.Client) GatewayHandler {
	return &gatewayHandler{
		client: client,
	}
}

func (g *gatewayHandler) Get(ctx context.Context, key types.NamespacedName) (*istionetworkclientv1beta1.Gateway, error) {
	gateway := &istionetworkclientv1beta1.Gateway{}
	if err := g.client.Get(ctx, key, gateway); err != nil {
		return nil, err
	}
	return gateway, nil
}

func (g *gatewayHandler) Update(ctx context.Context, gateway *istionetworkclientv1beta1.Gateway) error {
	if err := g.client.Update(ctx, gateway); err != nil {
		return fmt.Errorf("could not UPDATE gateway %s/%s. cause %w", gateway.Namespace, gateway.Name, err)
	}
	return nil
}
