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
func main() {
	debug := flag.Bool("debug", false, "debug")
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

	if vErr := utils.ValidateApiKey(logger, apiKey, runtimes); vErr != nil {
		panic(vErr)
	}
	logger.Info("API Key validated successfully")

	models, dErr := utils.GetNimModelData(logger, apiKey, runtimes)
	if dErr != nil {
		panic(dErr)
	}
	logger.Info(fmt.Sprintf("Got %d models info successfully", len(models)))
}
