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
	"os"

	"github.com/go-logr/logr"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type PodReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// SetupWithManager sets up the controller with the Manager.
func (r *PodReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Create a builder that only watch OpenDataHub global certificate ConfigMap
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Pod{}).
		Named("core-pod").
		WithEventFilter(reconcileServingPod()).
		Complete(r)
}

// reconcileOpenDataHubGlobalCertConfigMap filters out all ConfigMaps that are not the OpenDataHub global certificate ConfigMap.
func reconcileServingPod() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			pod := e.Object.(*corev1.Pod)
			return checkPodHasIP(nil, pod)
		},

		UpdateFunc: func(e event.UpdateEvent) bool {
			old := e.ObjectOld.(*corev1.Pod)
			new := e.ObjectNew.(*corev1.Pod)
			return checkPodHasIP(old, new)
		},

		DeleteFunc: func(e event.DeleteEvent) bool {
			return false
		},
	}
}

func checkPodHasIP(oldPod *corev1.Pod, newPod *corev1.Pod) bool {
	if oldPod != nil {
		return !(oldPod.Status.PodIP == newPod.Status.PodIP)
	}

	return newPod.Status.PodIP != ""
}

func (r *PodReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	// Initialize logger format
	logger := log.FromContext(ctx).WithValues("Pod", req.Name, "namespace", req.Namespace)

	controllerNs := os.Getenv("POD_NAMESPACE")

	pod := &corev1.Pod{}
	if err := r.Client.Get(ctx, req.NamespacedName, pod); err != nil {
		if errors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		logger.Error(err, "Failed to get Pod", "pod", req.Name, "namespace", req.Namespace)
		return reconcile.Result{}, err
	}

	err := r.reconcileRayTls(ctx, logger, controllerNs, req.Namespace, pod)
	if err != nil {
		return reconcile.Result{}, err
	}
	return reconcile.Result{}, nil
}

func (r *PodReconciler) reconcileRayTls(ctx context.Context, logger logr.Logger, controllerNamespace, targetNamespace string, pod *corev1.Pod) error {
	// Get the CA certificate secret
	caCertSecret := &corev1.Secret{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: constants.RayCASecretName, Namespace: controllerNamespace}, caCertSecret); err != nil {
		logger.Error(err, "Failed to get ray CA secret", "secret", constants.RayCASecretName, "namespace", controllerNamespace)
		return err
	}

	// Get the default Ray server certificate secret
	defaultRayServerCertSecret := &corev1.Secret{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: constants.RayTLSSecretName, Namespace: targetNamespace}, defaultRayServerCertSecret); err != nil {
		logger.Error(err, "Failed to get ray TLS secret", "secret", constants.RayTLSSecretName, "namespace", targetNamespace)
		return err
	}

	// Add a new certificate to the default Ray server certificate secret
	if err := r.addOrUpdateRayServerCert(ctx, logger, caCertSecret, defaultRayServerCertSecret, pod); err != nil {
		return err
	}

	return nil
}

func (r *PodReconciler) addOrUpdateRayServerCert(ctx context.Context, logger logr.Logger, caCertSecret, rayCertSecret *corev1.Secret, pod *corev1.Pod) error {
	podIP := pod.Status.PodIP
	sanIPs := []string{podIP}
	sanDNSs := []string{"*." + pod.Namespace + ".svc.cluster.local"}

	// Remove redundant certificates from the secret
	updatedCertSecret, err := r.removeRedundantCertsFromSecretData(ctx, logger, rayCertSecret)
	if err != nil {
		logger.Error(err, "Failed to remove redundant certificates", "secret", rayCertSecret.Name, "namespace", rayCertSecret.Namespace)
		return err
	}

	// Generate a new self-signed certificate
	newRayCertSecret, err := utils.GenerateSelfSignedCertificateAsSecret(caCertSecret, rayCertSecret.Name, rayCertSecret.Namespace, sanIPs, sanDNSs)
	if err != nil {
		logger.Error(err, "Failed to generate self-signed certificate", "secret", rayCertSecret.Name, "namespace", rayCertSecret.Namespace)
		return err
	}

	// Combine the new certificate data
	combinedCert := append(newRayCertSecret.Data[corev1.TLSCertKey], newRayCertSecret.Data[corev1.TLSPrivateKeyKey]...)
	updatedCertSecret.Data[podIP] = combinedCert

	// Update the secret using retry to handle conflicts
	return updateSecret(ctx, r.Client, rayCertSecret.Namespace, rayCertSecret.Name, func(secret *corev1.Secret) {
		secret.Data = updatedCertSecret.Data
	})
}

// Helper function to update a Kubernetes secret with retry logic (it solves 'Operation cannot be fulfilled error')
func updateSecret(ctx context.Context, k8sClient client.Client, namespace, name string, updateFn func(*corev1.Secret)) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		secret := &corev1.Secret{}
		if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, secret); err != nil {
			return err
		}

		// Apply updates using the provided function
		updateFn(secret)

		// Attempt to update the secret
		return k8sClient.Update(ctx, secret)
	})
}

func (r *PodReconciler) removeRedundantCertsFromSecretData(ctx context.Context, logger logr.Logger, rayCertSecret *corev1.Secret) (*corev1.Secret, error) {
	updatedRayCertSecret := rayCertSecret.DeepCopy()

	podList := &corev1.PodList{}
	err := r.Client.List(ctx, podList, &client.ListOptions{Namespace: rayCertSecret.Namespace})
	if err != nil {
		logger.Error(err, "failed to list pods in namespace", "namespace", rayCertSecret.Namespace)
		return nil, err
	}

	for key := range updatedRayCertSecret.Data {
		found := false
		for _, pod := range podList.Items {
			if key == pod.Status.PodIP || key == "ca.crt" {
				found = true
				break
			}
		}

		if !found {
			delete(updatedRayCertSecret.Data, key)
		}
	}
	return updatedRayCertSecret, nil
}
