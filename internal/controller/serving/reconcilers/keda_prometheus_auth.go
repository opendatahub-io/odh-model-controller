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
	"fmt"

	"github.com/go-logr/logr"
	"github.com/hashicorp/go-multierror"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	kedaapi "github.com/kedacore/keda/v2/apis/keda/v1alpha1"
	kservev1alpha2 "github.com/kserve/kserve/pkg/apis/serving/v1alpha2"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
)

const (
	KEDAResourcesPrefix                            = "inference-"
	KEDAPrometheusPrefix                           = KEDAResourcesPrefix + "prometheus-"
	KEDAPrometheusAuthResourceName                 = KEDAPrometheusPrefix + "auth"
	KEDAPrometheusAuthServiceAccountName           = KEDAPrometheusAuthResourceName
	KEDAPrometheusAuthTriggerSecretName            = KEDAPrometheusAuthResourceName
	KEDAPrometheusAuthMetricsReaderRoleName        = KEDAPrometheusAuthResourceName
	KEDAPrometheusAuthMetricsReaderRoleBindingName = KEDAPrometheusAuthResourceName
	KEDAPrometheusAuthTriggerAuthName              = KEDAPrometheusAuthResourceName

	KEDAResourcesLabelKey   = "odh-model-controller"
	KEDAResourcesLabelValue = "keda-reconciler"
)

var KedaLabelPredicate = predicate.NewPredicateFuncs(func(o client.Object) bool {
	return o.GetLabels()[KEDAResourcesLabelKey] == KEDAResourcesLabelValue
})

// kedaPrometheusAuthResources owns the CRUD lifecycle of the per-namespace KEDA Prometheus authentication
// resources (ServiceAccount, Secret, Role, RoleBinding, TriggerAuthentication). It is intentionally agnostic of
// InferenceService vs. LLMInferenceService: callers (KserveKEDAReconciler, LLMKEDAReconciler) pass in the
// namespace, a non-controlling OwnerReference for the object driving reconciliation, and that object's
// annotations. This lets both CRDs safely co-own and share the exact same set of resources in a namespace.
type kedaPrometheusAuthResources struct {
	client client.Client
}

// reconcile creates/updates the full set of KEDA Prometheus auth resources, upserting ownerRef onto each.
func (k *kedaPrometheusAuthResources) reconcile(ctx context.Context, log logr.Logger, namespace string, ownerRef metav1.OwnerReference, annotations map[string]string) error {
	if err := retryOnConflicts(func() error { return k.reconcileServiceAccount(ctx, log, namespace, ownerRef) }); err != nil {
		return fmt.Errorf("failed to reconcile service account: %w", err)
	}
	if err := retryOnConflicts(func() error { return k.reconcileSecret(ctx, log, namespace, ownerRef) }); err != nil {
		return fmt.Errorf("failed to reconcile secret: %w", err)
	}
	if err := retryOnConflicts(func() error { return k.reconcileRole(ctx, log, namespace, ownerRef, annotations) }); err != nil {
		return fmt.Errorf("failed to reconcile role: %w", err)
	}
	if err := retryOnConflicts(func() error { return k.reconcileRoleBinding(ctx, log, namespace, ownerRef, annotations) }); err != nil {
		return fmt.Errorf("failed to reconcile role binding: %w", err)
	}
	if err := retryOnConflicts(func() error { return k.reconcileTriggerAuthentication(ctx, log, namespace, ownerRef, annotations) }); err != nil && !meta.IsNoMatchError(err) {
		return fmt.Errorf("failed to reconcile trigger authentication: %w", err)
	}
	return nil
}

