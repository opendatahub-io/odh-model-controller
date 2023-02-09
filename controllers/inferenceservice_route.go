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
	predictorv1 "github.com/kserve/modelmesh-serving/apis/serving/v1alpha1"
	inferenceservicev1 "github.com/kserve/modelmesh-serving/apis/serving/v1beta1"
	"reflect"

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
	modelmeshServiceName     = "modelmesh-serving"
	modelmeshAuthServicePort = 8443
	modelmeshServicePort     = 8008
	modelmeshGrpcPort        = 8033
)

type RouteOptions int8
const (
	DEFAULT        RouteOptions = iota
	ENABLE_AUTH
	ENABLE_GRPC
)

// NewInferenceServiceRoute defines the desired route object
func NewInferenceServiceRoute(inferenceservice *inferenceservicev1.InferenceService, routeOptions RouteOptions) *routev1.Route {

	finalRoute := &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      inferenceservice.Name,
			Namespace: inferenceservice.Namespace,
			Labels: map[string]string{
				"inferenceservice-name": inferenceservice.Name,
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
			WildcardPolicy: routev1.WildcardPolicyNone,
			Path:           "/v2/models/" + inferenceservice.Name,
		},
		Status: routev1.RouteStatus{
			Ingress: []routev1.RouteIngress{},
		},
	}

	if routeOptions.DEFAULT {
		finalRoute.Spec.TLS = &routev1.TLSConfig{
			Termination:                   routev1.TLSTerminationEdge,
			InsecureEdgeTerminationPolicy: routev1.InsecureEdgeTerminationPolicyRedirect,
		}
	} else {
		if routeOptions.ENABLE_AUTH {
			finalRoute.Spec.Port = &routev1.RoutePort{
				TargetPort: intstr.FromInt(modelmeshAuthServicePort),
			}
			finalRoute.Spec.TLS = &routev1.TLSConfig{
				Termination:                   routev1.TLSTerminationReencrypt,
				InsecureEdgeTerminationPolicy: routev1.InsecureEdgeTerminationPolicyRedirect,
			}
		} else if routeOptions.ENABLE_GRPC {
			finalRoute.Spec.Port = &routev1.RoutePort{
				TargetPort: intstr.FromInt(modelmeshGrpcPort),
			}
			// INFO: Does gRPC need to enable TLS? 
			finalRoute.Spec.TLS = &routev1.TLSConfig{
				Termination:                   routev1.TLSTerminationReencrypt,
				InsecureEdgeTerminationPolicy: routev1.InsecureEdgeTerminationPolicyRedirect,
			}
		}
	}

	return finalRoute
}

// CompareInferenceServiceRoutes checks if two routes are equal, if not return false
func CompareInferenceServiceRoutes(r1 routev1.Route, r2 routev1.Route) bool {
	// Omit the host field since it is reconciled by the ingress controller
	r1.Spec.Host, r2.Spec.Host = "", ""

	// Two routes will be equal if the labels and spec are identical
	return reflect.DeepEqual(r1.ObjectMeta.Labels, r2.ObjectMeta.Labels) &&
		reflect.DeepEqual(r1.Spec, r2.Spec)
}

// Find existing route or create a new one
func FindOrCreateRoute(
	desiredRoute *routev1.Route,
	ctx context.Context,
	inferenceservice *inferenceservicev1.InferenceService,
	reconciler *OpenshiftInferenceServiceReconciler
) (bool, error) {
	justCreated := false
	if apierrs.IsNotFound(err) {
		// FIXME: Add logger back at some point
		// --
		// log.Info("Creating Route")

		// Add .metatada.ownerReferences to the route to be deleted by the
		// Kubernetes garbage collector if the predictor is deleted
		err = ctrl.SetControllerReference(inferenceservice, desiredRoute, reconciler.Scheme)
		if err != nil {
			// FIXME
			// log.Error(err, "Unable to add OwnerReference to the Route")

			return false, err
		}

		// Create the route in the Openshift cluster
		err = reconciler.Create(ctx, desiredRoute)
		if err != nil && !apierrs.IsAlreadyExists(err) {
			// FIXME
			// log.Error(err, "Unable to create the Route")

			return false, err
		}
		justCreated = true
	} else {
		// FIXME
		// log.Error(err, "Unable to fetch the Route")

		return false, err
	}

	return justCreated, nil
}

