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
	kserveconstants "github.com/kserve/kserve/pkg/constants"
	"github.com/kserve/kserve/pkg/utils"
	"github.com/opendatahub-io/odh-model-controller/controllers/comparators"
	"github.com/opendatahub-io/odh-model-controller/controllers/constants"
	"github.com/opendatahub-io/odh-model-controller/controllers/processors"
	"github.com/opendatahub-io/odh-model-controller/controllers/resources"
	v1 "github.com/openshift/api/route/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"
	"knative.dev/pkg/network"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ SubResourceReconciler = (*KserveRouteReconciler)(nil)

type KserveRouteReconciler struct {
	client         client.Client
	routeHandler   resources.RouteHandler
	deltaProcessor processors.DeltaProcessor
}

func NewKserveRouteReconciler(client client.Client) *KserveRouteReconciler {
	return &KserveRouteReconciler{
		client:         client,
		routeHandler:   resources.NewRouteHandler(client),
		deltaProcessor: processors.NewDeltaProcessor(),
	}
}

func (r *KserveRouteReconciler) Reconcile(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {
	log.V(1).Info("Reconciling Generic Route for Kserve InferenceService")

	//migrate annotation on isvcs created before "opt-in" routes for kserve were enabled. See: https://issues.redhat.com/browse/RHOAIENG-5222
	needsMigration, err := r.DoesIsvcNeedAnnotationMigration(ctx, log, isvc)
	if err != nil {
		return err
	}
	if needsMigration {
		isvc1, err := r.MigrateRouteAnnotationForExistingIsvcs(ctx, log, isvc)
		if err != nil {
			return err
		}
		isvc = isvc1
	}

	// Create Desired resource
	desiredResource, err := r.createDesiredResource(isvc, log)
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

func (r *KserveRouteReconciler) DoesIsvcNeedAnnotationMigration(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) (bool, error) {

	existingRoute, err := r.routeHandler.FetchRoute(ctx, log, types.NamespacedName{Name: getKServeRouteName(isvc), Namespace: constants.IstioNamespace})
	if err != nil {
		return false, err
	}
	if existingRoute != nil {
		return true, nil
	}

	return false, nil
}

func (r *KserveRouteReconciler) MigrateRouteAnnotationForExistingIsvcs(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) (*kservev1beta1.InferenceService, error) {

	log.Info("Adding annotation '" + constants.AnnotationEnableRoute + "' to Inferenceservice because an existing route was found.")
	if isvc.Annotations == nil {
		isvc.Annotations = map[string]string{}
	}
	isvc.Annotations[constants.AnnotationEnableRoute] = "true"
	if err := r.client.Update(ctx, isvc); err != nil {
		return nil, err
	}

	return isvc, nil
}

func (r *KserveRouteReconciler) Delete(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {
	log.V(1).Info("Deleting Kserve inference service generic route")
	return r.routeHandler.DeleteRoute(ctx, types.NamespacedName{Name: getKServeRouteName(isvc), Namespace: constants.IstioNamespace})
}

func (r *KserveRouteReconciler) Cleanup(_ context.Context, _ logr.Logger, _ string) error {
	// NOOP - resources are deleted together with ISVCs
	return nil
}

func (r *KserveRouteReconciler) createDesiredResource(isvc *kservev1beta1.InferenceService, log logr.Logger) (*v1.Route, error) {

	createRoute, err := strconv.ParseBool(isvc.Annotations[constants.AnnotationEnableRoute])
	if err != nil {
		createRoute = false
	}
	enableAuth, err := strconv.ParseBool(isvc.Annotations[constants.AnnotationEnableAuth])
	if err != nil {
		enableAuth = false
	}

	if !createRoute {
		log.Info("InferenceService does not have '" + constants.AnnotationEnableRoute + "' annotation set to 'True'. Skipping route creation")
		return nil, nil
	}
	if !enableAuth {
		log.Info("InferenceService does not have '" + constants.AnnotationEnableAuth + "' annotation set to 'True'. The created route is not secured.")
	}

	ingressConfig, err := kservev1beta1.NewIngressConfig(r.client)
	if err != nil {
		return nil, err
	}

	disableIstioVirtualHost := ingressConfig.DisableIstioVirtualHost
	if disableIstioVirtualHost == false {

		serviceHost := getServiceHost(isvc)
		if serviceHost == "" {
			return nil, fmt.Errorf("failed to load serviceHost from InferenceService status")
		}
		isInternal := false
		//if service is labelled with cluster local or knative domain is configured as internal
		if val, ok := isvc.Labels[kserveconstants.VisibilityLabel]; ok && val == kserveconstants.ClusterLocalVisibility {
			isInternal = true
		}
		serviceInternalHostName := network.GetServiceHostname(isvc.Name, isvc.Namespace)
		if serviceHost == serviceInternalHostName {
			isInternal = true
		}
		if isInternal {
			return nil, nil
		}

		if ingressConfig.PathTemplate != "" {
			serviceHost = ingressConfig.IngressDomain
		}
		annotations := utils.Filter(isvc.Annotations, func(key string) bool {
			return !utils.Includes(kserveconstants.ServiceAnnotationDisallowedList, key)
		})

		urlScheme := ingressConfig.UrlScheme
		var targetPort intstr.IntOrString
		var tlsConfig *v1.TLSConfig
		if urlScheme == "http" {
			targetPort = intstr.FromString(constants.IstioIngressServiceHTTPPortName)
		} else {
			targetPort = intstr.FromString(constants.IstioIngressServiceHTTPSPortName)
			tlsConfig = &v1.TLSConfig{
				Termination:                   v1.TLSTerminationPassthrough,
				InsecureEdgeTerminationPolicy: v1.InsecureEdgeTerminationPolicyRedirect,
			}
		}

		route := &v1.Route{
			ObjectMeta: metav1.ObjectMeta{
				Name:        getKServeRouteName(isvc),
				Namespace:   constants.IstioNamespace,
				Annotations: annotations,
				Labels:      isvc.Labels,
			},
			Spec: v1.RouteSpec{
				Host: serviceHost,
				To: v1.RouteTargetReference{
					Kind:   "Service",
					Name:   constants.IstioIngressService,
					Weight: pointer.Int32(100),
				},
				Port: &v1.RoutePort{
					TargetPort: targetPort,
				},
				TLS:            tlsConfig,
				WildcardPolicy: v1.WildcardPolicyNone,
			},
			Status: v1.RouteStatus{
				Ingress: []v1.RouteIngress{},
			},
		}
		return route, nil
	}
	return nil, nil
}

func (r *KserveRouteReconciler) getExistingResource(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) (*v1.Route, error) {
	return r.routeHandler.FetchRoute(ctx, log, types.NamespacedName{Name: getKServeRouteName(isvc), Namespace: constants.IstioNamespace})
}

func (r *KserveRouteReconciler) processDelta(ctx context.Context, log logr.Logger, desiredRoute *v1.Route, existingRoute *v1.Route) (err error) {
	comparator := comparators.GetKServeRouteComparator()
	delta := r.deltaProcessor.ComputeDelta(comparator, desiredRoute, existingRoute)

	if !delta.HasChanges() {
		log.V(1).Info("No delta found")
		return nil
	}

	if delta.IsAdded() {
		log.V(1).Info("Delta found", "create", desiredRoute.GetName())
		if err = r.client.Create(ctx, desiredRoute); err != nil {
			return
		}
	}
	if delta.IsUpdated() {
		log.V(1).Info("Delta found", "update", existingRoute.GetName())
		rp := existingRoute.DeepCopy()
		rp.Labels = desiredRoute.Labels
		rp.Annotations = desiredRoute.Annotations
		rp.Spec = desiredRoute.Spec

		if err = r.client.Update(ctx, rp); err != nil {
			return
		}
	}
	if delta.IsRemoved() {
		log.V(1).Info("Delta found", "delete", existingRoute.GetName())
		if err = r.client.Delete(ctx, existingRoute); err != nil {
			return
		}
	}
	return nil
}

func getServiceHost(isvc *kservev1beta1.InferenceService) string {
	if isvc.Status.URL == nil {
		return ""
	}
	//Derive the ingress service host from underlying service url
	return isvc.Status.URL.Host
}

func getKServeRouteName(isvc *kservev1beta1.InferenceService) string {
	return isvc.Name + "-" + isvc.Namespace
}
