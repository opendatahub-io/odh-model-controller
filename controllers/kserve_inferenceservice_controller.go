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

package controllers

import (
	"context"

	kservev1beta1 "github.com/kserve/modelmesh-serving/apis/serving/v1beta1"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"istio.io/api/security/v1beta1"
	"istio.io/api/telemetry/v1alpha1"
	istiotypes "istio.io/api/type/v1beta1"
	istiosecv1beta1 "istio.io/client-go/pkg/apis/security/v1beta1"
	telemetryv1alpha1 "istio.io/client-go/pkg/apis/telemetry/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	k8srbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	maistrav1 "maistra.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	ServiceMeshLabelKey                = "opendatahub.io/service-mesh"
	peerAuthenticationName             = "default"
	networkPolicyName                  = "allow-from-openshift-monitoring-ns"
	userWorkloadMonitoringNS           = "openshift-user-workload-monitoring"
	serviceMeshMemberRollName          = "default"
	telemetryName                      = "enable-prometheus-metrics"
	istioServiceMonitorName            = "istiod-monitor"
	istioPodMonitorName                = "istio-proxies-monitor"
	clusterPrometheusAccessRoleBinding = "kserve-prometheus-k8s"
	clusterPrometheusAccessRole        = "kserve-prometheus-k8s"
	InferenceSercviceLabelName         = "serving.kserve.io/inferenceservice"
	TelemetryCRD                       = "telemetries.telemetry.istio.io"
	PeerAuthCRD                        = "peerauthentications.security.istio.io"
)

func (r *OpenshiftInferenceServiceReconciler) ensurePrometheusRoleBinding(ctx context.Context, req ctrl.Request, ns string) error {
	// Initialize logger format
	log := r.Log.WithValues("namespace", ns)

	roleBinding := &k8srbacv1.RoleBinding{}
	roleBindingFound := true
	err := r.Client.Get(ctx, types.NamespacedName{Name: clusterPrometheusAccessRoleBinding, Namespace: ns}, roleBinding)
	if err != nil {
		if apierrs.IsNotFound(err) {
			roleBindingFound = false
		} else {
			r.Log.Error(err, "Unable to get RoleBinding")
			return err
		}
	}
	if !roleBindingFound {
		desiredRoleBinding := &k8srbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterPrometheusAccessRoleBinding,
				Namespace: ns,
			},
			RoleRef: k8srbacv1.RoleRef{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "ClusterRole",
				Name:     "kserve-prometheus-k8s",
			},
			Subjects: []k8srbacv1.Subject{
				{
					Kind:      "ServiceAccount",
					Name:      "prometheus-k8s",
					Namespace: "openshift-monitoring",
				},
			},
		}
		err = r.Client.Create(ctx, desiredRoleBinding)
		if err != nil {
			r.Log.Error(err, "Unable to create RoleBinding for Prometheus Access")
			return err
		}
	}
	log.Info("RoleBinding for Prometheus Access already exists in target namespace ")
	return nil
}

func (r *OpenshiftInferenceServiceReconciler) ensureServiceMeshMemberRollEntry(ctx context.Context, req ctrl.Request, ns string) error {
	// Initialize logger format
	log := r.Log.WithValues("namespace", ns)
	observedServiceMeshMemberRoll := &maistrav1.ServiceMeshMemberRoll{}
	err := r.Client.Get(ctx, types.NamespacedName{Name: serviceMeshMemberRollName, Namespace: "istio-system"}, observedServiceMeshMemberRoll)
	if err != nil {
		if apierrs.IsNotFound(err) {
			log.Error(err, "default ServiceMeshMemberRoll not found in namespace: istio-system")
			return err
		}
		log.Error(err, "Unable to get ServiceMeshMemberRoll in namespace: istio-system")
		return err
	}
	//check if the namespace is already in the list, if it does not exists, append and update
	serviceMeshMemberRollEntryExists := false
	memberList := observedServiceMeshMemberRoll.Spec.Members
	for _, member := range memberList {
		if member == ns {
			serviceMeshMemberRollEntryExists = true
		}
	}
	if !serviceMeshMemberRollEntryExists {
		observedServiceMeshMemberRoll.Spec.Members = append(memberList, ns)
		err := r.Client.Update(ctx, observedServiceMeshMemberRoll)
		if err != nil {
			log.Error(err, "Unable to add namespace to default ServiceMeshMemberRoll")
			return err
		}
	}
	log.Info("default ServiceMeshMemberRoll already has the target namespace")
	return nil
}

