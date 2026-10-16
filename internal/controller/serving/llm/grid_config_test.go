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
	"os"
	"strings"
	"testing"

	kservev1alpha2 "github.com/kserve/kserve/pkg/apis/serving/v1alpha2"
	"github.com/stretchr/testify/require"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestGridOperatorIdentity(t *testing.T) {
	t.Setenv("POD_NAMESPACE", "platform-custom")
	for _, name := range []string{GridOperatorNamespaceEnv, GridOperatorServiceAccountEnv} {
		t.Setenv(name, "")
		require.NoError(t, os.Unsetenv(name))
	}
	identity, err := gridOperatorIdentity()
	require.NoError(t, err)
	require.Equal(t, rbacv1.Subject{Kind: "ServiceAccount", Name: "grid-operator", Namespace: "platform-custom"}, identity)
	t.Run("explicit installation identity", func(t *testing.T) {
		t.Setenv(GridOperatorNamespaceEnv, "separate-platform")
		t.Setenv(GridOperatorServiceAccountEnv, "custom-operator")
		identity, err := gridOperatorIdentity()
		require.NoError(t, err)
		require.Equal(t, rbacv1.Subject{Kind: "ServiceAccount", Name: "custom-operator", Namespace: "separate-platform"}, identity)
	})
	for _, tc := range []struct{ env, value string }{
		{GridOperatorNamespaceEnv, ""}, {GridOperatorNamespaceEnv, "invalid.namespace"},
		{GridOperatorServiceAccountEnv, ""}, {GridOperatorServiceAccountEnv, "invalid account"}, {"POD_NAMESPACE", ""},
	} {
		t.Run(tc.env+"="+tc.value, func(t *testing.T) {
			t.Setenv(tc.env, tc.value)
			_, err := gridOperatorIdentity()
			require.Error(t, err)
		})
	}
}

func TestGridConfigFromGateway(t *testing.T) {
	t.Setenv(GridGatewayNamespacesEnv, "openshift-ingress,redhat-ods-applications,opendatahub")
	valid := `{"networkName":"grid-local","namespace":"grid-system","serviceAccount":"grid-client","tokenSecret":"grid-token","siteLabels":"grid.praxis.fast/provider-site=local"}`
	ns := &gatewayv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "openshift-ingress", Annotations: map[string]string{GridServingAnnotation: valid}}}
	cfg, err := GridConfigFromGateway(ns)
	require.NoError(t, err)
	require.Equal(t, "grid-local", cfg.NetworkName)
	require.Equal(t, "local", cfg.SiteLabels["grid.praxis.fast/provider-site"])
	for _, value := range []string{`null`, `[]`, `{}`, `{"unknown":true}`, strings.Replace(valid, `"networkName":"grid-local"`, `"networkName":"grid-local","networkName":"other"`, 1), valid + ` {}`, strings.Replace(valid, "grid-system", "not.a.namespace", 1), strings.Replace(valid, "grid-token", "invalid secret", 1), strings.Replace(valid, "grid.praxis.fast/provider-site=local", "site in (a,b)", 1)} {
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
