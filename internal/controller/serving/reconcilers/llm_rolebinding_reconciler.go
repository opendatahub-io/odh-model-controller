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
	"errors"
	"fmt"
	"reflect"

	"github.com/go-logr/logr"
	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	machineryTypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/comparators"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/processors"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/resources"
	controllerutils "github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
)

var _ LLMSubResourceReconciler = (*LLMRoleBindingReconciler)(nil)

type LLMRoleBindingReconciler struct {
	LLMNoResourceRemoval
	client             client.Client
	recorder           record.EventRecorder
	roleBindingHandler resources.RoleBindingHandler
	deltaProcessor     processors.DeltaProcessor
	tierConfigLoader   *TierConfigLoader
}

func NewLLMRoleBindingReconciler(client client.Client, recorder record.EventRecorder) *LLMRoleBindingReconciler {
	return &LLMRoleBindingReconciler{
		client:             client,
		recorder:           recorder,
		roleBindingHandler: resources.NewRoleBindingHandler(client),
		deltaProcessor:     processors.NewDeltaProcessor(),
		tierConfigLoader:   NewTierConfigLoader(client),
	}
}

func (r *LLMRoleBindingReconciler) Reconcile(ctx context.Context, log logr.Logger, llmisvc *kservev1alpha1.LLMInferenceService) error {
	log.V(1).Info("Reconciling tier binding", "name", llmisvc.Name, "namespace", llmisvc.Namespace)

	existingResource, err := r.getExistingResource(ctx, log, llmisvc)
	if err != nil {
		return err
	}

	if existingResource != nil {
		if controllerutils.IsExplicitlyUnmanaged(existingResource) {
			log.V(1).Info("Skipping unmanaged RoleBinding",
				"llmisvc", llmisvc.Name, "llmisvcNamespace", llmisvc.Namespace,
				"rolebinding", existingResource.Name, "rolebindingNamespace", existingResource.Namespace)
			return nil
		}
		if !controllerutils.IsManagedResource(llmisvc, existingResource) {
			return nil
		}
	}

	desiredResource := r.createDesiredResource(ctx, log, llmisvc)

	return r.processDelta(ctx, log, desiredResource, existingResource)
}

func (r *LLMRoleBindingReconciler) createDesiredResource(ctx context.Context, log logr.Logger, llmisvc *kservev1alpha1.LLMInferenceService) *v1.RoleBinding {
	if annotations := llmisvc.GetAnnotations(); annotations == nil {
		return nil
	} else if _, found := annotations[TierAnnotationKey]; !found {
		return nil
	}

	subjects, err := r.getTierSubjects(ctx, log, llmisvc)
	if err != nil {
		var tierNotFoundErr *TierNotFoundError
		if errors.As(err, &tierNotFoundErr) {
			log.Error(err, "Tier misconfiguration - requested tiers not found",
				"name", llmisvc.Name, "namespace", llmisvc.Namespace,
				"missing", tierNotFoundErr.MissingTiers, "available", tierNotFoundErr.AvailableTiers)
			r.recorder.Event(llmisvc, corev1.EventTypeWarning, EventReasonTierNotFound,
				fmt.Sprintf("Requested tiers %v not found (available: %v)",
					tierNotFoundErr.MissingTiers, tierNotFoundErr.AvailableTiers))
		} else {
			log.Error(err, "Tier config unavailable",
				"name", llmisvc.Name, "namespace", llmisvc.Namespace)
			r.recorder.Event(llmisvc, corev1.EventTypeWarning, EventReasonTierConfigUnavail,
				fmt.Sprintf("Tier configuration unavailable: %v", err))
		}
		return nil
	}

	if subjects == nil {
		log.Error(nil, "Tier annotation present but no subjects resolved",
			"name", llmisvc.Name, "namespace", llmisvc.Namespace)
		r.recorder.Event(llmisvc, corev1.EventTypeWarning, EventReasonTierMisconfiguration,
			"Tier annotation present but no subjects could be resolved")
		return nil
	}

	desiredRoleBinding := &v1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      controllerutils.GetMaaSRoleBindingName(llmisvc),
			Namespace: llmisvc.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "odh-model-controller",
			},
		},
		Subjects: subjects,
		RoleRef: v1.RoleRef{
			Kind:     "Role",
			Name:     controllerutils.GetMaaSRoleName(llmisvc),
			APIGroup: "rbac.authorization.k8s.io",
		},
	}

	_ = controllerutil.SetControllerReference(llmisvc, desiredRoleBinding, r.client.Scheme())
	return desiredRoleBinding
}

