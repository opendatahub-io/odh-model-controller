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

package v1alpha2

import (
	"context"
	"encoding/json"
	"fmt"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	kservev1alpha2 "github.com/kserve/kserve/pkg/apis/serving/v1alpha2"
	"knative.dev/pkg/apis"

	"github.com/opendatahub-io/odh-model-controller/internal/webhook/connectionapi"
)

// nolint:unused
var llmisvclog = logf.Log.WithName("llminferenceservice-resource")

// +kubebuilder:webhook:path=/mutate-serving-kserve-io-v1alpha2-llminferenceservice,mutating=true,failurePolicy=fail,sideEffects=NoneOnDryRun,groups=serving.kserve.io,resources=llminferenceservices,verbs=create;update,versions=v1alpha1;v1alpha2,name=connection-llmisvc.odh-model-controller.opendatahub.io,admissionReviewVersions=v1

// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create

// LLMInferenceServiceCustomDefaulter injects ConnectionsAPI credentials into LLMInferenceService resources.
type LLMInferenceServiceCustomDefaulter struct {
	client    client.Client
	apiReader client.Reader
}

var _ webhook.CustomDefaulter = &LLMInferenceServiceCustomDefaulter{}

// SetupLLMInferenceServiceWebhookWithManager registers the LLMInferenceService mutating webhook with the manager.
//
// Parameters:
//   - mgr: the controller-runtime manager
//
// Returns any registration error.
func SetupLLMInferenceServiceWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(&kservev1alpha2.LLMInferenceService{}).
		WithDefaulter(&LLMInferenceServiceCustomDefaulter{
			client:    mgr.GetClient(),
			apiReader: mgr.GetAPIReader(),
		}).
		Complete()
}

// Default implements webhook.CustomDefaulter. It applies ConnectionsAPI injection or cleanup to the
// LLMInferenceService based on the opendatahub.io/connections annotation.
//
// Parameters:
//   - ctx: context carrying the admission request (via admission.RequestFromContext)
//   - obj: the LLMInferenceService object to mutate
//
// Returns any error that should block admission.
func (d *LLMInferenceServiceCustomDefaulter) Default(ctx context.Context, obj runtime.Object) error {
	llmisvc, ok := obj.(*kservev1alpha2.LLMInferenceService)
	if !ok {
		return fmt.Errorf("expected a LLMInferenceService object but got %T", obj)
	}
	logger := llmisvclog.WithValues("name", llmisvc.GetName())
	logger.Info("Defaulting for LLMInferenceService")

	if llmisvc.DeletionTimestamp != nil {
		return nil
	}

	req, err := admission.RequestFromContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to get admission request from context: %w", err)
	}

	newConn, secret, err := connectionapi.ValidateConnectionAnnotation(ctx, d.apiReader, llmisvc, req.Namespace)
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

	log := logf.FromContext(ctx)

	switch action {
	case connectionapi.ConnectionActionInject:
		isDryRun := req.DryRun != nil && *req.DryRun
		if err := connectionapi.ServiceAccountCreation(ctx, d.client, newConn.SecretName, newConn.Type, req.Namespace, isDryRun); err != nil {
			return err
		}
		if err := performLLMISVCInjection(ctx, req, llmisvc, newConn, secret); err != nil {
			log.Error(err, "failed to inject connection")
			return err
		}

	case connectionapi.ConnectionActionRemove:
		if err := performLLMISVCCleanup(req, llmisvc, oldConn); err != nil {
			log.Error(err, "failed to cleanup connection")
			return err
		}

	case connectionapi.ConnectionActionReplace:
		log.V(1).Info("connection changed, performing replacement",
			"oldType", oldConn.Type, "newType", newConn.Type,
			"oldSecret", oldConn.SecretName, "newSecret", newConn.SecretName)
		if err := performLLMISVCCleanup(req, llmisvc, oldConn); err != nil {
			log.Error(err, "failed to cleanup old connection")
			return err
		}
		isDryRun := req.DryRun != nil && *req.DryRun
		if err := connectionapi.ServiceAccountCreation(ctx, d.client, newConn.SecretName, newConn.Type, req.Namespace, isDryRun); err != nil {
			return err
		}
		if err := performLLMISVCInjection(ctx, req, llmisvc, newConn, secret); err != nil {
			log.Error(err, "failed to inject new connection")
			return err
		}

	case connectionapi.ConnectionActionNone:
		// no-op
	}

	return nil
}

