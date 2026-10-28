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
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/go-logr/logr"
	"github.com/hashicorp/go-multierror"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	kserveconstants "github.com/kserve/kserve/pkg/constants"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	knativekmeta "knative.dev/pkg/kmeta"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
)

const (
	predictorNetworkPolicyComponent   = "predictor"
	transformerNetworkPolicyComponent = "transformer"
	rawNetworkPolicyNameSuffix        = "-network-policy"
	rawNetworkPolicyManagedComponent  = "kserve-inferenceservice-network-policy"
	monitoringNamespaceEnvVar         = "MONITORING_NAMESPACE"
	platformMonitoringNamespace       = "openshift-monitoring"
	userWorkloadMonitoringNamespace   = "openshift-user-workload-monitoring"
	rhoaiMonitoringNamespace          = "redhat-ods-monitoring"
	openshiftIngressPolicyGroupLabel  = "policy-group.network.openshift.io/ingress"
)

const (
	managedByLabel = "app.kubernetes.io/managed-by"
	managedByValue = "odh-model-controller"
)

var _ SubResourceReconciler = (*KserveRawNetworkPolicyReconciler)(nil)

// KserveRawNetworkPolicyReconciler owns the NetworkPolicies that protect raw
// InferenceService Pods. The policies are deliberately per component: this
// keeps Route access to the component selected by the Route reconciler and
// prevents an explainer or another ISVC from being selected accidentally.
type KserveRawNetworkPolicyReconciler struct {
	client client.Client
}

func NewKserveRawNetworkPolicyReconciler(client client.Client) *KserveRawNetworkPolicyReconciler {
	return &KserveRawNetworkPolicyReconciler{client: client}
}

