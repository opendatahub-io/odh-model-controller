package controllers

import (
	"context"
	"os"
	"reflect"

	"sigs.k8s.io/controller-runtime/pkg/handler"

	"github.com/go-logr/logr"
	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	"github.com/opendatahub-io/odh-model-controller/controllers/constants"
	"github.com/opendatahub-io/odh-model-controller/controllers/utils"
	corev1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// KServeRayTlsReconciler holds the controller configuration.
type KServeRayTlsReconciler struct {
	client client.Client
	log    logr.Logger
}

func NewKServeRayTlsReconciler(client client.Client, log logr.Logger) *KServeRayTlsReconciler {
	return &KServeRayTlsReconciler{
		client: client,
		log:    log,
	}
}

// The reconcile logic works as follows:
// ServingRuntime(multinode):
// - On creation: The ray-tls-script ConfigMap and ray-ca-cert Secret are created in the target namespace.
// - On deletion: The ray-tls-script ConfigMap and ray-ca-cert Secret are deleted only when multinode ServingRuntimes are deleted from the target namespace.

// ConfigMap:
// - When the original ConfigMap is updated in the control namespace: The ray-tls-scripts ConfigMap is deleted and recreated in the namespace where multinode ServingRuntimes exist.
// - When the ConfigMap is deleted in the target namespace: The ray-tls-scripts ConfigMap will be recreated.

// Secret:
// - When the original Secret is updated in the control namespace: The ray-ca-cert Secret is deleted and recreated in the namespace where multinode ServingRuntimes exist.
// - When the Secret is deleted in the target namespace: The ray-ca-cert Secret will be recreated.
func (r *KServeRayTlsReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.log
	controllerNs := os.Getenv("POD_NAMESPACE")
	var servingRuntimeList kservev1alpha1.ServingRuntimeList
	if err := r.client.List(ctx, &servingRuntimeList); err != nil {
		return ctrl.Result{}, err
	}
	noMultiNodeSrExistInNs := !existMultiNodeServingRuntimeInNs(req.Namespace, servingRuntimeList)

	if req.Name == constants.RayTlsScriptConfigMapName {
		if req.Namespace == controllerNs {
			log.Info("Original Ray TLS Scripts ConfigMap is updated", "name", constants.RayTlsScriptConfigMapName, "namespace", req.Namespace)
			for _, sr := range servingRuntimeList.Items {
				if isMultiNodeServingRuntime(sr) {
					if err := r.cleanupRayResourcesByKind(ctx, log, sr.Namespace, "ConfigMap"); err != nil {
						return ctrl.Result{}, err
					}
				}
			}
		}
		if err := r.reconcileRayTlsScriptsConfigMap(ctx, log, controllerNs, req.Namespace, noMultiNodeSrExistInNs); err != nil {
			return ctrl.Result{}, err
		}
	} else if req.Name == constants.RayCATlsSecretName {
		if req.Namespace == controllerNs {
			log.Info("Original Ray CA Cert Secret is updated", "name", constants.RayCATlsSecretName, "namespace", req.Namespace)
			for _, sr := range servingRuntimeList.Items {
				if isMultiNodeServingRuntime(sr) {
					if err := r.cleanupRayResourcesByKind(ctx, log, sr.Namespace, "Secret"); err != nil {
						return ctrl.Result{}, err
					}
				}
			}
		}
		if err := r.reconcileRayCACertSecret(ctx, log, controllerNs, req.Namespace, noMultiNodeSrExistInNs); err != nil {
			return ctrl.Result{}, err
		}
	} else {
		err := r.reconcileRayTlsScriptsConfigMap(ctx, log, controllerNs, req.Namespace, !existMultiNodeServingRuntimeInNs(req.Namespace, servingRuntimeList))
		if err != nil {
			return ctrl.Result{}, err
		}
		err = r.reconcileRayCACertSecret(ctx, log, controllerNs, req.Namespace, !existMultiNodeServingRuntimeInNs(req.Namespace, servingRuntimeList))
		if err != nil {
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{}, nil
}

func checkRayTLSResource(objectName string) bool {
	return objectName == constants.RayCATlsSecretName || objectName == constants.RayTlsScriptConfigMapName
}

// reconcileRayTLSResource filters out ConfigMaps and Secrets that do not match the predefined constants: RayCATlsSecretName or RayTlsScriptConfigMapName.
// This ensures that only the relevant ConfigMaps and Secrets for Ray TLS configuration are captured and processed for the servingRuntime.
func reconcileRayTLSResource() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			if _, ok := e.Object.(*kservev1alpha1.ServingRuntime); ok {
				return true
			}
			return false
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			if _, ok := e.Object.(*kservev1alpha1.ServingRuntime); ok {
				return true
			}
			objectName := e.Object.GetName()
			return checkRayTLSResource(objectName)
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			if _, ok := e.ObjectNew.(*kservev1alpha1.ServingRuntime); ok {
				return true
			}
			objectName := e.ObjectNew.GetName()
			return checkRayTLSResource(objectName)
		},
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *KServeRayTlsReconciler) SetupWithManager(mgr ctrl.Manager) error {
	builder := ctrl.NewControllerManagedBy(mgr).
		For(&kservev1alpha1.ServingRuntime{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.Secret{}).
		Watches(&corev1.ConfigMap{}, &handler.EnqueueRequestForObject{}).
		Watches(&corev1.Secret{}, &handler.EnqueueRequestForObject{}).
		WithEventFilter(reconcileRayTLSResource())

	return builder.Complete(r)
}

// reconcileRayTlsScriptsConfigMap watch ray-tls-scripts configmap in the cluster
// and it will create/update/delete ray-tls-scripts configmap in the namespace where multinode ServingRuntime created
func (r *KServeRayTlsReconciler) reconcileRayTlsScriptsConfigMap(ctx context.Context, log logr.Logger, ctrlNs string, targetNs string, noMultiNodeSrExistInNs bool) error {
	// When original configmap is updated, it does not need to reconcile
	if ctrlNs == targetNs {
		return nil
	}

	log.Info("Reconciling Ray TLS Scripts ConfigMap", "name", constants.RayTlsScriptConfigMapName, "namespace", targetNs)
	srcConfigMap := &corev1.ConfigMap{}
	err := r.client.Get(ctx, types.NamespacedName{Name: constants.RayTlsScriptConfigMapName, Namespace: ctrlNs}, srcConfigMap)
	if err != nil {
		return err
	}

	// Create Desired resource
	desiredConfigMapResource, err := r.createDesiredConfigMapResource(targetNs, srcConfigMap)
	if err != nil {
		return err
	}

	// Get Existing resource
	existingConfigMapResource := &corev1.ConfigMap{}
	err = r.client.Get(ctx, types.NamespacedName{Name: constants.RayTlsScriptConfigMapName, Namespace: targetNs}, existingConfigMapResource)
	if err != nil {
		if apierrs.IsNotFound(err) {
			existingConfigMapResource = nil
		} else {
			return err
		}
	}

	// Process Delta
	if err = r.processDeltaConfigMap(ctx, log, desiredConfigMapResource, existingConfigMapResource, noMultiNodeSrExistInNs); err != nil {
		return err
	}
	return nil
}

func (r *KServeRayTlsReconciler) createDesiredConfigMapResource(destNs string, srcConfigmap *corev1.ConfigMap) (*corev1.ConfigMap, error) {
	desiredConfigMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      srcConfigmap.Name,
			Namespace: destNs,
			Labels: map[string]string{
				"opendatahub.io/managed":       "true",
				"app.kubernetes.io/name":       "odh-model-controller",
				"app.kubernetes.io/component":  "kserve",
				"app.kubernetes.io/part-of":    "odh-model-serving",
				"app.kubernetes.io/managed-by": "odh-model-controller",
			},
		},
		Data: srcConfigmap.Data,
	}
	return desiredConfigMap, nil
}