// performLLMISVCInjection injects connection credentials into the LLMInferenceService using typed field access.
//
// S3 injects serviceAccountName and sets spec.model.uri to s3://{bucket}/{path}.
// URI sets spec.model.uri from the Secret's https-host or URI key.
// OCI appends to spec.template.imagePullSecrets.
//
// Parameters:
//   - ctx: context for logging
//   - req: the admission request (carries namespace)
//   - llmisvc: the LLMInferenceService to mutate
//   - connInfo: validated connection info
//   - secret: the pre-fetched connection Secret (returned by ValidateConnectionAnnotation)
//
// Returns any error from injection.
func performLLMISVCInjection(
	ctx context.Context,
	req admission.Request,
	llmisvc *kservev1alpha2.LLMInferenceService,
	connInfo connectionapi.ConnectionInfo,
	secret *corev1.Secret,
) error {
	log := logf.FromContext(ctx)

	switch connInfo.Type {
	case connectionapi.ConnectionTypeProtocolOCI.String(), connectionapi.ConnectionTypeRefOCI.String():
		if llmisvc.Spec.Template == nil {
			llmisvc.Spec.Template = &corev1.PodSpec{}
		}
		connectionapi.InjectOCIImagePullSecrets(&llmisvc.Spec.Template.ImagePullSecrets, connInfo.SecretName)
		log.V(1).Info("injected OCI imagePullSecrets", "connection", connInfo.SecretName)
		// TODO: inject spec.model.uri for OCI
		return nil

	case connectionapi.ConnectionTypeProtocolURI.String(), connectionapi.ConnectionTypeRefURI.String():
		uriValue, err := connectionapi.GetURIValue(secret)
		if err != nil {
			return fmt.Errorf("failed to get URI value: %w", err)
		}
		parsed, err := apis.ParseURL(uriValue)
		if err != nil {
			return fmt.Errorf("invalid URI %q from secret %s: %w", uriValue, connInfo.SecretName, err)
		}
		llmisvc.Spec.Model.URI = *parsed
		log.V(1).Info("injected URI spec.model.uri", "connection", connInfo.SecretName)
		return nil

	case connectionapi.ConnectionTypeProtocolS3.String(), connectionapi.ConnectionTypeRefS3.String():
		if llmisvc.Spec.Template == nil {
			llmisvc.Spec.Template = &corev1.PodSpec{}
		}
		connectionapi.InjectServiceAccountName(&llmisvc.Spec.Template.ServiceAccountName, connInfo.SecretName+"-sa")
		log.V(1).Info("injected serviceAccountName", "saName", connInfo.SecretName+"-sa")

		s3URI, err := connectionapi.BuildS3URI(secret, connInfo)
		if err != nil {
			return fmt.Errorf("failed to build S3 URI: %w", err)
		}
		parsed, err := apis.ParseURL(s3URI)
		if err != nil {
			return fmt.Errorf("invalid S3 URI %q: %w", s3URI, err)
		}
		llmisvc.Spec.Model.URI = *parsed
		log.V(1).Info("injected S3 spec.model.uri", "connection", connInfo.SecretName)
		return nil

	default:
		log.V(1).Info("unknown connection type, skipping injection", "connectionType", connInfo.Type)
		return nil
	}
}

