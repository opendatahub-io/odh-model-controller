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
	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	authorinov1beta2 "github.com/kuadrant/authorino/api/v1beta2"
	"github.com/opendatahub-io/odh-model-controller/controllers/reconcilers"
	"github.com/opendatahub-io/odh-model-controller/controllers/utils"
	routev1 "github.com/openshift/api/route/v1"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	authv1 "k8s.io/api/rbac/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	// "sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// OpenshiftInferenceServiceReconciler holds the controller configuration.
type OpenshiftInferenceServiceReconciler struct {
	client                         client.Client
	clientReader                   client.Reader
	log                            logr.Logger
	MeshDisabled                   bool
	mmISVCReconciler               *reconcilers.ModelMeshInferenceServiceReconciler
	kserveServerlessISVCReconciler *reconcilers.KserveServerlessInferenceServiceReconciler
	kserveRawISVCReconciler        *reconcilers.KserveRawInferenceServiceReconciler
}

func NewOpenshiftInferenceServiceReconciler(client client.Client, clientReader client.Reader, log logr.Logger, meshDisabled bool) *OpenshiftInferenceServiceReconciler {
	return &OpenshiftInferenceServiceReconciler{
		client:                         client,
		clientReader:                   clientReader,
		log:                            log,
		MeshDisabled:                   meshDisabled,
		mmISVCReconciler:               reconcilers.NewModelMeshInferenceServiceReconciler(client),
		kserveServerlessISVCReconciler: reconcilers.NewKServeServerlessInferenceServiceReconciler(client, clientReader),
		kserveRawISVCReconciler:        reconcilers.NewKServeRawInferenceServiceReconciler(client),
	}
}

// Reconcile performs the reconciling of the Openshift objects for a Kubeflow
// InferenceService.
func (r *OpenshiftInferenceServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Initialize logger format
	log := r.log.WithValues("InferenceService", req.Name, "namespace", req.Namespace)
	// Get the InferenceService object when a reconciliation event is triggered (create,
	// update, delete)
	isvc := &kservev1beta1.InferenceService{}
	err := r.client.Get(ctx, req.NamespacedName, isvc)
	if err != nil && apierrs.IsNotFound(err) {
		log.Info("Stop InferenceService reconciliation")
		// InferenceService not found, so we check for any other inference services that might be using Kserve/ModelMesh
		// If none are found, we delete the common namespace-scoped resources that were created for Kserve/ModelMesh.
		err1 := r.DeleteResourcesIfNoIsvcExists(ctx, log, req.Namespace)
		if err1 != nil {
			log.Error(err1, "Unable to clean up resources")
			return ctrl.Result{}, err1
		}
		return ctrl.Result{}, nil
	} else if err != nil {
		log.Error(err, "Unable to fetch the InferenceService")
		return ctrl.Result{}, err
	}

	if isvc.GetDeletionTimestamp() != nil {
		return reconcile.Result{}, r.onDeletion(ctx, log, isvc)
	}

	// Check what deployment mode is used by the InferenceService. We have differing reconciliation logic for Kserve and ModelMesh
	IsvcDeploymentMode, err := utils.GetDeploymentModeForIsvc(ctx, r.client, isvc)
	if err != nil {
		return ctrl.Result{}, err
	}
	switch IsvcDeploymentMode {
	case utils.ModelMesh:
		log.Info("Reconciling InferenceService for ModelMesh")
		err = r.mmISVCReconciler.Reconcile(ctx, log, isvc)
	case utils.Serverless:
		log.Info("Reconciling InferenceService for Kserve in mode Serverless")
		err = r.kserveServerlessISVCReconciler.Reconcile(ctx, log, isvc)
	case utils.RawDeployment:
		log.Info("Reconciling InferenceService for Kserve in mode RawDeployment")
		err = r.kserveRawISVCReconciler.Reconcile(ctx, log, isvc)
	}

	return ctrl.Result{}, err
}