func (r *KServeRayTlsReconciler) processDeltaConfigMap(ctx context.Context, log logr.Logger, desiredConfigMapResource *corev1.ConfigMap, existingConfigMapResource *corev1.ConfigMap, noMultiNodeSrExistInNs bool) (err error) {
	hasChanged := false

	if shouldAddRayConfigMap(existingConfigMapResource, noMultiNodeSrExistInNs) {
		hasChanged = true
		log.V(1).Info("Delta found", "create", desiredConfigMapResource.GetName(), "namespace", desiredConfigMapResource.Namespace)
		if err = r.client.Create(ctx, desiredConfigMapResource); err != nil {
			return err
		}
	}

	if isUpdatedRayConfigMap(desiredConfigMapResource, existingConfigMapResource) {
		hasChanged = true
		log.V(1).Info("Delta found", "update", existingConfigMapResource.GetName(), "namespace", existingConfigMapResource.Namespace)
		rp := desiredConfigMapResource.DeepCopy()

		if err = r.client.Update(ctx, rp); err != nil {
			return err
		}
	}

	if shouldDeleteRayConfigMap(existingConfigMapResource, noMultiNodeSrExistInNs) {
		hasChanged = true
		log.V(1).Info("Delta found", "remove", existingConfigMapResource.GetName(), "namespace", existingConfigMapResource.Namespace)
		if err = r.client.Delete(ctx, existingConfigMapResource); err != nil {
			return err
		}
	}

	if !hasChanged && !noMultiNodeSrExistInNs {
		log.V(1).Info("No delta found", "name", desiredConfigMapResource.GetName(), "namespace", desiredConfigMapResource.Namespace)
	}

	return nil
}

