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

package webhook

import (
	"context"
	"fmt"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"k8s.io/apimachinery/pkg/runtime"
	types2 "k8s.io/apimachinery/pkg/types"
	knservingv1 "knative.dev/serving/pkg/apis/serving/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/opendatahub-io/odh-model-controller/controllers/constants"
	"github.com/opendatahub-io/odh-model-controller/controllers/resources"
	"github.com/opendatahub-io/odh-model-controller/controllers/utils"
)

// +kubebuilder:webhook:admissionReviewVersions=v1,path=/validate-serving-knative-dev-v1-service,mutating=false,failurePolicy=fail,groups="serving.knative.dev",resources=services,verbs=create,versions=v1,name=validating.ksvc.odh-model-controller.opendatahub.io,sideEffects=None

type ksvcValidator struct {
	client client.Client
}

func NewKsvcValidator(client client.Client) *ksvcValidator {
	return &ksvcValidator{client: client}
}

func (v *ksvcValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (warnings admission.Warnings, err error) {
	log := logf.FromContext(ctx).WithName("KsvcValidatingWebhook")
	ksvc, ok := obj.(*knservingv1.Service)
	if !ok {
		return nil, fmt.Errorf("expected a Knative Service but got a %T", obj)
	}

	log = log.WithValues("namespace", ksvc.Namespace, "ksvc", ksvc.Name)
	log.Info("Validating Knative Service")

	// If there is an explicit intent for not having a sidecar, skip validation
	ksvcTemplateMeta := ksvc.Spec.Template.GetObjectMeta()
	if ksvcTemplateMeta != nil {
		if templateAnnotations := ksvcTemplateMeta.GetAnnotations(); templateAnnotations[constants.IstioSidecarInjectAnnotationName] == "false" {
			log.V(1).Info("Skipping validation of Knative Service because there is an explicit intent to exclude it from the Service Mesh")
			return nil, nil
		}
	}

	// Only validate the KSVC if it is owned by KServe controller
	ksvcMetadata := ksvc.GetObjectMeta()
	if ksvcMetadata == nil {
		log.V(1).Info("Skipping validation of Knative Service because it does not have metadata")
		return nil, nil
	}
	ksvcOwnerReferences := ksvcMetadata.GetOwnerReferences()
	if ksvcOwnerReferences == nil {
		log.V(1).Info("Skipping validation of Knative Service because it does not have owner references")
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
		log.V(1).Info("Skipping validation of Knative Service because it is not owned by KServe")
		return nil, nil
	}

	// Since the Ksvc is owned by an InferenceService, it is known that it is required to be
	// in the Mesh. Thus, the involved namespace needs to be enrolled in the mesh.
	// Go and check the ServiceMeshMemberRoll to verify that the namespace is already a
	// member. If it is still not a member, reject creation of the Ksvc to prevent
	// creation of a Pod that would not be in the Mesh.
	smmrQuerier := resources.NewServiceMeshMemberRole(v.client)
	_, meshNamespace := utils.GetIstioControlPlaneName(ctx, v.client)
	smmr, fetchSmmrErr := smmrQuerier.FetchSMMR(ctx, log, types2.NamespacedName{Name: constants.ServiceMeshMemberRollName, Namespace: meshNamespace})
	if fetchSmmrErr != nil {
		log.Error(fetchSmmrErr, "Error when fetching ServiceMeshMemberRoll", "smmr.namespace", meshNamespace, "smmr.name", constants.ServiceMeshMemberRollName)
		return nil, fetchSmmrErr
	}
	if smmr == nil {
		log.Info("Rejecting Knative service because the ServiceMeshMemberRoll does not exist", "smmr.namespace", meshNamespace, "smmr.name", constants.ServiceMeshMemberRollName)
		return nil, fmt.Errorf("rejecting creation of Knative service %s on namespace %s because the ServiceMeshMemberRoll does not exist", ksvc.Name, ksvc.Namespace)
	}

	log = log.WithValues("smmr.namespace", smmr.Namespace, "smmr.name", smmr.Name)

	for _, memberNamespace := range smmr.Status.ConfiguredMembers {
		if memberNamespace == ksvc.Namespace {
			log.V(1).Info("The Knative service is accepted")
			return nil, nil
		}
	}

	log.Info("Rejecting Knative service because its namespace is not a member of the service mesh")
	return nil, fmt.Errorf("rejecting creation of Knative service %s on namespace %s because the namespace is not a configured member of the mesh", ksvc.Name, ksvc.Namespace)
}

func (v *ksvcValidator) ValidateUpdate(_ context.Context, _, _ runtime.Object) (warnings admission.Warnings, err error) {
	// Nothing to validate on updates
	return nil, nil
}

func (v *ksvcValidator) ValidateDelete(_ context.Context, _ runtime.Object) (warnings admission.Warnings, err error) {
	// For deletion, we don't need to validate anything.
	return nil, nil
}
