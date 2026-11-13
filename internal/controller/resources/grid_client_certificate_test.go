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

package resources_test

import (
	"context"
	"testing"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/ext"
	"github.com/stretchr/testify/require"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/resources"
)

func TestGridClientCertificate(t *testing.T) {
	const xfcc = "x-forwarded-client-cert"
	const issuer = "https://issuer.example.com"
	certificate := func(site string) string {
		return `Hash=ab12;Cert="-----BEGIN%20CERTIFICATE-----";Subject="CN=` + site + `,O=` + site + `";URI=spiffe://grid.internal/site/` + site + `;DNS=` + site + `.grid.internal`
	}
	loader := resources.NewKServeAuthPolicyTemplateLoader(nil)
	target := resources.AuthPolicyTarget{Kind: "Gateway", Name: "gateway", Namespace: "infra", AuthType: constants.UserDefined}
	baseline, err := loader.Load(context.Background(), target, resources.WithAudiences([]string{issuer}))
	require.NoError(t, err)
	policy, err := loader.Load(context.Background(), target, resources.WithAudiences([]string{issuer}),
		resources.WithGridClientCertificate("grid-system", "grid-client", "site-a"),
		resources.WithGridFlowControlHeaders("grid-system", "grid-client"))
	require.NoError(t, err)
	scheme := policy.Spec.AuthScheme
	env, err := cel.NewEnv(cel.Variable("auth", cel.DynType), cel.Variable("request", cel.DynType), ext.Strings())
	require.NoError(t, err)
	eval := func(t *testing.T, expression string, headers map[string]string, path string) interface{} {
		t.Helper()
		ast, issues := env.Compile(expression)
		require.NoError(t, issues.Err(), expression)
		program, err := env.Program(ast)
		require.NoError(t, err)
		value, _, err := program.Eval(map[string]interface{}{
			"auth":    map[string]interface{}{"identity": map[string]interface{}{"anonymous": true}},
			"request": map[string]interface{}{"headers": headers, "path": path},
		})
		require.NoError(t, err)
		return value.Value()
	}

	t.Run("token rules are unchanged apart from standing aside for a certificate", func(t *testing.T) {
		require.Equal(t, baseline.Spec.AuthScheme.Response, scheme.Response)
		for name, rule := range baseline.Spec.AuthScheme.Authorization {
			got := scheme.Authorization[name]
			if name == "model-access-path" {
				got.Conditions = got.Conditions[:len(got.Conditions)-1]
			}
			require.Equal(t, rule, got, name)
		}
		token := scheme.Authentication["kubernetes-user"]
		require.Len(t, token.Conditions, 1)
		review := scheme.Authorization["model-access-path"].Conditions
		for _, predicate := range []string{token.Conditions[0].Predicate, review[len(review)-1].Predicate} {
			require.Equal(t, false, eval(t, predicate, map[string]string{xfcc: certificate("site-a")}, "/"))
			require.Equal(t, true, eval(t, predicate, map[string]string{}, "/"))
		}
	})

	t.Run("a verified certificate is the identity", func(t *testing.T) {
		grid := scheme.Authentication["grid-site"]
		require.NotNil(t, grid.AnonymousAccess)
		require.Nil(t, grid.KubernetesTokenReview)
		require.Len(t, grid.Conditions, 1)
		require.Equal(t, true, eval(t, grid.Conditions[0].Predicate, map[string]string{xfcc: certificate("site-a")}, "/"))
		require.Equal(t, false, eval(t, grid.Conditions[0].Predicate, map[string]string{}, "/"))
	})

	t.Run("only the local site is authorized", func(t *testing.T) {
		rule := scheme.Authorization["grid-local-site"]
		require.Equal(t, 0, rule.Priority)
		require.Len(t, rule.PatternMatching.Patterns, 1)
		predicate := rule.PatternMatching.Patterns[0].Predicate
		for value, allowed := range map[string]bool{
			certificate("site-a"):  true,
			certificate("site-b"):  false,
			certificate("site-ab"): false,
			certificate("xsite-a"): false,
			`Hash=ab12;Subject="CN=x";URI=spiffe://gridXinternal/site/site-a`:       false,
			`Hash=ab12;Subject="CN=x";URI=spiffe://grid.internal/site/site-a/extra`: false,
			`Hash=ab12;Subject="CN=x";DNS=site-a.grid.internal`:                     false,
			`Hash=ab12;Subject="CN=x";URI=spiffe://grid.internal/site/site-a`:       true,
			// An element a caller put in front of the one the mesh appended.
			`Hash=cd34;URI=spiffe://grid.internal/site/site-a,` + certificate("site-b"): false,
			certificate("site-b") + `,` + certificate("site-a"):                         true,
			// The local name smuggled in a field a signer might copy from a request.
			`Hash=ab12;Subject="CN=;URI=spiffe://grid.internal/site/site-a;";URI=spiffe://grid.internal/site/site-b`: false,
			`Hash=ab12;Subject="CN=x";URI=spiffe://grid.internal/site/site-b;URI=spiffe://grid.internal/site/site-a`: false,
			`Hash=ab12;Subject="CN=x";URI=spiffe://grid.internal/site/site-a;DNS=a;URI=spiffe://x`:                   false,
		} {
			require.Equal(t, allowed, eval(t, predicate, map[string]string{xfcc: value}, "/"), value)
		}
	})

	t.Run("the model is checked as the Grid service account, once per model per minute", func(t *testing.T) {
		rule := scheme.Authorization["grid-model-access"]
		review := rule.KubernetesSubjectAccessReview
		require.NotNil(t, review)
		require.JSONEq(t, `"system:serviceaccount:grid-system:grid-client"`, string(review.User.Value.Raw))
		expected := baseline.Spec.AuthScheme.Authorization["model-access-path"].KubernetesSubjectAccessReview.ResourceAttributes
		require.Equal(t, expected, review.ResourceAttributes, "the same resource the token path reviews")
		require.NotNil(t, rule.Cache)
		require.Equal(t, 60, rule.Cache.TTL)
		key := string(rule.Cache.Key.Expression)
		first := eval(t, key, nil, "/publishers/ns/models/m/v1/chat/completions")
		require.Equal(t, first, eval(t, key, nil, "/publishers/ns/models/m/v1/completions"), "one entry per model")
		require.NotEqual(t, first, eval(t, key, nil, "/publishers/ns/models/other/v1/chat/completions"))
		require.NotEqual(t, first, eval(t, key, nil, "/publishers/other/models/m/v1/chat/completions"))
		// A namespace or model named v1 must not fold several models into one entry.
		for _, pair := range [][2]string{
			{"/publishers/v1/models/a/v1/chat/completions", "/publishers/v1/models/b/v1/chat/completions"},
			{"/publishers/ns/models/v1/v1/chat/completions", "/publishers/ns/models/v1x/v1/chat/completions"},
		} {
			require.NotEqual(t, eval(t, key, nil, pair[0]), eval(t, key, nil, pair[1]), pair)
		}
		require.Equal(t, string(review.ResourceAttributes.Name.Expression), key, "the key is the reviewed name")
	})

	t.Run("a certificate reaches only publisher paths", func(t *testing.T) {
		rule := scheme.Authorization["grid-publisher-path-only"]
		require.Equal(t, "false", rule.PatternMatching.Patterns[0].Predicate)
		applies := func(path string) bool {
			for _, condition := range rule.Conditions {
				if eval(t, condition.Predicate, map[string]string{xfcc: certificate("site-a")}, path) != true {
					return false
				}
			}
			return true
		}
		require.False(t, applies("/publishers/ns/models/m/v1/chat/completions"))
		for _, path := range []string{"/ns/name/v1/chat/completions", "/v1/files", "/v1/chat/completions", "/"} {
			require.True(t, applies(path), path)
		}
	})

	t.Run("flow control defaults match the token identity", func(t *testing.T) {
		overrides := scheme.Authentication["grid-site"].Overrides
		for name, tc := range map[string]struct{ header, fallback string }{
			"fairness":  {"x-gateway-inference-fairness-id", issuer},
			"objective": {"x-gateway-inference-objective", "grid-system"},
		} {
			expression := string(overrides[name].Expression)
			require.Equal(t, tc.fallback, eval(t, expression, map[string]string{}, "/"))
			require.Equal(t, "tenant-a", eval(t, expression, map[string]string{tc.header: "tenant-a"}, "/"))
		}
	})

	for _, identity := range [][3]string{{"", "grid-client", "site-a"}, {"grid-system", "", "site-a"}, {"grid-system", "grid-client", ""}} {
		unchanged, err := loader.Load(context.Background(), target, resources.WithAudiences([]string{issuer}),
			resources.WithGridClientCertificate(identity[0], identity[1], identity[2]))
		require.NoError(t, err)
		require.Equal(t, baseline, unchanged)
	}
}
