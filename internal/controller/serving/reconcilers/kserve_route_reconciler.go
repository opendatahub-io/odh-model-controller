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

	"github.com/go-logr/logr"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"github.com/kserve/kserve/pkg/constants"
	"github.com/kserve/kserve/pkg/utils"
	v1 "github.com/openshift/api/route/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
	"knative.dev/pkg/network"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/comparators"
	constants2 "github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/processors"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/resources"
	utils2 "github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
)

var _ SubResourceReconciler = (*KserveRouteReconciler)(nil)

type KserveRouteReconciler struct {
	client         client.Client
	kClient        kubernetes.Interface
	routeHandler   resources.RouteHandler
	deltaProcessor processors.DeltaProcessor
}

func NewKserveRouteReconciler(client client.Client, kClient kubernetes.Interface) *KserveRouteReconciler {
	return &KserveRouteReconciler{
		client:         client,
		kClient:        kClient,
		routeHandler:   resources.NewRouteHandler(client),
		deltaProcessor: processors.NewDeltaProcessor(),
	}
}

func (r *KserveRouteReconciler) Reconcile(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {
	log.V(1).Info("Reconciling Generic Route for Kserve InferenceService")

	// Create Desired resource
	desiredResource, err := r.createDesiredResource(ctx, isvc)
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

func (r *KserveRouteReconciler) Delete(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {
	log.V(1).Info("Deleting Kserve inference service generic route")
	_, meshNamespace := utils2.GetIstioControlPlaneName(ctx, r.client)
	return r.routeHandler.DeleteRoute(ctx, types.NamespacedName{Name: getKServeRouteName(isvc), Namespace: meshNamespace})
}

func (r *KserveRouteReconciler) Cleanup(_ context.Context, _ logr.Logger, _ string) error {
	// NOOP - resources are deleted together with ISVCs
	return nil
}

func (r *KserveRouteReconciler) createDesiredResource(ctx context.Context, isvc *kservev1beta1.InferenceService) (*v1.Route, error) {
	isvcConfigMap, err := kservev1beta1.GetInferenceServiceConfigMap(ctx, r.kClient)
	if err != nil {
		return nil, err
	}
	ingressConfig, err := kservev1beta1.NewIngressConfig(isvcConfigMap)
	if err != nil {
		return nil, err
	}

	disableIstioVirtualHost := ingressConfig.DisableIstioVirtualHost
	if !disableIstioVirtualHost {

		serviceHost := getServiceHost(isvc)
		if serviceHost == "" {
			return nil, fmt.Errorf("failed to load serviceHost from InferenceService status")
		}
		isInternal := false
		// if service is labelled with cluster local or knative domain is configured as internal
		if val, ok := isvc.Labels[constants.VisibilityLabel]; ok && val == constants.ClusterLocalVisibility {
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
			return !utils.Includes(constants.ServiceAnnotationDisallowedList, key)
		})

		urlScheme := ingressConfig.UrlScheme
		var targetPort intstr.IntOrString
		var tlsConfig *v1.TLSConfig
		if urlScheme == "http" {
			targetPort = intstr.FromString(constants2.IstioIngressServiceHTTPPortName)
		} else {
			targetPort = intstr.FromString(constants2.IstioIngressServiceHTTPSPortName)
			tlsConfig = &v1.TLSConfig{
				Termination:                   v1.TLSTerminationPassthrough,
				InsecureEdgeTerminationPolicy: v1.InsecureEdgeTerminationPolicyRedirect,
			}
		}

		_, meshNamespace := utils2.GetIstioControlPlaneName(context.Background(), r.client)

		route := &v1.Route{
			ObjectMeta: metav1.ObjectMeta{
				Name:        getKServeRouteName(isvc),
				Namespace:   meshNamespace,
				Annotations: annotations,
				Labels:      isvc.Labels,
			},
			Spec: v1.RouteSpec{
				Host: serviceHost,
				To: v1.RouteTargetReference{
					Kind:   "Service",
					Name:   constants2.IstioIngressService,
					Weight: ptr.To(int32(100)),
				},
				Port: &v1.RoutePort{
					TargetPort: targetPort,
				},
				TLS:            tlsConfig,
				WildcardPolicy: v1.WildcardPolicyNone,
			},
		}
		return route, nil
	}
	return nil, nil
}

func (r *KserveRouteReconciler) getExistingResource(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) (*v1.Route, error) {
	_, meshNamespace := utils2.GetIstioControlPlaneName(ctx, r.client)
	return r.routeHandler.FetchRoute(ctx, log, types.NamespacedName{Name: getKServeRouteName(isvc), Namespace: meshNamespace})
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
			return err
		}
	}
	if delta.IsUpdated() {
		log.V(1).Info("Delta found", "update", existingRoute.GetName())
		rp := existingRoute.DeepCopy()
		rp.Labels = desiredRoute.Labels
		rp.Annotations = desiredRoute.Annotations
		rp.Spec = desiredRoute.Spec

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

func getServiceHost(isvc *kservev1beta1.InferenceService) string {
	if isvc.Status.URL == nil {
		return ""
	}
	// Derive the ingress service host from underlying service url
	return isvc.Status.URL.Host
}

func getKServeRouteName(isvc *kservev1beta1.InferenceService) string {
	return isvc.Name + "-" + isvc.Namespace
}