func (r *KserveRawNetworkPolicyReconciler) Reconcile(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {
	services, err := r.listComponentServices(ctx, isvc)
	if err != nil {
		return err
	}

	metricsPorts, err := r.metricsPorts(ctx, log, isvc)
	if err != nil {
		return err
	}

	hasTransformer := isvc.Spec.Transformer != nil || len(services[transformerNetworkPolicyComponent]) > 0
	// KServe currently creates multi-node worker/head resources only for the
	// stable predictor. Keep the all-port peer scoped to that concrete resource
	// shape; a canary WorkerSpec is not materialized by the raw KServe builder.
	multiNode := isvc.Spec.Predictor.WorkerSpec != nil
	components := make([]string, 0, 2)
	components = append(components, predictorNetworkPolicyComponent)
	if hasTransformer {
		components = append(components, transformerNetworkPolicyComponent)
	}

	var reconcileErrors *multierror.Error
	for _, component := range components {
		desired, err := r.desiredNetworkPolicy(isvc, component, services, metricsPorts, multiNode)
		if err != nil {
			reconcileErrors = multierror.Append(reconcileErrors, err)
			continue
		}
		if err := r.reconcileNetworkPolicy(ctx, log, isvc, desired); err != nil {
			reconcileErrors = multierror.Append(reconcileErrors, err)
		}
	}
	// A transformer may have been removed. Delete its old policy after building
	// the desired set, while preserving a same-name policy owned by another
	// object.
	if !hasTransformer {
		if err := r.deleteOwnedNetworkPolicy(ctx, log, isvc, transformerNetworkPolicyComponent); err != nil {
			reconcileErrors = multierror.Append(reconcileErrors, err)
		}
	}

	return reconcileErrors.ErrorOrNil()
}

func (r *KserveRawNetworkPolicyReconciler) Delete(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {
	var deleteErrors *multierror.Error
	for _, component := range []string{predictorNetworkPolicyComponent, transformerNetworkPolicyComponent} {
		if err := r.deleteOwnedNetworkPolicy(ctx, log, isvc, component); err != nil {
			deleteErrors = multierror.Append(deleteErrors, err)
		}
	}
	return deleteErrors.ErrorOrNil()
}

func (r *KserveRawNetworkPolicyReconciler) Cleanup(ctx context.Context, log logr.Logger, namespace string) error {
	policies := &networkingv1.NetworkPolicyList{}
	if err := r.client.List(ctx, policies, client.InNamespace(namespace), client.MatchingLabels{
		managedByLabel: managedByValue,
	}); err != nil {
		return err
	}

	var cleanupErrors *multierror.Error
	for i := range policies.Items {
		policy := &policies.Items[i]
		if policy.Labels["app.kubernetes.io/component"] != rawNetworkPolicyManagedComponent {
			continue
		}
		if !hasInferenceServiceOwner(policy) {
			continue
		}
		log.V(1).Info("Deleting orphaned raw InferenceService NetworkPolicy", "name", policy.Name)
		if err := r.client.Delete(ctx, policy); err != nil && !apierrors.IsNotFound(err) {
			cleanupErrors = multierror.Append(cleanupErrors, err)
		}
	}
	return cleanupErrors.ErrorOrNil()
}

func (r *KserveRawNetworkPolicyReconciler) listComponentServices(ctx context.Context, isvc *kservev1beta1.InferenceService) (map[string][]corev1.Service, error) {
	serviceList := &corev1.ServiceList{}
	if err := r.client.List(ctx, serviceList,
		client.InNamespace(isvc.Namespace),
		client.MatchingLabels{constants.KserveGroupAnnotation: isvc.Name},
	); err != nil {
		return nil, fmt.Errorf("failed to list services for InferenceService %q: %w", isvc.Name, err)
	}

	services := map[string][]corev1.Service{
		predictorNetworkPolicyComponent:   {},
		transformerNetworkPolicyComponent: {},
	}
	for _, service := range serviceList.Items {
		if !isOwnedByInferenceServiceOwnerReferences(service.OwnerReferences, isvc) {
			// Labels alone are user-controlled. Only KServe Services owned by
			// this exact ISVC may contribute target ports or Route peers to the
			// policy, so a same-label Service cannot widen ingress. The existing
			// Route reconciler still resolves its backend by labels; that separate
			// pre-existing behavior is not used to broaden this policy.
			continue
		}
		component := service.Labels[kserveconstants.KServiceComponentLabel]
		if component != predictorNetworkPolicyComponent && component != transformerNetworkPolicyComponent {
			continue
		}
		services[component] = append(services[component], service)
	}
	for component := range services {
		sort.Slice(services[component], func(i, j int) bool {
			return services[component][i].Name < services[component][j].Name
		})
	}
	return services, nil
}

func (r *KserveRawNetworkPolicyReconciler) desiredNetworkPolicy(
	isvc *kservev1beta1.InferenceService,
	component string,
	services map[string][]corev1.Service,
	metricsPorts []networkingv1.NetworkPolicyPort,
	multiNode bool,
) (*networkingv1.NetworkPolicy, error) {
	policy := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rawNetworkPolicyName(isvc, component),
			Namespace: isvc.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/component":          rawNetworkPolicyManagedComponent,
				"app.kubernetes.io/part-of":            "kserve",
				managedByLabel:                         managedByValue,
				kserveconstants.KServiceComponentLabel: component,
				constants.KserveGroupAnnotation:        isvc.Name,
			},
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{
				constants.KserveGroupAnnotation:        isvc.Name,
				kserveconstants.KServiceComponentLabel: component,
			}},
			PolicyTypes: []networkingv1.PolicyType{
				networkingv1.PolicyTypeIngress,
				networkingv1.PolicyTypeEgress,
			},
			// Model storage and user-configured model endpoints may use changing
			// addresses. The platform decision for raw ISVCs is explicit allow-all
			// egress; ingress remains restricted below.
			Egress: []networkingv1.NetworkPolicyEgressRule{{}},
		},
	}

	if err := ctrl.SetControllerReference(isvc, policy, r.client.Scheme()); err != nil {
		return nil, fmt.Errorf("set NetworkPolicy owner reference for %s: %w", policy.Name, err)
	}

	authEnabled := rawAuthEnabled(isvc)
	componentServices := services[component]

	// OpenShift Router access is allowed only for the component selected by the
	// Route reconciler. A Route targets the transformer when one exists, and the
	// predictor otherwise. Do not infer Route access from Pod existence alone.
	// Canary Route alternate backends are predictor Services, even when the
	// primary Route target is a transformer. Permit the router on predictor
	// Pods in that case using the same actual Service target ports.
	if routeEnabled(isvc) && (routeComponent(services) == component ||
		(component == predictorNetworkPolicyComponent && len(isvc.Spec.Canary) > 0)) {
		routePorts, err := routeTargetPorts(componentServices, authEnabled)
		if err != nil {
			return nil, err
		}
		if len(routePorts) > 0 {
			policy.Spec.Ingress = append(policy.Spec.Ingress, networkingv1.NetworkPolicyIngressRule{
				From:  []networkingv1.NetworkPolicyPeer{routerPeer()},
				Ports: routePorts,
			})
		}
	}

	// The transformer calls the predictor Service directly. Use the predictor
	// Service target port, rather than Service.Port, because NetworkPolicy is
	// evaluated against the Pod port. This also covers stable and canary
	// predictor Services with one per-component policy.
	if component == predictorNetworkPolicyComponent && len(services[transformerNetworkPolicyComponent]) > 0 {
		predictorPorts, err := serviceTargetPorts(services[predictorNetworkPolicyComponent], authEnabled)
		if err != nil {
			return nil, err
		}
		if len(predictorPorts) > 0 {
			policy.Spec.Ingress = append(policy.Spec.Ingress, networkingv1.NetworkPolicyIngressRule{
				From: []networkingv1.NetworkPolicyPeer{{
					PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{
						constants.KserveGroupAnnotation:        isvc.Name,
						kserveconstants.KServiceComponentLabel: transformerNetworkPolicyComponent,
					}},
				}},
				Ports: predictorPorts,
			})
		}
	}
	// InferenceGraph and explainer callers are intentionally outside this raw
	// policy slice. Their named peer rules must be added with those workload
	// reconcilers rather than admitting all same-namespace Pods here.

	// Metrics are enabled by a ServingRuntime annotation. The metrics Service
	// selects the ISVC Pods, so the same monitoring rule is applied to each
	// component policy when metrics are configured.
	if len(metricsPorts) > 0 {
		policy.Spec.Ingress = append(policy.Spec.Ingress, networkingv1.NetworkPolicyIngressRule{
			From:  monitoringPeers(),
			Ports: metricsPorts,
		})
	}

	if component == predictorNetworkPolicyComponent && multiNode {
		// Multi-node head and worker Pods share the predictor labels. The
		// platform contract requires explicit ports, so represent the user-approved
		// all-port peer rule as bounded TCP, UDP, and SCTP ranges rather than
		// omitting Ports. Actual CNI enforcement and protocol availability,
		// especially SCTP, require cluster-level qualification.
		policy.Spec.Ingress = append(policy.Spec.Ingress, multiNodePredictorPeer(isvc))
	}

	return policy, nil
}

