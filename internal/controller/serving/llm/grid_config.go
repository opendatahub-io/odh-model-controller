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
	"errors"
	"fmt"
	"os"
	"strings"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/validation"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	sigsjson "sigs.k8s.io/json"
)

const (
	GridServingAnnotation    = "grid.opendatahub.io/serving"
	GridGatewayNamespacesEnv = "GRID_GATEWAY_NAMESPACES"
	GridAllowedLabel         = "grid.opendatahub.io/allowed"
	gridFinalizer            = "grid.praxis.fast/inference-provider"
	gridSourceUID            = "grid.praxis.fast/source-uid"
	gridSourceName           = "grid.praxis.fast/source-name"
	gridSourceNamespace      = "grid.praxis.fast/source-namespace"
)

// GridConfig is administrator-controlled through Gateway metadata.
type GridConfig struct {
	NetworkName    string            `json:"networkName"`
	Namespace      string            `json:"namespace"`
	ServiceAccount string            `json:"serviceAccount"`
	TokenSecret    string            `json:"tokenSecret"`
	SiteLabels     map[string]string `json:"-"`
	// ClientCertificate, when set, replaces the token: Grid presents its site certificate on the named listener.
	ClientCertificate *GridClientCertificate `json:"clientCertificate,omitempty"`
	// PublishMetrics opts into publishing the EPP metrics endpoint.
	PublishMetrics bool `json:"publishMetrics"`
	// MetricsAllowPlaintext grants Grid's auth.allowPlaintext on an http EPP endpoint; Grid defaults it to false.
	MetricsAllowPlaintext bool `json:"metricsAllowPlaintext"`
}

// GridClientCertificate names the Gateway listener that requires the Grid client certificate and the site allowed on it.
type GridClientCertificate struct {
	Listener string `json:"listener"`
	SiteName string `json:"siteName"`
}

// GridConfigFromGateway returns nil when the Gateway does not publish to Grid.
func GridConfigFromGateway(gateway *gatewayv1.Gateway) (*GridConfig, error) {
	if gateway.Annotations[GridServingAnnotation] == "" {
		return nil, nil
	}
	allowedNamespaces, configured := os.LookupEnv(GridGatewayNamespacesEnv)
	if !configured {
		allowedNamespaces = "openshift-ingress,redhat-ods-applications,opendatahub"
	}
	allowed := false
	for namespace := range strings.SplitSeq(allowedNamespaces, ",") {
		if namespace = strings.TrimSpace(namespace); namespace != "" && namespace == gateway.Namespace {
			allowed = true
			break
		}
	}
	if !allowed {
		return nil, fmt.Errorf("Gateway namespace %q is not allowed by %s", gateway.Namespace, GridGatewayNamespacesEnv)
	}
	var raw struct {
		NetworkName           string                 `json:"networkName"`
		Namespace             string                 `json:"namespace"`
		ServiceAccount        string                 `json:"serviceAccount"`
		TokenSecret           string                 `json:"tokenSecret"`
		SiteLabels            string                 `json:"siteLabels"`
		PublishMetrics        bool                   `json:"publishMetrics"`
		MetricsAllowPlaintext bool                   `json:"metricsAllowPlaintext"`
		ClientCertificate     *GridClientCertificate `json:"clientCertificate"`
	}
	strictErrors, err := sigsjson.UnmarshalStrict([]byte(gateway.Annotations[GridServingAnnotation]), &raw)
	if err != nil {
		return nil, fmt.Errorf("invalid %s: %w", GridServingAnnotation, err)
	}
	if err := errors.Join(strictErrors...); err != nil {
		return nil, fmt.Errorf("invalid %s: %w", GridServingAnnotation, err)
	}
	cfg := &GridConfig{NetworkName: raw.NetworkName, Namespace: raw.Namespace, ServiceAccount: raw.ServiceAccount, TokenSecret: raw.TokenSecret, PublishMetrics: raw.PublishMetrics, MetricsAllowPlaintext: raw.MetricsAllowPlaintext, ClientCertificate: raw.ClientCertificate}
	fields := []struct{ name, value string }{{"networkName", cfg.NetworkName}, {"namespace", cfg.Namespace}, {"serviceAccount", cfg.ServiceAccount}}
	// The certificate replaces the token, so the Secret is optional beside it.
	if cfg.ClientCertificate == nil || cfg.TokenSecret != "" {
		fields = append(fields, struct{ name, value string }{"tokenSecret", cfg.TokenSecret})
	}
	for _, field := range fields {
		if errs := validation.IsDNS1123Subdomain(field.value); len(errs) > 0 {
			return nil, fmt.Errorf("invalid %s %s: %v", GridServingAnnotation, field.name, errs)
		}
	}
	if cert := cfg.ClientCertificate; cert != nil {
		// The site name lands in an authorization pattern, so only a DNS label is accepted.
		if errs := validation.IsDNS1123Label(cert.SiteName); len(errs) > 0 {
			return nil, fmt.Errorf("invalid %s clientCertificate.siteName: %v", GridServingAnnotation, errs)
		}
		if _, err := gridClientCertificatePort(gateway, cert.Listener); err != nil {
			return nil, fmt.Errorf("invalid %s clientCertificate.listener: %w", GridServingAnnotation, err)
		}
	}
	if errs := validation.IsDNS1123Label(cfg.Namespace); len(errs) > 0 {
		return nil, fmt.Errorf("invalid %s namespace: %v", GridServingAnnotation, errs)
	}
	cfg.SiteLabels, err = labels.ConvertSelectorToLabelsMap(raw.SiteLabels)
	if err != nil {
		return nil, fmt.Errorf("invalid %s siteLabels: %w", GridServingAnnotation, err)
	}
	return cfg, nil
}

// gridClientCertificatePort returns the port of the named HTTPS listener.
func gridClientCertificatePort(gateway *gatewayv1.Gateway, name string) (gatewayv1.PortNumber, error) {
	for _, listener := range gateway.Spec.Listeners {
		if string(listener.Name) != name {
			continue
		}
		if listener.Protocol != gatewayv1.HTTPSProtocolType {
			return 0, fmt.Errorf("listener %q is %s, not HTTPS", name, listener.Protocol)
		}
		return listener.Port, nil
	}
	return 0, fmt.Errorf("listener %q is not on the Gateway", name)
}
