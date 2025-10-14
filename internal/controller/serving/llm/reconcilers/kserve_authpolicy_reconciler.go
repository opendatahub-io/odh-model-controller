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
	"fmt"

	"github.com/go-logr/logr"
	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kuadrantv1 "github.com/kuadrant/kuadrant-operator/api/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/comparators"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/processors"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/resources"
	parentreconcilers "github.com/opendatahub-io/odh-model-controller/internal/controller/serving/reconcilers"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
)

var _ parentreconcilers.LLMSubResourceReconciler = (*KserveAuthPolicyReconciler)(nil)

type KserveAuthPolicyReconciler struct {
	client         client.Client
	scheme         *runtime.Scheme
	deltaProcessor processors.DeltaProcessor
	detector       resources.AuthPolicyDetector
	templateLoader resources.AuthPolicyTemplateLoader
	store          resources.AuthPolicyStore
	matcher        resources.AuthPolicyMatcher
}

func NewKserveAuthPolicyReconciler(client client.Client, scheme *runtime.Scheme) *KserveAuthPolicyReconciler {
	return &KserveAuthPolicyReconciler{
		client:         client,
		scheme:         scheme,
		deltaProcessor: processors.NewDeltaProcessor(),
		detector:       resources.NewKServeAuthPolicyDetector(client),
		templateLoader: resources.NewKServeAuthPolicyTemplateLoader(client),
		store:          resources.NewClientAuthPolicyStore(client),
		matcher:        resources.NewKServeAuthPolicyMatcher(client),
	}
}

func (r *KserveAuthPolicyReconciler) Reconcile(ctx context.Context, log logr.Logger, llmisvc *kservev1alpha1.LLMInferenceService) error {
	log.V(1).Info("Starting AuthPolicy reconciliation for LLMInferenceService")

	if err := r.reconcileGatewayAuthpolicy(ctx, log, llmisvc); err != nil {
		log.Error(err, "Failed to reconcile Gateway AuthPolicy")
		return err
	}

	if err := r.reconcileHTTPRouteAuthpolicy(ctx, log, llmisvc); err != nil {
		log.Error(err, "Failed to reconcile HTTPRoute AuthPolicy")
		return err
	}

	return nil
}

func (r *KserveAuthPolicyReconciler) reconcileGatewayAuthpolicy(ctx context.Context, log logr.Logger, llmisvc *kservev1alpha1.LLMInferenceService) error {
	log.V(1).Info("Reconciling Gateway AuthPolicy")

	desiredAuthPolicies, err := r.templateLoader.Load(ctx, constants.UserDefined, llmisvc)
	if err != nil {
		log.Error(err, "Failed to load Gateway AuthPolicy templates")
		return err
	}

	for _, desired := range desiredAuthPolicies {
		existing, err := r.getExistingAuthPolicy(ctx, desired.GetName(), desired.GetNamespace())
		if err != nil && !apierrors.IsNotFound(err) {
			log.Error(err, "Failed to get existing gateway AuthPolicy", "name", desired.GetName())
			return err
		}

		if existing != nil && !utils.IsManagedByOpenDataHub(existing) {
			log.V(1).Info("Skipping reconciliation - AuthPolicy is not managed by odh-model-controller")
			continue
		}

		if err := r.gatewayAuthPolicyProcessDelta(ctx, log, desired, existing); err != nil {
			log.Error(err, "Failed to process Gateway AuthPolicy delta", "name", desired.GetName())
			return err
		}
	}

	return nil
}

func (r *KserveAuthPolicyReconciler) reconcileHTTPRouteAuthpolicy(ctx context.Context, log logr.Logger, llmisvc *kservev1alpha1.LLMInferenceService) error {
	log.V(1).Info("Reconciling HTTPRoute AuthPolicy")

	desiredAuthPolicies, err := r.templateLoader.Load(ctx, constants.Anonymous, llmisvc)
	if err != nil {
		log.Error(err, "Failed to load HTTPRoute AuthPolicy templates")
		return err
	}

	for _, desired := range desiredAuthPolicies {
		existing, err := r.getExistingAuthPolicy(ctx, desired.GetName(), desired.GetNamespace())
		if err != nil && !apierrors.IsNotFound(err) {
			log.Error(err, "Failed to get existing HTTPRoute AuthPolicy", "name", desired.GetName())
			return err
		}

		if existing != nil && !utils.IsManagedByOpenDataHub(existing) {
			log.V(1).Info("Skipping reconciliation - AuthPolicy is not managed by odh-model-controller")
			continue
		}

		if err := r.httpRouteAuthPolicyProcessDelta(ctx, log, llmisvc, desired, existing); err != nil {
			log.Error(err, "Failed to process HTTPRoute AuthPolicy delta", "name", desired.GetName())
			return err
		}
	}

	return nil
}

