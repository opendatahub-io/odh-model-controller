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
	istiov1beta1 "istio.io/api/networking/v1beta1"
	istioclientv1beta1 "istio.io/client-go/pkg/apis/networking/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func GetGatewayComparator() ResourceComparator {
	return func(existing client.Object, desired client.Object) bool {
		existingGateway := existing.(*istioclientv1beta1.Gateway)
		desiredGateway := desired.(*istioclientv1beta1.Gateway)

		exists := false
		for _, server := range existingGateway.Spec.Servers {
			if serversEqual(server, desiredGateway.Spec.Servers[0]) {
				exists = true
				break
			}
		}

		return exists
	}
}

// serversEquals compare if the inferenceservice name matches got the given resources
func serversEqual(s1, s2 *istiov1beta1.Server) bool {
	return s1.Port.Name == s2.Port.Name
}
