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

	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/validation"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	sigsjson "sigs.k8s.io/json"
)

const (
	GridServingAnnotation         = "grid.opendatahub.io/serving"
	GridGatewayNamespacesEnv      = "GRID_GATEWAY_NAMESPACES"
	GridOperatorNamespaceEnv      = "GRID_OPERATOR_NAMESPACE"
	GridOperatorServiceAccountEnv = "GRID_OPERATOR_SERVICE_ACCOUNT"
	GridAllowedLabel              = "grid.opendatahub.io/allowed"
	gridFinalizer                 = "grid.praxis.fast/inference-provider"
	gridSourceUID                 = "grid.praxis.fast/source-uid"
	gridSourceName                = "grid.praxis.fast/source-name"
	gridSourceNamespace           = "grid.praxis.fast/source-namespace"
)

// gridOperatorIdentity is installation configuration, independent of the
// model-client identity supplied by a trusted Gateway administrator.
func gridOperatorIdentity() (rbacv1.Subject, error) {
	namespace, configured := os.LookupEnv(GridOperatorNamespaceEnv)
	if !configured {
		namespace = os.Getenv("POD_NAMESPACE")
	}
	name, configured := os.LookupEnv(GridOperatorServiceAccountEnv)
	if !configured {
		name = "grid-operator"
	}
	if errs := validation.IsDNS1123Label(namespace); len(errs) > 0 {
		return rbacv1.Subject{}, fmt.Errorf("invalid %s (defaults to POD_NAMESPACE): %v", GridOperatorNamespaceEnv, errs)
	}
	if errs := validation.IsDNS1123Subdomain(name); len(errs) > 0 {
		return rbacv1.Subject{}, fmt.Errorf("invalid %s: %v", GridOperatorServiceAccountEnv, errs)
	}
	return rbacv1.Subject{Kind: "ServiceAccount", Name: name, Namespace: namespace}, nil
}

// GridConfig is administrator-controlled through Gateway metadata.
type GridConfig struct {
	NetworkName    string            `json:"networkName"`
	Namespace      string            `json:"namespace"`
	ServiceAccount string            `json:"serviceAccount"`
	TokenSecret    string            `json:"tokenSecret"`
	SiteLabels     map[string]string `json:"-"`
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
		NetworkName    string `json:"networkName"`
		Namespace      string `json:"namespace"`
		ServiceAccount string `json:"serviceAccount"`
		TokenSecret    string `json:"tokenSecret"`
		SiteLabels     string `json:"siteLabels"`
	}
	strictErrors, err := sigsjson.UnmarshalStrict([]byte(gateway.Annotations[GridServingAnnotation]), &raw)
	if err != nil {
		return nil, fmt.Errorf("invalid %s: %w", GridServingAnnotation, err)
	}
	if err := errors.Join(strictErrors...); err != nil {
		return nil, fmt.Errorf("invalid %s: %w", GridServingAnnotation, err)
	}
	cfg := &GridConfig{NetworkName: raw.NetworkName, Namespace: raw.Namespace, ServiceAccount: raw.ServiceAccount, TokenSecret: raw.TokenSecret}
	for _, field := range []struct{ name, value string }{{"networkName", cfg.NetworkName}, {"namespace", cfg.Namespace}, {"serviceAccount", cfg.ServiceAccount}, {"tokenSecret", cfg.TokenSecret}} {
		if errs := validation.IsDNS1123Subdomain(field.value); len(errs) > 0 {
			return nil, fmt.Errorf("invalid %s %s: %v", GridServingAnnotation, field.name, errs)
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
