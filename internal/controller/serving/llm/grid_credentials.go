/*
Copyright 2026.

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
	"crypto/sha256"
	"fmt"

	kservev1alpha2 "github.com/kserve/kserve/pkg/apis/serving/v1alpha2"
	rbacv1 "k8s.io/api/rbac/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func gridCredentialKey(svc *kservev1alpha2.LLMInferenceService, publication gridPublication) client.ObjectKey {
	// Each publication owns its grant, so shared credentials need no reference
	// counting. Hash the complete identity to preserve both source and Gateway UIDs.
	digest := sha256.Sum256([]byte(gridResourceName(svc, publication.gateway)))
	return client.ObjectKey{Namespace: publication.config.Namespace, Name: fmt.Sprintf("grid-credential-%x", digest[:20])}
}

func (r *GridInferenceProviderReconciler) reconcileCredentialAccess(ctx context.Context, svc *kservev1alpha2.LLMInferenceService, publication gridPublication) error {
	key := gridCredentialKey(svc, publication)
	metadata := gridMetadata(svc, publication.gateway)
	metadata.Name, metadata.Namespace = key.Name, key.Namespace
	role := &rbacv1.Role{ObjectMeta: *metadata.DeepCopy()}
	binding := &rbacv1.RoleBinding{ObjectMeta: *metadata.DeepCopy()}
	for _, obj := range []client.Object{role, binding} {
		_, err := controllerutil.CreateOrUpdate(ctx, r.Client, obj, func() error {
			if obj.GetResourceVersion() != "" && !gridOwned(obj, svc) {
				return fmt.Errorf("refusing to overwrite unmanaged Grid credential access %s/%s", obj.GetNamespace(), obj.GetName())
			}
			if !obj.GetDeletionTimestamp().IsZero() {
				return fmt.Errorf("Grid credential access %s/%s is still deleting", obj.GetNamespace(), obj.GetName())
			}
			// The source may be in another namespace. Its finalizer owns cleanup.
			obj.SetOwnerReferences(nil)
			switch resource := obj.(type) {
			case *rbacv1.Role:
				resource.Rules = []rbacv1.PolicyRule{{APIGroups: []string{""}, Resources: []string{"secrets"}, ResourceNames: []string{publication.config.TokenSecret}, Verbs: []string{"get"}}}
			case *rbacv1.RoleBinding:
				resource.RoleRef = rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: role.Name}
				resource.Subjects = []rbacv1.Subject{publication.operator}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}
