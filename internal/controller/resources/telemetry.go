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
	telemetryv1alpha1 "istio.io/client-go/pkg/apis/telemetry/v1alpha1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TelemetryHandler to provide telemetry specific implementation.
type TelemetryHandler interface {
	FetchTelemetry(ctx context.Context, log logr.Logger, key types.NamespacedName) (*telemetryv1alpha1.Telemetry, error)
	DeleteTelemetry(ctx context.Context, key types.NamespacedName) error
}

type telemetryHandler struct {
	client client.Client
}

func NewTelemetryHandler(client client.Client) TelemetryHandler {
	return &telemetryHandler{
		client: client,
	}
}

func (r *telemetryHandler) FetchTelemetry(ctx context.Context, log logr.Logger, key types.NamespacedName) (*telemetryv1alpha1.Telemetry, error) {
	telemetry := &telemetryv1alpha1.Telemetry{}
	err := r.client.Get(ctx, key, telemetry)
	if err != nil && errors.IsNotFound(err) {
		log.V(1).Info("Istio Telemetry not found.")
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	log.V(1).Info("Successfully fetch deployed Istio Telemetry")
	return telemetry, nil
}

func (r *telemetryHandler) DeleteTelemetry(ctx context.Context, key types.NamespacedName) error {
	telemetry := &telemetryv1alpha1.Telemetry{}
	err := r.client.Get(ctx, key, telemetry)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}
	if err = r.client.Delete(ctx, telemetry); err != nil {
		return fmt.Errorf("failed to delete Istio Telemetry: %w", err)
	}
	return nil
}
