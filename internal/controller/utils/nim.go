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
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

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
	nimGetNgcCatalog         = "https://api.ngc.nvidia.com/v2/search/catalog/resources/CONTAINER"
	nimGetNgcToken           = "https://authn.nvidia.com/token?service=ngc&"
	nimGetNgcModelDataFmt    = "https://api.ngc.nvidia.com/v2/org/%s/team/%s/repos/%s?resolve-labels=true"
	IsNimRuntimeAnnotation   = "runtimes.opendatahub.io/nvidia-nim"
)

var NimHttpClient HttpClient

func init() {
	NimHttpClient = &http.Client{Timeout: time.Second * 30}
}

// GetAvailableNimRuntimes is used for fetching a list of available NIM custom runtimes
func GetAvailableNimRuntimes() ([]NimRuntime, error) {
	return getNimRuntimes([]NimRuntime{}, 0, 1000)
}

// ValidateApiKey is used for validating the given API key by retrieving the token and pulling the given custom runtime
func ValidateApiKey(apiKey string, runtime NimRuntime) error {
	tokenResp, tokenErr := getRuntimeRegistryToken(apiKey, runtime.Resource)
	if tokenErr != nil {
		return tokenErr
	}

	manifestErr := attemptToPullManifest(runtime, tokenResp)
	if manifestErr != nil {
		return manifestErr
	}

	return nil
}

// GetNimModelData is used for fetching the model info for the given runtimes, returns configmap data
func GetNimModelData(apiKey string, runtimes []NimRuntime) (map[string]string, error) {
	data := map[string]string{}
	tokenResp, tokenErr := getNgcToken(apiKey)
	if tokenErr != nil {
		return data, tokenErr
	}

	var modelErr error
	for _, runtime := range runtimes {
		model, unmarshaled, err := getModelData(runtime, tokenResp)
		if err != nil {
			modelErr = err
			break
		}
		data[model.Name] = unmarshaled
	}

	if modelErr != nil {
		return nil, modelErr
	}

	return data, nil
}

// getNimRuntimes is used to send multiple requests to NVIDIA NIM runtimes endpoint, response pagination-based.
// it parses the runtimes from every response and returns a list of all runtimes
func getNimRuntimes(runtimes []NimRuntime, page, pageSize int) ([]NimRuntime, error) {
	req, reqErr := http.NewRequest("GET", nimGetNgcCatalog, nil)
	if reqErr != nil {
		return runtimes, reqErr
	}

	params, _ := json.Marshal(NimCatalogQuery{Query: "orgName:nim", Page: page, PageSize: pageSize})
	query := req.URL.Query()
	query.Add("q", string(params))

	req.URL.RawQuery = query.Encode()

	resp, respErr := NimHttpClient.Do(req)
	if respErr != nil {
		return runtimes, respErr
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
		return getNimRuntimes(runtimes, page+1, pageSize)
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
func getRuntimeRegistryToken(apiKey, repo string) (*NimTokenResponse, error) {
	req, reqErr := http.NewRequest("GET", fmt.Sprintf(nimGetRuntimeTokenFmt, repo), nil)
	if reqErr != nil {
		return nil, reqErr
	}

	encoded := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("$oauthtoken:%s", apiKey)))
	req.Header.Add("Authorization", fmt.Sprintf("Basic %s", encoded))

	return requestToken(req)
}

// getNgcToken is used for fetching the token required for accessing NIM's models
func getNgcToken(apiKey string) (*NimTokenResponse, error) {
	req, reqErr := http.NewRequest("GET", nimGetNgcToken, nil)
	if reqErr != nil {
		return nil, reqErr
	}

	req.Header.Add("Authorization", fmt.Sprintf("ApiKey %s", apiKey))

	return requestToken(req)
}

// requestToken is used for sending a token requests and parse the response
func requestToken(req *http.Request) (*NimTokenResponse, error) {
	resp, respErr := NimHttpClient.Do(req)
	if respErr != nil {
		return nil, respErr
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
func attemptToPullManifest(runtime NimRuntime, tokenResp *NimTokenResponse) error {
	req, reqErr := http.NewRequest("GET", fmt.Sprintf(nimGetRuntimeManifestFmt, runtime.Resource, runtime.Version), nil)
	if reqErr != nil {
		return reqErr
	}

	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", tokenResp.Token))
	req.Header.Add("Accept", "application/vnd.oci.image.index.v1+json")

	resp, respErr := NimHttpClient.Do(req)
	if respErr != nil {
		return respErr
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to pull manifest")
	}

	return nil
}

// getModelData is used for fetching NIM model data for the given runtime
func getModelData(runtime NimRuntime, tokenResp *NimTokenResponse) (*NimModel, string, error) {
	req, reqErr := http.NewRequest("GET", fmt.Sprintf(nimGetNgcModelDataFmt, runtime.Org, runtime.Team, runtime.Image), nil)
	if reqErr != nil {
		return nil, "", reqErr
	}

	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", tokenResp.Token))

	resp, respErr := NimHttpClient.Do(req)
	if respErr != nil {
		return nil, "", respErr
	}

	body, bodyErr := io.ReadAll(resp.Body)
	if bodyErr != nil {
		return nil, "", bodyErr
	}

	model := &NimModel{}
	if err := json.Unmarshal(body, model); err != nil {
		return nil, "", err
	}

	return model, string(body), nil
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
					"prometheus.io/path":                    "/metrics",
					"prometheus.io/port":                    "8000",
					"serving.knative.dev/progress-deadline": "30m",
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
		account.Status = *status.DeepCopy()
		if err = kubeClient.Status().Update(ctx, account); err != nil {
			return err
		}
	}
	return nil
}
