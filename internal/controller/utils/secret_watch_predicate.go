package utils

import (
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
)

// ShouldEnqueueInferenceServiceForSecret reports whether a Secret informer event may
// enqueue an InferenceService reconcile via the ISVC Secret watch handler.
func ShouldEnqueueInferenceServiceForSecret(obj client.Object) bool {
	if obj == nil || IsRayTLSSecret(obj.GetName()) {
		return false
	}
	labels := obj.GetLabels()
	if labels == nil {
		return false
	}
	return labels[constants.ODHManaged] == "true"
}

// InferenceServiceSecretWatchPredicate limits Secret watch events to ODH-managed secrets.
func InferenceServiceSecretWatchPredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return ShouldEnqueueInferenceServiceForSecret(e.Object)
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			return ShouldEnqueueInferenceServiceForSecret(e.ObjectNew)
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return ShouldEnqueueInferenceServiceForSecret(e.Object)
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return ShouldEnqueueInferenceServiceForSecret(e.Object)
		},
	}
}
