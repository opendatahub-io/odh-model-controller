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
	"fmt"

	kserveconstants "github.com/kserve/kserve/pkg/constants"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kservev1alpha2 "github.com/kserve/kserve/pkg/apis/serving/v1alpha2"
	"knative.dev/pkg/apis"

	"github.com/opendatahub-io/odh-model-controller/internal/webhook/connectionapi"
	"github.com/opendatahub-io/odh-model-controller/internal/webhook/serving/hardwareprofile"
)

// nolint:unused
var llmisvclog = logf.Log.WithName("llminferenceservice-resource")

// +kubebuilder:webhook:path=/mutate-serving-kserve-io-v1alpha2-llminferenceservice,mutating=true,failurePolicy=fail,sideEffects=NoneOnDryRun,groups=serving.kserve.io,resources=llminferenceservices,verbs=create;update,versions=v1alpha2,name=connection-llmisvc-v1alpha2.odh-model-controller.opendatahub.io,admissionReviewVersions=v1
// +kubebuilder:webhook:path=/mutate-serving-kserve-io-v1alpha1-llminferenceservice,mutating=true,failurePolicy=fail,sideEffects=NoneOnDryRun,groups=serving.kserve.io,resources=llminferenceservices,verbs=create;update,versions=v1alpha1,name=connection-llmisvc-v1alpha1.odh-model-controller.opendatahub.io,admissionReviewVersions=v1
// +kubebuilder:webhook:path=/validate-serving-kserve-io-v1alpha2-llminferenceservice,mutating=false,failurePolicy=fail,sideEffects=None,groups=serving.kserve.io,resources=llminferenceservices,verbs=create;update,versions=v1alpha2,name=validating.llmisvc-v1alpha2.odh-model-controller.opendatahub.io,admissionReviewVersions=v1
// +kubebuilder:webhook:path=/validate-serving-kserve-io-v1alpha1-llminferenceservice,mutating=false,failurePolicy=fail,sideEffects=None,groups=serving.kserve.io,resources=llminferenceservices,verbs=create;update,versions=v1alpha1,name=validating.llmisvc-v1alpha1.odh-model-controller.opendatahub.io,admissionReviewVersions=v1

// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create

// LLMInferenceServiceCustomDefaulter injects ConnectionsAPI credentials into LLMInferenceService resources.
type LLMInferenceServiceCustomDefaulter struct {
	client    client.Client
	apiReader client.Reader
}

// LLMInferenceServiceCustomValidator checks HardwareProfile DRA references at admission.
type LLMInferenceServiceCustomValidator struct {
	apiReader client.Reader
}

type llmISVCValidatorV1alpha2 struct {
	*LLMInferenceServiceCustomValidator
}
type llmISVCValidatorV1alpha1 struct {
	*LLMInferenceServiceCustomValidator
}

var _ admission.Validator[*kservev1alpha2.LLMInferenceService] = &llmISVCValidatorV1alpha2{}
var _ admission.Validator[*kservev1alpha1.LLMInferenceService] = &llmISVCValidatorV1alpha1{}

func (v *llmISVCValidatorV1alpha2) ValidateCreate(ctx context.Context, obj *kservev1alpha2.LLMInferenceService) (admission.Warnings, error) {
	return nil, v.validateHardwareProfileDRA(ctx, nil, &obj.ObjectMeta, obj.Spec.Template)
}

func (v *llmISVCValidatorV1alpha2) ValidateUpdate(ctx context.Context, oldObj, obj *kservev1alpha2.LLMInferenceService) (admission.Warnings, error) {
	var oldMeta *metav1.ObjectMeta
	if oldObj != nil {
		oldMeta = &oldObj.ObjectMeta
	}
	return nil, v.validateHardwareProfileDRA(ctx, oldMeta, &obj.ObjectMeta, obj.Spec.Template)
}

func (v *llmISVCValidatorV1alpha2) ValidateDelete(context.Context, *kservev1alpha2.LLMInferenceService) (admission.Warnings, error) {
	return nil, nil
}

func (v *llmISVCValidatorV1alpha1) ValidateCreate(ctx context.Context, obj *kservev1alpha1.LLMInferenceService) (admission.Warnings, error) {
	return nil, v.validateHardwareProfileDRA(ctx, nil, &obj.ObjectMeta, obj.Spec.Template)
}

