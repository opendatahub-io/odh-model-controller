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
	"reflect"

	predictorv1 "github.com/kserve/modelmesh-serving/apis/serving/v1alpha1"
	routev1 "github.com/openshift/api/route/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	modelmeshServiceName = "modelmesh-serving"
	modelmeshServicePort = 8008
)

// NewpredictorRoute defines the desired route object
func NewpredictorRoute(predictor *predictorv1.Predictor) *routev1.Route {
	return &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      predictor.Name,
			Namespace: predictor.Namespace,
			Labels: map[string]string{
				"predictor-name": predictor.Name,
			},
		},
		Spec: routev1.RouteSpec{
			To: routev1.RouteTargetReference{
				Kind:   "Service",
				Name:   modelmeshServiceName,
				Weight: pointer.Int32Ptr(100),
			},
			Port: &routev1.RoutePort{
				TargetPort: intstr.FromInt(modelmeshServicePort),
			},
			TLS: &routev1.TLSConfig{
				Termination:                   routev1.TLSTerminationEdge,
				InsecureEdgeTerminationPolicy: routev1.InsecureEdgeTerminationPolicyRedirect,
			},
			WildcardPolicy: routev1.WildcardPolicyNone,
			Path:           "/v2/models/" + predictor.Name,
		},
		Status: routev1.RouteStatus{
			Ingress: []routev1.RouteIngress{},
		},
	}
}

// ComparepredictorRoutes checks if two routes are equal, if not return false
func ComparePredictorRoutes(r1 routev1.Route, r2 routev1.Route) bool {
	// Omit the host field since it is reconciled by the ingress controller
	r1.Spec.Host, r2.Spec.Host = "", ""

	// Two routes will be equal if the labels and spec are identical
	return reflect.DeepEqual(r1.ObjectMeta.Labels, r2.ObjectMeta.Labels) &&
		reflect.DeepEqual(r1.Spec, r2.Spec)
}

// Reconcile will manage the creation, update and deletion of the route returned
// by the newRoute function
func (r *OpenshiftPredictorReconciler) reconcileRoute(predictor *predictorv1.Predictor,
	ctx context.Context, newRoute func(*predictorv1.Predictor) *routev1.Route) error {
	// Initialize logger format
	log := r.Log.WithValues("predictor", predictor.Name, "namespace", predictor.Namespace)

	// Generate the desired route
	desiredRoute := newRoute(predictor)

	// Create the route if it does not already exist
	foundRoute := &routev1.Route{}
	justCreated := false
	err := r.Get(ctx, types.NamespacedName{
		Name:      desiredRoute.Name,
		Namespace: predictor.Namespace,
	}, foundRoute)
	if err != nil {
		if apierrs.IsNotFound(err) {
			log.Info("Creating Route")
			// Add .metatada.ownerReferences to the route to be deleted by the
			// Kubernetes garbage collector if the predictor is deleted
			err = ctrl.SetControllerReference(predictor, desiredRoute, r.Scheme)
			if err != nil {
				log.Error(err, "Unable to add OwnerReference to the Route")
				return err
			}
			// Create the route in the Openshift cluster
			err = r.Create(ctx, desiredRoute)
			if err != nil && !apierrs.IsAlreadyExists(err) {
				log.Error(err, "Unable to create the Route")
				return err
			}
			justCreated = true
		} else {
			log.Error(err, "Unable to fetch the Route")
			return err
		}
	}

	// Reconcile the route spec if it has been manually modified
	if !justCreated && !ComparePredictorRoutes(*desiredRoute, *foundRoute) {
		log.Info("Reconciling Route")
		// Retry the update operation when the ingress controller eventually
		// updates the resource version field
		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			// Get the last route revision
			if err := r.Get(ctx, types.NamespacedName{
				Name:      desiredRoute.Name,
				Namespace: predictor.Namespace,
			}, foundRoute); err != nil {
				return err
			}
			// Reconcile labels and spec field
			foundRoute.Spec = desiredRoute.Spec
			foundRoute.ObjectMeta.Labels = desiredRoute.ObjectMeta.Labels
			return r.Update(ctx, foundRoute)
		})
		if err != nil {
			log.Error(err, "Unable to reconcile the Route")
			return err
		}
	}

	return nil
}

// ReconcileRoute will manage the creation, update and deletion of the
// TLS route when the predictor is reconciled
func (r *OpenshiftPredictorReconciler) ReconcileRoute(
	predictor *predictorv1.Predictor, ctx context.Context) error {
	return r.reconcileRoute(predictor, ctx, NewpredictorRoute)
}
