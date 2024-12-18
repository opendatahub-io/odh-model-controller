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
)

type Reconciler interface {
	// Reconcile ensures the resource related to given InferenceService is in the desired state.
	Reconcile(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error
}

type SubResourceReconciler interface {
	Reconciler
	// Delete removes subresource owned by InferenceService.
	Delete(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error
	// Cleanup ensures singleton resource (such as ServiceMonitor) is removed
	// when there is no InferenceServices left in the namespace.
	Cleanup(ctx context.Context, log logr.Logger, isvcNs string) error
}

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