// SetupWithManager sets up the controller with the Manager.
func (r *OpenshiftInferenceServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	builder := ctrl.NewControllerManagedBy(mgr).
		For(&kservev1beta1.InferenceService{}).
		Owns(&kservev1alpha1.ServingRuntime{}).
		Owns(&corev1.Namespace{}).
		Owns(&routev1.Route{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.Secret{}).
		Owns(&authv1.ClusterRoleBinding{}).
		Owns(&networkingv1.NetworkPolicy{}).
		Owns(&monitoringv1.ServiceMonitor{}).
		Owns(&monitoringv1.PodMonitor{}).
		Watches(&kservev1alpha1.ServingRuntime{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, o client.Object) []reconcile.Request {
				r.log.Info("Reconcile event triggered by serving runtime: " + o.GetName())
				inferenceServicesList := &kservev1beta1.InferenceServiceList{}
				opts := []client.ListOption{client.InNamespace(o.GetNamespace())}

				// Todo: Get only Inference Services that are deploying on the specific serving runtime
				err := r.client.List(ctx, inferenceServicesList, opts...)
				if err != nil {
					r.log.Info("Error getting list of inference services for namespace")
					return []reconcile.Request{}
				}

				if len(inferenceServicesList.Items) == 0 {
					r.log.Info("No InferenceServices found for Serving Runtime: " + o.GetName())
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
			})).
		Watches(&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, o client.Object) []reconcile.Request {
				r.log.Info("Reconcile event triggered by Secret: " + o.GetName())
				isvc := &kservev1beta1.InferenceService{}
				err := r.client.Get(ctx, types.NamespacedName{Name: o.GetName(), Namespace: o.GetNamespace()}, isvc)
				if err != nil {
					if apierrs.IsNotFound(err) {
						return []reconcile.Request{}
					}
					r.log.Error(err, "Error getting the inferenceService", "name", o.GetName())
					return []reconcile.Request{}
				}

				return []reconcile.Request{
					{NamespacedName: types.NamespacedName{Name: o.GetName(), Namespace: o.GetNamespace()}},
				}
			}))

	kserveWithMeshEnabled, kserveWithMeshEnabledErr := utils.VerifyIfComponentIsEnabled(context.Background(), mgr.GetClient(), utils.KServeWithServiceMeshComponent)
	if kserveWithMeshEnabledErr != nil {
		r.log.V(1).Error(kserveWithMeshEnabledErr, "could not determine if kserve have service mesh enabled")
	}

	isAuthConfigAvailable, crdErr := utils.IsCrdAvailable(mgr.GetConfig(), authorinov1beta2.GroupVersion.String(), "AuthConfig")
	if crdErr != nil {
		r.log.V(1).Error(crdErr, "could not determine if AuthConfig CRD is available")
		return crdErr
	}

	if kserveWithMeshEnabled && isAuthConfigAvailable {
		r.log.Info("KServe is enabled and AuthConfig CRD is available, watching AuthConfigs")
		builder.Owns(&authorinov1beta2.AuthConfig{})
	} else if kserveWithMeshEnabled {
		r.log.Info("Using KServe with Service Mesh, but AuthConfig CRD is not installed - skipping AuthConfigs watches.")
	} else {
		r.log.Info("Didn't find KServe with Service Mesh.")
	}

	return builder.Complete(r)
}

// general clean-up, mostly resources in different namespaces from kservev1beta1.InferenceService
func (r *OpenshiftInferenceServiceReconciler) onDeletion(ctx context.Context, log logr.Logger, inferenceService *kservev1beta1.InferenceService) error {
	log.V(1).Info("Running cleanup logic")

	IsvcDeploymentMode, err := utils.GetDeploymentModeForIsvc(ctx, r.client, inferenceService)
	if err != nil {
		log.V(1).Error(err, "Could not determine deployment mode for ISVC. Some resources related to the inferenceservice might not be deleted.")
	}
	if IsvcDeploymentMode == utils.Serverless {
		log.V(1).Info("Deleting kserve inference resource (Serverless Mode)")
		return r.kserveServerlessISVCReconciler.OnDeletionOfKserveInferenceService(ctx, log, inferenceService)
	}
	return nil
}

func (r *OpenshiftInferenceServiceReconciler) DeleteResourcesIfNoIsvcExists(ctx context.Context, log logr.Logger, namespace string) error {
	if err := r.kserveServerlessISVCReconciler.CleanupNamespaceIfNoKserveIsvcExists(ctx, log, namespace); err != nil {
		return err
	}
	if err := r.mmISVCReconciler.DeleteModelMeshResourcesIfNoMMIsvcExists(ctx, log, namespace); err != nil {
		return err
	}
	return nil
}
