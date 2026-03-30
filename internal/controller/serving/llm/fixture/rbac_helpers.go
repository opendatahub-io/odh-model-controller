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

package fixture

import (
	"context"

	rbacv1 "k8s.io/api/rbac/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const inferenceAccessSuffix = "-llmisvc-inference-access"

func GetInferenceAccessRoleName(llmisvcName string) string {
	return llmisvcName + inferenceAccessSuffix
}

func VerifyInferenceAccessRoleExists(ctx context.Context, c client.Client, namespace, llmisvcName string) {
	VerifyResourceExists(ctx, c, namespace, GetInferenceAccessRoleName(llmisvcName), &rbacv1.Role{})
}

func VerifyInferenceAccessRoleBindingExists(ctx context.Context, c client.Client, namespace, llmisvcName string) {
	VerifyResourceExists(ctx, c, namespace, GetInferenceAccessRoleName(llmisvcName), &rbacv1.RoleBinding{})
}

func VerifyInferenceAccessRoleNotExist(ctx context.Context, c client.Client, namespace, llmisvcName string) {
	VerifyResourceNotExist(ctx, c, namespace, GetInferenceAccessRoleName(llmisvcName), &rbacv1.Role{})
}

func VerifyInferenceAccessRoleBindingNotExist(ctx context.Context, c client.Client, namespace, llmisvcName string) {
	VerifyResourceNotExist(ctx, c, namespace, GetInferenceAccessRoleName(llmisvcName), &rbacv1.RoleBinding{})
}

func WaitForInferenceAccessRole(ctx context.Context, c client.Client, namespace, llmisvcName string) *rbacv1.Role {
	return WaitForResource(ctx, c, namespace, GetInferenceAccessRoleName(llmisvcName), &rbacv1.Role{})
}

func WaitForInferenceAccessRoleBinding(ctx context.Context, c client.Client, namespace, llmisvcName string) *rbacv1.RoleBinding {
	return WaitForResource(ctx, c, namespace, GetInferenceAccessRoleName(llmisvcName), &rbacv1.RoleBinding{})
}
