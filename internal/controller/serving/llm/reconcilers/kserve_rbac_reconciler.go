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

package reconcilers

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	kservev1alpha2 "github.com/kserve/kserve/pkg/apis/serving/v1alpha2"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	parentreconcilers "github.com/opendatahub-io/odh-model-controller/internal/controller/serving/reconcilers"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
)

// inferenceAccessSuffix is appended to the LLMInferenceService name to form the
// Role and RoleBinding names used for inference access control.
const inferenceAccessSuffix = "-llmisvc-inference-access"

var _ parentreconcilers.LLMSubResourceReconciler = (*KserveRBACReconciler)(nil)

// KserveRBACReconciler creates a namespaced Role and RoleBinding that grant
// "get" on the LLMInferenceService to all authenticated users.
//
// This is required because the Gateway-level AuthPolicy (maas-default-gateway-authn)
// created by this controller performs a SubjectAccessReview with verb "get" to
// authorize every inference request. Without these bindings every inference call
// returns 403 Forbidden regardless of any other auth configuration.
//
// The Role is scoped to the specific LLMInferenceService by resourceNames so
// it does not broaden access to other resources in the namespace.
// Platform operators can further restrict access by replacing the default
// "system:authenticated" binding with their own RoleBindings and setting
// the "opendatahub.io/managed: false" annotation on the generated resources.
type KserveRBACReconciler struct {
	client client.Client
	scheme *runtime.Scheme
}

func NewKserveRBACReconciler(client client.Client, scheme *runtime.Scheme) *KserveRBACReconciler {
	return &KserveRBACReconciler{
		client: client,
		scheme: scheme,
	}
}

func (r *KserveRBACReconciler) Reconcile(ctx context.Context, log logr.Logger, llmisvc *kservev1alpha2.LLMInferenceService) error {
	log.V(1).Info("Reconciling inference access RBAC for LLMInferenceService")

	if err := r.reconcileRole(ctx, log, llmisvc); err != nil {
		return fmt.Errorf("failed to reconcile inference access Role: %w", err)
	}

	if err := r.reconcileRoleBinding(ctx, log, llmisvc); err != nil {
		return fmt.Errorf("failed to reconcile inference access RoleBinding: %w", err)
	}

	return nil
}

// Delete is a no-op: the Role and RoleBinding carry an OwnerReference to the
// LLMInferenceService and are garbage-collected automatically on deletion.
func (r *KserveRBACReconciler) Delete(_ context.Context, _ logr.Logger, _ *kservev1alpha2.LLMInferenceService) error {
	return nil
}

// Cleanup is a no-op: per-service resources are cleaned up via OwnerReferences.
func (r *KserveRBACReconciler) Cleanup(_ context.Context, _ logr.Logger, _ string) error {
	return nil
}

func (r *KserveRBACReconciler) resourceName(llmisvc *kservev1alpha2.LLMInferenceService) string {
	return llmisvc.Name + inferenceAccessSuffix
}

func (r *KserveRBACReconciler) commonLabels(llmisvc *kservev1alpha2.LLMInferenceService) map[string]string {
	return map[string]string{
		"app.kubernetes.io/managed-by": "odh-model-controller",
		"app.kubernetes.io/name":       llmisvc.Name,
		"app.kubernetes.io/component":  "llminferenceservice-policies",
		"app.kubernetes.io/part-of":    "llminferenceservice",
	}
}

func (r *KserveRBACReconciler) reconcileRole(ctx context.Context, log logr.Logger, llmisvc *kservev1alpha2.LLMInferenceService) error {
	desired := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.resourceName(llmisvc),
			Namespace: llmisvc.Namespace,
			Labels:    r.commonLabels(llmisvc),
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups:     []string{"serving.kserve.io"},
				Resources:     []string{"llminferenceservices"},
				ResourceNames: []string{llmisvc.Name},
				Verbs:         []string{"get"},
			},
		},
	}

	if err := controllerutil.SetControllerReference(llmisvc, desired, r.scheme); err != nil {
		return fmt.Errorf("failed to set owner reference on inference access Role: %w", err)
	}

	existing := &rbacv1.Role{}
	err := r.client.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, existing)
	if apierrors.IsNotFound(err) {
		log.V(1).Info("Creating inference access Role", "name", desired.Name, "namespace", desired.Namespace)
		return r.client.Create(ctx, desired)
	}
	if err != nil {
		return fmt.Errorf("failed to get existing inference access Role %s/%s: %w", desired.Namespace, desired.Name, err)
	}

	if !utils.IsManagedByOpenDataHub(existing) {
		log.V(1).Info("Skipping reconciliation — Role is not managed by odh-model-controller", "name", existing.Name)
		return nil
	}

	if equality.Semantic.DeepEqual(existing.Rules, desired.Rules) {
		return nil
	}

	log.V(1).Info("Updating inference access Role (rules drifted)", "name", existing.Name)
	existing.Rules = desired.Rules
	return r.client.Update(ctx, existing)
}

func (r *KserveRBACReconciler) reconcileRoleBinding(ctx context.Context, log logr.Logger, llmisvc *kservev1alpha2.LLMInferenceService) error {
	desired := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.resourceName(llmisvc),
			Namespace: llmisvc.Namespace,
			Labels:    r.commonLabels(llmisvc),
		},
		// Bind to system:authenticated so that all valid cluster identities
		// (human users, service accounts) pass the SAR check performed by
		// maas-default-gateway-authn. The actual per-model authorisation is
		// enforced by the MaaSAuthPolicy / subscription system at the HTTPRoute
		// level; the gateway-level SAR only validates that the token is real.
		//
		// Platform operators who want finer-grained per-model access control
		// can delete this RoleBinding and create their own bindings targeting
		// specific users or groups.
		Subjects: []rbacv1.Subject{
			{
				Kind:     "Group",
				APIGroup: rbacv1.GroupName,
				Name:     "system:authenticated",
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "Role",
			Name:     r.resourceName(llmisvc),
		},
	}

	if err := controllerutil.SetControllerReference(llmisvc, desired, r.scheme); err != nil {
		return fmt.Errorf("failed to set owner reference on inference access RoleBinding: %w", err)
	}

	existing := &rbacv1.RoleBinding{}
	err := r.client.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, existing)
	if apierrors.IsNotFound(err) {
		log.V(1).Info("Creating inference access RoleBinding", "name", desired.Name, "namespace", desired.Namespace)
		return r.client.Create(ctx, desired)
	}
	if err != nil {
		return fmt.Errorf("failed to get existing inference access RoleBinding %s/%s: %w", desired.Namespace, desired.Name, err)
	}

	if !utils.IsManagedByOpenDataHub(existing) {
		log.V(1).Info("Skipping reconciliation — RoleBinding is not managed by odh-model-controller", "name", existing.Name)
		return nil
	}

	// RoleRef is immutable in the Kubernetes API; a mismatch requires delete/recreate.
	if existing.RoleRef != desired.RoleRef {
		log.V(1).Info("RoleRef mismatch detected, recreating RoleBinding", "name", desired.Name)
		if err := r.client.Delete(ctx, existing); err != nil {
			return fmt.Errorf("failed to delete mismatched RoleBinding %s/%s: %w", existing.Namespace, existing.Name, err)
		}
		return r.client.Create(ctx, desired)
	}

	if equality.Semantic.DeepEqual(existing.Subjects, desired.Subjects) {
		return nil
	}

	log.V(1).Info("Updating inference access RoleBinding (subjects drifted)", "name", existing.Name)
	existing.Subjects = desired.Subjects
	return r.client.Update(ctx, existing)
}