// resourcesExist reports whether the shared KEDA Prometheus auth resources have been created in namespace, using
// a single cheap Get against the (cached) client as a proxy for the whole set: all resources are always
// created together in reconcile() and deleted together in cleanupNamespace(), so checking one is sufficient.
func (k *kedaPrometheusAuthResources) resourcesExist(ctx context.Context, namespace string) (bool, error) {
	sa := &corev1.ServiceAccount{}
	err := k.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: KEDAPrometheusAuthServiceAccountName}, sa)
	if err != nil {
		if errors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// removeOwnerReferenceIfPresent removes ownerRef from the shared resources and, if that leaves them unused,
// cleans up the namespace - but only if the resources actually exist. This avoids the cost of a full
// removeOwnerReference pass plus the cross-type cleanupNamespaceIfUnused list scan on every reconcile of every
// InferenceService/LLMInferenceService that never configured Prometheus KEDA autoscaling in the first place.
func (k *kedaPrometheusAuthResources) removeOwnerReferenceIfPresent(ctx context.Context, log logr.Logger, namespace string, ownerRef metav1.OwnerReference) error {
	exists, err := k.resourcesExist(ctx, namespace)
	if err != nil {
		return fmt.Errorf("failed to check for existing KEDA resources: %w", err)
	}
	if !exists {
		return nil
	}

	if err := k.removeOwnerReference(ctx, log, namespace, ownerRef); err != nil {
		return fmt.Errorf("failed to remove owner reference from KEDA resources: %w", err)
	}
	return k.cleanupNamespaceIfUnused(ctx, log, namespace)
}

// cleanupNamespaceIfUnused deletes the shared KEDA Prometheus auth resources from namespace, unless at least one
// InferenceService or LLMInferenceService in that namespace still requires Prometheus-based KEDA autoscaling.
func (k *kedaPrometheusAuthResources) cleanupNamespaceIfUnused(ctx context.Context, log logr.Logger, namespace string) error {
	inferenceServiceList := &kservev1beta1.InferenceServiceList{}
	if err := k.client.List(ctx, inferenceServiceList, client.InNamespace(namespace)); err != nil {
		return err
	}
	for _, isvc := range inferenceServiceList.Items {
		// Ignore InferenceServices already being deleted: they may still be visible here (pending finalizer
		// removal by the InferenceService controller) but no longer count as requiring the shared resources.
		if !isvc.GetDeletionTimestamp().IsZero() {
			continue
		}
		if hasPrometheusExternalAutoscalingMetric(&isvc, log) {
			return nil
		}
	}

	llmInferenceServiceList := &kservev1alpha2.LLMInferenceServiceList{}
	if err := k.client.List(ctx, llmInferenceServiceList, client.InNamespace(namespace)); err != nil {
		if !meta.IsNoMatchError(err) {
			return err
		}
	} else {
		for _, llmisvc := range llmInferenceServiceList.Items {
			// Ignore LLMInferenceServices already being deleted, for the same reason as above.
			if !llmisvc.GetDeletionTimestamp().IsZero() {
				continue
			}
			if hasPrometheusKEDATrigger(&llmisvc, log) {
				return nil
			}
		}
	}

	return k.cleanupNamespace(ctx, log, namespace)
}

func (k *kedaPrometheusAuthResources) cleanupNamespace(ctx context.Context, log logr.Logger, namespace string) error {
	log.Info("Cleaning up KEDA resources in namespace", "namespace", namespace)
	var encounteredErrors []error
	for _, r := range k.resourcesToCleanup() {
		r.SetNamespace(namespace)
		if err := k.client.Delete(ctx, r); err != nil && !errors.IsNotFound(err) && !meta.IsNoMatchError(err) {
			encounteredErrors = append(encounteredErrors, err)
		}
	}
	if len(encounteredErrors) > 0 {
		return multierror.Append(nil, encounteredErrors...)
	}
	return nil
}

// --- TriggerAuthentication ---
func (k *kedaPrometheusAuthResources) reconcileTriggerAuthentication(ctx context.Context, log logr.Logger, namespace string, ownerRef metav1.OwnerReference, annotations map[string]string) error {
	expected := k.expectedTriggerAuthentication(namespace, ownerRef, annotations)
	curr := &kedaapi.TriggerAuthentication{}
	key := client.ObjectKey{Namespace: expected.Namespace, Name: expected.Name}

	err := k.client.Get(ctx, key, curr)
	if err != nil {
		if errors.IsNotFound(err) {
			return k.createTriggerAuthentication(ctx, log, namespace, ownerRef, annotations)
		}
		return fmt.Errorf("failed to get TriggerAuthentication %s: %w", key.String(), err)
	}

	expected.OwnerReferences = upsertOwnerReference(ownerRef, curr)
	expected.ResourceVersion = curr.ResourceVersion

	if equality.Semantic.DeepDerivative(expected.Spec, curr.Spec) &&
		equality.Semantic.DeepDerivative(expected.Labels, curr.Labels) &&
		equality.Semantic.DeepDerivative(expected.Annotations, curr.Annotations) &&
		equality.Semantic.DeepDerivative(expected.OwnerReferences, curr.OwnerReferences) {
		log.V(2).Info("TriggerAuthentication is up-to-date", "namespace", key.Namespace, "name", key.Name)
		return nil
	}

	log.Info("Updating TriggerAuthentication", "namespace", key.Namespace, "name", key.Name)
	if err := k.client.Update(ctx, expected); err != nil {
		return fmt.Errorf("failed to update TriggerAuthentication %s: %w", key.String(), err)
	}
	return nil
}

func (k *kedaPrometheusAuthResources) createTriggerAuthentication(ctx context.Context, log logr.Logger, namespace string, ownerRef metav1.OwnerReference, annotations map[string]string) error {
	log.Info("Creating TriggerAuthentication", "name", KEDAPrometheusAuthTriggerAuthName)
	ta := k.expectedTriggerAuthentication(namespace, ownerRef, annotations)
	if err := k.client.Create(ctx, ta); err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create TriggerAuthentication %s/%s: %w", ta.Namespace, ta.Name, err)
	}
	return nil
}

