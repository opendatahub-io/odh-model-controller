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

	predictorv1 "github.com/kserve/modelmesh-serving/apis/serving/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
)

const (
	requiredNamespaceLabel      = "modelmesh-enabled"
	requiredNamespaceLabelValue = "true"
)

// NewPredictorNamespace defines the desired Namespace object
func NewPredictorNamespace(predictor *predictorv1.Predictor) *corev1.Namespace {
	return &corev1.Namespace{
		TypeMeta:   metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{Name: predictor.Namespace, Namespace: predictor.Namespace, Labels: map[string]string{"predictor-name": predictor.Name, "modelmesh-enabled": "true"}},
	}
}

// Make sure our desired label/value is there
func CheckForNamespaceLabel(searchlabel string, searchValue string, ns *corev1.Namespace) bool {
	if value, found := ns.ObjectMeta.Labels[searchlabel]; found {
		if value == searchValue {
			return true
		} else {
			return false
		}
	} else {
		return false
	}
}

// Reconcile will manage the creation, update and deletion of the Namespace returned
// by the newNamespace function
func (r *OpenshiftPredictorReconciler) reconcileNamespace(predictor *predictorv1.Predictor,
	ctx context.Context, newPredictorNamespace func(*predictorv1.Predictor) *corev1.Namespace) error {
	// Initialize logger format
	log := r.Log.WithValues("Predictor", predictor.Name, "namespace", predictor.Namespace)

	// Create the Namespace if it does not already exist
	foundNamespace := &corev1.Namespace{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      predictor.Namespace,
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

	// Reconcile the Namespace spec if it has been manually modified
	if !CheckForNamespaceLabel(requiredNamespaceLabel, requiredNamespaceLabelValue, foundNamespace) {
		// Retry the update operation when the ingress controller eventually
		// updates the resource version field
		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			// Get the last Namespace revision
			if err := r.Get(ctx, types.NamespacedName{
				Name:      predictor.Namespace,
				Namespace: predictor.Namespace,
			}, foundNamespace); err != nil {
				log.Error(err, "Unable to reconcile namespace")
				return err
			}
			// Reconcile labels and spec field
			foundNamespace.ObjectMeta.Labels[requiredNamespaceLabel] = requiredNamespaceLabelValue
			return r.Update(ctx, foundNamespace)
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
