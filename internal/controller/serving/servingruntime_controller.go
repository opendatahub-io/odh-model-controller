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
	"bytes"
	"context"
	"os"
	"reflect"

	"github.com/go-logr/logr"
	servingv1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
	corev1 "k8s.io/api/core/v1"
	k8srbacv1 "k8s.io/api/rbac/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	RoleBindingName       = "prometheus-ns-access"
	OpenshiftMonitoringNS = "openshift-monitoring"
	// PrometheusClusterRole & MonitoringSA specified within odh-manifests
	PrometheusClusterRole = "prometheus-ns-access"
	MonitoringSA          = "prometheus-custom"
	rayCaCertNameInUserNS = "ca.crt"
)

// ServingRuntimeReconciler reconciles a ServingRuntime object. Formerly
// known as MonitoringReconciler.
type ServingRuntimeReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=serving.kserve.io,resources=servingruntimes,verbs=get;list;watch;create;update
// +kubebuilder:rbac:groups=serving.kserve.io,resources=servingruntimes/finalizers,verbs=update

// RoleBindingsAreEqual checks if RoleBinding are equal, if not return false
func RoleBindingsAreEqual(sm1 k8srbacv1.RoleBinding, sm2 k8srbacv1.RoleBinding) bool {
	areEqual :=
		reflect.DeepEqual(sm1.ObjectMeta.Labels, sm2.ObjectMeta.Labels) &&
			reflect.DeepEqual(sm1.Subjects, sm2.Subjects) &&
			reflect.DeepEqual(sm1.RoleRef, sm2.RoleRef)
	return areEqual
}

func buildDesiredRB(rbNS string, monitoringNS string) *k8srbacv1.RoleBinding {
	desiredRB := &k8srbacv1.RoleBinding{}
	desiredRB.ObjectMeta = metav1.ObjectMeta{
		Name:      RoleBindingName,
		Namespace: rbNS,
		Labels:    map[string]string{"opendatahub.io/managed": "true"},
	}
	desiredRB.RoleRef = k8srbacv1.RoleRef{
		APIGroup: "rbac.authorization.k8s.io",
		Kind:     "ClusterRole",
		Name:     PrometheusClusterRole,
	}
	desiredRB.Subjects = []k8srbacv1.Subject{{
		Kind:      "ServiceAccount",
		Name:      MonitoringSA,
		Namespace: monitoringNS,
	}}
	return desiredRB
}

// foundRB stores monitoring rbac in actualRB if it is found in ns namespace
func (r *ServingRuntimeReconciler) foundRB(ctx context.Context, actualRB *k8srbacv1.RoleBinding, ns string) (bool, error) {
	logger := log.FromContext(ctx)
	namespacedName := types.NamespacedName{
		Name:      RoleBindingName,
		Namespace: ns,
	}
	err := r.Client.Get(ctx, namespacedName, actualRB)
	if apierrs.IsNotFound(err) {
		return false, nil
	} else if err != nil {
		logger.Error(err, "Failed to get rolebinding"+RoleBindingName)
		return false, err
	}
	return true, nil
}

// createRBIfDNE will attempt to create desiredRB if it does not exist, or is different from actualRB
func (r *ServingRuntimeReconciler) createRBIfDNE(ctx context.Context, exists bool, desiredRB, actualRB *k8srbacv1.RoleBinding) error {
	logger := log.FromContext(ctx)
	if !exists {
		err := r.Create(ctx, desiredRB)
		if err != nil {
			logger.Error(err, "Failed to create Rolebinding"+RoleBindingName)
			return err
		}
		logger.Info("Created RoleBinding: " + RoleBindingName)
		return nil
	}

	// If it does exist, and it is what we expect, do nothing
	changed := !RoleBindingsAreEqual(*desiredRB, *actualRB)
	if !changed {
		return nil
	}

	// If it does exist but RoleBinding has changed, revert
	err := r.Client.Update(ctx, desiredRB)
	if apierrs.IsConflict(err) {
		// may occur during if the RoleBinding was updated during this reconcile loop
		logger.Error(err, "Failed to create/update RoleBinding: "+RoleBindingName+" due to resource conflict")
		return err
	} else if err != nil {
		logger.Error(err, "Failed to create/update RoleBinding: "+RoleBindingName)
		return err
	}
	logger.Info("Updated RoleBinding: " + RoleBindingName)
	return nil
}

