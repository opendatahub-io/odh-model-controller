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
	"reflect"

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	servingv1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
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
)

// ServingRuntimeReconciler reconciles a ServingRuntime object. Formerly
// known as MonitoringReconciler.
type ServingRuntimeReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	MonitoringNS string
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
func (r *ServingRuntimeReconciler) monitoringThisNameSpace(ns string, labels map[string]string) bool {
	if ns == OpenshiftMonitoringNS || ns == r.MonitoringNS {
		return true
	}
	return r.modelMeshEnabled(ns, labels)
}

func (r *ServingRuntimeReconciler) reconcileRoleBinding(ctx context.Context, req ctrl.Request) error {
	logger := log.FromContext(ctx)

	ns := &corev1.Namespace{}
	err := r.Client.Get(ctx, types.NamespacedName{Name: req.Namespace}, ns)
	if err != nil {
		return err
	}

	monitoringNS := r.monitoringThisNameSpace(req.Namespace, ns.Labels)

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
		desiredRB := buildDesiredRB(req.Namespace, r.MonitoringNS)
		err = r.createRBIfDNE(ctx, roleBindingExists, desiredRB, actualRB)
		if err != nil {
			return err
		}
		return nil
	}

	// Get ServingRuntimes
	servingRuntimes := &kservev1alpha1.ServingRuntimeList{}
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
	desiredRB := buildDesiredRB(req.Namespace, r.MonitoringNS)

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
	_ = log.FromContext(ctx)
	// Initialize logger format
	logger := log.FromContext(ctx).WithValues("ResourceName", req.Name, "Namespace", req.Namespace)
	ctx = log.IntoContext(ctx, logger)
	if r.MonitoringNS == "" {
		logger.Info("No monitoring namespace detected, skipping monitoring reconciliation.")
		return ctrl.Result{}, nil
	}

	ns := &corev1.Namespace{}
	namespacedName := types.NamespacedName{
		Name: req.Namespace,
	}
	err := r.Client.Get(ctx, namespacedName, ns)
	if err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("Monitoring Controller reconciling.")
	err = r.reconcileRoleBinding(ctx, req)
	if err != nil {
		return ctrl.Result{}, err
	}
	logger.Info("Monitoring Controller reconciled successfully.")
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ServingRuntimeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&servingv1alpha1.ServingRuntime{}).
		Named("servingruntime").
		// Watch for changes to ModelMesh Enabled namespaces & a select few others
		Watches(&corev1.Namespace{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, o client.Object) []reconcile.Request {
				if !r.monitoringThisNameSpace(o.GetName(), o.GetLabels()) {
					return []reconcile.Request{}
				}

				namespacedName := types.NamespacedName{
					Name:      o.GetName(),
					Namespace: o.GetName(),
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
