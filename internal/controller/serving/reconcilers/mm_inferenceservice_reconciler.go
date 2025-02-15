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
	"github.com/hashicorp/go-multierror"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
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

func (r *ModelMeshInferenceServiceReconciler) DeleteModelMeshResourcesIfNoMMIsvcExists(ctx context.Context, log logr.Logger, namespace string) error {
	var cleanupErrors *multierror.Error
	for _, reconciler := range r.subResourceReconcilers {
		cleanupErrors = multierror.Append(cleanupErrors, reconciler.Cleanup(ctx, log, namespace))
	}
	return cleanupErrors.ErrorOrNil()
}