func (r *KserveAuthPolicyReconciler) Delete(ctx context.Context, log logr.Logger, llmisvc *kservev1alpha1.LLMInferenceService) error {
	log.V(1).Info("Deleting AuthPolicies for LLMInferenceService")

	// 1. Delete HTTPRoute AuthPolicies (always delete)
	if err := r.deleteHTTPRouteAuthPolicies(ctx, log, llmisvc); err != nil {
		return err
	}

	// 2. Delete Gateway AuthPolicies (only if no other LLMInferenceService uses the same gateway)
	if err := r.deleteGatewayAuthPoliciesIfUnused(ctx, log, llmisvc); err != nil {
		return err
	}

	return nil
}

func (r *KserveAuthPolicyReconciler) deleteHTTPRouteAuthPolicies(ctx context.Context, log logr.Logger, llmisvc *kservev1alpha1.LLMInferenceService) error {
	log.V(1).Info("Deleting HTTPRoute AuthPolicy for LLMInferenceService")

	httpRouteAuthPolicies, err := r.templateLoader.Load(ctx, constants.Anonymous, llmisvc)
	if err != nil {
		log.Error(err, "Failed to load HTTPRoute AuthPolicy templates for deletion")
		return err
	}

	for _, authPolicy := range httpRouteAuthPolicies {
		namespacedName := types.NamespacedName{
			Name:      authPolicy.GetName(),
			Namespace: authPolicy.GetNamespace(),
		}

		log.V(1).Info("Attempting to delete HTTPRoute AuthPolicy", "name", namespacedName.Name)

		if err := r.store.Remove(ctx, namespacedName); err != nil {
			if !apierrors.IsNotFound(err) {
				log.Error(err, "Failed to delete HTTPRoute AuthPolicy", "name", namespacedName.Name)
				return err
			}
			log.V(1).Info("HTTPRoute AuthPolicy not found, already deleted", "name", namespacedName.Name)
		} else {
			log.Info("Successfully deleted HTTPRoute AuthPolicy", "name", namespacedName.Name)
		}
	}

	return nil
}

func (r *KserveAuthPolicyReconciler) deleteGatewayAuthPoliciesIfUnused(ctx context.Context, log logr.Logger, llmisvc *kservev1alpha1.LLMInferenceService) error {
	log.V(1).Info("Checking Gateway AuthPolicies for deletion")

	gatewayAuthPolicies, err := r.templateLoader.Load(ctx, constants.UserDefined, llmisvc)
	if err != nil {
		log.Error(err, "Failed to load Gateway AuthPolicy templates for deletion")
		return err
	}

	for _, authPolicy := range gatewayAuthPolicies {
		namespacedName := types.NamespacedName{
			Name:      authPolicy.GetName(),
			Namespace: authPolicy.GetNamespace(),
		}

		log.V(1).Info("Checking if Gateway AuthPolicy is used by other LLMInferenceServices", "name", namespacedName.Name)

		existingAuthPolicy, err := r.store.Get(ctx, namespacedName)
		if err != nil {
			if apierrors.IsNotFound(err) {
				log.V(1).Info("Gateway AuthPolicy not found, already deleted", "name", namespacedName.Name)
				continue
			}
			log.Error(err, "Failed to get Gateway AuthPolicy", "name", namespacedName.Name)
			return err
		}

		matchedServices, err := r.matcher.FindLLMServiceFromGatewayAuthPolicy(ctx, existingAuthPolicy)
		if err != nil {
			log.Error(err, "Failed to find LLMInferenceServices using Gateway AuthPolicy", "name", namespacedName.Name)
			return err
		}

		hasOtherUsers := false
		for _, svc := range matchedServices {
			if svc.Name != llmisvc.Name || svc.Namespace != llmisvc.Namespace {
				hasOtherUsers = true
				break
			}
		}

		if hasOtherUsers {
			log.Info("Skipping Gateway AuthPolicy deletion as it is used by other LLMInferenceServices",
				"authPolicy", namespacedName.Name)
			continue
		}

		log.V(1).Info("Attempting to delete Gateway AuthPolicy", "name", namespacedName.Name)

		if err := r.store.Remove(ctx, namespacedName); err != nil {
			if !apierrors.IsNotFound(err) {
				log.Error(err, "Failed to delete Gateway AuthPolicy", "name", namespacedName.Name)
				return err
			}
			log.V(1).Info("Gateway AuthPolicy not found, already deleted", "name", namespacedName.Name)
		} else {
			log.Info("Successfully deleted Gateway AuthPolicy", "name", namespacedName.Name)
		}
	}

	return nil
}