func (r *OpenshiftInferenceServiceReconciler) createOrUpdateIstioTelemetry(ctx context.Context, req ctrl.Request, ns string) error {
	// Initialize logger format
	log := r.Log.WithValues("namespace", ns)

	telemetry := &telemetryv1alpha1.Telemetry{}
	telemetryFound := true
	err := r.Client.Get(ctx, types.NamespacedName{Name: telemetryName, Namespace: ns}, telemetry)
	if err != nil {
		if apierrs.IsNotFound(err) {
			telemetryFound = false
		} else {
			log.Error(err, "Unable to get Telemetry object")
			return err
		}
	}
	if !telemetryFound {
		desiredTelemetry := &telemetryv1alpha1.Telemetry{
			ObjectMeta: metav1.ObjectMeta{
				Name:      telemetryName,
				Namespace: ns,
			},
			Spec: v1alpha1.Telemetry{
				Selector: &istiotypes.WorkloadSelector{
					MatchLabels: map[string]string{
						"component": "predictor",
					},
				},
				Metrics: []*v1alpha1.Metrics{
					{
						Providers: []*v1alpha1.ProviderRef{
							{
								Name: "prometheus",
							},
						},
					},
				},
			},
		}
		err = r.Client.Create(ctx, desiredTelemetry)
		if err != nil {
			log.Error(err, "Unable to create Telemetry object")
			return err
		}
	}
	log.Info("Telemetry already exists in target namespace ")
	return nil
}

func (r *OpenshiftInferenceServiceReconciler) createOrUpdateIstioServiceMonitor(ctx context.Context, req ctrl.Request, ns string) error {

	// Initialize logger format
	log := r.Log.WithValues("namespace", ns)

	istioServiceMonitor := &monitoringv1.ServiceMonitor{}
	istioServiceMonitorFound := true
	err := r.Client.Get(ctx, types.NamespacedName{Name: istioServiceMonitorName, Namespace: ns}, istioServiceMonitor)
	if err != nil {
		if apierrs.IsNotFound(err) {
			istioServiceMonitorFound = false
		} else {
			log.Error(err, "Unable to get Istio ServiceMonitor")
			return err
		}
	}
	if !istioServiceMonitorFound {
		desiredServiceMonitor := &monitoringv1.ServiceMonitor{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "istiod-monitor",
				Namespace: ns,
			},
			Spec: monitoringv1.ServiceMonitorSpec{
				Selector: metav1.LabelSelector{
					MatchLabels: map[string]string{
						"istio": "pilot",
					},
				},
				TargetLabels: []string{"app"},
				Endpoints: []monitoringv1.Endpoint{
					{
						Port:     "http-monitoring",
						Interval: "30s",
					},
				},
			},
		}
		err = r.Client.Create(ctx, desiredServiceMonitor)
		if err != nil {
			log.Error(err, "Unable to create Istio ServiceMonitor")
			return err
		}
	}
	log.Info("Istio ServiceMonitor already exists in target namespace ")
	return nil
}

func (r *OpenshiftInferenceServiceReconciler) createOrUpdateIstioPodMonitor(ctx context.Context, req ctrl.Request, ns string) error {
	// Initialize logger format
	log := r.Log.WithValues("namespace", ns)

	istioPodMonitor := &monitoringv1.PodMonitor{}
	istioPodMonitorFound := true
	err := r.Client.Get(ctx, types.NamespacedName{Name: istioPodMonitorName, Namespace: ns}, istioPodMonitor)
	if err != nil {
		if apierrs.IsNotFound(err) {
			istioPodMonitorFound = false
		} else {
			log.Error(err, "Unable to get Istio PodMonitor")
			return err
		}
	}
	if !istioPodMonitorFound {
		desiredPodMonitor := &monitoringv1.PodMonitor{
			ObjectMeta: metav1.ObjectMeta{
				Name:      istioPodMonitorName,
				Namespace: ns,
			},
			Spec: monitoringv1.PodMonitorSpec{
				Selector: metav1.LabelSelector{
					MatchExpressions: []metav1.LabelSelectorRequirement{
						{
							Key:      "istio-prometheus-ignore",
							Operator: metav1.LabelSelectorOpDoesNotExist,
						},
					},
				},
				PodMetricsEndpoints: []monitoringv1.PodMetricsEndpoint{
					{
						Path:     "/stats/prometheus",
						Interval: "30s",
					},
				},
			},
		}
		err = r.Client.Create(ctx, desiredPodMonitor)
		if err != nil {
			log.Error(err, "Unable to create Istio PodMonitor")
			return err
		}
	}
	log.Info("Istio PodMonitor already exists in target namespace ")
	return nil
}

