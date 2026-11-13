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

package comparators

import (
	kuadrantv1 "github.com/kuadrant/kuadrant-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func GetAuthPolicyComparator() ResourceComparator {
	return func(current client.Object, desired client.Object) bool {
		currentAP := current.(*kuadrantv1.AuthPolicy)
		desiredAP := desired.(*kuadrantv1.AuthPolicy)

		return equality.Semantic.DeepDerivative(desiredAP.Spec, currentAP.Spec) &&
			sameAuthRules(desiredAP.Spec.AuthScheme, currentAP.Spec.AuthScheme) &&
			equality.Semantic.DeepDerivative(desiredAP.GetLabels(), currentAP.GetLabels())
	}
}

// sameAuthRules reports whether current has no rule or condition that desired lacks, which
// DeepDerivative accepts. Without it a rule the controller stops rendering stays in force.
func sameAuthRules(desired, current *kuadrantv1.AuthSchemeSpec) bool {
	if desired == nil {
		desired = &kuadrantv1.AuthSchemeSpec{}
	}
	if current == nil {
		current = &kuadrantv1.AuthSchemeSpec{}
	}
	if len(desired.Authentication) != len(current.Authentication) || len(desired.Authorization) != len(current.Authorization) {
		return false
	}
	for name, rule := range current.Authentication {
		if len(desired.Authentication[name].Conditions) != len(rule.Conditions) {
			return false
		}
	}
	for name, rule := range current.Authorization {
		if len(desired.Authorization[name].Conditions) != len(rule.Conditions) {
			return false
		}
	}
	return true
}
