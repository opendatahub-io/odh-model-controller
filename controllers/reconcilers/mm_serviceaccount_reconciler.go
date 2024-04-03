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

package reconcilers

import (
	"context"
	"github.com/go-logr/logr"
	"github.com/opendatahub-io/odh-model-controller/controllers/comparators"
	"github.com/opendatahub-io/odh-model-controller/controllers/processors"
	"github.com/opendatahub-io/odh-model-controller/controllers/resources"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"k8s.io/apimachinery/pkg/types"

	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	modelMeshServiceAccountName = "modelmesh-serving-sa"
)

var _ SubResourceReconciler = (*ModelMeshServiceAccountReconciler)(nil)

type ModelMeshServiceAccountReconciler struct {
	SingleResourcePerNamespace
	client                client.Client
	scheme                *runtime.Scheme
	serviceAccountHandler resources.ServiceAccountHandler
	deltaProcessor        processors.DeltaProcessor
}

func NewModelMeshServiceAccountReconciler(client client.Client, scheme *runtime.Scheme) *ModelMeshServiceAccountReconciler {
	return &ModelMeshServiceAccountReconciler{
		client:                client,
		scheme:                scheme,
		serviceAccountHandler: resources.NewServiceAccountHandler(client),
		deltaProcessor:        processors.NewDeltaProcessor(),
	}
}

func (r *ModelMeshServiceAccountReconciler) Reconcile(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {
	log.V(1).Info("Reconciling ServiceAccount for InferenceService")
	// Create Desired resource
	desiredResource, err := r.createDesiredResource(isvc)
	if err != nil {
		return err
	}

	// Get Existing resource
	existingResource, err := r.getExistingResource(ctx, log, isvc)
	if err != nil {
		return err
	}

	// Process Delta
	if err = r.processDelta(ctx, log, desiredResource, existingResource); err != nil {
		return err
	}
	return nil
}

func (r *ModelMeshServiceAccountReconciler) Cleanup(ctx context.Context, log logr.Logger, isvcNs string) error {
	log.V(1).Info("Deleting ServiceAccount object for target namespace")
	return r.serviceAccountHandler.DeleteServiceAccount(ctx, types.NamespacedName{Name: modelMeshServiceAccountName, Namespace: isvcNs})
}

func (r *ModelMeshServiceAccountReconciler) createDesiredResource(isvc *kservev1beta1.InferenceService) (*corev1.ServiceAccount, error) {
	desiredSA := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      modelMeshServiceAccountName,
			Namespace: isvc.Namespace,
		},
	}
	return desiredSA, nil
}

func (r *ModelMeshServiceAccountReconciler) getExistingResource(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) (*corev1.ServiceAccount, error) {
	return r.serviceAccountHandler.FetchServiceAccount(ctx, log, types.NamespacedName{Name: modelMeshServiceAccountName, Namespace: isvc.Namespace})
}

func (r *ModelMeshServiceAccountReconciler) processDelta(ctx context.Context, log logr.Logger, desiredSA *corev1.ServiceAccount, existingSA *corev1.ServiceAccount) (err error) {
	comparator := comparators.GetServiceAccountComparator()
	delta := r.deltaProcessor.ComputeDelta(comparator, desiredSA, existingSA)

	if !delta.HasChanges() {
		log.V(1).Info("No delta found")
		return nil
	}

	if delta.IsAdded() {
		log.V(1).Info("Delta found", "create", desiredSA.GetName())
		if err = r.client.Create(ctx, desiredSA); err != nil {
			return
		}
	}
	if delta.IsUpdated() {
		log.V(1).Info("Delta found", "update", existingSA.GetName())
		rp := existingSA.DeepCopy()
		if err = r.client.Update(ctx, rp); err != nil {
			return
		}
	}
	if delta.IsRemoved() {
		log.V(1).Info("Delta found", "delete", existingSA.GetName())
		if err = r.client.Delete(ctx, existingSA); err != nil {
			return
		}
	}
	return nil
}
