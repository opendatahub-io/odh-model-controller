package reconcilers

import (
	"context"
	"fmt"
	"github.com/go-logr/logr"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"github.com/kserve/kserve/pkg/constants"
	"github.com/kserve/kserve/pkg/utils"
	"github.com/opendatahub-io/odh-model-controller/controllers/components"
	"github.com/opendatahub-io/odh-model-controller/controllers/processors"
	v1 "github.com/openshift/api/route/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"
	"knative.dev/pkg/network"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
)

const (
	IstioNamespace      = "istio-system"
	IstioIngressService = "istio-ingressgateway"
)

type kserveInferenceServiceRouteReconciler struct {
	client         client.Client
	scheme         *runtime.Scheme
	ctx            context.Context
	isvc           *kservev1beta1.InferenceService
	log            logr.Logger
	routeHandler   components.RouteHandler
	deltaProcessor processors.DeltaProcessor
}

func NewKserveInferenceServiceRouteReconciler(client client.Client, scheme *runtime.Scheme, ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) Reconciler {
	logger := log.WithValues("resource", "KserveInferenceServiceRoute")
	return &kserveInferenceServiceRouteReconciler{
		client:         client,
		scheme:         scheme,
		ctx:            ctx,
		isvc:           isvc,
		log:            logger,
		routeHandler:   components.NewRouteHandler(client, ctx, logger),
		deltaProcessor: processors.NewDeltaProcessor(),
	}
}

func (r *kserveInferenceServiceRouteReconciler) Reconcile() error {

	// Create Desired resource
	desiredResource, err := r.createDesiredResource()
	if err != nil {
		return err
	}

	// Get Existing resource
	existingResource, err := r.getExistingResource()
	if err != nil {
		return err
	}

	// Process Delta
	if err = r.processDelta(desiredResource, existingResource); err != nil {
		return err
	}
	return nil
}

func (r *kserveInferenceServiceRouteReconciler) createDesiredResource() (*v1.Route, error) {
	ingressConfig, err := kservev1beta1.NewIngressConfig(r.client)
	if err != nil {
		return nil, err
	}

	disableIstioVirtualHost := ingressConfig.DisableIstioVirtualHost
	if disableIstioVirtualHost == false {

		serviceHost := getServiceHost(r.isvc)
		isInternal := false
		//if service is labelled with cluster local or knative domain is configured as internal
		if val, ok := r.isvc.Labels[constants.VisibilityLabel]; ok && val == constants.ClusterLocalVisibility {
			isInternal = true
		}
		serviceInternalHostName := network.GetServiceHostname(r.isvc.Name, r.isvc.Namespace)
		if serviceHost == serviceInternalHostName {
			isInternal = true
		}
		if isInternal {
			return nil, nil
		}

		if ingressConfig.PathTemplate != "" {
			serviceHost = ingressConfig.IngressDomain
		}
		annotations := utils.Filter(r.isvc.Annotations, func(key string) bool {
			return !utils.Includes(constants.ServiceAnnotationDisallowedList, key)
		})
		route := &v1.Route{
			ObjectMeta: metav1.ObjectMeta{
				Name:        r.isvc.Name,
				Namespace:   IstioNamespace,
				Annotations: annotations,
				Labels:      r.isvc.Labels,
			},
			Spec: v1.RouteSpec{
				Host: serviceHost,
				To: v1.RouteTargetReference{
					Kind:   "Service",
					Name:   IstioIngressService,
					Weight: pointer.Int32(100),
				},
				Port: &v1.RoutePort{
					TargetPort: intstr.FromString("https"),
				},
				TLS: &v1.TLSConfig{
					Termination:                   v1.TLSTerminationPassthrough,
					InsecureEdgeTerminationPolicy: v1.InsecureEdgeTerminationPolicyRedirect,
				},
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

func (r *kserveInferenceServiceRouteReconciler) getExistingResource() (*v1.Route, error) {
	return r.routeHandler.FetchRoute(types.NamespacedName{Name: r.isvc.Name, Namespace: IstioNamespace})
}

func (r *kserveInferenceServiceRouteReconciler) processDelta(desiredRoute *v1.Route, existingRoute *v1.Route) (err error) {
	comparator := r.routeHandler.GetComparator()
	delta := r.deltaProcessor.ComputeDelta(comparator, desiredRoute, existingRoute)

	if !delta.HasChanges() {
		r.log.Info("No delta found")
		return nil
	}

	if delta.IsAdded() {
		r.log.Info("Will", "create", desiredRoute.GetName())
		if err = r.client.Create(r.ctx, desiredRoute); err != nil {
			return
		}
	}
	if delta.IsUpdated() {
		r.log.Info("Will", "update", existingRoute.GetName())
		rp := existingRoute.DeepCopy()
		rp.Labels = desiredRoute.Labels
		rp.Annotations = desiredRoute.Annotations
		rp.Spec = desiredRoute.Spec

		if err = r.client.Update(r.ctx, rp); err != nil {
			return
		}
	}
	if delta.IsRemoved() {
		r.log.Info("Will", "delete", existingRoute.GetName())
		if err = r.client.Delete(r.ctx, existingRoute); err != nil {
			return
		}
	}
	return nil
}

func getServiceHost(isvc *kservev1beta1.InferenceService) string {
	if isvc.Status.Components == nil {
		return ""
	}
	//Derive the ingress service host from underlying service url
	if isvc.Spec.Transformer != nil {
		if transformerStatus, ok := isvc.Status.Components[kservev1beta1.TransformerComponent]; !ok {
			return ""
		} else if transformerStatus.URL == nil {
			return ""
		} else {
			if strings.Contains(transformerStatus.URL.Host, "-default") {
				return strings.Replace(transformerStatus.URL.Host, fmt.Sprintf("-%s-default", string(constants.Transformer)), "",
					1)
			} else {
				return strings.Replace(transformerStatus.URL.Host, fmt.Sprintf("-%s", string(constants.Transformer)), "",
					1)
			}
		}
	}

	if predictorStatus, ok := isvc.Status.Components[kservev1beta1.PredictorComponent]; !ok {
		return ""
	} else if predictorStatus.URL == nil {
		return ""
	} else {
		if strings.Contains(predictorStatus.URL.Host, "-default") {
			return strings.Replace(predictorStatus.URL.Host, fmt.Sprintf("-%s-default", string(constants.Predictor)), "",
				1)
		} else {
			return strings.Replace(predictorStatus.URL.Host, fmt.Sprintf("-%s", string(constants.Predictor)), "",
				1)
		}
	}
}
