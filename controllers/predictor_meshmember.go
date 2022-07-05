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
	"reflect"

	predictorv1 "github.com/kserve/modelmesh-serving/apis/serving/v1alpha1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	maistrav1 "maistra.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

// NewPredictorMeshMember defines the desired MeshMember object
func NewPredictorMeshMember(predictor *predictorv1.Predictor) *maistrav1.ServiceMeshMember {
	return &maistrav1.ServiceMeshMember{
		TypeMeta: metav1.TypeMeta{},
		// The name MUST be default, per the maistra docs
		ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: predictor.Namespace, Labels: map[string]string{"predictor-name": predictor.Name}},
		Spec: maistrav1.ServiceMeshMemberSpec{
			ControlPlaneRef: maistrav1.ServiceMeshControlPlaneRef{
				Name:      "odh",
				Namespace: "istio-system",
			},
		},
	}
}

// ComparePredictorMeshMembers checks if two MeshMembers are equal, if not return false
func ComparePredictorMeshMembers(mm1 *maistrav1.ServiceMeshMember, mm2 *maistrav1.ServiceMeshMember) bool {
	// Two MeshMembers will be equal if the labels and spec are identical
	return reflect.DeepEqual(mm1.ObjectMeta.Labels, mm2.ObjectMeta.Labels)
}

// Reconcile will manage the creation, update and deletion of the MeshMember returned
// by the newMeshMember function
func (r *OpenshiftPredictorReconciler) reconcileMeshMember(predictor *predictorv1.Predictor,
	ctx context.Context, newMeshMember func(*predictorv1.Predictor) *maistrav1.ServiceMeshMember) error {
	// Initialize logger format
	log := r.Log.WithValues("Predictor", predictor.Name, "namespace", predictor.Namespace)

	// Generate the desired ServiceMeshMember
	desiredMeshMember := newMeshMember(predictor)

	// Create the ServiceMeshMember if it does not already exist
	foundMeshMember := &maistrav1.ServiceMeshMember{}
	justCreated := false
	err := r.Get(ctx, types.NamespacedName{
		Name:      desiredMeshMember.Name,
		Namespace: predictor.Namespace,
	}, foundMeshMember)
	if err != nil {
		if apierrs.IsNotFound(err) {
			log.Info("Creating ServiceMeshMember")
			// Add .metatada.ownerReferences to the MeshMember to be deleted by the
			// Kubernetes garbage collector if the Predictor is deleted
			err = ctrl.SetControllerReference(predictor, desiredMeshMember, r.Scheme)
			if err != nil {
				log.Error(err, "Unable to add OwnerReference to the MeshMember")
				return err
			}
			// Create the ServiceMeshMember in the Openshift cluster
			err = r.Create(ctx, desiredMeshMember)
			if err != nil && !apierrs.IsAlreadyExists(err) {
				log.Error(err, "Unable to create the ServiceMeshMember")
				return err
			}
			justCreated = true
		} else {
			log.Error(err, "Unable to fetch the ServiceMeshMember")
			return err
		}
	}

	// Reconcile the MeshMember spec if it has been manually modified
	if !justCreated && !ComparePredictorMeshMembers(desiredMeshMember, foundMeshMember) {
		log.Info("Reconciling ServiceMeshMember")
		// Retry the update operation when the ingress controller eventually
		// updates the resource version field
		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			// Get the last MeshMember revision
			if err := r.Get(ctx, types.NamespacedName{
				Name:      desiredMeshMember.Name,
				Namespace: predictor.Namespace,
			}, foundMeshMember); err != nil {
				return err
			}
			// Reconcile labels and spec field
			foundMeshMember.Spec = *desiredMeshMember.Spec.DeepCopy()
			foundMeshMember.ObjectMeta.Labels = desiredMeshMember.ObjectMeta.Labels
			return r.Update(ctx, foundMeshMember)
		})
		if err != nil {
			log.Error(err, "Unable to reconcile the ServiceMeshMember")
			return err
		}
	}

	return nil
}

// ReconcileMeshMember will manage the creation, update and deletion of the
// MeshMember when the Predictor is reconciled
func (r *OpenshiftPredictorReconciler) ReconcileMeshMember(
	predictor *predictorv1.Predictor, ctx context.Context) error {
	return r.reconcileMeshMember(predictor, ctx, NewPredictorMeshMember)
}
