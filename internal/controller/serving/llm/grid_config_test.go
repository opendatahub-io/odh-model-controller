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
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestGridConfigFromGateway(t *testing.T) {
	t.Setenv(GridGatewayNamespacesEnv, "openshift-ingress,redhat-ods-applications,opendatahub")
	valid := `{"networkName":"grid-local","namespace":"grid-system","serviceAccount":"grid-client","tokenSecret":"grid-token","siteLabels":"grid.praxis.fast/provider-site=local"}`
	ns := &gatewayv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "openshift-ingress", Annotations: map[string]string{GridServingAnnotation: valid}}}
	cfg, err := GridConfigFromGateway(ns)
	require.NoError(t, err)
	require.Equal(t, "grid-local", cfg.NetworkName)
	require.Equal(t, "local", cfg.SiteLabels["grid.praxis.fast/provider-site"])
	require.False(t, cfg.PublishMetrics)
	t.Run("metrics plaintext opt-in", func(t *testing.T) {
		copy := ns.DeepCopy()
		copy.Annotations[GridServingAnnotation] = strings.Replace(valid, `}`, `,"publishMetrics":true}`, 1)
		cfg, err := GridConfigFromGateway(copy)
		require.NoError(t, err)
		require.True(t, cfg.PublishMetrics)
		require.False(t, cfg.MetricsAllowPlaintext)
		copy.Annotations[GridServingAnnotation] = strings.Replace(valid, `}`, `,"publishMetrics":true,"metricsAllowPlaintext":true}`, 1)
		cfg, err = GridConfigFromGateway(copy)
		require.NoError(t, err)
		require.True(t, cfg.MetricsAllowPlaintext)
	})
	for _, value := range []string{strings.Replace(valid, `}`, `,"publishMetrics":"true"}`, 1), strings.Replace(valid, `}`, `,"metricsAllowPlaintext":"true"}`, 1), `null`, `[]`, `{}`, `{"unknown":true}`, strings.Replace(valid, `"networkName":"grid-local"`, `"networkName":"grid-local","networkName":"other"`, 1), valid + ` {}`, strings.Replace(valid, "grid-system", "not.a.namespace", 1), strings.Replace(valid, "grid-token", "invalid secret", 1), strings.Replace(valid, "grid.praxis.fast/provider-site=local", "site in (a,b)", 1)} {
		t.Run(value, func(t *testing.T) {
			copy := ns.DeepCopy()
			copy.Annotations[GridServingAnnotation] = value
			_, err := GridConfigFromGateway(copy)
			require.Error(t, err)
		})
	}
	for _, namespace := range []string{"openshift-ingress", "redhat-ods-applications", "opendatahub", "grid-system", "models", "openshift-ingress-other", ""} {
		t.Run("Gateway namespace "+namespace, func(t *testing.T) {
			copy := ns.DeepCopy()
			copy.Namespace = namespace
			cfg, err := GridConfigFromGateway(copy)
			if namespace == "openshift-ingress" || namespace == "redhat-ods-applications" || namespace == "opendatahub" {
				require.NoError(t, err)
				require.Equal(t, "grid-system", cfg.Namespace)
			} else {
				require.ErrorContains(t, err, GridGatewayNamespacesEnv)
				require.Nil(t, cfg)
			}
		})
	}
	t.Run("configured namespaces replace defaults", func(t *testing.T) {
		t.Setenv(GridGatewayNamespacesEnv, " custom-ingress, another-ingress ")
		for _, namespace := range []string{"custom-ingress", "another-ingress"} {
			copy := ns.DeepCopy()
			copy.Namespace = namespace
			cfg, err := GridConfigFromGateway(copy)
			require.NoError(t, err)
			require.Equal(t, "grid-system", cfg.Namespace)
		}
		_, err := GridConfigFromGateway(ns)
		require.Error(t, err)
	})
	t.Run("explicit empty list denies all", func(t *testing.T) {
		t.Setenv(GridGatewayNamespacesEnv, "")
		_, err := GridConfigFromGateway(ns)
		require.Error(t, err)
	})
	delete(ns.Annotations, GridServingAnnotation)
	cfg, err = GridConfigFromGateway(ns)
	require.NoError(t, err)
	require.Nil(t, cfg)
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
			gateway := &gatewayv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "infra", Name: "gateway", UID: "gateway-uid"}}
			name := gridResourceName(svc, gateway)
			require.Empty(t, validation.IsDNS1123Label(name))
			require.Equal(t, name, gridResourceName(svc.DeepCopy(), gateway))
			for _, modify := range []func(*gatewayv1.Gateway){
				func(g *gatewayv1.Gateway) { g.Name = "another-gateway" },
				func(g *gatewayv1.Gateway) { g.Namespace = "another-infra" },
				func(g *gatewayv1.Gateway) { g.UID = "recreated-gateway" },
			} {
				other := gateway.DeepCopy()
				modify(other)
				require.NotEqual(t, name, gridResourceName(svc, other))
			}
			svc.Namespace = "other-tenant"
			require.NotEqual(t, name, gridResourceName(svc, gateway))
			svc.Namespace = tc.namespace
			svc.UID = "00000000-0000-0000-0000-000000000002"
			require.NotEqual(t, name, gridResourceName(svc, gateway))
		})
	}
}

