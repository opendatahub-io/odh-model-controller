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
	"fmt"
	"time"

	"github.com/go-logr/logr"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"github.com/opendatahub-io/odh-model-controller/controllers/comparators"
	"github.com/opendatahub-io/odh-model-controller/controllers/constants"
	"github.com/opendatahub-io/odh-model-controller/controllers/processors"
	"github.com/opendatahub-io/odh-model-controller/controllers/resources"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	istiov1beta1 "istio.io/api/networking/v1beta1"
	istioclientv1beta1 "istio.io/client-go/pkg/apis/networking/v1beta1"
)

const (
	internalSerivceHostnameDomain = "svc.cluster.local"
	opendatahubManagedLabelName   = "opendatahub.io/managed='true'"
	secretCheckInterval           = 1 * time.Second
	maxSecretCheckAttempts        = 5
)

var _ SubResourceReconciler = (*KserveGatewayReconciler)(nil)

type KserveGatewayReconciler struct {
	client         client.Client
	secretHandler  resources.SecretHandler
	gatewayHandler resources.GatewayHandler
	deltaProcessor processors.DeltaProcessor
}

func NewKserveGatewayReconciler(client client.Client) *KserveGatewayReconciler {
	return &KserveGatewayReconciler{
		client:         client,
		secretHandler:  resources.NewSecretHandler(client),
		gatewayHandler: resources.NewGatewayHandler(client),
		deltaProcessor: processors.NewDeltaProcessor(),
	}
}

func (r *KserveGatewayReconciler) Reconcile(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {
	log.V(1).Info("Reconciling KServe local gateway for Kserve InferenceService")

	// return if URL is not set
	if isvc.Status.URL == nil {
		log.V(1).Info("Waiting for the URL as the Inference Service is not ready yet")
		return nil
	}

	destinationSecretExist := true
	if _, err := r.secretHandler.Get(ctx, types.NamespacedName{Name: isvc.Name, Namespace: constants.IstioNamespace}); err != nil {
		if errors.IsNotFound(err) {
			destinationSecretExist = false
		} else {
			return err
		}
	}

	if !destinationSecretExist {
		secret := &corev1.Secret{}
		for attempt := 1; attempt <= maxSecretCheckAttempts; attempt++ {
			getSecretErr := r.client.Get(ctx, types.NamespacedName{Name: isvc.Name, Namespace: isvc.Namespace}, secret)
			if getSecretErr == nil {
				break
			}
			if errors.IsNotFound(getSecretErr) {
				log.V(2).Info("The certificate secret is not created yet. Retrying...", "attempt_number", attempt)
				time.Sleep(secretCheckInterval)
			} else {
				log.Error(getSecretErr, "Failed to retrieve the certificate secret for the InferenceService (ISVC)")
				return getSecretErr
			}
		}

		if err := r.copyServingCertSecretFromIsvcNamespace(ctx, secret, isvc); err != nil {
			log.V(1).Error(err, "Failed to copy the Secret for InferenceService in the istio-system namespace")
			return err
		}
	}

	// Create Desired resource
	desiredResource, err := r.createDesiredResource(isvc)
	if err != nil {
		return err
	}

	// Get Existing resource
	existingResource, err := r.getExistingResource(ctx)
	if err != nil {
		if errors.IsNotFound(err) {
			log.Error(err, "Failed to find KServe local gateway in istio-system namespace")
		}
		return err
	}

	// Process Delta
	if err = r.processDelta(ctx, log, desiredResource, existingResource); err != nil {
		return err
	}
	return nil
}

func (r *KserveGatewayReconciler) createDesiredResource(isvc *kservev1beta1.InferenceService) (*istioclientv1beta1.Gateway, error) {
	hostname := fmt.Sprintf("%s.%s.%s", isvc.Name, isvc.Namespace, internalSerivceHostnameDomain)

	desiredGateway := &istioclientv1beta1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Name:      constants.KServeGatewayName,
			Namespace: isvc.Namespace,
		},
		Spec: istiov1beta1.Gateway{
			Servers: []*istiov1beta1.Server{
				{
					Hosts: []string{hostname},
					Port: &istiov1beta1.Port{
						Name:     isvc.Name,
						Number:   8445,
						Protocol: "HTTPS",
					},
					Tls: &istiov1beta1.ServerTLSSettings{
						CredentialName: isvc.Name,
						Mode:           istiov1beta1.ServerTLSSettings_SIMPLE,
					},
				},
			},
		},
	}

	return desiredGateway, nil
}

