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
	"context"

	"github.com/go-logr/logr"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ Reconciler = (*KserveRawInferenceServiceReconciler)(nil)

type KserveRawInferenceServiceReconciler struct {
	client client.Client
}

func NewKServeRawInferenceServiceReconciler(client client.Client) *KserveRawInferenceServiceReconciler {
	return &KserveRawInferenceServiceReconciler{
		client: client,
	}
}

func (r *KserveRawInferenceServiceReconciler) Reconcile(_ context.Context, log logr.Logger, _ *kservev1beta1.InferenceService) error {
	log.V(1).Info("No Reconciliation to be done for inferenceservice as it is using RawDeployment mode")
	return nil
}