func (r *KserveRawNetworkPolicyReconciler) metricsPorts(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) ([]networkingv1.NetworkPolicyPort, error) {
	runtime, err := utils.FindSupportingRuntimeForISvc(ctx, r.client, log, isvc)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// The ServingRuntime may be created after the ISVC. Keep the
			// deny-ingress policy lifecycle independent from that race and
			// add the metrics rule on the next reconcile.
			return nil, nil
		}
		log.Error(err, "Ignoring metrics ingress configuration because ServingRuntime lookup failed")
		return nil, nil
	}
	if runtime == nil || runtime.Spec.Annotations == nil {
		return nil, nil
	}

	portValue := strings.TrimSpace(runtime.Spec.Annotations[kserveconstants.PrometheusPortAnnotationKey])
	if portValue == "" {
		return nil, nil
	}
	port, err := strconv.ParseInt(portValue, 10, 32)
	if err != nil || port < 1 || port > 65535 {
		if err == nil {
			err = fmt.Errorf("port must be between 1 and 65535")
		}
		log.Error(err, "Ignoring invalid Prometheus port annotation", "port", portValue, "servingRuntime", runtime.Name)
		// Metrics are optional. Keep the core policy (including its explicit
		// egress rule and any Route/component peers) reconciled when a runtime
		// annotation is malformed; the metrics rule will be restored after the
		// annotation is corrected.
		return nil, nil
	}

	// Prefer the actual metrics Service target port when the Service already
	// exists. During the first reconcile KServe/ODH may not have created it yet;
	// the ServingRuntime value is the same source used by the metrics Service
	// reconciler and lets us create a safe policy before that Service appears.
	metricsService := &corev1.Service{}
	err = r.client.Get(ctx, types.NamespacedName{Name: getMetricsServiceName(isvc), Namespace: isvc.Namespace}, metricsService)
	if err == nil {
		if isOwnedByInferenceServiceOwnerReferences(metricsService.OwnerReferences, isvc) {
			ports, portErr := networkPolicyPorts(metricsService.Spec.Ports)
			if portErr != nil {
				return nil, portErr
			}
			if len(ports) > 0 {
				return ports, nil
			}
		} else {
			log.Info("Ignoring unowned metrics Service when building NetworkPolicy", "name", metricsService.Name, "inferenceService", isvc.Name)
		}
	} else if !apierrors.IsNotFound(err) {
		// Metrics are optional. Fall back to the validated runtime port when
		// the metrics Service is temporarily unavailable instead of blocking
		// the baseline NetworkPolicy and Route lifecycle.
		log.Error(err, "Falling back to ServingRuntime metrics port because metrics Service lookup failed", "inferenceService", isvc.Name)
	}

	return networkPolicyPorts([]corev1.ServicePort{{
		Protocol:   corev1.ProtocolTCP,
		Port:       int32(port),
		TargetPort: intstr.FromInt32(int32(port)),
	}})
}

