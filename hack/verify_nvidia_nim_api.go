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
	"flag"
	"fmt"

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
// ** add -debug for debug logs.
// ** add -model to run the script for a specific model, i.e. -model codellama-70b-instruct (ignored for validate-all)
// ** add -validate-all to run validation for all models (only validation, no metadata scraping)
//
//	if no model is specified, validation is performed for the first model in the list and metadata scrapping for all.
//	if the models specified doesn't exist, we panic.
func main() {
	debug := flag.Bool("debug", false, "debug")
	model := flag.String("model", "", "model name")
	vall := flag.Bool("validate-all", false, "validate all")
	flag.Parse()

	if len(flag.Args()) < 1 {
		panic("Please provide the API Key as the first positional argument")
	}
	apiKey := flag.Arg(0)

	logger := zap.New(zap.UseDevMode(*debug))

	runtimes, rErr := utils.GetAvailableNimRuntimes(logger)
	if rErr != nil {
		panic(rErr)
	}
	logger.Info(fmt.Sprintf("Got %d available runtimes successfully", len(runtimes)))

	if *vall {
		// validate for all models, no metadata scraping
		for _, rt := range runtimes {
			if vErr := utils.ValidateApiKey(logger, apiKey, []utils.NimRuntime{rt}); vErr != nil {
				logger.Error(vErr, fmt.Sprintf("API Key validation failed for %s", rt.Resource))
			}
			logger.Info(fmt.Sprintf("API Key validated successfully for %s", rt.Resource))
		}

	} else {
		// validate with one model, either random or as specified
		var targetRts []utils.NimRuntime
		var msg string

		if *model == "" {
			// no model specified, use first runtime for validation and all runtimes for metadata scrapping
			targetRts = runtimes
			msg = "API Key validated successfully"
		} else {
			// model specified, fetch it from the runtime list and only use it for validation and metadata scraping
			for _, rt := range runtimes {
				if rt.Image == *model {
					targetRts = []utils.NimRuntime{rt}
					break
				}
			}
			// model specified, but not found
			if len(targetRts) == 0 {
				panic(fmt.Sprintf("model %s not found", *model))
			}
			msg = fmt.Sprintf("API Key validated successfully for %s", targetRts[0].Resource)
		}

		if vErr := utils.ValidateApiKey(logger, apiKey, targetRts); vErr != nil {
			panic(vErr)
		}
		logger.Info(msg)

		models, dErr := utils.GetNimModelData(logger, apiKey, targetRts)
		if dErr != nil {
			panic(dErr)
		}
		logger.Info(fmt.Sprintf("Got %d models info successfully", len(models)))
	}
}
