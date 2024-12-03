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

	"github.com/hashicorp/go-multierror"
	"github.com/opendatahub-io/odh-model-controller/controllers/constants"

	"github.com/go-logr/logr"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"github.com/opendatahub-io/odh-model-controller/controllers/utils"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ Reconciler = (*ModelMeshInferenceServiceReconciler)(nil)

type ModelMeshInferenceServiceReconciler struct {
	client                 client.Client
	subResourceReconcilers []SubResourceReconciler
}

func NewModelMeshInferenceServiceReconciler(client client.Client) *ModelMeshInferenceServiceReconciler {
	return &ModelMeshInferenceServiceReconciler{
		client: client,
		subResourceReconcilers: []SubResourceReconciler{
			NewModelMeshRouteReconciler(client),
			NewServiceAccountReconciler(client, constants.ModelMeshServiceAccountName),
			NewModelMeshClusterRoleBindingReconciler(client),
		},
	}
}

func (r *ModelMeshInferenceServiceReconciler) Reconcile(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {
	var reconcileErrors *multierror.Error
	for _, reconciler := range r.subResourceReconcilers {
		reconcileErrors = multierror.Append(reconcileErrors, reconciler.Reconcile(ctx, log, isvc))
	}

	return reconcileErrors.ErrorOrNil()
}

func (r *ModelMeshInferenceServiceReconciler) DeleteModelMeshResourcesIfNoMMIsvcExists(ctx context.Context, log logr.Logger, isvcNs string) error {
	inferenceServiceList := &kservev1beta1.InferenceServiceList{}
	if err := r.client.List(ctx, inferenceServiceList, client.InNamespace(isvcNs)); err != nil {
		return err
	}

	for i := len(inferenceServiceList.Items) - 1; i >= 0; i-- {
		inferenceService := inferenceServiceList.Items[i]
		isvcDeploymentMode, err := utils.GetDeploymentModeForIsvc(ctx, r.client, &inferenceService)
		if err != nil {
			return err
		}
		if isvcDeploymentMode != constants.ModelMesh {
			inferenceServiceList.Items = append(inferenceServiceList.Items[:i], inferenceServiceList.Items[i+1:]...)
		}
	}

	// If there are no ModelMesh InferenceServices in the namespace, delete namespace-scoped resources needed for ModelMesh
	var cleanupErrors *multierror.Error
	if len(inferenceServiceList.Items) == 0 {
		for _, reconciler := range r.subResourceReconcilers {
			cleanupErrors = multierror.Append(cleanupErrors, reconciler.Cleanup(ctx, log, isvcNs))
		}
	}

	return cleanupErrors.ErrorOrNil()
}
