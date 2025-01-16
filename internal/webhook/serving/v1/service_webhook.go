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

package v1

import (
	"context"
	"fmt"
	"strings"

	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"k8s.io/apimachinery/pkg/runtime"
	types2 "k8s.io/apimachinery/pkg/types"
	servingv1 "knative.dev/serving/pkg/apis/serving/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/resources"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
)

// nolint:unused
// log is for logging in this package.
var servicelog = logf.Log.WithName("knative-service-validating-webhook")

// SetupServiceWebhookWithManager registers the webhook for Service in the manager.
func SetupServiceWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).For(&servingv1.Service{}).
		WithValidator(&ServiceCustomValidator{client: mgr.GetClient()}).
		Complete()
}

// NOTE: The 'path' attribute must follow a specific pattern and should not be modified directly here.
// Modifying the path for an invalid path can cause API server errors; failing to locate the webhook.
// +kubebuilder:webhook:path=/validate-serving-knative-dev-v1-service,mutating=false,failurePolicy=fail,sideEffects=None,groups="serving.knative.dev",resources=services,verbs=create,versions=v1,name=validating.ksvc.odh-model-controller.opendatahub.io,admissionReviewVersions=v1

// ServiceCustomValidator struct is responsible for validating the Knative Service resource
// when it is created, updated, or deleted.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as this struct is used only for temporary operations and does not need to be deeply copied.
type ServiceCustomValidator struct {
	client client.Client
}

var _ webhook.CustomValidator = &ServiceCustomValidator{}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the Knative type Service.
func (v *ServiceCustomValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	ksvc, ok := obj.(*servingv1.Service)
	if !ok {
		return nil, fmt.Errorf("expected a Knative Service object but got %T", obj)
	}

	logger := servicelog.WithValues("namespace", ksvc.Namespace, "ksvc", ksvc.Name)
	logger.Info("Validation for Knative Service upon creation")

	// If there is an explicit intent for not having a sidecar, skip validation
	ksvcTemplateMeta := ksvc.Spec.Template.GetObjectMeta()
	// if ksvcTemplateMeta != nil {
	if templateAnnotations := ksvcTemplateMeta.GetAnnotations(); templateAnnotations[constants.IstioSidecarInjectAnnotationName] == "false" {
		logger.V(1).Info("Skipping validation of Knative Service because there is an explicit intent to exclude it from the Service Mesh")
		return nil, nil
	}
	// }

	// Only validate the KSVC if it is owned by KServe controller
	ksvcMetadata := ksvc.GetObjectMeta()
	// if ksvcMetadata == nil {
	//	logger.V(1).Info("Skipping validation of Knative Service because it does not have metadata")
	//	return nil, nil
	// }
	ksvcOwnerReferences := ksvcMetadata.GetOwnerReferences()
	if ksvcOwnerReferences == nil {
		logger.V(1).Info("Skipping validation of Knative Service because it does not have owner references")
		return nil, nil
	}
	isOwnedByKServe := false
	for _, owner := range ksvcOwnerReferences {
		if owner.Kind == constants.InferenceServiceKind {
			if strings.Contains(owner.APIVersion, kservev1beta1.SchemeGroupVersion.Group) {
				isOwnedByKServe = true
			}
		}
	}
	if !isOwnedByKServe {
		logger.V(1).Info("Skipping validation of Knative Service because it is not owned by KServe")
		return nil, nil
	}

	// Since the Ksvc is owned by an InferenceService, it is known that it is required to be
	// in the Mesh. Thus, the involved namespace needs to be enrolled in the mesh.
	// Go and check the ServiceMeshMemberRoll to verify that the namespace is already a
	// member. If it is still not a member, reject creation of the Ksvc to prevent
	// creation of a Pod that would not be in the Mesh.
	smmrQuerier := resources.NewServiceMeshMemberRole(v.client)
	_, meshNamespace := utils.GetIstioControlPlaneName(ctx, v.client)
	smmr, fetchSmmrErr := smmrQuerier.FetchSMMR(ctx, logger, types2.NamespacedName{Name: constants.ServiceMeshMemberRollName, Namespace: meshNamespace})
	if fetchSmmrErr != nil {
		logger.Error(fetchSmmrErr, "Error when fetching ServiceMeshMemberRoll", "smmr.namespace", meshNamespace, "smmr.name", constants.ServiceMeshMemberRollName)
		return nil, fetchSmmrErr
	}
	if smmr == nil {
		logger.Info("Rejecting Knative service because the ServiceMeshMemberRoll does not exist", "smmr.namespace", meshNamespace, "smmr.name", constants.ServiceMeshMemberRollName)
		return nil, fmt.Errorf("rejecting creation of Knative service %s on namespace %s because the ServiceMeshMemberRoll does not exist", ksvc.Name, ksvc.Namespace)
	}

	logger = logger.WithValues("smmr.namespace", smmr.Namespace, "smmr.name", smmr.Name)

	for _, memberNamespace := range smmr.Status.ConfiguredMembers {
		if memberNamespace == ksvc.Namespace {
			logger.V(1).Info("The Knative service is accepted")
			return nil, nil
		}
	}

	logger.Info("Rejecting Knative service because its namespace is not a member of the service mesh")
	return nil, fmt.Errorf("rejecting creation of Knative service %s on namespace %s because the namespace is not a configured member of the mesh", ksvc.Name, ksvc.Namespace)
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the Knative type Service.
func (v *ServiceCustomValidator) ValidateUpdate(_ context.Context, _, _ runtime.Object) (admission.Warnings, error) {
	// Nothing to validate on updates
	return nil, nil
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the Knative type Service.
func (v *ServiceCustomValidator) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	// Nothing to validate on updates
	return nil, nil
}
