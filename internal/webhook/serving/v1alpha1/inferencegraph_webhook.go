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

	servingv1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/opendatahub-io/odh-model-controller/internal/webhook/serving"
)

// nolint:unused
// log is for logging in this package.
var inferencegraphlog = logf.Log.WithName("inferencegraph-resource")

// SetupInferenceGraphWebhookWithManager registers the webhook for InferenceGraph in the manager.
func SetupInferenceGraphWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).For(&servingv1alpha1.InferenceGraph{}).
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
