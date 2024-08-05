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

package reconcilers

import (
	"context"
	"github.com/hashicorp/go-multierror"

	"github.com/go-logr/logr"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ Reconciler = (*KserveRawInferenceServiceReconciler)(nil)

type KserveRawInferenceServiceReconciler struct {
	client                 client.Client
	subResourceReconcilers []SubResourceReconciler
}

func NewKServeRawInferenceServiceReconciler(client client.Client) *KserveRawInferenceServiceReconciler {

	subResourceReconciler := []SubResourceReconciler{
		NewKserveRawRouteReconciler(client),
		NewKServeRawMetricsServiceReconciler(client),
		NewRawKServeMetricsServiceMonitorReconciler(client),
		NewKserveMetricsDashboardReconciler(client),
	}

	return &KserveRawInferenceServiceReconciler{
		client:                 client,
		subResourceReconcilers: subResourceReconciler,
	}
}

func (r *KserveRawInferenceServiceReconciler) Reconcile(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {
	var reconcileErrors *multierror.Error
	for _, reconciler := range r.subResourceReconcilers {
		reconcileErrors = multierror.Append(reconcileErrors, reconciler.Reconcile(ctx, log, isvc))
	}

	return reconcileErrors.ErrorOrNil()
}
