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

	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func GetInferenceServiceComparator() ResourceComparator {
	return func(deployed client.Object, requested client.Object) bool {
		deployedInferenceService := deployed.(*kservev1beta1.InferenceService)
		requestedInferenceService := requested.(*kservev1beta1.InferenceService)
		return reflect.DeepEqual(deployedInferenceService.Labels[constants.ModelRegistryInferenceServiceIdLabel], requestedInferenceService.Labels[constants.ModelRegistryInferenceServiceIdLabel]) &&
			reflect.DeepEqual(deployedInferenceService.Labels[constants.ModelRegistryRegisteredModelIdLabel], requestedInferenceService.Labels[constants.ModelRegistryRegisteredModelIdLabel]) &&
			reflect.DeepEqual(deployedInferenceService.Labels[constants.ModelRegistryModelVersionIdLabel], requestedInferenceService.Labels[constants.ModelRegistryModelVersionIdLabel])
	}
}
