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

package utils

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/hashicorp/go-multierror"
	"github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kserveconstants "github.com/kserve/kserve/pkg/constants"
	v1 "github.com/opendatahub-io/odh-model-controller/api/nim/v1"
	templatev1 "github.com/openshift/api/template/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/semantic"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

type (
	// NimCatalogQuery is used for constructing a query for NIM catalog fetch
	NimCatalogQuery struct {
		Query    string `json:"query"`
		Page     int    `json:"page"`
		PageSize int    `json:"pageSize"`
	}

	// NimCatalogResponse represents the NIM catalog fetch response
	NimCatalogResponse struct {
		ResultPageTotal int `json:"resultPageTotal"`
		Params          struct {
			Page int `json:"page"`
		} `json:"params"`
		Results []struct {
			GroupValue string `json:"groupValue"`
			Resources  []struct {
				ResourceId string `json:"resourceId"`
				Attributes []struct {
					Key   string `json:"key"`
					Value string `json:"value"`
				} `json:"attributes"`
				Name string `json:"name"`
			} `json:"resources"`
		} `json:"results"`
	}

	// NimTokenResponse represents the NIM token response
	NimTokenResponse struct {
		Token     string `json:"token"`
		ExpiresIn int    `json:"expires_in"`
	}

	// NimRuntime is a representation of a NIM custom runtime
	NimRuntime struct {
		Resource string
		Version  string
		Org      string
		Team     string
		Image    string
		Name     string
	}

	// NimModel is a representation of NIM model info
	NimModel struct {
		Name             string   `json:"name"`
		DisplayName      string   `json:"displayName"`
		ShortDescription string   `json:"shortDescription"`
		Namespace        string   `json:"namespace"`
		Tags             []string `json:"tags"`
		LatestTag        string   `json:"latestTag"`
		UpdatedDate      string   `json:"updatedDate"`
	}

	HttpClient interface {
		Do(*http.Request) (*http.Response, error)
	}
)

const (
	nimGetRuntimeTokenFmt    = "https://nvcr.io/proxy_auth?account=$oauthtoken&offline_token=true&scope=repository:%s:pull"
	nimGetRuntimeManifestFmt = "https://nvcr.io/v2/%s/manifests/%s"
	nimGetCallerInfoFmt      = "https://api.ngc.nvidia.com/v3/keys/get-caller-info"
	nimGetNgcCatalog         = "https://api.ngc.nvidia.com/v2/search/catalog/resources/CONTAINER"
	nimGetNgcToken           = "https://authn.nvidia.com/token?service=ngc&"
	nimGetNgcModelDataFmt    = "https://api.ngc.nvidia.com/v2/org/%s/team/%s/repos/%s?resolve-labels=true"
	IsNimRuntimeAnnotation   = "runtimes.opendatahub.io/nvidia-nim"
)

var NimHttpClient HttpClient
var NimEqualities semantic.Equalities

func init() {
	NimHttpClient = &http.Client{Timeout: time.Second * 30}
	NimEqualities = semantic.EqualitiesOrDie(
		func(a, b resource.Quantity) bool {
			return true
		},
		func(a, b metav1.Time) bool {
			return a.UTC() == b.UTC()
		},
	)
}

// GetAvailableNimRuntimes is used for fetching a list of available NIM custom runtimes
func GetAvailableNimRuntimes(logger logr.Logger) ([]NimRuntime, error) {
	return getNimRuntimes(logger, []NimRuntime{}, 0, 1000)
}

// ValidateApiKey is used for validating the given API key by retrieving the token and pulling the given custom runtime
func ValidateApiKey(logger logr.Logger, apiKey string, runtimes []NimRuntime) error {
	if isPersonalApiKey(apiKey) {
		return validatePersonalApiKey(logger, apiKey)
	}
	return validateLegacyApiKey(logger, apiKey, runtimes)
}

// validatePersonalApiKey is used for validating the given API key by retrieving the token and pulling the given custom runtime
func validatePersonalApiKey(logger logr.Logger, apiKey string) error {
	req, reqErr := http.NewRequest("POST", nimGetCallerInfoFmt, strings.NewReader(fmt.Sprintf("credentials=%s", apiKey)))
	if reqErr != nil {
		return reqErr
	}

	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", apiKey))

	resp, respErr := handleRequest(logger, req)
	if respErr != nil {
		return respErr
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("got response %s", resp.Status)
	}
	return nil
}

