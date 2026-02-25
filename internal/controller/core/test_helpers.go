/*
Copyright 2024.

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

package core

import (
	"context"
	"fmt"

	"github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// GetResourceByName retrieves a Kubernetes resource by namespace and name
func GetResourceByName[T client.Object](ctx context.Context, c client.Client, namespace, name string, obj T) (T, error) {
	err := c.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      name,
	}, obj)
	return obj, err
}

// WaitForResource waits for a Kubernetes resource to exist and returns it
func WaitForResource[T client.Object](ctx context.Context, cli client.Client, namespace, name string, obj T) T {
	gomega.Eventually(func() error {
		_, err := GetResourceByName(ctx, cli, namespace, name, obj)
		return err
	}).WithContext(ctx).Should(gomega.Succeed())
	// Retrieve one final time after Eventually succeeds to return the actual object with data
	retrieved, _ := GetResourceByName(ctx, cli, namespace, name, obj)
	return retrieved
}

// WaitForResourceWithCondition waits for a resource to exist and satisfy a custom condition
func WaitForResourceWithCondition[T client.Object](ctx context.Context, cli client.Client, namespace, name string, obj T, condition func(T) error) T {
	gomega.Eventually(func() error {
		retrieved, err := GetResourceByName(ctx, cli, namespace, name, obj)
		if err != nil {
			return err
		}
		return condition(retrieved)
	}).WithContext(ctx).Should(gomega.Succeed())
	// Retrieve one final time after Eventually succeeds to return the actual object
	retrieved, _ := GetResourceByName(ctx, cli, namespace, name, obj)
	return retrieved
}

// CheckResourceNotFound checks if a resource does not exist (is NotFound)
func CheckResourceNotFound[T client.Object](ctx context.Context, cli client.Client, namespace, name string, obj T) error {
	_, err := GetResourceByName(ctx, cli, namespace, name, obj)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}
	return fmt.Errorf("resource still exists, expected it to be deleted")
}

// WaitForResourceDeletion waits for a resource to be deleted (not found)
func WaitForResourceDeletion[T client.Object](ctx context.Context, cli client.Client, namespace, name string, obj T) {
	gomega.Eventually(func() error {
		return CheckResourceNotFound(ctx, cli, namespace, name, obj)
	}).WithContext(ctx).Should(gomega.Succeed())
}