func (r *OpenshiftInferenceServiceReconciler) createOrUpdatePeerAuth(ctx context.Context, req ctrl.Request, inferenceService *kservev1beta1.InferenceService) error {
	// Initialize logger format
	log := r.Log.WithValues("namespace", inferenceService.Namespace)

	peerAuthFound := true
	peerAuth := &istiosecv1beta1.PeerAuthentication{}
	err := r.Client.Get(ctx, types.NamespacedName{Name: peerAuthenticationName, Namespace: inferenceService.Namespace}, peerAuth)
	if err != nil {
		if apierrs.IsNotFound(err) {
			peerAuthFound = false
		} else {
			log.Error(err, "Unable to get PeerAuthentication")
			return err
		}
	}

	if !peerAuthFound {
		desiredPeerAuth := &istiosecv1beta1.PeerAuthentication{
			ObjectMeta: metav1.ObjectMeta{
				Name:      peerAuthenticationName,
				Namespace: req.Namespace,
			},
			Spec: v1beta1.PeerAuthentication{
				Selector: &istiotypes.WorkloadSelector{
					MatchLabels: map[string]string{
						"component": "predictor",
					},
				},
				Mtls: &v1beta1.PeerAuthentication_MutualTLS{Mode: 3},
				PortLevelMtls: map[uint32]*v1beta1.PeerAuthentication_MutualTLS{
					8086: {Mode: 2},
					3000: {Mode: 2},
				},
			},
		}
		err = r.Client.Create(ctx, desiredPeerAuth)
		if err != nil {
			log.Error(err, "Unable to create PeerAuthentication")
			return err
		}
	}
	log.Info("PeerAuth already exists for target namespace ")
	return nil
}

func (r *OpenshiftInferenceServiceReconciler) createOrUpdateNetworkPolicy(ctx context.Context, req ctrl.Request, inferenceService *kservev1beta1.InferenceService) error {
	// Initialize logger format
	log := r.Log.WithValues("namespace", inferenceService.Namespace)

	networkPolicy := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      networkPolicyName,
			Namespace: inferenceService.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/version":               "release-v1.9",
				"networking.knative.dev/ingress-provider": "istio",
			},
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			Ingress: []networkingv1.NetworkPolicyIngressRule{
				{
					From: []networkingv1.NetworkPolicyPeer{
						{
							NamespaceSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"name": "openshift-user-workload-monitoring",
								},
							},
						},
					},
				},
			},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
		},
	}

	if err := r.Client.Create(ctx, networkPolicy); err != nil {
		if errors.IsAlreadyExists(err) {
			log.Info("NetworkPolicy already exists for target namespace ")
			if err := r.Client.Update(ctx, networkPolicy); err != nil {
				return err
			}
		} else {
			log.Error(err, "Unable to create Networkpolicy")
			return err
		}
	}
	return nil
}

func (r *OpenshiftInferenceServiceReconciler) createOrUpdateMetricsService(ctx context.Context, req ctrl.Request, inferenceService *kservev1beta1.InferenceService) error {
	// Initialize logger format
	log := r.Log.WithValues("InferenceService", inferenceService.Name, "namespace", inferenceService.Namespace)

	serviceMetricsName := inferenceService.Name + "-metrics"

	metricsService := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceMetricsName,
			Namespace: inferenceService.Namespace,
			Labels: map[string]string{
				"name": serviceMetricsName,
			},
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Name:       "caikit-metrics",
					Protocol:   corev1.ProtocolTCP,
					Port:       8086,
					TargetPort: intstr.FromInt(8086),
				},
				{
					Name:       "tgis-metrics",
					Protocol:   corev1.ProtocolTCP,
					Port:       3000,
					TargetPort: intstr.FromInt(3000),
				},
			},
			Type: corev1.ServiceTypeClusterIP,
			Selector: map[string]string{
				InferenceSercviceLabelName: inferenceService.Name,
			},
		},
	}
	err := ctrl.SetControllerReference(inferenceService, metricsService, r.Scheme)
	if err != nil {
		log.Error(err, "Unable to add OwnerReference to the Metrics Service")
		return err
	}
	if err := r.Client.Create(ctx, metricsService); err != nil {
		if errors.IsAlreadyExists(err) {
			log.Info("Metrics Service already exists for InferenceService ")
			if err := r.Client.Update(ctx, metricsService); err != nil {
				return err
			}
		} else {
			log.Error(err, "Unable to create Metrics Service")
			return err
		}
	}
	return nil
}

