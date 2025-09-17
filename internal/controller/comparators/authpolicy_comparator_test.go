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

func createAuthPolicy(appLabel string) *unstructured.Unstructured {
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
					"strategy": "atomic",
				},
			},
		},
	}
}

func createAuthPolicyWithExtraLabels(appLabel string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "kuadrant.io/v1",
			"kind":       "AuthPolicy",
			"metadata": map[string]interface{}{
				"name":      "test-authn",
				"namespace": "test-ns",
				"labels": map[string]interface{}{
					"app": appLabel,
					"kubectl.kubernetes.io/last-applied-configuration": "...",
					"app.kubernetes.io/managed-by":                     "controller",
				},
			},
			"spec": map[string]interface{}{
				"defaults": map[string]interface{}{
					"strategy": "atomic",
				},
			},
		},
	}
}

func createAuthPolicyWithoutLabels() *unstructured.Unstructured {
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
					"strategy": "atomic",
				},
			},
		},
	}
}

func createComplexAuthPolicy(gatewayName, authType string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "kuadrant.io/v1",
			"kind":       "AuthPolicy",
			"metadata": map[string]interface{}{
				"name":      "llm-gateway-authn",
				"namespace": "openshift-ingress",
				"labels": map[string]interface{}{
					"app": "test",
				},
			},
			"spec": map[string]interface{}{
				"targetRef": map[string]interface{}{
					"group": "gateway.networking.k8s.io",
					"kind":  "Gateway",
					"name":  gatewayName,
				},
				"defaults": map[string]interface{}{
					"rules": map[string]interface{}{
						"authentication": map[string]interface{}{
							"kubernetes-user": map[string]interface{}{
								"credentials": map[string]interface{}{
									"authorizationHeader": map[string]interface{}{},
								},
								"kubernetesTokenReview": map[string]interface{}{
									"audiences": []interface{}{
										"https://rh-oidc.s3.us-east-1.amazonaws.com/27bd6cg0vs7nn08mue83fbof94dj4m9a",
									},
								},
							},
						},
						"authorization": map[string]interface{}{
							authType: map[string]interface{}{
								"kubernetesSubjectAccessReview": map[string]interface{}{
									"user": map[string]interface{}{
										"expression": "auth.identity.user.username",
									},
									"authorizationGroups": map[string]interface{}{
										"expression": "auth.identity.user.groups",
									},
									"resourceAttributes": map[string]interface{}{
										"group": map[string]interface{}{
											"value": "serving.kserve.io",
										},
										"resource": map[string]interface{}{
											"value": "llminferenceservices",
										},
										"namespace": map[string]interface{}{
											"expression": "request.path.split(\"/\")[1]",
										},
										"name": map[string]interface{}{
											"expression": "request.path.split(\"/\")[2]",
										},
										"verb": map[string]interface{}{
											"value": "get",
										},
									},
								},
								"priority": 1,
							},
						},
					},
				},
			},
		},
	}
}

func TestAuthPolicyComparator(t *testing.T) {
	comparator := comparators.GetAuthPolicyComparator()

	tests := []struct {
		name      string
		deployed  *unstructured.Unstructured
		requested *unstructured.Unstructured
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
			expected:  false,
		},
		{
			name:      "requested with extra labels - deployed cannot contain all requested fields",
			deployed:  createAuthPolicy("test"),
			requested: createAuthPolicyWithExtraLabels("test"),
			expected:  true,
		},
		{
			name:      "complex AuthPolicy with different authorization types should return false",
			deployed:  createComplexAuthPolicy("openshift-ai-inference", "tier-access"),
			requested: createComplexAuthPolicy("openshift-ai-inference", "admin-access"),
			expected:  false,
		},
		{
			name:      "complex AuthPolicy with identical structure should return true",
			deployed:  createComplexAuthPolicy("openshift-ai-inference", "tier-access"),
			requested: createComplexAuthPolicy("openshift-ai-inference", "tier-access"),
			expected:  true,
		},
		{
			name:      "complex AuthPolicy with different gateway names should return false",
			deployed:  createComplexAuthPolicy("gateway-1", "tier-access"),
			requested: createComplexAuthPolicy("gateway-2", "tier-access"),
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