func (v *llmISVCValidatorV1alpha1) ValidateUpdate(ctx context.Context, oldObj, obj *kservev1alpha1.LLMInferenceService) (admission.Warnings, error) {
	var oldMeta *metav1.ObjectMeta
	if oldObj != nil {
		oldMeta = &oldObj.ObjectMeta
	}
	return nil, v.validateHardwareProfileDRA(ctx, oldMeta, &obj.ObjectMeta, obj.Spec.Template)
}

func (v *llmISVCValidatorV1alpha1) ValidateDelete(context.Context, *kservev1alpha1.LLMInferenceService) (admission.Warnings, error) {
	return nil, nil
}

func (v *LLMInferenceServiceCustomValidator) validateHardwareProfileDRA(
	ctx context.Context, oldMeta, meta *metav1.ObjectMeta, template *corev1.PodSpec,
) error {
	name, namespace := hardwareprofile.ProfileRef(meta.Annotations, meta.Namespace)
	if name == "" {
		return nil
	}

	changed := hardwareprofile.DRAReferenceAnnotationsChanged(oldMeta, meta)
	activeTemplate := meta.Annotations[hardwareprofile.DRATemplateAnnotation]
	if changed {
		profile, err := hardwareprofile.Resolve(ctx, v.apiReader, name, namespace)
		if err != nil {
			return err
		}
		activeTemplate = profile.ResourceClaimTemplateName
	}
	if activeTemplate == "" {
		return nil
	}
	if _, managed := meta.Annotations[kserveconstants.ManagedDRADeviceClassAnnotationKey]; managed {
		return fmt.Errorf("HardwareProfile DRA cannot be combined with KServe Managed DRA (%s)",
			kserveconstants.ManagedDRADeviceClassAnnotationKey)
	}
	if template != nil && len(template.Containers) > 0 && mainContainerIndex(template.Containers) < 0 {
		return fmt.Errorf("HardwareProfile DRA requires a %q container in spec.template.containers", hardwareprofile.LLMIsvcMainContainerName)
	}
	if changed {
		return hardwareprofile.ValidateResourceClaimTemplate(ctx, v.apiReader, activeTemplate, meta.Namespace)
	}
	return nil
}

// llmISVCDefaulterV1alpha2 adapts the shared defaulter for the v1alpha2 generic webhook API.
type llmISVCDefaulterV1alpha2 struct {
	*LLMInferenceServiceCustomDefaulter
}

var _ admission.Defaulter[*kservev1alpha2.LLMInferenceService] = &llmISVCDefaulterV1alpha2{}

func (d *llmISVCDefaulterV1alpha2) Default(ctx context.Context, obj *kservev1alpha2.LLMInferenceService) error {
	if err := d.applyLLMISVCDefaults(ctx, obj, &obj.Spec.Model.URI, &obj.Spec.Template); err != nil {
		return err
	}
	return d.applyHardwareProfileLLMISVC(ctx, &obj.ObjectMeta, &obj.Spec.Template,
		"serving.kserve.io/v1alpha2", "LLMInferenceService")
}

// llmISVCDefaulterV1alpha1 adapts the shared defaulter for the v1alpha1 generic webhook API.
type llmISVCDefaulterV1alpha1 struct {
	*LLMInferenceServiceCustomDefaulter
}

var _ admission.Defaulter[*kservev1alpha1.LLMInferenceService] = &llmISVCDefaulterV1alpha1{}

func (d *llmISVCDefaulterV1alpha1) Default(ctx context.Context, obj *kservev1alpha1.LLMInferenceService) error {
	if err := d.applyLLMISVCDefaults(ctx, obj, &obj.Spec.Model.URI, &obj.Spec.Template); err != nil {
		return err
	}
	return d.applyHardwareProfileLLMISVC(ctx, &obj.ObjectMeta, &obj.Spec.Template,
		"serving.kserve.io/v1alpha1", "LLMInferenceService")
}

