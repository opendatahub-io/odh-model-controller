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
	"os"

	"github.com/go-logr/logr"
	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	NamespaceRoleBindingName = "odh-model-controller-namespace"
	NamespaceClusterRoleName = "odh-model-controller-namespace"
)

// NamespaceRBACReconciler manages dynamic RoleBindings that grant the controller
// write permissions in namespaces where InferenceServices or LLMInferenceServices exist.
type NamespaceRBACReconciler struct {
	client client.Client
}

func NewNamespaceRBACReconciler(client client.Client) *NamespaceRBACReconciler {
	return &NamespaceRBACReconciler{
		client: client,
	}
}

// ReconcileNamespaceRBAC ensures a RoleBinding exists in the given namespace,
// binding the controller's ServiceAccount to the odh-model-controller-namespace ClusterRole.
func (r *NamespaceRBACReconciler) ReconcileNamespaceRBAC(ctx context.Context, log logr.Logger, namespace string) error {
	controllerNamespace := os.Getenv("POD_NAMESPACE")
	if controllerNamespace == "" {
		controllerNamespace = "opendatahub"
	}

	// Skip if this is the controller's own namespace — it has a static Role/RoleBinding
	if namespace == controllerNamespace {
		return nil
	}

	log = log.WithName("NamespaceRBACReconciler")

	existing := &rbacv1.RoleBinding{}
	err := r.client.Get(ctx, types.NamespacedName{
		Name:      NamespaceRoleBindingName,
		Namespace: namespace,
	}, existing)

	expectedRoleRef := rbacv1.RoleRef{
		APIGroup: "rbac.authorization.k8s.io",
		Kind:     "ClusterRole",
		Name:     NamespaceClusterRoleName,
	}
	expectedSubjects := []rbacv1.Subject{
		{
			Kind:      "ServiceAccount",
			Name:      "odh-model-controller",
			Namespace: controllerNamespace,
		},
	}

	if err == nil {
		// RoleBinding exists — validate RoleRef and Subjects
		needsUpdate := false

		if existing.RoleRef != expectedRoleRef {
			// RoleRef is immutable; delete and recreate
			log.Info("Existing RoleBinding has wrong RoleRef, recreating", "namespace", namespace)
			if err := r.client.Delete(ctx, existing); err != nil && !errors.IsNotFound(err) {
				return err
			}
			// Fall through to create below
		} else if !subjectsMatch(existing.Subjects, expectedSubjects) {
			existing.Subjects = expectedSubjects
			needsUpdate = true
		}

		if needsUpdate {
			log.Info("Updating RoleBinding subjects", "namespace", namespace)
			return r.client.Update(ctx, existing)
		}

		if existing.RoleRef == expectedRoleRef {
			return nil
		}
	}

	if !errors.IsNotFound(err) {
		return err
	}

	// Create the RoleBinding
	rb := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      NamespaceRoleBindingName,
			Namespace: namespace,
			Labels: map[string]string{
				"opendatahub.io/managed": "true",
			},
		},
		RoleRef:  expectedRoleRef,
		Subjects: expectedSubjects,
	}

	if err := r.client.Create(ctx, rb); err != nil {
		if errors.IsAlreadyExists(err) {
			return nil
		}
		log.Error(err, "Failed to create namespace RoleBinding", "namespace", namespace)
		return err
	}

	log.Info("Created namespace RoleBinding", "namespace", namespace)
	return nil
}

// subjectsMatch checks if two subject slices are equivalent.
func subjectsMatch(a, b []rbacv1.Subject) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// CleanupNamespaceRBAC removes the namespace RoleBinding if no active InferenceServices
// or LLMInferenceServices remain in the namespace.
func (r *NamespaceRBACReconciler) CleanupNamespaceRBAC(ctx context.Context, log logr.Logger, namespace string) error {
	controllerNamespace := os.Getenv("POD_NAMESPACE")
	if controllerNamespace == "" {
		controllerNamespace = "opendatahub"
	}

	// Skip if this is the controller's own namespace
	if namespace == controllerNamespace {
		return nil
	}

	log = log.WithName("NamespaceRBACReconciler")

	// Check for active InferenceServices
	isvcList := &kservev1beta1.InferenceServiceList{}
	if err := r.client.List(ctx, isvcList, client.InNamespace(namespace)); err != nil {
		return err
	}
	for _, isvc := range isvcList.Items {
		if isvc.GetDeletionTimestamp() == nil {
			return nil // Active ISVC exists, keep the RoleBinding
		}
	}

	// Check for active LLMInferenceServices
	llmIsvcList := &kservev1alpha1.LLMInferenceServiceList{}
	if err := r.client.List(ctx, llmIsvcList, client.InNamespace(namespace)); err != nil {
		return err
	}
	for _, llmIsvc := range llmIsvcList.Items {
		if llmIsvc.GetDeletionTimestamp() == nil {
			return nil // Active LLMInferenceService exists, keep the RoleBinding
		}
	}

	// No active services, delete the RoleBinding if it's managed by us
	existing := &rbacv1.RoleBinding{}
	if err := r.client.Get(ctx, types.NamespacedName{
		Name:      NamespaceRoleBindingName,
		Namespace: namespace,
	}, existing); err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}

	// Only delete if the RoleBinding is managed by us
	if existing.Labels["opendatahub.io/managed"] != "true" {
		log.V(1).Info("RoleBinding exists but is not managed by us, skipping cleanup", "namespace", namespace)
		return nil
	}

	if err := r.client.Delete(ctx, existing); err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		log.Error(err, "Failed to delete namespace RoleBinding", "namespace", namespace)
		return err
	}

	log.Info("Deleted namespace RoleBinding", "namespace", namespace)
	return nil
}
