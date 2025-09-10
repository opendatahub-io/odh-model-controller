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

	"github.com/go-logr/logr"
	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/comparators"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/processors"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/resources"
	parentreconcilers "github.com/opendatahub-io/odh-model-controller/internal/controller/serving/reconcilers"
)

// KserveAuthPolicyReconciler is a reconciler for AuthPolicy resources for LLMInferenceService objects
var _ parentreconcilers.LLMSubResourceReconciler = (*KserveAuthPolicyReconciler)(nil)

type KserveAuthPolicyReconciler struct {
	client         client.Client
	kClient        kubernetes.Interface
	restConfig     *rest.Config
	deltaProcessor processors.DeltaProcessor
	detector       resources.AuthPolicyDetector
	templateLoader resources.AuthPolicyTemplateLoader
}

func NewKserveAuthPolicyReconciler(client client.Client, kClient kubernetes.Interface, restConfig *rest.Config, scheme *runtime.Scheme) *KserveAuthPolicyReconciler {
	return &KserveAuthPolicyReconciler{
		client:         client,
		kClient:        kClient,
		restConfig:     restConfig,
		deltaProcessor: processors.NewDeltaProcessor(),
		detector:       resources.NewKServeAuthPolicyDetector(client),
		templateLoader: resources.NewKServeAuthPolicyTemplateLoader(client, scheme),
	}
}

func (r *KserveAuthPolicyReconciler) Reconcile(ctx context.Context, log logr.Logger, llmisvc *kservev1alpha1.LLMInferenceService) error {
	log.V(1).Info("Starting AuthPolicy reconciliation for LLMInferenceService")

	existingAuthPolicy, err := r.getExistingAuthPolicy(ctx, llmisvc)
	if err != nil && !apierrors.IsNotFound(err) {
		log.Error(err, "Failed to get existing AuthPolicy")
		return err
	}

	if err = r.processDelta(ctx, log, llmisvc, existingAuthPolicy); err != nil {
		log.Error(err, "Failed to process AuthPolicy delta")
		return err
	}

	return nil
}

func (r *KserveAuthPolicyReconciler) Delete(ctx context.Context, log logr.Logger, llmisvc *kservev1alpha1.LLMInferenceService) error {
	log.V(1).Info("Deleting AuthPolicy for LLMInferenceService")

	authPolicyName := constants.GetAuthPolicyName(llmisvc.Name)
	authPolicy := &unstructured.Unstructured{}
	r.setAuthPolicyGVK(authPolicy)
	authPolicy.SetName(authPolicyName)
	authPolicy.SetNamespace(llmisvc.Namespace)

	if err := r.client.Delete(ctx, authPolicy); err != nil {
		if apierrors.IsNotFound(err) {
			log.V(1).Info("AuthPolicy not found, already deleted", "name", authPolicyName)
			return nil
		}
		return fmt.Errorf("failed to delete AuthPolicy %s: %w", authPolicyName, err)
	}

	log.V(1).Info("Successfully deleted AuthPolicy", "name", authPolicyName)
	return nil
}

func (r *KserveAuthPolicyReconciler) Cleanup(ctx context.Context, log logr.Logger, isvcNs string) error {
	return nil
}

func (r *KserveAuthPolicyReconciler) getExistingAuthPolicy(ctx context.Context, llmisvc *kservev1alpha1.LLMInferenceService) (*unstructured.Unstructured, error) {
	authPolicy := &unstructured.Unstructured{}
	r.setAuthPolicyGVK(authPolicy)

	err := r.client.Get(ctx, types.NamespacedName{
		Name:      constants.GetAuthPolicyName(llmisvc.Name),
		Namespace: llmisvc.Namespace,
	}, authPolicy)

	if err != nil {
		return nil, err
	}

	return authPolicy, nil
}

func (r *KserveAuthPolicyReconciler) processDelta(ctx context.Context, log logr.Logger, llmisvc *kservev1alpha1.LLMInferenceService, existing *unstructured.Unstructured) error {
	authType := r.detector.Detect(ctx, llmisvc.GetAnnotations())
	log.V(1).Info("Auth enabled for LLMInferenceService, proceeding with AuthPolicy reconciliation")

	desired, err := r.templateLoader.Load(ctx, authType, llmisvc)
	if err != nil {
		log.Error(err, "Failed to load AuthPolicy template")
		return err
	}

	delta := r.deltaProcessor.ComputeDelta(comparators.GetAuthPolicyComparator(), desired, existing)

	if !delta.HasChanges() {
		log.V(1).Info("No delta found for AuthPolicy")
		return nil
	}

	if delta.IsAdded() {
		log.V(1).Info("Delta found", "action", "create", "authpolicy", desired.GetName())
		// Ensure GC on LLMInferenceService deletion
		if err := controllerutil.SetControllerReference(llmisvc, desired, r.client.Scheme()); err != nil {
			return fmt.Errorf("failed to set controller reference for AuthPolicy %s: %w", desired.GetName(), err)
		}
		if err := r.client.Create(ctx, desired); err != nil {
			return fmt.Errorf("failed to create AuthPolicy %s: %w", desired.GetName(), err)
		}
	} else if delta.IsUpdated() {
		log.V(1).Info("Delta found", "action", "update", "authpolicy", existing.GetName())

		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			current := &unstructured.Unstructured{}
			r.setAuthPolicyGVK(current)
			if err := r.client.Get(ctx, types.NamespacedName{
				Name:      existing.GetName(),
				Namespace: existing.GetNamespace(),
			}, current); err != nil {
				return err
			}

			current.Object["spec"] = desired.Object["spec"]

			// Update metadata (labels and annotations) to match desired state
			if desired.Object["metadata"] != nil {
				currentMetadata, ok := current.Object["metadata"].(map[string]interface{})
				if !ok {
					currentMetadata = make(map[string]interface{})
					current.Object["metadata"] = currentMetadata
				}

				desiredMetadata, ok := desired.Object["metadata"].(map[string]interface{})
				if ok {
					// Update labels
					if desiredLabels, exists := desiredMetadata["labels"]; exists {
						currentMetadata["labels"] = desiredLabels
					}
					// Update annotations
					if desiredAnnotations, exists := desiredMetadata["annotations"]; exists {
						currentMetadata["annotations"] = desiredAnnotations
					}
				}
			}

			return r.client.Update(ctx, current)
		})

		if err != nil {
			return fmt.Errorf("failed to update AuthPolicy %s after retries: %w", existing.GetName(), err)
		}
	}

	return nil
}

func (r *KserveAuthPolicyReconciler) setAuthPolicyGVK(obj *unstructured.Unstructured) {
	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   constants.AuthPolicyGroup,
		Version: constants.AuthPolicyVersion,
		Kind:    constants.AuthPolicyKind,
	})
}
