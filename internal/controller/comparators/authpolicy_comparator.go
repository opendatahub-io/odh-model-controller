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
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func GetAuthPolicyComparator() ResourceComparator {
	return func(deployed client.Object, requested client.Object) bool {
		deployedAP := deployed.(*unstructured.Unstructured)
		requestedAP := requested.(*unstructured.Unstructured)

		return equality.Semantic.DeepDerivative(deployedAP.Object["spec"], requestedAP.Object["spec"]) &&
			equality.Semantic.DeepDerivative(deployedAP.GetLabels(), requestedAP.GetLabels())
	}
}
