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

	istioclientv1alpha3 "istio.io/client-go/pkg/apis/networking/v1alpha3"
	"k8s.io/apimachinery/pkg/api/equality"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func GetEnvoyFilterComparator() ResourceComparator {
	return func(current client.Object, desired client.Object) bool {
		currentEF := current.(*istioclientv1alpha3.EnvoyFilter)
		desiredEF := desired.(*istioclientv1alpha3.EnvoyFilter)

		// Compare individual fields to avoid copying lock values from protobuf structs
		return reflect.DeepEqual(desiredEF.Spec.TargetRefs, currentEF.Spec.TargetRefs) &&
			reflect.DeepEqual(desiredEF.Spec.ConfigPatches, currentEF.Spec.ConfigPatches) &&
			desiredEF.Spec.Priority == currentEF.Spec.Priority &&
			equality.Semantic.DeepDerivative(desiredEF.GetLabels(), currentEF.GetLabels())
	}
}