func (r *LLMRoleBindingReconciler) getTierSubjects(ctx context.Context, log logr.Logger, llmisvc *kservev1alpha1.LLMInferenceService) ([]v1.Subject, error) {
	groupNames, err := r.tierConfigLoader.DefinedGroups(ctx, log, llmisvc)
	if err != nil {
		return nil, err
	}
	if groupNames == nil {
		return nil, nil
	}
	if len(groupNames) == 0 {
		return nil, fmt.Errorf("no tiers configured in ConfigMap")
	}

	subjects := make([]v1.Subject, 0, len(groupNames))
	for _, groupName := range groupNames {
		subjects = append(subjects, v1.Subject{
			Kind:     "Group",
			Name:     groupName,
			APIGroup: "rbac.authorization.k8s.io",
		})
	}

	return subjects, nil
}

func (r *LLMRoleBindingReconciler) getExistingResource(ctx context.Context, log logr.Logger, llmisvc *kservev1alpha1.LLMInferenceService) (*v1.RoleBinding, error) {
	return r.roleBindingHandler.FetchRoleBinding(ctx, log, machineryTypes.NamespacedName{Name: controllerutils.GetMaaSRoleBindingName(llmisvc), Namespace: llmisvc.Namespace})
}

func (r *LLMRoleBindingReconciler) processDelta(ctx context.Context, log logr.Logger, desiredRoleBinding *v1.RoleBinding, existingRoleBinding *v1.RoleBinding) (err error) {
	comparator := comparators.GetRoleBindingComparator()
	delta := r.deltaProcessor.ComputeDelta(comparator, desiredRoleBinding, existingRoleBinding)

	if !delta.HasChanges() {
		log.V(1).Info("No delta found")
		return nil
	}

	if delta.IsAdded() {
		log.V(1).Info("Delta found", "action", "create", "name", desiredRoleBinding.GetName(), "namespace", desiredRoleBinding.GetNamespace())
		if err = r.client.Create(ctx, desiredRoleBinding); err != nil {
			return err
		}
	}
	if delta.IsUpdated() {
		if reflect.DeepEqual(existingRoleBinding.RoleRef, desiredRoleBinding.RoleRef) {
			log.V(1).Info("Delta found", "action", "update", "name", existingRoleBinding.GetName(), "namespace", existingRoleBinding.GetNamespace())

			rp := existingRoleBinding.DeepCopy()
			rp.Subjects = desiredRoleBinding.Subjects
			rp.RoleRef = desiredRoleBinding.RoleRef

			if err = r.client.Update(ctx, rp); err != nil {
				return err
			}
		} else {
			// The RoleRef is immutable. To fix any diversion, recreation is required.
			log.V(1).Info("Delta found", "action", "recreate", "name", existingRoleBinding.GetName(), "namespace", existingRoleBinding.GetNamespace())

			if err = r.roleBindingHandler.DeleteRoleBinding(ctx, log, machineryTypes.NamespacedName{
				Name:      existingRoleBinding.GetName(),
				Namespace: existingRoleBinding.GetNamespace(),
			}); err != nil {
				return err
			}

			if err = r.client.Create(ctx, desiredRoleBinding); err != nil {
				return err
			}
		}
	}
	if delta.IsRemoved() {
		log.V(1).Info("Delta found", "action", "delete", "name", existingRoleBinding.GetName(), "namespace", existingRoleBinding.GetNamespace())
		if err = r.roleBindingHandler.DeleteRoleBinding(ctx, log, machineryTypes.NamespacedName{
			Name:      existingRoleBinding.GetName(),
			Namespace: existingRoleBinding.GetNamespace(),
		}); client.IgnoreNotFound(err) != nil {
			return err
		}
	}
	return nil
}
