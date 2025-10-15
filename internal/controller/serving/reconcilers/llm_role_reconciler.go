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
	machineryTypes "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/comparators"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/processors"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/resources"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
)

var _ LLMSubResourceReconciler = (*LLMRoleReconciler)(nil)

type LLMRoleReconciler struct {
	LLMNoResourceRemoval
	client         client.Client
	roleHandler    resources.RoleHandler
	deltaProcessor processors.DeltaProcessor
}

func NewLLMRoleReconciler(client client.Client) *LLMRoleReconciler {
	return &LLMRoleReconciler{
		client:         client,
		roleHandler:    resources.NewRoleHandler(client),
		deltaProcessor: processors.NewDeltaProcessor(),
	}
}

func (r *LLMRoleReconciler) Reconcile(ctx context.Context, log logr.Logger, llmisvc *kservev1alpha1.LLMInferenceService) error {
	log.V(1).Info("Verifying that the model user role exists")

	existingResource, err := r.getExistingResource(ctx, log, llmisvc)
	if err != nil {
		return err
	}

	if existingResource != nil && !utils.IsManagedResource(llmisvc, existingResource) {
		return nil
	}

	desiredResource, err := r.createDesiredResource(llmisvc)
	if err != nil {
		return err
	}

	return r.processDelta(ctx, log, desiredResource, existingResource)
}

func (r *LLMRoleReconciler) createDesiredResource(llmisvc *kservev1alpha1.LLMInferenceService) (*v1.Role, error) {
	if annotations := llmisvc.GetAnnotations(); annotations == nil {
		return nil, nil
	} else if _, found := annotations[TierAnnotationKey]; !found {
		return nil, nil
	}

	desiredRole := &v1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      utils.GetMaaSRoleName(llmisvc),
			Namespace: llmisvc.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "odh-model-controller",
			},
		},
		Rules: []v1.PolicyRule{
			{
				APIGroups:     []string{"serving.kserve.io"},
				Resources:     []string{"llminferenceservices"},
				ResourceNames: []string{llmisvc.Name},
				Verbs:         []string{"post"},
			},
		},
	}

	// Set the LLMInferenceService as the owner of the Role
	// This ensures the Role is deleted when the LLMInferenceService is deleted
	if err := controllerutil.SetControllerReference(llmisvc, desiredRole, r.client.Scheme()); err != nil {
		return nil, err
	}

	return desiredRole, nil
}

func (r *LLMRoleReconciler) getExistingResource(ctx context.Context, log logr.Logger, llmisvc *kservev1alpha1.LLMInferenceService) (*v1.Role, error) {
	return r.roleHandler.FetchRole(ctx, log, machineryTypes.NamespacedName{Name: utils.GetMaaSRoleName(llmisvc), Namespace: llmisvc.Namespace})
}

func (r *LLMRoleReconciler) processDelta(ctx context.Context, log logr.Logger, desiredRole *v1.Role, existingRole *v1.Role) (err error) {
	comparator := comparators.GetRoleComparator()
	delta := r.deltaProcessor.ComputeDelta(comparator, desiredRole, existingRole)

	if !delta.HasChanges() {
		log.V(1).Info("No delta found")
		return nil
	}

	if delta.IsAdded() {
		log.V(1).Info("Delta found", "create", desiredRole.GetName())
		if err = r.client.Create(ctx, desiredRole); err != nil {
			return err
		}
	}
	if delta.IsUpdated() {
		log.V(1).Info("Delta found", "update", existingRole.GetName())
		rp := existingRole.DeepCopy()
		rp.Rules = desiredRole.Rules

		if err = r.client.Update(ctx, rp); err != nil {
			return err
		}
	}
	if delta.IsRemoved() {
		log.V(1).Info("Delta found", "delete", existingRole.GetName())
		if err = r.client.Delete(ctx, existingRole); client.IgnoreNotFound(err) != nil {
			return err
		}
	}

	return nil
}
