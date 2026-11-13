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

package resources

import (
	_ "embed"
	"encoding/json"
	"fmt"

	authorinov1beta3 "github.com/kuadrant/authorino/api/v1beta3"
	kuadrantv1 "github.com/kuadrant/kuadrant-operator/api/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

//go:embed template/authpolicy_grid_client_certificate.yaml
var authPolicyGridClientCertificate []byte

// gridClientCertificateRules is the embedded fragment, parsed once so a bad one fails at start.
var gridClientCertificateRules = func() *kuadrantv1.AuthSchemeSpec {
	rules := &kuadrantv1.AuthSchemeSpec{}
	if err := yaml.UnmarshalStrict(authPolicyGridClientCertificate, rules); err != nil {
		panic(fmt.Sprintf("invalid Grid client certificate rules: %v", err))
	}
	return rules
}()

// gridClientCertificatePresent matches a request the Gateway verified a client certificate for.
const gridClientCertificatePresent = "'x-forwarded-client-cert' in request.headers"

// ObjectOption is a functional option for modifying Kubernetes objects.
type ObjectOption func(client.Object)

// WithLabels returns an ObjectOption that merges the given labels into the object.
func WithLabels(labels map[string]string) ObjectOption {
	return func(obj client.Object) {
		existing := obj.GetLabels()
		if existing == nil {
			existing = make(map[string]string)
		}
		for k, v := range labels {
			existing[k] = v
		}
		obj.SetLabels(existing)
	}
}

// WithAudiences returns an ObjectOption that sets the audiences for all KubernetesTokenReview authentications
// and updates the fairness override to match the first audience (issuer).
func WithAudiences(audiences []string) ObjectOption {
	return func(obj client.Object) {
		ap, ok := obj.(*kuadrantv1.AuthPolicy)
		if !ok || ap.Spec.AuthScheme == nil {
			return
		}
		for key, auth := range ap.Spec.AuthScheme.Authentication {
			if auth.KubernetesTokenReview != nil {
				auth.KubernetesTokenReview.Audiences = audiences
				if len(audiences) > 0 && auth.Overrides != nil {
					if fairness, exists := auth.Overrides["fairness"]; exists {
						raw, _ := json.Marshal(audiences[0])
						fairness.Value = k8sruntime.RawExtension{Raw: raw}
						auth.Overrides["fairness"] = fairness
					}
				}
				ap.Spec.AuthScheme.Authentication[key] = auth
			}
		}
	}
}

// WithObjectiveExpression returns an ObjectOption that sets the objective override expression.
func WithObjectiveExpression(expression string) ObjectOption {
	return func(obj client.Object) {
		ap, ok := obj.(*kuadrantv1.AuthPolicy)
		if !ok || ap.Spec.AuthScheme == nil {
			return
		}
		for key, auth := range ap.Spec.AuthScheme.Authentication {
			if auth.Overrides != nil {
				if objective, exists := auth.Overrides["objective"]; exists {
					objective.Expression = authorinov1beta3.CelExpression(expression)
					auth.Overrides["objective"] = objective
				}
				ap.Spec.AuthScheme.Authentication[key] = auth
			}
		}
	}
}

// WithGridFlowControlHeaders preserves caller flow-control headers only for the
// configured Grid identity authenticated by Kubernetes TokenReview. Apply after
// audience and objective options so absent headers retain their configured defaults.
func WithGridFlowControlHeaders(namespace, serviceAccount string) ObjectOption {
	return func(obj client.Object) {
		ap, ok := obj.(*kuadrantv1.AuthPolicy)
		if !ok || ap.Spec.AuthScheme == nil || namespace == "" || serviceAccount == "" {
			return
		}
		username := "system:serviceaccount:" + namespace + ":" + serviceAccount
		for key, auth := range ap.Spec.AuthScheme.Authentication {
			if auth.KubernetesTokenReview == nil {
				continue
			}
			for name, header := range map[string]string{
				"fairness":  "x-gateway-inference-fairness-id",
				"objective": "x-gateway-inference-objective",
			} {
				override, exists := auth.Overrides[name]
				if !exists {
					continue
				}
				fallback := string(override.Expression)
				if fallback == "" {
					fallback = string(override.Value.Raw)
				}
				if fallback == "" {
					continue
				}
				override.Expression = authorinov1beta3.CelExpression(fmt.Sprintf(
					"auth.identity.user.username == %q && %q in request.headers ? request.headers[%q] : (%s)",
					username, header, header, fallback))
				override.Value = k8sruntime.RawExtension{}
				auth.Overrides[name] = override
			}
			ap.Spec.AuthScheme.Authentication[key] = auth
		}
	}
}

// WithGridClientCertificate authorizes the named site's Grid certificate, which the Gateway
// verifies on the listener at port and reports in x-forwarded-client-cert, as the Grid
// service account. Token rules stop applying to such a request. Apply before
// WithGridFlowControlHeaders, so the fairness default is read before that option rewrites it.
func WithGridClientCertificate(namespace, serviceAccount, siteName string, port int32) ObjectOption {
	return func(obj client.Object) {
		ap, ok := obj.(*kuadrantv1.AuthPolicy)
		if !ok || ap.Spec.AuthScheme == nil || namespace == "" || serviceAccount == "" || siteName == "" || port == 0 {
			return
		}
		user, err := json.Marshal("system:serviceaccount:" + namespace + ":" + serviceAccount)
		if err != nil {
			return
		}
		fairness := fmt.Sprintf("%q", namespace)
		if override, exists := ap.Spec.AuthScheme.Authentication["kubernetes-user"].Overrides["fairness"]; exists {
			if override.Expression != "" {
				fairness = string(override.Expression)
			} else if len(override.Value.Raw) > 0 {
				fairness = string(override.Value.Raw)
			}
		}
		header := func(name, fallback string) authorinov1beta3.ValueOrSelector {
			return authorinov1beta3.ValueOrSelector{Expression: authorinov1beta3.CelExpression(
				fmt.Sprintf("%q in request.headers ? request.headers[%q] : (%s)", name, name, fallback))}
		}
		rules := gridClientCertificateRules.DeepCopy()
		site := rules.Authentication["grid-site"]
		site.Overrides = authorinov1beta3.ExtendedProperties{
			"fairness":  header("x-gateway-inference-fairness-id", fairness),
			"objective": header("x-gateway-inference-objective", fmt.Sprintf("%q", namespace)),
		}
		rules.Authentication["grid-site"] = site
		// The mesh appends the element it verified, so the site is read from the end of the
		// header and one element may name one URI. siteName is a DNS label, so it adds nothing
		// to the pattern but its own letters.
		rules.Authorization["grid-local-site"].PatternMatching.Patterns[0].Predicate = fmt.Sprintf(
			`request.headers['x-forwarded-client-cert'].matches(';URI=spiffe://grid\\.internal/site/%s(;DNS=[^;,"]*)*$')`+
				` && !request.headers['x-forwarded-client-cert'].matches(';URI=[^,]*;URI=')`, siteName)
		rules.Authorization["grid-model-access"].KubernetesSubjectAccessReview.User = &authorinov1beta3.ValueOrSelector{
			Value: k8sruntime.RawExtension{Raw: user},
		}

		// Another listener may trust another CA, so only the Grid listener's certificate counts.
		listener := fmt.Sprintf("destination.port == %d", port)
		predicate := func(expression string) authorinov1beta3.PatternExpressionOrRef {
			return authorinov1beta3.PatternExpressionOrRef{CelPredicate: authorinov1beta3.CelPredicate{Predicate: expression}}
		}
		for key, auth := range rules.Authentication {
			auth.Conditions = append(auth.Conditions, predicate(listener))
			rules.Authentication[key] = auth
		}
		for key, rule := range rules.Authorization {
			rule.Conditions = append(rule.Conditions, predicate(listener))
			rules.Authorization[key] = rule
		}
		absent := predicate("!(" + gridClientCertificatePresent + " && " + listener + ")")
		for key, auth := range ap.Spec.AuthScheme.Authentication {
			auth.Conditions = append(auth.Conditions, absent)
			ap.Spec.AuthScheme.Authentication[key] = auth
		}
		// A rule that reads the token identity would fail closed anyway; this one would
		// also spend a SubjectAccessReview on every Grid request.
		if rule, exists := ap.Spec.AuthScheme.Authorization["model-access-path"]; exists {
			rule.Conditions = append(rule.Conditions, absent)
			ap.Spec.AuthScheme.Authorization["model-access-path"] = rule
		}
		for key, auth := range rules.Authentication {
			ap.Spec.AuthScheme.Authentication[key] = auth
		}
		if ap.Spec.AuthScheme.Authorization == nil {
			ap.Spec.AuthScheme.Authorization = map[string]kuadrantv1.MergeableAuthorizationSpec{}
		}
		for key, rule := range rules.Authorization {
			ap.Spec.AuthScheme.Authorization[key] = rule
		}
	}
}
