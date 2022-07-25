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

	"github.com/go-logr/logr"
	predictorv1 "github.com/kserve/modelmesh-serving/apis/serving/v1alpha1"
	routev1 "github.com/openshift/api/route/v1"
	corev1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// OpenshiftPredictorReconciler holds the controller configuration.
type OpenshiftPredictorReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	Log          logr.Logger
	MeshDisabled bool
}

// ClusterRole permissions

// +kubebuilder:rbac:groups=serving.kserve.io,resources=predictors,verbs=get;list;watch
// +kubebuilder:rbac:groups=serving.kserve.io,resources=predictors/finalizers,verbs=get;list;watch;update
// +kubebuilder:rbac:groups=serving.kserve.io,resources=servingruntimes,verbs=get;list;watch;create;update
// +kubebuilder:rbac:groups=serving.kserve.io,resources=servingruntimes/finalizers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.istio.io,resources=virtualservices,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.istio.io,resources=virtualservices/finalizers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=maistra.io,resources=servicemeshmembers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=maistra.io,resources=servicemeshmembers/finalizers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=maistra.io,resources=servicemeshcontrolplanes,verbs=get;list;watch;create;update;patch;use
// +kubebuilder:rbac:groups=route.openshift.io,resources=routes,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups="",resources=configmaps;namespaces;pods;services;serviceaccounts;secrets,verbs=get;list;watch;create;update;patch

// ComparePredictors checks if two predictors are equal, if not return false
func ComparePredictors(pr1 predictorv1.Predictor, pr2 predictorv1.Predictor) bool {
	return reflect.DeepEqual(pr1.ObjectMeta.Labels, pr2.ObjectMeta.Labels) &&
		reflect.DeepEqual(pr1.ObjectMeta.Annotations, pr2.ObjectMeta.Annotations) &&
		reflect.DeepEqual(pr1.Spec, pr2.Spec)
}

// Reconcile performs the reconciling of the Openshift objects for a Kubeflow
// Predictor.
func (r *OpenshiftPredictorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Initialize logger format
	log := r.Log.WithValues("Predictor", req.Name, "namespace", req.Namespace)

	// Get the Predictor object when a reconciliation event is triggered (create,
	// update, delete)
	predictor := &predictorv1.Predictor{}
	err := r.Get(ctx, req.NamespacedName, predictor)
	if err != nil && apierrs.IsNotFound(err) {
		log.Info("Stop Predictor reconciliation")
		return ctrl.Result{}, nil
	} else if err != nil {
		log.Error(err, "Unable to fetch the Predictor")
		return ctrl.Result{}, err
	}

	err = r.ReconcileNamespace(predictor, ctx)
	if err != nil {
		return ctrl.Result{}, err
	}

	err = r.ReconcileServingRuntimes(predictor, ctx)
	if err != nil {
		return ctrl.Result{}, err
	}

	err = r.ReconcileRoute(predictor, ctx)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Service Mesh related...uncomment to activate
	//err = r.ReconcileMeshMember(predictor, ctx)
	//if err != nil {
	//		return ctrl.Result{}, err
	//	}
	//err = r.ReconcileVirtualService(predictor, ctx)
	//if err != nil {
	//		return ctrl.Result{}, err
	//	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *OpenshiftPredictorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	builder := ctrl.NewControllerManagedBy(mgr).
		For(&predictorv1.Predictor{}).
		Owns(&predictorv1.ServingRuntime{}).
		// Service Mesh related functionality
		//Owns(&virtualservicev1.VirtualService{}).
		//Owns(&maistrav1.ServiceMeshMember{}).
		Owns(&corev1.Namespace{}).
		Owns(&routev1.Route{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.Secret{})

	err := builder.Complete(r)
	if err != nil {
		return err
	}

	return nil
}
