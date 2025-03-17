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

package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

// Use this script for validating NVIDIA API access used by the NIM Account Controller.
// Controller code is in: controllers/nim_account_controller.go
// NVIDIA NIM access is encapsulated in: controllers/utils/nim.go
//
// This tool takes NVIDIA NGC API Key as the first positional argument.
// It mimics the controller work by performing the following:
//
//  1. Fetch a list of the available runtime images from NVIDIA image registry.
//  2. Validate the API Key by requesting an NVIDIA image registry token and using it to pull the manifest for
//     the first item in the list of runtime images.
//  3. Fetch model info for all the available runtimes using the API Key to request the required NGC model
//     registry token.
//
// ** add -verbose to the script name to print the runtime images and models (it's a lot of data).
func main() {
	verbose := flag.Bool("verbose", false, "verbose")
	logger := zap.New()

	flag.Parse()
	if len(flag.Args()) < 1 {
		panic("Please provide the API Key as the first positional argument")
	}
	apiKey := flag.Arg(0)

	runtimes, rErr := utils.GetAvailableNimRuntimes(logger)
	if rErr != nil {
		panic(rErr)
	}
	fmt.Printf("Got %d available runtimes successfully\n", len(runtimes))

	if *verbose {
		for _, runtime := range runtimes {
			j, jErr := json.MarshalIndent(runtime, "", "  ")
			if jErr != nil {
				panic(jErr)
			}
			fmt.Println(string(j))
		}
	}

	if vErr := utils.ValidateApiKey(logger, apiKey, runtimes); vErr != nil {
		panic(vErr)
	}
	fmt.Println("API Key validated successfully")

	models, dErr := utils.GetNimModelData(logger, apiKey, runtimes)
	if dErr != nil {
		panic(dErr)
	}
	fmt.Printf("Got %d models info successfully\n", len(models))

	if *verbose {
		for k, v := range models {
			var model bytes.Buffer
			if jErr := json.Indent(&model, []byte(v), "", "  "); jErr != nil {
				panic(jErr)
			}
			fmt.Println(k)
			fmt.Println(strings.Repeat("*", len(k)))
			if _, wErr := model.WriteTo(os.Stdout); wErr != nil {
				panic(wErr)
			}
			fmt.Println()
		}
	}
}