// modelMeshEnabled return true if this Namespace is modelmesh enabled
func (r *ServingRuntimeReconciler) modelMeshEnabled(_ string, labels map[string]string) bool {
	enabled, ok := labels["modelmesh-enabled"]
	if !ok || enabled != "true" {
		return false
	}
	return true
}

// monitoringThisNameSpace return true if this Namespace should be monitored by monitoring stack
func (r *ServingRuntimeReconciler) monitoringThisNameSpace(ns string, labels map[string]string, monitoringNs string) bool {
	if monitoringNs == ""{
		return false
	}
	if ns == OpenshiftMonitoringNS || ns == monitoringNs {
		return true
	}
	return r.modelMeshEnabled(ns, labels)
}

func (r *ServingRuntimeReconciler) reconcileRoleBinding(ctx context.Context, req ctrl.Request, monitoringNs string) error {
	logger := log.FromContext(ctx)

	ns := &corev1.Namespace{}
	err := r.Client.Get(ctx, types.NamespacedName{Name: req.Namespace}, ns)
	if err != nil {
		return err
	}

	monitoringNS := r.monitoringThisNameSpace(req.Namespace, ns.Labels, monitoringNs)

	if !monitoringNS {
		logger.Info("Namespace is not modelmesh enabled, or configured for monitoring, skipping.")
		return nil
	}

	// We are also adding RoleBindings in  OpenShift Monitoring
	// handle this case separately
	if monitoringNS && !r.modelMeshEnabled(req.Namespace, ns.Labels) {
		// Create an RB in OCP monitoring NS for federation
		actualRB := &k8srbacv1.RoleBinding{}
		roleBindingExists, err := r.foundRB(ctx, actualRB, req.Namespace)
		if err != nil {
			return err
		}
		desiredRB := buildDesiredRB(req.Namespace, monitoringNs)
		err = r.createRBIfDNE(ctx, roleBindingExists, desiredRB, actualRB)
		if err != nil {
			return err
		}
		return nil
	}

	// Get ServingRuntimes
	servingRuntimes := &servingv1alpha1.ServingRuntimeList{}
	listOptions := client.ListOptions{
		Namespace: req.Namespace,
	}
	err = r.List(ctx, servingRuntimes, &listOptions)
	noServingRuntimes := len(servingRuntimes.Items) == 0
	if err != nil {
		if apierrs.IsNotFound(err) {
			noServingRuntimes = true
		} else {
			logger.Error(err, "Unable to fetch the ServingRuntimes")
			return err
		}
	}

	// Fetch RoleBinding in this Namespace
	actualRB := &k8srbacv1.RoleBinding{}
	roleBindingExists, err := r.foundRB(ctx, actualRB, req.Namespace)
	if err != nil {
		return err
	}

	// If there are no ServingRuntimes in this NS, remove RB
	if noServingRuntimes {
		if roleBindingExists {
			err := r.Delete(ctx, actualRB)
			if err != nil {
				logger.Error(err, "Failed to delete monitoring Rolebinding"+RoleBindingName)
				return err
			}
			logger.Info("No Serving Runtimes detected in this namespace, deleted monitoring RoleBinding : " + RoleBindingName)
		}
		return nil
	}

	// The RoleBinding we expect to exist in this NS
	desiredRB := buildDesiredRB(req.Namespace, monitoringNs)

	// If it does not exist create it
	err = r.createRBIfDNE(ctx, roleBindingExists, desiredRB, actualRB)
	if err != nil {
		return err
	}

	return nil
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the ServingRuntime object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.1/pkg/reconcile
func (r *ServingRuntimeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Initialize logger format
	logger := log.FromContext(ctx).WithValues("ResourceName", req.Name, "Namespace", req.Namespace)

	controllerNs := os.Getenv("POD_NAMESPACE")
	monitoringNs := os.Getenv("MONITORING_NAMESPACE")
	ns := &corev1.Namespace{}
	namespacedName := types.NamespacedName{
		Name: req.Namespace,
	}
	err := r.Client.Get(ctx, namespacedName, ns)
	if err != nil {
		return ctrl.Result{}, err
	}

	if monitoringNs == "" {
		logger.Info("No monitoring namespace detected, skipping monitoring reconciliation.")
	} else if !isRayTLSSecret(req.Name) {
		logger.Info("Monitoring Controller reconciling.")
		err = r.reconcileRoleBinding(ctx, req, monitoringNs)
		if err != nil {
			return ctrl.Result{}, err
		}
		logger.Info("Monitoring Controller reconciled successfully.")
	}

	var servingRuntimeList servingv1alpha1.ServingRuntimeList
	if err := r.Client.List(ctx, &servingRuntimeList, &client.ListOptions{Namespace: req.Namespace}); err != nil {
		return ctrl.Result{}, err
	}
	multiNodeSRExistInNS := existMultiNodeServingRuntimeInNs(servingRuntimeList)
	logger.Info("Multi Node reconciling.")

	if err = r.reconcileMultiNodeSR(ctx, logger, req.Name, multiNodeSRExistInNS, controllerNs, req.Namespace); err != nil {
		return ctrl.Result{}, err
	}
	logger.Info("Multi Node reconciled successfully.")

	return ctrl.Result{}, nil
}

func (r *ServingRuntimeReconciler) reconcileMultiNodeSR(ctx context.Context, logger logr.Logger, reqName string, multiNodeSRExistInNS bool, controllerNamespace, targetNamespace string) error {
	// recnocile Ray Ca Cert
	if ctx.Err() == nil { // this is for unit test, when context is down, still it tries to create secret even though api server is down
		createCaCertErr := utils.CreateSelfSignedCACertificate(r.Client, constants.RayCASecretName, "", controllerNamespace)
		if createCaCertErr != nil {
			logger.Error(createCaCertErr, "fail to create/update ray ca cert secret", "secret", constants.RayCASecretName, "namespace", controllerNamespace)
			return createCaCertErr
		}
	}
	caCertSecret := &corev1.Secret{}
	err := r.Client.Get(context.TODO(), types.NamespacedName{Name: constants.RayCASecretName, Namespace: controllerNamespace}, caCertSecret)
	if err != nil {
		logger.Error(err, "fail to get ray ca cert secret", "secret", constants.RayCASecretName, "namespace", controllerNamespace)
		return err
	}
	// reconcile a default ray tls secret
	createServerCertErr := r.reconcileDefaultRayServerCertSecretInUserNS(logger, reqName, targetNamespace, caCertSecret, multiNodeSRExistInNS)
	if createServerCertErr != nil {
		return createServerCertErr
	}

	return nil
}

func (r *ServingRuntimeReconciler) reconcileDefaultRayServerCertSecretInUserNS(logger logr.Logger, reqName, targetNamespace string, caCertSecret *corev1.Secret, multiNodeSRExistInNS bool) error {
	rayDefaultSecret := getDesiredRayDefaultSecret(targetNamespace, caCertSecret)

	if multiNodeSRExistInNS {
		// it creates a ray tls secret with ca cert in the user namespace where multinode runtime created.
		defaultCertSecret := &corev1.Secret{}
		if err := r.Client.Get(context.TODO(), types.NamespacedName{Name: constants.RayTLSSecretName, Namespace: targetNamespace}, defaultCertSecret); err != nil {
			if apierrs.IsNotFound(err) {
				if err := r.Client.Create(context.TODO(), rayDefaultSecret); err != nil {
					logger.Error(err, "fail to create ray tls secret", "secret", constants.RayTLSSecretName, "namespace", targetNamespace)
					return err
				}
			}
			logger.Error(err, "fail to get ray tls secret", "secret", constants.RayCASecretName, "namespace", targetNamespace)
			return err
		}

		// If the ca secret is updated, it updates the ca cert in default ray tls secret in the user namespace
		if !bytes.Equal(caCertSecret.Data[corev1.TLSCertKey], defaultCertSecret.Data[rayCaCertNameInUserNS]) {
			updatedDefaultCertSecret := defaultCertSecret.DeepCopy()
			updatedDefaultCertSecret.Data[rayCaCertNameInUserNS] = caCertSecret.Data[corev1.TLSCertKey]
			if err := r.Client.Update(context.TODO(), updatedDefaultCertSecret); err != nil {
				logger.Error(err, "fail to update ray tls secret", "secret", constants.RayCASecretName, "namespace", targetNamespace)
				return err
			}
		}

	} else {
		if !isRayTLSSecret(reqName) {
			if err := r.Client.Delete(context.TODO(), rayDefaultSecret); err != nil {
				if apierrs.IsNotFound(err) {
					return nil
				}
				logger.Error(err, "fail to delete ray tls secret", "secret", constants.RayCASecretName, "namespace", targetNamespace)
				return err
			}
		}
	}

	if err := r.reconcileRayTlsSecretReader(logger, targetNamespace, multiNodeSRExistInNS); err != nil {
		return err
	}
	return nil
}

func getDesiredRayDefaultSecret(namespace string, caSecret *corev1.Secret) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      constants.RayTLSSecretName,
			Namespace: namespace,
			Labels: map[string]string{
				"opendatahub.io/managed":       "true",
				"app.kubernetes.io/name":       "self-signed-ray-cert",
				"app.kubernetes.io/component":  "odh-model-serving",
				"app.kubernetes.io/part-of":    "kserve",
				"app.kubernetes.io/managed-by": "odh-model-controller",
			},
		},
		Data: map[string][]byte{
			rayCaCertNameInUserNS: caSecret.Data[corev1.TLSCertKey],
		},
		Type: corev1.SecretTypeOpaque,
	}
}

