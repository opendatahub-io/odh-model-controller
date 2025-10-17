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
	"fmt"
	"strings"
	"text/template"

	"github.com/go-logr/logr"
	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	controllerutils "github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
	istioclientv1alpha3 "istio.io/client-go/pkg/apis/networking/v1alpha3"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	"sigs.k8s.io/yaml"
)

type EnvoyFilterDetector interface {
	Detect(ctx context.Context, annotations map[string]string) bool
}

type EnvoyFilterTemplateLoader interface {
	Load(ctx context.Context, llmisvc *kservev1alpha1.LLMInferenceService) ([]*istioclientv1alpha3.EnvoyFilter, error)
}

type EnvoyFilterStore interface {
	Get(ctx context.Context, key types.NamespacedName) (*istioclientv1alpha3.EnvoyFilter, error)
	Remove(ctx context.Context, key types.NamespacedName) error
	Create(ctx context.Context, envoyFilter *istioclientv1alpha3.EnvoyFilter) error
	Update(ctx context.Context, envoyFilter *istioclientv1alpha3.EnvoyFilter) error
}

type EnvoyFilterMatcher interface {
	FindLLMServiceFromEnvoyFilter(ctx context.Context, envoyFilter *istioclientv1alpha3.EnvoyFilter) ([]types.NamespacedName, error)
}

//go:embed template/envoyfilter_ssl.yaml
var envoyFilterTemplateSSL []byte

type kserveEnvoyFilterDetector struct {
	client client.Client
}

func NewKServeEnvoyFilterDetector(client client.Client) EnvoyFilterDetector {
	return &kserveEnvoyFilterDetector{
		client: client,
	}
}

func (k *kserveEnvoyFilterDetector) Detect(_ context.Context, annotations map[string]string) bool {
	if value, exist := annotations[constants.EnableAuthODHAnnotation]; exist {
		if strings.EqualFold(strings.TrimSpace(value), "false") {
			return false
		}
	}
	return true
}

type kserveEnvoyFilterTemplateLoader struct {
	client client.Client
}

func NewKServeEnvoyFilterTemplateLoader(client client.Client) EnvoyFilterTemplateLoader {
	return &kserveEnvoyFilterTemplateLoader{
		client: client,
	}
}

func (k *kserveEnvoyFilterTemplateLoader) Load(ctx context.Context, llmisvc *kservev1alpha1.LLMInferenceService) ([]*istioclientv1alpha3.EnvoyFilter, error) {
	logger := logr.FromContextOrDiscard(ctx).WithName("envoyfilter")
	gateways := k.getGatewayInfo(ctx, logger, llmisvc)

	envoyFilters := make([]*istioclientv1alpha3.EnvoyFilter, 0, len(gateways))

	for _, gateway := range gateways {
		envoyFilter, err := k.renderSSLTemplate(gateway.Namespace, gateway.Name)
		if err != nil {
			return nil, fmt.Errorf("failed to render EnvoyFilter for gateway %s/%s: %w", gateway.Namespace, gateway.Name, err)
		}
		envoyFilters = append(envoyFilters, envoyFilter)
	}

	return envoyFilters, nil
}

func (k *kserveEnvoyFilterTemplateLoader) renderSSLTemplate(gatewayNamespace, gatewayName string) (*istioclientv1alpha3.EnvoyFilter, error) {
	tmpl, err := template.New("envoyfilter").Parse(string(envoyFilterTemplateSSL))
	if err != nil {
		return nil, fmt.Errorf("failed to parse EnvoyFilter template: %w", err)
	}

	templateData := struct {
		Name             string
		GatewayName      string
		GatewayNamespace string
	}{
		Name:             constants.GetGatewayEnvoyFilterName(gatewayName),
		GatewayName:      gatewayName,
		GatewayNamespace: gatewayNamespace,
	}

	var builder strings.Builder
	if err := tmpl.Execute(&builder, templateData); err != nil {
		return nil, fmt.Errorf("failed to execute EnvoyFilter template with data %+v: %w", templateData, err)
	}

	envoyFilter := &istioclientv1alpha3.EnvoyFilter{}
	if err := yaml.Unmarshal([]byte(builder.String()), envoyFilter); err != nil {
		return nil, fmt.Errorf("failed to unmarshal EnvoyFilter YAML: %w", err)
	}

	return envoyFilter, nil
}