// validateLegacyApiKey is used for validating the given Legacy API key by retrieving the token and pulling the given custom runtime
// DEPRECATED: please use personal api keys to avoid disruption. Legacy api keys are deprecated and will be removed in the future.
func validateLegacyApiKey(logger logr.Logger, apiKey string, runtimes []NimRuntime) error {
	logger.Info("validating as legacy api key, this is deprecated, please use personal api key to avoid disruption")
	for _, runtime := range runtimes {
		tokenResp, tokenErr := getRuntimeRegistryToken(logger, apiKey, runtime.Resource)
		if tokenErr != nil {
			return tokenErr
		}

		manifestResp, manifestErr := attemptToPullManifest(logger, runtime, tokenResp)
		if manifestErr != nil {
			return manifestErr
		}

		switch manifestResp.StatusCode {
		case http.StatusUnavailableForLegalReasons:
			continue
		case http.StatusOK:
			return nil
		}
		break
	}
	return fmt.Errorf("failed to validate api key")
}

// GetNimModelData is used for fetching the model info for the given runtimes, returns configmap data
func GetNimModelData(logger logr.Logger, apiKey string, runtimes []NimRuntime) (map[string]string, error) {
	data := map[string]string{}
	var key string
	if isPersonalApiKey(apiKey) {
		// personal api keys can be used directly as bearer tokens
		key = apiKey
	} else {
		// legacy api keys require a token creation
		logger.Info("using a legacy api key to fetch models data, this is deprecated, please use personal api key to avoid disruption")
		tokenResp, tokenErr := getNgcToken(logger, apiKey)
		if tokenErr != nil {
			return data, tokenErr
		}
		key = tokenResp.Token
	}

	for _, runtime := range runtimes {
		if model, unmarshaled := getModelData(logger, runtime, key); model != nil {
			data[model.Name] = unmarshaled
		}
	}

	if len(data) < 1 {
		return nil, fmt.Errorf("no models found")
	}
	return data, nil
}

// getNimRuntimes is used to send multiple requests to NVIDIA NIM runtimes endpoint, response pagination-based.
// it parses the runtimes from every response and returns a list of all runtimes
func getNimRuntimes(logger logr.Logger, runtimes []NimRuntime, page, pageSize int) ([]NimRuntime, error) {
	req, reqErr := http.NewRequest("GET", nimGetNgcCatalog, nil)
	if reqErr != nil {
		return runtimes, reqErr
	}

	params, _ := json.Marshal(NimCatalogQuery{Query: "orgName:nim", Page: page, PageSize: pageSize})
	query := req.URL.Query()
	query.Add("q", string(params))

	req.URL.RawQuery = query.Encode()

	resp, respErr := handleRequest(logger, req)
	if respErr != nil {
		return runtimes, respErr
	}

	if resp.StatusCode != http.StatusOK {
		return runtimes, errors.New(resp.Status)
	}

	body, bodyErr := io.ReadAll(resp.Body)
	if bodyErr != nil {
		return runtimes, bodyErr
	}

	catRes := &NimCatalogResponse{}
	if err := json.Unmarshal(body, catRes); err != nil {
		return runtimes, err
	}

	runtimes = append(runtimes, mapNimCatalogResponseToRuntimeList(catRes)...)
	if catRes.Params.Page < catRes.ResultPageTotal-1 {
		return getNimRuntimes(logger, runtimes, page+1, pageSize)
	}

	return runtimes, nil
}

// mapNimCatalogResponseToRuntimeList is used for parsing the ngc catalog response to a list of available runtimes
func mapNimCatalogResponseToRuntimeList(resp *NimCatalogResponse) []NimRuntime {
	var runtimes []NimRuntime
	for _, result := range resp.Results {
		if result.GroupValue == "CONTAINER" {
			for _, res := range result.Resources {
				for _, attribute := range res.Attributes {
					if attribute.Key == "latestTag" {
						parts := strings.Split(res.ResourceId, "/")
						runtimes = append(runtimes, NimRuntime{
							Resource: res.ResourceId,
							Version:  attribute.Value,
							Org:      parts[0],
							Team:     parts[1],
							Image:    parts[2],
							Name:     res.Name,
						})
						break
					}
				}
			}
		}
	}

	return runtimes
}

