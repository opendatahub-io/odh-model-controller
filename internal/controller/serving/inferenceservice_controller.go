/*
Copyright 2024.

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

package serving

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	"github.com/hashicorp/go-multierror"
	kedaapi "github.com/kedacore/keda/v2/apis/keda/v1alpha1"
	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	authorinov1beta2 "github.com/kuadrant/authorino/api/v1beta2"
	routev1 "github.com/openshift/api/route/v1"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	authv1 "k8s.io/api/rbac/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlbuilder "sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/yaml"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/serving/reconcilers"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
)

// InferenceServiceReconciler reconciles a InferenceService object
type InferenceServiceReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	clientReader client.Reader
	bearerToken  string

	MeshDisabled                   bool
	ModelRegistryEnabled           bool
	modelRegistrySkipTls           bool
	kserveServerlessISVCReconciler *reconcilers.KserveServerlessInferenceServiceReconciler
	kserveRawISVCReconciler        *reconcilers.KserveRawInferenceServiceReconciler
}

func NewInferenceServiceReconciler(setupLog logr.Logger, client client.Client, scheme *runtime.Scheme, clientReader client.Reader,
	kClient kubernetes.Interface, meshDisabled bool, modelRegistryReconcileEnabled, modelRegistrySkipTls bool, bearerToken string) *InferenceServiceReconciler {
	isvcReconciler := &InferenceServiceReconciler{
		Client:                         client,
		Scheme:                         scheme,
		clientReader:                   clientReader,
		MeshDisabled:                   meshDisabled,
		ModelRegistryEnabled:           modelRegistryReconcileEnabled,
		modelRegistrySkipTls:           modelRegistrySkipTls,
		kserveServerlessISVCReconciler: reconcilers.NewKServeServerlessInferenceServiceReconciler(client, clientReader, kClient),
		kserveRawISVCReconciler:        reconcilers.NewKServeRawInferenceServiceReconciler(client),
		bearerToken:                    bearerToken,
	}

	if modelRegistryReconcileEnabled {
		setupLog.Info("Model registry inference service reconciliation enabled.")
	} else {
		setupLog.Info("Model registry inference service reconciliation disabled. To enable model registry " +
			"reconciliation for InferenceService, please provide --model-registry-inference-reconcile flag.")
	}

	return isvcReconciler
}

// +kubebuilder:rbac:groups=serving.kserve.io,resources=inferenceservices,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=serving.kserve.io,resources=inferenceservices/finalizers,verbs=get;list;watch;update;create;patch;delete

// +kubebuilder:rbac:groups=networking.istio.io,resources=virtualservices,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.istio.io,resources=virtualservices/finalizers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.istio.io,resources=gateways,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=security.istio.io,resources=peerauthentications,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=security.istio.io,resources=authorizationpolicies,verbs=get;list
// +kubebuilder:rbac:groups=telemetry.istio.io,resources=telemetries,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=maistra.io,resources=servicemeshmembers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=maistra.io,resources=servicemeshmemberrolls,verbs=get;list;watch
// +kubebuilder:rbac:groups=maistra.io,resources=servicemeshcontrolplanes,verbs=get;list;watch;use
// +kubebuilder:rbac:groups=route.openshift.io,resources=routes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=route.openshift.io,resources=routes/custom-host,verbs=create
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterrolebindings;roles;rolebindings,verbs=get;list;watch;create;update;patch;watch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch
// +kubebuilder:rbac:groups=monitoring.coreos.com,resources=servicemonitors;podmonitors,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=extensions,resources=ingresses,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=namespaces;pods;endpoints,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups="",resources=secrets;configmaps;serviceaccounts;services,verbs=get;list;watch;create;update;delete;patch
// +kubebuilder:rbac:groups=authorino.kuadrant.io,resources=authconfigs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=datasciencecluster.opendatahub.io,resources=datascienceclusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=dscinitialization.opendatahub.io,resources=dscinitializations,verbs=get;list;watch
// +kubebuilder:rbac:groups=keda.sh,resources=triggerauthentications,verbs=get;list;watch;create;update;patch;watch;delete
// +kubebuilder:rbac:groups=metrics.k8s.io,resources=pods;nodes,verbs=get;list;watch

// Reconcile performs the reconciling of the Openshift objects for a Kubeflow
// InferenceService.
func (r *InferenceServiceReconciler) ReconcileServing(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Initialize logger format
	logger := log.FromContext(ctx).WithValues("InferenceService", req.Name, "namespace", req.Namespace)
	// Get the InferenceService object when a reconciliation event is triggered (create,
	// update, delete)
	isvc := &kservev1beta1.InferenceService{}
	err := r.Client.Get(ctx, req.NamespacedName, isvc)
	if err != nil && apierrs.IsNotFound(err) {
		logger.Info("Stop InferenceService reconciliation")
		// InferenceService not found, so we check for any other inference services that might be using Kserve
		// If none are found, we delete the common namespace-scoped resources that were created for Kserve.
		if err1 := r.DeleteResourcesIfNoIsvcExists(ctx, logger, req.Namespace); err1 != nil {
			logger.Error(err1, "Unable to clean up resources")
			return ctrl.Result{}, err1
		}
		return ctrl.Result{}, nil
	} else if err != nil {
		logger.Error(err, "Unable to fetch the InferenceService")
		return ctrl.Result{}, err
	}

	if isvc.GetDeletionTimestamp() == nil {
		// The object is not being deleted, so if it does not have our finalizer,
		// then lets add the finalizer and update the object. This is equivalent
		// registering our finalizer.
		logger.Info("Adding Finalizer")
		if !controllerutil.ContainsFinalizer(isvc, constants.InferenceServiceODHFinalizerName) {
			controllerutil.AddFinalizer(isvc, constants.InferenceServiceODHFinalizerName)
			patchYaml := "metadata:\n  finalizers: [" + strings.Join(isvc.ObjectMeta.Finalizers, ",") + "]"
			patchJson, _ := yaml.YAMLToJSON([]byte(patchYaml))
			if err := r.Patch(ctx, isvc, client.RawPatch(types.MergePatchType, patchJson)); err != nil {
				return reconcile.Result{}, err
			}
		}
	} else {
		var deleteErrors *multierror.Error
		logger.Info("InferenceService being deleted")
		if controllerutil.ContainsFinalizer(isvc, constants.InferenceServiceODHFinalizerName) {
			err := r.onDeletion(ctx, logger, isvc)
			if err != nil {
				deleteErrors = multierror.Append(deleteErrors, err)
			}
			// Check if we need to also perform cleanup on the namespace
			err1 := r.DeleteResourcesIfNoIsvcExists(ctx, logger, req.Namespace)
			if err1 != nil {
				deleteErrors = multierror.Append(deleteErrors, err1)
			}
			controllerutil.RemoveFinalizer(isvc, constants.InferenceServiceODHFinalizerName)
			patchYaml := "metadata:\n  finalizers: [" + strings.Join(isvc.ObjectMeta.Finalizers, ",") + "]"
			patchJson, _ := yaml.YAMLToJSON([]byte(patchYaml))
			if err := r.Patch(ctx, isvc, client.RawPatch(types.MergePatchType, patchJson)); err != nil {
				return reconcile.Result{}, err
			}

		}
		return reconcile.Result{}, deleteErrors.ErrorOrNil()
	}

	// Check what deployment mode is used by the InferenceService. We have differing reconciliation logic for Kserve
	IsvcDeploymentMode, err := utils.GetDeploymentModeForKServeResource(ctx, r.Client, isvc.GetAnnotations())
	if err != nil {
		return ctrl.Result{}, err
	}
	switch IsvcDeploymentMode {
	case constants.Serverless:
		logger.Info("Reconciling InferenceService for Kserve in mode Serverless")
		err = r.kserveServerlessISVCReconciler.Reconcile(ctx, logger, isvc)
	case constants.RawDeployment:
		logger.Info("Reconciling InferenceService for Kserve in mode RawDeployment")
		err = r.kserveRawISVCReconciler.Reconcile(ctx, logger, isvc)
	}

	return ctrl.Result{}, err
}

// Reconcile is the top-level function to run the different integrations with ODH platform:
// - Model Registry integration (opt-in via CLI flag)
// - OpenShift and other ODH features, like Data Connections, Routes and certificate trust.
func (r *InferenceServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	reconcileResult, reconcileErr := r.ReconcileServing(ctx, req)

	if r.ModelRegistryEnabled {
		mrReconciler, err := reconcilers.NewModelRegistryInferenceServiceReconciler(
			r.Client,
			log.FromContext(ctx).WithName("controllers").WithName("ModelRegistryInferenceService"),
			r.modelRegistrySkipTls,
			r.bearerToken,
		)
		if err != nil {
			return reconcileResult, errors.Join(reconcileErr, err)
		}

		mrResult, mrErr := mrReconciler.Reconcile(ctx, req)

		if mrResult.Requeue {
			reconcileResult.Requeue = true
		}

		if mrResult.RequeueAfter > 0 && (reconcileResult.RequeueAfter == 0 || mrResult.RequeueAfter < reconcileResult.RequeueAfter) {
			reconcileResult.RequeueAfter = mrResult.RequeueAfter
		}

		reconcileErr = errors.Join(reconcileErr, mrErr)
	}

	return reconcileResult, reconcileErr
}

// SetupWithManager sets up the controller with the Manager.
func (r *InferenceServiceReconciler) SetupWithManager(mgr ctrl.Manager, setupLog logr.Logger) error {
	builder := ctrl.NewControllerManagedBy(mgr).
		For(&kservev1beta1.InferenceService{}).
		Owns(&kservev1alpha1.ServingRuntime{}).
		Owns(&corev1.Namespace{}).
		Owns(&routev1.Route{}).
		Owns(&corev1.ServiceAccount{}, ctrlbuilder.MatchEveryOwner).
		Owns(&corev1.Service{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.Secret{}, ctrlbuilder.MatchEveryOwner).
		Owns(&authv1.ClusterRoleBinding{}).
		Owns(&networkingv1.NetworkPolicy{}).
		Owns(&monitoringv1.ServiceMonitor{}).
		Owns(&monitoringv1.PodMonitor{}).
		Owns(&authv1.Role{}, ctrlbuilder.MatchEveryOwner, ctrlbuilder.WithPredicates(reconcilers.KedaLabelPredicate)).
		Owns(&authv1.RoleBinding{}, ctrlbuilder.MatchEveryOwner, ctrlbuilder.WithPredicates(reconcilers.KedaLabelPredicate)).
		Named("inferenceservice").
		Watches(&kservev1alpha1.ServingRuntime{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, o client.Object) []reconcile.Request {
				logger := log.FromContext(ctx)
				logger.Info("Reconcile event triggered by serving runtime: " + o.GetName())
				inferenceServicesList := &kservev1beta1.InferenceServiceList{}
				opts := []client.ListOption{client.InNamespace(o.GetNamespace())}

				// Todo: Get only Inference Services that are deploying on the specific serving runtime
				err := r.Client.List(ctx, inferenceServicesList, opts...)
				if err != nil {
					logger.Info("Error getting list of inference services for namespace")
					return []reconcile.Request{}
				}

				if len(inferenceServicesList.Items) == 0 {
					logger.Info("No InferenceServices found for Serving Runtime: " + o.GetName())
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
				if utils.IsRayTLSSecret(o.GetName()) {
					return []reconcile.Request{}
				}
				logger := log.FromContext(ctx)
				logger.Info("Reconcile event triggered by Secret: " + o.GetName())
				isvc := &kservev1beta1.InferenceService{}
				err := r.Client.Get(ctx, types.NamespacedName{Name: o.GetName(), Namespace: o.GetNamespace()}, isvc)
				if err != nil {
					if apierrs.IsNotFound(err) {
						return []reconcile.Request{}
					}
					logger.Error(err, "Error getting the inferenceService", "name", o.GetName())
					return []reconcile.Request{}
				}

				return []reconcile.Request{
					{NamespacedName: types.NamespacedName{Name: o.GetName(), Namespace: o.GetNamespace()}},
				}
			}))

	kserveWithMeshEnabled, kserveWithMeshEnabledErr := utils.VerifyIfComponentIsEnabled(context.Background(), mgr.GetClient(), utils.KServeWithServiceMeshComponent)
	if kserveWithMeshEnabledErr != nil {
		setupLog.V(1).Error(kserveWithMeshEnabledErr, "could not determine if kserve have service mesh enabled")
	}

	isAuthConfigAvailable, crdErr := utils.IsCrdAvailable(mgr.GetConfig(), authorinov1beta2.GroupVersion.String(), "AuthConfig")
	if crdErr != nil {
		setupLog.V(1).Error(crdErr, "could not determine if AuthConfig CRD is available")
		return crdErr
	}

	isKedaTriggerAuthenticationAvailable, err := utils.IsCrdAvailable(mgr.GetConfig(), kedaapi.GroupVersion.String(), "TriggerAuthentication")
	if err != nil {
		setupLog.V(1).Error(err, "could not determine if TriggerAuthentication CRD is available")
		return err
	}
	if isKedaTriggerAuthenticationAvailable {
		builder.Owns(&kedaapi.TriggerAuthentication{},
			ctrlbuilder.MatchEveryOwner,
			ctrlbuilder.WithPredicates(reconcilers.KedaLabelPredicate),
		)
	}

	if kserveWithMeshEnabled && isAuthConfigAvailable {
		setupLog.Info("KServe is enabled and AuthConfig CRD is available, watching AuthConfigs")
		builder.Owns(&authorinov1beta2.AuthConfig{})
	} else if kserveWithMeshEnabled {
		setupLog.Info("Using KServe with Service Mesh, but AuthConfig CRD is not installed - skipping AuthConfigs watches.")
	} else {
		setupLog.Info("Didn't find KServe with Service Mesh.")
	}

	return builder.Complete(r)
}

// general clean-up, mostly resources in different namespaces from kservev1beta1.InferenceService
func (r *InferenceServiceReconciler) onDeletion(ctx context.Context, log logr.Logger, inferenceService *kservev1beta1.InferenceService) error {

	log.V(1).Info("Triggering Delete for InferenceService: " + inferenceService.Name)

	IsvcDeploymentMode, err := utils.GetDeploymentModeForKServeResource(ctx, r.Client, inferenceService.GetAnnotations())
	if err != nil {
		log.V(1).Error(err, "Could not determine deployment mode for ISVC. Some resources related to the inferenceservice might not be deleted.")
	}
	if IsvcDeploymentMode == constants.Serverless {
		log.V(1).Info("Deleting kserve inference resource (Serverless Mode)")
		return r.kserveServerlessISVCReconciler.OnDeletionOfKserveInferenceService(ctx, log, inferenceService)
	}
	if IsvcDeploymentMode == constants.RawDeployment {
		log.V(1).Info("Deleting kserve inference resource (RawDeployment Mode)")
		return r.kserveRawISVCReconciler.OnDeletionOfKserveInferenceService(ctx, log, inferenceService)
	}
	return nil
}

func (r *InferenceServiceReconciler) DeleteResourcesIfNoIsvcExists(ctx context.Context, log logr.Logger, namespace string) error {
	inferenceServiceList := &kservev1beta1.InferenceServiceList{}
	if err := r.Client.List(ctx, inferenceServiceList, client.InNamespace(namespace)); err != nil {
		return err
	}

	var existingServerlessIsvcs []kservev1beta1.InferenceService
	var existingRawIsvcs []kservev1beta1.InferenceService
	for _, isvc := range inferenceServiceList.Items {
		isvcDeploymentMode, err := utils.GetDeploymentModeForKServeResource(ctx, r.Client, isvc.GetAnnotations())
		if err != nil {
			return err
		}
		switch isvcDeploymentMode {
		case constants.Serverless:
			if isvc.GetDeletionTimestamp() == nil {
				existingServerlessIsvcs = append(existingServerlessIsvcs, isvc)
			}
		case constants.RawDeployment:
			if isvc.GetDeletionTimestamp() == nil {
				existingRawIsvcs = append(existingRawIsvcs, isvc)
			}
		default:
			return fmt.Errorf("Unknown deployment mode for InferenceService: %v", isvc)
		}
	}
	// If there are no ISVCs left, all 3 of these functions get called. Issue is that when the Serverless reconciler gets called
	// and there is no servicemesh installed (which is the case with RawDeployment) the Serverless reconciler will fail when it calls
	// the subresourcereconciler Cleanup function. This is because the Serverless reconciler will try to get the ServiceMesh resources and clean them up.
	// Adding a no match error check will prevent this from erroring out due to that.

	if len(existingServerlessIsvcs) == 0 {
		log.V(1).Info("Triggering Serverless Cleanup for Namespace: " + namespace)
		if err := r.kserveServerlessISVCReconciler.CleanupNamespaceIfNoKserveIsvcExists(ctx, log, namespace); err != nil {
			return err
		}
	}
	if len(existingRawIsvcs) == 0 {
		log.V(1).Info("Triggering RawDeployment Cleanup for Namespace: " + namespace)
		if err := r.kserveRawISVCReconciler.CleanupNamespaceIfNoRawKserveIsvcExists(ctx, log, namespace); err != nil {
			return err
		}
	}
	return nil
}
