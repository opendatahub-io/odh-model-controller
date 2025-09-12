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
	"reflect"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func GetAuthPolicyComparator() ResourceComparator {
	return func(deployed client.Object, requested client.Object) bool {
		deployedAP, dok := deployed.(*unstructured.Unstructured)
		requestedAP, rok := requested.(*unstructured.Unstructured)
		if !dok || !rok || deployedAP == nil || requestedAP == nil {
			return false
		}

		// Compare spec and labels
		deployedSpec, f1, err1 := unstructured.NestedMap(deployedAP.Object, "spec")
		requestedSpec, f2, err2 := unstructured.NestedMap(requestedAP.Object, "spec")
		if err1 != nil || err2 != nil {
			return false
		}
		if !f1 {
			deployedSpec = map[string]interface{}{}
		}
		if !f2 {
			requestedSpec = map[string]interface{}{}
		}

		return reflect.DeepEqual(deployedSpec, requestedSpec) &&
			reflect.DeepEqual(filterLabels(deployedAP.GetLabels()), filterLabels(requestedAP.GetLabels()))
	}
}

func filterLabels(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		if k == "kubectl.kubernetes.io/last-applied-configuration" ||
			strings.HasPrefix(k, "app.kubernetes.io/managed-by") {
			continue
		}
		out[k] = v
	}
	return out
}
