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
	"github.com/opendatahub-io/odh-model-controller/controllers/constants"
	"github.com/opendatahub-io/odh-model-controller/controllers/processors"
	"github.com/opendatahub-io/odh-model-controller/controllers/resources"
	v1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ SubResourceReconciler = (*KserveRawRoleBindingReconciler)(nil)

type KserveRawRoleBindingReconciler struct {
	client             client.Client
	roleBindingHandler resources.RoleBindingHandler
	deltaProcessor     processors.DeltaProcessor
	serviceAccountName string
}

func NewKserveRawRoleBindingReconciler(client client.Client) *KserveRawRoleBindingReconciler {
	return &KserveRawRoleBindingReconciler{
		client:             client,
		roleBindingHandler: resources.NewRoleBindingHandler(client),
		deltaProcessor:     processors.NewDeltaProcessor(),
		serviceAccountName: constants.KserveServiceAccountName,
	}
}

func (r *KserveRawRoleBindingReconciler) Reconcile(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {
	log.V(1).Info("Reconciling RoleBinding for InferenceService")
	// Create Desired resource
	desiredResource, err := r.createDesiredResource(isvc)
	if err != nil {
		return err
	}

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

func (r *KserveRawRoleBindingReconciler) Cleanup(ctx context.Context, log logr.Logger, isvcNs string) error {
	log.V(1).Info("Deleting RoleBinding object for target namespace")
	rbName := r.roleBindingHandler.GetRoleBindingName(isvcNs, r.serviceAccountName)
	return r.roleBindingHandler.DeleteRoleBinding(ctx, types.NamespacedName{Name: rbName, Namespace: isvcNs})
}

func (r *KserveRawRoleBindingReconciler) Delete(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {
	log.V(1).Info("Custom RoleBinding Deletion logic triggered")
	if isvc.Spec.Predictor.ServiceAccountName == "" {
		// isvc has default SA, no op
		return nil
	}
	serviceAccount := isvc.Spec.Predictor.ServiceAccountName
	// Get isvc list in namespace and filter for isvcs with the same SA
	inferenceServiceList := &kservev1beta1.InferenceServiceList{}
	if err := r.client.List(ctx, inferenceServiceList, client.InNamespace(isvc.Namespace)); err != nil {
		log.V(1).Info("Error getting InferenceServiceList for RoleBinding cleanup")
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
		rbName := r.roleBindingHandler.GetRoleBindingName(isvc.Namespace, serviceAccount)
		return r.roleBindingHandler.DeleteRoleBinding(ctx, types.NamespacedName{Name: rbName, Namespace: isvc.Namespace})
	}
	// There are other isvcs with the same SA still present in the namespace, so do not delete yet
	return nil
}

func (r *KserveRawRoleBindingReconciler) createDesiredResource(isvc *kservev1beta1.InferenceService) (*v1.RoleBinding, error) {
	isvcSA := r.serviceAccountName
	if isvc.Spec.Predictor.ServiceAccountName != "" {
		isvcSA = isvc.Spec.Predictor.ServiceAccountName
	}
	desiredRoleBindingName := r.roleBindingHandler.GetRoleBindingName(isvc.Namespace, isvcSA)
	desiredRoleBinding := r.roleBindingHandler.CreateDesiredRoleBinding(desiredRoleBindingName, isvcSA, isvc.Namespace)
	return desiredRoleBinding, nil
}

func (r *KserveRawRoleBindingReconciler) getExistingResource(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) (*v1.RoleBinding, error) {
	isvcSA := r.serviceAccountName
	if isvc.Spec.Predictor.ServiceAccountName != r.serviceAccountName {
		isvcSA = isvc.Spec.Predictor.ServiceAccountName
	}
	rbName := r.roleBindingHandler.GetRoleBindingName(isvc.Namespace, isvcSA)
	return r.roleBindingHandler.FetchRoleBinding(ctx, log, types.NamespacedName{Name: rbName, Namespace: isvc.Namespace})
}

func (r *KserveRawRoleBindingReconciler) processDelta(ctx context.Context, log logr.Logger, desiredRB *v1.RoleBinding, existingRB *v1.RoleBinding) (err error) {
	return r.roleBindingHandler.ProcessDelta(ctx, log, desiredRB, existingRB, r.deltaProcessor)
}