func (r *KserveRawNetworkPolicyReconciler) reconcileNetworkPolicy(
	ctx context.Context,
	log logr.Logger,
	isvc *kservev1beta1.InferenceService,
	desired *networkingv1.NetworkPolicy,
) error {
	key := client.ObjectKeyFromObject(desired)
	existing := &networkingv1.NetworkPolicy{}
	if err := r.client.Get(ctx, key, existing); err != nil {
		if apierrors.IsNotFound(err) {
			log.V(1).Info("Creating raw InferenceService NetworkPolicy", "name", desired.Name)
			return r.client.Create(ctx, desired)
		}
		return fmt.Errorf("failed to get NetworkPolicy %s: %w", key, err)
	}

	if !isOwnedByInferenceService(existing, isvc) {
		return fmt.Errorf("refusing to overwrite NetworkPolicy %s: it is not owned by InferenceService %s/%s", key, isvc.Namespace, isvc.Name)
	}
	if !existing.GetDeletionTimestamp().IsZero() {
		return fmt.Errorf("NetworkPolicy %s is still deleting", key)
	}

	if equality.Semantic.DeepEqual(existing.Spec, desired.Spec) &&
		equality.Semantic.DeepEqual(existing.Labels, desired.Labels) &&
		equality.Semantic.DeepEqual(existing.Annotations, desired.Annotations) &&
		equality.Semantic.DeepEqual(existing.OwnerReferences, desired.OwnerReferences) {
		return nil
	}

	updated := existing.DeepCopy()
	updated.Labels = desired.Labels
	updated.Annotations = desired.Annotations
	updated.OwnerReferences = desired.OwnerReferences
	updated.Spec = *desired.Spec.DeepCopy()
	log.V(1).Info("Updating raw InferenceService NetworkPolicy", "name", desired.Name)
	if err := r.client.Update(ctx, updated); err != nil {
		return fmt.Errorf("failed to update NetworkPolicy %s: %w", key, err)
	}
	return nil
}

