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
	"encoding/json"
	"fmt"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	servingv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
	"github.com/opendatahub-io/odh-model-controller/internal/webhook/connectionapi"
)

// nolint:unused
// log is for logging in this package.
var inferenceservicelog = logf.Log.WithName("inferenceservice-resource")

// SetupInferenceServiceWebhookWithManager registers the webhook for InferenceService in the manager.
func SetupInferenceServiceWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).For(&servingv1beta1.InferenceService{}).
		WithValidator(&InferenceServiceCustomValidator{client: mgr.GetClient()}).
		WithDefaulter(&InferenceServiceCustomDefaulter{
			client:    mgr.GetClient(),
			apiReader: mgr.GetAPIReader(),
		}).
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

// +kubebuilder:webhook:path=/mutate-serving-kserve-io-v1beta1-inferenceservice,mutating=true,failurePolicy=fail,sideEffects=NoneOnDryRun,groups=serving.kserve.io,resources=inferenceservices,verbs=create;update,versions=v1beta1,name=minferenceservice-v1beta1.odh-model-controller.opendatahub.io,admissionReviewVersions=v1

// InferenceServiceCustomDefaulter struct is responsible for setting default values on the custom resource of the
// Kind InferenceService when those are created or updated.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as it is used only for temporary operations and does not need to be deeply copied.
type InferenceServiceCustomDefaulter struct {
	client    client.Client
	apiReader client.Reader
}

var _ webhook.CustomDefaulter = &InferenceServiceCustomDefaulter{}

// Default implements webhook.CustomDefaulter so a webhook will be registered for the Kind InferenceService.
func (d *InferenceServiceCustomDefaulter) Default(ctx context.Context, obj runtime.Object) error {
	isvc, ok := obj.(*servingv1beta1.InferenceService)
	if !ok {
		return fmt.Errorf("expected an InferenceService object but got %T", obj)
	}
	logger := inferenceservicelog.WithValues("name", isvc.GetName())
	logger.Info("Defaulting for InferenceService", "name", isvc.GetName())

	req, err := admission.RequestFromContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to get admission request from context: %w", err)
	}

	return d.applyConnectionsAPI(ctx, req, isvc)
}

// applyConnectionsAPI validates the connection annotation and performs the appropriate injection,
// cleanup, or replacement on the InferenceService based on the annotation state.
//
// Parameters:
//   - ctx: context for logging and API calls
//   - req: the admission request (carries operation type, dry-run flag, and old object)
//   - isvc: the InferenceService being admitted (mutated in-place)
//
// Returns any error that should block admission.
func (d *InferenceServiceCustomDefaulter) applyConnectionsAPI(
	ctx context.Context,
	req admission.Request,
	isvc *servingv1beta1.InferenceService,
) error {
	log := logf.FromContext(ctx)

	if isvc.DeletionTimestamp != nil {
		return nil
	}

	newConn, secret, err := connectionapi.ValidateConnectionAnnotation(ctx, d.apiReader, isvc, req.Namespace)
	if err != nil {
		return err
	}

	var oldConn connectionapi.ConnectionInfo
	var action connectionapi.ConnectionAction

	if req.Operation == admissionv1.Update {
		oldConn, err = connectionapi.GetOldConnectionInfo(ctx, req, d.apiReader)
		if err != nil {
			return err
		}
		action = connectionapi.DetermineAction(oldConn, newConn)
	} else {
		if newConn.SecretName == "" {
			action = connectionapi.ConnectionActionNone
		} else {
			action = connectionapi.ConnectionActionInject
		}
	}

	switch action {
	case connectionapi.ConnectionActionInject:
		isDryRun := req.DryRun != nil && *req.DryRun
		if err := connectionapi.ServiceAccountCreation(ctx, d.client, newConn.SecretName, newConn.Type, req.Namespace, isDryRun); err != nil {
			return err
		}
		if err := performISVCInjection(ctx, req, isvc, newConn, secret); err != nil {
			log.Error(err, "failed to inject connection")
			return err
		}

	case connectionapi.ConnectionActionRemove:
		if err := performISVCCleanup(req, isvc, oldConn); err != nil {
			log.Error(err, "failed to cleanup connection")
			return err
		}

	case connectionapi.ConnectionActionReplace:
		log.V(1).Info("connection changed, performing replacement",
			"oldType", oldConn.Type, "newType", newConn.Type,
			"oldSecret", oldConn.SecretName, "newSecret", newConn.SecretName)
		if err := performISVCCleanup(req, isvc, oldConn); err != nil {
			log.Error(err, "failed to cleanup old connection")
			return err
		}
		isDryRun := req.DryRun != nil && *req.DryRun
		if err := connectionapi.ServiceAccountCreation(ctx, d.client, newConn.SecretName, newConn.Type, req.Namespace, isDryRun); err != nil {
			return err
		}
		if err := performISVCInjection(ctx, req, isvc, newConn, secret); err != nil {
			log.Error(err, "failed to inject new connection")
			return err
		}

	case connectionapi.ConnectionActionNone:
		// no-op
	}

	return nil
}

