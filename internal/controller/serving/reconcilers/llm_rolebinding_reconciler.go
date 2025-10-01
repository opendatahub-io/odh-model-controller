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
	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	v1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"knative.dev/pkg/kmeta"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/comparators"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/processors"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/resources"
)

var _ LLMSubResourceReconciler = (*LLMRoleBindingReconciler)(nil)

type LLMRoleBindingReconciler struct {
	LLMNoResourceRemoval
	client             client.Client
	roleBindingHandler resources.RoleBindingHandler
	deltaProcessor     processors.DeltaProcessor
}

func NewLLMRoleBindingReconciler(client client.Client) *LLMRoleBindingReconciler {
	return &LLMRoleBindingReconciler{
		client:             client,
		roleBindingHandler: resources.NewRoleBindingHandler(client),
		deltaProcessor:     processors.NewDeltaProcessor(),
	}
}

func (r *LLMRoleBindingReconciler) Reconcile(ctx context.Context, log logr.Logger, llmisvc *kservev1alpha1.LLMInferenceService) error {
	log.V(1).Info("Verifying that the model tier binding exists")

	// Create Desired resource
	desiredResource := r.createDesiredResource(llmisvc)

	// Get Existing resource
	existingResource, err := r.getExistingResource(ctx, log, llmisvc)
	if err != nil {
		return err
	}

	// Process Delta
	if err = r.processDelta(ctx, log, desiredResource, existingResource); err != nil {
		return err
	}
	return nil
}

func (r *LLMRoleBindingReconciler) createDesiredResource(llmisvc *kservev1alpha1.LLMInferenceService) *v1.RoleBinding {
	desiredRoleBinding := &v1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      kmeta.ChildName(llmisvc.Name, "-tier-binding"),
			Namespace: llmisvc.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "odh-model-controller",
			},
		},
		Subjects: []v1.Subject{
			{
				Kind:     "Group",
				Name:     "system:serviceaccounts:openshift-ai-inference-tier-free",
				APIGroup: "rbac.authorization.k8s.io",
			},
			{
				Kind:     "Group",
				Name:     "system:serviceaccounts:openshift-ai-inference-tier-premium",
				APIGroup: "rbac.authorization.k8s.io",
			},
			{
				Kind:     "Group",
				Name:     "system:serviceaccounts:openshift-ai-inference-tier-enterprise",
				APIGroup: "rbac.authorization.k8s.io",
			},
		},
		RoleRef: v1.RoleRef{
			Kind:     "Role",
			Name:     kmeta.ChildName(llmisvc.Name, "-model-user"),
			APIGroup: "rbac.authorization.k8s.io",
		},
	}

	// Set the LLMInferenceService as the owner of the RoleBinding
	// This ensures the RoleBinding is deleted when the LLMInferenceService is deleted
	_ = controllerutil.SetControllerReference(llmisvc, desiredRoleBinding, r.client.Scheme())

	return desiredRoleBinding
}

func (r *LLMRoleBindingReconciler) getExistingResource(ctx context.Context, log logr.Logger, llmisvc *kservev1alpha1.LLMInferenceService) (*v1.RoleBinding, error) {
	roleBindingName := kmeta.ChildName(llmisvc.Name, "-tier-binding")
	return r.roleBindingHandler.FetchRoleBinding(ctx, log, types.NamespacedName{Name: roleBindingName, Namespace: llmisvc.Namespace})
}

func (r *LLMRoleBindingReconciler) processDelta(ctx context.Context, log logr.Logger, desiredRoleBinding *v1.RoleBinding, existingRoleBinding *v1.RoleBinding) (err error) {
	comparator := comparators.GetRoleBindingComparator()
	delta := r.deltaProcessor.ComputeDelta(comparator, desiredRoleBinding, existingRoleBinding)

	if !delta.HasChanges() {
		log.V(1).Info("No delta found")
		return nil
	}

	if delta.IsAdded() {
		log.V(1).Info("Delta found", "create", desiredRoleBinding.GetName())
		if err = r.client.Create(ctx, desiredRoleBinding); err != nil {
			return err
		}
	}
	if delta.IsUpdated() {
		log.V(1).Info("Delta found", "update", existingRoleBinding.GetName())
		rp := existingRoleBinding.DeepCopy()
		rp.Subjects = desiredRoleBinding.Subjects
		rp.RoleRef = desiredRoleBinding.RoleRef

		if err = r.client.Update(ctx, rp); err != nil {
			return err
		}
	}
	if delta.IsRemoved() {
		log.V(1).Info("Delta found", "delete", existingRoleBinding.GetName())
		if err = r.client.Delete(ctx, existingRoleBinding); err != nil {
			return err
		}
	}
	return nil
}
