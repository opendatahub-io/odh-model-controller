/*
Copyright 2025.

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

package v1alpha1

import (
	"context"
	"errors"
	"fmt"

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/serving/reconcilers"
)

const (
	TierAnnotationKey = reconcilers.TierAnnotationKey
	TierConfigMapName = reconcilers.TierConfigMapName
)

// nolint:unused
// log is for logging in this package.
var llminferenceservicelog = logf.Log.WithName("llminferenceservice-resource")

// SetupLLMInferenceServiceWebhookWithManager registers the webhook for LLMInferenceService in the manager.
func SetupLLMInferenceServiceWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).For(&kservev1alpha1.LLMInferenceService{}).
		WithValidator(&LLMInferenceServiceTierValidator{
			client:           mgr.GetClient(),
			tierConfigLoader: reconcilers.NewTierConfigLoader(mgr.GetClient()),
		}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-serving-kserve-io-v1alpha1-llminferenceservice,mutating=false,failurePolicy=fail,sideEffects=None,groups=serving.kserve.io,resources=llminferenceservices,verbs=create;update,versions=v1alpha1,name=validating.llmisvc.odh-model-controller.opendatahub.io,admissionReviewVersions=v1

// LLMInferenceServiceTierValidator struct is responsible for validating the LLMInferenceService resource
// when it is created or updated. The validation is based on the tier annotation.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as this struct is used only for temporary operations and does not need to be deeply copied.
type LLMInferenceServiceTierValidator struct {
	client           client.Client
	tierConfigLoader *reconcilers.TierConfigLoader
}

var _ webhook.CustomValidator = &LLMInferenceServiceTierValidator{}

func (v *LLMInferenceServiceTierValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	llmisvc, ok := obj.(*kservev1alpha1.LLMInferenceService)
	if !ok {
		return nil, fmt.Errorf("expected a LLMInferenceService object but got %T", obj)
	}
	logger := llminferenceservicelog.WithValues("namespace", llmisvc.Namespace, "llmisvc", llmisvc.GetName())
	logger.Info("Validation for LLMInferenceService upon creation")

	return v.validate(ctx, llmisvc)
}

func (v *LLMInferenceServiceTierValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	llmisvc, ok := newObj.(*kservev1alpha1.LLMInferenceService)
	if !ok {
		return nil, fmt.Errorf("expected a LLMInferenceService object but got %T", newObj)
	}

	logger := llminferenceservicelog.WithValues("namespace", llmisvc.Namespace, "llmisvc", llmisvc.GetName())
	logger.Info("Validation for LLMInferenceService upon update")

	return v.validate(ctx, llmisvc)
}

func (v *LLMInferenceServiceTierValidator) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	// No validation needed on delete
	return nil, nil
}

func (v *LLMInferenceServiceTierValidator) validate(ctx context.Context, llmisvc *kservev1alpha1.LLMInferenceService) (admission.Warnings, error) {
	logger := llminferenceservicelog.WithValues("namespace", llmisvc.Namespace, "llmisvc", llmisvc.GetName())
	logger.Info("Validating tier annotation")

	requestedTiers, availableTiers, err := v.tierConfigLoader.ValidateTierAnnotation(ctx, logger, llmisvc)
	if err != nil {
		var annotationNotFoundError *reconcilers.AnnotationNotFoundError
		if errors.As(err, &annotationNotFoundError) {
			// No annotation found, nothing to validate
			return nil, nil
		}

		annotationPath := field.NewPath("metadata").Child("annotations").Key(TierAnnotationKey)
		var errorMessage string

		if availableTiers != nil {
			errorMessage = fmt.Sprintf("%v. Available tiers: %v", err.Error(), availableTiers)
		} else if requestedTiers != nil {
			errorMessage = err.Error()
		} else {
			errorMessage = fmt.Sprintf("Invalid JSON format in tier annotation. Expected format: [\"tier1\", \"tier2\"] or [] for all tiers. Error: %v", err)
		}

		validationErrors := field.ErrorList{
			field.Invalid(annotationPath, llmisvc.GetAnnotations()[TierAnnotationKey], errorMessage),
		}

		logger.Info("Tier annotation validation failed", "error", err)
		return nil, k8sErrors.NewInvalid(
			schema.GroupKind{Group: llmisvc.GroupVersionKind().Group, Kind: llmisvc.Kind},
			llmisvc.GetName(),
			validationErrors)
	}

	logger.Info("Tier annotation validation passed")
	return nil, nil
}
