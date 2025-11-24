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
	"strconv"

	"github.com/go-logr/logr"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	v1 "github.com/openshift/api/route/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/comparators"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/processors"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/resources"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
)

var _ SubResourceReconciler = (*KserveRawRouteReconciler)(nil)

type KserveRawRouteReconciler struct {
	client         client.Client
	routeHandler   resources.RouteHandler
	deltaProcessor processors.DeltaProcessor
}

func NewKserveRawRouteReconciler(client client.Client) *KserveRawRouteReconciler {
	return &KserveRawRouteReconciler{
		client:         client,
		routeHandler:   resources.NewRouteHandler(client),
		deltaProcessor: processors.NewDeltaProcessor(),
	}
}

func (r *KserveRawRouteReconciler) Reconcile(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {
	log.V(1).Info("Reconciling Generic Route for Kserve Raw InferenceService")

	// Create Desired resource
	desiredResource, err := r.createDesiredResource(ctx, log, isvc)
	if err != nil {
		return err
	}

	// Get Existing resource
	existingResource, err := r.getExistingResource(ctx, log, isvc)
	if err != nil {
		return err
	}

	// Process Delta
	if err = r.processDelta(ctx, log, desiredResource, existingResource); err != nil {
		return err
	}
	return nil
}

func (r *KserveRawRouteReconciler) Delete(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {
	// No OP as route will be deleted by the ownerref
	return nil
}

func (r *KserveRawRouteReconciler) Cleanup(_ context.Context, _ logr.Logger, _ string) error {
	// NOOP - resources are deleted together with ISVCs
	return nil
}

func (r *KserveRawRouteReconciler) createDesiredResource(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) (*v1.Route, error) {
	var err error
	enableSSL := false
	if enableSSL, err = strconv.ParseBool(isvc.Annotations[constants.EnableAuthODHAnnotation]); err != nil {
		enableSSL = false
	}
	createRoute := false
	if val, ok := isvc.Labels[constants.KserveNetworkVisibility]; ok && val == constants.LabelEnableKserveRawRoute {
		createRoute = true
	}
	if !createRoute {
		log.Info("InferenceService does not have label '" + constants.KserveNetworkVisibility + "' annotation" +
			" set to '" + constants.LabelEnableKserveRawRoute + "'. Skipping route creation")
		return nil, nil
	}

	// Fetch the service with the label "serving.kserve.io/inferenceservice=isvc.Name" in the isvc namespace
	serviceList := &corev1.ServiceList{}
	labelSelector := client.MatchingLabels{constants.KserveGroupAnnotation: isvc.Name}
	err = r.client.List(ctx, serviceList, client.InNamespace(isvc.Namespace), labelSelector)
	if err != nil || len(serviceList.Items) == 0 {
		log.Error(err, "Failed to fetch service for InferenceService", "InferenceService", isvc.Name)
		return nil, err
	}
	var targetService corev1.Service
	var predictorService, transformerService *corev1.Service
	predictorName := isvc.Name + "-predictor"
	for i := range serviceList.Items {
		svc := &serviceList.Items[i]
		if val, ok := svc.Labels["component"]; ok && val == "transformer" {
			transformerService = svc
			continue
		}
		if svc.Name == predictorName {
			predictorService = svc
		} else if val, ok := svc.Labels["component"]; ok && val == "predictor" && predictorService == nil {
			predictorService = svc
		}
	}
	switch {
	case transformerService != nil:
		targetService = *transformerService
	case predictorService != nil:
		targetService = *predictorService
	default:
		return nil, fmt.Errorf("no predictor or transformer Service found for InferenceService %q", isvc.Name)
	}

	targetPort, err := setRouteTargetPort(enableSSL, &targetService)
	if err != nil {
		return nil, err
	}

	desiredRoute := &v1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      isvc.Name,
			Namespace: isvc.Namespace,
			Labels: map[string]string{
				"inferenceservice-name": isvc.Name,
			},
		},
		Spec: v1.RouteSpec{
			To: v1.RouteTargetReference{
				Kind:   "Service",
				Name:   targetService.Name,
				Weight: ptr.To(int32(100)),
			},
			Port: &v1.RoutePort{
				TargetPort: targetPort,
			},
			WildcardPolicy: v1.WildcardPolicyNone,
		},
		Status: v1.RouteStatus{
			Ingress: []v1.RouteIngress{},
		},
	}

	// Set route timeout
	utils.SetOpenshiftRouteTimeoutForIsvc(desiredRoute, isvc)

	if enableSSL {
		desiredRoute.Spec.TLS = &v1.TLSConfig{
			Termination:                   v1.TLSTerminationReencrypt,
			InsecureEdgeTerminationPolicy: v1.InsecureEdgeTerminationPolicyRedirect,
		}
	} else {
		desiredRoute.Spec.TLS = &v1.TLSConfig{
			Termination:                   v1.TLSTerminationEdge,
			InsecureEdgeTerminationPolicy: v1.InsecureEdgeTerminationPolicyRedirect,
		}
	}
	if err = ctrl.SetControllerReference(isvc, desiredRoute, r.client.Scheme()); err != nil {
		return nil, err
	}

	return desiredRoute, nil
}

