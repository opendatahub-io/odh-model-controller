/*
Copyright 2026.

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

package llm

import (
	"fmt"
	"os"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/yaml"
)

const (
	GridEnabledAnnotation = "grid.praxis.fast/enabled"
	gridFinalizer         = "grid.praxis.fast/inference-provider"
	gridSourceUID         = "grid.praxis.fast/source-uid"
	gridSourceName        = "grid.praxis.fast/source-name"
	gridSourceNamespace   = "grid.praxis.fast/source-namespace"
)

// GridConfig is administrator-controlled. Services cannot choose credentials or
// a GridNetwork by annotation.
type GridConfig struct {
	NamespaceSelector labels.Selector
	NetworkName       string
	Namespace         string
	ServiceAccount    string
	TokenSecret       string
	SiteLabels        map[string]string
}

// GridConfigFromEnv reads configuration without defaulting deployment names.
// An explicit selector enables publishing; '{}' allows every namespace.
func GridConfigFromEnv() (GridConfig, error) {
	cfg := GridConfig{NamespaceSelector: labels.Nothing()}
	selector := os.Getenv("GRID_NAMESPACE_SELECTOR")
	if selector == "" {
		return cfg, nil
	}
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return cfg, fmt.Errorf("invalid GRID_NAMESPACE_SELECTOR: provide a LabelSelector object; '{}' allows all namespaces")
	}
	var labelSelector *metav1.LabelSelector
	if err := yaml.UnmarshalStrict([]byte(selector), &labelSelector); err != nil {
		return cfg, fmt.Errorf("invalid GRID_NAMESPACE_SELECTOR: %w", err)
	}
	if labelSelector == nil {
		return cfg, fmt.Errorf("invalid GRID_NAMESPACE_SELECTOR: expected a LabelSelector object, got null")
	}
	var err error
	cfg.NamespaceSelector, err = metav1.LabelSelectorAsSelector(labelSelector)
	if err != nil {
		return cfg, fmt.Errorf("invalid GRID_NAMESPACE_SELECTOR: %w", err)
	}
	cfg.NetworkName = os.Getenv("GRID_NETWORK_NAME")
	cfg.Namespace = os.Getenv("GRID_NAMESPACE")
	cfg.ServiceAccount = os.Getenv("GRID_SERVICE_ACCOUNT")
	cfg.TokenSecret = os.Getenv("GRID_TOKEN_SECRET")
	for _, field := range []struct{ name, value string }{
		{"GRID_NETWORK_NAME", cfg.NetworkName}, {"GRID_NAMESPACE", cfg.Namespace},
		{"GRID_SERVICE_ACCOUNT", cfg.ServiceAccount}, {"GRID_TOKEN_SECRET", cfg.TokenSecret},
	} {
		if errs := validation.IsDNS1123Subdomain(field.value); len(errs) > 0 {
			return cfg, fmt.Errorf("invalid %s: %v", field.name, errs)
		}
	}
	if errs := validation.IsDNS1123Label(cfg.Namespace); len(errs) > 0 {
		return cfg, fmt.Errorf("invalid GRID_NAMESPACE: %v", errs)
	}
	cfg.SiteLabels, err = labels.ConvertSelectorToLabelsMap(os.Getenv("GRID_SITE_LABELS"))
	if err != nil {
		return cfg, fmt.Errorf("invalid GRID_SITE_LABELS: %w", err)
	}
	return cfg, nil
}
