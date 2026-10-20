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

func TestGridFlowControlHeaders(t *testing.T) {
	const fairnessHeader = "x-gateway-inference-fairness-id"
	const objectiveHeader = "x-gateway-inference-objective"
	const gridUser = "system:serviceaccount:grid-system:grid-client"
	const issuer = "https://issuer.example.com"
	loader := resources.NewKServeAuthPolicyTemplateLoader(nil)
	target := resources.AuthPolicyTarget{Kind: "Gateway", Name: "gateway", Namespace: "infra", AuthType: constants.UserDefined}
	baseline, err := loader.Load(context.Background(), target, resources.WithAudiences([]string{issuer}))
	require.NoError(t, err)
	policy, err := loader.Load(context.Background(), target, resources.WithAudiences([]string{issuer}),
		resources.WithGridFlowControlHeaders("grid-system", "grid-client"))
	require.NoError(t, err)
	require.Equal(t, baseline.Spec.AuthScheme.Authorization, policy.Spec.AuthScheme.Authorization)
	require.Equal(t, baseline.Spec.AuthScheme.Response, policy.Spec.AuthScheme.Response)
	env, err := cel.NewEnv(cel.Variable("auth", cel.DynType), cel.Variable("request", cel.DynType), ext.Strings())
	require.NoError(t, err)
	for _, tc := range []struct {
		name, user, fairness, objective string
		headers                         map[string]string
	}{
		{"preserve both", gridUser, "tenant-a", "premium", map[string]string{fairnessHeader: "tenant-a", objectiveHeader: "premium"}},
		{"missing both", gridUser, issuer, "grid-system", map[string]string{}},
		{"missing fairness", gridUser, issuer, "premium", map[string]string{objectiveHeader: "premium"}},
		{"missing objective", gridUser, "tenant-a", "grid-system", map[string]string{fairnessHeader: "tenant-a"}},
		{"same namespace other account", "system:serviceaccount:grid-system:other", issuer, "grid-system", map[string]string{fairnessHeader: "spoof", objectiveHeader: "spoof"}},
		{"same account other namespace", "system:serviceaccount:other:grid-client", issuer, "other", map[string]string{fairnessHeader: "spoof", objectiveHeader: "spoof"}},
		{"regular user", "alice", issuer, "authenticated", map[string]string{fairnessHeader: "spoof", objectiveHeader: "spoof"}},
		{"present empty headers", gridUser, "", "", map[string]string{fairnessHeader: "", objectiveHeader: ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for name, expected := range map[string]string{"fairness": tc.fairness, "objective": tc.objective} {
				override := policy.Spec.AuthScheme.Authentication["kubernetes-user"].Overrides[name]
				require.Empty(t, override.Value.Raw)
				ast, issues := env.Compile(string(override.Expression))
				require.NoError(t, issues.Err())
				program, err := env.Program(ast)
				require.NoError(t, err)
				value, _, err := program.Eval(map[string]interface{}{
					"auth":    map[string]interface{}{"identity": map[string]interface{}{"user": map[string]interface{}{"username": tc.user}}},
					"request": map[string]interface{}{"headers": tc.headers},
				})
				require.NoError(t, err)
				require.Equal(t, expected, value.Value())
			}
		})
	}

	t.Run("custom objective fallback", func(t *testing.T) {
		custom, err := loader.Load(context.Background(), target,
			resources.WithObjectiveExpression("'custom-objective'"), resources.WithGridFlowControlHeaders("grid-system", "grid-client"))
		require.NoError(t, err)
		ast, issues := env.Compile(string(custom.Spec.AuthScheme.Authentication["kubernetes-user"].Overrides["objective"].Expression))
		require.NoError(t, issues.Err())
		program, err := env.Program(ast)
		require.NoError(t, err)
		value, _, err := program.Eval(map[string]interface{}{
			"auth":    map[string]interface{}{"identity": map[string]interface{}{"user": map[string]interface{}{"username": gridUser}}},
			"request": map[string]interface{}{"headers": map[string]string{}},
		})
		require.NoError(t, err)
		require.Equal(t, "custom-objective", value.Value())
	})

	for _, identity := range [][2]string{{"", "grid-client"}, {"grid-system", ""}} {
		unchanged, err := loader.Load(context.Background(), target, resources.WithAudiences([]string{issuer}),
			resources.WithGridFlowControlHeaders(identity[0], identity[1]))
		require.NoError(t, err)
		require.Equal(t, baseline, unchanged)
	}
}
