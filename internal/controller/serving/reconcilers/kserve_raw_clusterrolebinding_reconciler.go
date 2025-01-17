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

var _ SubResourceReconciler = (*KserveRawClusterRoleBindingReconciler)(nil)

type KserveRawClusterRoleBindingReconciler struct {
	client                    client.Client
	clusterRoleBindingHandler resources.ClusterRoleBindingHandler
	deltaProcessor            processors.DeltaProcessor
	serviceAccountName        string
}

func NewKserveRawClusterRoleBindingReconciler(client client.Client) *KserveRawClusterRoleBindingReconciler {
	return &KserveRawClusterRoleBindingReconciler{
		client:                    client,
		clusterRoleBindingHandler: resources.NewClusterRoleBindingHandler(client),
		deltaProcessor:            processors.NewDeltaProcessor(),
		serviceAccountName:        constants.KserveServiceAccountName,
	}
}

func (r *KserveRawClusterRoleBindingReconciler) Reconcile(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {
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

func (r *KserveRawClusterRoleBindingReconciler) Cleanup(ctx context.Context, log logr.Logger, isvcNs string) error {
	log.V(1).Info("Deleting ClusterRoleBinding object for target namespace")
	crbName := r.clusterRoleBindingHandler.GetClusterRoleBindingName(isvcNs, r.serviceAccountName)
	return r.clusterRoleBindingHandler.DeleteClusterRoleBinding(ctx, types.NamespacedName{Name: crbName, Namespace: isvcNs})
}

func (r *KserveRawClusterRoleBindingReconciler) Delete(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {
	log.V(1).Info("Custom CRB Deletion logic triggered")
	if isvc.Spec.Predictor.ServiceAccountName == "" {
		// isvc has default SA, no op
		return nil
	}
	serviceAccount := isvc.Spec.Predictor.ServiceAccountName
	// Get isvc list in namespace and filter for isvcs with the same SA
	inferenceServiceList := &kservev1beta1.InferenceServiceList{}
	if err := r.client.List(ctx, inferenceServiceList, client.InNamespace(isvc.Namespace)); err != nil {
		log.V(1).Info("Error getting InferenceServiceList for CRB cleanup")
		return err
	}
	for i := len(inferenceServiceList.Items) - 1; i >= 0; i-- {
		inferenceService := inferenceServiceList.Items[i]
		if inferenceService.Spec.Predictor.ServiceAccountName != serviceAccount {
			inferenceServiceList.Items = append(inferenceServiceList.Items[:i], inferenceServiceList.Items[i+1:]...)
		}
	}
	log.V(1).Info("len of isvclist", "len", len(inferenceServiceList.Items))
	// If there are no isvcs left with this SA, then the current isvc was the last one, so it is deleted
	if len(inferenceServiceList.Items) == 0 {
		crbName := r.clusterRoleBindingHandler.GetClusterRoleBindingName(isvc.Namespace, serviceAccount)
		return r.clusterRoleBindingHandler.DeleteClusterRoleBinding(ctx, types.NamespacedName{Name: crbName, Namespace: isvc.Namespace})
	}
	// There are other isvcs with the same SA still present in the namespace, so do not delete yet
	return nil
}

func (r *KserveRawClusterRoleBindingReconciler) createDesiredResource(isvc *kservev1beta1.InferenceService) *v1.ClusterRoleBinding {
	isvcSA := r.serviceAccountName
	if isvc.Spec.Predictor.ServiceAccountName != "" {
		isvcSA = isvc.Spec.Predictor.ServiceAccountName
	}
	desiredClusterRoleBindingName := r.clusterRoleBindingHandler.GetClusterRoleBindingName(isvc.Namespace, isvcSA)
	desiredClusterRoleBinding := r.clusterRoleBindingHandler.CreateDesiredClusterRoleBinding(desiredClusterRoleBindingName, isvcSA, isvc.Namespace)
	return desiredClusterRoleBinding
}

func (r *KserveRawClusterRoleBindingReconciler) getExistingResource(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) (*v1.ClusterRoleBinding, error) {
	isvcSA := r.serviceAccountName
	if isvc.Spec.Predictor.ServiceAccountName != r.serviceAccountName {
		isvcSA = isvc.Spec.Predictor.ServiceAccountName
	}
	crbName := r.clusterRoleBindingHandler.GetClusterRoleBindingName(isvc.Namespace, isvcSA)
	return r.clusterRoleBindingHandler.FetchClusterRoleBinding(ctx, log, types.NamespacedName{Name: crbName, Namespace: isvc.Namespace})
}

func (r *KserveRawClusterRoleBindingReconciler) processDelta(ctx context.Context, log logr.Logger, desiredCRB *v1.ClusterRoleBinding, existingCRB *v1.ClusterRoleBinding) (err error) {
	return r.clusterRoleBindingHandler.ProcessDelta(ctx, log, desiredCRB, existingCRB, r.deltaProcessor)
}