func shouldAddRayConfigMap(existingConfigMap *corev1.ConfigMap, noMultiNodeSrExistInNs bool) bool {
	return !noMultiNodeSrExistInNs && utils.IsNil(existingConfigMap)
}
func isUpdatedRayConfigMap(desiredConfigMap *corev1.ConfigMap, existingConfigMap *corev1.ConfigMap) bool {
	return utils.IsNotNil(existingConfigMap) && !reflect.DeepEqual(desiredConfigMap.Data, existingConfigMap.Data)
}
func shouldDeleteRayConfigMap(existingConfigMap *corev1.ConfigMap, noMultiNodeSrExistInNs bool) bool {
	return utils.IsNotNil(existingConfigMap) && noMultiNodeSrExistInNs
}

// reconcileRayCACertSecret watch ray-ca-cert secret in the cluster
// and it will create/update/delete ray-ca-cert secret in the namespace where multinode ServingRuntime created
func (r *KServeRayTlsReconciler) reconcileRayCACertSecret(ctx context.Context, log logr.Logger, ctrlNs string, targetNs string, noMultiNodeSrExistInNs bool) error {
	// When original secret is updated, it does not need to reconcile
	if ctrlNs == targetNs {
		return nil
	}
	log.Info("Reconciling Ray CA Cert Secret", "name", constants.RayCATlsSecretName, "namespace", targetNs)
	srcSecret := &corev1.Secret{}
	err := r.client.Get(ctx, types.NamespacedName{Name: constants.RayCATlsSecretName, Namespace: ctrlNs}, srcSecret)
	if err != nil {
		return err
	}

	// Create Desired resource
	desiredSecretResource, err := r.createDesiredSecretResource(targetNs, srcSecret)
	if err != nil {
		return err
	}

	// Get Existing resource
	existingSecretResource := &corev1.Secret{}
	err = r.client.Get(ctx, types.NamespacedName{Name: constants.RayCATlsSecretName, Namespace: targetNs}, existingSecretResource)
	if err != nil {
		if apierrs.IsNotFound(err) {
			existingSecretResource = nil
		} else {
			return err
		}
	}

	// Process Delta
	if err = r.processDeltaSecret(ctx, log, desiredSecretResource, existingSecretResource, noMultiNodeSrExistInNs); err != nil {
		return err
	}
	return nil
}

func (r *KServeRayTlsReconciler) createDesiredSecretResource(destNs string, sourceSecret *corev1.Secret) (*corev1.Secret, error) {
	desiredSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sourceSecret.Name,
			Namespace: destNs,
			Labels: map[string]string{
				"opendatahub.io/managed":       "true",
				"app.kubernetes.io/name":       "odh-model-controller",
				"app.kubernetes.io/component":  "kserve",
				"app.kubernetes.io/part-of":    "odh-model-serving",
				"app.kubernetes.io/managed-by": "odh-model-controller",
			},
		},
		Data: sourceSecret.Data,
		Type: sourceSecret.Type,
	}
	return desiredSecret, nil
}