func (r *KserveRawNetworkPolicyReconciler) deleteOwnedNetworkPolicy(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService, component string) error {
	key := types.NamespacedName{Name: rawNetworkPolicyName(isvc, component), Namespace: isvc.Namespace}
	policy := &networkingv1.NetworkPolicy{}
	if err := r.client.Get(ctx, key, policy); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to get NetworkPolicy %s for deletion: %w", key, err)
	}
	if !isOwnedByInferenceService(policy, isvc) {
		log.V(1).Info("Leaving unowned raw InferenceService NetworkPolicy in place", "name", key)
		return nil
	}
	if err := r.client.Delete(ctx, policy); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete NetworkPolicy %s: %w", key, err)
	}
	return nil
}

func rawNetworkPolicyName(isvc *kservev1beta1.InferenceService, component string) string {
	return knativekmeta.ChildName(isvc.Name, "-"+component+rawNetworkPolicyNameSuffix)
}

func isOwnedByInferenceService(policy *networkingv1.NetworkPolicy, isvc *kservev1beta1.InferenceService) bool {
	return isOwnedByInferenceServiceOwnerReferences(policy.OwnerReferences, isvc)
}

func isOwnedByInferenceServiceOwnerReferences(ownerReferences []metav1.OwnerReference, isvc *kservev1beta1.InferenceService) bool {
	for _, ref := range ownerReferences {
		if !isInferenceServiceOwnerReference(ref) || ref.Name != isvc.Name {
			continue
		}
		// Require an exact UID match so same-name resources cannot be taken over
		// after an ISVC replacement. Empty UIDs compare equal for unit fixtures.
		if ref.UID != isvc.UID {
			continue
		}
		return true
	}
	return false
}

func hasInferenceServiceOwner(policy *networkingv1.NetworkPolicy) bool {
	for _, ref := range policy.OwnerReferences {
		if isInferenceServiceOwnerReference(ref) && ref.Name != "" {
			return true
		}
	}
	return false
}

func isInferenceServiceOwnerReference(ref metav1.OwnerReference) bool {
	return ref.APIVersion == kservev1beta1.SchemeGroupVersion.String() &&
		ref.Kind == constants.InferenceServiceKind && ref.Controller != nil && *ref.Controller
}

func rawAuthEnabled(isvc *kservev1beta1.InferenceService) bool {
	enabled, err := strconv.ParseBool(strings.TrimSpace(isvc.Annotations[constants.EnableAuthODHAnnotation]))
	return err == nil && enabled
}

func routeEnabled(isvc *kservev1beta1.InferenceService) bool {
	return isvc.Labels[constants.KserveNetworkVisibility] == constants.LabelEnableKserveRawRoute
}

func routeComponent(services map[string][]corev1.Service) string {
	if len(services[transformerNetworkPolicyComponent]) > 0 {
		return transformerNetworkPolicyComponent
	}
	return predictorNetworkPolicyComponent
}

func routeTargetPorts(services []corev1.Service, authEnabled bool) ([]networkingv1.NetworkPolicyPort, error) {
	return serviceTargetPorts(services, authEnabled)
}

func serviceTargetPorts(services []corev1.Service, authEnabled bool) ([]networkingv1.NetworkPolicyPort, error) {
	ports := make([]corev1.ServicePort, 0, len(services))
	for i := range services {
		service := &services[i]
		// KServe's multi-node head and worker Services are labeled as
		// predictor Services but are not serving endpoints for Route or
		// transformer traffic. Their all-port peer rule is handled separately
		// by desiredNetworkPolicy.
		if len(service.Spec.Ports) == 0 || service.Labels[kserveconstants.MultiNodeRoleLabelKey] != "" {
			continue
		}
		selected, err := setRouteTargetPort(authEnabled, service)
		if err != nil {
			return nil, err
		}
		for _, servicePort := range service.Spec.Ports {
			if selected.Type == intstr.String && servicePort.Name != selected.StrVal {
				continue
			}
			if selected.Type == intstr.Int && servicePort.Port != selected.IntVal {
				continue
			}
			ports = append(ports, servicePort)
		}
	}
	return networkPolicyPorts(ports)
}

