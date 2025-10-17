/*
Copyright 2025.

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

package resources

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"text/template"

	"github.com/go-logr/logr"
	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kuadrantv1 "github.com/kuadrant/kuadrant-operator/api/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	"sigs.k8s.io/yaml"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	controllerutils "github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
)

type AuthPolicyDetector interface {
	Detect(ctx context.Context, annotations map[string]string) constants.AuthType
}

type AuthPolicyTemplateLoader interface {
	Load(ctx context.Context, authType constants.AuthType, llmisvc *kservev1alpha1.LLMInferenceService) ([]*kuadrantv1.AuthPolicy, error)
}

type AuthPolicyStore interface {
	Get(ctx context.Context, key types.NamespacedName) (*kuadrantv1.AuthPolicy, error)
	Remove(ctx context.Context, key types.NamespacedName) error
	Create(ctx context.Context, authPolicy *kuadrantv1.AuthPolicy) error
	Update(ctx context.Context, authPolicy *kuadrantv1.AuthPolicy) error
}

type AuthPolicyMatcher interface {
	FindLLMServiceFromHTTPRouteAuthPolicy(authPolicy *kuadrantv1.AuthPolicy) (types.NamespacedName, bool)
	FindLLMServiceFromGatewayAuthPolicy(ctx context.Context, authPolicy *kuadrantv1.AuthPolicy) ([]types.NamespacedName, error)
}

//go:embed template/authpolicy_llm_isvc_userdefined.yaml
var authPolicyTemplateUserDefined []byte

//go:embed template/authpolicy_anonymous.yaml
var authPolicyTemplateAnonymous []byte

type kserveAuthPolicyDetector struct {
	client client.Client
}

func NewKServeAuthPolicyDetector(client client.Client) AuthPolicyDetector {
	return &kserveAuthPolicyDetector{
		client: client,
	}
}

func (k *kserveAuthPolicyDetector) Detect(_ context.Context, annotations map[string]string) constants.AuthType {
	if value, exist := annotations[constants.EnableAuthODHAnnotation]; exist {
		if strings.EqualFold(strings.TrimSpace(value), "false") {
			return constants.Anonymous
		}
	}
	return constants.UserDefined
}

type kserveAuthPolicyTemplateLoader struct {
	client client.Client
}

func NewKServeAuthPolicyTemplateLoader(client client.Client) AuthPolicyTemplateLoader {
	return &kserveAuthPolicyTemplateLoader{
		client: client,
	}
}

func (k *kserveAuthPolicyTemplateLoader) Load(ctx context.Context, authType constants.AuthType, llmisvc *kservev1alpha1.LLMInferenceService) ([]*kuadrantv1.AuthPolicy, error) {
	switch authType {
	case constants.UserDefined:
		return k.loadUserDefinedTemplates(ctx, llmisvc)
	case constants.Anonymous:
		return k.loadAnonymousTemplate(llmisvc)
	default:
		return nil, fmt.Errorf("unsupported AuthPolicy type: %s", authType)
	}
}

func (k *kserveAuthPolicyTemplateLoader) loadUserDefinedTemplates(ctx context.Context, llmisvc *kservev1alpha1.LLMInferenceService) ([]*kuadrantv1.AuthPolicy, error) {
	logger := logr.FromContextOrDiscard(ctx).WithName("authpolicy")
	gateways := k.getGatewayInfo(ctx, logger, llmisvc)

	authPolicies := make([]*kuadrantv1.AuthPolicy, 0, len(gateways))

	for _, gateway := range gateways {
		authPolicy, err := k.renderUserDefinedTemplate(ctx, gateway.Namespace, gateway.Name)
		if err != nil {
			return nil, fmt.Errorf("failed to render AuthPolicy for gateway %s/%s: %w", gateway.Namespace, gateway.Name, err)
		}
		authPolicies = append(authPolicies, authPolicy)
	}

	return authPolicies, nil
}

func (k *kserveAuthPolicyTemplateLoader) renderUserDefinedTemplate(ctx context.Context, gatewayNamespace, gatewayName string) (*kuadrantv1.AuthPolicy, error) {
	tmpl, err := template.New("authpolicy").Parse(string(authPolicyTemplateUserDefined))
	if err != nil {
		return nil, fmt.Errorf("failed to parse AuthPolicy template: %w", err)
	}

	audiences := controllerutils.GetAuthAudience(ctx, k.client, constants.KubernetesAudience)
	audiencesJSON, err := json.Marshal(audiences)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal audiences %v to JSON: %w", audiences, err)
	}

	templateData := struct {
		Name             string
		GatewayName      string
		GatewayNamespace string
		Audiences        []string
		AudiencesJSON    string
	}{
		Name:             constants.GetGatewayAuthPolicyName(gatewayName),
		GatewayName:      gatewayName,
		GatewayNamespace: gatewayNamespace,
		Audiences:        audiences,
		AudiencesJSON:    string(audiencesJSON),
	}

	var builder strings.Builder
	if err := tmpl.Execute(&builder, templateData); err != nil {
		return nil, fmt.Errorf("failed to execute AuthPolicy template with data %+v: %w", templateData, err)
	}

	authPolicy := &kuadrantv1.AuthPolicy{}
	if err := yaml.Unmarshal([]byte(builder.String()), authPolicy); err != nil {
		return nil, fmt.Errorf("failed to unmarshal AuthPolicy YAML: %w", err)
	}

	return authPolicy, nil
}

func (k *kserveAuthPolicyTemplateLoader) loadAnonymousTemplate(llmisvc *kservev1alpha1.LLMInferenceService) ([]*kuadrantv1.AuthPolicy, error) {
	httpRoutes := k.getHTTPRouteInfo(llmisvc)

	authPolicies := make([]*kuadrantv1.AuthPolicy, 0, len(httpRoutes))

	for _, route := range httpRoutes {
		authPolicy, err := k.renderAnonymousTemplate(llmisvc, route.Name)
		if err != nil {
			return nil, fmt.Errorf("failed to render AuthPolicy for HTTPRoute %s: %w", route.Name, err)
		}
		authPolicies = append(authPolicies, authPolicy)
	}

	return authPolicies, nil
}

func (k *kserveAuthPolicyTemplateLoader) renderAnonymousTemplate(llmisvc *kservev1alpha1.LLMInferenceService, httpRouteName string) (*kuadrantv1.AuthPolicy, error) {
	tmpl, err := template.New("authpolicy").Parse(string(authPolicyTemplateAnonymous))
	if err != nil {
		return nil, fmt.Errorf("failed to parse AuthPolicy anonymous template: %w", err)
	}

	templateData := struct {
		Name          string
		Namespace     string
		LLMISvcName   string
		HTTPRouteName string
	}{
		Name:          constants.GetHTTPRouteAuthPolicyName(httpRouteName),
		Namespace:     llmisvc.Namespace,
		LLMISvcName:   llmisvc.Name,
		HTTPRouteName: httpRouteName,
	}

	var builder strings.Builder
	if err := tmpl.Execute(&builder, templateData); err != nil {
		return nil, fmt.Errorf("failed to execute AuthPolicy anonymous template with data %+v: %w", templateData, err)
	}

	authPolicy := &kuadrantv1.AuthPolicy{}
	if err := yaml.Unmarshal([]byte(builder.String()), authPolicy); err != nil {
		return nil, fmt.Errorf("failed to unmarshal AuthPolicy anonymous YAML: %w", err)
	}

	return authPolicy, nil
}

// getHTTPRouteInfo returns HTTPRoute list with fallback logic
func (k *kserveAuthPolicyTemplateLoader) getHTTPRouteInfo(llmisvc *kservev1alpha1.LLMInferenceService) []struct{ Name string } {
	var httpRoutes []struct{ Name string }

	if llmisvc.Spec.Router != nil && llmisvc.Spec.Router.Route != nil && llmisvc.Spec.Router.Route.HTTP != nil && llmisvc.Spec.Router.Route.HTTP.HasRefs() {
		for _, ref := range llmisvc.Spec.Router.Route.HTTP.Refs {
			httpRoutes = append(httpRoutes, struct{ Name string }{
				Name: ref.Name,
			})
		}
		return httpRoutes
	}

	// Fallback to default naming convention
	httpRoutes = append(httpRoutes, struct{ Name string }{
		Name: constants.GetHTTPRouteName(llmisvc.Name),
	})

	return httpRoutes
}

// getGatewayInfo returns gateway list with fallback logic, filtering out gateways with opendatahub.io/managed: false
func (k *kserveAuthPolicyTemplateLoader) getGatewayInfo(ctx context.Context, log logr.Logger, llmisvc *kservev1alpha1.LLMInferenceService) []struct{ Namespace, Name string } {
	var gateways []struct{ Namespace, Name string }

	if llmisvc.Spec.Router != nil && llmisvc.Spec.Router.Gateway != nil && llmisvc.Spec.Router.Gateway.HasRefs() {
		for _, ref := range llmisvc.Spec.Router.Gateway.Refs {
			// Check if the gateway exists and should be managed
			gateway := &gatewayapiv1.Gateway{}
			if err := controllerutils.GetResource(ctx, k.client, string(ref.Namespace), string(ref.Name), gateway); err == nil && !controllerutils.IsExplicitlyUnmanaged(gateway) {
				gateways = append(gateways, struct{ Namespace, Name string }{
					Namespace: string(ref.Namespace),
					Name:      string(ref.Name),
				})
			}
		}
		return gateways
	}

	var fallbackNamespace, fallbackName string
	if userDefinedGatewayNS, userDefinedGatewayName, err := controllerutils.GetGatewayInfoFromConfigMap(ctx, k.client); err == nil {
		fallbackNamespace = userDefinedGatewayNS
		fallbackName = userDefinedGatewayName
	} else {
		log.Info("Using default gateway values due to ConfigMap parsing failure",
			"error", err.Error(),
			"defaultNamespace", constants.DefaultGatewayNamespace,
			"defaultName", constants.DefaultGatewayName)
		fallbackNamespace = constants.DefaultGatewayNamespace
		fallbackName = constants.DefaultGatewayName
	}

	// Check if the fallback gateway exists and should be managed
	gateway := &gatewayapiv1.Gateway{}
	if err := controllerutils.GetResource(ctx, k.client, fallbackNamespace, fallbackName, gateway); err == nil && !controllerutils.IsExplicitlyUnmanaged(gateway) {
		gateways = append(gateways, struct{ Namespace, Name string }{
			Namespace: fallbackNamespace,
			Name:      fallbackName,
		})
	}

	return gateways
}

type clientAuthPolicyStore struct {
	client client.Client
}

func NewClientAuthPolicyStore(client client.Client) AuthPolicyStore {
	return &clientAuthPolicyStore{
		client: client,
	}
}

func (c *clientAuthPolicyStore) Get(ctx context.Context, key types.NamespacedName) (*kuadrantv1.AuthPolicy, error) {
	authPolicy := &kuadrantv1.AuthPolicy{}

	err := c.client.Get(ctx, key, authPolicy)
	if err != nil {
		return nil, fmt.Errorf("could not GET AuthPolicy %s: %w", key, err)
	}
	return authPolicy, nil
}

func (c *clientAuthPolicyStore) Remove(ctx context.Context, key types.NamespacedName) error {
	authPolicy := &kuadrantv1.AuthPolicy{}
	authPolicy.SetName(key.Name)
	authPolicy.SetNamespace(key.Namespace)

	if err := c.client.Delete(ctx, authPolicy); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("could not DELETE AuthPolicy %s: %w", key, err)
	}
	return nil
}

func (c *clientAuthPolicyStore) Create(ctx context.Context, authPolicy *kuadrantv1.AuthPolicy) error {
	if err := c.client.Create(ctx, authPolicy); err != nil {
		return fmt.Errorf("could not CREATE AuthPolicy %s/%s: %w", authPolicy.GetNamespace(), authPolicy.GetName(), err)
	}
	return nil
}

func (c *clientAuthPolicyStore) Update(ctx context.Context, authPolicy *kuadrantv1.AuthPolicy) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current := &kuadrantv1.AuthPolicy{}
		if err := controllerutils.GetResource(ctx, c.client, authPolicy.GetNamespace(), authPolicy.GetName(), current); err != nil {
			return err
		}

		authPolicy.SetResourceVersion(current.GetResourceVersion())
		return c.client.Update(ctx, authPolicy)
	})
}

type kserveAuthPolicyMatcher struct {
	client client.Client
}

func NewKServeAuthPolicyMatcher(client client.Client) AuthPolicyMatcher {
	return &kserveAuthPolicyMatcher{
		client: client,
	}
}

func (k *kserveAuthPolicyMatcher) FindLLMServiceFromHTTPRouteAuthPolicy(authPolicy *kuadrantv1.AuthPolicy) (types.NamespacedName, bool) {
	for _, ownerRef := range authPolicy.OwnerReferences {
		if ownerRef.Kind == "LLMInferenceService" {
			return types.NamespacedName{
				Name:      ownerRef.Name,
				Namespace: authPolicy.Namespace,
			}, true
		}
	}

	httpRouteName := string(authPolicy.Spec.TargetRef.Name)
	if strings.HasSuffix(httpRouteName, constants.HTTPRouteNameSuffix) {
		llmisvcName := strings.TrimSuffix(httpRouteName, constants.HTTPRouteNameSuffix)
		return types.NamespacedName{
			Name:      llmisvcName,
			Namespace: authPolicy.Namespace,
		}, true
	}

	return types.NamespacedName{}, false
}

func (k *kserveAuthPolicyMatcher) FindLLMServiceFromGatewayAuthPolicy(ctx context.Context, authPolicy *kuadrantv1.AuthPolicy) ([]types.NamespacedName, error) {
	gatewayNamespace, gatewayName, err := controllerutils.GetGatewayInfoFromConfigMap(ctx, k.client)
	if err != nil {
		// Fallback to default gateway values when ConfigMap is not available
		gatewayNamespace = constants.DefaultGatewayNamespace
		gatewayName = constants.DefaultGatewayName
	}

	var matchedServices []types.NamespacedName
	listNamespace := metav1.NamespaceAll
	continueToken := ""
	for {
		llmSvcList := &kservev1alpha1.LLMInferenceServiceList{}
		if err := k.client.List(ctx, llmSvcList, &client.ListOptions{Namespace: listNamespace, Continue: continueToken}); err != nil {
			return nil, err
		}

		for _, llmSvc := range llmSvcList.Items {
			if k.isGatewayMatchedWithInfo(&llmSvc, authPolicy, gatewayNamespace, gatewayName) {
				matchedServices = append(matchedServices, types.NamespacedName{
					Name:      llmSvc.Name,
					Namespace: llmSvc.Namespace,
				})
			}
		}

		if llmSvcList.Continue == "" {
			break
		}
		continueToken = llmSvcList.Continue
	}

	return matchedServices, nil
}

func (k *kserveAuthPolicyMatcher) isGatewayMatchedWithInfo(llmSvc *kservev1alpha1.LLMInferenceService, authPolicy *kuadrantv1.AuthPolicy, gatewayNamespace, gatewayName string) bool {
	if llmSvc.Spec.Router == nil || llmSvc.Spec.Router.Gateway == nil || !llmSvc.Spec.Router.Gateway.HasRefs() {
		return authPolicy.Namespace == gatewayNamespace && string(authPolicy.Spec.TargetRef.Name) == gatewayName
	}

	for _, ref := range llmSvc.Spec.Router.Gateway.Refs {
		if string(ref.Name) == string(authPolicy.Spec.TargetRef.Name) && string(ref.Namespace) == authPolicy.Namespace {
			return true
		}
	}
	return false
}