// getRuntimeRegistryToken is used for fetching the token required for accessing NIM's runtimes
func getRuntimeRegistryToken(logger logr.Logger, apiKey, repo string) (*NimTokenResponse, error) {
	req, reqErr := http.NewRequest("GET", fmt.Sprintf(nimGetRuntimeTokenFmt, repo), nil)
	if reqErr != nil {
		return nil, reqErr
	}

	encoded := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("$oauthtoken:%s", apiKey)))
	req.Header.Add("Authorization", fmt.Sprintf("Basic %s", encoded))

	return requestToken(logger, req)
}

// getNgcToken is used for fetching the token required for accessing NIM's models
func getNgcToken(logger logr.Logger, apiKey string) (*NimTokenResponse, error) {
	req, reqErr := http.NewRequest("GET", nimGetNgcToken, nil)
	if reqErr != nil {
		return nil, reqErr
	}

	req.Header.Add("Authorization", fmt.Sprintf("ApiKey %s", apiKey))

	return requestToken(logger, req)
}

// requestToken is used for sending a token requests and parse the response
func requestToken(logger logr.Logger, req *http.Request) (*NimTokenResponse, error) {
	resp, respErr := handleRequest(logger, req)
	if respErr != nil {
		return nil, respErr
	}

	if resp.StatusCode != http.StatusOK {
		return nil, errors.New(resp.Status)
	}

	body, bodyErr := io.ReadAll(resp.Body)
	if bodyErr != nil {
		return nil, bodyErr
	}

	tokenResponse := &NimTokenResponse{}
	if err := json.Unmarshal(body, tokenResponse); err != nil {
		return nil, err
	}

	return tokenResponse, nil
}

// attemptToPullManifest is used for pulling a runtime for verifying access
func attemptToPullManifest(logger logr.Logger, runtime NimRuntime, tokenResp *NimTokenResponse) (*http.Response, error) {
	req, reqErr := http.NewRequest("GET", fmt.Sprintf(nimGetRuntimeManifestFmt, runtime.Resource, runtime.Version), nil)
	if reqErr != nil {
		return nil, reqErr
	}

	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", tokenResp.Token))
	req.Header.Add("Accept", "application/vnd.oci.image.index.v1+json")

	return handleRequest(logger, req)
}

// getModelData is used for fetching NIM model data for the given runtime
func getModelData(logger logr.Logger, runtime NimRuntime, key string) (*NimModel, string) {
	req, reqErr := http.NewRequest("GET", fmt.Sprintf(nimGetNgcModelDataFmt, runtime.Org, runtime.Team, runtime.Image), nil)
	if reqErr != nil {
		logger.Error(reqErr, "failed to construct request")
		return nil, ""
	}

	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", key))

	resp, respErr := handleRequest(logger, req)
	if respErr != nil {
		return nil, ""
	}

	if resp.StatusCode != http.StatusOK {
		sErr := fmt.Errorf("got response %s", resp.Status)
		if resp.StatusCode == http.StatusUnavailableForLegalReasons {
			logger.Info("content not available for legal reasons")
		} else {
			logger.Error(sErr, "unexpected response status code")
		}
		return nil, ""
	}

	body, bodyErr := io.ReadAll(resp.Body)
	if bodyErr != nil {
		logger.Error(bodyErr, "failed to read response body")
		return nil, ""
	}

	model := &NimModel{}
	if err := json.Unmarshal(body, model); err != nil {
		logger.Error(err, "failed to deserialize body")
		return nil, ""
	}
	return model, string(body)
}