func (r *KServeRayTlsReconciler) processDeltaSecret(ctx context.Context, log logr.Logger, desiredSecretResource *corev1.Secret, existingSecretResource *corev1.Secret, noMultiNodeSrExistInNs bool) (err error) {
	hasChanged := false

	if shouldAddRaySecret(existingSecretResource, noMultiNodeSrExistInNs) {
		hasChanged = true
		log.V(1).Info("Delta found", "create", desiredSecretResource.GetName(), "namespace", desiredSecretResource.Namespace)
		if err = r.client.Create(ctx, desiredSecretResource); err != nil {
			return err
		}
	}

	if isUpdatedRaySecret(desiredSecretResource, existingSecretResource) {
		hasChanged = true
		log.V(1).Info("Delta found", "update", existingSecretResource.GetName(), "namespace", existingSecretResource.Namespace)
		rp := desiredSecretResource.DeepCopy()

		if err = r.client.Update(ctx, rp); err != nil {
			return err
		}
	}

	if shouldDeletedRaySecret(existingSecretResource, noMultiNodeSrExistInNs) {
		hasChanged = true
		log.V(1).Info("Delta found", "remove", existingSecretResource.GetName(), "namespace", existingSecretResource.Namespace)
		if err = r.client.Delete(ctx, existingSecretResource); err != nil {
			return err
		}
	}
	if !hasChanged && !noMultiNodeSrExistInNs {
		log.V(1).Info("No delta found", "name", desiredSecretResource.GetName(), "namespace", desiredSecretResource.Namespace)
	}
	return nil
}

func shouldAddRaySecret(existingSecret *corev1.Secret, noMultiNodeSrExistInNs bool) bool {
	return !noMultiNodeSrExistInNs && utils.IsNil(existingSecret)
}
func isUpdatedRaySecret(desiredSecret *corev1.Secret, existingSecret *corev1.Secret) bool {
	return utils.IsNotNil(existingSecret) && !reflect.DeepEqual(desiredSecret.Data, existingSecret.Data)
}
func shouldDeletedRaySecret(existingSecret *corev1.Secret, noMultiNodeSrExistInNs bool) bool {
	return utils.IsNotNil(existingSecret) && noMultiNodeSrExistInNs
}

func (r *KServeRayTlsReconciler) cleanupRayResourcesByKind(ctx context.Context, log logr.Logger, targetNs string, kind string) error {
	if kind == "ConfigMap" {
		configmap := &corev1.ConfigMap{}
		err := r.client.Get(ctx, types.NamespacedName{
			Name:      constants.RayTlsScriptConfigMapName,
			Namespace: targetNs,
		}, configmap)
		if err != nil {
			if apierrs.IsNotFound(err) {
				log.Info("ConfigMap not found, skipping", "name", constants.RayTlsScriptConfigMapName, "namespace", targetNs)
			}
			return err
		}

		log.Info("Deleting ConfigMap", "name", constants.RayTlsScriptConfigMapName, "namespace", targetNs)
		err = r.client.Delete(ctx, configmap)
		if err != nil {
			return err
		}
	}

	if kind == "Secret" {
		secret := &corev1.Secret{}
		err := r.client.Get(ctx, types.NamespacedName{
			Name:      constants.RayCATlsSecretName,
			Namespace: targetNs,
		}, secret)
		if err != nil {
			if apierrs.IsNotFound(err) {
				log.Info("Secret not found, skipping", "name", constants.RayCATlsSecretName, "namespace", targetNs)
			}
			return err
		}

		log.Info("Deleting Secret", "name", constants.RayCATlsSecretName, "namespace", targetNs)
		err = r.client.Delete(ctx, secret)
		if err != nil {
			return err
		}
	}
	return nil
}

func existMultiNodeServingRuntimeInNs(targetNs string, srList kservev1alpha1.ServingRuntimeList) bool {
	for _, sr := range srList.Items {
		if sr.Namespace == targetNs {
			return isMultiNodeServingRuntime(sr)
		}
	}
	return false
}

// Determine if ServingRuntime matches specific conditions
// TO-DO upstream Kserve 0.15 will have a new API WorkerSpec
// So for now, it will check servingRuntime name, but after we move to 0.15, it needs to check workerSpec is specified or not.(RHOAIENG-16147)
func isMultiNodeServingRuntime(servingRuntime kservev1alpha1.ServingRuntime) bool {
	return servingRuntime.Name == "vllm-multinode-runtime"
}