// Determine if ServingRuntime matches specific conditions
// TO-DO upstream Kserve 0.15 will have a new API WorkerSpec
// So for now, it will check servingRuntime name, but after we move to 0.15, it needs to check workerSpec is specified or not.(RHOAIENG-16147)
func existMultiNodeServingRuntimeInNs(srList servingv1alpha1.ServingRuntimeList) bool {
	isMultiNodeServingRuntime := false
	for _, sr := range srList.Items {
		if sr.Name == "vllm-multinode-runtime" {
			isMultiNodeServingRuntime = true
		}
	}
	return isMultiNodeServingRuntime
}

func isRayTLSSecret(name string) bool {
	return name == constants.RayCASecretName || name == constants.RayTLSSecretName
}

func (r *ServingRuntimeReconciler) reconcileRayTlsSecretReader(logger logr.Logger, namespace string, multiNodeExist bool) error {
	desiredRole := getDesiredRayTlsSecretReaderRole(namespace)
	desiredRoleBinding := getDesiredRayTlsSecretReaderRB(namespace)
	if multiNodeExist {
		existRole := &k8srbacv1.Role{}
		if err := r.Client.Get(context.TODO(), types.NamespacedName{Name: desiredRole.Name, Namespace: desiredRole.Namespace}, existRole); err != nil {
			if apierrs.IsNotFound(err) {
				if err := r.Client.Create(context.TODO(), desiredRole); err != nil {
					logger.Error(err, "fail to create role", "role", constants.RayTLSSecretReaderRoleName, "namespace", namespace)
					return err
				}
			}
			logger.Error(err, "fail to get role", "role", constants.RayTLSSecretReaderRoleName, "namespace", namespace)
			return err
		}
		if !reflect.DeepEqual(desiredRole.Rules, existRole.Rules) {
			if err := r.Client.Update(context.TODO(), desiredRole); err != nil {
				logger.Error(err, "fail to update role", "role", constants.RayTLSSecretReaderRoleName, "namespace", namespace)
				return err
			}
		}
		existRB := &k8srbacv1.RoleBinding{}
		if err := r.Client.Get(context.TODO(), types.NamespacedName{Name: desiredRoleBinding.Name, Namespace: desiredRoleBinding.Namespace}, existRB); err != nil {
			if apierrs.IsNotFound(err) {
				if err := r.Client.Create(context.TODO(), desiredRoleBinding); err != nil {
					logger.Error(err, "fail to create rolebinding", "rolebinding", constants.RayTLSSecretReaderRoleBindingName, "namespace", namespace)
					return err
				}
			}
			logger.Error(err, "fail to get rolebinding", "rolebinding", constants.RayTLSSecretReaderRoleBindingName, "namespace", namespace)
			return err
		}
		if !(reflect.DeepEqual(desiredRoleBinding.Subjects, existRB.Subjects) && reflect.DeepEqual(desiredRoleBinding.RoleRef, existRB.RoleRef)) {
			if err := r.Client.Update(context.TODO(), desiredRoleBinding); err != nil {
				logger.Error(err, "fail to update rolebinding", "rolebinding", constants.RayTLSSecretReaderRoleBindingName, "namespace", namespace)
				return err
			}
		}
	} else {
		if err := r.Client.Delete(context.TODO(), desiredRoleBinding); err != nil {
			if apierrs.IsNotFound(err) {
				return nil
			}
			logger.Error(err, "fail to delete rolebinding", "rolebinding", constants.RayTLSSecretReaderRoleBindingName, "namespace", namespace)
			return err
		}
		if err := r.Client.Delete(context.TODO(), desiredRole); err != nil {
			if apierrs.IsNotFound(err) {
				return nil
			}
			logger.Error(err, "fail to delete rolebinding", "role", constants.RayTLSSecretReaderRoleName, "namespace", namespace)
			return err
		}
	}
	return nil
}
func getDesiredRayTlsSecretReaderRole(namespace string) *k8srbacv1.Role {
	return &k8srbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      constants.RayTLSSecretReaderRoleName,
			Namespace: namespace,
			Labels: map[string]string{
				"opendatahub.io/managed":       "true",
				"app.kubernetes.io/name":       "ray-tls-secret-reader",
				"app.kubernetes.io/component":  "odh-model-serving",
				"app.kubernetes.io/part-of":    "kserve",
				"app.kubernetes.io/managed-by": "odh-model-controller",
			},
		},
		Rules: []k8srbacv1.PolicyRule{
			{
				APIGroups:     []string{""},
				Resources:     []string{"secrets"},
				Verbs:         []string{"get"},
				ResourceNames: []string{constants.RayTLSSecretName},
			},
		},
	}
}

