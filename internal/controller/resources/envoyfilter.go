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

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	controllerutils "github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
	istioclientv1alpha3 "istio.io/client-go/pkg/apis/networking/v1alpha3"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

// We can remove the EnvoyFilter creation once https://github.com/Kuadrant/kuadrant-operator/issues/1531 is resolved.

// EnvoyFilterTemplateLoader renders EnvoyFilter templates.
type EnvoyFilterTemplateLoader interface {
	Load(ctx context.Context, gatewayNamespace, gatewayName string) (*istioclientv1alpha3.EnvoyFilter, error)
}

type EnvoyFilterStore interface {
	Get(ctx context.Context, key types.NamespacedName) (*istioclientv1alpha3.EnvoyFilter, error)
	Remove(ctx context.Context, key types.NamespacedName) error
	Create(ctx context.Context, envoyFilter *istioclientv1alpha3.EnvoyFilter) error
	Update(ctx context.Context, envoyFilter *istioclientv1alpha3.EnvoyFilter) error
}

//go:embed template/envoyfilter_ssl.yaml
var envoyFilterTemplateSSL []byte

type kserveEnvoyFilterTemplateLoader struct {
	client client.Client
}

func NewKServeEnvoyFilterTemplateLoader(client client.Client) EnvoyFilterTemplateLoader {
	return &kserveEnvoyFilterTemplateLoader{
		client: client,
	}
}

// Load renders the EnvoyFilter SSL template for the given gateway.
func (k *kserveEnvoyFilterTemplateLoader) Load(ctx context.Context, gatewayNamespace, gatewayName string) (*istioclientv1alpha3.EnvoyFilter, error) {
	kuadrantNamespace := controllerutils.GetKuadrantNamespace(ctx, k.client)
	return k.renderSSLTemplate(gatewayNamespace, gatewayName, kuadrantNamespace)
}

type envoyFilterTemplateData struct {
	Name              string
	Namespace         string
	TargetKind        string
	TargetName        string
	KuadrantNamespace string
}

// renderSSLTemplate renders the EnvoyFilter template. Pure function.
func (k *kserveEnvoyFilterTemplateLoader) renderSSLTemplate(gatewayNamespace, gatewayName, kuadrantNamespace string) (*istioclientv1alpha3.EnvoyFilter, error) {
	tmpl, err := template.New("envoyfilter").Parse(string(envoyFilterTemplateSSL))
	if err != nil {
		return nil, fmt.Errorf("failed to parse EnvoyFilter template: %w", err)
	}

	data := envoyFilterTemplateData{
		Name:              constants.GetGatewayEnvoyFilterName(gatewayName),
		Namespace:         gatewayNamespace,
		TargetKind:        "Gateway",
		TargetName:        gatewayName,
		KuadrantNamespace: kuadrantNamespace,
	}

	var builder strings.Builder
	if err := tmpl.Execute(&builder, data); err != nil {
		return nil, fmt.Errorf("failed to execute EnvoyFilter template with data %+v: %w", data, err)
	}

	envoyFilter := &istioclientv1alpha3.EnvoyFilter{}
	if err := yaml.Unmarshal([]byte(builder.String()), envoyFilter); err != nil {
		return nil, fmt.Errorf("failed to unmarshal EnvoyFilter YAML: %w", err)
	}

	return envoyFilter, nil
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