// getGatewayInfo returns gateway list with fallback logic, filtering out gateways with opendatahub.io/managed: false
func (k *kserveEnvoyFilterTemplateLoader) getGatewayInfo(ctx context.Context, log logr.Logger, llmisvc *kservev1alpha1.LLMInferenceService) []struct{ Namespace, Name string } {
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

type clientEnvoyFilterStore struct {
	client client.Client
}

func NewClientEnvoyFilterStore(client client.Client) EnvoyFilterStore {
	return &clientEnvoyFilterStore{
		client: client,
	}
}

func (c *clientEnvoyFilterStore) Get(ctx context.Context, key types.NamespacedName) (*istioclientv1alpha3.EnvoyFilter, error) {
	envoyFilter := &istioclientv1alpha3.EnvoyFilter{}

	err := c.client.Get(ctx, key, envoyFilter)
	if err != nil {
		return nil, fmt.Errorf("could not GET EnvoyFilter %s: %w", key, err)
	}
	return envoyFilter, nil
}

func (c *clientEnvoyFilterStore) Remove(ctx context.Context, key types.NamespacedName) error {
	envoyFilter := &istioclientv1alpha3.EnvoyFilter{}
	envoyFilter.SetName(key.Name)
	envoyFilter.SetNamespace(key.Namespace)

	if err := c.client.Delete(ctx, envoyFilter); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("could not DELETE EnvoyFilter %s: %w", key, err)
	}
	return nil
}

func (c *clientEnvoyFilterStore) Create(ctx context.Context, envoyFilter *istioclientv1alpha3.EnvoyFilter) error {
	if err := c.client.Create(ctx, envoyFilter); err != nil {
		return fmt.Errorf("could not CREATE EnvoyFilter %s/%s: %w", envoyFilter.GetNamespace(), envoyFilter.GetName(), err)
	}
	return nil
}

func (c *clientEnvoyFilterStore) Update(ctx context.Context, envoyFilter *istioclientv1alpha3.EnvoyFilter) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current := &istioclientv1alpha3.EnvoyFilter{}
		if err := controllerutils.GetResource(ctx, c.client, envoyFilter.GetNamespace(), envoyFilter.GetName(), current); err != nil {
			return err
		}

		envoyFilter.SetResourceVersion(current.GetResourceVersion())
		return c.client.Update(ctx, envoyFilter)
	})
}

type kserveEnvoyFilterMatcher struct {
	client client.Client
}

func NewKServeEnvoyFilterMatcher(client client.Client) EnvoyFilterMatcher {
	return &kserveEnvoyFilterMatcher{
		client: client,
	}
}

func (k *kserveEnvoyFilterMatcher) FindLLMServiceFromEnvoyFilter(ctx context.Context, envoyFilter *istioclientv1alpha3.EnvoyFilter) ([]types.NamespacedName, error) {
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
			if k.isGatewayMatchedWithInfo(&llmSvc, envoyFilter, gatewayNamespace, gatewayName) {
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

func (k *kserveEnvoyFilterMatcher) isGatewayMatchedWithInfo(llmSvc *kservev1alpha1.LLMInferenceService, envoyFilter *istioclientv1alpha3.EnvoyFilter, gatewayNamespace, gatewayName string) bool {
	if len(envoyFilter.Spec.TargetRefs) == 0 {
		return false
	}

	targetRef := envoyFilter.Spec.TargetRefs[0]

	if llmSvc.Spec.Router == nil || llmSvc.Spec.Router.Gateway == nil || !llmSvc.Spec.Router.Gateway.HasRefs() {
		return envoyFilter.Namespace == gatewayNamespace && targetRef.Name == gatewayName
	}

	for _, ref := range llmSvc.Spec.Router.Gateway.Refs {
		if string(ref.Name) == targetRef.Name && string(ref.Namespace) == envoyFilter.Namespace {
			return true
		}
	}
	return false
}
