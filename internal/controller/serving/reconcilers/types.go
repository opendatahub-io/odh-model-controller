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

// Reconciler is a basic reconciler interface for InferenceService
type Reconciler interface {
	// Reconcile ensures the resource related to given InferenceService is in the desired state.
	Reconcile(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error
}

// GenericSubResourceReconciler interface that can handle both InferenceService and LLMInferenceService
type GenericSubResourceReconciler[T client.Object] interface {
	// Reconcile ensures the resource related to given object is in the desired state.
	Reconcile(ctx context.Context, log logr.Logger, obj T) error
	// Delete removes subresource owned by the object.
	Delete(ctx context.Context, log logr.Logger, obj T) error
	// Cleanup ensures singleton resource (such as ServiceMonitor) is removed
	// when there are no objects left in the namespace.
	Cleanup(ctx context.Context, log logr.Logger, objNs string) error
}

// SubResourceReconciler interface for InferenceService-specific reconcilers (backward compatibility)
type SubResourceReconciler = GenericSubResourceReconciler[*kservev1beta1.InferenceService]

// NoResourceRemoval is a trait to indicate that given reconciler
// is not supposed to delete any resources left.
type NoResourceRemoval struct{}

func (r *NoResourceRemoval) Delete(_ context.Context, _ logr.Logger, _ *kservev1beta1.InferenceService) error {
	// NOOP
	return nil
}

func (r *NoResourceRemoval) Cleanup(_ context.Context, _ logr.Logger, _ string) error {
	// NOOP
	return nil
}

// SingleResourcePerNamespace is a trait to indicate that given reconciler is only supposed to
// clean up owned resources when there is no relevant ISVC left.
type SingleResourcePerNamespace struct{}

func (r *SingleResourcePerNamespace) Delete(_ context.Context, _ logr.Logger, _ *kservev1beta1.InferenceService) error {
	// NOOP it needs to be cleaned up when no ISVCs left in the Namespace
	return nil
}
