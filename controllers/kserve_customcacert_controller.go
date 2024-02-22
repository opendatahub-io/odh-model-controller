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
	"fmt"

	"github.com/go-logr/logr"
	"github.com/opendatahub-io/odh-model-controller/controllers/constants"
	corev1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	kserveCustomCACertConfigMapName = constants.KServeCACertConfigMapName
	odhGlobalCACertConfigMapName    = constants.ODHGlobalCertConfigMapName
)

type KServeCustomCACertReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Log    logr.Logger
}

// reconcileConfigMap watch odh global ca cert and it will create/update/delete kserve custom cert configmap
func (r *KServeCustomCACertReconciler) reconcileConfigMap(configmap *corev1.ConfigMap, ctx context.Context) error {
	// Initialize logger format
	log := r.Log

	odhCustomCertData := configmap.Data[constants.ODHCustomCACertFileName]
	if odhCustomCertData == "" {
		log.Info(fmt.Sprintf("Detected opendatahub global cert ConfigMap (%s), but custom cert is not set\n", odhGlobalCACertConfigMapName))
		kserveCustomCertConfigMap := &corev1.ConfigMap{}
		err := r.Get(ctx, types.NamespacedName{Name: kserveCustomCACertConfigMapName, Namespace: configmap.Namespace}, kserveCustomCertConfigMap)
		if err == nil {
			log.Info(fmt.Sprintf("Deleting KServe custom cert ConfigMap (%s)", kserveCustomCACertConfigMapName))
			if err := r.Delete(ctx, kserveCustomCertConfigMap); err != nil {
				return err
			}
		} else if !apierrs.IsNotFound(err) {
			return err
		}
		return nil
	}

	configData := map[string]string{constants.KServeCACertFileName: odhCustomCertData}
	newCaCertConfigMap := getDesiredCaCertConfigMapForKServe(kserveCustomCACertConfigMapName, configmap.Namespace, configData)

	kserveCustomCertConfigMap := &corev1.ConfigMap{}
	if err := r.Get(ctx, types.NamespacedName{Name: kserveCustomCACertConfigMapName, Namespace: configmap.Namespace}, kserveCustomCertConfigMap); err != nil {
		if !apierrs.IsNotFound(err) {
			return err
		}
		log.Info(fmt.Sprintf("Creating KServe custom cert ConfigMap (%s) because it detected the creation of the opendatahub global cert ConfigMap (%s)", kserveCustomCACertConfigMapName, odhGlobalCACertConfigMapName))
		if err := r.Create(ctx, newCaCertConfigMap); err != nil {
			log.Error(err, "Failed to create KServe custom cert ConfigMap")
			return err
		}
	} else {
		log.Info("Checking if the data in KServe custom cert ConfigMap differs from the data in the opendatahub global CA cert ConfigMap")
		existingOdhCustomCertData := kserveCustomCertConfigMap.Data[constants.KServeCACertFileName]
		if existingOdhCustomCertData == odhCustomCertData {
			log.Info(fmt.Sprintf("No updates required for KServe custom cert ConfigMap (%s) as the data matches the opendatahub global cert ConfigMap (%s)", kserveCustomCACertConfigMapName, odhGlobalCACertConfigMapName))
			return nil
		}
		log.Info(fmt.Sprintf("Updating KServe custom cert ConfigMap due to changes in the opendatahub global cert ConfigMap (%s)", odhGlobalCACertConfigMapName))
		if err := r.Update(ctx, newCaCertConfigMap); err != nil {
			return err
		}
		if err := r.deleteStorageSecret(ctx, configmap.Namespace); err != nil {
			log.Error(err, "Failed to delete the storage-config secret to update the custom cert")
			return err
		}
		log.V(1).Info("Deleted the storage-config Secret to update the custom cert")
	}

	return nil
}

// This section is intended for regenerating StorageSecret using new data.
func (r *KServeCustomCACertReconciler) deleteStorageSecret(ctx context.Context, namespace string) error {
	foundStorageSecret := &corev1.Secret{}

	err := r.Get(ctx, types.NamespacedName{
		Name:      constants.DefaultStorageConfig,
		Namespace: namespace,
	}, foundStorageSecret)

	if err == nil {
		err = r.Delete(ctx, foundStorageSecret)
		if err != nil {
			return err
		}
	}

	return nil
}

func checkOpenDataHubGlobalCertCAConfigMapName(objectName string) bool {
	return objectName == odhGlobalCACertConfigMapName
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
		GenericFunc: func(e event.GenericEvent) bool {
			objectName := e.Object.GetName()
			return checkOpenDataHubGlobalCertCAConfigMapName(objectName)
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			objectName := e.ObjectNew.GetName()
			return checkOpenDataHubGlobalCertCAConfigMapName(objectName)
		},
	}
}

func (r *KServeCustomCACertReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Initialize logger format
	log := r.Log.WithValues("ConfigMap", req.Name, "namespace", req.Namespace)

	configmap := &corev1.ConfigMap{}
	err := r.Get(ctx, req.NamespacedName, configmap)
	if err != nil && apierrs.IsNotFound(err) {
		log.Info("Opendatahub global cert ConfigMap not found")
		configmap.Namespace = req.Namespace
	} else if err != nil {
		log.Error(err, "Unable to fetch the ConfigMap")
		return ctrl.Result{}, err
	}

	err = r.reconcileConfigMap(configmap, ctx)
	if err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *KServeCustomCACertReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Create a builder that only watch OpenDataHub global certificate ConfigMap
	builder := ctrl.NewControllerManagedBy(mgr).
		For(&corev1.ConfigMap{}).
		WithEventFilter(reconcileOpenDataHubGlobalCACertConfigMap())
	err := builder.Complete(r)
	if err != nil {
		return err
	}
	return nil
}

func getDesiredCaCertConfigMapForKServe(configmapName string, namespace string, caCertData map[string]string) *corev1.ConfigMap {
	desiredConfigMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configmapName,
			Namespace: namespace,
		},
		Data: caCertData,
	}

	return desiredConfigMap
}