func (r *KserveRawRouteReconciler) getExistingResource(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) (*v1.Route, error) {
	return r.routeHandler.FetchRoute(ctx, log, types.NamespacedName{Name: isvc.Name, Namespace: isvc.Namespace})
}

func checkRouteTargetPort(ctx context.Context, c client.Client, route *v1.Route) error {
	if route.Spec.Port != nil {
		targetPort := route.Spec.Port.TargetPort
		if (targetPort.Type == intstr.String && targetPort.StrVal != "") || (targetPort.Type == intstr.Int && targetPort.IntVal != 0) {
			return nil
		}
	}

	var svc corev1.Service
	if err := c.Get(ctx, types.NamespacedName{Namespace: route.Namespace, Name: route.Spec.To.Name}, &svc); err != nil {
		return err
	}
	enableSSL := route.Spec.TLS != nil && route.Spec.TLS.Termination == v1.TLSTerminationReencrypt
	targetPort, err := setRouteTargetPort(enableSSL, &svc)
	if err != nil {
		return err
	}
	if route.Spec.Port == nil {
		route.Spec.Port = &v1.RoutePort{}
	}
	route.Spec.Port.TargetPort = targetPort
	return nil
}

func setRouteTargetPort(enableSSL bool, svc *corev1.Service) (intstr.IntOrString, error) {
	if enableSSL {
		for _, port := range svc.Spec.Ports {
			if port.Name == "https" {
				return intstr.FromString(port.Name), nil
			}
		}
	} else {
		for _, port := range svc.Spec.Ports {
			if port.Name == "http" {
				return intstr.FromString(port.Name), nil
			}
		}
	}

	for _, port := range svc.Spec.Ports {
		if port.Name != "" {
			return intstr.FromString(port.Name), nil
		}
	}

	if len(svc.Spec.Ports) > 0 {
		return intstr.FromInt32(svc.Spec.Ports[0].Port), nil

	}
	return intstr.IntOrString{}, fmt.Errorf("service %q has no ports defined", svc.Name)
}

func (r *KserveRawRouteReconciler) processDelta(ctx context.Context, log logr.Logger, desiredRoute *v1.Route, existingRoute *v1.Route) (err error) {
	comparator := comparators.GetKServeRouteComparator()
	delta := r.deltaProcessor.ComputeDelta(comparator, desiredRoute, existingRoute)

	if !delta.HasChanges() {
		log.V(1).Info("No delta found")
		return nil
	}

	if delta.IsAdded() {
		log.V(1).Info("Delta found", "create", desiredRoute.GetName())
		if err = checkRouteTargetPort(ctx, r.client, desiredRoute); err != nil {
			return err
		}
		if err = r.client.Create(ctx, desiredRoute); err != nil {
			return err
		}
	}
	if delta.IsUpdated() {
		log.V(1).Info("Delta found", "update", existingRoute.GetName())
		rp := existingRoute.DeepCopy()
		rp.Labels = desiredRoute.Labels
		rp.Annotations = desiredRoute.Annotations
		rp.Spec = desiredRoute.Spec

		if err = checkRouteTargetPort(ctx, r.client, rp); err != nil {
			return err
		}
		if err = r.client.Update(ctx, rp); err != nil {
			return err
		}
	}
	if delta.IsRemoved() {
		log.V(1).Info("Delta found", "delete", existingRoute.GetName())
		if err = r.client.Delete(ctx, existingRoute); err != nil {
			return err
		}
	}
	return nil
}
