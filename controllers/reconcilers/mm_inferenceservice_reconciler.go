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
	"github.com/opendatahub-io/odh-model-controller/controllers/utils"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ Reconciler = (*ModelMeshInferenceServiceReconciler)(nil)

type ModelMeshInferenceServiceReconciler struct {
	SingleResourcePerNamespace
	client                       client.Client
	routeReconciler              *ModelMeshRouteReconciler
	serviceAccountReconciler     *ModelMeshServiceAccountReconciler
	clusterRoleBindingReconciler *ModelMeshClusterRoleBindingReconciler
}

func NewModelMeshInferenceServiceReconciler(client client.Client, scheme *runtime.Scheme) *ModelMeshInferenceServiceReconciler {
	return &ModelMeshInferenceServiceReconciler{
		client:                       client,
		routeReconciler:              NewModelMeshRouteReconciler(client, scheme),
		serviceAccountReconciler:     NewModelMeshServiceAccountReconciler(client, scheme),
		clusterRoleBindingReconciler: NewModelMeshClusterRoleBindingReconciler(client, scheme),
	}
}

func (r *ModelMeshInferenceServiceReconciler) Reconcile(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {

	if err := r.routeReconciler.Reconcile(ctx, log, isvc); err != nil {
		return err
	}

	if err := r.serviceAccountReconciler.Reconcile(ctx, log, isvc); err != nil {
		return err
	}

	if err := r.clusterRoleBindingReconciler.Reconcile(ctx, log, isvc); err != nil {
		return err
	}
	return nil
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
		if isvcDeploymentMode != utils.ModelMesh {
			inferenceServiceList.Items = append(inferenceServiceList.Items[:i], inferenceServiceList.Items[i+1:]...)
		}
	}

	// If there are no ModelMesh InferenceServices in the namespace, delete namespace-scoped resources needed for ModelMesh
	if len(inferenceServiceList.Items) == 0 {

		if err := r.serviceAccountReconciler.Cleanup(ctx, log, isvcNs); err != nil {
			return err
		}

		if err := r.clusterRoleBindingReconciler.Cleanup(ctx, log, isvcNs); err != nil {
			return err
		}
	}
	return nil
}
