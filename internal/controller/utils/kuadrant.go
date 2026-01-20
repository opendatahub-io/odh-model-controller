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

package utils

import (
	"cmp"
	"context"
	"slices"

	authorinooperatorv1beta1 "github.com/kuadrant/authorino-operator/api/v1beta1"
	kuadrantv1beta1 "github.com/kuadrant/kuadrant-operator/api/v1beta1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// GetKuadrantNamespace finds the namespace where Kuadrant is installed.
func GetKuadrantNamespace(ctx context.Context, c client.Client) string {
	logger := log.FromContext(ctx)
	const defaultKuadrantNamespace = "kuadrant-system"
	kuadrantNamespace := GetEnvOr("KUADRANT_NAMESPACE", defaultKuadrantNamespace)

	kuadrantList := &kuadrantv1beta1.KuadrantList{}
	if err := c.List(ctx, kuadrantList); err != nil {
		logger.V(1).Info("Unable to list Kuadrant resources, using default namespace", "kuadrant.namespace", kuadrantNamespace, "error", err)
		return kuadrantNamespace
	}

	if len(kuadrantList.Items) == 0 {
		return kuadrantNamespace
	}

	// Ensure a stable ordering of resources.
	slices.SortStableFunc(kuadrantList.Items, func(a, b kuadrantv1beta1.Kuadrant) int {
		c := cmp.Compare(a.Namespace, b.Namespace)
		if c != 0 {
			return c
		}
		return cmp.Compare(a.Name, b.Name)
	})

	// Prioritize Kuadrant in "default Kuadrant namespace"
	for _, kuadrant := range kuadrantList.Items {
		if kuadrant.Namespace == kuadrantNamespace {
			return kuadrantNamespace
		}
	}

	// Fall back to first Kuadrant found
	return kuadrantList.Items[0].Namespace
}

// IsAuthorinoTLSEnabled checks if Authorino has TLS enabled in the given namespace.
// Returns true if TLS is enabled or cannot be determined (safe default).
func IsAuthorinoTLSEnabled(ctx context.Context, c client.Client, kuadrantNamespace string) bool {
	logger := log.FromContext(ctx)

	authorinoList := &authorinooperatorv1beta1.AuthorinoList{}
	if err := c.List(ctx, authorinoList, client.InNamespace(kuadrantNamespace)); err != nil {
		logger.V(1).Info("Unable to list Authorino resources, assuming TLS is enabled", "kuadrant.namespace", kuadrantNamespace, "error", err)
		return true
	}

	if len(authorinoList.Items) == 0 {
		return true
	}

	// Ensure a stable ordering of resources.
	slices.SortStableFunc(authorinoList.Items, func(a, b authorinooperatorv1beta1.Authorino) int {
		return cmp.Compare(a.Name, b.Name)
	})

	// Check if TLS is disabled (default to false if unset)
	tlsEnabled := ptr.Deref(authorinoList.Items[0].Spec.Listener.Tls.Enabled, false)
	if !tlsEnabled {
		logger.V(1).Info("Authorino has TLS disabled", "listener.tls", authorinoList.Items[0].Spec.Listener.Tls)
	}
	return tlsEnabled
}