// SetupLLMInferenceServiceWebhookWithManager registers mutating and validating webhooks for both
// LLMInferenceService API versions using shared defaulter and validator instances.
//
// Parameters:
//   - mgr: the controller-runtime manager
//
// Returns any registration error.
func SetupLLMInferenceServiceWebhookWithManager(mgr ctrl.Manager) error {
	core := &LLMInferenceServiceCustomDefaulter{
		client:    mgr.GetClient(),
		apiReader: mgr.GetAPIReader(),
	}
	validator := &LLMInferenceServiceCustomValidator{apiReader: mgr.GetAPIReader()}
	if err := ctrl.NewWebhookManagedBy(mgr, &kservev1alpha2.LLMInferenceService{}).
		WithValidator(&llmISVCValidatorV1alpha2{validator}).
		WithDefaulter(&llmISVCDefaulterV1alpha2{core}).
		Complete(); err != nil {
		return err
	}
	return ctrl.NewWebhookManagedBy(mgr, &kservev1alpha1.LLMInferenceService{}).
		WithValidator(&llmISVCValidatorV1alpha1{validator}).
		WithDefaulter(&llmISVCDefaulterV1alpha1{core}).
		Complete()
}

// applyLLMISVCDefaults is the version-agnostic core of the LLMInferenceService webhook. It determines
// the ConnectionsAPI action and applies injection or cleanup via field pointers that are compatible
// with both v1alpha1 and v1alpha2.
//
// Parameters:
//   - ctx: context carrying the admission request
//   - obj: the admitted resource as metav1.Object (for metadata access)
//   - modelURI: pointer to the spec.model.uri field
//   - template: pointer to the spec.template field pointer
//
// Returns any error that should block admission.
func (d *LLMInferenceServiceCustomDefaulter) applyLLMISVCDefaults(
	ctx context.Context,
	obj metav1.Object,
	modelURI *apis.URL,
	template **corev1.PodSpec,
) error {
	logger := llmisvclog.WithValues("name", obj.GetName())
	logger.Info("Defaulting for LLMInferenceService")

	if obj.GetDeletionTimestamp() != nil {
		return nil
	}

	req, err := admission.RequestFromContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to get admission request from context: %w", err)
	}

	newConn, secret, err := connectionapi.ValidateConnectionAnnotation(ctx, d.apiReader, obj, req.Namespace)
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
		if err := connectionapi.CreateServiceAccountIfNeeded(ctx, d.client, newConn.SecretName, newConn.Type, req.Namespace, isDryRun); err != nil {
			return err
		}
		if err := performLLMISVCInjection(ctx, modelURI, template, newConn, secret); err != nil {
			log.Error(err, "failed to inject connection")
			return err
		}
		connectionapi.SetInjectedConnectionType(obj, newConn.Type)

	case connectionapi.ConnectionActionRemove:
		performLLMISVCCleanup(modelURI, template, oldConn)
		connectionapi.RemoveInjectedConnectionType(obj)

	case connectionapi.ConnectionActionReplace:
		log.V(1).Info("connection changed, performing replacement",
			"oldType", oldConn.Type, "newType", newConn.Type,
			"oldSecret", oldConn.SecretName, "newSecret", newConn.SecretName)
		performLLMISVCCleanup(modelURI, template, oldConn)
		isDryRun := req.DryRun != nil && *req.DryRun
		if err := connectionapi.CreateServiceAccountIfNeeded(ctx, d.client, newConn.SecretName, newConn.Type, req.Namespace, isDryRun); err != nil {
			return err
		}
		if err := performLLMISVCInjection(ctx, modelURI, template, newConn, secret); err != nil {
			log.Error(err, "failed to inject new connection")
			return err
		}
		connectionapi.SetInjectedConnectionType(obj, newConn.Type)

	case connectionapi.ConnectionActionNone:
		// no-op
	}

	return nil
}