func getDesiredRayTlsSecretReaderRB(namespace string) *k8srbacv1.RoleBinding {
	return &k8srbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      constants.RayTLSSecretReaderRoleBindingName,
			Namespace: namespace,
			Labels: map[string]string{
				"opendatahub.io/managed":       "true",
				"app.kubernetes.io/name":       "ray-tls-secret-reader",
				"app.kubernetes.io/component":  "odh-model-serving",
				"app.kubernetes.io/part-of":    "kserve",
				"app.kubernetes.io/managed-by": "odh-model-controller",
			},
		},
		Subjects: []k8srbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      "default",
				Namespace: namespace,
			},
		},
		RoleRef: k8srbacv1.RoleRef{
			Kind:     "Role",
			Name:     constants.RayTLSSecretReaderRoleName,
			APIGroup: "rbac.authorization.k8s.io",
		},
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *ServingRuntimeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&servingv1alpha1.ServingRuntime{}).
		Named("servingruntime").
		Watches(
			&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, o client.Object) []reconcile.Request {
				if !isRayTLSSecret(o.GetName()) {
					return []reconcile.Request{}
				}

				return []reconcile.Request{
					{NamespacedName: client.ObjectKey{
						Name:      o.GetName(),
						Namespace: o.GetNamespace(),
					}},
				}
			}),
		).
		// Watch for changes to ModelMesh Enabled namespaces & a select few others
		Watches(&corev1.Namespace{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, o client.Object) []reconcile.Request {
				monitoringNs := os.Getenv("MONITORING_NAMESPACE")
				if !r.monitoringThisNameSpace(o.GetName(), o.GetLabels(), monitoringNs) {
					return []reconcile.Request{}
				}

				namespacedName := types.NamespacedName{
					Name:      o.GetName(),
					Namespace: o.GetNamespace(),
				}
				reconcileRequests := append([]reconcile.Request{}, reconcile.Request{NamespacedName: namespacedName})
				return reconcileRequests
			})).
		// Watch for RoleBinding in modelmesh enabled namespaces & a select few others
		Watches(&k8srbacv1.RoleBinding{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, o client.Object) []reconcile.Request {
				logger := log.FromContext(ctx)
				// Only reconcile on RoleBindings that this controller creates.
				// We avoid using owner references, as there is no logical owner
				// of the RoleBinding this controller creates.
				if o.GetName() != RoleBindingName {
					return []reconcile.Request{}
				}
				_, odhManaged := o.GetLabels()["opendatahub.io/managed"]
				if !odhManaged {
					return []reconcile.Request{}
				}
				logger.Info("Reconcile event triggered by Rolebinding: " + o.GetName())

				namespacedName := types.NamespacedName{
					Name:      o.GetName(),
					Namespace: o.GetNamespace(),
				}

				reconcileRequests := append([]reconcile.Request{}, reconcile.Request{NamespacedName: namespacedName})

				return reconcileRequests
			})).
		Complete(r)
}
