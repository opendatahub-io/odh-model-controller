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
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	v1 "maistra.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ServiceMeshMemberHandler interface {
	Fetch(ctx context.Context, log logr.Logger, namespace, name string) (*v1.ServiceMeshMember, error)
}

type serviceMeshMemberHandler struct {
	client client.Client
}

func NewServiceMeshMember(client client.Client) ServiceMeshMemberHandler {
	return &serviceMeshMemberHandler{
		client: client,
	}
}

func (r *serviceMeshMemberHandler) Fetch(ctx context.Context, log logr.Logger, namespace, name string) (*v1.ServiceMeshMember, error) {
	smmr := &v1.ServiceMeshMember{}
	err := r.client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, smmr)
	if err != nil && errors.IsNotFound(err) {
		log.V(1).Info("ServiceMeshMember not found", "smm.name", name)
		return nil, nil
	} else if err != nil {
		return nil, err
	}

	log.V(1).Info("Successfully fetch deployed ServiceMeshMember", "smm.name", name)

	return smmr, nil
}