// performLLMISVCInjection injects connection credentials into an LLMInferenceService using field
// pointers, making it compatible with both v1alpha1 and v1alpha2.
//
// S3 injects serviceAccountName and sets spec.model.uri to s3://{bucket}/{path}.
// URI sets spec.model.uri from the Secret's https-host or URI key.
// OCI appends to spec.template.imagePullSecrets.
//
// Parameters:
//   - ctx: context for logging
//   - modelURI: pointer to the spec.model.uri field
//   - template: pointer to the spec.template field pointer
//   - connInfo: validated connection info
//   - secret: the pre-fetched connection Secret (returned by ValidateConnectionAnnotation)
//
// Returns any error from injection.
func performLLMISVCInjection(
	ctx context.Context,
	modelURI *apis.URL,
	template **corev1.PodSpec,
	connInfo connectionapi.ConnectionInfo,
	secret *corev1.Secret,
) error {
	log := logf.FromContext(ctx)

	switch connInfo.Type {
	case connectionapi.ConnectionTypeProtocolOCI.String(), connectionapi.ConnectionTypeRefOCI.String():
		if *template == nil {
			*template = &corev1.PodSpec{}
		}
		connectionapi.InjectOCIImagePullSecrets(&(*template).ImagePullSecrets, connInfo.SecretName)
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
		*modelURI = *parsed
		log.V(1).Info("injected URI spec.model.uri", "connection", connInfo.SecretName)
		return nil

	case connectionapi.ConnectionTypeProtocolS3.String(), connectionapi.ConnectionTypeRefS3.String():
		if *template == nil {
			*template = &corev1.PodSpec{}
		}
		connectionapi.InjectServiceAccountName(&(*template).ServiceAccountName, connInfo.SecretName+"-sa")
		log.V(1).Info("injected serviceAccountName", "saName", connInfo.SecretName+"-sa")

		s3URI, err := connectionapi.BuildS3URI(secret, connInfo)
		if err != nil {
			return fmt.Errorf("failed to build S3 URI: %w", err)
		}
		parsed, err := apis.ParseURL(s3URI)
		if err != nil {
			return fmt.Errorf("invalid S3 URI %q: %w", s3URI, err)
		}
		*modelURI = *parsed
		log.V(1).Info("injected S3 spec.model.uri", "connection", connInfo.SecretName)
		return nil

	default:
		log.V(1).Info("unknown connection type, skipping injection", "connectionType", connInfo.Type)
		return nil
	}
}

