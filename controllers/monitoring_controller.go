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
	"github.com/go-logr/logr"
	mmv1alpha1 "github.com/kserve/modelmesh-serving/apis/serving/v1alpha1"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"reflect"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
	"sort"
)

const serviceMonitorName = "modelmesh-metrics-monitor"

type MonitoringReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	Log          logr.Logger
	MonitoringNS string
}

// ServiceMonitorsAreEqual checks if Service Monitors are equal, if not return false
func ServiceMonitorsAreEqual(sm1 monitoringv1.ServiceMonitor, sm2 monitoringv1.ServiceMonitor) bool {
	areEqual :=
		reflect.DeepEqual(sm1.ObjectMeta.Labels, sm2.ObjectMeta.Labels) &&
			reflect.DeepEqual(sm1.Spec.NamespaceSelector, sm2.Spec.NamespaceSelector) &&
			reflect.DeepEqual(sm1.Spec.Endpoints, sm2.Spec.Endpoints) &&
			reflect.DeepEqual(sm1.Spec.Selector, sm2.Spec.Selector)
	return areEqual
}

func ContainsString(s []string, e string) bool {
	for _, a := range s {
		if a == e {
			return true
		}
	}
	return false
}

// FetchServingRuntimeNamespaces returns a list of all namespaces that contain ServingRuntimes
func FetchServingRuntimeNamespaces(r *MonitoringReconciler, ctx context.Context) ([]string, error) {
	foundServingRuntimes := &mmv1alpha1.ServingRuntimeList{}
	var namespaces []string

	if err := r.List(ctx, foundServingRuntimes); err != nil {
		return namespaces, err
	}
	for _, predictors := range foundServingRuntimes.Items {
		if !ContainsString(namespaces, predictors.Namespace) {
			namespaces = append(namespaces, predictors.Namespace)
		}
	}
	return namespaces, nil
}

// Reconcile will manage the creation, update and deletion of the ModelMesh monitoring resources
func (r *MonitoringReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Initialize logger format
	log := r.Log.WithValues("ResourceName", req.Name, "Namespace", req.Namespace)

	if r.MonitoringNS == "" {
		log.Info("No monitoring namespace detected, skipping creation/update of ServiceMonitor.")
		return ctrl.Result{}, nil
	}
	log.Info("Monitoring Controller reconciling.")

	// Retrieve the current service monitor
	actualServiceMonitor := &monitoringv1.ServiceMonitor{}
	namespacedName := types.NamespacedName{
		Name:      serviceMonitorName,
		Namespace: r.MonitoringNS,
	}

	err := r.Client.Get(ctx, namespacedName, actualServiceMonitor)

	exists := true
	if apierrs.IsNotFound(err) {
		exists = false
	} else if err != nil {
		log.Error(err, "Unable to get service monitor "+serviceMonitorName)
		return ctrl.Result{}, err
	}

	namespaces, err := FetchServingRuntimeNamespaces(r, ctx)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Service Monitor will always select for monitoring Namespace
	namespaces = append(namespaces, r.MonitoringNS)

	// Ensure the list is deterministic
	sort.Strings(namespaces)

	desiredServiceMonitor := &monitoringv1.ServiceMonitor{}
	desiredServiceMonitor.ObjectMeta = actualServiceMonitor.ObjectMeta
	desiredServiceMonitor.ObjectMeta.Labels = map[string]string{
		"modelmesh-service": "modelmesh-serving",
	}
	desiredServiceMonitor.Spec = monitoringv1.ServiceMonitorSpec{
		Selector: metav1.LabelSelector{
			MatchLabels: map[string]string{
				"modelmesh-service": "modelmesh-serving",
			},
		},
		NamespaceSelector: monitoringv1.NamespaceSelector{
			MatchNames: namespaces,
		},
		Endpoints: []monitoringv1.Endpoint{{
			Interval: "30s",
			Port:     "prometheus",
			Scheme:   "https",
			TLSConfig: &monitoringv1.TLSConfig{
				SafeTLSConfig: monitoringv1.SafeTLSConfig{
					InsecureSkipVerify: true,
				},
			}},
		},
	}

	changed := !ServiceMonitorsAreEqual(*desiredServiceMonitor, *actualServiceMonitor)

	// Create the ServiceMonitor if not found
	if !exists {
		if err := r.Client.Create(ctx, desiredServiceMonitor); err != nil {
			log.Error(err, "Failed to create service monitor "+serviceMonitorName)
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// If service monitor exists, but we detect no change, do nothing
	if !changed {
		return ctrl.Result{}, nil
	}

	err = r.Client.Update(ctx, desiredServiceMonitor)

	if apierrs.IsConflict(err) {
		// this can occur during normal operations if the Service Monitor was updated during this reconcile loop
		log.Error(err, "Could not create/update service monitor: "+serviceMonitorName+" due to resource conflict")
		return ctrl.Result{}, err
	} else if err != nil {
		log.Error(err, "Could not create/update service monitor: "+serviceMonitorName)
		return ctrl.Result{}, err
	} else {
		log.Info("Created/updated ServiceMonitor: " + serviceMonitorName)
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *MonitoringReconciler) SetupWithManager(mgr ctrl.Manager) error {
	builder := ctrl.NewControllerManagedBy(mgr).
		For(&mmv1alpha1.ServingRuntime{}).
		// We only want to watch the ServiceMonitor in the designated monitoring namespace
		Watches(&source.Kind{Type: &monitoringv1.ServiceMonitor{}},
			handler.EnqueueRequestsFromMapFunc(func(o client.Object) []reconcile.Request {
				if o.GetNamespace() != r.MonitoringNS || o.GetName() != serviceMonitorName {
					return []reconcile.Request{}
				}
				r.Log.Info("Reconcile event triggered by service monitor: " + o.GetName())

				namespacedName := types.NamespacedName{
					Name:      o.GetName(),
					Namespace: o.GetNamespace(),
				}

				reconcileRequests := append([]reconcile.Request{}, reconcile.Request{NamespacedName: namespacedName})

				return reconcileRequests
			}))
	err := builder.Complete(r)
	if err != nil {
		return err
	}
	return nil
}
