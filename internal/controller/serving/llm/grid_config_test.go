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
	"strings"
	"testing"

	kservev1alpha2 "github.com/kserve/kserve/pkg/apis/serving/v1alpha2"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/validation"
)

func TestGridConfigFromEnv(t *testing.T) {
	for _, variable := range []string{"GRID_NAMESPACE_SELECTOR", "GRID_NETWORK_NAME", "GRID_NAMESPACE", "GRID_SERVICE_ACCOUNT", "GRID_TOKEN_SECRET", "GRID_SITE_LABELS"} {
		t.Setenv(variable, "")
	}
	cfg, err := GridConfigFromEnv()
	require.NoError(t, err)
	require.False(t, cfg.NamespaceSelector.Matches(labels.Set{}))
	require.Empty(t, cfg.Namespace)
	require.Empty(t, cfg.ServiceAccount)

	t.Setenv("GRID_NETWORK_NAME", "network")
	t.Setenv("GRID_NAMESPACE", "downstream-grid")
	t.Setenv("GRID_SERVICE_ACCOUNT", "downstream-client")
	t.Setenv("GRID_TOKEN_SECRET", "token")
	t.Setenv("GRID_SITE_LABELS", "grid.praxis.fast/provider-site=rome")
	for _, tc := range []struct{ name, selector string }{
		{"JSON", `{"matchLabels":{"grid.praxis.fast/allowed":"true"},"matchExpressions":[{"key":"tenant","operator":"In","values":["a","b"]},{"key":"restricted","operator":"DoesNotExist"}]}`},
		{"YAML", `matchLabels:
  grid.praxis.fast/allowed: "true"
matchExpressions:
  - key: tenant
    operator: In
    values: [a, b]
  - key: restricted
    operator: DoesNotExist
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GRID_NAMESPACE_SELECTOR", tc.selector)
			cfg, err := GridConfigFromEnv()
			require.NoError(t, err)
			require.Equal(t, "downstream-grid", cfg.Namespace)
			require.Equal(t, "downstream-client", cfg.ServiceAccount)
			require.True(t, cfg.NamespaceSelector.Matches(labels.Set{"grid.praxis.fast/allowed": "true", "tenant": "a"}))
			require.True(t, cfg.NamespaceSelector.Matches(labels.Set{"grid.praxis.fast/allowed": "true", "tenant": "b"}))
			require.False(t, cfg.NamespaceSelector.Matches(labels.Set{"tenant": "a"}))
			require.False(t, cfg.NamespaceSelector.Matches(labels.Set{"grid.praxis.fast/allowed": "true", "tenant": "c"}))
			require.False(t, cfg.NamespaceSelector.Matches(labels.Set{"grid.praxis.fast/allowed": "true", "tenant": "a", "restricted": "true"}))
			require.Equal(t, "rome", cfg.SiteLabels["grid.praxis.fast/provider-site"])
		})
	}

	t.Setenv("GRID_NAMESPACE_SELECTOR", `{"matchExpressions":[{"key":"tenant","operator":"Exists"},{"key":"tier","operator":"NotIn","values":["restricted"]}]}`)
	cfg, err = GridConfigFromEnv()
	require.NoError(t, err)
	require.True(t, cfg.NamespaceSelector.Matches(labels.Set{"tenant": "a", "tier": "open"}))
	require.False(t, cfg.NamespaceSelector.Matches(labels.Set{"tier": "open"}))
	require.False(t, cfg.NamespaceSelector.Matches(labels.Set{"tenant": "a", "tier": "restricted"}))

	t.Setenv("GRID_NAMESPACE_SELECTOR", "{}")
	cfg, err = GridConfigFromEnv()
	require.NoError(t, err)
	require.True(t, cfg.NamespaceSelector.Matches(labels.Set{}))

	for _, tc := range []struct{ key, value string }{
		{"GRID_NAMESPACE_SELECTOR", "broken in ("},
		{"GRID_NAMESPACE_SELECTOR", " \t\n "},
		{"GRID_NAMESPACE_SELECTOR", "null"},
		{"GRID_NAMESPACE_SELECTOR", "*"},
		{"GRID_NAMESPACE_SELECTOR", "tenant=a"},
		{"GRID_NAMESPACE_SELECTOR", `{"matchLabel":{"tenant":"a"}}`},
		{"GRID_NAMESPACE_SELECTOR", `{"matchLabels":{"tenant":"a"},"matchLabels":{"tenant":"b"}}`},
		{"GRID_NAMESPACE_SELECTOR", `{"matchExpressions":[{"key":"tenant","operator":"Equals","values":["a"]}]}`},
		{"GRID_NAMESPACE_SELECTOR", `{"matchExpressions":[{"key":"tenant","operator":"In","values":[]}]}`},
		{"GRID_NAMESPACE_SELECTOR", `{"matchExpressions":[{"key":"tenant","operator":"Exists","values":["a"]}]}`},
		{"GRID_NAMESPACE_SELECTOR", `{"matchLabels":{"bad key":"a"}}`},
		{"GRID_NAMESPACE_SELECTOR", `{"matchLabels":{"tenant":"bad value"}}`},
		{"GRID_NAMESPACE_SELECTOR", "[]"},
		{"GRID_NAMESPACE_SELECTOR", `{"matchLabels":`},
		{"GRID_NETWORK_NAME", ""},
		{"GRID_NAMESPACE", ""},
		{"GRID_NAMESPACE", "not.a.namespace"},
		{"GRID_SERVICE_ACCOUNT", ""},
		{"GRID_TOKEN_SECRET", ""},
		{"GRID_TOKEN_SECRET", "invalid secret"},
		{"GRID_SITE_LABELS", "site in (a,b)"},
	} {
		t.Run(tc.key+"/"+tc.value, func(t *testing.T) {
			t.Setenv(tc.key, tc.value)
			_, err := GridConfigFromEnv()
			require.ErrorContains(t, err, tc.key)
		})
	}
}

func TestGridResourceName(t *testing.T) {
	for _, tc := range []struct {
		name, namespace, serviceName string
	}{
		{"short dotted name", "models", "qwen.2-7b"},
		{"long service name", "models", strings.Repeat("a", 253)},
		{"long namespace", strings.Repeat("n", 63), "model"},
		{"long namespace and service name", strings.Repeat("n", 63), strings.Repeat("a", 253)},
		{"separator at truncation boundary", "models", strings.Repeat("a", 18) + "." + strings.Repeat("b", 40)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &kservev1alpha2.LLMInferenceService{ObjectMeta: metav1.ObjectMeta{
				Name: tc.serviceName, Namespace: tc.namespace, UID: "00000000-0000-0000-0000-000000000001",
			}}
			name := gridResourceName(svc)
			require.Empty(t, validation.IsDNS1123Label(name))
			require.Equal(t, name, gridResourceName(svc.DeepCopy()))
			if tc.name == "short dotted name" {
				require.Equal(t, "grid-models-qwen-2-7b-00000000-0000-0000-0000-000000000001", name)
			}
			svc.Namespace = "other-tenant"
			require.NotEqual(t, name, gridResourceName(svc))
			svc.Namespace = tc.namespace
			svc.UID = "00000000-0000-0000-0000-000000000002"
			require.NotEqual(t, name, gridResourceName(svc))
		})
	}
}
