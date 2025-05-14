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
	"encoding/json"
	"reflect"

	"github.com/go-logr/logr"

	corev1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
)

const (
	storageSecretName = constants.DefaultStorageConfig
)

// SecretReconciler reconciles a Secret object. Formerly
// known as StorageSecretReconciler
type SecretReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update;patch;delete

// newStorageSecret takes a list of data connection secrets and generates a single storage config secret
// https://github.com/kserve/modelmesh-serving/blob/main/docs/predictors/setup-storage.md
func newStorageSecret(dataConnectionSecretsList *corev1.SecretList, odhCustomCertData string) *corev1.Secret {
	desiredSecret := &corev1.Secret{}
	desiredSecret.Data = map[string][]byte{}
	dataConnectionElement := map[string]string{}
	storageByteData := map[string][]byte{}
	for _, secret := range dataConnectionSecretsList.Items {
		dataConnectionElement["type"] = secret.Annotations["opendatahub.io/connection-type"]
		dataConnectionElement["access_key_id"] = string(secret.Data["AWS_ACCESS_KEY_ID"])
		dataConnectionElement["secret_access_key"] = string(secret.Data["AWS_SECRET_ACCESS_KEY"])
		dataConnectionElement["endpoint_url"] = string(secret.Data["AWS_S3_ENDPOINT"])
		// We add both "default_bucket" and "bucket" because kserve and modelmesh expect different keys for the same value
		// Once upstream has reconciled to one common key, we should remove the other one.
		dataConnectionElement["default_bucket"] = string(secret.Data["AWS_S3_BUCKET"])
		dataConnectionElement["bucket"] = string(secret.Data["AWS_S3_BUCKET"])
		dataConnectionElement["region"] = string(secret.Data["AWS_DEFAULT_REGION"])

		if odhCustomCertData != "" {
			dataConnectionElement["certificate"] = odhCustomCertData
			dataConnectionElement["cabundle_configmap"] = constants.KServeCACertConfigMapName
		}
		jsonBytes, _ := json.Marshal(dataConnectionElement)
		storageByteData[secret.Name] = jsonBytes
	}
	desiredSecret.Data = storageByteData
	return desiredSecret
}

// CompareStorageSecrets checks if two secrets are equal, if not return false
func CompareStorageSecrets(s1 corev1.Secret, s2 corev1.Secret) bool {
	return reflect.DeepEqual(s1.ObjectMeta.Labels, s2.ObjectMeta.Labels) && reflect.DeepEqual(s1.Data, s2.Data)
}

