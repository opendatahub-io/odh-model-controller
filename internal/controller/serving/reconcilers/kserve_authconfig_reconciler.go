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

	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"

	"github.com/go-logr/logr"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	authorinov1beta2 "github.com/kuadrant/authorino/api/v1beta2"
	k8serror "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/comparators"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/processors"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/resources"
)

var _ SubResourceReconciler = (*KserveAuthConfigReconciler)(nil)

type KserveAuthConfigReconciler struct {
	client         client.Client
	deltaProcessor processors.DeltaProcessor
	detector       resources.AuthTypeDetector
	store          resources.AuthConfigStore
	templateLoader resources.AuthConfigTemplateLoader
	hostExtractor  resources.InferenceEndpointsHostExtractor
}

func NewKserveAuthConfigReconciler(client client.Client) *KserveAuthConfigReconciler {
	return &KserveAuthConfigReconciler{
		client:         client,
		deltaProcessor: processors.NewDeltaProcessor(),
		detector:       resources.NewKServeAuthTypeDetector(client),
		store:          resources.NewClientAuthConfigStore(client),
		templateLoader: resources.NewConfigMapTemplateLoader(client, resources.NewStaticTemplateLoader()),
		hostExtractor:  resources.NewKServeInferenceServiceHostExtractor(),
	}
}

func (r *KserveAuthConfigReconciler) Reconcile(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {
	log.V(1).Info("Reconciling Authorino AuthConfig for InferenceService")
	authorinoEnabled, capabilityErr := utils.VerifyIfMeshAuthorizationIsEnabled(ctx, r.client)
	if capabilityErr != nil {
		log.V(1).Error(capabilityErr, "Error while verifying if Authorino is enabled")
		return nil
	}
	if !authorinoEnabled {
		log.V(1).Info("Skipping AuthConfig reconciliation, authorization is not enabled")
		return nil
	}

	if isvc.Status.URL == nil {
		log.V(1).Info("Inference Service not ready yet, waiting for URL")
		return nil
	}

	desiredState, err := r.createDesiredResource(ctx, isvc)
	if err != nil {
		return err
	}

	existingState, err := r.getExistingResource(ctx, isvc)
	if err != nil && !k8serror.IsNotFound(err) {
		return err
	}

	// Process Delta
	if err = r.processDelta(ctx, log, desiredState, existingState); err != nil {
		return err
	}
	return nil
}

func (r *KserveAuthConfigReconciler) Delete(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {
	log.V(1).Info("Deleting Kserve inference service authorino authconfig entry")
	typeName := types.NamespacedName{
		Name:      isvc.GetName(),
		Namespace: isvc.GetNamespace(),
	}
	return r.store.Remove(ctx, typeName)
}

func (r *KserveAuthConfigReconciler) Cleanup(_ context.Context, _ logr.Logger, _ string) error {
	// NOOP
	return nil
}

func (r *KserveAuthConfigReconciler) createDesiredResource(ctx context.Context, isvc *kservev1beta1.InferenceService) (*authorinov1beta2.AuthConfig, error) {
	typeName := types.NamespacedName{
		Name:      isvc.GetName(),
		Namespace: isvc.GetNamespace(),
	}

	authType := r.detector.Detect(ctx, isvc.GetAnnotations())
	template, err := r.templateLoader.Load(ctx, authType, isvc)
	if err != nil {
		return nil, fmt.Errorf("could not load template for AuthType %s for InferenceService %s. cause: %w", authType, typeName, err)
	}

	template.Name = typeName.Name
	template.Namespace = typeName.Namespace
	template.Spec.Hosts = r.hostExtractor.Extract(isvc)
	if template.Labels == nil {
		template.Labels = map[string]string{}
	}
	template.Labels[constants.LabelAuthGroup] = "default"

	err = ctrl.SetControllerReference(isvc, &template, r.client.Scheme())
	if err != nil {
		return nil, err
	}

	return &template, nil
}

func (r *KserveAuthConfigReconciler) getExistingResource(ctx context.Context, isvc *kservev1beta1.InferenceService) (*authorinov1beta2.AuthConfig, error) {
	typeName := types.NamespacedName{
		Name:      isvc.GetName(),
		Namespace: isvc.GetNamespace(),
	}
	return r.store.Get(ctx, typeName)
}

func (r *KserveAuthConfigReconciler) processDelta(ctx context.Context, log logr.Logger, desiredState *authorinov1beta2.AuthConfig, existingState *authorinov1beta2.AuthConfig) (err error) {
	comparator := comparators.GetAuthConfigComparator()
	delta := r.deltaProcessor.ComputeDelta(comparator, desiredState, existingState)

	if !delta.HasChanges() {
		log.V(1).Info("No delta found")
		return nil
	}

	if delta.IsAdded() {
		log.V(1).Info("Delta found", "create", desiredState.GetName())
		err := r.store.Create(ctx, desiredState)
		if err != nil {
			return fmt.Errorf(
				"could not store AuthConfig %s for InferenceService %s. cause: %w", desiredState.Name, desiredState.Name, err)
		}
	} else if delta.IsUpdated() {
		log.V(1).Info("Delta found", "update", desiredState.GetName())
		rp := existingState.DeepCopy()
		rp.Spec = desiredState.Spec
		rp.Labels = desiredState.Labels
		err := r.store.Update(ctx, rp)
		if err != nil {
			return fmt.Errorf(
				"could not store AuthConfig %s for InferenceService %s. cause: %w", desiredState.Name, desiredState.Name, err)
		}
	} else if delta.IsRemoved() {
		log.V(1).Info("Delta found", "delete", existingState.GetName())
		err := r.store.Remove(ctx, types.NamespacedName{Namespace: existingState.Namespace, Name: existingState.Name})
		if err != nil {
			return fmt.Errorf(
				"could not remove AuthConfig %s for InferenceService %s. cause: %w", existingState.Name, existingState.Name, err)
		}
	}
	return nil
}
