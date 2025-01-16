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

	istiosecv1beta1 "istio.io/client-go/pkg/apis/security/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func GetPeerAuthenticationComparator() ResourceComparator {
	return func(deployed client.Object, requested client.Object) bool {
		deployedPeerAuthentication := deployed.(*istiosecv1beta1.PeerAuthentication)
		requestedPeerAuthentication := requested.(*istiosecv1beta1.PeerAuthentication)
		return reflect.DeepEqual(deployedPeerAuthentication.Spec.Selector, requestedPeerAuthentication.Spec.Selector) &&
			reflect.DeepEqual(deployedPeerAuthentication.Spec.Mtls, requestedPeerAuthentication.Spec.Mtls) &&
			reflect.DeepEqual(deployedPeerAuthentication.Spec.PortLevelMtls, requestedPeerAuthentication.Spec.PortLevelMtls)
	}
}
