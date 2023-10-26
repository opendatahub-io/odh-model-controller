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

type ModelMeshInferenceServiceReconciler struct {
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

	log.V(1).Info("Reconciling Route for InferenceService")
	if err := r.routeReconciler.Reconcile(ctx, log, isvc); err != nil {
		return err
	}

	log.V(1).Info("Reconciling ServiceAccount for InferenceService")
	if err := r.serviceAccountReconciler.Reconcile(ctx, log, isvc); err != nil {
		return err
	}

	log.V(1).Info("Reconciling ClusterRoleBinding for InferenceService")
	if err := r.clusterRoleBindingReconciler.Reconcile(ctx, log, isvc); err != nil {
		return err
	}
	return nil
}

func (r *ModelMeshInferenceServiceReconciler) DeleteModelMeshResourcesIfNoMMIsvcExists(ctx context.Context, log logr.Logger, isvcNamespace string) error {
	inferenceServiceList := &kservev1beta1.InferenceServiceList{}
	if err := r.client.List(ctx, inferenceServiceList, client.InNamespace(isvcNamespace)); err != nil {
		return err
	}

	for i := len(inferenceServiceList.Items) - 1; i >= 0; i-- {
		inferenceService := inferenceServiceList.Items[i]
		if !utils.IsDeploymentModeForIsvcModelMesh(&inferenceService) {
			inferenceServiceList.Items = append(inferenceServiceList.Items[:i], inferenceServiceList.Items[i+1:]...)
		}
	}

	// If there are no ModelMesh InferenceServices in the namespace, delete namespace-scoped resources needed for ModelMesh
	if len(inferenceServiceList.Items) == 0 {

		log.V(1).Info("Deleting ServiceAccount object for target namespace")
		if err := r.serviceAccountReconciler.DeleteServiceAccount(ctx, isvcNamespace); err != nil {
			return err
		}

		log.V(1).Info("Deleting ClusterRoleBinding object for target namespace")
		if err := r.clusterRoleBindingReconciler.DeleteClusterRoleBinding(ctx, isvcNamespace); err != nil {
			return err
		}
	}
	return nil
}