// Reconcile a route if it's been manually modified
func UpdateRouteIfModified(
	desiredRoute *routev1.Route,
	ctx context.Context,
	inferenceservice *inferenceservicev1.InferenceService,
	reconciler *OpenshiftInferenceServiceReconciler
) error {
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Get the last route revision
		if err := r.Get(ctx, types.NamespacedName{
			Name:      desiredRoute.Name,
			Namespace: inferenceservice.Namespace,
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
	return nil
}

// Reconcile will manage the creation, update and deletion of the route returned
// by the newRoute function
func (r *OpenshiftInferenceServiceReconciler) reconcileRoute(
	inferenceservice *inferenceservicev1.InferenceService,
	ctx context.Context,
	newRoute func(service *inferenceservicev1.InferenceService, routeOptions RouteOptions) *routev1.Route
) error {
	// Initialize logger format
	log := r.Log.WithValues("inferenceservice", inferenceservice.Name, "namespace", inferenceservice.Namespace)
	
	desiredServingRuntime := &predictorv1.ServingRuntime{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      *inferenceservice.Spec.Predictor.Model.Runtime,
		Namespace: inferenceservice.Namespace,
	}, desiredServingRuntime)
	if err != nil {
		if apierrs.IsNotFound(err) {
			log.Info("Serving Runtime ", *inferenceservice.Spec.Predictor.Model.Runtime, " desired by ", inferenceservice.Name, "was not found in namespace")
		}
	}

	enableAuth := true
	if desiredServingRuntime.Annotations["enable-auth"] != "true" {
		enableAuth = false
	}
	enableGrpc := true
	if desiredServingRuntime.Annotations["enable-grpc"] != "true" {
		enableGrpc = false
	}
	createRoute := true
	if desiredServingRuntime.Annotations["enable-route"] != "true" {
		createRoute = false
	}

	// Generate the desired route
	if enableAuth {
		desiredRoute := newRoute(inferenceservice, RouteOptions.ENABLE_AUTH)
	} else {
		desiredRoute := newRoute(inferenceservice, RouteOptions.DEFAULT)
	}

	// Create the default route (possible with auth enable) if it does not already exist
	foundRoute := &routev1.Route{}
	justCreated := false
	err = r.Get(ctx, types.NamespacedName{
		Name:      desiredRoute.Name,
		Namespace: inferenceservice.Namespace,
	}, foundRoute)
	if err != nil {
		if !createRoute {
			log.Info("Serving runtime does not have 'enable-route' annotation set to 'True'. Skipping route creation")
			return nil
		}

		justCreated, err := FindOrCreateRoute(desiredRoute, ctx, inferenceservice, r)
		if err != nil {
			log.Error(err, "Unable to find/create the route")
			return err
		}
	}

	if !createRoute {
		log.Info("Serving Runtime does not have 'enable-route' annotation set to 'True'. Deleting existing route")
		return r.Delete(ctx, foundRoute)
	}
	// Reconcile the route spec if it has been manually modified
	if !justCreated && !CompareInferenceServiceRoutes(*desiredRoute, *foundRoute) {
		log.Info("Reconciling Route")
		// Retry the update operation when the ingress controller eventually
		// updates the resource version field
		err := UpdateRouteIfModified(desiredRoute, ctx, inferenceservice, r)
		if err != nil {
			log.Error(err, "Unable to reconcile the Route")
			return err
		}
	}

	if enableGrpc {
		desiredRouteWithGrpc := newRoute(inferenceservice, RouteOptions.ENABLE_GRPC)

		// Create the gRPC-enabled route if it does not already exist
		foundGrpcRoute := &routev1.Route{}
		justCreated := false
		err = r.Get(ctx, types.NamespacedName{
			Name:      desiredRouteWithGrpc.Name,
			Namespace: inferenceservice.Namespace,
		}, foundGrpcRoute)
		if err != nil {
			if !createRoute {
				log.Info("Serving runtime does not have 'enable-route' annotation set to 'True'. Skipping route creation")
				return nil
			}

			justCreated, err := FindOrCreateRoute(desiredRouteWithGrpc, ctx, inferenceservice, r)
			if err != nil {
				log.Error(err, "Unable to find/create the route")
				return err
			}
		}

		if !createRoute {
			log.Info("Serving Runtime does not have 'enable-route' annotation set to 'True'. Deleting existing route")
			return r.Delete(ctx, foundGrpcRoute)
		}
		// Reconcile the route spec if it has been manually modified
		if !justCreated && !CompareInferenceServiceRoutes(*desiredRouteWithGrpc, *foundGrpcRoute) {
			log.Info("Reconciling Route")
			// Retry the update operation when the ingress controller eventually
			// updates the resource version field
			err := UpdateRouteIfModified(desiredRouteWithGrpc, ctx, inferenceservice, r)
			if err != nil {
				log.Error(err, "Unable to reconcile the Route")
				return err
			}
		}
	}

	return nil
}

// ReconcileRoute will manage the creation, update and deletion of the
// TLS route when the predictor is reconciled
func (r *OpenshiftInferenceServiceReconciler) ReconcileRoute(
	inferenceservice *inferenceservicev1.InferenceService, ctx context.Context) error {
	return r.reconcileRoute(inferenceservice, ctx, NewInferenceServiceRoute)
}
