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

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/yaml"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	controllerutils "github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
)

type AuthPolicyDetector interface {
	Detect(ctx context.Context, annotations map[string]string) constants.AuthType
}

type AuthPolicyTemplateLoader interface {
	Load(ctx context.Context, authType constants.AuthType, llmisvc *kservev1alpha1.LLMInferenceService) (*unstructured.Unstructured, error)
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
		if strings.ToLower(value) == "false" {
			return constants.Anonymous
		}
	}
	return constants.UserDefined
}

type kserveAuthPolicyTemplateLoader struct {
	client client.Client
	scheme *runtime.Scheme
}

func NewKServeAuthPolicyTemplateLoader(client client.Client, scheme *runtime.Scheme) AuthPolicyTemplateLoader {
	return &kserveAuthPolicyTemplateLoader{
		client: client,
		scheme: scheme,
	}
}

func (k *kserveAuthPolicyTemplateLoader) Load(ctx context.Context, authType constants.AuthType, llmisvc *kservev1alpha1.LLMInferenceService) (*unstructured.Unstructured, error) {
	switch authType {
	case constants.UserDefined:
		return k.loadUserDefinedTemplate(llmisvc)
	case constants.Anonymous:
		return k.loadAnonymousTemplate(llmisvc)
	default:
		return nil, fmt.Errorf("unsupported AuthPolicy type: %s", authType)
	}
}

func (k *kserveAuthPolicyTemplateLoader) loadUserDefinedTemplate(llmisvc *kservev1alpha1.LLMInferenceService) (*unstructured.Unstructured, error) {
	tmpl, err := template.New("authpolicy").Parse(string(authPolicyTemplateUserDefined))
	if err != nil {
		return nil, fmt.Errorf("failed to parse AuthPolicy template: %w", err)
	}

	audiences := controllerutils.GetAuthAudience(constants.KubernetesAudience)
	audiencesJSON, err := json.Marshal(audiences)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal audiences to JSON: %w", err)
	}

	templateData := struct {
		Name                    string
		Namespace               string
		LLMInferenceServiceName string
		HTTPRouteName           string
		Audiences               []string
		AudiencesJSON           string
	}{
		Name:                    constants.GetAuthPolicyName(llmisvc.Name),
		Namespace:               llmisvc.Namespace,
		LLMInferenceServiceName: llmisvc.Name,
		HTTPRouteName:           constants.GetHTTPRouteName(llmisvc.Name),
		Audiences:               audiences,
		AudiencesJSON:           string(audiencesJSON),
	}

	var builder strings.Builder
	if err := tmpl.Execute(&builder, templateData); err != nil {
		return nil, fmt.Errorf("failed to execute AuthPolicy template: %w", err)
	}

	var authPolicyObj map[string]interface{}
	if err := yaml.Unmarshal([]byte(builder.String()), &authPolicyObj); err != nil {
		return nil, fmt.Errorf("failed to unmarshal AuthPolicy YAML: %w", err)
	}

	authPolicy := &unstructured.Unstructured{Object: authPolicyObj}
	if err := controllerutil.SetControllerReference(llmisvc, authPolicy, k.scheme); err != nil {
		return nil, fmt.Errorf("failed to set controller reference: %w", err)
	}

	return authPolicy, nil
}

func (k *kserveAuthPolicyTemplateLoader) loadAnonymousTemplate(llmisvc *kservev1alpha1.LLMInferenceService) (*unstructured.Unstructured, error) {
	tmpl, err := template.New("authpolicy").Parse(string(authPolicyTemplateAnonymous))
	if err != nil {
		return nil, fmt.Errorf("failed to parse AuthPolicy anonymous template: %w", err)
	}

	templateData := struct {
		Name          string
		Namespace     string
		HTTPRouteName string
	}{
		Name:          constants.GetAuthPolicyName(llmisvc.Name),
		Namespace:     llmisvc.Namespace,
		HTTPRouteName: constants.GetHTTPRouteName(llmisvc.Name),
	}

	var builder strings.Builder
	if err := tmpl.Execute(&builder, templateData); err != nil {
		return nil, fmt.Errorf("failed to execute AuthPolicy anonymous template: %w", err)
	}

	var authPolicyObj map[string]interface{}
	if err := yaml.Unmarshal([]byte(builder.String()), &authPolicyObj); err != nil {
		return nil, fmt.Errorf("failed to unmarshal AuthPolicy anonymous YAML: %w", err)
	}

	authPolicy := &unstructured.Unstructured{Object: authPolicyObj}
	if err := controllerutil.SetControllerReference(llmisvc, authPolicy, k.scheme); err != nil {
		return nil, fmt.Errorf("failed to set controller reference: %w", err)
	}

	return authPolicy, nil
}
