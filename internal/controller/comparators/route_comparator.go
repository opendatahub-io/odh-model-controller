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

	v1 "github.com/openshift/api/route/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func GetMMRouteComparator() ResourceComparator {
	return func(deployed client.Object, requested client.Object) bool {
		deployedRoute := deployed.(*v1.Route)
		requestedRoute := requested.(*v1.Route)
		return reflect.DeepEqual(deployedRoute.Spec.To, requestedRoute.Spec.To) &&
			reflect.DeepEqual(deployedRoute.Spec.Port, requestedRoute.Spec.Port) &&
			reflect.DeepEqual(deployedRoute.Spec.WildcardPolicy, requestedRoute.Spec.WildcardPolicy) &&
			reflect.DeepEqual(deployedRoute.Spec.Path, requestedRoute.Spec.Path) &&
			reflect.DeepEqual(deployedRoute.Spec.TLS, requestedRoute.Spec.TLS) &&
			reflect.DeepEqual(deployedRoute.Labels, requestedRoute.Labels) &&
			reflect.DeepEqual(deployedRoute.Annotations, requestedRoute.Annotations)
	}
}

func GetKServeRouteComparator() ResourceComparator {
	return func(deployed client.Object, requested client.Object) bool {
		deployedRoute := deployed.(*v1.Route)
		requestedRoute := requested.(*v1.Route)
		return reflect.DeepEqual(deployedRoute.Spec.Host, requestedRoute.Spec.Host) &&
			reflect.DeepEqual(deployedRoute.Spec.To, requestedRoute.Spec.To) &&
			reflect.DeepEqual(deployedRoute.Spec.Port, requestedRoute.Spec.Port) &&
			reflect.DeepEqual(deployedRoute.Spec.TLS, requestedRoute.Spec.TLS) &&
			reflect.DeepEqual(deployedRoute.Spec.WildcardPolicy, requestedRoute.Spec.WildcardPolicy) &&
			reflect.DeepEqual(deployedRoute.ObjectMeta.Labels, requestedRoute.ObjectMeta.Labels) &&
			reflect.DeepEqual(deployedRoute.ObjectMeta.Annotations, requestedRoute.ObjectMeta.Annotations)
	}
}
