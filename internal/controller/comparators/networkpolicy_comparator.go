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

	v1 "k8s.io/api/networking/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func GetNetworkPolicyComparator() ResourceComparator {
	return func(deployed client.Object, requested client.Object) bool {
		deployedCRB := deployed.(*v1.NetworkPolicy)
		requestedCRB := requested.(*v1.NetworkPolicy)
		return reflect.DeepEqual(deployedCRB.Spec, requestedCRB.Spec) &&
			reflect.DeepEqual(deployedCRB.Labels, requestedCRB.Labels)
	}
}
