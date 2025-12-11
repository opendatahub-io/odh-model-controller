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
	"fmt"
	"os"
	"time"

	"github.com/go-logr/logr"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
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
			return checkMultiNodePod(pod) && checkPodHasIP(nil, pod)
		},

		UpdateFunc: func(e event.UpdateEvent) bool {
			old := e.ObjectOld.(*corev1.Pod)
			new := e.ObjectNew.(*corev1.Pod)
			return checkMultiNodePod(new) && checkPodHasIP(old, new)
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

func checkMultiNodePod(pod *corev1.Pod) bool {
	for _, container := range pod.Spec.Containers {
		// Only check RAY_USE_TLS in kserve-container or worker-container
		if container.Name == "kserve-container" || container.Name == constants.WorkerContainerName {
			for _, env := range container.Env {
				if env.Name == constants.RayUseTlsEnvName && env.Value != "0" {
					return true
				}
			}
		}
	}

	return false
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

	// Add a new certificate to the default Ray server certificate secret
	if err := updateSecret(ctx, r.Client, logger, caCertSecret, targetNamespace, pod); err != nil {
		return err
	}

	return nil
}

// Helper function to update a Kubernetes secret with retry logic (it solves 'Operation cannot be fulfilled error')
func updateSecret(ctx context.Context, k8sClient client.Client, logger logr.Logger, caCertSecret *corev1.Secret, targetNamespace string, pod *corev1.Pod) error {

	backoff := wait.Backoff{
		Steps:    5,               // t retries. return error after t retries.
		Duration: 1 * time.Second, // first retry after 1 sec
		Factor:   2.0,             // from second retry, it will wait double
	}
	return retry.RetryOnConflict(backoff, func() error {
		updatedSecret, err := addOrUpdateRayServerCert(ctx, k8sClient, logger, caCertSecret, targetNamespace, pod)
		if err != nil {
			return err
		}
		currentSecret := &corev1.Secret{}
		if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: updatedSecret.Namespace, Name: updatedSecret.Name}, currentSecret); err != nil {
			return err
		}
		// check if secret is already updated or not.
		if currentSecret.ResourceVersion != updatedSecret.ResourceVersion {
			return errors.NewConflict(schema.GroupResource{Group: "v1", Resource: "secrets"}, updatedSecret.Name, fmt.Errorf("resource version changed"))
		}

		// Attempt to update the secret
		currentSecret.Data = updatedSecret.Data
		return k8sClient.Update(ctx, currentSecret)
	})
}

func addOrUpdateRayServerCert(ctx context.Context, k8sClient client.Client, logger logr.Logger, caCertSecret *corev1.Secret, targetNamespace string, pod *corev1.Pod) (*corev1.Secret, error) {
	podIP := pod.Status.PodIP
	sanIPs := []string{podIP}
	sanDNSs := []string{"*." + pod.Namespace + ".svc.cluster.local"}

	// Get the default Ray server certificate secret
	rayCertSecret := &corev1.Secret{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: constants.RayTLSSecretName, Namespace: targetNamespace}, rayCertSecret); err != nil {
		logger.Error(err, "Failed to get ray TLS secret", "secret", constants.RayTLSSecretName, "namespace", targetNamespace)
		return nil, err
	}

	// Remove redundant certificates from the secret
	updatedCertSecret, err := removeRedundantCertsFromSecretData(ctx, k8sClient, logger, rayCertSecret)
	if err != nil {
		logger.Error(err, "Failed to remove redundant certificates", "secret", rayCertSecret.Name, "namespace", rayCertSecret.Namespace)
		return nil, err
	}

	// Generate a new self-signed certificate
	newRayCertSecret, err := utils.GenerateSelfSignedCertificateAsSecret(caCertSecret, rayCertSecret.Name, rayCertSecret.Namespace, sanIPs, sanDNSs)
	if err != nil {
		logger.Error(err, "Failed to generate self-signed certificate", "secret", rayCertSecret.Name, "namespace", rayCertSecret.Namespace)
		return nil, err
	}

	// Combine the new certificate data
	combinedCert := append(newRayCertSecret.Data[corev1.TLSCertKey], newRayCertSecret.Data[corev1.TLSPrivateKeyKey]...)
	updatedCertSecret.Data[podIP] = combinedCert

	// To use Optimistic Locking
	updatedCertSecret.SetResourceVersion(rayCertSecret.ResourceVersion)

	return updatedCertSecret, nil
}

func removeRedundantCertsFromSecretData(ctx context.Context, k8sClient client.Client, logger logr.Logger, rayCertSecret *corev1.Secret) (*corev1.Secret, error) {
	updatedRayCertSecret := rayCertSecret.DeepCopy()

	podList := &corev1.PodList{}
	err := k8sClient.List(ctx, podList, &client.ListOptions{Namespace: rayCertSecret.Namespace, LabelSelector: labels.SelectorFromSet(labels.Set{"component": "predictor"})})
	if err != nil {
		logger.Error(err, "failed to list pods in namespace", "namespace", rayCertSecret.Namespace)
		return nil, err
	}

	existingPodIPs := make(map[string]struct{})
	for _, pod := range podList.Items {
		existingPodIPs[pod.Status.PodIP] = struct{}{}
	}

	for key := range updatedRayCertSecret.Data {
		if key == "ca.crt" {
			continue
		}

		if len(podList.Items) == 0 || !contains(existingPodIPs, key) {
			delete(updatedRayCertSecret.Data, key)
		}
	}

	return updatedRayCertSecret, nil
}

func contains(podIPs map[string]struct{}, key string) bool {
	_, exists := podIPs[key]
	return exists
}