func handleRequest(logger logr.Logger, req *http.Request) (*http.Response, error) {
	logger.V(1).Info(fmt.Sprintf("sending api request %s", req.URL))

	resp, doErr := NimHttpClient.Do(req)
	if doErr != nil {
		logger.Error(doErr, "failed to send request")
		return nil, doErr
	}

	logger.V(1).Info(fmt.Sprintf("got api response %s", resp.Status))
	if resp.StatusCode != http.StatusOK {
		if resp.ContentLength > 0 {
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				logger.V(1).Error(err, "failed to parse body")
			} else {
				logger.V(1).Info(fmt.Sprintf("got body %s", string(body)))
			}
		}
	}
	return resp, nil
}

func isPersonalApiKey(apiKey string) bool {
	return strings.HasPrefix(apiKey, "nvapi-")
}

// GetNimServingRuntimeTemplate returns the Template used by ODH for creating serving runtimes
func GetNimServingRuntimeTemplate(scheme *runtime.Scheme) (*v1alpha1.ServingRuntime, error) {
	multiModel := false
	sr := &v1alpha1.ServingRuntime{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				"opendatahub.io/recommended-accelerators": "[\"nvidia.com/gpu\"]",
				"openshift.io/display-name":               "NVIDIA NIM",
				IsNimRuntimeAnnotation:                    "true",
			},
			Labels: map[string]string{
				"opendatahub.io/dashboard": "true",
			},
			Name: "nvidia-nim-runtime",
		},
		Spec: v1alpha1.ServingRuntimeSpec{
			ServingRuntimePodSpec: v1alpha1.ServingRuntimePodSpec{
				Annotations: map[string]string{
					"prometheus.io/path": "/metrics",
					"prometheus.io/port": "8000",
				},
				Containers: []corev1.Container{
					{Env: []corev1.EnvVar{
						{
							Name:  "NIM_CACHE_PATH",
							Value: "/mnt/models/cache",
						},
						{
							Name: "NGC_API_KEY",
							ValueFrom: &corev1.EnvVarSource{
								SecretKeyRef: &corev1.SecretKeySelector{
									Key: "NGC_API_KEY",
									LocalObjectReference: corev1.LocalObjectReference{
										Name: "nvidia-nim-secrets",
									},
								},
							},
						},
					},
						Image: "",
						Name:  "kserve-container",
						Ports: []corev1.ContainerPort{
							{
								ContainerPort: 8000,
								Protocol:      corev1.ProtocolTCP,
							},
						},
						Resources: corev1.ResourceRequirements{
							Limits: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("2"),
								corev1.ResourceMemory: resource.MustParse("8Gi"),
								"nvidia.com/gpu":      resource.MustParse("2"),
							},
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("1"),
								corev1.ResourceMemory: resource.MustParse("4Gi"),
								"nvidia.com/gpu":      resource.MustParse("2"),
							},
						},
						VolumeMounts: []corev1.VolumeMount{
							{
								MountPath: "/dev/shm",
								Name:      "shm",
							},
							{
								MountPath: "/mnt/models/cache",
								Name:      "nim-pvc",
							},
							{
								MountPath: "/opt/nim/workspace",
								Name:      "nim-workspace",
							},
							{
								MountPath: "/.cache",
								Name:      "nim-cache",
							},
						},
					},
				},
				ImagePullSecrets: []corev1.LocalObjectReference{
					{
						Name: "ngc-secret",
					},
				},
				Volumes: []corev1.Volume{
					{
						Name: "nim-pvc",
						VolumeSource: corev1.VolumeSource{
							PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
								ClaimName: "nim-pvc",
							},
						},
					},
					{
						Name: "nim-workspace",
						VolumeSource: corev1.VolumeSource{
							EmptyDir: &corev1.EmptyDirVolumeSource{},
						},
					},
					{
						Name: "nim-cache",
						VolumeSource: corev1.VolumeSource{
							EmptyDir: &corev1.EmptyDirVolumeSource{},
						},
					},
				},
			},
			MultiModel: &multiModel,
			ProtocolVersions: []kserveconstants.InferenceServiceProtocol{
				kserveconstants.ProtocolGRPCV2,
				kserveconstants.ProtocolV2,
			},
			SupportedModelFormats: []v1alpha1.SupportedModelFormat{{Name: "replace-me"}},
		},
	}

	gvk, err := apiutil.GVKForObject(sr, scheme)
	if err != nil {
		return nil, err
	}
	sr.SetGroupVersionKind(gvk)

	return sr, nil
}

