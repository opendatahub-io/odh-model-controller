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

type ServiceMeshMemberRollHandler interface {
	FetchSMMR(ctx context.Context, log logr.Logger, key types.NamespacedName) (*v1.ServiceMeshMemberRoll, error)
}

type serviceMeshMemberRollHandler struct {
	client client.Client
}

func NewServiceMeshMemberRole(client client.Client) ServiceMeshMemberRollHandler {
	return &serviceMeshMemberRollHandler{
		client: client,
	}
}

func (r *serviceMeshMemberRollHandler) FetchSMMR(ctx context.Context, log logr.Logger, key types.NamespacedName) (*v1.ServiceMeshMemberRoll, error) {
	smmr := &v1.ServiceMeshMemberRoll{}
	err := r.client.Get(ctx, key, smmr)
	if err != nil && errors.IsNotFound(err) {
		log.V(1).Info("ServiceMeshMemberRole not found.")
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	log.V(1).Info("Successfully fetch deployed ServiceMeshMemberRole")
	return smmr, nil
}
