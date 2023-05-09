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
	"strconv"

	"github.com/go-logr/logr"
	predictorv1 "github.com/kserve/modelmesh-serving/apis/serving/v1alpha1"
	inferenceservicev1 "github.com/kserve/modelmesh-serving/apis/serving/v1beta1"
	routev1 "github.com/openshift/api/route/v1"
	virtualservicev1 "istio.io/client-go/pkg/apis/networking/v1alpha3"
	corev1 "k8s.io/api/core/v1"
	authv1 "k8s.io/api/rbac/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	OpendataHubServiceMesh = "opendatahub.io/service-mesh"
)

// OpenshiftInferenceServiceReconciler holds the controller configuration.
type OpenshiftInferenceServiceReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Log    logr.Logger
}

// ClusterRole permissions

// +kubebuilder:rbac:groups=serving.kserve.io,resources=inferenceservices,verbs=get;list;watch;update
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

// Reconcile performs the reconciling of the Openshift objects for a Kubeflow
// InferenceService.
func (r *OpenshiftInferenceServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Initialize logger format
	log := r.Log.WithValues("InferenceService", req.Name, "namespace", req.Namespace)

	// Get the InferenceService object when a reconciliation event is triggered (create,
	// update, delete)
	inferenceservice := &inferenceservicev1.InferenceService{}
	err := r.Get(ctx, req.NamespacedName, inferenceservice)
	if err != nil && apierrs.IsNotFound(err) {
		log.Info("Stop InferenceService reconciliation")
		return ctrl.Result{}, nil
	} else if err != nil {
		log.Error(err, "Unable to fetch the InferenceService")
		return ctrl.Result{}, err
	}

	// Get InferenceService Namespace
	namespace := &corev1.Namespace{}
	err = r.Get(ctx, types.NamespacedName{Name: req.Namespace}, namespace)
	if err != nil && apierrs.IsNotFound(err) {
		return ctrl.Result{}, nil
	} else if err != nil {
		return ctrl.Result{}, err
	}

	isServiceMeshEnabled, _ := strconv.ParseBool(namespace.Labels[OpendataHubServiceMesh])

	// Finalizer, to delete VS for traffic splitting
	myFinalizerName := "serving.opendatahub.io/finalizer"
	if inferenceservice.ObjectMeta.DeletionTimestamp.IsZero() {
		log.Info("Add finalizer", "isvc", inferenceservice.Name)
		// The object is not being deleted, so if it does not have our finalizer,
		// then lets add the finalizer and update the object. This is equivalent
		// registering our finalizer.
		if !controllerutil.ContainsFinalizer(inferenceservice, myFinalizerName) {
			controllerutil.AddFinalizer(inferenceservice, myFinalizerName)
			if err := r.Update(ctx, inferenceservice); err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		// The object is being deleted
		if controllerutil.ContainsFinalizer(inferenceservice, myFinalizerName) {
			// our finalizer is present, so lets handle any external dependency
			log.Info("isServiceMeshEnabled", "is", isServiceMeshEnabled)
			if isServiceMeshEnabled {
				log.Info("model-tag", "model-tag", inferenceservice.Labels["serving.kserve.io/model-tag"])
				if tag, ok := inferenceservice.Labels["serving.kserve.io/model-tag"]; ok && len(tag) != 0 {
					err := r.ReconcileTrafficSplitting(inferenceservice, ctx)
					if err != nil {
						return ctrl.Result{}, err
					}
				}
			}

			// remove our finalizer from the list and update it.
			log.Info("Remove finalizer", "isvc", inferenceservice.Name)
			controllerutil.RemoveFinalizer(inferenceservice, myFinalizerName)
			if err := r.Update(ctx, inferenceservice); err != nil {
				return ctrl.Result{}, err
			}
		}

		// Stop reconciliation as the item is being deleted
		return ctrl.Result{}, nil
	}

	if isServiceMeshEnabled {
		err = r.ReconcileVirtualService(inferenceservice, ctx)
		if err != nil {
			return ctrl.Result{}, err
		}

		tag, tagOk := inferenceservice.Labels["serving.kserve.io/model-tag"]
		vsName, vsNameOk := inferenceservice.Annotations["serving.opendatahub.io/vs-traffic-splitting"]
		if (tagOk && len(tag) != 0) || (vsNameOk && len(vsName) != 0) {
			err := r.ReconcileTrafficSplitting(inferenceservice, ctx)
			if err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		err = r.ReconcileRoute(inferenceservice, ctx)
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	err = r.ReconcileSA(inferenceservice, ctx)
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *OpenshiftInferenceServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	builder := ctrl.NewControllerManagedBy(mgr).
		For(&inferenceservicev1.InferenceService{}).
		Owns(&predictorv1.ServingRuntime{}).
		Owns(&corev1.Namespace{}).
		Owns(&routev1.Route{}).
		Owns(&virtualservicev1.VirtualService{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.Secret{}).
		Owns(&authv1.ClusterRoleBinding{}).
		Watches(&source.Kind{Type: &predictorv1.ServingRuntime{}},
			handler.EnqueueRequestsFromMapFunc(func(o client.Object) []reconcile.Request {
				r.Log.Info("Reconcile event triggered by serving runtime: " + o.GetName())
				inferenceServicesList := &inferenceservicev1.InferenceServiceList{}
				opts := []client.ListOption{client.InNamespace(o.GetNamespace())}

				// Todo: Get only Inference Services that are deploying on the specific serving runtime
				err := r.List(context.TODO(), inferenceServicesList, opts...)
				if err != nil {
					r.Log.Info("Error getting list of inference services for namespace")
					return []reconcile.Request{}
				}

				if len(inferenceServicesList.Items) == 0 {
					r.Log.Info("No InferenceServices found for Serving Runtime: " + o.GetName())
					return []reconcile.Request{}
				}

				reconcileRequests := make([]reconcile.Request, 0, len(inferenceServicesList.Items))
				for _, inferenceService := range inferenceServicesList.Items {
					reconcileRequests = append(reconcileRequests, reconcile.Request{
						NamespacedName: types.NamespacedName{
							Name:      inferenceService.Name,
							Namespace: inferenceService.Namespace,
						},
					})
				}
				return reconcileRequests
			}))
	err := builder.Complete(r)
	if err != nil {
		return err
	}

	return nil
}
