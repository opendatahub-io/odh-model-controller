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

	"github.com/go-logr/logr"
	"github.com/hashicorp/go-multierror"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ Reconciler = (*KserveRawInferenceServiceReconciler)(nil)

type KserveRawInferenceServiceReconciler struct {
	client                 client.Client
	subResourceReconcilers []SubResourceReconciler
}

func NewKServeRawInferenceServiceReconciler(client client.Client) *KserveRawInferenceServiceReconciler {

	networkPolicyReconciler := NewKserveRawNetworkPolicyReconciler(client)
	subResourceReconciler := []SubResourceReconciler{
		NewKserveRawClusterRoleBindingReconciler(client),
		networkPolicyReconciler,
		NewKserveRawRouteReconciler(client),
		NewKServeRawMetricsServiceReconciler(client),
		NewKServeRawMetricsServiceMonitorReconciler(client),
		NewKserveMetricsDashboardReconciler(client),
		NewKServeKEDAReconciler(client),
	}

	return &KserveRawInferenceServiceReconciler{
		client:                 client,
		subResourceReconcilers: subResourceReconciler,
	}
}

func (r *KserveRawInferenceServiceReconciler) Reconcile(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {
	var reconcileErrors *multierror.Error
	networkPolicyFailed := false
	for _, reconciler := range r.subResourceReconcilers {
		if networkPolicyFailed {
			if routeReconciler, isRouteReconciler := reconciler.(*KserveRawRouteReconciler); isRouteReconciler {
				// A policy failure must not create or update an exposed Route. This
				// removes an owned Route whether exposure was removed or the
				// policy failure requires fail-closed behavior. It leaves an
				// unrelated same-name Route untouched.
				reconcileErr := routeReconciler.DeleteOwnedRouteOnNetworkPolicyFailure(ctx, log, isvc)
				reconcileErrors = multierror.Append(reconcileErrors, reconcileErr)
			}
			// Continue with independent metrics, dashboard, and KEDA
			// reconciliation. Only Route create/update is gated by the failed
			// NetworkPolicy reconciliation.
			continue
		}

		reconcileErr := reconciler.Reconcile(ctx, log, isvc)
		reconcileErrors = multierror.Append(reconcileErrors, reconcileErr)
		if reconcileErr != nil {
			if _, isNetworkPolicyReconciler := reconciler.(*KserveRawNetworkPolicyReconciler); isNetworkPolicyReconciler {
				// Route create/update must wait until the component policy is
				// successfully created or repaired. Route removal and independent
				// subreconcilers are handled in the gated path above.
				networkPolicyFailed = true
			}
		}
	}

	return reconcileErrors.ErrorOrNil()
}

func (r *KserveRawInferenceServiceReconciler) OnDeletionOfKserveInferenceService(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {
	var deleteErrors *multierror.Error
	for _, reconciler := range r.subResourceReconcilers {
		deleteErrors = multierror.Append(deleteErrors, reconciler.Delete(ctx, log, isvc))
	}

	return deleteErrors.ErrorOrNil()
}

func (r *KserveRawInferenceServiceReconciler) CleanupNamespaceIfNoRawKserveIsvcExists(ctx context.Context, log logr.Logger, namespace string) error {
	var cleanupErrors *multierror.Error
	for _, reconciler := range r.subResourceReconcilers {
		cleanupErrors = multierror.Append(cleanupErrors, reconciler.Cleanup(ctx, log, namespace))
	}
	return cleanupErrors.ErrorOrNil()
}
