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
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// NewPredictorServingRuntimes defines the desired ServingRuntimes object
func NewPredictorServingRuntimes(predictor *predictorv1.Predictor, ctx context.Context, r *OpenshiftPredictorReconciler) *predictorv1.ServingRuntimeList {
	srList := &predictorv1.ServingRuntimeList{}
	// Read the configmap to see the list of servingruntimes we need
	configmap := &corev1.ConfigMap{}
	r.Get(ctx, types.NamespacedName{
		Name:      "odh-model-controller-servingruntimes-config",
		Namespace: "odh-model-controller-system", // TODO get actaul live namespace
	}, configmap)
	runtimes := configmap.Data["servingruntimes_config.yaml"]

	decode := serializer.NewCodecFactory(r.Scheme).UniversalDeserializer().Decode
	obj, _, _ := decode([]byte(runtimes), nil, nil)
	cm := obj.(*corev1.ConfigMap)

	for key := range cm.Data {
		sr := cm.Data[key]
		obj, _, _ := decode([]byte(sr), nil, nil)
		srobject := obj.(*predictorv1.ServingRuntime)
		srobject.ObjectMeta.Namespace = predictor.Namespace
		srList.Items = append(srList.Items, *srobject)
	}
	return srList
}

// ComparePredictorServingRuntimess checks if two ServingRuntimess are equal, if not return false
func ComparePredictorServingRuntimess(srl1 *predictorv1.ServingRuntimeList, srl2 *predictorv1.ServingRuntimeList) bool {
	// Two ServingRuntimess will be equal if they have the same names
	// listonekeys := srl1.Items

	return false //TODO do it for real
}

// Reconcile will manage the creation, update and deletion of the ServingRuntimes returned
// by the newServingRuntimes function
func (r *OpenshiftPredictorReconciler) reconcileServingRuntimes(predictor *predictorv1.Predictor,
	ctx context.Context, newServingRuntimes func(*predictorv1.Predictor, context.Context, *OpenshiftPredictorReconciler) *predictorv1.ServingRuntimeList) error {
	// Initialize logger format
	log := r.Log.WithValues("Predictor", predictor.Name, "namespace", predictor.Namespace)

	// Generate the desired ServiceServingRuntimes
	desiredServingRuntimes := newServingRuntimes(predictor, ctx, r)

	// Create the ServiceServingRuntimes if it does not already exist
	foundServingRuntimes := &predictorv1.ServingRuntimeList{}
	justCreated := false
	listOptions := client.ListOptions{
		Namespace: predictor.Namespace,
	}
	err := r.List(ctx, foundServingRuntimes, &listOptions)
	if err != nil {
		// if apierrs.IsNotFound(err) {
		// 	log.Info("Creating ServiceServingRuntimes")
		// 	// Add .metatada.ownerReferences to the ServingRuntimes to be deleted by the
		// 	// Kubernetes garbage collector if the Predictor is deleted
		// 	err = ctrl.SetControllerReference(predictor, desiredServingRuntimes, r.Scheme)
		// 	if err != nil {
		// 		log.Error(err, "Unable to add OwnerReference to the ServingRuntimes")
		// 		return err
		// 	}
		// 	// Create the ServiceServingRuntimes in the Openshift cluster
		// 	err = r.Create(ctx, desiredServingRuntimes)
		// 	if err != nil && !apierrs.IsAlreadyExists(err) {
		// 		log.Error(err, "Unable to create the ServiceServingRuntimes")
		// 		return err
		// 	}
		// 	justCreated = true
		// } else {
		// 	log.Error(err, "Unable to fetch the ServiceServingRuntimes")
		// 	return err
		// }
	}

	// Reconcile the ServingRuntimes spec if it has been manually modified
	if !justCreated && !ComparePredictorServingRuntimess(desiredServingRuntimes, foundServingRuntimes) {
		log.Info("Reconciling ServiceServingRuntimes")
		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			listOptions := client.ListOptions{
				Namespace: predictor.Namespace,
			}
			if err := r.List(ctx, foundServingRuntimes, &listOptions); err != nil {
				return err
			}
			// Reconcile ServingRuntimes by adding them as needed, updating the list isn't possible
			for key := range desiredServingRuntimes.Items {
				sr := desiredServingRuntimes.Items[key]
				r.Create(ctx, &sr) // TODO...not ready yet
				// if err != nil {
				// 	log.Error(err, "problem updating SR")
				// }
			}
			return nil
		})
		if err != nil {
			log.Error(err, "Unable to reconcile the ServiceServingRuntimes")
			return err
		}
	}

	return nil
}

// ReconcileServingRuntimes will manage the creation, update and deletion of the
// ServingRuntimes when the Predictor is reconciled
func (r *OpenshiftPredictorReconciler) ReconcileServingRuntimes(
	predictor *predictorv1.Predictor, ctx context.Context) error {
	return r.reconcileServingRuntimes(predictor, ctx, NewPredictorServingRuntimes)
}
