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

package reconcilers

import (
	"context"

	"github.com/go-logr/logr"
	"github.com/hashicorp/go-multierror"
	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type KserveLLMInferenceServiceReconciler struct {
	client                 client.Client
	subResourceReconcilers []LLMSubResourceReconciler
}

func NewKServeLLMInferenceServiceReconciler(client client.Client, kClient kubernetes.Interface, restConfig *rest.Config) *KserveLLMInferenceServiceReconciler {

	subResourceReconcilers := []LLMSubResourceReconciler{
		// TODO: NewKserveAuthPolicyReconciler(client, kClient, restConfig),
	}

	return &KserveLLMInferenceServiceReconciler{
		client:                 client,
		subResourceReconcilers: subResourceReconcilers,
	}
}

func (r *KserveLLMInferenceServiceReconciler) Reconcile(ctx context.Context, log logr.Logger, llmisvc *kservev1alpha1.LLMInferenceService) error {
	log.V(1).Info("Reconciling LLMInferenceService sub-resources")
	var reconcileErrors *multierror.Error

	for _, subReconciler := range r.subResourceReconcilers {
		if err := subReconciler.Reconcile(ctx, log, llmisvc); err != nil {
			log.Error(err, "Failed to reconcile sub-resource")
			reconcileErrors = multierror.Append(reconcileErrors, err)
		}
	}

	return reconcileErrors.ErrorOrNil()
}

func (r *KserveLLMInferenceServiceReconciler) Delete(ctx context.Context, log logr.Logger, llmisvc *kservev1alpha1.LLMInferenceService) error {
	log.V(1).Info("Deleting LLMInferenceService sub-resources")
	var deleteErrors *multierror.Error

	for _, subReconciler := range r.subResourceReconcilers {
		if err := subReconciler.Delete(ctx, log, llmisvc); err != nil {
			log.Error(err, "Failed to delete sub-resource")
			deleteErrors = multierror.Append(deleteErrors, err)
		}
	}

	return deleteErrors.ErrorOrNil()
}

func (r *KserveLLMInferenceServiceReconciler) Cleanup(ctx context.Context, log logr.Logger, namespace string) error {
	log.V(1).Info("Cleaning up LLMInferenceService namespace resources", "namespace", namespace)
	var cleanupErrors *multierror.Error

	for _, subReconciler := range r.subResourceReconcilers {
		if err := subReconciler.Cleanup(ctx, log, namespace); err != nil {
			log.Error(err, "Failed to cleanup sub-resource")
			cleanupErrors = multierror.Append(cleanupErrors, err)
		}
	}

	return cleanupErrors.ErrorOrNil()
}
