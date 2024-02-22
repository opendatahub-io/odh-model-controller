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

type KserveInferenceServiceReconciler struct {
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

func NewKServeInferenceServiceReconciler(client client.Client, scheme *runtime.Scheme) *KserveInferenceServiceReconciler {
	return &KserveInferenceServiceReconciler{
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

func (r *KserveInferenceServiceReconciler) Reconcile(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {

	//  Resource created per namespace
	log.V(1).Info("Verifying that the default ServiceMeshMemberRoll has the target namespace")
	if err := r.istioSMMRReconciler.Reconcile(ctx, log, isvc); err != nil {
		return err
	}

	log.V(1).Info("Verifying that the role binding to enable prometheus access exists")
	if err := r.prometheusRoleBindingReconciler.Reconcile(ctx, log, isvc); err != nil {
		return err
	}

	log.V(1).Info("Creating Istio Telemetry object for target namespace")
	if err := r.istioTelemetryReconciler.Reconcile(ctx, log, isvc); err != nil {
		return err
	}

	log.V(1).Info("Creating Istio ServiceMonitor for target namespace")
	if err := r.istioServiceMonitorReconciler.Reconcile(ctx, log, isvc); err != nil {
		return err
	}

	log.V(1).Info("Creating Istio PodMonitor for target namespace")
	if err := r.istioPodMonitorReconciler.Reconcile(ctx, log, isvc); err != nil {
		return err
	}

	log.V(1).Info("Reconciling PeerAuthentication for target namespace")
	if err := r.istioPeerAuthenticationReconciler.Reconcile(ctx, log, isvc); err != nil {
		return err
	}

	log.V(1).Info("Reconciling NetworkPolicy for target namespace")
	if err := r.networkPolicyReconciler.Reconcile(ctx, log, isvc); err != nil {
		return err
	}

	//  Resource created for each ISVC resource
	log.V(1).Info("Reconciling Authorino AuthConfig for InferenceService")
	if err := r.authConfigReconciler.Reconcile(ctx, log, isvc); err != nil {
		return err
	}

	log.V(1).Info("Reconciling Generic Route for Kserve InferenceService")
	if err := r.routeReconciler.Reconcile(ctx, log, isvc); err != nil {
		return err
	}

	log.V(1).Info("Reconciling Metrics Service for InferenceService")
	if err := r.metricsServiceReconciler.Reconcile(ctx, log, isvc); err != nil {
		return err
	}

	log.V(1).Info("Reconciling Metrics ServiceMonitor for InferenceService")
	if err := r.metricsServiceMonitorReconciler.Reconcile(ctx, log, isvc); err != nil {
		return err
	}

	return nil
}

func (r *KserveInferenceServiceReconciler) OnDeletionOfKserveInferenceService(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {
	log.V(1).Info("Deleting Kserve inference service generic route")
	if err := r.routeReconciler.DeleteRoute(ctx, isvc); err != nil {
		return err
	}

	log.V(1).Info("Deleting Kserve inference service authorino authconfig entry")
	if err := r.authConfigReconciler.Remove(ctx, log, isvc); err != nil {
		return err
	}
	return nil
}

func (r *KserveInferenceServiceReconciler) DeleteKserveMetricsResourcesIfNoKserveIsvcExists(ctx context.Context, log logr.Logger, isvcNamespace string) error {
	inferenceServiceList := &kservev1beta1.InferenceServiceList{}
	if err := r.client.List(ctx, inferenceServiceList, client.InNamespace(isvcNamespace)); err != nil {
		return err
	}

	for i := len(inferenceServiceList.Items) - 1; i >= 0; i-- {
		inferenceService := inferenceServiceList.Items[i]
		if utils.IsDeploymentModeForIsvcModelMesh(&inferenceService) {
			inferenceServiceList.Items = append(inferenceServiceList.Items[:i], inferenceServiceList.Items[i+1:]...)
		}
	}

	// If there are no Kserve InferenceServices in the namespace, delete namespace-scoped resources needed for Kserve Metrics
	if len(inferenceServiceList.Items) == 0 {

		log.V(1).Info("Removing target namespace from ServiceMeshMemberRole")
		if err := r.istioSMMRReconciler.RemoveMemberFromSMMR(ctx, isvcNamespace); err != nil {
			return err
		}

		log.V(1).Info("Deleting Prometheus RoleBinding object for target namespace")
		if err := r.prometheusRoleBindingReconciler.DeleteRoleBinding(ctx, isvcNamespace); err != nil {
			return err
		}

		log.V(1).Info("Deleting Istio Telemetry object for target namespace")
		if err := r.istioTelemetryReconciler.DeleteTelemetry(ctx, isvcNamespace); err != nil {
			return err
		}

		log.V(1).Info("Deleting ServiceMonitor object for target namespace")
		if err := r.istioServiceMonitorReconciler.DeleteServiceMonitor(ctx, isvcNamespace); err != nil {
			return err
		}

		log.V(1).Info("Deleting PodMonitor object for target namespace")
		if err := r.istioPodMonitorReconciler.DeletePodMonitor(ctx, isvcNamespace); err != nil {
			return err
		}

		log.V(1).Info("Deleting PeerAuthentication object for target namespace")
		if err := r.istioPeerAuthenticationReconciler.DeletePeerAuthentication(ctx, isvcNamespace); err != nil {
			return err
		}

		log.V(1).Info("Deleting NetworkPolicy object for target namespace")
		if err := r.networkPolicyReconciler.DeleteNetworkPolicy(ctx, isvcNamespace); err != nil {
			return err
		}
	}
	return nil
}
