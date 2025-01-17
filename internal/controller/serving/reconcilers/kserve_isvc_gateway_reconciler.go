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
	"net/url"
	"reflect"

	"github.com/go-logr/logr"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/comparators"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/processors"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/resources"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	istiov1beta1 "istio.io/api/networking/v1beta1"
	istioclientv1beta1 "istio.io/client-go/pkg/apis/networking/v1beta1"
)

var _ SubResourceReconciler = (*KserveGatewayReconciler)(nil)
var meshNamespace string

type KserveGatewayReconciler struct {
	client         client.Client
	clientReader   client.Reader
	secretHandler  resources.SecretHandler
	gatewayHandler resources.GatewayHandler
	deltaProcessor processors.DeltaProcessor
}

// The clientReader uses the API server to retrieve Secrets that are not cached. By default, only Secrets with the specific label "opendatahub.io/managed: true" are cached.
func NewKserveGatewayReconciler(client client.Client, clientReader client.Reader) *KserveGatewayReconciler {

	return &KserveGatewayReconciler{
		client:         client,
		clientReader:   clientReader,
		secretHandler:  resources.NewSecretHandler(client),
		gatewayHandler: resources.NewGatewayHandler(client),
		deltaProcessor: processors.NewDeltaProcessor(),
	}
}

func (r *KserveGatewayReconciler) Reconcile(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {
	log.V(1).Info("Reconciling KServe local gateway for Kserve InferenceService")

	_, meshNamespace = utils.GetIstioControlPlaneName(ctx, r.client)

	// return if Address.URL is not set
	if isvc.Status.Address == nil || isvc.Status.Address.URL == nil {
		log.V(1).Info("Waiting for the URL as the InferenceService is not ready yet")
		return nil
	}

	// return if serving cert secret in the source namespace is not created
	srcCertSecret := &corev1.Secret{}
	err := r.clientReader.Get(ctx, types.NamespacedName{Name: isvc.Name, Namespace: isvc.Namespace}, srcCertSecret)
	if err != nil {
		if errors.IsNotFound(err) {
			log.V(1).Info(fmt.Sprintf("Waiting for the creation of the serving certificate Secret(%s) in %s namespace", isvc.Name, isvc.Namespace))
			return nil
		}
		return err
	}

	// Copy src secret to destination namespace when there is not the synced secret.
	// This use clientReader because the secret that it looks for is not cached.
	copiedCertSecret := &corev1.Secret{}
	err = r.clientReader.Get(ctx, types.NamespacedName{Name: fmt.Sprintf("%s-%s", isvc.Name, isvc.Namespace), Namespace: meshNamespace}, copiedCertSecret)
	if err != nil {
		if errors.IsNotFound(err) {
			if err := r.copyServingCertSecretFromIsvcNamespace(ctx, srcCertSecret, nil); err != nil {
				log.V(1).Error(err, fmt.Sprintf("Failed to copy the serving certificate Secret(%s) to %s namespace", srcCertSecret.Name, meshNamespace))
				return err
			}
		}
		return err
	} else {
		// Recreate copied secrt when src secret is updated
		if !reflect.DeepEqual(srcCertSecret.Data, copiedCertSecret.Data) {
			log.V(2).Info(fmt.Sprintf("Recreating for serving certificate Secret(%s) in %s namespace", copiedCertSecret.Name, meshNamespace))
			if err := r.copyServingCertSecretFromIsvcNamespace(ctx, srcCertSecret, copiedCertSecret); err != nil {
				log.V(1).Error(err, fmt.Sprintf("Failed to copy the Secret(%s) for InferenceService in %s namespace", copiedCertSecret.Name, meshNamespace))
				return err
			}
		}
	}

	// Create Desired resource
	desiredResource, err := r.getDesiredResource(isvc)
	if err != nil {
		return err
	}

	// Get Existing resource
	existingResource, err := r.getExistingResource(ctx)
	if err != nil {
		if errors.IsNotFound(err) {
			log.Error(err, fmt.Sprintf("Failed to find KServe local gateway in %s namespace", meshNamespace))
		}
		return err
	}

	// Process Delta
	if err = r.processDelta(ctx, log, desiredResource, existingResource); err != nil {
		return err
	}
	return nil
}

func (r *KserveGatewayReconciler) getDesiredResource(isvc *kservev1beta1.InferenceService) (*istioclientv1beta1.Gateway, error) {
	hostname, err := getURLWithoutScheme(isvc)
	if err != nil {
		return nil, err
	}

	desiredGateway := &istioclientv1beta1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Name:      constants.KServeGatewayName,
			Namespace: meshNamespace,
		},
		Spec: istiov1beta1.Gateway{
			Servers: []*istiov1beta1.Server{
				{
					Hosts: []string{hostname},
					Port: &istiov1beta1.Port{
						Name:     fmt.Sprintf("%s-%s", "https", isvc.Name),
						Number:   8445,
						Protocol: "HTTPS",
					},
					Tls: &istiov1beta1.ServerTLSSettings{
						CredentialName: fmt.Sprintf("%s-%s", isvc.Name, isvc.Namespace),
						Mode:           istiov1beta1.ServerTLSSettings_SIMPLE,
					},
				},
			},
		},
	}

	return desiredGateway, nil
}

