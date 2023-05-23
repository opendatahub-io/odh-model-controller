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

	"github.com/go-logr/logr"
	mmv1alpha1 "github.com/kserve/modelmesh-serving/apis/serving/v1alpha1"
	routev1 "github.com/openshift/api/route/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ServingRuntimeRouteReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	Log          logr.Logger
	MeshDisabled bool
}

// ClusterRole permissions

// +kubebuilder:rbac:groups=serving.kserve.io,resources=inferenceservices,verbs=get;list;watch
// +kubebuilder:rbac:groups=serving.kserve.io,resources=inferenceservices/finalizers,verbs=get;list;watch;update
// +kubebuilder:rbac:groups=serving.kserve.io,resources=servingruntimes,verbs=get;list;watch;create;update
// +kubebuilder:rbac:groups=serving.kserve.io,resources=servingruntimes/finalizers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.istio.io,resources=virtualservices,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.istio.io,resources=virtualservices/finalizers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=maistra.io,resources=servicemeshmembers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=maistra.io,resources=servicemeshmembers/finalizers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=maistra.io,resources=servicemeshcontrolplanes,verbs=get;list;watch;create;update;patch;use
// +kubebuilder:rbac:groups=route.openshift.io,resources=routes,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterrolebindings,verbs=get;list;watch;create;update;patch;watch;delete
// +kubebuilder:rbac:groups="",resources=configmaps;namespaces;pods;services;serviceaccounts;secrets,verbs=get;list;watch;create;update;patch

func newRoute(servingruntime *mmv1alpha1.ServingRuntime, enableAuth bool) *routev1.Route {

	finalRoute := &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      servingruntime.Namespace + "-model-route",
			Namespace: servingruntime.Namespace,
			Labels: map[string]string{
				"servingruntime-name": servingruntime.Name,
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
		},
		Status: routev1.RouteStatus{
			Ingress: []routev1.RouteIngress{},
		},
	}

	if enableAuth {
		finalRoute.Spec.Port = &routev1.RoutePort{
			TargetPort: intstr.FromInt(modelmeshAuthServicePort),
		}
		finalRoute.Spec.TLS = &routev1.TLSConfig{
			Termination:                   routev1.TLSTerminationReencrypt,
			InsecureEdgeTerminationPolicy: routev1.InsecureEdgeTerminationPolicyRedirect,
		}
	} else {
		finalRoute.Spec.TLS = &routev1.TLSConfig{
			Termination:                   routev1.TLSTerminationEdge,
			InsecureEdgeTerminationPolicy: routev1.InsecureEdgeTerminationPolicyRedirect,
		}
	}

	return finalRoute
}

func (r *ServingRuntimeRouteReconciler) reconcileRoute(servingruntime *mmv1alpha1.ServingRuntime, ctx context.Context) error {

	// Initialize logger format
	log := r.Log.WithValues("servingruntime", servingruntime.Name, "namespace", servingruntime.Namespace)

	enableAuth := true
	if servingruntime.Annotations["enable-auth"] != "true" {
		enableAuth = false
	}
	createRoute := true
	if servingruntime.Annotations["enable-route"] != "true" {
		createRoute = false
	}

	// Generate the desired route
	desiredRoute := newRoute(servingruntime, enableAuth)

	// Create the route if it does not already exist
	foundRoute := &routev1.Route{}
	justCreated := false
	err := r.Get(ctx, types.NamespacedName{
		Name:      desiredRoute.Name,
		Namespace: servingruntime.Namespace,
	}, foundRoute)

	if err != nil {
		if !createRoute {
			log.Info("Serving runtime does not have 'enable-route' annotation set to 'True'. Skipping route creation")
			return nil
		}
		if apierrs.IsNotFound(err) {
			log.Info("Creating Route")
			// Add .metatada.ownerReferences to the route to be deleted by the
			// Kubernetes garbage collector if the predictor is deleted
			err = ctrl.SetControllerReference(servingruntime, desiredRoute, r.Scheme)
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
	if !createRoute {
		log.Info("Serving Runtime does not have 'enable-route' annotation set to 'True'. Deleting existing route")
		return r.Delete(ctx, foundRoute)
	}
	// Reconcile the route spec if it has been manually modified
	if !justCreated && !CompareInferenceServiceRoutes(*desiredRoute, *foundRoute) {
		log.Info("Reconciling Route")
		// Retry the update operation when the ingress controller eventually
		// updates the resource version field
		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			// Get the last route revision
			if err := r.Get(ctx, types.NamespacedName{
				Name:      desiredRoute.Name,
				Namespace: servingruntime.Namespace,
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

func (r *ServingRuntimeRouteReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("Resouce Name", req.Name, "Namespace", req.Namespace)

	servingRuntime := &mmv1alpha1.ServingRuntime{}
	err := r.Get(ctx, req.NamespacedName, servingRuntime)
	if err != nil && apierrs.IsNotFound(err) {
		log.Info("ServingRuntime not found")
		return ctrl.Result{}, nil
	} else if err != nil {
		log.Error(err, "Unable to fetch the ServingRuntime")
		return ctrl.Result{}, err
	}

	err = r.reconcileRoute(servingRuntime, ctx)
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ServingRuntimeRouteReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&mmv1alpha1.ServingRuntime{}).
		Owns(&routev1.Route{}).
		Complete(r)
}
