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
	istiosecv1beta1 "istio.io/client-go/pkg/apis/security/v1beta1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/comparators"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/processors"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/resources"
)

const (
	peerAuthenticationName = "default"
)

var _ SubResourceReconciler = (*KserveIstioPeerAuthenticationReconciler)(nil)

type KserveIstioPeerAuthenticationReconciler struct {
	SingleResourcePerNamespace
	client                    client.Client
	peerAuthenticationHandler resources.PeerAuthenticationHandler
	deltaProcessor            processors.DeltaProcessor
}

func NewKServeIstioPeerAuthenticationReconciler(client client.Client) *KserveIstioPeerAuthenticationReconciler {
	return &KserveIstioPeerAuthenticationReconciler{
		client:                    client,
		peerAuthenticationHandler: resources.NewPeerAuthenticationHandler(client),
		deltaProcessor:            processors.NewDeltaProcessor(),
	}
}

// TODO remove this reconcile loop in future versions
func (r *KserveIstioPeerAuthenticationReconciler) Reconcile(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {
	log.V(1).Info("Reconciling PeerAuthentication for target namespace, checking if there are resources for deletion")
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

func (r *KserveIstioPeerAuthenticationReconciler) Cleanup(ctx context.Context, log logr.Logger, isvcNs string) error {
	log.V(1).Info("Deleting PeerAuthentication object for target namespace")
	return r.peerAuthenticationHandler.DeletePeerAuthentication(ctx, types.NamespacedName{Name: peerAuthenticationName, Namespace: isvcNs})
}

func (r *KserveIstioPeerAuthenticationReconciler) createDesiredResource(isvc *kservev1beta1.InferenceService) (*istiosecv1beta1.PeerAuthentication, error) {
	return nil, nil
}

func (r *KserveIstioPeerAuthenticationReconciler) getExistingResource(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) (*istiosecv1beta1.PeerAuthentication, error) {
	return r.peerAuthenticationHandler.FetchPeerAuthentication(ctx, log, types.NamespacedName{Name: peerAuthenticationName, Namespace: isvc.Namespace})
}

func (r *KserveIstioPeerAuthenticationReconciler) processDelta(ctx context.Context, log logr.Logger, desiredPeerAuthentication *istiosecv1beta1.PeerAuthentication, existingPeerAuthentication *istiosecv1beta1.PeerAuthentication) (err error) {
	comparator := comparators.GetPeerAuthenticationComparator()
	delta := r.deltaProcessor.ComputeDelta(comparator, desiredPeerAuthentication, existingPeerAuthentication)

	if !delta.HasChanges() {
		log.V(1).Info("No delta found")
		return nil
	}

	if delta.IsAdded() {
		log.V(1).Info("Delta found", "create", desiredPeerAuthentication.GetName())
		if err = r.client.Create(ctx, desiredPeerAuthentication); err != nil {
			return err
		}
	}
	if delta.IsUpdated() {
		log.V(1).Info("Delta found", "update", existingPeerAuthentication.GetName())
		rp := existingPeerAuthentication.DeepCopy()
		rp.Spec.Selector = desiredPeerAuthentication.Spec.Selector
		rp.Spec.Mtls = desiredPeerAuthentication.Spec.Mtls
		rp.Spec.PortLevelMtls = desiredPeerAuthentication.Spec.PortLevelMtls

		if err = r.client.Update(ctx, rp); err != nil {
			return err
		}
	}
	if delta.IsRemoved() {
		log.V(1).Info("Delta found", "delete", existingPeerAuthentication.GetName())
		if err = r.client.Delete(ctx, existingPeerAuthentication); err != nil {
			return err
		}
	}
	return nil
}
