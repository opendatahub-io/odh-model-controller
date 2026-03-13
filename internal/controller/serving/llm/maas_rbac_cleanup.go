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

package llm

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	roleSuffix        = "-model-post-access"
	roleBindingSuffix = "-model-post-access-tier-binding"
	managedByLabel    = "app.kubernetes.io/managed-by"
	managedByValue    = "odh-model-controller"

	validatingWebhookConfigName = "validating.odh-model-controller.opendatahub.io"
)

// Webhook entry names from the MaaS tier model (3.3) that must be removed on upgrade.
var staleWebhookNames = map[string]bool{
	"validating.configmap.odh-model-controller.opendatahub.io": true,
	"validating.llmisvc.odh-model-controller.opendatahub.io":   true,
}

// MaaSRBACCleanupRunner removes resources that were previously created by
// odh-model-controller for the MaaS tier-based RBAC model.
// This handles migration from 3.3 (which created these resources) to 3.4+
// (which no longer manages them).
//
// Resources cleaned up:
//   - Roles named *-model-post-access (per-namespace, per-LLMInferenceService)
//   - RoleBindings named *-model-post-access-tier-binding (same)
//   - Stale webhook entries in the ValidatingWebhookConfiguration
//     (validating.configmap and validating.llmisvc, both failurePolicy:Fail)
type MaaSRBACCleanupRunner struct {
	Client client.Client
	Logger logr.Logger
}

// +kubebuilder:rbac:groups=admissionregistration.k8s.io,resources=validatingwebhookconfigurations,verbs=get;update;patch

func (r *MaaSRBACCleanupRunner) Start(ctx context.Context) error {
	r.Logger.Info("Starting MaaS tier resource cleanup")

	var errs []error

	rolesRemoved, err := r.cleanupRoles(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("Role cleanup failed: %w", err))
	}

	roleBindingsRemoved, err := r.cleanupRoleBindings(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("RoleBinding cleanup failed: %w", err))
	}

	webhooksRemoved, err := r.cleanupWebhookEntries(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("webhook cleanup failed: %w", err))
	}

	r.Logger.Info("MaaS tier resource cleanup completed",
		"rolesRemoved", rolesRemoved,
		"roleBindingsRemoved", roleBindingsRemoved,
		"webhookEntriesRemoved", webhooksRemoved)

	if len(errs) > 0 {
		return fmt.Errorf("MaaS tier resource cleanup encountered errors: %v", errs)
	}
	return nil
}

func (r *MaaSRBACCleanupRunner) NeedLeaderElection() bool {
	return true
}

func (r *MaaSRBACCleanupRunner) cleanupRoles(ctx context.Context) (int, error) {
	roleList := &rbacv1.RoleList{}
	if err := r.Client.List(ctx, roleList, client.MatchingLabels{managedByLabel: managedByValue}); err != nil {
		return 0, err
	}

	var errs []error
	removed := 0
	for i := range roleList.Items {
		role := &roleList.Items[i]
		if !strings.HasSuffix(role.Name, roleSuffix) {
			continue
		}
		r.Logger.Info("Deleting legacy MaaS Role", "name", role.Name, "namespace", role.Namespace)
		if err := r.Client.Delete(ctx, role); client.IgnoreNotFound(err) != nil {
			errs = append(errs, fmt.Errorf("failed to delete Role %s/%s: %w", role.Namespace, role.Name, err))
			continue
		}
		removed++
	}
	if len(errs) > 0 {
		return removed, fmt.Errorf("%v", errs)
	}
	return removed, nil
}

func (r *MaaSRBACCleanupRunner) cleanupRoleBindings(ctx context.Context) (int, error) {
	rbList := &rbacv1.RoleBindingList{}
	if err := r.Client.List(ctx, rbList, client.MatchingLabels{managedByLabel: managedByValue}); err != nil {
		return 0, err
	}

	var errs []error
	removed := 0
	for i := range rbList.Items {
		rb := &rbList.Items[i]
		if !strings.HasSuffix(rb.Name, roleBindingSuffix) {
			continue
		}
		r.Logger.Info("Deleting legacy MaaS RoleBinding", "name", rb.Name, "namespace", rb.Namespace)
		if err := r.Client.Delete(ctx, rb); client.IgnoreNotFound(err) != nil {
			errs = append(errs, fmt.Errorf("failed to delete RoleBinding %s/%s: %w", rb.Namespace, rb.Name, err))
			continue
		}
		removed++
	}
	if len(errs) > 0 {
		return removed, fmt.Errorf("%v", errs)
	}
	return removed, nil
}

func (r *MaaSRBACCleanupRunner) cleanupWebhookEntries(ctx context.Context) (int, error) {
	vwc := &admissionregistrationv1.ValidatingWebhookConfiguration{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: validatingWebhookConfigName}, vwc); err != nil {
		if apierrs.IsNotFound(err) {
			return 0, nil
		}
		return 0, err
	}

	filtered := make([]admissionregistrationv1.ValidatingWebhook, 0, len(vwc.Webhooks))
	removed := 0
	for i := range vwc.Webhooks {
		if staleWebhookNames[vwc.Webhooks[i].Name] {
			r.Logger.Info("Removing stale webhook entry", "name", vwc.Webhooks[i].Name)
			removed++
			continue
		}
		filtered = append(filtered, vwc.Webhooks[i])
	}

	if removed == 0 {
		return 0, nil
	}

	vwc.Webhooks = filtered
	if err := r.Client.Update(ctx, vwc); err != nil {
		return 0, err
	}

	return removed, nil
}