// performISVCInjection injects connection credentials into the InferenceService using typed field access.
//
// Dispatches to type-specific injection: S3 sets serviceAccountName and storage key/path;
// URI sets storageUri; OCI appends to imagePullSecrets.
//
// Parameters:
//   - ctx: context for logging
//   - req: the admission request (used for UPDATE old-object path priority)
//   - isvc: the InferenceService to mutate
//   - connInfo: validated connection info
//   - secret: the pre-fetched connection Secret (returned by ValidateConnectionAnnotation)
//
// Returns any error from injection.
func performISVCInjection(
	ctx context.Context,
	req admission.Request,
	isvc *servingv1beta1.InferenceService,
	connInfo connectionapi.ConnectionInfo,
	secret *corev1.Secret,
) error {
	log := logf.FromContext(ctx)

	switch connInfo.Type {
	case connectionapi.ConnectionTypeProtocolOCI.String(), connectionapi.ConnectionTypeRefOCI.String():
		connectionapi.InjectOCIImagePullSecrets(&isvc.Spec.Predictor.ImagePullSecrets, connInfo.SecretName)
		log.V(1).Info("injected OCI imagePullSecrets", "connection", connInfo.SecretName)
		// TODO: inject spec.predictor.model.uri for OCI
		return nil

	case connectionapi.ConnectionTypeProtocolURI.String(), connectionapi.ConnectionTypeRefURI.String():
		uriValue, err := connectionapi.GetURIValue(secret)
		if err != nil {
			return fmt.Errorf("failed to get URI value: %w", err)
		}
		if isvc.Spec.Predictor.Model == nil {
			isvc.Spec.Predictor.Model = &servingv1beta1.ModelSpec{}
		}
		isvc.Spec.Predictor.Model.StorageURI = &uriValue
		log.V(1).Info("injected URI storageUri", "connection", connInfo.SecretName)
		return nil

	case connectionapi.ConnectionTypeProtocolS3.String(), connectionapi.ConnectionTypeRefS3.String():
		connectionapi.InjectServiceAccountName(&isvc.Spec.Predictor.ServiceAccountName, connInfo.SecretName+"-sa")
		log.V(1).Info("injected serviceAccountName", "saName", connInfo.SecretName+"-sa")

		// Retrieve the old spec storage path for priority resolution.
		var oldSpecPath string
		if req.Operation == admissionv1.Update && req.OldObject.Raw != nil {
			oldISVC := &servingv1beta1.InferenceService{}
			if err := json.Unmarshal(req.OldObject.Raw, oldISVC); err == nil {
				if oldISVC.Spec.Predictor.Model != nil &&
					oldISVC.Spec.Predictor.Model.Storage != nil &&
					oldISVC.Spec.Predictor.Model.Storage.Path != nil {
					oldSpecPath = *oldISVC.Spec.Predictor.Model.Storage.Path
				}
			}
		}

		if isvc.Spec.Predictor.Model == nil {
			isvc.Spec.Predictor.Model = &servingv1beta1.ModelSpec{}
		}
		if isvc.Spec.Predictor.Model.Storage == nil {
			isvc.Spec.Predictor.Model.Storage = &servingv1beta1.ModelStorageSpec{}
		}

		secretName := connInfo.SecretName
		isvc.Spec.Predictor.Model.Storage.StorageKey = &secretName

		// Path priority: user-set value > annotation value > old spec value > unset.
		switch {
		case isvc.Spec.Predictor.Model.Storage.Path != nil && *isvc.Spec.Predictor.Model.Storage.Path != "":
			// keep the user-set path
		case connInfo.Path != "":
			isvc.Spec.Predictor.Model.Storage.Path = &connInfo.Path
		case oldSpecPath != "":
			isvc.Spec.Predictor.Model.Storage.Path = &oldSpecPath
		}

		log.V(1).Info("injected S3 storage key and path", "connection", connInfo.SecretName)
		return nil

	default:
		log.V(1).Info("unknown connection type, skipping injection", "connectionType", connInfo.Type)
		return nil
	}
}

// performISVCCleanup removes previously injected connection credentials from the InferenceService
// using typed field access.
//
// Dispatches cleanup based on the old connection type. Unknown type triggers full cleanup across
// all possible injected fields.
//
// Parameters:
//   - req: the admission request (used for namespace in log messages)
//   - isvc: the InferenceService to clean up
//   - oldConn: connection info from the previous object version
//
// Returns any error from cleanup.
func performISVCCleanup(
	req admission.Request,
	isvc *servingv1beta1.InferenceService,
	oldConn connectionapi.ConnectionInfo,
) error {
	switch oldConn.Type {
	case connectionapi.ConnectionTypeProtocolOCI.String(), connectionapi.ConnectionTypeRefOCI.String():
		connectionapi.CleanupOCIImagePullSecrets(&isvc.Spec.Predictor.ImagePullSecrets, oldConn.SecretName)

	case connectionapi.ConnectionTypeProtocolURI.String(), connectionapi.ConnectionTypeRefURI.String():
		if isvc.Spec.Predictor.Model != nil {
			isvc.Spec.Predictor.Model.StorageURI = nil
		}

	case connectionapi.ConnectionTypeProtocolS3.String(), connectionapi.ConnectionTypeRefS3.String():
		connectionapi.RemoveServiceAccountName(&isvc.Spec.Predictor.ServiceAccountName, oldConn.SecretName+"-sa")
		if isvc.Spec.Predictor.Model != nil {
			isvc.Spec.Predictor.Model.Storage = nil
		}

	case "":
		// Unknown type: the old Secret has been deleted or was never annotated.
		// Perform full cleanup across all fields that any connection type could have set.
		if oldConn.SecretName != "" {
			connectionapi.CleanupOCIImagePullSecrets(&isvc.Spec.Predictor.ImagePullSecrets, oldConn.SecretName)
		} else {
			isvc.Spec.Predictor.ImagePullSecrets = nil
		}
		if isvc.Spec.Predictor.Model != nil {
			isvc.Spec.Predictor.Model.StorageURI = nil
			isvc.Spec.Predictor.Model.Storage = nil
		}
		connectionapi.RemoveServiceAccountName(&isvc.Spec.Predictor.ServiceAccountName, oldConn.SecretName+"-sa")
	}

	return nil
}
