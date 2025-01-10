package core

import (
	"context"
	"os"

	"github.com/go-logr/logr"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
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
	if err := r.Client.Get(context.Background(), req.NamespacedName, pod); err != nil {
		logger.Error(err, "Failed to get Pod", "pod", req.Name, "namespace", req.Namespace)
		return reconcile.Result{}, err
	}

	err := r.reconcileRayTls(logger, controllerNs, req.Namespace, pod)
	if err != nil {
		return reconcile.Result{}, err
	}
	return reconcile.Result{}, nil
}

func (r *PodReconciler) reconcileRayTls(logger logr.Logger, controllerNamespace, targetNamespace string, pod *corev1.Pod) error {
	caCertSecret := &corev1.Secret{}
	err := r.Client.Get(context.TODO(), types.NamespacedName{Name: constants.RayCASecretName, Namespace: controllerNamespace}, caCertSecret)
	if err != nil {
		logger.Error(err, "Failed to get ray ca secret", "sercret", constants.RayCASecretName, "namespace", controllerNamespace)
		return err
	}

	defaultRayServerCertSecret := &corev1.Secret{}
	err = r.Client.Get(context.TODO(), types.NamespacedName{Name: constants.RayTLSSecretName, Namespace: targetNamespace}, defaultRayServerCertSecret)
	if err != nil {
		logger.Error(err, "Failed to get ray tls secret", "sercret", constants.RayTLSSecretName, "namespace", targetNamespace)
		return err
	}
	// add a new cert to default Ray server cert Secret.
	createServerCertErr := r.addNewCertToDefaultRayServerCertSecretInUserNS(logger, caCertSecret, defaultRayServerCertSecret, pod)
	if createServerCertErr != nil {
		return createServerCertErr
	}

	return nil
}

func (r *PodReconciler) addNewCertToDefaultRayServerCertSecretInUserNS(logger logr.Logger, caCertSecret, rayCertSecret *corev1.Secret, pod *corev1.Pod) error {
	podIP := pod.Status.PodIP
	sanIPs := []string{podIP}
	sanDNSs := []string{"*." + pod.Namespace + ".svc.cluster.local"}

	updatedCertSecret, err := r.removeRedundantCertsFromSecretData(logger, rayCertSecret)
	if err != nil {
		return err
	}

	newRayCertSecret, err := utils.GenerateSelfSignedCertificateAsSecret(caCertSecret, rayCertSecret.Name, rayCertSecret.Namespace, sanIPs, sanDNSs)
	if err != nil {
		return err
	}
	combinedCert := append(newRayCertSecret.Data[corev1.TLSCertKey], newRayCertSecret.Data[corev1.TLSPrivateKeyKey]...)
	updatedCertSecret.Data[podIP] = combinedCert

	if err := r.Client.Update(context.TODO(), updatedCertSecret); err != nil {
		return err
	}

	return nil

}

func (r *PodReconciler) removeRedundantCertsFromSecretData(logger logr.Logger, rayCertSecret *corev1.Secret) (*corev1.Secret, error) {
	updatedRayCertSecret := rayCertSecret.DeepCopy()

	podList := &corev1.PodList{}
	err := r.Client.List(context.TODO(), podList, &client.ListOptions{Namespace: rayCertSecret.Namespace})
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
