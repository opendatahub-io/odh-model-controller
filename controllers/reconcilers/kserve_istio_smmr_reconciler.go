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
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"github.com/opendatahub-io/odh-model-controller/controllers/comparators"
	"github.com/opendatahub-io/odh-model-controller/controllers/constants"
	"github.com/opendatahub-io/odh-model-controller/controllers/processors"
	"github.com/opendatahub-io/odh-model-controller/controllers/resources"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	v1 "maistra.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ SubResourceReconciler = (*KserveIstioSMMRReconciler)(nil)

type KserveIstioSMMRReconciler struct {
	SingleResourcePerNamespace
	client         client.Client
	smmrHandler    resources.ServiceMeshMemberRollHandler
	deltaProcessor processors.DeltaProcessor
}

func NewKServeIstioSMMRReconciler(client client.Client) *KserveIstioSMMRReconciler {
	return &KserveIstioSMMRReconciler{
		client:         client,
		smmrHandler:    resources.NewServiceMeshMemberRole(client),
		deltaProcessor: processors.NewDeltaProcessor(),
	}
}

func (r *KserveIstioSMMRReconciler) Reconcile(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {
	log.V(1).Info("Verifying that the default ServiceMeshMemberRoll has the target namespace")

	// Create Desired resource
	desiredResource, err := r.createDesiredResource(ctx, log, isvc)
	if err != nil {
		return err
	}

	// Get Existing resource
	existingResource, err := r.getExistingResource(ctx, log)
	if err != nil {
		return err
	}

	// Process Delta
	if err = r.processDelta(ctx, log, desiredResource, existingResource); err != nil {
		return err
	}
	return nil
}

func (r *KserveIstioSMMRReconciler) Cleanup(ctx context.Context, log logr.Logger, isvcNs string) error {
	log.V(1).Info("Removing target namespace from ServiceMeshMemberRole")
	return r.smmrHandler.RemoveMemberFromSMMR(ctx, types.NamespacedName{Name: constants.ServiceMeshMemberRollName, Namespace: constants.IstioNamespace}, isvcNs)
}

func (r *KserveIstioSMMRReconciler) createDesiredResource(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) (*v1.ServiceMeshMemberRoll, error) {
	desiredSMMR, err := r.smmrHandler.FetchSMMR(ctx, log, types.NamespacedName{Name: constants.ServiceMeshMemberRollName, Namespace: constants.IstioNamespace})
	if err != nil {
		return nil, err
	}

	if desiredSMMR != nil {
		//check if the namespace is already in the list, if it does not exists, append and update
		serviceMeshMemberRollEntryExists := false
		memberList := desiredSMMR.Spec.Members
		for _, member := range memberList {
			if member == isvc.Namespace {
				serviceMeshMemberRollEntryExists = true
			}
		}
		if !serviceMeshMemberRollEntryExists {
			desiredSMMR.Spec.Members = append(memberList, isvc.Namespace)
		}
	} else {
		desiredSMMR = &v1.ServiceMeshMemberRoll{
			ObjectMeta: metav1.ObjectMeta{
				Name:      constants.ServiceMeshMemberRollName,
				Namespace: constants.IstioNamespace,
			},
			Spec: v1.ServiceMeshMemberRollSpec{
				Members: []string{
					isvc.Namespace,
				},
			},
		}
	}
	return desiredSMMR, nil
}

func (r *KserveIstioSMMRReconciler) getExistingResource(ctx context.Context, log logr.Logger) (*v1.ServiceMeshMemberRoll, error) {
	return r.smmrHandler.FetchSMMR(ctx, log, types.NamespacedName{Name: constants.ServiceMeshMemberRollName, Namespace: constants.IstioNamespace})
}

func (r *KserveIstioSMMRReconciler) processDelta(ctx context.Context, log logr.Logger, desiredSMMR *v1.ServiceMeshMemberRoll, existingSMMR *v1.ServiceMeshMemberRoll) (err error) {
	comparator := comparators.GetServiceMeshMemberRollComparator()
	delta := r.deltaProcessor.ComputeDelta(comparator, desiredSMMR, existingSMMR)

	if !delta.HasChanges() {
		log.V(1).Info("No delta found")
		return nil
	}

	if delta.IsAdded() {
		log.V(1).Info("Delta found", "create", desiredSMMR.GetName())
		if err = r.client.Create(ctx, desiredSMMR); err != nil {
			return
		}
	}
	if delta.IsUpdated() {
		log.V(1).Info("Delta found", "update", existingSMMR.GetName())
		rp := existingSMMR.DeepCopy()
		rp.Spec = desiredSMMR.Spec

		if err = r.client.Update(ctx, rp); err != nil {
			return
		}
	}
	if delta.IsRemoved() {
		log.V(1).Info("Delta found", "delete", existingSMMR.GetName())
		if err = r.client.Delete(ctx, existingSMMR); err != nil {
			return
		}
	}
	return nil
}
