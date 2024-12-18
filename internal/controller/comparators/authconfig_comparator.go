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

	authorinov1beta2 "github.com/kuadrant/authorino/api/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func GetAuthConfigComparator() ResourceComparator {
	return func(deployed client.Object, requested client.Object) bool {
		deployedAC := deployed.(*authorinov1beta2.AuthConfig)
		requestedAC := requested.(*authorinov1beta2.AuthConfig)
		return reflect.DeepEqual(deployedAC.Spec, requestedAC.Spec) &&
			reflect.DeepEqual(deployedAC.Labels, requestedAC.Labels)
	}
}
