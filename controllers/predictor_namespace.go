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
	"reflect"

	predictorv1 "github.com/kserve/modelmesh-serving/apis/serving/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// NewPredictorNamespace defines the desired Namespace object
func NewPredictorNamespace(predictor *predictorv1.Predictor) *corev1.Namespace {
	return &corev1.Namespace{
		TypeMeta: metav1.TypeMeta{},
		// The name MUST be default, per the maistra docs
		ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: predictor.Namespace, Labels: map[string]string{"predictor-name": predictor.Name, "modelmesh-enabled": "true"}},
		// Spec: corev1.NamespaceSpec{
		// 	corev1.NamespaceSpec.Finalizers{
		// 		[]corev1.FinalizerName{"kubernetes"},
		// 	},
		// },
	}
}

// ComparePredictorNamespaces checks if two Namespaces are equal, if not return false
func ComparePredictorNamespaces(ns1 *corev1.Namespace, ns2 *corev1.Namespace) bool {
	// Two Namespaces will be equal if the labels and spec are identical
	return reflect.DeepEqual(ns1.ObjectMeta.Labels, ns2.ObjectMeta.Labels)
}

// Reconcile will manage the creation, update and deletion of the Namespace returned
// by the newNamespace function
func (r *OpenshiftPredictorReconciler) reconcileNamespace(predictor *predictorv1.Predictor,
	ctx context.Context, newPredictorNamespace func(*predictorv1.Predictor) *corev1.Namespace) error {
	// Initialize logger format
	log := r.Log.WithValues("Predictor", predictor.Name, "namespace", predictor.Namespace)

	// Generate the desired Namespace
	desiredNamespace := newPredictorNamespace(predictor)

	// Create the Namespace if it does not already exist
	foundNamespace := &corev1.Namespace{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      desiredNamespace.Name,
		Namespace: predictor.Namespace,
	}, foundNamespace)
	if err != nil {
		if apierrs.IsNotFound(err) {
			log.Error(err, "Namespace not found...this should NOT happen")
		} else {
			log.Error(err, "Unable to fetch the Namespace")
			return err
		}
	}

	// log.Info("Comparing namespaces")
	// log.Info("NS1:  " + fmt.Sprintf("%s", desiredNamespace.ObjectMeta.Labels))
	// log.Info("NS2:  " + fmt.Sprintf("%s", foundNamespace.ObjectMeta.Labels))

	// Reconcile the Namespace spec if it has been manually modified
	if !ComparePredictorNamespaces(desiredNamespace, foundNamespace) {
		log.Info("Reconciling Namespace")
		// Retry the update operation when the ingress controller eventually
		// updates the resource version field
		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			// Get the last Namespace revision
			if err := r.Get(ctx, types.NamespacedName{
				Name:      desiredNamespace.Name,
				Namespace: predictor.Namespace,
			}, foundNamespace); err != nil {
				log.Error(err, "Unable to reconcile namespace")
				return err
			}
			// Reconcile labels and spec field
			foundNamespace.ObjectMeta.Labels = desiredNamespace.ObjectMeta.Labels
			log.Info("Going to update namespace labels with: " + fmt.Sprintf("%s", foundNamespace.ObjectMeta.Labels))
			// return r.Update(ctx, foundNamespace)
			patch := client.RawPatch(types.MergePatchType, []byte(`{"metadata":{"labels":{"modelmesh-enabled":"true"}}}`))
			return r.Patch(ctx, foundNamespace, patch)
		})
		if err != nil {
			log.Error(err, "Unable to reconcile the Namespace")
			return err
		}
	}

	return nil
}

// ReconcileNamespace will manage the creation, update and deletion of the
// Namespace when the Predictor is reconciled
func (r *OpenshiftPredictorReconciler) ReconcileNamespace(
	predictor *predictorv1.Predictor, ctx context.Context) error {
	return r.reconcileNamespace(predictor, ctx, NewPredictorNamespace)
}
