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
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"github.com/opendatahub-io/odh-model-controller/controllers/utils"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ Reconciler = (*KserveServerlessInferenceServiceReconciler)(nil)

type KserveServerlessInferenceServiceReconciler struct {
	client                            client.Client
	routeReconciler                   *KserveRouteReconciler
	metricsServiceReconciler          *KserveMetricsServiceReconciler
	metricsServiceMonitorReconciler   *KserveMetricsServiceMonitorReconciler
	prometheusRoleBindingReconciler   *KservePrometheusRoleBindingReconciler
	istioSMMRReconciler               *KserveIstioSMMRReconciler
	istioTelemetryReconciler          *KserveIstioTelemetryReconciler
	istioServiceMonitorReconciler     *KserveIstioServiceMonitorReconciler
	istioPodMonitorReconciler         *KserveIstioPodMonitorReconciler
	istioPeerAuthenticationReconciler *KserveIstioPeerAuthenticationReconciler
	networkPolicyReconciler           *KserveNetworkPolicyReconciler
	authConfigReconciler              *KserveAuthConfigReconciler
}

func NewKServeServerlessInferenceServiceReconciler(client client.Client, scheme *runtime.Scheme) *KserveServerlessInferenceServiceReconciler {
	return &KserveServerlessInferenceServiceReconciler{
		client:                            client,
		istioSMMRReconciler:               NewKServeIstioSMMRReconciler(client, scheme),
		routeReconciler:                   NewKserveRouteReconciler(client, scheme),
		metricsServiceReconciler:          NewKServeMetricsServiceReconciler(client, scheme),
		metricsServiceMonitorReconciler:   NewKServeMetricsServiceMonitorReconciler(client, scheme),
		prometheusRoleBindingReconciler:   NewKServePrometheusRoleBindingReconciler(client, scheme),
		istioTelemetryReconciler:          NewKServeIstioTelemetryReconciler(client, scheme),
		istioServiceMonitorReconciler:     NewKServeIstioServiceMonitorReconciler(client, scheme),
		istioPodMonitorReconciler:         NewKServeIstioPodMonitorReconciler(client, scheme),
		istioPeerAuthenticationReconciler: NewKServeIstioPeerAuthenticationReconciler(client, scheme),
		networkPolicyReconciler:           NewKServeNetworkPolicyReconciler(client, scheme),
		authConfigReconciler:              NewKserveAuthConfigReconciler(client, scheme),
	}
}

// TODO(reconciler): make it a slice. keep order
func (r *KserveServerlessInferenceServiceReconciler) Reconcile(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {
	//  Resource created per namespace

	if err := r.istioSMMRReconciler.Reconcile(ctx, log, isvc); err != nil {
		return err
	}

	if err := r.prometheusRoleBindingReconciler.Reconcile(ctx, log, isvc); err != nil {
		return err
	}

	if err := r.istioTelemetryReconciler.Reconcile(ctx, log, isvc); err != nil {
		return err
	}

	if err := r.istioServiceMonitorReconciler.Reconcile(ctx, log, isvc); err != nil {
		return err
	}

	if err := r.istioPodMonitorReconciler.Reconcile(ctx, log, isvc); err != nil {
		return err
	}

	if err := r.istioPeerAuthenticationReconciler.Reconcile(ctx, log, isvc); err != nil {
		return err
	}

	if err := r.networkPolicyReconciler.Reconcile(ctx, log, isvc); err != nil {
		return err
	}

	if err := r.authConfigReconciler.Reconcile(ctx, log, isvc); err != nil {
		return err
	}

	if err := r.routeReconciler.Reconcile(ctx, log, isvc); err != nil {
		return err
	}

	if err := r.metricsServiceReconciler.Reconcile(ctx, log, isvc); err != nil {
		return err
	}

	if err := r.metricsServiceMonitorReconciler.Reconcile(ctx, log, isvc); err != nil {
		return err
	}

	return nil
}

func (r *KserveServerlessInferenceServiceReconciler) OnDeletionOfKserveInferenceService(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {

	// TODO(reconciler): shouldnt we iterate over all deletes?
	if err := r.routeReconciler.Delete(ctx, log, isvc); err != nil {
		return err
	}

	if err := r.authConfigReconciler.Delete(ctx, log, isvc); err != nil {
		return err
	}
	return nil
}

func (r *KserveServerlessInferenceServiceReconciler) DeleteKserveMetricsResourcesIfNoKserveIsvcExists(ctx context.Context, log logr.Logger, isvcNamespace string) error {
	inferenceServiceList := &kservev1beta1.InferenceServiceList{}
	if err := r.client.List(ctx, inferenceServiceList, client.InNamespace(isvcNamespace)); err != nil {
		return err
	}

	for i := len(inferenceServiceList.Items) - 1; i >= 0; i-- {
		inferenceService := inferenceServiceList.Items[i]
		isvcDeploymentMode, err := utils.GetDeploymentModeForIsvc(ctx, r.client, &inferenceService)
		if err != nil {
			return err
		}
		if isvcDeploymentMode != utils.Serverless {
			inferenceServiceList.Items = append(inferenceServiceList.Items[:i], inferenceServiceList.Items[i+1:]...)
		}
	}

	// If there are no Kserve InferenceServices in the namespace, delete namespace-scoped resources needed for Kserve Metrics
	if len(inferenceServiceList.Items) == 0 {

		if err := r.istioSMMRReconciler.Cleanup(ctx, log, isvcNamespace); err != nil {
			return err
		}

		if err := r.prometheusRoleBindingReconciler.Cleanup(ctx, log, isvcNamespace); err != nil {
			return err
		}

		if err := r.istioTelemetryReconciler.Cleanup(ctx, log, isvcNamespace); err != nil {
			return err
		}

		if err := r.istioServiceMonitorReconciler.Cleanup(ctx, log, isvcNamespace); err != nil {
			return err
		}

		if err := r.istioPodMonitorReconciler.Cleanup(ctx, log, isvcNamespace); err != nil {
			return err
		}

		if err := r.istioPeerAuthenticationReconciler.Cleanup(ctx, log, isvcNamespace); err != nil {
			return err
		}

		if err := r.networkPolicyReconciler.Cleanup(ctx, log, isvcNamespace); err != nil {
			return err
		}
	}
	return nil
}
