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
	"fmt"

	"github.com/go-logr/logr"
	servingv1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	authorinov1beta2 "github.com/kuadrant/authorino/api/v1beta2"
	v1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/comparators"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/processors"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/resources"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
)

// InferenceGraphReconciler reconciles a InferenceGraph object
type InferenceGraphReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	Recorder       record.EventRecorder
	deltaProcessor processors.DeltaProcessor
	detector       resources.AuthTypeDetector
	templateLoader resources.AuthConfigTemplateLoader
	hostExtractor  resources.InferenceEndpointsHostExtractor
}

// +kubebuilder:rbac:groups=serving.kserve.io,resources=inferencegraphs,verbs=get;list;watch
// +kubebuilder:rbac:groups=serving.kserve.io,resources=inferencegraphs/finalizers,verbs=update
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=authorino.kuadrant.io,resources=authconfigs,verbs=get;list;watch;create;update;patch;delete

func NewInferenceGraphReconciler(mgr ctrl.Manager) *InferenceGraphReconciler {
	return &InferenceGraphReconciler{
		Client:         mgr.GetClient(),
		Scheme:         mgr.GetScheme(),
		Recorder:       mgr.GetEventRecorderFor("serving-inferencegraph-controller"),
		deltaProcessor: processors.NewDeltaProcessor(),
		detector:       resources.NewKServeAuthTypeDetector(mgr.GetClient()),
		templateLoader: resources.NewStaticTemplateLoader(),
		hostExtractor:  resources.NewKServeInferenceServiceHostExtractor(),
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

	if err = r.reconcileAuthConfig(ctx, logger, ig); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *InferenceGraphReconciler) reconcileAuthConfig(ctx context.Context, logger logr.Logger, ig *servingv1alpha1.InferenceGraph) error {
	logger.V(1).Info("Reconciling Authorino AuthConfig for InferenceGraph")
	authorinoEnabled, capabilityErr := utils.VerifyIfMeshAuthorizationIsEnabled(ctx, r.Client)
	if capabilityErr != nil {
		logger.V(1).Error(capabilityErr, "Error while verifying if Authorino is enabled")
		return capabilityErr
	}
	if !authorinoEnabled {
		logger.V(1).Info("Skipping AuthConfig reconciliation, authorization is not enabled")

		authType := r.detector.Detect(ctx, ig.GetAnnotations())
		if authType == resources.UserDefined {
			// Raise an event that the IG wants auth enabled, but the auth stack is missing
			r.Recorder.Eventf(ig, v1.EventTypeWarning, constants.AuthUnavailable, "InferenceGraph %s is requiring auth, but the auth stack is not available", ig.GetName())
		}

		return nil
	}

	if ig.Status.URL == nil {
		logger.V(1).Info("Inference graph URL not yet initialized")
		return nil
	}

	desiredState, err := r.createDesiredAuthConfig(ctx, ig)
	if err != nil {
		return err
	}

	existingState, err := r.getExistingAuthConfig(ctx, ig)
	if err != nil && !apierrs.IsNotFound(err) {
		return err
	}

	if err = r.processAuthConfigDelta(ctx, logger, desiredState, existingState); err != nil {
		return err
	}

	return nil
}

func (r *InferenceGraphReconciler) createDesiredAuthConfig(ctx context.Context, ig *servingv1alpha1.InferenceGraph) (*authorinov1beta2.AuthConfig, error) {
	authType := r.detector.Detect(ctx, ig.GetAnnotations())
	template, err := r.templateLoader.Load(ctx, authType, ig)
	if err != nil {
		return nil, fmt.Errorf("failed to load template for AuthType %s: %w", authType, err)
	}

	template.Name = ig.GetName() + "-ig"
	template.Namespace = ig.GetNamespace()
	template.Spec.Hosts = r.hostExtractor.Extract(ig)
	if template.Labels == nil {
		template.Labels = map[string]string{}
	}
	template.Labels[constants.LabelAuthGroup] = "default"

	err = ctrl.SetControllerReference(ig, &template, r.Scheme)
	if err != nil {
		return nil, err
	}

	return &template, nil
}

func (r *InferenceGraphReconciler) getExistingAuthConfig(ctx context.Context, ig *servingv1alpha1.InferenceGraph) (*authorinov1beta2.AuthConfig, error) {
	typeName := types.NamespacedName{Namespace: ig.GetNamespace(), Name: ig.GetName()}
	existing := authorinov1beta2.AuthConfig{}
	err := r.Client.Get(ctx, typeName, &existing)
	if err != nil {
		if apierrs.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return &existing, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *InferenceGraphReconciler) SetupWithManager(mgr ctrl.Manager, isServerlessMode bool) error {
	builder := ctrl.NewControllerManagedBy(mgr).
		For(&servingv1alpha1.InferenceGraph{}).
		Named("serving-inferencegraph")
	if !isServerlessMode {
		return builder.Complete(r)
	}

	// dynamic add authconfig to watchlist based on serving mode + if authconfig crd is available
	authConfigCrdAvailable, authCrdErr := utils.IsCrdAvailable(
		mgr.GetConfig(),
		authorinov1beta2.GroupVersion.String(),
		"AuthConfig")
	if authCrdErr != nil {
		return fmt.Errorf("failed to check if  AuthConfig CRD in the cluster: %w", authCrdErr)
	}
	if authConfigCrdAvailable {
		builder.Owns(&authorinov1beta2.AuthConfig{})
	}
	return builder.Complete(r)
}

func (r *InferenceGraphReconciler) processAuthConfigDelta(ctx context.Context, logger logr.Logger, desiredState *authorinov1beta2.AuthConfig, existingState *authorinov1beta2.AuthConfig) error {
	logger.WithValues("auth_config_name", desiredState.GetName())
	comparator := comparators.GetAuthConfigComparator()
	delta := r.deltaProcessor.ComputeDelta(comparator, desiredState, existingState)

	if !delta.HasChanges() {
		logger.V(1).Info("No delta found")
		return nil
	}

	if delta.IsAdded() {
		logger.V(1).Info("Delta found", "action", "create")
		err := r.Client.Create(ctx, desiredState)
		if err != nil {
			return fmt.Errorf("failed to create AuthConfig %s on namespace %s: %w", desiredState.GetName(), desiredState.GetNamespace(), err)
		}
	} else if delta.IsUpdated() {
		logger.V(1).Info("Delta found", "action", "update")
		existingCopy := existingState.DeepCopy()
		existingCopy.Spec = desiredState.Spec
		existingCopy.Labels = desiredState.Labels
		err := r.Client.Update(ctx, existingCopy)
		if err != nil {
			return fmt.Errorf("failed to update AuthConfig %s on namespace %s: %w", desiredState.GetName(), desiredState.GetNamespace(), err)
		}
	}
	return nil
}
