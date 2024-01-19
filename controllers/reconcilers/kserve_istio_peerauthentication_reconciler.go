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
	"github.com/opendatahub-io/odh-model-controller/controllers/processors"
	"github.com/opendatahub-io/odh-model-controller/controllers/resources"
	"istio.io/api/security/v1beta1"
	istiotypes "istio.io/api/type/v1beta1"
	istiosecv1beta1 "istio.io/client-go/pkg/apis/security/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	peerAuthenticationName = "default"
)

type KserveIstioPeerAuthenticationReconciler struct {
	client                    client.Client
	scheme                    *runtime.Scheme
	peerAuthenticationHandler resources.PeerAuthenticationHandler
	deltaProcessor            processors.DeltaProcessor
}

func NewKServeIstioPeerAuthenticationReconciler(client client.Client, scheme *runtime.Scheme) *KserveIstioPeerAuthenticationReconciler {
	return &KserveIstioPeerAuthenticationReconciler{
		client:                    client,
		scheme:                    scheme,
		peerAuthenticationHandler: resources.NewPeerAuthenticationHandler(client),
		deltaProcessor:            processors.NewDeltaProcessor(),
	}
}

func (r *KserveIstioPeerAuthenticationReconciler) Reconcile(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {

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

func (r *KserveIstioPeerAuthenticationReconciler) createDesiredResource(isvc *kservev1beta1.InferenceService) (*istiosecv1beta1.PeerAuthentication, error) {
	desiredPeerAuthentication := &istiosecv1beta1.PeerAuthentication{
		ObjectMeta: metav1.ObjectMeta{
			Name:      peerAuthenticationName,
			Namespace: isvc.Namespace,
		},
		Spec: v1beta1.PeerAuthentication{
			Selector: &istiotypes.WorkloadSelector{
				MatchLabels: map[string]string{
					"component": "predictor",
				},
			},
			Mtls: &v1beta1.PeerAuthentication_MutualTLS{Mode: 3},
			PortLevelMtls: map[uint32]*v1beta1.PeerAuthentication_MutualTLS{
				8086: {Mode: 2},
				3000: {Mode: 2},
			},
		},
	}
	return desiredPeerAuthentication, nil
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
			return
		}
	}
	if delta.IsUpdated() {
		log.V(1).Info("Delta found", "update", existingPeerAuthentication.GetName())
		rp := existingPeerAuthentication.DeepCopy()
		rp.Spec.Selector = desiredPeerAuthentication.Spec.Selector
		rp.Spec.Mtls = desiredPeerAuthentication.Spec.Mtls
		rp.Spec.PortLevelMtls = desiredPeerAuthentication.Spec.PortLevelMtls

		if err = r.client.Update(ctx, rp); err != nil {
			return
		}
	}
	if delta.IsRemoved() {
		log.V(1).Info("Delta found", "delete", existingPeerAuthentication.GetName())
		if err = r.client.Delete(ctx, existingPeerAuthentication); err != nil {
			return
		}
	}
	return nil
}

func (r *KserveIstioPeerAuthenticationReconciler) DeletePeerAuthentication(ctx context.Context, isvcNamespace string) error {
	return r.peerAuthenticationHandler.DeletePeerAuthentication(ctx, types.NamespacedName{Name: peerAuthenticationName, Namespace: isvcNamespace})
}