func (k *kedaPrometheusAuthResources) expectedTriggerAuthentication(namespace string, ownerRef metav1.OwnerReference, annotations map[string]string) *kedaapi.TriggerAuthentication {
	return &kedaapi.TriggerAuthentication{
		ObjectMeta: metav1.ObjectMeta{
			Name:            KEDAPrometheusAuthTriggerAuthName,
			Namespace:       namespace,
			OwnerReferences: []metav1.OwnerReference{ownerRef},
			Labels:          getKedaLabels(),
			Annotations:     annotations,
		},
		Spec: kedaapi.TriggerAuthenticationSpec{
			SecretTargetRef: []kedaapi.AuthSecretTargetRef{
				{
					Parameter: "bearerToken",
					Name:      KEDAPrometheusAuthTriggerSecretName,
					Key:       "token",
				},
				{
					Parameter: "ca",
					Name:      KEDAPrometheusAuthTriggerSecretName,
					Key:       "ca.crt",
				},
			},
		},
	}
}

// --- ServiceAccount ---
func (k *kedaPrometheusAuthResources) reconcileServiceAccount(ctx context.Context, log logr.Logger, namespace string, ownerRef metav1.OwnerReference) error {
	expected := k.expectedServiceAccount(namespace, ownerRef)
	curr := &corev1.ServiceAccount{}
	key := client.ObjectKey{Namespace: expected.Namespace, Name: expected.Name}

	err := k.client.Get(ctx, key, curr)
	if err != nil {
		if errors.IsNotFound(err) {
			return k.createServiceAccount(ctx, log, namespace, ownerRef)
		}
		return fmt.Errorf("failed to get ServiceAccount %s: %w", key.String(), err)
	}

	expected.OwnerReferences = upsertOwnerReference(ownerRef, curr)
	expected.ResourceVersion = curr.ResourceVersion

	if equality.Semantic.DeepDerivative(expected.Labels, curr.Labels) &&
		equality.Semantic.DeepDerivative(expected.Annotations, curr.Annotations) &&
		equality.Semantic.DeepDerivative(expected.OwnerReferences, curr.OwnerReferences) {
		log.V(2).Info("ServiceAccount is up-to-date", "namespace", expected.Namespace, "name", expected.Name)
		return nil
	}

	log.Info("Updating ServiceAccount", "namespace", key.Namespace, "name", key.Name)
	if err := k.client.Update(ctx, expected); err != nil {
		return fmt.Errorf("failed to update ServiceAccount %s: %w", key.String(), err)
	}
	return nil
}

func (k *kedaPrometheusAuthResources) createServiceAccount(ctx context.Context, log logr.Logger, namespace string, ownerRef metav1.OwnerReference) error {
	log.Info("Creating ServiceAccount", "name", KEDAPrometheusAuthServiceAccountName)
	sa := k.expectedServiceAccount(namespace, ownerRef)
	if err := k.client.Create(ctx, sa); err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create ServiceAccount %s/%s: %w", sa.Namespace, sa.Name, err)
	}
	return nil
}

func (k *kedaPrometheusAuthResources) expectedServiceAccount(namespace string, ownerRef metav1.OwnerReference) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:            KEDAPrometheusAuthServiceAccountName,
			Namespace:       namespace,
			OwnerReferences: []metav1.OwnerReference{ownerRef},
			Labels:          getKedaLabels(),
		},
		// ServiceAccount Spec is mostly empty; Secrets and ImagePullSecrets are managed via sub-resources or user additions.
	}
}

