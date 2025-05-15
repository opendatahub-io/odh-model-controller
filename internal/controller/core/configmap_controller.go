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

package core

import (
	"context"
	"reflect"
	"strings"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
)

const (
	kserveCustomCACertConfigMapName = constants.KServeCACertConfigMapName
	kserveCustomCACertFileName      = constants.KServeCACertFileName
	odhGlobalCACertConfigMapName    = constants.ODHGlobalCertConfigMapName
)

// ConfigMapReconciler was formerly known as KServeCustomCACertReconciler.
type ConfigMapReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;patch;delete

// reconcileConfigMap watch odh global ca cert and it will create/update/delete kserve custom cert configmap
func (r *ConfigMapReconciler) reconcileConfigMap(configmap *corev1.ConfigMap, ctx context.Context, log logr.Logger) error {

	// If kserve custom cert configmap changed, rollback it
	if configmap.Name == kserveCustomCACertConfigMapName {
		odhCustomCertConfigMap := &corev1.ConfigMap{}
		err := r.Get(ctx, types.NamespacedName{Name: odhGlobalCACertConfigMapName, Namespace: configmap.Namespace}, odhCustomCertConfigMap)
		if err != nil {
			return err
		}
		configmap = odhCustomCertConfigMap
	}

	// Include all custom CA bundle certificates in the kserve custom cert configmap.
	certData := strings.TrimSpace(configmap.Data[constants.ODHCustomCACertFileName])

	// If there are any custom CA bundle certificates present, include the cluster-wide CA bundle certificates as well.
	if certData != "" {
		certData = certData + "\n\n" + strings.TrimSpace(configmap.Data[constants.ODHClusterCACertFileName])
	}

	// Create Desired resource
	configData := map[string]string{kserveCustomCACertFileName: certData}
	desiredResource := getDesiredCaCertConfigMapForKServe(kserveCustomCACertConfigMapName, configmap.Namespace, configData)

	// Get Existing resource
	existingResource := &corev1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{Name: kserveCustomCACertConfigMapName, Namespace: configmap.Namespace}, existingResource)
	if err != nil {
		if apierrs.IsNotFound(err) {
			existingResource = nil
		} else {
			return err
		}
	}
	// Process Delta
	if err = r.processDelta(ctx, log, desiredResource, existingResource); err != nil {
		return err
	}
	return nil

}

func checkOpenDataHubGlobalCertCAConfigMapName(objectName string) bool {
	return objectName == odhGlobalCACertConfigMapName || objectName == kserveCustomCACertConfigMapName
}

// reconcileOpenDataHubGlobalCertConfigMap filters out all ConfigMaps that are not the OpenDataHub global certificate ConfigMap.
func reconcileOpenDataHubGlobalCACertConfigMap() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			objectName := e.Object.GetName()
			return checkOpenDataHubGlobalCertCAConfigMapName(objectName)
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			objectName := e.Object.GetName()
			return checkOpenDataHubGlobalCertCAConfigMapName(objectName)
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			objectName := e.ObjectNew.GetName()
			return checkOpenDataHubGlobalCertCAConfigMapName(objectName)
		},
	}
}

func (r *ConfigMapReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Initialize logger format
	log := log.FromContext(ctx).WithValues("ConfigMap", req.Name, "namespace", req.Namespace)

	configmap := &corev1.ConfigMap{}
	err := r.Get(ctx, req.NamespacedName, configmap)
	if err != nil && apierrs.IsNotFound(err) {
		log.Info("Opendatahub global cert ConfigMap not found")
		configmap.Namespace = req.Namespace
	} else if err != nil {
		log.Error(err, "Unable to fetch the ConfigMap")
		return ctrl.Result{}, err
	}

	err = r.reconcileConfigMap(configmap, ctx, log)
	if err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ConfigMapReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Create a builder that only watch OpenDataHub global certificate ConfigMap
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.ConfigMap{}).
		Named("core-configmap").
		WithEventFilter(reconcileOpenDataHubGlobalCACertConfigMap()).
		Complete(r)
}

func getDesiredCaCertConfigMapForKServe(configmapName string, namespace string, caCertData map[string]string) *corev1.ConfigMap {
	desiredConfigMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configmapName,
			Namespace: namespace,
			Labels:    map[string]string{"opendatahub.io/managed": "true"},
		},
		Data: caCertData,
	}

	return desiredConfigMap
}

func (r *ConfigMapReconciler) processDelta(ctx context.Context, log logr.Logger, desiredConfigMap *corev1.ConfigMap, existingConfigMap *corev1.ConfigMap) (err error) {
	hasChanged := false

	if isAdded(desiredConfigMap, existingConfigMap) {
		hasChanged = true
		log.V(1).Info("Delta found", "create", desiredConfigMap.GetName())
		if err = r.Create(ctx, desiredConfigMap); err != nil {
			return err
		}
	}

	if isUpdated(desiredConfigMap, existingConfigMap) {
		hasChanged = true
		log.V(1).Info("Delta found", "update", existingConfigMap.GetName())
		rp := desiredConfigMap.DeepCopy()
		rp.Labels = existingConfigMap.Labels

		if err = r.Update(ctx, rp); err != nil {
			return err
		}
	}

	if isRemoved(desiredConfigMap, existingConfigMap) {
		hasChanged = true
		log.V(1).Info("Delta found", "delete", existingConfigMap.GetName())
		if err = r.Delete(ctx, existingConfigMap); err != nil {
			return err
		}
	}

	if !hasChanged {
		log.V(1).Info("No delta found")
		return nil
	}

	if err := r.deleteStorageSecret(ctx, desiredConfigMap.Namespace); err != nil {
		log.Error(err, "Failed to delete the storage-config secret to update the custom cert")
		return err
	}
	log.V(1).Info("Deleted the storage-config Secret to update the custom cert")

	return nil
}

// This section is intended for regenerating StorageSecret using new data.
func (r *ConfigMapReconciler) deleteStorageSecret(ctx context.Context, namespace string) error {
	foundStorageSecret := &corev1.Secret{}

	err := r.Get(ctx, types.NamespacedName{
		Name:      constants.DefaultStorageConfig,
		Namespace: namespace,
	}, foundStorageSecret)

	if err == nil && foundStorageSecret.Labels["opendatahub.io/managed"] == "true" {
		err = r.Delete(ctx, foundStorageSecret)
		if err != nil {
			return err
		}
	}

	return nil
}

func isAdded(desiredConfigMap *corev1.ConfigMap, existingConfigMap *corev1.ConfigMap) bool {
	return desiredConfigMap.Data[kserveCustomCACertFileName] != "" && utils.IsNil(existingConfigMap)
}

func isUpdated(desiredConfigMap *corev1.ConfigMap, existingConfigMap *corev1.ConfigMap) bool {
	return utils.IsNotNil(existingConfigMap) && desiredConfigMap.Data[kserveCustomCACertFileName] != "" && !reflect.DeepEqual(desiredConfigMap.Data, existingConfigMap.Data)
}

func isRemoved(desiredConfigMap *corev1.ConfigMap, existingConfigMap *corev1.ConfigMap) bool {
	return utils.IsNotNil(existingConfigMap) && desiredConfigMap.Data[kserveCustomCACertFileName] == ""
}