// applyHardwareProfileLLMISVC applies HardwareProfile scheduling stanzas to an LLMInferenceService
// at admission time.
//
// Resolves the HardwareProfile referenced by opendatahub.io/hardware-profile-name annotation and
// injects container resources, nodeSelector, tolerations, and the Kueue queue label. On UPDATE
// where the annotation is removed, surgically cleans up previously-injected values.
//
// When spec.template.containers is non-empty but has no "main", a DRA profile is rejected.
// For profiles without DRA, the existing Warning Event and skip behavior is preserved.
//
// Parameters:
//   - ctx: context with logger and admission request
//   - meta: the LLMInferenceService ObjectMeta (mutated in-place for labels/annotations)
//   - template: pointer to the spec.template PodSpec pointer (created if nil and injection is needed)
//   - apiVersion: the API version string used when emitting events (e.g. "serving.kserve.io/v1alpha2")
//   - kind: the kind string used when emitting events
//
// Returns any error that should block admission.
func (d *LLMInferenceServiceCustomDefaulter) applyHardwareProfileLLMISVC(
	ctx context.Context,
	meta *metav1.ObjectMeta,
	template **corev1.PodSpec,
	apiVersion, kind string,
) error {
	log := logf.FromContext(ctx)

	if meta.DeletionTimestamp != nil {
		return nil
	}

	req, err := admission.RequestFromContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to get admission request from context: %w", err)
	}

	annotations := meta.GetAnnotations()
	profileName, profileNamespace := hardwareprofile.ProfileRef(annotations, meta.Namespace)

	if profileName == "" {
		if err := applyLLMHardwareProfileDRA(req, meta, template, ""); err != nil {
			return err
		}
		if req.Operation == admissionv1.Update {
			return d.handleHWPRemovalLLMISVC(ctx, req, meta, template)
		}
		return nil
	}

	// Resolve the HardwareProfile CR.
	profile, err := hardwareprofile.Resolve(ctx, d.apiReader, profileName, profileNamespace)
	if err != nil {
		if k8serr.IsNotFound(err) {
			return fmt.Errorf("hardware profile %q not found in namespace %q", profileName, profileNamespace)
		}
		return fmt.Errorf("failed fetching hardware profile: %w", err)
	}

	// Stamp the namespace annotation so callers can always find the namespace.
	if annotations[hardwareprofile.HardwareProfileAnnotationNamespace] == "" {
		if meta.Annotations == nil {
			meta.Annotations = make(map[string]string)
		}
		meta.Annotations[hardwareprofile.HardwareProfileAnnotationNamespace] = profileNamespace
	}
	if err := applyLLMHardwareProfileDRA(req, meta, template, profile.ResourceClaimTemplateName); err != nil {
		return err
	}

	// Validate container names after HWP fetch (matches opendatahub-operator ordering).
	// When containers is non-empty but has no "main", emit an event and skip all HWP injection.
	if *template != nil && len((*template).Containers) > 0 && mainContainerIndex((*template).Containers) < 0 {
		log.Info("LLMIsvc containers non-empty but has no 'main' container, skipping all HWP injection",
			"name", meta.Name, "namespace", meta.Namespace, "profile", profileName)
		d.emitContainerValidationEvent(ctx, meta, apiVersion, kind, profileName)
		return nil
	}

	profileChanged := hardwareprofile.ProfileChanged(req, profileName, profileNamespace)

	// On profile switch, clear all previous scheduling stanzas before applying the new profile.
	if profileChanged {
		if *template != nil {
			(*template).NodeSelector = nil
			(*template).Tolerations = nil
		}
		if meta.Labels != nil {
			delete(meta.Labels, hardwareprofile.KueueQueueNameLabel)
		}
	}

	// Apply resource identifiers to the "main" container (existing values take priority).
	var existingContainers []corev1.Container
	if *template != nil {
		existingContainers = (*template).Containers
	}
	newContainers, err := hardwareprofile.ApplyToLLMIsvcResources(profile, existingContainers)
	if err != nil {
		return fmt.Errorf("failed to apply HWP resources to LLMIsvc: %w", err)
	}
	if *template == nil && len(newContainers) > 0 {
		*template = &corev1.PodSpec{}
	}
	if *template != nil {
		(*template).Containers = newContainers
	}

	// Kueue and node scheduling are mutually exclusive.
	if profile.KueueQueueName != "" {
		for _, w := range hardwareprofile.ApplyKueueLabel(profile, meta, profileChanged, profileName) {
			log.Info(w)
		}
		return nil
	}

	// Apply node scheduling.
	if len(profile.NodeSelector) > 0 || len(profile.Tolerations) > 0 {
		if *template == nil {
			*template = &corev1.PodSpec{}
		}
		var existingNS map[string]string
		var existingTols []corev1.Toleration
		existingNS = (*template).NodeSelector
		existingTols = (*template).Tolerations

		var newNS map[string]string
		var newTols []corev1.Toleration
		if profileChanged {
			newNS, newTols = hardwareprofile.SetNodeScheduling(profile, existingNS, existingTols)
		} else {
			var warnings []string
			newNS, newTols, warnings = hardwareprofile.MergeNodeScheduling(profile, existingNS, existingTols, profileName)
			for _, w := range warnings {
				log.Info(w)
			}
		}
		(*template).NodeSelector = newNS
		(*template).Tolerations = newTols
	}

	return nil
}

func mainContainerIndex(containers []corev1.Container) int {
	for i := range containers {
		if containers[i].Name == hardwareprofile.LLMIsvcMainContainerName {
			return i
		}
	}
	return -1
}

func applyLLMHardwareProfileDRA(req admission.Request, meta *metav1.ObjectMeta, template **corev1.PodSpec, selectedTemplate string) error {
	oldTemplate, referenceChanged, err := hardwareprofile.DRAStateFromRequest(req, meta)
	if err != nil {
		return err
	}
	desired := hardwareprofile.DesiredDRATemplate(selectedTemplate, oldTemplate, referenceChanged)
	if desired != "" {
		if _, managed := meta.Annotations[kserveconstants.ManagedDRADeviceClassAnnotationKey]; managed {
			return fmt.Errorf("HardwareProfile DRA cannot be combined with KServe Managed DRA (%s)",
				kserveconstants.ManagedDRADeviceClassAnnotationKey)
		}
	}
	return reconcileLLMHardwareProfileDRA(meta, template, oldTemplate, selectedTemplate, referenceChanged)
}

