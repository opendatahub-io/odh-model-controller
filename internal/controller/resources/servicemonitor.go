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
	v1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ServiceMonitorHandler interface {
	FetchServiceMonitor(ctx context.Context, log logr.Logger, key types.NamespacedName) (*v1.ServiceMonitor, error)
	DeleteServiceMonitor(ctx context.Context, key types.NamespacedName) error
}

type serviceMonitorHandler struct {
	client client.Client
}

func NewServiceMonitorHandler(client client.Client) ServiceMonitorHandler {
	return &serviceMonitorHandler{
		client: client,
	}
}

func (r *serviceMonitorHandler) FetchServiceMonitor(ctx context.Context, log logr.Logger, key types.NamespacedName) (*v1.ServiceMonitor, error) {
	serviceMonitor := &v1.ServiceMonitor{}
	err := r.client.Get(ctx, key, serviceMonitor)
	if err != nil && errors.IsNotFound(err) {
		log.V(1).Info("ServiceMonitor not found.")
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	log.V(1).Info("Successfully fetch deployed ServiceMonitor")
	return serviceMonitor, nil
}

func (r *serviceMonitorHandler) DeleteServiceMonitor(ctx context.Context, key types.NamespacedName) error {
	serviceMonitor := &v1.ServiceMonitor{}
	err := r.client.Get(ctx, key, serviceMonitor)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}
	if err = r.client.Delete(ctx, serviceMonitor); err != nil {
		return fmt.Errorf("failed to delete ServiceMonitor: %w", err)
	}
	return nil
}