func (r *OpenshiftInferenceServiceReconciler) createOrUpdateMetricsServiceMonitor(ctx context.Context, req ctrl.Request, inferenceService *kservev1beta1.InferenceService) error {
	// Initialize logger format
	log := r.Log.WithValues("InferenceService", inferenceService.Name, "namespace", inferenceService.Namespace)

	serviceMetricsName := inferenceService.Name + "-metrics"

	metricsServiceMonitor := &monitoringv1.ServiceMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceMetricsName,
			Namespace: inferenceService.Namespace,
		},
		Spec: monitoringv1.ServiceMonitorSpec{
			Endpoints: []monitoringv1.Endpoint{
				{
					Port:   "caikit-metrics",
					Scheme: "http",
				},
				{
					Port:   "tgis-metrics",
					Scheme: "http",
				},
			},
			NamespaceSelector: monitoringv1.NamespaceSelector{},
			Selector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					"name": serviceMetricsName,
				},
			},
		},
	}
	err := ctrl.SetControllerReference(inferenceService, metricsServiceMonitor, r.Scheme)
	if err != nil {
		log.Error(err, "Unable to add OwnerReference to the Metrics ServiceMonitor")
		return err
	}
	if err = r.Client.Create(ctx, metricsServiceMonitor); err != nil {
		if errors.IsAlreadyExists(err) {
			log.Info("Metrics ServiceMonitor already exists for InferenceService")
			if err := r.Client.Update(ctx, metricsServiceMonitor); err != nil {
				return err
			}
		} else {
			log.Error(err, "Unable to create Metrics ServiceMonitor")
			return err
		}
	}
	return nil
}

func (r *OpenshiftInferenceServiceReconciler) DeleteKserveMetricsResourcesIfNoKserveIsvcExists(ctx context.Context, req ctrl.Request, ns string) error {
	// Initialize logger format
	log := r.Log.WithValues("namespace", ns)

	inferenceServiceList := &kservev1beta1.InferenceServiceList{}
	err := r.List(ctx, inferenceServiceList, client.InNamespace(req.Namespace))
	if err != nil {
		log.Error(err, "Unable to get the list of InferenceServices in the namespace. Cannot delete PeerAuthentication and NetworkPolicy.")
		return err
	}

	for i := len(inferenceServiceList.Items) - 1; i >= 0; i-- {
		inferenceService := inferenceServiceList.Items[i]
		if r.isDeploymentModeForIsvcModelMesh(&inferenceService) {
			inferenceServiceList.Items = append(inferenceServiceList.Items[:i], inferenceServiceList.Items[i+1:]...)
		}
	}

	// If there are no Kserve InferenceServices in the namespace, delete namespace-scoped resources needed for Kserve Metrics
	if len(inferenceServiceList.Items) == 0 {
		peerAuth := &istiosecv1beta1.PeerAuthentication{
			ObjectMeta: metav1.ObjectMeta{
				Name:      peerAuthenticationName,
				Namespace: req.Namespace,
			},
		}

		err := r.Delete(ctx, peerAuth)
		if err != nil && !apierrs.IsNotFound(err) {
			log.Error(err, "No Kserve InferenceServices exist in the namespace. Unable to delete PeerAuthentication")
			return err
		}

		networkPolicy := &networkingv1.NetworkPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      networkPolicyName,
				Namespace: req.Namespace,
			},
		}

		err = r.Delete(ctx, networkPolicy)
		if err != nil && !apierrs.IsNotFound(err) {
			log.Error(err, "No Kserve InferenceServices exist in the namespace. Unable to delete NetworkPolicy")
			return err
		}

		serviceMeshMemberRoll := &maistrav1.ServiceMeshMemberRoll{}
		err = r.Client.Get(ctx, types.NamespacedName{Name: serviceMeshMemberRollName, Namespace: "istio-system"}, serviceMeshMemberRoll)
		if err != nil {
			log.Error(err, "Failed to get ServiceMeshMemberRoll.")
			return err
		}
		serviceMeshMemberList := serviceMeshMemberRoll.Spec.Members
		// remove the namespace from the list
		for i, member := range serviceMeshMemberList {
			if member == req.Namespace {
				serviceMeshMemberList = append(serviceMeshMemberList[:i], serviceMeshMemberList[i+1:]...)
			}
		}
		serviceMeshMemberRoll.Spec.Members = serviceMeshMemberList
		err = r.Client.Update(ctx, serviceMeshMemberRoll)
		if err != nil {
			log.Error(err, "No Kserve InferenceServices exist in the namespace. Unable to delete namespace from default ServiceMeshMemberRoll.")
			return err
		}

		telemetry := &telemetryv1alpha1.Telemetry{
			ObjectMeta: metav1.ObjectMeta{
				Name:      telemetryName,
				Namespace: req.Namespace,
			},
		}
		err = r.Delete(ctx, telemetry)
		if err != nil && !apierrs.IsNotFound(err) {
			log.Error(err, "No Kserve InferenceServices exist in the namespace. Unable to delete Telemetry object.")
			return err
		}

		istioServiceMonitor := &monitoringv1.ServiceMonitor{
			ObjectMeta: metav1.ObjectMeta{
				Name:      istioServiceMonitorName,
				Namespace: req.Namespace,
			},
		}
		err = r.Delete(ctx, istioServiceMonitor)
		if err != nil && !apierrs.IsNotFound(err) {
			log.Error(err, "No Kserve InferenceServices exist in the namespace. Unable to delete Istio ServiceMonitor.")
			return err
		}

		istioPodMonitor := &monitoringv1.PodMonitor{
			ObjectMeta: metav1.ObjectMeta{
				Name:      istioPodMonitorName,
				Namespace: req.Namespace,
			},
		}
		err = r.Delete(ctx, istioPodMonitor)
		if err != nil && !apierrs.IsNotFound(err) {
			log.Error(err, "No Kserve InferenceServices exist in the namespace. Unable to delete Istio PodMonitor.")
			return err
		}

		prometheusRoleBinding := &k8srbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterPrometheusAccessRoleBinding,
				Namespace: req.Namespace,
			},
		}
		err = r.Delete(ctx, prometheusRoleBinding)
		if err != nil && !apierrs.IsNotFound(err) {
			log.Error(err, "No Kserve InferenceServices exist in the namespace. Unable to delete RoleBinding for Prometheus Access.")
			return err
		}
	}
	return nil
}