// --- Secret ---
func (k *kedaPrometheusAuthResources) reconcileSecret(ctx context.Context, log logr.Logger, namespace string, ownerRef metav1.OwnerReference) error {
	expected := k.expectedSecret(namespace, ownerRef)
	curr := &corev1.Secret{}
	key := client.ObjectKey{Namespace: expected.Namespace, Name: expected.Name}

	err := k.client.Get(ctx, key, curr)
	if err != nil {
		if errors.IsNotFound(err) {
			return k.createSecret(ctx, log, namespace, ownerRef)
		}
		return fmt.Errorf("failed to get Secret %s: %w", key.String(), err)
	}

	expected.OwnerReferences = upsertOwnerReference(ownerRef, curr)
	expected.ResourceVersion = curr.ResourceVersion

	if expected.Type == curr.Type &&
		equality.Semantic.DeepDerivative(expected.Annotations, curr.Annotations) &&
		equality.Semantic.DeepDerivative(expected.Labels, curr.Labels) &&
		equality.Semantic.DeepDerivative(expected.OwnerReferences, curr.OwnerReferences) {
		log.V(2).Info("Secret is up-to-date", "namespace", key.Namespace, "name", key.Name)
		return nil
	}

	// Preserve data from the current secret as it's managed by Kubernetes for ServiceAccountToken secrets.
	expected.Data = curr.Data

	log.Info("Updating Secret", "namespace", key.Namespace, "name", key.Name)
	if err := k.client.Update(ctx, expected); err != nil {
		return fmt.Errorf("failed to update Secret %s: %w", key.String(), err)
	}
	return nil
}

func (k *kedaPrometheusAuthResources) createSecret(ctx context.Context, log logr.Logger, namespace string, ownerRef metav1.OwnerReference) error {
	log.Info("Creating Secret", "name", KEDAPrometheusAuthTriggerSecretName)
	secret := k.expectedSecret(namespace, ownerRef)
	if err := k.client.Create(ctx, secret); err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create Secret %s/%s: %w", secret.Namespace, secret.Name, err)
	}
	return nil
}

func (k *kedaPrometheusAuthResources) expectedSecret(namespace string, ownerRef metav1.OwnerReference) *corev1.Secret {

	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            KEDAPrometheusAuthTriggerSecretName,
			Namespace:       namespace,
			OwnerReferences: []metav1.OwnerReference{ownerRef},
			Labels:          getKedaLabels(),
			Annotations: map[string]string{
				corev1.ServiceAccountNameKey: KEDAPrometheusAuthServiceAccountName,
			},
		},
		Type: corev1.SecretTypeServiceAccountToken,
		// Data field is populated by the Kubernetes controller for service account tokens.
	}
}

// --- Role ---
func (k *kedaPrometheusAuthResources) reconcileRole(ctx context.Context, log logr.Logger, namespace string, ownerRef metav1.OwnerReference, annotations map[string]string) error {
	expected := k.expectedRole(namespace, ownerRef, annotations)
	curr := &rbacv1.Role{}
	key := client.ObjectKey{Namespace: expected.Namespace, Name: expected.Name}

	err := k.client.Get(ctx, key, curr)
	if err != nil {
		if errors.IsNotFound(err) {
			return k.createRole(ctx, log, namespace, ownerRef, annotations)
		}
		return fmt.Errorf("failed to get Role %s: %w", key.String(), err)
	}

	expected.OwnerReferences = upsertOwnerReference(ownerRef, curr)
	expected.ResourceVersion = curr.ResourceVersion

	if equality.Semantic.DeepDerivative(expected.Rules, curr.Rules) &&
		equality.Semantic.DeepDerivative(expected.Labels, curr.Labels) &&
		equality.Semantic.DeepDerivative(expected.Annotations, curr.Annotations) &&
		equality.Semantic.DeepDerivative(expected.OwnerReferences, curr.OwnerReferences) {
		log.V(2).Info("Role is up-to-date", "namespace", key.Namespace, "name", key.Name)
		return nil
	}

	log.Info("Updating Role", "namespace", key.Namespace, "name", key.Name)
	if err := k.client.Update(ctx, expected); err != nil {
		return fmt.Errorf("failed to update Role %s: %w", key.String(), err)
	}
	return nil
}

func (k *kedaPrometheusAuthResources) createRole(ctx context.Context, log logr.Logger, namespace string, ownerRef metav1.OwnerReference, annotations map[string]string) error {
	log.Info("Creating Role", "name", KEDAPrometheusAuthMetricsReaderRoleName)
	role := k.expectedRole(namespace, ownerRef, annotations)
	if err := k.client.Create(ctx, role); err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create Role %s/%s: %w", role.Namespace, role.Name, err)
	}
	return nil
}

