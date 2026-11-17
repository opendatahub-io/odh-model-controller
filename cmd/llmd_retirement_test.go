package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRetiredLLMDControllerIsNotRegistered(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	require.NoError(t, err)
	for _, imported := range file.Imports {
		require.NotEqual(t,
			`"github.com/opendatahub-io/odh-model-controller/internal/controller/serving/llm"`,
			imported.Path.Value)
	}
	registered := map[string]bool{}
	ast.Inspect(file, func(node ast.Node) bool {
		function, ok := node.(*ast.FuncDecl)
		if !ok || function.Name.Name != "setupReconcilers" {
			return true
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if ok {
				if name, ok := call.Fun.(*ast.Ident); ok {
					registered[name.Name] = true
				}
			}
			return true
		})
		return false
	})
	require.NotContains(t, registered, "setupLLMInferenceServiceReconciler")
	for _, name := range []string{
		"setupInferenceServiceReconciler",
		"setupServingRuntimeReconciler",
		"setupInferenceGraphReconciler",
		"setupSecretReconciler",
		"setupConfigMapReconciler",
		"setupPodReconciler",
	} {
		require.True(t, registered[name], "%s must remain registered", name)
	}
}
