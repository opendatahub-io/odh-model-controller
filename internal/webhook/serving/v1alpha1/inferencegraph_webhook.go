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

package v1alpha1

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	servingv1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"knative.dev/pkg/network"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/opendatahub-io/odh-model-controller/internal/webhook/serving"
)

// nolint:unused
// log is for logging in this package.
var inferencegraphlog = logf.Log.WithName("inferencegraph-resource")

// SetupInferenceGraphWebhookWithManager registers the webhook for InferenceGraph in the manager.
func SetupInferenceGraphWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).For(&servingv1alpha1.InferenceGraph{}).
		WithValidator(&InferenceGraphCustomValidator{}).
		WithDefaulter(&InferenceGraphCustomDefaulter{client: mgr.GetClient()}).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-serving-kserve-io-v1alpha1-inferencegraph,mutating=true,failurePolicy=fail,sideEffects=None,groups=serving.kserve.io,resources=inferencegraphs,verbs=create,versions=v1alpha1,name=minferencegraph-v1alpha1.odh-model-controller.opendatahub.io,admissionReviewVersions=v1

// InferenceGraphCustomDefaulter struct is responsible for setting default values on the custom resource of the
// Kind InferenceGraph when those are created or updated.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as it is used only for temporary operations and does not need to be deeply copied.
type InferenceGraphCustomDefaulter struct {
	client client.Client
}

var _ webhook.CustomDefaulter = &InferenceGraphCustomDefaulter{}

// Default implements webhook.CustomDefaulter so a webhook will be registered for the Kind InferenceGraph.
func (d *InferenceGraphCustomDefaulter) Default(ctx context.Context, obj runtime.Object) error {
	inferencegraph, ok := obj.(*servingv1alpha1.InferenceGraph)

	if !ok {
		return fmt.Errorf("expected an InferenceGraph object but got %T", obj)
	}
	logger := inferencegraphlog.WithValues("name", inferencegraph.GetName())
	logger.Info("Defaulting for InferenceGraph")

	err := serving.ApplyDefaultServerlessAnnotations(ctx, d.client, inferencegraph.GetName(), &inferencegraph.ObjectMeta, logger)
	if err != nil {
		return err
	}

	return nil
}

// NOTE: The 'path' attribute must follow a specific pattern and should not be modified directly here.
// Modifying the path for an invalid path can cause API server errors; failing to locate the webhook.
// +kubebuilder:webhook:path=/validate-serving-kserve-io-v1alpha1-inferencegraph,mutating=false,failurePolicy=fail,sideEffects=None,groups=serving.kserve.io,resources=inferencegraphs,verbs=create;update,versions=v1alpha1,name=vinferencegraph-v1alpha1.odh-model-controller.opendatahub.io,admissionReviewVersions=v1

// InferenceGraphCustomValidator struct is responsible for validating the InferenceGraph resource
// when it is created, updated, or deleted.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as this struct is used only for temporary operations and does not need to be deeply copied.
type InferenceGraphCustomValidator struct{}

var _ webhook.CustomValidator = &InferenceGraphCustomValidator{}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type InferenceGraph.
func (v *InferenceGraphCustomValidator) ValidateCreate(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	inferencegraph, ok := obj.(*servingv1alpha1.InferenceGraph)
	if !ok {
		return nil, fmt.Errorf("expected a InferenceGraph object but got %T", obj)
	}
	inferencegraphlog.Info("Validation for InferenceGraph upon creation", "name", inferencegraph.GetName())
	return validateInferenceGraph(inferencegraph)
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type InferenceGraph.
func (v *InferenceGraphCustomValidator) ValidateUpdate(_ context.Context, _, newObj runtime.Object) (admission.Warnings, error) {
	inferencegraph, ok := newObj.(*servingv1alpha1.InferenceGraph)
	if !ok {
		return nil, fmt.Errorf("expected a InferenceGraph object for the newObj but got %T", newObj)
	}
	inferencegraphlog.Info("Validation for InferenceGraph upon update", "name", inferencegraph.GetName())
	return validateInferenceGraph(inferencegraph)
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type InferenceGraph.
func (v *InferenceGraphCustomValidator) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	// ODH does not do any validations when an InferenceGraph is being deleted
	return nil, nil
}

func validateInferenceGraph(inferenceGraph *servingv1alpha1.InferenceGraph) (admission.Warnings, error) {
	clusterDomainName := network.GetClusterDomainName()
	var validationErrors field.ErrorList
	for igNodeName, igNode := range inferenceGraph.Spec.Nodes {
		for stepIdx, step := range igNode.Steps {
			if len(step.ServiceURL) != 0 {
				fieldPath := field.NewPath("spec").Child("nodes").Key(igNodeName).Child("steps").Index(stepIdx).Child("serviceUrl")
				stepUrl, urlParseErr := url.Parse(step.ServiceURL)

				if urlParseErr != nil {
					validationErrors = append(validationErrors, field.Invalid(
						fieldPath,
						step.ServiceURL,
						fmt.Sprintf("error when parsing serviceUrl: %s", urlParseErr.Error())))
					continue
				}

				serviceHost := stepUrl.Hostname()
				if strings.HasSuffix(serviceHost, ".svc") || strings.HasSuffix(serviceHost, ".svc."+clusterDomainName) {
					serviceHost = strings.TrimSuffix(serviceHost, ".svc")
					serviceHost = strings.TrimSuffix(serviceHost, ".svc."+clusterDomainName)

					// There should be only two components in the resulting serviceHost
					serviceHostComponents := strings.Split(serviceHost, ".")
					if len(serviceHostComponents) != 2 {
						validationErrors = append(validationErrors, field.Invalid(
							fieldPath,
							step.ServiceURL,
							"unexpected number of subdomain entries in hostname"))
						continue
					}

					// Namespace is the second entry. Require it to be the same as the InferenceGraph namespace
					targetNamespace := serviceHostComponents[1]
					if targetNamespace != inferenceGraph.GetNamespace() {
						validationErrors = append(validationErrors, field.Invalid(
							fieldPath,
							step.ServiceURL,
							"an InferenceGraph cannot refer InferenceServices in other namespaces"))
					}
				} else {
					// For the time being, reject using non-internal hostnames. Specially because of
					// security (e.g ensuring TLS), it may not be OK that IGs are using workloads that
					// aren't inside the cluster (e.g. preventing a leak of the Authorization header).
					validationErrors = append(validationErrors, field.Invalid(
						fieldPath, step.ServiceURL, "using public hostnames is not allowed"))
				}
			}
		}
	}

	if len(validationErrors) > 0 {
		return nil, errors.NewInvalid(
			inferenceGraph.GroupVersionKind().GroupKind(),
			inferenceGraph.GetName(),
			validationErrors,
		)
	}
	return nil, nil
}
