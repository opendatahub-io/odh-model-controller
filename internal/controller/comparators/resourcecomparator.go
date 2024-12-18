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
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ResourceComparator would compare deployed & requested resource. It would retrun `true` if both resource are same else it would return `false`
type ResourceComparator func(deployed client.Object, requested client.Object) bool