// CleanupResources is used for deleting the integration related resources (configmap, template, pull secret)
func CleanupResources(ctx context.Context, account *v1.Account, kubeClient client.Client) error {
	var delObjs []client.Object

	if account.Status.NIMPullSecret != nil {
		delObjs = append(delObjs, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      account.Status.NIMPullSecret.Name,
				Namespace: account.Status.NIMPullSecret.Namespace,
			},
		})
	}

	if account.Status.NIMConfig != nil {
		delObjs = append(delObjs, &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      account.Status.NIMConfig.Name,
				Namespace: account.Status.NIMConfig.Namespace,
			},
		})
	}

	if account.Status.RuntimeTemplate != nil {
		delObjs = append(delObjs, &templatev1.Template{
			ObjectMeta: metav1.ObjectMeta{
				Name:      account.Status.RuntimeTemplate.Name,
				Namespace: account.Status.RuntimeTemplate.Namespace,
			},
		})
	}

	var deleteErrors *multierror.Error
	for _, obj := range delObjs {
		if err := kubeClient.Delete(ctx, obj); err != nil {
			if !k8serrors.IsNotFound(err) {
				deleteErrors = multierror.Append(deleteErrors, err)
			}
		}
	}
	return deleteErrors.ErrorOrNil()
}

// UpdateStatus is used for fetching an updating the status of the account
func UpdateStatus(ctx context.Context, subject types.NamespacedName, status v1.AccountStatus, kubeClient client.Client) error {
	account := &v1.Account{}
	if err := kubeClient.Get(ctx, subject, account); err != nil {
		if !k8serrors.IsNotFound(err) {
			return err
		}
	} else {
		if !NimEqualities.DeepEqual(account.Status, status) {
			account.Status = *status.DeepCopy()
			if err = kubeClient.Status().Update(ctx, account); err != nil {
				return err
			}
		}
	}
	return nil
}

// NIMCleanupRunner is a Runnable used for cleaning up NIM when disabled
type NIMCleanupRunner struct {
	Client client.Client
	Logger logr.Logger
}

func (r *NIMCleanupRunner) Start(ctx context.Context) error {
	accounts := &v1.AccountList{}
	if err := r.Client.List(ctx, accounts); err != nil {
		r.Logger.Error(err, "failed to fetch accounts")
	}
	for _, account := range accounts.Items {
		r.Logger.V(1).Info("Cleaning up resources for account", "namespace", account.Namespace, "name", account.Name)
		// Call CleanupResources for the current account
		if err := CleanupResources(ctx, &account, r.Client); err != nil {
			r.Logger.Error(err, "failed to perform clean up on some accounts")
		}

		msg := "NIM has been disabled"
		cleanStatus := v1.AccountStatus{
			NIMConfig:       nil,
			RuntimeTemplate: nil,
			NIMPullSecret:   nil,
			Conditions: []metav1.Condition{
				MakeNimCondition(NimConditionAccountStatus, metav1.ConditionUnknown, account.Generation,
					"AccountNotReconciled", msg),
				MakeNimCondition(NimConditionAPIKeyValidation, metav1.ConditionUnknown, account.Generation,
					"ApiKeyNotReconciled", msg),
				MakeNimCondition(NimConditionConfigMapUpdate, metav1.ConditionUnknown, account.Generation,
					"ConfigMapNotReconciled", msg),
				MakeNimCondition(NimConditionTemplateUpdate, metav1.ConditionUnknown, account.Generation,
					"TemplateNotReconciled", msg),
				MakeNimCondition(NimConditionSecretUpdate, metav1.ConditionUnknown, account.Generation,
					"SecretNotReconciled", msg),
			},
		}

		if err := UpdateStatus(ctx, client.ObjectKeyFromObject(&account), cleanStatus, r.Client); err != nil {
			r.Logger.Error(err, "failed to perform clean up on some accounts")
		}
	}
	return nil
}

// ObjectKeyFromReference returns the ObjectKey given a ObjectReference
func ObjectKeyFromReference(ref *corev1.ObjectReference) client.ObjectKey {
	return types.NamespacedName{Name: ref.Name, Namespace: ref.Namespace}
}
