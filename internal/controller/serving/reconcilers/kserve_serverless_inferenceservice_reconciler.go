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
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	constants "github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
)

var _ Reconciler = (*KserveServerlessInferenceServiceReconciler)(nil)

type KserveServerlessInferenceServiceReconciler struct {
	client                 client.Client
	subResourceReconcilers []SubResourceReconciler
}

func NewKServeServerlessInferenceServiceReconciler(client client.Client, clientReader client.Reader, kClient kubernetes.Interface) *KserveServerlessInferenceServiceReconciler {

	subResourceReconciler := []SubResourceReconciler{
		NewKserveServiceMeshMemberReconciler(client),
		NewKserveRouteReconciler(client, kClient),
		NewKServePrometheusRoleBindingReconciler(client),
		NewKServeIstioTelemetryReconciler(client),
		NewKServeIstioServiceMonitorReconciler(client),
		NewKServeIstioPodMonitorReconciler(client),
		NewKServeIstioPeerAuthenticationReconciler(client),
		NewKServeNetworkPolicyReconciler(client),
		NewKserveAuthConfigReconciler(client),
		NewKserveIsvcServiceReconciler(client),
		NewKserveGatewayReconciler(client, clientReader),
		NewKserveMetricsDashboardReconciler(client),
	}

	return &KserveServerlessInferenceServiceReconciler{
		client:                 client,
		subResourceReconcilers: subResourceReconciler,
	}
}

func (r *KserveServerlessInferenceServiceReconciler) Reconcile(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {
	var reconcileErrors *multierror.Error
	for _, reconciler := range r.subResourceReconcilers {
		reconcileErrors = multierror.Append(reconcileErrors, reconciler.Reconcile(ctx, log, isvc))
	}

	return reconcileErrors.ErrorOrNil()
}

func (r *KserveServerlessInferenceServiceReconciler) OnDeletionOfKserveInferenceService(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {
	var deleteErrors *multierror.Error
	for _, reconciler := range r.subResourceReconcilers {
		deleteErrors = multierror.Append(deleteErrors, reconciler.Delete(ctx, log, isvc))
	}

	return deleteErrors.ErrorOrNil()
}

func (r *KserveServerlessInferenceServiceReconciler) CleanupNamespaceIfNoKserveIsvcExists(ctx context.Context, log logr.Logger, namespace string) error {
	inferenceServiceList := &kservev1beta1.InferenceServiceList{}
	if err := r.client.List(ctx, inferenceServiceList, client.InNamespace(namespace)); err != nil {
		return err
	}

	for i := len(inferenceServiceList.Items) - 1; i >= 0; i-- {
		inferenceService := inferenceServiceList.Items[i]
		isvcDeploymentMode, err := utils.GetDeploymentModeForKServeResource(ctx, r.client, inferenceService.GetAnnotations())
		if err != nil {
			return err
		}
		if isvcDeploymentMode != constants.Serverless {
			inferenceServiceList.Items = append(inferenceServiceList.Items[:i], inferenceServiceList.Items[i+1:]...)
		}
	}

	// If there are no Kserve InferenceServices in the namespace, delete namespace-scoped resources needed for Kserve Metrics
	var cleanupErrors *multierror.Error
	if len(inferenceServiceList.Items) == 0 {
		for _, reconciler := range r.subResourceReconcilers {
			cleanupErrors = multierror.Append(cleanupErrors, reconciler.Cleanup(ctx, log, namespace))
		}
	}

	return cleanupErrors.ErrorOrNil()
}