func (k *kedaPrometheusAuthResources) expectedRole(namespace string, ownerRef metav1.OwnerReference, annotations map[string]string) *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:            KEDAPrometheusAuthMetricsReaderRoleName,
			Namespace:       namespace,
			OwnerReferences: []metav1.OwnerReference{ownerRef},
			Labels:          getKedaLabels(),
			Annotations:     annotations,
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"pods"},
				Verbs:     []string{"get"},
			},
			{
				APIGroups: []string{"metrics.k8s.io"},
				Resources: []string{"pods", "nodes"},
				Verbs:     []string{"get", "list", "watch"},
			},
		},
	}
}

// --- RoleBinding ---
func (k *kedaPrometheusAuthResources) reconcileRoleBinding(ctx context.Context, log logr.Logger, namespace string, ownerRef metav1.OwnerReference, annotations map[string]string) error {
	expected := k.expectedRoleBinding(namespace, ownerRef, annotations)
	curr := &rbacv1.RoleBinding{}
	key := client.ObjectKey{Namespace: expected.Namespace, Name: expected.Name}

	err := k.client.Get(ctx, key, curr)
	if err != nil {
		if errors.IsNotFound(err) {
			return k.createRoleBinding(ctx, log, namespace, ownerRef, annotations)
		}
		return fmt.Errorf("failed to get RoleBinding %s: %w", key.String(), err)
	}

	expected.OwnerReferences = upsertOwnerReference(ownerRef, curr)
	expected.ResourceVersion = curr.ResourceVersion

	if equality.Semantic.DeepDerivative(expected.Subjects, curr.Subjects) &&
		equality.Semantic.DeepDerivative(expected.RoleRef, curr.RoleRef) &&
		equality.Semantic.DeepDerivative(expected.Labels, curr.Labels) &&
		equality.Semantic.DeepDerivative(expected.Annotations, curr.Annotations) &&
		equality.Semantic.DeepDerivative(expected.OwnerReferences, curr.OwnerReferences) {
		log.V(2).Info("RoleBinding is up-to-date", "namespace", key.Namespace, "name", key.Name)
		return nil
	}

	log.Info("Updating RoleBinding", "namespace", key.Namespace, "name", key.Name)
	if err := k.client.Update(ctx, expected); err != nil {
		return fmt.Errorf("failed to update RoleBinding %s: %w", key.String(), err)
	}
	return nil
}

func (k *kedaPrometheusAuthResources) createRoleBinding(ctx context.Context, log logr.Logger, namespace string, ownerRef metav1.OwnerReference, annotations map[string]string) error {
	log.Info("Creating RoleBinding", "name", KEDAPrometheusAuthMetricsReaderRoleBindingName)
	rb := k.expectedRoleBinding(namespace, ownerRef, annotations)
	if err := k.client.Create(ctx, rb); err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create RoleBinding %s/%s: %w", rb.Namespace, rb.Name, err)
	}
	return nil
}

func (k *kedaPrometheusAuthResources) expectedRoleBinding(namespace string, ownerRef metav1.OwnerReference, annotations map[string]string) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:            KEDAPrometheusAuthMetricsReaderRoleBindingName,
			Namespace:       namespace,
			OwnerReferences: []metav1.OwnerReference{ownerRef},
			Labels:          getKedaLabels(),
			Annotations:     annotations,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      rbacv1.ServiceAccountKind,
				Name:      KEDAPrometheusAuthResourceName,
				Namespace: namespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "Role",
			Name:     KEDAPrometheusAuthMetricsReaderRoleName,
		},
	}
}

// removeOwnerReference attempts to remove ownerRefToRemove from all KEDA-related resources in namespace.
// It collects errors and returns a summary error if any occurred.
func (k *kedaPrometheusAuthResources) removeOwnerReference(ctx context.Context, log logr.Logger, namespace string, ownerRefToRemove metav1.OwnerReference) error {
	var encounteredErrors []error

	resourceCleanups := k.resourcesToCleanup()

	for _, rc := range resourceCleanups {
		if err := retryOnConflicts(func() error { return k.removeOwnerReferenceFromObject(ctx, log, rc, namespace, ownerRefToRemove) }); err != nil {
			err := fmt.Errorf("failed to remove owner reference from %s %s: %w", rc.GetObjectKind(), rc.GetName(), err)
			encounteredErrors = append(encounteredErrors, err)
		}
	}

	if len(encounteredErrors) > 0 {
		err := multierror.Append(nil, encounteredErrors...)
		return fmt.Errorf("encountered %d error(s) during owner reference removal: %w", len(encounteredErrors), err)
	}
	return nil
}

