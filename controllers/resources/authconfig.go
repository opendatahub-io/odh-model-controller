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

package resources

import (
	"bytes"
	"context"
	_ "embed" // needed for go:embed directive
	"fmt"
	"os"
	"sort"
	"strings"
	"text/template"

	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	authorinov1beta2 "github.com/kuadrant/authorino/api/v1beta2"
	"github.com/opendatahub-io/odh-model-controller/controllers/constants"
	"github.com/opendatahub-io/odh-model-controller/controllers/utils"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type AuthType string

const (
	UserDefined    AuthType = "userdefined"
	Anonymous      AuthType = "anonymous"
	AuthAudience            = "AUTH_AUDIENCE"
	AuthorinoLabel          = "AUTHORINO_LABEL"
)

type InferenceServiceHostExtractor interface {
	Extract(isvc *kservev1beta1.InferenceService) []string
}

type AuthConfigTemplateLoader interface {
	Load(ctx context.Context, authType AuthType, key types.NamespacedName) (authorinov1beta2.AuthConfig, error)
}

type AuthTypeDetector interface {
	Detect(ctx context.Context, isvc *kservev1beta1.InferenceService) (AuthType, error)
}

type AuthConfigStore interface {
	Get(ctx context.Context, key types.NamespacedName) (*authorinov1beta2.AuthConfig, error)
	Remove(ctx context.Context, key types.NamespacedName) error
	Create(ctx context.Context, authConfig *authorinov1beta2.AuthConfig) error
	Update(ctx context.Context, authConfig *authorinov1beta2.AuthConfig) error
}

//go:embed template/authconfig_anonymous.yaml
var authConfigTemplateAnonymous []byte

//go:embed template/authconfig_userdefined.yaml
var authConfigTemplateUserDefined []byte

type staticTemplateLoader struct {
}

func NewStaticTemplateLoader() AuthConfigTemplateLoader {
	return &staticTemplateLoader{}
}

func (s *staticTemplateLoader) Load(ctx context.Context, authType AuthType, key types.NamespacedName) (authorinov1beta2.AuthConfig, error) {
	authConfig := authorinov1beta2.AuthConfig{}

	authKey, authVal, err := getAuthorinoLabel()
	if err != nil {
		return authConfig, err
	}

	templateData := map[string]interface{}{
		"Namespace":            key.Namespace,
		"Audiences":            getAuthAudience(),
		"AuthorinoLabel":       authKey + ": " + authVal,
		"InferenceServiceName": key.Name,
	}
	template := authConfigTemplateAnonymous
	if authType == UserDefined {
		template = authConfigTemplateUserDefined
	}

	resolvedTemplate, err := s.resolveTemplate(template, templateData)
	if err != nil {
		return authConfig, fmt.Errorf("could not resovle auth template. cause %w", err)
	}
	err = utils.ConvertToStructuredResource(resolvedTemplate, &authConfig)
	if err != nil {
		return authConfig, fmt.Errorf("could not load auth template. cause %w", err)
	}
	return authConfig, nil
}

func (s *staticTemplateLoader) resolveTemplate(tmpl []byte, data map[string]interface{}) ([]byte, error) {
	engine, err := template.New("authconfig").Parse(string(tmpl))
	if err != nil {
		return []byte{}, err
	}
	buf := new(bytes.Buffer)
	err = engine.Execute(buf, data)
	if err != nil {
		return []byte{}, err
	}
	return buf.Bytes(), nil

}

type configMapTemplateLoader struct {
	client   client.Client
	fallback AuthConfigTemplateLoader
}

func NewConfigMapTemplateLoader(client client.Client, fallback AuthConfigTemplateLoader) AuthConfigTemplateLoader {
	return &configMapTemplateLoader{
		client:   client,
		fallback: fallback,
	}
}

func (c *configMapTemplateLoader) Load(ctx context.Context, authType AuthType, key types.NamespacedName) (authorinov1beta2.AuthConfig, error) {
	// TOOD: check "authconfig-template" CM in key.Namespace to see if there is a "spec" to use, construct a AuthConfig object
	// https://issues.redhat.com/browse/RHOAIENG-847

	// else
	return c.fallback.Load(ctx, authType, key)
}

type clientAuthConfigStore struct {
	client client.Client
}

func NewClientAuthConfigStore(client client.Client) AuthConfigStore {
	return &clientAuthConfigStore{
		client: client,
	}
}

func (c *clientAuthConfigStore) Get(ctx context.Context, key types.NamespacedName) (*authorinov1beta2.AuthConfig, error) {
	authConfig := &authorinov1beta2.AuthConfig{
		TypeMeta: v1.TypeMeta{
			APIVersion: "authorino.kuadrant.io/v1beta2",
			Kind:       "AuthConfig",
		},
	}

	err := c.client.Get(ctx, key, authConfig)
	if err != nil {
		return nil, fmt.Errorf("could not GET authconfig %s. cause %w", key, err)
	}
	return authConfig, nil
}

func (c *clientAuthConfigStore) Remove(ctx context.Context, key types.NamespacedName) error {
	authConfig := authorinov1beta2.AuthConfig{}
	authConfig.Name = key.Name
	authConfig.Namespace = key.Namespace
	if err := c.client.Delete(ctx, &authConfig); err != nil {
		return fmt.Errorf("could not DELETE authconfig %s. cause %w", key, err)
	}
	return nil
}

func (c *clientAuthConfigStore) Create(ctx context.Context, authConfig *authorinov1beta2.AuthConfig) error {
	if err := c.client.Create(ctx, authConfig); err != nil {
		return fmt.Errorf("could not CREATE authconfig %s/%s. cause %w", authConfig.Namespace, authConfig.Name, err)
	}
	return nil
}

func (c *clientAuthConfigStore) Update(ctx context.Context, authConfig *authorinov1beta2.AuthConfig) error {
	if err := c.client.Update(ctx, authConfig); err != nil {
		return fmt.Errorf("could not UPDATE authconfig %s/%s. cause %w", authConfig.Namespace, authConfig.Name, err)
	}
	return nil
}

type kserveAuthTypeDetector struct {
	client client.Client
}

func NewKServeAuthTypeDetector(client client.Client) AuthTypeDetector {
	return &kserveAuthTypeDetector{
		client: client,
	}
}

func (k *kserveAuthTypeDetector) Detect(ctx context.Context, isvc *kservev1beta1.InferenceService) (AuthType, error) {
	if value, exist := isvc.Annotations[constants.LabelEnableAuthODH]; exist {
		if strings.ToLower(value) == "true" {
			return UserDefined, nil
		}
	} else { // backward compat
		if strings.ToLower(isvc.Annotations[constants.LabelEnableAuth]) == "true" {
			return UserDefined, nil
		}
	}
	return Anonymous, nil
}

type kserveInferenceServiceHostExtractor struct {
}

func NewKServeInferenceServiceHostExtractor() InferenceServiceHostExtractor {
	return &kserveInferenceServiceHostExtractor{}
}

func (k *kserveInferenceServiceHostExtractor) Extract(isvc *kservev1beta1.InferenceService) []string {

	hosts := k.findAllURLHosts(isvc)

	for _, host := range hosts {
		if strings.HasSuffix(host, ".svc.cluster.local") {
			hosts = append(hosts, strings.ReplaceAll(host, ".svc.cluster.local", ""))
			hosts = append(hosts, strings.ReplaceAll(host, ".svc.cluster.local", ".svc"))
		}
	}
	sort.Strings(hosts)
	return hosts
}

func (k *kserveInferenceServiceHostExtractor) findAllURLHosts(isvc *kservev1beta1.InferenceService) []string {
	hosts := []string{}

	if isvc.Status.URL != nil {
		hosts = append(hosts, isvc.Status.URL.Host)
	}

	if isvc.Status.Address != nil && isvc.Status.Address.URL != nil {
		hosts = append(hosts, isvc.Status.Address.URL.Host)
	}

	for _, comp := range isvc.Status.Components {
		if comp.Address != nil && comp.Address.URL != nil {
			hosts = append(hosts, comp.Address.URL.Host)
		}
		if comp.URL != nil {
			hosts = append(hosts, comp.URL.Host)
		}
		if comp.GrpcURL != nil {
			hosts = append(hosts, comp.GrpcURL.Host)
		}
		if comp.RestURL != nil {
			hosts = append(hosts, comp.RestURL.Host)
		}
		for _, tt := range comp.Traffic {
			if tt.URL != nil {
				hosts = append(hosts, tt.URL.Host)
			}
		}
	}

	unique := func(in []string) []string {
		m := map[string]bool{}
		for _, v := range in {
			m[v] = true
		}
		k := make([]string, len(m))
		i := 0
		for v := range m {
			k[i] = v
			i++
		}
		return k
	}
	return unique(hosts)
}

func getAuthAudience() []string {
	aud := getEnvOr(AuthAudience, "https://kubernetes.default.svc")
	audiences := strings.Split(aud, ",")
	for i := range audiences {
		audiences[i] = strings.TrimSpace(audiences[i])
	}
	return audiences
}

func getAuthorinoLabel() (string, string, error) {
	label := getEnvOr(AuthorinoLabel, "security.opendatahub.io/authorization-group=default")
	keyValue := strings.Split(label, "=")

	if len(keyValue) != 2 {
		return "", "", fmt.Errorf("expected authorino label to be in format key=value, got [%s]", label)
	}

	return keyValue[0], keyValue[1], nil
}

func getEnvOr(key, defaultValue string) string {
	if env, defined := os.LookupEnv(key); defined {
		return env
	}

	return defaultValue
}