func reconcileLLMHardwareProfileDRA(
	meta *metav1.ObjectMeta, template **corev1.PodSpec,
	oldTemplate, selectedTemplate string, referenceChanged bool,
) error {
	desired := hardwareprofile.DesiredDRATemplate(selectedTemplate, oldTemplate, referenceChanged)
	if desired != "" && *template == nil {
		*template = &corev1.PodSpec{}
	}
	if *template == nil {
		var claims []corev1.PodResourceClaim
		return hardwareprofile.ReconcileDRAClaims(meta, &claims, nil, oldTemplate, selectedTemplate, referenceChanged)
	}
	if referenceChanged && oldTemplate != "" {
		hardwareprofile.RemoveDRAContainerClaims((*template).Containers, (*template).InitContainers,
			(*template).EphemeralContainers)
	}
	if desired != "" && len((*template).Containers) == 0 {
		(*template).Containers = []corev1.Container{{Name: hardwareprofile.LLMIsvcMainContainerName}}
	}
	var resources *corev1.ResourceRequirements
	idx := mainContainerIndex((*template).Containers)
	if idx >= 0 {
		resources = &(*template).Containers[idx].Resources
	}
	if desired != "" && resources == nil {
		return fmt.Errorf("HardwareProfile DRA requires a %q container in spec.template.containers", hardwareprofile.LLMIsvcMainContainerName)
	}
	if desired != "" {
		if err := hardwareprofile.ValidateDRAContainerClaims((*template).Containers, (*template).InitContainers,
			(*template).EphemeralContainers, idx); err != nil {
			return err
		}
	}
	return hardwareprofile.ReconcileDRAClaims(meta, &(*template).ResourceClaims, resources,
		oldTemplate, selectedTemplate, referenceChanged)
}

// handleHWPRemovalLLMISVC handles the case where the HWP annotation is removed from an
// LLMInferenceService on UPDATE. Fetches the old HWP and surgically removes only its contributed
// nodeSelector entries, tolerations, and Kueue label. When the old HWP cannot be fetched, logs a
// warning and removes only the namespace annotation (does not block admission).
//
// Parameters:
//   - ctx: context with logger
//   - req: the admission request containing the old object
//   - meta: the LLMInferenceService ObjectMeta (mutated in-place)
//   - template: pointer to the spec.template PodSpec pointer (mutated in-place)
//
// Returns any error that should block admission (only internal errors, not "old HWP not found").
func (d *LLMInferenceServiceCustomDefaulter) handleHWPRemovalLLMISVC(
	ctx context.Context,
	req admission.Request,
	meta *metav1.ObjectMeta,
	template **corev1.PodSpec,
) error {
	log := logf.FromContext(ctx)

	if req.OldObject.Raw == nil {
		return nil
	}
	oldObj := &unstructured.Unstructured{}
	if err := oldObj.UnmarshalJSON(req.OldObject.Raw); err != nil {
		return nil
	}

	oldAnnotations := oldObj.GetAnnotations()
	oldProfileName := oldAnnotations[hardwareprofile.HardwareProfileAnnotationName]
	if oldProfileName == "" {
		return nil
	}

	oldProfileNamespace := oldAnnotations[hardwareprofile.HardwareProfileAnnotationNamespace]
	if oldProfileNamespace == "" {
		oldProfileNamespace = oldObj.GetNamespace()
	}

	oldProfile, err := hardwareprofile.Resolve(ctx, d.client, oldProfileName, oldProfileNamespace)
	if err != nil {
		log.Info("could not fetch old HWP for LLMIsvc cleanup — stanzas may remain in spec",
			"error", err, "profile", oldProfileName, "namespace", oldProfileNamespace)
		if meta.Annotations != nil {
			delete(meta.Annotations, hardwareprofile.HardwareProfileAnnotationNamespace)
		}
		return nil
	}

	if oldProfile != nil {
		if *template != nil {
			newNS, newTols := hardwareprofile.RemoveNodeScheduling(
				oldProfile, (*template).NodeSelector, (*template).Tolerations)
			(*template).NodeSelector = newNS
			(*template).Tolerations = newTols
		}
		if oldProfile.KueueQueueName != "" {
			hardwareprofile.RemoveKueueLabel(meta)
		}
	}

	if meta.Annotations != nil {
		delete(meta.Annotations, hardwareprofile.HardwareProfileAnnotationNamespace)
	}

	return nil
}