// removeOwnerReferenceFromObject fetches a Kubernetes object and removes a specific owner reference.
// If the object or the owner reference is not found, it's a no-op for that object.
// obj parameter must be a pointer to an empty struct of the target resource kind (e.g., &corev1.ServiceAccount{}).
func (k *kedaPrometheusAuthResources) removeOwnerReferenceFromObject(
	ctx context.Context,
	log logr.Logger,
	obj client.Object,
	namespace string,
	ownerRefToRemove metav1.OwnerReference,
) error {
	key := client.ObjectKey{Namespace: namespace, Name: obj.GetName()}
	err := k.client.Get(ctx, key, obj)
	if err != nil {
		if errors.IsNotFound(err) || meta.IsNoMatchError(err) {
			log.V(1).Info("Resource not found, skipping owner reference removal", "kind", obj.GetObjectKind(), "namespace", key.Namespace, "name", key.Name)
			return nil // No-op: resource doesn't exist.
		}
		return fmt.Errorf("failed to get %s %s for owner reference removal: %w", obj.GetObjectKind(), key.String(), err)
	}

	originalOwnerReferences := obj.GetOwnerReferences()
	if len(originalOwnerReferences) == 0 {
		log.V(1).Info("Resource has no owner references, skipping removal attempt", "kind", obj.GetObjectKind(), "resourceName", obj.GetName(), "namespace", obj.GetNamespace())
		return nil // No-op: resource has no owners.
	}

	newOwnerReferences := make([]metav1.OwnerReference, 0, len(originalOwnerReferences))

	for _, ref := range originalOwnerReferences {
		// Match by UID, as this is the unique identifier for an owner reference instance.
		if ref.UID == ownerRefToRemove.UID {
			log.V(1).Info("Matching owner reference found, will be removed.",
				"ownerRefUID", ref.UID, "resourceKind", obj.GetObjectKind(), "resourceName", obj.GetName())
		} else {
			newOwnerReferences = append(newOwnerReferences, ref)
		}
	}

	if equality.Semantic.DeepEqual(newOwnerReferences, originalOwnerReferences) {
		return nil
	}

	obj.SetOwnerReferences(newOwnerReferences)
	if err := k.client.Update(ctx, obj); err != nil && !errors.IsNotFound(err) && !meta.IsNoMatchError(err) {
		return fmt.Errorf("failed to update %s %s after removing owner reference: %w", obj.GetObjectKind(), key.String(), err)
	}
	log.Info("Successfully removed owner reference and updated resource",
		"resourceKind", obj.GetObjectKind(), "resourceName", obj.GetName(), "namespace", obj.GetNamespace())

	return nil
}

func upsertOwnerReference(expected metav1.OwnerReference, obj client.Object) []metav1.OwnerReference {
	references := obj.GetOwnerReferences()
	newReferences := make([]metav1.OwnerReference, 0, len(references)+1)
	found := false
	for _, ref := range references {
		if ref.APIVersion == expected.APIVersion && ref.Kind == expected.Kind && ref.Name == expected.Name {
			newReferences = append(newReferences, expected) // Replace with the new reference to update UID etc.
			found = true
		} else {
			newReferences = append(newReferences, ref)
		}
	}

	if !found {
		newReferences = append(newReferences, expected)
	}
	return newReferences
}

func getKedaLabels() map[string]string {
	return map[string]string{
		KEDAResourcesLabelKey: KEDAResourcesLabelValue,
	}
}

func retryOnConflicts(f func() error) error {
	return retry.RetryOnConflict(retry.DefaultRetry, f)
}

func (k *kedaPrometheusAuthResources) resourcesToCleanup() []client.Object {
	return []client.Object{
		&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: KEDAPrometheusAuthServiceAccountName}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: KEDAPrometheusAuthTriggerSecretName}},
		&rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: KEDAPrometheusAuthMetricsReaderRoleName}},
		&rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: KEDAPrometheusAuthMetricsReaderRoleBindingName}},
		&kedaapi.TriggerAuthentication{ObjectMeta: metav1.ObjectMeta{Name: KEDAPrometheusAuthTriggerAuthName}},
	}
}
