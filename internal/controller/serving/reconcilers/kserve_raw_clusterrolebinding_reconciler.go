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
	desiredResource, err := r.createDesiredResource(ctx, log, isvc)
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

func (r *KserveRawClusterRoleBindingReconciler) Delete(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {
	isvcSA := r.serviceAccountName
	if len(isvc.Spec.Predictor.ServiceAccountName) > 0 {
		isvcSA = isvc.Spec.Predictor.ServiceAccountName
	}
	existingIsvcs, err := getExistingIsvcsWithTheSameSAandAuthEnabled(ctx, r.client, log, isvcSA, isvc.Namespace)
	if err != nil {
		return err
	}
	if len(existingIsvcs) == 0 {
		crbName := r.clusterRoleBindingHandler.GetClusterRoleBindingName(isvc.Namespace, isvcSA)
		log.V(1).Info("Deleting ClusterRoleBinding " + crbName + " in namespace " + isvc.Namespace)
		return r.clusterRoleBindingHandler.DeleteClusterRoleBinding(ctx, types.NamespacedName{Name: crbName, Namespace: isvc.Namespace})
	}
	return nil
}

func (r *KserveRawClusterRoleBindingReconciler) createDesiredResource(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) (*v1.ClusterRoleBinding, error) {
	isvcSA := r.serviceAccountName
	if isvc.Spec.Predictor.ServiceAccountName != "" {
		isvcSA = isvc.Spec.Predictor.ServiceAccountName
	}

	if val, ok := isvc.Annotations[constants.EnableAuthODHAnnotation]; !ok || val != "true" {
		existingIsvcs, err := getExistingIsvcsWithTheSameSAandAuthEnabled(ctx, r.client, log, isvcSA, isvc.Namespace)
		if err != nil {
			return nil, err
		}
		if len(existingIsvcs) == 0 {
			// This ISVC does not request auth and no other ISVC using the same SA requests auth, so no CRB is needed
			return nil, nil
		}
		// if execution has reached here, it means at least one other ISVC using the same SA requests auth
		// thus, CRB is needed
	}

	desiredClusterRoleBindingName := r.clusterRoleBindingHandler.GetClusterRoleBindingName(isvc.Namespace, isvcSA)
	desiredClusterRoleBinding := r.clusterRoleBindingHandler.CreateDesiredClusterRoleBinding(desiredClusterRoleBindingName, isvcSA, isvc.Namespace)
	return desiredClusterRoleBinding, nil
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

func (r *KserveRawClusterRoleBindingReconciler) Cleanup(_ context.Context, _ logr.Logger, _ string) error {
	// NO OP
	return nil
}

func getExistingIsvcsWithTheSameSAandAuthEnabled(ctx context.Context, cli client.Client, log logr.Logger, isvcSA string, namespace string) ([]kservev1beta1.InferenceService, error) {
	var isvcList kservev1beta1.InferenceServiceList
	listOpts := client.ListOptions{Namespace: namespace}
	// List all inference services in the namespace
	if err := cli.List(ctx, &isvcList, &listOpts); err != nil {
		log.Error(err, "failed to list inference services")
		return nil, err
	}
	var existingIsvcs []kservev1beta1.InferenceService
	for _, svc := range isvcList.Items {
		if svc.GetDeletionTimestamp() != nil {
			continue
		}
		if val, ok := svc.Annotations[constants.EnableAuthODHAnnotation]; ok && val == "true" {
			if isvcSA == constants.KserveServiceAccountName && len(svc.Spec.Predictor.ServiceAccountName) == 0 {
				existingIsvcs = append(existingIsvcs, svc)
			} else if isvcSA == svc.Spec.Predictor.ServiceAccountName {
				existingIsvcs = append(existingIsvcs, svc)
			}
		}
	}
	return existingIsvcs, nil
}
