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
	v1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/processors"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/resources"
)

var _ SubResourceReconciler = (*ModelMeshClusterRoleBindingReconciler)(nil)

type ModelMeshClusterRoleBindingReconciler struct {
	SingleResourcePerNamespace
	client                    client.Client
	clusterRoleBindingHandler resources.ClusterRoleBindingHandler
	deltaProcessor            processors.DeltaProcessor
	serviceAccountName        string
}

func NewModelMeshClusterRoleBindingReconciler(client client.Client) *ModelMeshClusterRoleBindingReconciler {
	return &ModelMeshClusterRoleBindingReconciler{
		client:                    client,
		clusterRoleBindingHandler: resources.NewClusterRoleBindingHandler(client),
		deltaProcessor:            processors.NewDeltaProcessor(),
		serviceAccountName:        constants.ModelMeshServiceAccountName,
	}
}

func (r *ModelMeshClusterRoleBindingReconciler) Reconcile(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {
	log.V(1).Info("Reconciling ClusterRoleBinding for InferenceService")
	// Create Desired resource
	desiredResource := r.createDesiredResource(isvc)

	// Get Existing resource
	existingResource, err := r.getExistingResource(ctx, log, isvc)
	if err != nil {
		return err
	}

	// Process Delta
	if err = r.processDelta(ctx, log, desiredResource, existingResource); err != nil {
		return err
	}
	return nil
}

func (r *ModelMeshClusterRoleBindingReconciler) Cleanup(ctx context.Context, log logr.Logger, isvcNs string) error {
	log.V(1).Info("Deleting ClusterRoleBinding object for target namespace")
	crbName := r.clusterRoleBindingHandler.GetClusterRoleBindingName(isvcNs, r.serviceAccountName)
	return r.clusterRoleBindingHandler.DeleteClusterRoleBinding(ctx, types.NamespacedName{Name: crbName, Namespace: isvcNs})
}

func (r *ModelMeshClusterRoleBindingReconciler) createDesiredResource(isvc *kservev1beta1.InferenceService) *v1.ClusterRoleBinding {
	desiredClusterRoleBindingName := r.clusterRoleBindingHandler.GetClusterRoleBindingName(isvc.Namespace, r.serviceAccountName)
	desiredClusterRoleBinding := r.clusterRoleBindingHandler.CreateDesiredClusterRoleBinding(desiredClusterRoleBindingName, r.serviceAccountName, isvc.Namespace)
	return desiredClusterRoleBinding
}

func (r *ModelMeshClusterRoleBindingReconciler) getExistingResource(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) (*v1.ClusterRoleBinding, error) {
	crbName := r.clusterRoleBindingHandler.GetClusterRoleBindingName(isvc.Namespace, r.serviceAccountName)
	return r.clusterRoleBindingHandler.FetchClusterRoleBinding(ctx, log, types.NamespacedName{Name: crbName, Namespace: isvc.Namespace})
}

func (r *ModelMeshClusterRoleBindingReconciler) processDelta(ctx context.Context, log logr.Logger, desiredCRB *v1.ClusterRoleBinding, existingCRB *v1.ClusterRoleBinding) (err error) {
	return r.clusterRoleBindingHandler.ProcessDelta(ctx, log, desiredCRB, existingCRB, r.deltaProcessor)
}
