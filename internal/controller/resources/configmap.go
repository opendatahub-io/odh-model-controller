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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ConfigMapHandler interface {
	FetchConfigMap(ctx context.Context, log logr.Logger, key types.NamespacedName) (*corev1.ConfigMap, error)
}

type configMapHandler struct {
	client client.Client
}

func NewConfigMapHandler(client client.Client) ConfigMapHandler {
	return &configMapHandler{
		client: client,
	}
}

func (r *configMapHandler) FetchConfigMap(ctx context.Context, log logr.Logger, key types.NamespacedName) (*corev1.ConfigMap, error) {
	configMap := &corev1.ConfigMap{}
	err := r.client.Get(ctx, key, configMap)
	if err != nil && errors.IsNotFound(err) {
		log.V(1).Info("ConfigMap not found.")
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	log.V(1).Info("Successfully fetched ConfigMap", "ConfigMap:", key.Name)
	return configMap, nil
}