// reconcileSecret grabs all data connection secrets in the triggering namespace and
// creates/updates the storage config secret
func (r *SecretReconciler) reconcileSecret(secret *corev1.Secret,
	ctx context.Context, newStorageSecret func(dataConnectionSecretsList *corev1.SecretList, odhCustomCertData string) *corev1.Secret) error {
	// Initialize logger format
	logger := log.FromContext(ctx)

	// Grab all data connections in the namespace
	dataConnectionSecretsList := &corev1.SecretList{}
	opts := []client.ListOption{
		client.InNamespace(secret.Namespace),
		client.MatchingLabels{"opendatahub.io/managed": "true", "opendatahub.io/dashboard": "true"},
	}
	err := r.List(ctx, dataConnectionSecretsList, opts...)
	if err != nil {
		return err
	}
	if len(dataConnectionSecretsList.Items) == 0 {
		logger.Info("No data connections found in namespace")
		if err := r.deleteStorageSecret(ctx, secret.Namespace, logger); err != nil {
			logger.Error(err, "Failed to delete the storage-config secret")
			return err
		}
		return nil
	}

	odhCustomCertData := ""
	odhGlobalCertConfigMap := &corev1.ConfigMap{}
	err = r.Get(ctx, types.NamespacedName{
		Name:      constants.KServeCACertConfigMapName,
		Namespace: secret.Namespace,
	}, odhGlobalCertConfigMap)

	if err != nil {
		if apierrs.IsNotFound(err) {
			logger.Info("unable to fetch the ODH Global Cert ConfigMap", "error", err)
		} else {
			return err
		}
	} else {
		odhCustomCertData = odhGlobalCertConfigMap.Data[constants.KServeCACertFileName]
	}

	// Generate desire Storage Config Secret
	desiredStorageSecret := newStorageSecret(dataConnectionSecretsList, odhCustomCertData)
	desiredStorageSecret.Name = storageSecretName
	desiredStorageSecret.Namespace = secret.Namespace
	desiredStorageSecret.Labels = map[string]string{}
	desiredStorageSecret.Labels["opendatahub.io/managed"] = "true"

	foundStorageSecret := &corev1.Secret{}
	justCreated := false
	err = r.Get(ctx, types.NamespacedName{
		Name:      desiredStorageSecret.Name,
		Namespace: secret.Namespace,
	}, foundStorageSecret)
	if err != nil {
		if apierrs.IsNotFound(err) {
			logger.Info("Creating Storage Config Secret")

			// Create the storage config secret if it doesn't already exist
			err = r.Create(ctx, desiredStorageSecret)
			if err != nil && !apierrs.IsAlreadyExists(err) {
				logger.Error(err, "Unable to create the Storage Config Secret")
				return err
			}
			justCreated = true
		} else {
			logger.Error(err, "Unable to fetch the Storage Config Secret")
			return err
		}
	}

	// Reconcile the Storage Config Secret if it has been manually modified
	if !justCreated && !CompareStorageSecrets(*desiredStorageSecret, *foundStorageSecret) {
		logger.Info("Reconciling Storage Config Secret")

		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			// Get the last Storage Config revision
			if err := r.Get(ctx, types.NamespacedName{
				Name:      desiredStorageSecret.Name,
				Namespace: secret.Namespace,
			}, foundStorageSecret); err != nil {
				return err
			}
			// Reconcile labels and spec field
			foundStorageSecret.Data = desiredStorageSecret.Data
			foundStorageSecret.ObjectMeta.Labels = desiredStorageSecret.ObjectMeta.Labels
			return r.Update(ctx, foundStorageSecret)
		})
		if err != nil {
			logger.Error(err, "Unable to reconcile the Storage Config Secret")
			return err
		}
	}

	return nil
}

// ReconcileStorageSecret will manage the creation, update and deletion of the Storage Config Secret
func (r *SecretReconciler) ReconcileStorageSecret(
	secret *corev1.Secret, ctx context.Context) error {
	return r.reconcileSecret(secret, ctx, newStorageSecret)
}

func checkOpenDataHubLabel(labels map[string]string) bool {
	return labels["opendatahub.io/managed"] == "true"
}

// reconcileOpenDataHubSecrets will filter out all secrets that are not managed by the ODH
func reconcileOpenDataHubSecrets() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			objectLabels := e.Object.GetLabels()
			return checkOpenDataHubLabel(objectLabels) && !utils.IsRayTLSSecret(e.Object.GetName())
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			objectLabels := e.Object.GetLabels()
			return checkOpenDataHubLabel(objectLabels) && !utils.IsRayTLSSecret(e.Object.GetName())
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			objectNewLabels := e.ObjectNew.GetLabels()
			return checkOpenDataHubLabel(objectNewLabels) && !utils.IsRayTLSSecret(e.ObjectNew.GetName())
		},
	}
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Secret object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.1/pkg/reconcile
func (r *SecretReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Initialize logger format
	logger := log.FromContext(ctx).WithValues("Secret", req.Name, "namespace", req.Namespace)
	ctx = log.IntoContext(ctx, logger)

	secret := &corev1.Secret{}
	err := r.Get(ctx, req.NamespacedName, secret)
	if err != nil && apierrs.IsNotFound(err) {
		logger.Info("Data Connection not found")
		secret.Namespace = req.Namespace
	} else if err != nil {
		logger.Error(err, "Unable to fetch the secret")
		return ctrl.Result{}, err
	}

	err = r.ReconcileStorageSecret(secret, ctx)
	if err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SecretReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Secret{}).
		Named("secret").
		WithEventFilter(reconcileOpenDataHubSecrets()).
		Complete(r)
}

func (r *SecretReconciler) deleteStorageSecret(ctx context.Context, namespace string, log logr.Logger) error {
	foundStorageSecret := &corev1.Secret{}

	err := r.Get(ctx, types.NamespacedName{
		Name:      constants.DefaultStorageConfig,
		Namespace: namespace,
	}, foundStorageSecret)

	if err == nil && foundStorageSecret.Labels["opendatahub.io/managed"] == "true" {
		log.Info("Deleting StorageSecret")
		err = r.Delete(ctx, foundStorageSecret)
		if err != nil {
			return err
		}
	}

	return nil
}
