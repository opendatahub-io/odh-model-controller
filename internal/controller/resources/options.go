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

package resources

import (
	kuadrantv1 "github.com/kuadrant/kuadrant-operator/api/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ObjectOption is a functional option for modifying Kubernetes objects.
type ObjectOption func(client.Object)

// WithLabels returns an ObjectOption that merges the given labels into the object.
func WithLabels(labels map[string]string) ObjectOption {
	return func(obj client.Object) {
		existing := obj.GetLabels()
		if existing == nil {
			existing = make(map[string]string)
		}
		for k, v := range labels {
			existing[k] = v
		}
		obj.SetLabels(existing)
	}
}

// WithAudiences returns an ObjectOption that sets the audiences for all KubernetesTokenReview authentications.
func WithAudiences(audiences []string) ObjectOption {
	return func(obj client.Object) {
		ap, ok := obj.(*kuadrantv1.AuthPolicy)
		if !ok || ap.Spec.AuthScheme == nil {
			return
		}
		for key, auth := range ap.Spec.AuthScheme.Authentication {
			if auth.KubernetesTokenReview != nil {
				auth.KubernetesTokenReview.Audiences = audiences
				ap.Spec.AuthScheme.Authentication[key] = auth
			}
		}
	}
}
