/*
Copyright 2024.

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

package v1beta1

import (
	"context"
	"fmt"

	servingv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
)

// nolint:unused
// log is for logging in this package.
var inferenceservicelog = logf.Log.WithName("inferenceservice-resource")

// SetupInferenceServiceWebhookWithManager registers the webhook for InferenceService in the manager.
func SetupInferenceServiceWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).For(&servingv1beta1.InferenceService{}).
		WithValidator(&InferenceServiceCustomValidator{client: mgr.GetClient()}).
		WithDefaulter(&InferenceServiceCustomDefaulter{client: mgr.GetClient()}).
		Complete()
}

// NOTE: The 'path' attribute must follow a specific pattern and should not be modified directly here.
// Modifying the path for an invalid path can cause API server errors; failing to locate the webhook.
// +kubebuilder:webhook:path=/validate-serving-kserve-io-v1beta1-inferenceservice,mutating=false,failurePolicy=fail,sideEffects=None,groups=serving.kserve.io,resources=inferenceservices,verbs=create,versions=v1beta1,name=validating.isvc.odh-model-controller.opendatahub.io,admissionReviewVersions=v1

// InferenceServiceCustomValidator struct is responsible for validating the InferenceService resource
// when it is created, updated, or deleted.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as this struct is used only for temporary operations and does not need to be deeply copied.
type InferenceServiceCustomValidator struct {
	client client.Client
}

var _ webhook.CustomValidator = &InferenceServiceCustomValidator{}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type InferenceService.
func (v *InferenceServiceCustomValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	inferenceservice, ok := obj.(*servingv1beta1.InferenceService)
	if !ok {
		return nil, fmt.Errorf("expected a InferenceService object but got %T", obj)
	}
	logger := inferenceservicelog.WithValues("namespace", inferenceservice.Namespace, "isvc", inferenceservice.GetName())
	logger.Info("Validation for InferenceService upon creation")

	// Validate the InferenceService name length
	if err := utils.ValidateInferenceServiceNameLength(inferenceservice); err != nil {
		logger.V(1).Info("InferenceService name validation failed", "name", inferenceservice.GetName())
		return nil, err
	}

	appNamespace, err := utils.GetApplicationNamespace(ctx, v.client)
	if err != nil {
		return nil, err
	}

	logger.Info("Checking if namespace is protected", "namespace", inferenceservice.Namespace, "protectedNamespace", appNamespace)
	if inferenceservice.Namespace == appNamespace {
		logger.V(1).Info("Namespace is protected, the InferenceService will not be created")
		return nil, errors.NewInvalid(
			schema.GroupKind{Group: inferenceservice.GroupVersionKind().Group, Kind: inferenceservice.Kind},
			inferenceservice.GetName(),
			field.ErrorList{
				field.Invalid(field.NewPath("metadata").Child("namespace"), inferenceservice.GetNamespace(), "specified namespace is protected"),
			})
	}

	logger.Info("Namespace is not protected")
	return nil, nil
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type InferenceService.
func (v *InferenceServiceCustomValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	// Unused. Code below is from scaffolding

	inferenceservice, ok := newObj.(*servingv1beta1.InferenceService)
	if !ok {
		return nil, fmt.Errorf("expected a InferenceService object for the newObj but got %T", newObj)
	}
	inferenceservicelog.Info("Validation for InferenceService upon update", "name", inferenceservice.GetName())

	// TODO(user): fill in your validation logic upon object update.

	return nil, nil
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type InferenceService.
func (v *InferenceServiceCustomValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	// Unused. Code below is from scaffolding

	inferenceservice, ok := obj.(*servingv1beta1.InferenceService)
	if !ok {
		return nil, fmt.Errorf("expected a InferenceService object but got %T", obj)
	}
	inferenceservicelog.Info("Validation for InferenceService upon deletion", "name", inferenceservice.GetName())

	// TODO(user): fill in your validation logic upon object deletion.

	return nil, nil
}

// +kubebuilder:webhook:path=/mutate-serving-kserve-io-v1beta1-inferenceservice,mutating=true,failurePolicy=fail,sideEffects=None,groups=serving.kserve.io,resources=inferenceservices,verbs=create,versions=v1beta1,name=minferenceservice-v1beta1.odh-model-controller.opendatahub.io,admissionReviewVersions=v1

// InferenceServiceCustomDefaulter struct is responsible for setting default values on the custom resource of the
// Kind InferenceService when those are created or updated.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as it is used only for temporary operations and does not need to be deeply copied.
type InferenceServiceCustomDefaulter struct {
	client client.Client
}

var _ webhook.CustomDefaulter = &InferenceServiceCustomDefaulter{}

// Default implements webhook.CustomDefaulter so a webhook will be registered for the Kind InferenceService.
func (d *InferenceServiceCustomDefaulter) Default(ctx context.Context, obj runtime.Object) error {
	inferenceservice, ok := obj.(*servingv1beta1.InferenceService)

	if !ok {
		return fmt.Errorf("expected an InferenceService object but got %T", obj)
	}
	logger := inferenceservicelog.WithValues("name", inferenceservice.GetName())
	logger.Info("Defaulting for InferenceService", "name", inferenceservice.GetName())

	return nil
}
