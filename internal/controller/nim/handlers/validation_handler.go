/*
Copyright 2024.

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

package handlers

import (
	"context"
	"fmt"
	"strings"
	"time"

	v1 "github.com/opendatahub-io/odh-model-controller/api/nim/v1"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

var _ NimHandler = &ValidationHandler{}

// ValidationHandler is a NimHandler implementation validating the api key
type ValidationHandler struct {
	Client     client.Client
	KeyManager *APIKeyManager
}

func (v *ValidationHandler) Handle(ctx context.Context, account *v1.Account) HandleResponse {
	logger := log.FromContext(ctx)

	apiKeySecret, kmErr := v.KeyManager.GetAPIKeySecret(ctx, account)
	if kmErr != nil {
		if k8serrors.IsNotFound(kmErr) {
			return HandleResponse{} // don't requeue if the api key secret is not found
		}
		return HandleResponse{Error: kmErr} // requeue for other errors
	}

	if _, skipValidation := apiKeySecret.Annotations[constants.NimSkipValidationAnnotation]; skipValidation {
		logger.Info("skipping validation")
		defer func() {
			logger.V(1).Info("removing skip validation annotation")

			patchFmt := "[{\"op\": \"remove\", \"path\": \"/metadata/annotations/%s\"}"
			patchStr := fmt.Sprintf(patchFmt, strings.ReplaceAll(constants.NimSkipValidationAnnotation, "/", "~1"))

			if _, forceValidation := apiKeySecret.Annotations[constants.NimForceValidationAnnotation]; forceValidation {
				patchStr += fmt.Sprintf(",%s", fmt.Sprintf(patchFmt, fmt.Sprintf(patchFmt, strings.ReplaceAll(constants.NimForceValidationAnnotation, "/", "~1"))))
			}

			patchStr += "]"
			if pErr := v.Client.Patch(ctx, apiKeySecret, client.RawPatch(types.JSONPatchType, []byte(patchStr))); pErr != nil {
				if k8serrors.IsNotFound(pErr) {
					logger.V(1).Info("account removed before the skip validation annotation was removed")
				} else {
					logger.Error(pErr, "failed removing skip annotation from secret")
				}
			}
		}()
	} else {
		if shouldReconcile := v.shouldReconcile(ctx, account); !shouldReconcile {
			return HandleResponse{Continue: true}
		}

		// fetch available runtimes
		availableRuntimes, runtimesErr := getRuntimes(ctx, v.Client, account)
		if runtimesErr != nil {
			msg := "failed to fetch NIM runtimes"

			failedStatus := account.Status
			failedStatus.LastAccountCheck = &metav1.Time{Time: time.Now()}
			meta.SetStatusCondition(&failedStatus.Conditions,
				utils.MakeNimCondition(utils.NimConditionAPIKeyValidation, metav1.ConditionFalse, account.Generation, "ApiKeyNotValidated", msg))
			meta.SetStatusCondition(&failedStatus.Conditions, utils.AccountFailCondition(account.Generation, msg))

			if updateErr := utils.UpdateStatus(ctx, client.ObjectKeyFromObject(account), failedStatus, v.Client); updateErr != nil {
				return HandleResponse{Error: updateErr}
			}
			return HandleResponse{Error: runtimesErr}
		}
		logger.V(1).Info("got NIM runtimes")

		apiKeyStr, kmSErr := v.KeyManager.GetAPIKey(ctx, account)
		if kmSErr != nil {
			if k8serrors.IsNotFound(kmSErr) {
				return HandleResponse{} // don't requeue if the api key secret is not found
			}
			return HandleResponse{Error: kmSErr} // requeue for other errors
		}

		// validate api key
		if err := utils.ValidateApiKey(logger, apiKeyStr, availableRuntimes); err != nil {
			msg := "api key failed validation"
			logger.Info(msg)

			logger.Info(fmt.Sprintf("%s, cleaning up", msg))
			if cleanErr := utils.CleanupResources(ctx, account, v.Client); cleanErr != nil {
				return HandleResponse{Error: cleanErr}
			}

			failedStatus := makeCleanStatusForAccountFailure(account, msg,
				utils.MakeNimCondition(utils.NimConditionAPIKeyValidation, metav1.ConditionFalse, account.Generation, "ApiKeyNotValidated", msg))
			if updateErr := utils.UpdateStatus(ctx, client.ObjectKeyFromObject(account), failedStatus, v.Client); updateErr != nil {
				return HandleResponse{Error: updateErr}
			}

			return HandleResponse{} // don't requeue or continue for invalid api keys
		}
	}
	apiKeyOk := "api key validated successfully"

	successStatus := account.Status
	condUpdated := meta.SetStatusCondition(&successStatus.Conditions,
		utils.MakeNimCondition(utils.NimConditionAPIKeyValidation, metav1.ConditionTrue, account.Generation, "ApiKeyValidated", apiKeyOk))

	if condUpdated {
		successStatus.LastSuccessfulValidation = &metav1.Time{Time: time.Now()}
		if updateErr := utils.UpdateStatus(ctx, client.ObjectKeyFromObject(account), successStatus, v.Client); updateErr != nil {
			return HandleResponse{Error: updateErr}
		}
	}
	logger.Info(apiKeyOk)

	// requeue after status update for successful validation
	return HandleResponse{Requeue: true}
}

func (v *ValidationHandler) shouldReconcile(ctx context.Context, account *v1.Account) bool {
	logger := log.FromContext(ctx)

	apiKeySecret, kmErr := v.KeyManager.GetAPIKeySecret(ctx, account)
	if kmErr != nil {
		return false
	}

	if _, forceValidation := apiKeySecret.Annotations[constants.NimForceValidationAnnotation]; forceValidation {
		logger.Info("forcing validation")
		defer func() {
			logger.V(1).Info("removing force validation annotation")
			sanAnn := strings.ReplaceAll(constants.NimForceValidationAnnotation, "/", "~1")
			patch := fmt.Sprintf("[{\"op\": \"remove\", \"path\": \"/metadata/annotations/%s\"}]", sanAnn)
			if pErr := v.Client.Patch(ctx, apiKeySecret, client.RawPatch(types.JSONPatchType, []byte(patch))); pErr != nil {
				if k8serrors.IsNotFound(pErr) {
					logger.V(1).Info("account removed before the force validation annotation was removed")
				} else {
					logger.Error(pErr, "failed removing force annotation from secret")
				}
			}
		}()
		return true // reconcile if force annotation is set
	}

	validCond := meta.FindStatusCondition(account.Status.Conditions, utils.NimConditionAPIKeyValidation.String())
	if validCond != nil && validCond.Status == metav1.ConditionTrue && account.Status.LastSuccessfulValidation != nil {
		dur, durErr := time.ParseDuration(account.Spec.ValidationRefreshRate)
		if durErr != nil {
			logger.Error(durErr, "failed to parse the validation refresh rate, using default")
			dur = constants.NimValidationRefreshRate
		}
		logger.V(1).Info("evaluating validation refresh rate")
		return time.Since(account.Status.LastSuccessfulValidation.Time) > dur // reconcile based on validation refresh rate
	}

	return true // reconcile first time or if previous reconciliation failed
}