// performLLMISVCCleanup removes previously injected connection credentials from the LLMInferenceService.
//
// Phase 1: type-specific cleanup of serviceAccountName and imagePullSecrets (typed field access).
// Phase 2: unconditional removal of spec.model if spec.model.uri is set, using the hybrid
// unstructured approach required because LLMInferenceServiceSpec.Model is a non-pointer,
// non-omitempty field whose URI sub-field has type apis.URL (not a plain string).
//
// Parameters:
//   - req: the admission request (used for namespace in log messages)
//   - llmisvc: the LLMInferenceService to clean up
//   - oldConn: connection info from the previous object version
//
// Returns any error from cleanup.
func performLLMISVCCleanup(
	req admission.Request,
	llmisvc *kservev1alpha2.LLMInferenceService,
	oldConn connectionapi.ConnectionInfo,
) error {
	// Phase 1: type-specific cleanup of typed fields.
	switch oldConn.Type {
	case connectionapi.ConnectionTypeProtocolS3.String(), connectionapi.ConnectionTypeRefS3.String():
		if llmisvc.Spec.Template != nil {
			connectionapi.RemoveServiceAccountName(&llmisvc.Spec.Template.ServiceAccountName, oldConn.SecretName+"-sa")
		}

	case connectionapi.ConnectionTypeProtocolOCI.String(), connectionapi.ConnectionTypeRefOCI.String():
		if llmisvc.Spec.Template != nil {
			if oldConn.SecretName != "" {
				connectionapi.CleanupOCIImagePullSecrets(&llmisvc.Spec.Template.ImagePullSecrets, oldConn.SecretName)
			} else {
				llmisvc.Spec.Template.ImagePullSecrets = nil
			}
		}

	case connectionapi.ConnectionTypeProtocolURI.String(), connectionapi.ConnectionTypeRefURI.String():
		// URI type only uses spec.model.uri, handled in Phase 2.

	case "":
		// Unknown type: perform full cleanup of all possible injected typed fields.
		if llmisvc.Spec.Template != nil {
			connectionapi.RemoveServiceAccountName(&llmisvc.Spec.Template.ServiceAccountName, oldConn.SecretName+"-sa")
			if oldConn.SecretName != "" {
				connectionapi.CleanupOCIImagePullSecrets(&llmisvc.Spec.Template.ImagePullSecrets, oldConn.SecretName)
			} else {
				llmisvc.Spec.Template.ImagePullSecrets = nil
			}
		}
	}

	// Phase 2: remove spec.model when spec.model.uri is set.
	// Uses the hybrid unstructured approach because Model is a non-pointer non-omitempty struct
	// and its URI field type (apis.URL) cannot be zeroed without failing CRD validation.
	if llmisvc.Spec.Model.URI.String() != "" {
		if err := removeModelSpec(llmisvc); err != nil {
			return fmt.Errorf("failed to remove spec.model: %w", err)
		}
	}

	return nil
}

// removeModelSpec removes the entire spec.model field from the LLMInferenceService using a hybrid
// unstructured approach.
//
// This is necessary because LLMInferenceServiceSpec.Model is a non-pointer non-omitempty struct
// and its URI field has type apis.URL whose zero value fails CRD validation. The approach:
//  1. Marshals the object to JSON.
//  2. Unmarshals into an Unstructured map and removes the "spec.model" field.
//  3. Unmarshals the cleaned JSON back into the typed struct.
//  4. Replaces the content of llmisvc with the cleaned version.
//
// Parameters:
//   - llmisvc: pointer to the LLMInferenceService whose spec.model should be removed
//
// Returns any marshaling/unmarshaling error.
func removeModelSpec(llmisvc *kservev1alpha2.LLMInferenceService) error {
	jsonBytes, err := json.Marshal(llmisvc)
	if err != nil {
		return fmt.Errorf("failed to marshal LLMInferenceService: %w", err)
	}

	u := &unstructured.Unstructured{}
	if err := json.Unmarshal(jsonBytes, &u.Object); err != nil {
		return fmt.Errorf("failed to unmarshal to unstructured: %w", err)
	}

	unstructured.RemoveNestedField(u.Object, "spec", "model")

	cleanedJSON, err := json.Marshal(u.Object)
	if err != nil {
		return fmt.Errorf("failed to marshal cleaned object: %w", err)
	}

	cleaned := &kservev1alpha2.LLMInferenceService{}
	if err := json.Unmarshal(cleanedJSON, cleaned); err != nil {
		return fmt.Errorf("failed to unmarshal cleaned object back to typed struct: %w", err)
	}

	*llmisvc = *cleaned
	return nil
}
