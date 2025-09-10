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

package comparators_test

import (
	"testing"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/comparators"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func createAuthPolicy(appLabel, strategy string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "kuadrant.io/v1",
			"kind":       "AuthPolicy",
			"metadata": map[string]interface{}{
				"name":      "test-authn",
				"namespace": "test-ns",
				"labels": map[string]interface{}{
					"app": appLabel,
				},
			},
			"spec": map[string]interface{}{
				"defaults": map[string]interface{}{
					"strategy": strategy,
				},
			},
		},
	}
}

func createAuthPolicyWithoutLabels(strategy string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "kuadrant.io/v1",
			"kind":       "AuthPolicy",
			"metadata": map[string]interface{}{
				"name":      "test-authn",
				"namespace": "test-ns",
			},
			"spec": map[string]interface{}{
				"defaults": map[string]interface{}{
					"strategy": strategy,
				},
			},
		},
	}
}

func TestAuthPolicyComparator(t *testing.T) {
	comparator := comparators.GetAuthPolicyComparator()

	tests := []struct {
		name     string
		policy1  *unstructured.Unstructured
		policy2  *unstructured.Unstructured
		expected bool
	}{
		{
			name:     "identical AuthPolicies should return true",
			policy1:  createAuthPolicy("test", "atomic"),
			policy2:  createAuthPolicy("test", "atomic"),
			expected: true,
		},
		{
			name:     "different specs should return false",
			policy1:  createAuthPolicy("test", "atomic"),
			policy2:  createAuthPolicy("test", "merge"),
			expected: false,
		},
		{
			name:     "different labels should return false",
			policy1:  createAuthPolicy("test", "atomic"),
			policy2:  createAuthPolicy("different", "atomic"),
			expected: false,
		},
		{
			name:     "policies without labels should return true when specs match",
			policy1:  createAuthPolicyWithoutLabels("atomic"),
			policy2:  createAuthPolicyWithoutLabels("atomic"),
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := comparator(tt.policy1, tt.policy2)
			if result != tt.expected {
				t.Errorf("comparator() = %v, expected %v", result, tt.expected)
			}
		})
	}
}
