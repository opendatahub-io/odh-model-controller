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
	"sort"
	"strconv"

	"github.com/go-logr/logr"
	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	v1 "github.com/openshift/api/route/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
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
)

const (
	modelmeshServiceName     = "modelmesh-serving"
	modelmeshAuthServicePort = 8443
	modelmeshServicePort     = 8008
)

var _ SubResourceReconciler = (*ModelMeshRouteReconciler)(nil)

type ModelMeshRouteReconciler struct {
	NoResourceRemoval
	client         client.Client
	routeHandler   resources.RouteHandler
	deltaProcessor processors.DeltaProcessor
}

func NewModelMeshRouteReconciler(client client.Client) *ModelMeshRouteReconciler {
	return &ModelMeshRouteReconciler{
		client:         client,
		routeHandler:   resources.NewRouteHandler(client),
		deltaProcessor: processors.NewDeltaProcessor(),
	}
}

// Reconcile will manage the creation, update and deletion of the  TLS route when the predictor is reconciled.
func (r *ModelMeshRouteReconciler) Reconcile(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {
	log.V(1).Info("Reconciling Route for InferenceService")
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

func (r *ModelMeshRouteReconciler) createDesiredResource(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) (*v1.Route, error) {

	desiredServingRuntime, err := r.findSupportingRuntimeForISvc(ctx, log, isvc)
	if err != nil {
		return nil, err
	}

	enableAuth := false
	if enableAuth, err = strconv.ParseBool(desiredServingRuntime.Annotations[constants.LabelEnableAuth]); err != nil {
		enableAuth = false
	}
	createRoute := false
	if createRoute, err = strconv.ParseBool(desiredServingRuntime.Annotations[constants.LabelEnableRoute]); err != nil {
		createRoute = false
	}

	if !createRoute {
		log.Info("Serving runtime does not have '" + constants.LabelEnableRoute + "' annotation set to 'True'. Skipping route creation")
		return nil, nil
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
				Name:   modelmeshServiceName,
				Weight: ptr.To(int32(100)),
			},
			Port: &v1.RoutePort{
				TargetPort: intstr.FromInt(modelmeshServicePort),
			},
			WildcardPolicy: v1.WildcardPolicyNone,
			Path:           "/v2/models/" + isvc.Name,
		},
		Status: v1.RouteStatus{
			Ingress: []v1.RouteIngress{},
		},
	}

	if enableAuth {
		desiredRoute.Spec.Port = &v1.RoutePort{
			TargetPort: intstr.FromInt(modelmeshAuthServicePort),
		}
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

func (r *ModelMeshRouteReconciler) getExistingResource(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) (*v1.Route, error) {
	return r.routeHandler.FetchRoute(ctx, log, types.NamespacedName{Name: isvc.Name, Namespace: isvc.Namespace})
}

func (r *ModelMeshRouteReconciler) processDelta(ctx context.Context, log logr.Logger, desiredRoute *v1.Route, existingRoute *v1.Route) (err error) {
	comparator := comparators.GetMMRouteComparator()
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

func (r *ModelMeshRouteReconciler) findSupportingRuntimeForISvc(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) (*kservev1alpha1.ServingRuntime, error) {
	desiredServingRuntime := &kservev1alpha1.ServingRuntime{}

	if isvc.Spec.Predictor.Model.Runtime != nil {
		err := r.client.Get(ctx, types.NamespacedName{
			Name:      *isvc.Spec.Predictor.Model.Runtime,
			Namespace: isvc.Namespace,
		}, desiredServingRuntime)
		if err != nil {
			if apierrs.IsNotFound(err) {
				return nil, err
			}
		}
		return desiredServingRuntime, nil
	} else {
		runtimes := &kservev1alpha1.ServingRuntimeList{}
		err := r.client.List(ctx, runtimes, client.InNamespace(isvc.Namespace))
		if err != nil {
			return nil, err
		}

		// Sort by creation date, to be somewhat deterministic
		sort.Slice(runtimes.Items, func(i, j int) bool {
			// Sorting descending by creation time leads to picking the most recently created runtimes first
			if runtimes.Items[i].CreationTimestamp.Before(&runtimes.Items[j].CreationTimestamp) {
				return false
			}
			if runtimes.Items[i].CreationTimestamp.Equal(&runtimes.Items[j].CreationTimestamp) {
				// For Runtimes created at the same time, use alphabetical order.
				return runtimes.Items[i].Name < runtimes.Items[j].Name
			}
			return true
		})

		for _, runtime := range runtimes.Items {
			if runtime.Spec.Disabled != nil && *runtime.Spec.Disabled {
				continue
			}

			if runtime.Spec.MultiModel != nil && !*runtime.Spec.MultiModel {
				continue
			}

			for _, supportedFormat := range runtime.Spec.SupportedModelFormats {
				if supportedFormat.AutoSelect != nil && *supportedFormat.AutoSelect && supportedFormat.Name == isvc.Spec.Predictor.Model.ModelFormat.Name {
					desiredServingRuntime = &runtime
					log.Info("Automatic runtime selection for InferenceService", "runtime", desiredServingRuntime.Name)
					return desiredServingRuntime, nil
				}
			}
		}

		log.Info("No suitable Runtime available for InferenceService")
		return desiredServingRuntime, nil
	}
}