func (r *KserveGatewayReconciler) getExistingResource(ctx context.Context) (*istioclientv1beta1.Gateway, error) {
	return r.gatewayHandler.Get(ctx, types.NamespacedName{Name: constants.KServeGatewayName, Namespace: constants.IstioNamespace})
}

func (r *KserveGatewayReconciler) Delete(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {
	log.V(1).Info(fmt.Sprintf("Deleting serving cert secret(%s) in the namespace(%s)", isvc.Name, isvc.Namespace))
	if err := r.deleteServingCertSecretInIstioNamespace(ctx, isvc.Name); err != nil {
		log.V(1).Error(err, "Failed to delete the copied serving cert secret in the namespace")
		return err
	}

	log.V(1).Info(fmt.Sprintf("Deleting the Server(%s) from KServe local gateway in the istio-system namespace", isvc.Name))
	if err := r.removeServerFromGateway(ctx, log, isvc.Name); err != nil {
		log.V(1).Error(err, "Failed to remove the server from KServe local gateway in the istio-system namespace")
		return err
	}

	return nil
}

func (r *KserveGatewayReconciler) Cleanup(ctx context.Context, log logr.Logger, isvcName string) error {
	// NOOP - Resources should not be deleted until the kserve component is uninstalled.
	return nil
}

func (r *KserveGatewayReconciler) processDelta(ctx context.Context, log logr.Logger, desiredGateway *istioclientv1beta1.Gateway, existingGateway *istioclientv1beta1.Gateway) (err error) {
	comparator := comparators.GetGatewayComparator()
	delta := r.deltaProcessor.ComputeDelta(comparator, desiredGateway, existingGateway)

	if !delta.HasChanges() {
		// Note: This code will not be executed.
		return nil
	}
	if delta.IsAdded() {
		// Note: This code will not be executed.
		return nil
	}

	if delta.IsUpdated() {
		log.V(1).Info("Delta found", "update", desiredGateway.GetName())
		gw := existingGateway.DeepCopy()
		gw.Spec.Servers = append(existingGateway.Spec.Servers, desiredGateway.Spec.Servers[0])

		if err = r.gatewayHandler.Update(ctx, gw); err != nil {
			log.V(1).Error(err, fmt.Sprintf("Failed to add the Server(%s) from KServe local gateway in the istio-system namespace", desiredGateway.Spec.Servers[0].Port.Name))
			return err
		}

		return nil
	}

	if delta.IsRemoved() {
		// Note: This code will not be executed.
		return nil
	}
	return nil
}

func (r *KserveGatewayReconciler) removeServerFromGateway(ctx context.Context, log logr.Logger, serverToRemove string) error {
	gateway, err := r.gatewayHandler.Get(ctx, types.NamespacedName{Name: constants.KServeGatewayName, Namespace: constants.IstioNamespace})
	if err != nil {
		log.V(1).Error(err, "Failed to retrieve KServe local gateway in istio-system namespace")
		return err
	}

	newServers := []*istiov1beta1.Server{}
	for _, server := range gateway.Spec.Servers {
		if server.Port.Name != serverToRemove {
			newServers = append(newServers, server)
		}
	}

	gateway.Spec.Servers = newServers
	if err := r.gatewayHandler.Update(ctx, gateway); err != nil {
		log.V(1).Error(err, "Failed to update KServe local gateway in istio-system namespace")
		return err
	}

	return nil
}

func (r *KserveGatewayReconciler) copyServingCertSecretFromIsvcNamespace(ctx context.Context, sourceSecret *corev1.Secret, isvc *kservev1beta1.InferenceService) error {

	destinationSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sourceSecret.Name,
			Namespace: constants.IstioNamespace,
		},
		Data: sourceSecret.Data,
		Type: sourceSecret.Type,
	}

	if err := r.client.Create(ctx, destinationSecret); err != nil {
		return err
	}
	return nil
}

func (r *KserveGatewayReconciler) deleteServingCertSecretInIstioNamespace(ctx context.Context, targetSecretName string) error {
	secret, err := r.secretHandler.Get(ctx, types.NamespacedName{Name: targetSecretName, Namespace: constants.IstioNamespace})
	if err != nil && errors.IsNotFound(err) {
		return nil
	}

	if err := r.client.Delete(ctx, secret); err != nil {
		return err
	}
	return nil
}
