/*
Copyright 2024.

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

package serving

import (
	"context"

	servingv1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/processors"
)

// InferenceGraphReconciler reconciles a InferenceGraph object
type InferenceGraphReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	Recorder       record.EventRecorder
	deltaProcessor processors.DeltaProcessor
}

// +kubebuilder:rbac:groups=serving.kserve.io,resources=inferencegraphs,verbs=get;list;watch
// +kubebuilder:rbac:groups=serving.kserve.io,resources=inferencegraphs/finalizers,verbs=update
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch

func NewInferenceGraphReconciler(mgr ctrl.Manager) *InferenceGraphReconciler {
	return &InferenceGraphReconciler{
		Client:         mgr.GetClient(),
		Scheme:         mgr.GetScheme(),
		Recorder:       mgr.GetEventRecorderFor("serving-inferencegraph-controller"),
		deltaProcessor: processors.NewDeltaProcessor(),
	}
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *InferenceGraphReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("InferenceGraph", req.Name, "namespace", req.Namespace)

	ig := &servingv1alpha1.InferenceGraph{}
	err := r.Client.Get(ctx, req.NamespacedName, ig)
	if err != nil && apierrs.IsNotFound(err) {
		if apierrs.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Unable to get the InferenceGraph resource")
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *InferenceGraphReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&servingv1alpha1.InferenceGraph{}).
		Named("serving-inferencegraph").
		Complete(r)
}
