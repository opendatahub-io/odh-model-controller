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

	istiov1alpha3 "istio.io/api/networking/v1alpha3"
	istiotypev1beta1 "istio.io/api/type/v1beta1"
	istioclientv1alpha3 "istio.io/client-go/pkg/apis/networking/v1alpha3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/comparators"
)

func createEnvoyFilter(appLabel string) *istioclientv1alpha3.EnvoyFilter {
	return &istioclientv1alpha3.EnvoyFilter{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-envoyfilter",
			Namespace: "test-ns",
			Labels: map[string]string{
				"app": appLabel,
			},
		},
		Spec: istiov1alpha3.EnvoyFilter{
			TargetRefs: []*istiotypev1beta1.PolicyTargetReference{
				{
					Group: "gateway.networking.k8s.io",
					Kind:  "Gateway",
					Name:  "test-gateway",
				},
			},
		},
	}
}

func createEnvoyFilterWithExtraLabels(appLabel string) *istioclientv1alpha3.EnvoyFilter {
	return &istioclientv1alpha3.EnvoyFilter{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-envoyfilter",
			Namespace: "test-ns",
			Labels: map[string]string{
				"app": appLabel,
				"kubectl.kubernetes.io/last-applied-configuration": "...",
				"app.kubernetes.io/managed-by":                     "controller",
			},
		},
		Spec: istiov1alpha3.EnvoyFilter{
			TargetRefs: []*istiotypev1beta1.PolicyTargetReference{
				{
					Group: "gateway.networking.k8s.io",
					Kind:  "Gateway",
					Name:  "test-gateway",
				},
			},
		},
	}
}

func createEnvoyFilterWithoutLabels() *istioclientv1alpha3.EnvoyFilter {
	return &istioclientv1alpha3.EnvoyFilter{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-envoyfilter",
			Namespace: "test-ns",
		},
		Spec: istiov1alpha3.EnvoyFilter{
			TargetRefs: []*istiotypev1beta1.PolicyTargetReference{
				{
					Group: "gateway.networking.k8s.io",
					Kind:  "Gateway",
					Name:  "test-gateway",
				},
			},
		},
	}
}

func TestEnvoyFilterComparator(t *testing.T) {
	comparator := comparators.GetEnvoyFilterComparator()

	tests := []struct {
		name      string
		deployed  *istioclientv1alpha3.EnvoyFilter
		requested *istioclientv1alpha3.EnvoyFilter
		expected  bool
	}{
		{
			name:      "identical EnvoyFilters should return true",
			deployed:  createEnvoyFilter("test"),
			requested: createEnvoyFilter("test"),
			expected:  true,
		},
		{
			name:      "different labels should return false",
			deployed:  createEnvoyFilter("test"),
			requested: createEnvoyFilter("different"),
			expected:  false,
		},
		{
			name:      "filters without labels should return true when specs match",
			deployed:  createEnvoyFilterWithoutLabels(),
			requested: createEnvoyFilterWithoutLabels(),
			expected:  true,
		},
		{
			name:      "deployed with extra labels - DeepDerivative checks if requested is subset of deployed",
			deployed:  createEnvoyFilterWithExtraLabels("test"),
			requested: createEnvoyFilter("test"),
			expected:  true,
		},
		{
			name:      "requested with extra labels - deployed cannot contain all requested fields",
			deployed:  createEnvoyFilter("test"),
			requested: createEnvoyFilterWithExtraLabels("test"),
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
