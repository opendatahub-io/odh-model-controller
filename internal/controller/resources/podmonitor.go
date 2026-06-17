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

type PodMonitorHandler interface {
	FetchPodMonitor(ctx context.Context, log logr.Logger, key types.NamespacedName) (*v1.PodMonitor, error)
	DeletePodMonitor(ctx context.Context, key types.NamespacedName) error
}

type podMonitorHandler struct {
	client client.Client
}

func NewPodMonitorHandler(client client.Client) PodMonitorHandler {
	return &podMonitorHandler{
		client: client,
	}
}

func (r *podMonitorHandler) FetchPodMonitor(ctx context.Context, log logr.Logger, key types.NamespacedName) (*v1.PodMonitor, error) {
	podMonitor := &v1.PodMonitor{}
	err := r.client.Get(ctx, key, podMonitor)
	if err != nil && errors.IsNotFound(err) {
		log.V(1).Info("PodMonitor not found.")
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	log.V(1).Info("Successfully fetch deployed PodMonitor")
	return podMonitor, nil
}

func (r *podMonitorHandler) DeletePodMonitor(ctx context.Context, key types.NamespacedName) error {
	podMonitor := &v1.PodMonitor{}
	err := r.client.Get(ctx, key, podMonitor)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}
	if err = r.client.Delete(ctx, podMonitor); err != nil {
		return fmt.Errorf("failed to delete PodMonitor: %w", err)
	}
	return nil
}