func (r *KserveAuthPolicyReconciler) Cleanup(ctx context.Context, log logr.Logger, isvcNs string) error {
	return nil
}

func (r *KserveAuthPolicyReconciler) getExistingAuthPolicy(ctx context.Context, name, namespace string) (*kuadrantv1.AuthPolicy, error) {
	return r.store.Get(ctx, types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	})
}

func (r *KserveAuthPolicyReconciler) getGatewayFromAuthPolicy(ctx context.Context, authPolicy *kuadrantv1.AuthPolicy) (*gatewayapiv1.Gateway, error) {
	if authPolicy.Spec.TargetRef.Kind != "Gateway" {
		return nil, fmt.Errorf("AuthPolicy %s does not target a Gateway", authPolicy.GetName())
	}

	gatewayName := string(authPolicy.Spec.TargetRef.Name)
	gatewayNamespace := authPolicy.GetNamespace()

	gateway := &gatewayapiv1.Gateway{}
	err := r.client.Get(ctx, types.NamespacedName{
		Name:      gatewayName,
		Namespace: gatewayNamespace,
	}, gateway)

	if err != nil {
		return nil, fmt.Errorf("failed to get gateway %s/%s: %w", gatewayNamespace, gatewayName, err)
	}

	return gateway, nil
}

func (r *KserveAuthPolicyReconciler) getHTTPRouteFromAuthPolicy(ctx context.Context, authPolicy *kuadrantv1.AuthPolicy) (*gatewayapiv1.HTTPRoute, error) {
	if authPolicy.Spec.TargetRef.Kind != "HTTPRoute" {
		return nil, fmt.Errorf("AuthPolicy %s does not target an HTTPRoute", authPolicy.GetName())
	}

	routeName := string(authPolicy.Spec.TargetRef.Name)
	routeNamespace := authPolicy.GetNamespace()
	httpRoute := &gatewayapiv1.HTTPRoute{}
	err := r.client.Get(ctx, types.NamespacedName{
		Name:      routeName,
		Namespace: routeNamespace,
	}, httpRoute)

	if err != nil {
		return nil, fmt.Errorf("failed to get HTTPRoute %s/%s: %w", routeNamespace, routeName, err)
	}

	return httpRoute, nil
}