func TestReconcileGridMetrics(t *testing.T) {
	const generated = "http://model-epp-service.models.svc.cluster.local:9090"
	const moved = "http://model-epp-service.models.svc.cluster.local:9091"
	const secureEndpoint = "https://model-epp-service.models.svc.cluster.local:9090"
	// path and timeout are left to the CRD defaults, so they appear only after an
	// API-server round trip; the envtest asserts them, this unit test does not.
	managed := func(endpoint string) map[string]interface{} {
		return map[string]interface{}{"endpoint": endpoint,
			"auth": map[string]interface{}{"type": "serviceAccountToken", "allowPlaintext": true}}
	}
	admin := map[string]interface{}{"endpoint": "https://metrics.example.com", "path": "/stats"}
	for _, tc := range []struct {
		name                string
		current             map[string]interface{}
		recorded            string
		endpoint            string
		secure              bool
		notGranted          bool
		wantConfig          map[string]interface{}
		wantRecorded        string
		wantNoMetricsConfig bool
	}{
		{name: "generates when absent", endpoint: generated, wantConfig: managed(generated), wantRecorded: generated},
		{name: "leaves Grid's plaintext refusal in place unless the Gateway grants it", endpoint: generated, notGranted: true,
			wantConfig: map[string]interface{}{"endpoint": generated, "auth": map[string]interface{}{"type": "serviceAccountToken"}}, wantRecorded: generated},
		{name: "withdraws the allowance when the Gateway stops granting it", current: managed(generated), recorded: generated, endpoint: generated, notGranted: true,
			wantConfig: map[string]interface{}{"endpoint": generated, "auth": map[string]interface{}{"type": "serviceAccountToken"}}, wantRecorded: generated},
		{name: "follows a moved EPP port", current: managed(generated), recorded: generated, endpoint: moved, wantConfig: managed(moved), wantRecorded: moved},
		{name: "regenerates after the administrator removes it", recorded: generated, endpoint: generated, wantConfig: managed(generated), wantRecorded: generated},
		{name: "keeps an administrator config", current: admin, endpoint: generated, wantConfig: admin},
		{name: "yields to an administrator endpoint edit", current: admin, recorded: generated, endpoint: generated, wantConfig: admin},
		{name: "removes a generated config when disabled", current: managed(generated), recorded: generated, wantNoMetricsConfig: true},
		{name: "keeps an administrator config when disabled", current: admin, wantConfig: admin},
		{name: "stays absent when disabled", wantNoMetricsConfig: true},
		{name: "names the service CA and refuses plaintext when the port declares https", endpoint: secureEndpoint, secure: true,
			wantConfig: map[string]interface{}{"endpoint": secureEndpoint,
				"auth": map[string]interface{}{"type": "serviceAccountToken"},
				"tls": map[string]interface{}{"caConfigMapRef": map[string]interface{}{
					"name": "openshift-service-ca.crt", "namespace": "grid-system", "key": "service-ca.crt"}}},
			wantRecorded: secureEndpoint},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := map[string]interface{}{"endpoint": "http://gateway"}
			if tc.current != nil {
				spec["metricsConfig"] = runtime.DeepCopyJSONValue(tc.current)
			}
			annotations := map[string]string{"other": "kept"}
			if tc.recorded != "" {
				annotations[gridMetricsEndpoint] = tc.recorded
			}
			reconcileGridMetrics(spec, annotations, tc.endpoint, tc.secure, !tc.notGranted, "grid-system")
			require.Equal(t, "http://gateway", spec["endpoint"])
			require.Equal(t, "kept", annotations["other"])
			if tc.wantNoMetricsConfig {
				require.NotContains(t, spec, "metricsConfig")
			} else {
				require.Equal(t, tc.wantConfig, spec["metricsConfig"])
			}
			if tc.wantRecorded == "" {
				require.NotContains(t, annotations, gridMetricsEndpoint)
			} else {
				require.Equal(t, tc.wantRecorded, annotations[gridMetricsEndpoint])
			}
		})
	}
	t.Run("keeps a plaintext refusal across a TLS period and back", func(t *testing.T) {
		current := managed(generated)
		current["auth"].(map[string]interface{})["allowPlaintext"] = false
		spec := map[string]interface{}{"metricsConfig": current}
		annotations := map[string]string{gridMetricsEndpoint: generated}
		allowPlaintext := func() interface{} {
			return spec["metricsConfig"].(map[string]interface{})["auth"].(map[string]interface{})["allowPlaintext"]
		}
		reconcileGridMetrics(spec, annotations, secureEndpoint, true, true, "grid-system")
		require.Equal(t, false, allowPlaintext())
		reconcileGridMetrics(spec, annotations, generated, false, true, "grid-system")
		require.Equal(t, false, allowPlaintext())
		require.NotContains(t, spec["metricsConfig"], "tls")
	})
	t.Run("drops a granted allowance under TLS and grants it again on return", func(t *testing.T) {
		spec := map[string]interface{}{"metricsConfig": managed(generated)}
		annotations := map[string]string{gridMetricsEndpoint: generated}
		reconcileGridMetrics(spec, annotations, secureEndpoint, true, true, "grid-system")
		require.NotContains(t, spec["metricsConfig"].(map[string]interface{})["auth"], "allowPlaintext")
		reconcileGridMetrics(spec, annotations, generated, false, true, "grid-system")
		require.Equal(t, managed(generated), spec["metricsConfig"])
	})
	t.Run("preserves administrator tuning of a generated config", func(t *testing.T) {
		current := managed(generated)
		current["timeout"] = "500ms"
		current["poolName"] = "model-pool"
		spec := map[string]interface{}{"metricsConfig": current}
		annotations := map[string]string{gridMetricsEndpoint: generated}
		reconcileGridMetrics(spec, annotations, moved, false, true, "grid-system")
		got := spec["metricsConfig"].(map[string]interface{})
		require.Equal(t, moved, got["endpoint"])
		require.Equal(t, "500ms", got["timeout"])
		require.Equal(t, "model-pool", got["poolName"])
	})
}