// networkPolicyPorts converts Service ports to Pod target ports, keeping the
// Service.Port versus TargetPort distinction explicit.
func networkPolicyPorts(servicePorts []corev1.ServicePort) ([]networkingv1.NetworkPolicyPort, error) {
	type portKey struct {
		protocol corev1.Protocol
		target   string
	}
	seen := map[portKey]struct{}{}
	ports := make([]networkingv1.NetworkPolicyPort, 0, len(servicePorts))
	for _, servicePort := range servicePorts {
		protocol := servicePort.Protocol
		if protocol == "" {
			protocol = corev1.ProtocolTCP
		}
		target := servicePort.TargetPort
		if (target.Type == intstr.Int && target.IntVal == 0) || (target.Type == intstr.String && target.StrVal == "") {
			target = intstr.FromInt32(servicePort.Port)
		}
		if (target.Type == intstr.Int && target.IntVal == 0) || (target.Type == intstr.String && target.StrVal == "") {
			continue
		}
		key := portKey{protocol: protocol, target: target.String()}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		ports = append(ports, networkingv1.NetworkPolicyPort{
			Protocol: ptr.To(protocol),
			Port:     ptr.To(target),
		})
	}
	sort.Slice(ports, func(i, j int) bool {
		left := string(*ports[i].Protocol) + ":" + ports[i].Port.String()
		right := string(*ports[j].Protocol) + ":" + ports[j].Port.String()
		return left < right
	})
	return ports, nil
}

func namespaceSelectorPeer(namespace string) networkingv1.NetworkPolicyPeer {
	return networkingv1.NetworkPolicyPeer{NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{
		corev1.LabelMetadataName: namespace,
	}}}
}

func routerPeer() networkingv1.NetworkPolicyPeer {
	// OpenShift's ingress namespace label is the supported peer identity for
	// router traffic, including host-network router deployments. The label is
	// preferred over the default openshift-ingress namespace-name selector
	// because router placement can vary by cluster configuration.
	return networkingv1.NetworkPolicyPeer{NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{
		openshiftIngressPolicyGroupLabel: "",
	}}}
}

func multiNodePredictorPeer(isvc *kservev1beta1.InferenceService) networkingv1.NetworkPolicyIngressRule {
	return networkingv1.NetworkPolicyIngressRule{
		From: []networkingv1.NetworkPolicyPeer{{
			PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{
				constants.KserveGroupAnnotation:        isvc.Name,
				kserveconstants.KServiceComponentLabel: predictorNetworkPolicyComponent,
			}},
		}},
		Ports: []networkingv1.NetworkPolicyPort{
			allPortsForProtocol(corev1.ProtocolTCP),
			allPortsForProtocol(corev1.ProtocolUDP),
			allPortsForProtocol(corev1.ProtocolSCTP),
		},
	}
}

func allPortsForProtocol(protocol corev1.Protocol) networkingv1.NetworkPolicyPort {
	return networkingv1.NetworkPolicyPort{
		Protocol: ptr.To(protocol),
		Port:     ptr.To(intstr.FromInt32(1)),
		EndPort:  ptr.To(int32(65535)),
	}
}

func monitoringNamespaces() []string {
	namespaces := []string{platformMonitoringNamespace, userWorkloadMonitoringNamespace, rhoaiMonitoringNamespace}
	if namespace := strings.TrimSpace(os.Getenv(monitoringNamespaceEnvVar)); namespace != "" {
		namespaces = append(namespaces, namespace)
	}
	seen := make(map[string]struct{}, len(namespaces))
	result := make([]string, 0, len(namespaces))
	for _, namespace := range namespaces {
		if _, ok := seen[namespace]; ok {
			continue
		}
		seen[namespace] = struct{}{}
		result = append(result, namespace)
	}
	return result
}

func monitoringPeers() []networkingv1.NetworkPolicyPeer {
	namespaces := monitoringNamespaces()
	peers := make([]networkingv1.NetworkPolicyPeer, 0, len(namespaces))
	for _, namespace := range namespaces {
		peers = append(peers, namespaceSelectorPeer(namespace))
	}
	return peers
}