func (r *OpenshiftInferenceServiceReconciler) ReconcileKserveInference(ctx context.Context, req ctrl.Request, inferenceService *kservev1beta1.InferenceService) error {

	// Initialize logger format
	log := r.Log.WithValues("InferenceService", inferenceService.Name, "namespace", inferenceService.Namespace)

	//Create the metrics service and servicemonitor with OwnerReferences, as these are not common namespace-scope resources
	log.Info("Reconciling Metrics Service for InferenceSercvice")
	err := r.createOrUpdateMetricsService(ctx, req, inferenceService)
	if err != nil {
		return err
	}

	log.Info("Reconciling Metrics ServiceMonitor for InferenceSercvice")
	err = r.createOrUpdateMetricsServiceMonitor(ctx, req, inferenceService)
	if err != nil {
		return err
	}

	log.Info("Verifying that the rolebinding to enable prometheus access exists")
	err = r.ensurePrometheusRoleBinding(ctx, req, inferenceService.Namespace)
	if err != nil {
		return err
	}

	log.Info("Verifying that the default ServiceMeshMemberRoll has the target namespace")
	err = r.ensureServiceMeshMemberRollEntry(ctx, req, inferenceService.Namespace)
	if err != nil {
		return err
	}

	log.Info("Creating Istio Telemetry object for target namespace")
	err = r.createOrUpdateIstioTelemetry(ctx, req, inferenceService.Namespace)
	if err != nil {
		return err
	}

	log.Info("Creating Istio ServiceMonitor for target namespace")
	err = r.createOrUpdateIstioServiceMonitor(ctx, req, inferenceService.Namespace)
	if err != nil {
		return err
	}

	log.Info("Creating Istio PodMonitor for target namespace")
	err = r.createOrUpdateIstioPodMonitor(ctx, req, inferenceService.Namespace)
	if err != nil {
		return err
	}

	log.Info("Reconciling PeerAuthentication for target namespace")
	err = r.createOrUpdatePeerAuth(ctx, req, inferenceService)
	if err != nil {
		return err
	}

	log.Info("Reconciling NetworkPolicy for target namespace")
	err = r.createOrUpdateNetworkPolicy(ctx, req, inferenceService)
	if err != nil {
		return err
	}
	return nil
}
