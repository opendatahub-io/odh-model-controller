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

	kuadrantv1 "github.com/kuadrant/kuadrant-operator/api/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"knative.dev/pkg/kmeta"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	controllerutils "github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
)

//go:embed template/authpolicy_llm_isvc_userdefined.yaml
var authPolicyTemplateUserDefined []byte

//go:embed template/authpolicy_anonymous.yaml
var authPolicyTemplateAnonymous []byte

type AuthPolicyDetector interface {
	Detect(ctx context.Context, annotations map[string]string) constants.AuthType
}

type AuthPolicyTarget struct {
	Kind      string // Currently only "Gateway" or "HTTPRoute" is supported
	Name      string
	Namespace string
	AuthType  constants.AuthType
}

type AuthPolicyTemplateLoader interface {
	Load(ctx context.Context, target AuthPolicyTarget, opts ...ObjectOption) (*kuadrantv1.AuthPolicy, error)
}

type AuthPolicyStore interface {
	Get(ctx context.Context, key types.NamespacedName) (*kuadrantv1.AuthPolicy, error)
	Remove(ctx context.Context, key types.NamespacedName) error
	Create(ctx context.Context, authPolicy *kuadrantv1.AuthPolicy) error
	Update(ctx context.Context, authPolicy *kuadrantv1.AuthPolicy) error
}

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

func withManagedLabels() ObjectOption {
	return WithLabels(map[string]string{
		"app.kubernetes.io/component":  "llminferenceservice-policies",
		"app.kubernetes.io/part-of":    "llminferenceservice",
		"app.kubernetes.io/managed-by": "odh-model-controller",
	})
}

func (k *kserveAuthPolicyTemplateLoader) Load(_ context.Context, target AuthPolicyTarget, opts ...ObjectOption) (*kuadrantv1.AuthPolicy, error) {
	var tmpl []byte

	switch target.AuthType {
	case constants.Anonymous:
		tmpl = authPolicyTemplateAnonymous
	case constants.UserDefined:
		tmpl = authPolicyTemplateUserDefined
	default:
		return nil, fmt.Errorf("unsupported auth type %s", target.AuthType)
	}

	authPolicy, err := k.renderTemplate(tmpl, authPolicyTemplateData{
		Name:       kmeta.ChildName(target.Name, constants.AuthPolicyNameSuffix),
		Namespace:  target.Namespace,
		TargetKind: target.Kind,
		TargetName: target.Name,
	})
	if err != nil {
		return nil, err
	}

	// Apply managed labels first, then caller-provided opts can extend/override
	withManagedLabels()(authPolicy)
	for _, opt := range opts {
		opt(authPolicy)
	}

	return authPolicy, nil
}

type authPolicyTemplateData struct {
	Name       string
	Namespace  string
	TargetKind string
	TargetName string
}

func (k *kserveAuthPolicyTemplateLoader) renderTemplate(templateBytes []byte, data authPolicyTemplateData) (*kuadrantv1.AuthPolicy, error) {
	tmpl, err := template.New("authpolicy").Parse(string(templateBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to parse AuthPolicy template: %w", err)
	}

	var builder strings.Builder
	if err := tmpl.Execute(&builder, data); err != nil {
		return nil, fmt.Errorf("failed to execute AuthPolicy template: %w", err)
	}

	authPolicy := &kuadrantv1.AuthPolicy{}
	if err := yaml.Unmarshal([]byte(builder.String()), authPolicy); err != nil {
		return nil, fmt.Errorf("failed to unmarshal AuthPolicy YAML: %w", err)
	}

	return authPolicy, nil
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

func FindLLMServiceFromAuthPolicy(authPolicy *kuadrantv1.AuthPolicy) (types.NamespacedName, bool) {
	for _, ownerRef := range authPolicy.OwnerReferences {
		if ownerRef.Kind == "LLMInferenceService" {
			return types.NamespacedName{
				Name:      ownerRef.Name,
				Namespace: authPolicy.Namespace,
			}, true
		}
	}
	return types.NamespacedName{}, false
}