func (r *KserveAuthPolicyReconciler) gatewayAuthPolicyProcessDelta(ctx context.Context, log logr.Logger, desired *kuadrantv1.AuthPolicy, existing *kuadrantv1.AuthPolicy) error {
	log.V(1).Info("Processing Gateway AuthPolicy delta", "name", desired.GetName())

	delta := r.deltaProcessor.ComputeDelta(comparators.GetAuthPolicyComparator(), desired, existing)

	if !delta.HasChanges() {
		log.V(1).Info("No delta found for Gateway AuthPolicy", "name", desired.GetName())
		return nil
	}

	if delta.IsAdded() {
		log.V(1).Info("Delta found", "action", "create", "authpolicy", desired.GetName())

		gateway, err := r.getGatewayFromAuthPolicy(ctx, desired)
		if err != nil {
			return fmt.Errorf("failed to get Gateway for AuthPolicy %s: %w", desired.GetName(), err)
		}
		if err := controllerutil.SetControllerReference(gateway, desired, r.scheme); err != nil {
			return fmt.Errorf("failed to set controller reference to Gateway for AuthPolicy %s: %w", desired.GetName(), err)
		}

		if err := r.store.Create(ctx, desired); err != nil {
			return fmt.Errorf("failed to create Gateway AuthPolicy %s: %w", desired.GetName(), err)
		}
	} else if delta.IsUpdated() {
		log.V(1).Info("Delta found", "action", "update", "authpolicy", existing.GetName())

		gateway, err := r.getGatewayFromAuthPolicy(ctx, desired)
		if err != nil {
			return fmt.Errorf("failed to get Gateway for AuthPolicy %s: %w", desired.GetName(), err)
		}
		if err := controllerutil.SetControllerReference(gateway, desired, r.scheme); err != nil {
			return fmt.Errorf("failed to set controller reference to Gateway for AuthPolicy %s: %w", desired.GetName(), err)
		}

		utils.MergeUserLabelsAndAnnotations(desired, existing)

		if err := r.store.Update(ctx, desired); err != nil {
			return fmt.Errorf("failed to update Gateway AuthPolicy %s: %w", existing.GetName(), err)
		}
	}

	return nil
}

func (r *KserveAuthPolicyReconciler) httpRouteAuthPolicyProcessDelta(ctx context.Context, log logr.Logger, llmisvc *kservev1alpha1.LLMInferenceService, desired *kuadrantv1.AuthPolicy, existing *kuadrantv1.AuthPolicy) error {
	log.V(1).Info("Processing HTTPRoute AuthPolicy delta", "name", desired.GetName())

	authType := r.detector.Detect(ctx, llmisvc.GetAnnotations())
	log.V(1).Info("AuthType", "authType", authType)

	if authType == constants.UserDefined {
		if existing != nil {
			log.V(1).Info("Deleting existing AuthPolicy for UserDefined access", "authpolicy", existing.GetName())
			if err := r.store.Remove(ctx, types.NamespacedName{
				Name:      existing.GetName(),
				Namespace: existing.GetNamespace(),
			}); err != nil {
				return fmt.Errorf("failed to delete AuthPolicy %s for UserDefined access: %w", existing.GetName(), err)
			}
		}
		return nil
	}

	_, err := r.getHTTPRouteFromAuthPolicy(ctx, desired)
	if err != nil {
		return fmt.Errorf("failed to get HTTPRoute for AuthPolicy %s: %w", desired.GetName(), err)
	}

	delta := r.deltaProcessor.ComputeDelta(comparators.GetAuthPolicyComparator(), desired, existing)

	if !delta.HasChanges() {
		log.V(1).Info("No delta found for HTTPRoute AuthPolicy", "name", desired.GetName())
		return nil
	}

	if delta.IsAdded() {
		log.V(1).Info("Creating AuthPolicy for Anonymous access", "authpolicy", desired.GetName())

		if err := controllerutil.SetControllerReference(llmisvc, desired, r.scheme); err != nil {
			return fmt.Errorf("failed to set controller reference for HTTPRoute AuthPolicy %s: %w", desired.GetName(), err)
		}

		if err := r.store.Create(ctx, desired); err != nil {
			return fmt.Errorf("failed to create HTTPRoute AuthPolicy %s: %w", desired.GetName(), err)
		}
	} else if delta.IsUpdated() {
		log.V(1).Info("Updating AuthPolicy for Anonymous access", "authpolicy", existing.GetName())

		if err := controllerutil.SetControllerReference(llmisvc, desired, r.scheme); err != nil {
			return fmt.Errorf("failed to set controller reference for HTTPRoute AuthPolicy %s: %w", desired.GetName(), err)
		}

		utils.MergeUserLabelsAndAnnotations(desired, existing)

		if err := r.store.Update(ctx, desired); err != nil {
			return fmt.Errorf("failed to update HTTPRoute AuthPolicy %s: %w", existing.GetName(), err)
		}
	}

	return nil
}