// emitContainerValidationEvent creates a Warning Kubernetes Event on the LLMInferenceService when
// its container list is non-empty but contains no container named "main". This provides persistent
// traceability without blocking admission.
//
// Parameters:
//   - ctx: context with logger
//   - meta: the LLMInferenceService ObjectMeta (provides name, namespace, UID)
//   - apiVersion: the API version string for the InvolvedObject reference
//   - kind: the kind string for the InvolvedObject reference
//   - profileName: the referenced HardwareProfile name, used in the event message
func (d *LLMInferenceServiceCustomDefaulter) emitContainerValidationEvent(
	ctx context.Context,
	meta *metav1.ObjectMeta,
	apiVersion, kind, profileName string,
) {
	log := logf.FromContext(ctx)

	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s.hwp-container-name-mismatch", meta.Name),
			Namespace: meta.Namespace,
		},
		InvolvedObject: corev1.ObjectReference{
			APIVersion: apiVersion,
			Kind:       kind,
			Namespace:  meta.Namespace,
			Name:       meta.Name,
			UID:        meta.UID,
		},
		Reason: "ContainerNameMismatch",
		Message: fmt.Sprintf(
			"HardwareProfile %q was not applied: no container named %q found in spec.template.containers. "+
				"Rename a container to %q to enable HWP injection.",
			profileName, hardwareprofile.LLMIsvcMainContainerName, hardwareprofile.LLMIsvcMainContainerName),
		Source: corev1.EventSource{
			Component: "hardwareprofile-webhook",
		},
		FirstTimestamp: metav1.Now(),
		LastTimestamp:  metav1.Now(),
		Count:          1,
		Type:           corev1.EventTypeWarning,
	}

	if err := d.client.Create(ctx, event); err != nil {
		log.Info("failed to create container validation warning event (non-blocking)", "error", err)
	}
}

// performLLMISVCCleanup removes previously injected connection credentials using field pointers,
// making it compatible with both v1alpha1 and v1alpha2.
//
// Dispatches cleanup based on the old connection type. Unknown type triggers full cleanup across
// all possible injected fields.
//
// Parameters:
//   - modelURI: pointer to the spec.model.uri field
//   - template: pointer to the spec.template field pointer
//   - oldConn: connection info from the previous object version
func performLLMISVCCleanup(
	modelURI *apis.URL,
	template **corev1.PodSpec,
	oldConn connectionapi.ConnectionInfo,
) {
	switch oldConn.Type {
	case connectionapi.ConnectionTypeProtocolS3.String(), connectionapi.ConnectionTypeRefS3.String():
		if *template != nil {
			connectionapi.RemoveServiceAccountName(&(*template).ServiceAccountName, oldConn.SecretName+"-sa")
		}
		if modelURI.String() != "" {
			*modelURI = apis.URL{}
		}

	case connectionapi.ConnectionTypeProtocolOCI.String(), connectionapi.ConnectionTypeRefOCI.String():
		if *template != nil {
			if oldConn.SecretName != "" {
				connectionapi.CleanupOCIImagePullSecrets(&(*template).ImagePullSecrets, oldConn.SecretName)
			} else {
				(*template).ImagePullSecrets = nil
			}
		}

	case connectionapi.ConnectionTypeProtocolURI.String(), connectionapi.ConnectionTypeRefURI.String():
		if modelURI.String() != "" {
			*modelURI = apis.URL{}
		}

	case "":
		// Unknown type: perform full cleanup of all possible injected fields.
		if *template != nil {
			connectionapi.RemoveServiceAccountName(&(*template).ServiceAccountName, oldConn.SecretName+"-sa")
			if oldConn.SecretName != "" {
				connectionapi.CleanupOCIImagePullSecrets(&(*template).ImagePullSecrets, oldConn.SecretName)
			} else {
				(*template).ImagePullSecrets = nil
			}
		}
		if modelURI.String() != "" {
			*modelURI = apis.URL{}
		}
	}
}
