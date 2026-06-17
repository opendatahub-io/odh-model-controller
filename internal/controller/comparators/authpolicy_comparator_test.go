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

	kuadrantv1 "github.com/kuadrant/kuadrant-operator/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gwapiv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/comparators"
)

func createAuthPolicy(appLabel string) *kuadrantv1.AuthPolicy {
	return &kuadrantv1.AuthPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-authn",
			Namespace: "test-ns",
			Labels: map[string]string{
				"app": appLabel,
			},
		},
		Spec: kuadrantv1.AuthPolicySpec{
			TargetRef: gwapiv1alpha2.LocalPolicyTargetReferenceWithSectionName{
				LocalPolicyTargetReference: gwapiv1alpha2.LocalPolicyTargetReference{
					Group: "gateway.networking.k8s.io",
					Kind:  "HTTPRoute",
					Name:  "test-route",
				},
			},
		},
	}
}

func createAuthPolicyWithExtraLabels(appLabel string) *kuadrantv1.AuthPolicy {
	return &kuadrantv1.AuthPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-authn",
			Namespace: "test-ns",
			Labels: map[string]string{
				"app": appLabel,
				"kubectl.kubernetes.io/last-applied-configuration": "...",
				"app.kubernetes.io/managed-by":                     "controller",
			},
		},
		Spec: kuadrantv1.AuthPolicySpec{
			TargetRef: gwapiv1alpha2.LocalPolicyTargetReferenceWithSectionName{
				LocalPolicyTargetReference: gwapiv1alpha2.LocalPolicyTargetReference{
					Group: "gateway.networking.k8s.io",
					Kind:  "HTTPRoute",
					Name:  "test-route",
				},
			},
		},
	}
}

func createAuthPolicyWithoutLabels() *kuadrantv1.AuthPolicy {
	return &kuadrantv1.AuthPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-authn",
			Namespace: "test-ns",
		},
		Spec: kuadrantv1.AuthPolicySpec{
			TargetRef: gwapiv1alpha2.LocalPolicyTargetReferenceWithSectionName{
				LocalPolicyTargetReference: gwapiv1alpha2.LocalPolicyTargetReference{
					Group: "gateway.networking.k8s.io",
					Kind:  "HTTPRoute",
					Name:  "test-route",
				},
			},
		},
	}
}

func TestAuthPolicyComparator(t *testing.T) {
	comparator := comparators.GetAuthPolicyComparator()

	tests := []struct {
		name      string
		deployed  *kuadrantv1.AuthPolicy
		requested *kuadrantv1.AuthPolicy
		expected  bool
	}{
		{
			name:      "identical AuthPolicies should return true",
			deployed:  createAuthPolicy("test"),
			requested: createAuthPolicy("test"),
			expected:  true,
		},
		{
			name:      "different labels should return false",
			deployed:  createAuthPolicy("test"),
			requested: createAuthPolicy("different"),
			expected:  false,
		},
		{
			name:      "policies without labels should return true when specs match",
			deployed:  createAuthPolicyWithoutLabels(),
			requested: createAuthPolicyWithoutLabels(),
			expected:  true,
		},
		{
			name:      "deployed with extra labels - DeepDerivative checks if requested is subset of deployed",
			deployed:  createAuthPolicyWithExtraLabels("test"),
			requested: createAuthPolicy("test"),
			expected:  true,
		},
		{
			name:      "requested with extra labels - deployed cannot contain all requested fields",
			deployed:  createAuthPolicy("test"),
			requested: createAuthPolicyWithExtraLabels("test"),
			expected:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := comparator(tt.deployed, tt.requested)
			if result != tt.expected {
				t.Errorf("comparator() = %v, expected %v", result, tt.expected)
			}
		})
	}
}