func (r *KserveGatewayReconciler) getExistingResource(ctx context.Context) (*istioclientv1beta1.Gateway, error) {
	return r.gatewayHandler.Get(ctx, types.NamespacedName{Name: constants.KServeGatewayName, Namespace: meshNamespace})
}

func (r *KserveGatewayReconciler) Delete(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {
	var errs []error

	log.V(1).Info(fmt.Sprintf("Deleting serving certificate Secret(%s) in %s namespace", fmt.Sprintf("%s-%s", isvc.Name, isvc.Namespace), isvc.Namespace))
	if err := r.deleteServingCertSecretInIstioNamespace(ctx, fmt.Sprintf("%s-%s", isvc.Name, isvc.Namespace)); err != nil {
		log.V(1).Error(err, fmt.Sprintf("Failed to delete the copied serving certificate Secret(%s) in %s namespace", fmt.Sprintf("%s-%s", isvc.Name, isvc.Namespace), isvc.Namespace))
		errs = append(errs, err)
	}

	log.V(1).Info(fmt.Sprintf("Deleting the Server(%s) from KServe local gateway in the %s namespace", fmt.Sprintf("%s-%s", "https", isvc.Name), meshNamespace))
	if err := r.removeServerFromGateway(ctx, log, fmt.Sprintf("%s-%s", "https", isvc.Name)); err != nil {
		log.V(1).Error(err, fmt.Sprintf("Failed to remove the Server(%s) from KServe local gateway in the %s namespace", fmt.Sprintf("%s-%s", "https", isvc.Name), meshNamespace))
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return fmt.Errorf("multiple errors: %v", errs)
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
	return nil
}

func (r *KserveGatewayReconciler) removeServerFromGateway(ctx context.Context, log logr.Logger, serverToRemove string) error {
	gateway, err := r.gatewayHandler.Get(ctx, types.NamespacedName{Name: constants.KServeGatewayName, Namespace: meshNamespace})
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

func (r *KserveGatewayReconciler) copyServingCertSecretFromIsvcNamespace(ctx context.Context, sourceSecret *corev1.Secret, preDestSecret *corev1.Secret) error {
	destinationSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s", sourceSecret.Name, sourceSecret.Namespace),
			Namespace: meshNamespace,
			Labels: map[string]string{
				"opendatahub.io/managed":       "true",
				"app.kubernetes.io/name":       "odh-model-controller",
				"app.kubernetes.io/component":  "kserve",
				"app.kubernetes.io/part-of":    "odh-model-serving",
				"app.kubernetes.io/managed-by": "odh-model-controller",
			},
		},
		Data: sourceSecret.Data,
		Type: sourceSecret.Type,
	}

	// Remove old secret if src secret is updated
	if preDestSecret != nil {
		if err := r.client.Delete(ctx, preDestSecret); err != nil {
			return err
		}
	}

	if err := r.client.Create(ctx, destinationSecret); err != nil {
		return err
	}

	// add label 'opendatahub.io/managed=true' to original Secret for caching
	if err := r.addServingCertSecretLabel(ctx, sourceSecret); err != nil {
		return err
	}
	return nil
}

func (r *KserveGatewayReconciler) addServingCertSecretLabel(ctx context.Context, sourceSecret *corev1.Secret) error {
	service := sourceSecret.DeepCopy()
	if service.Labels == nil {
		service.Labels = make(map[string]string)
	}

	service.Labels["opendatahub.io/managed"] = "true"

	err := r.client.Update(ctx, service)

	return err
}

func (r *KserveGatewayReconciler) deleteServingCertSecretInIstioNamespace(ctx context.Context, targetSecretName string) error {
	secret, err := r.secretHandler.Get(ctx, types.NamespacedName{Name: targetSecretName, Namespace: meshNamespace})
	if err != nil && errors.IsNotFound(err) {
		return nil
	}

	if err := r.client.Delete(ctx, secret); err != nil {
		return err
	}
	return nil
}

func getURLWithoutScheme(isvc *kservev1beta1.InferenceService) (string, error) {
	parsedURL, err := url.Parse(isvc.Status.Address.URL.String())
	if err != nil {
		return "", err
	}

	return parsedURL.Host + parsedURL.Path, nil
}
