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
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"github.com/opendatahub-io/odh-model-controller/controllers/utils"
	"net/http"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// +kubebuilder:webhook:admissionReviewVersions=v1beta1,path=/validate-isvc-odh-service,mutating=false,failurePolicy=fail,groups="serving.kserve.io",resources=inferenceservices,verbs=create,versions=v1beta1,name=validating.isvc.odh-model-controller.opendatahub.io,sideEffects=None

type IsvcValidator struct {
	Client  client.Client
	Decoder *admission.Decoder
}

// NewIsvcValidator For tests purposes
func NewIsvcValidator(client client.Client, decoder *admission.Decoder) *IsvcValidator {
	return &IsvcValidator{
		Client:  client,
		Decoder: decoder,
	}
}

// Handle implements admission.Validator so a webhook will be registered for the type serving.kserve.io.inferenceServices
// This webhook will filter out the protected namespaces preventing the user to create the InferenceService in those namespaces
// which are: The Istio control plane namespace, Application namespace and Serving namespace
// func (is *isvcValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (warnings admission.Warnings, err error) {
func (is *IsvcValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	protectedNamespaces := make([]string, 3)
	// hardcoding for now since there is no plan to install knative on other namespaces
	protectedNamespaces[0] = "knative-serving"

	log := logf.FromContext(ctx).WithName("InferenceServiceValidatingWebhook")

	isvc := &kservev1beta1.InferenceService{}
	errs := is.Decoder.Decode(req, isvc)
	if errs != nil {
		return admission.Errored(http.StatusBadRequest, errs)
	}

	log = log.WithValues("namespace", isvc.Namespace, "isvc", isvc.Name)
	log.Info("Validating InferenceService")

	_, meshNamespace := utils.GetIstioControlPlaneName(ctx, is.Client)
	protectedNamespaces[1] = meshNamespace

	appNamespace, err := utils.GetApplicationNamespace(ctx, is.Client)
	if err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}
	protectedNamespaces[2] = appNamespace

	log.Info("Filtering protected namespaces", "namespaces", protectedNamespaces)
	for _, ns := range protectedNamespaces {
		if isvc.Namespace == ns {
			log.V(1).Info("Namespace is protected, the InferenceService will not be created")
			return admission.Denied(fmt.Sprintf("The InferenceService %s "+
				"cannot be created in protected namespace %s.", isvc.Name, isvc.Namespace))
		}
	}
	log.Info("Namespace is not protected")
	return admission.Allowed("Namespace is not protected")
}
