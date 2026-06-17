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

package reconcilers

import (
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"
)

// AsOwnerRef returns the non-controlling OwnerReference representation of the given object.
func AsOwnerRef(obj metav1.Object, gvk schema.GroupVersionKind) metav1.OwnerReference {
	or := *metav1.NewControllerRef(obj, gvk)
	// Setting Controller to false makes this a non-controlling owner reference.
	// The owned object will be garbage collected when the ISVC is deleted,
	// but the ISVC doesn't "control" its lifecycle in other controller-runtime senses.
	or.Controller = ptr.To(false)
	return or
}

// AsIsvcOwnerRef returns the non-controlling OwnerReference representation of the given isvc.
func AsIsvcOwnerRef(isvc *kservev1beta1.InferenceService) metav1.OwnerReference {
	return AsOwnerRef(isvc, kservev1beta1.SchemeGroupVersion.WithKind("InferenceService"))
}
