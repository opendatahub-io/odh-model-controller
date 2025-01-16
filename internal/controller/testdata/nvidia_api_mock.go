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

package testdata

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
)

type NimHttpClientMock struct{}

const (
	FakeApiKey = "NGo4bmk2cmh1Z242amQyMGVhdmNwbW4zdTU6MTIyYmE4NTctMTA4My00ZDU0LWJkZmYtZDc5Njk1OWRlY2Q1"
)

func (r *NimHttpClientMock) Do(req *http.Request) (*http.Response, error) {

	// stub NGC catalog, requested from the utils.GetAvailableNimRuntimes function (nim)
	if req.URL.Host == "api.ngc.nvidia.com" && req.URL.Path == "/v2/search/catalog/resources/CONTAINER" {
		catParams := &utils.NimCatalogQuery{}
		_ = json.Unmarshal([]byte(req.URL.Query().Get("q")), catParams)
		if catParams.Query == "orgName:nim" {
			f, _ := os.ReadFile(fmt.Sprintf("testdata/ngc_catalog_response_page_%d.json", catParams.Page))
			return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(f))}, nil
		}
	}

	// stub runtime registry token, requested from the utils.ValidateApiKey function (nim)
	if req.URL.Host == "nvcr.io" && req.URL.Path == "/proxy_auth" {
		if req.URL.Query().Get("account") == "$oauthtoken" && req.URL.Query().Get("offline_token") == "true" {
			if req.URL.Query().Get("scope") == "repository:nim/microsoft/phi-3-mini-4k-instruct:pull" {
				// repository name "nim/microsoft/phi-3-mini-4k-instruct" is the FIRST resource from the available
				// from runtimes returned by the ngc catalog endpoint, check testdata/ngc_catalog_response_page_0.json
				authHeaderParts := strings.Split(req.Header.Get("Authorization"), " ")
				token, _ := base64.StdEncoding.DecodeString(authHeaderParts[1])
				if authHeaderParts[0] == "Basic" && string(token) == "$oauthtoken:"+FakeApiKey {
					f, _ := os.ReadFile("testdata/runtime_token_response.json")
					return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(f))}, nil
				}
			}
		}
	}

	// stub runtime manifest fetching, requested from the utils.ValidateApiKey function (nim)
	if req.URL.Host == "nvcr.io" && req.URL.Path == "/v2/nim/microsoft/phi-3-mini-4k-instruct/manifests/1.2.3" {
		// repository name "nim/microsoft/phi-3-mini-4k-instruct" is the FIRST resource from the available
		// from runtimes returned by the ngc catalog endpoint, version "1.2.3" is the latestTag attribute for the runtime
		// check testdata/ngc_catalog_response_page_0.json
		authHeaderParts := strings.Split(req.Header.Get("Authorization"), " ")
		if authHeaderParts[0] == "Bearer" && authHeaderParts[1] == "this-is-my-fake-token-please-dont-share-it-with-anyone" {
			// the token is returned by the nvcr.io/proxy-auth endpoint (stubbed), check testdata/runtime_token_response.json
			return &http.Response{StatusCode: 200}, nil
		}
	}

	// stub ngc model token, requested from the utils.GetNimModelData function (nim)
	if req.URL.Host == "authn.nvidia.com" && req.URL.Path == "/token" && req.URL.Query().Get("service") == "ngc" {
		authHeaderParts := strings.Split(req.Header.Get("Authorization"), " ")
		if authHeaderParts[0] == "ApiKey" && authHeaderParts[1] == FakeApiKey {
			f, _ := os.ReadFile("testdata/ngc_token_response.json")
			return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(f))}, nil
		}
	}

	// stub model info for the FIRST model we stub, requested by utils.GetNimModelData (nim)
	if req.URL.Host == "api.ngc.nvidia.com" && req.URL.Path == "/v2/org/nim/team/microsoft/repos/phi-3-mini-4k-instruct" {
		// repository name "nim/microsoft/phi-3-mini-4k-instruct" is the FIRST resource from the available
		// from runtimes returned by the ngc catalog endpoint, check testdata/ngc_catalog_response_page_0.json
		authHeaderParts := strings.Split(req.Header.Get("Authorization"), " ")
		if authHeaderParts[0] == "Bearer" && authHeaderParts[1] == "this-is-yet-another-fake-token-of-mine-you-know-what-not-do-to" {
			// the token is returned by the authn.nvidia.com/token endpoint (stubbed), check testdata/ngc_token_response.json
			f, _ := os.ReadFile("testdata/ngc_model_phi-3-mini-4k-instruct_response.json")
			return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(f))}, nil
		}
	}

	// stub model info for the SECOND model we stub, requested by utils.GetNimModelData (nim)
	if req.URL.Host == "api.ngc.nvidia.com" && req.URL.Path == "/v2/org/nim/team/meta/repos/llama-3.1-8b-instruct" {
		// repository name "nim/meta/llama-3.1-8b-instruct" is the SECOND resource from the available
		// from runtimes returned by the ngc catalog endpoint, check testdata/ngc_catalog_response_page_1.json
		authHeaderParts := strings.Split(req.Header.Get("Authorization"), " ")
		if authHeaderParts[0] == "Bearer" && authHeaderParts[1] == "this-is-yet-another-fake-token-of-mine-you-know-what-not-do-to" {
			// the token is returned by the authn.nvidia.com/token endpoint (stubbed), check testdata/ngc_token_response.json
			f, _ := os.ReadFile("testdata/ngc_model_llama-3_1-8b-instruct_response.json")
			return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(f))}, nil
		}
	}

	return &http.Response{StatusCode: 500}, errors.New("if not intentional, perhaps you forgot to stub something")
}
