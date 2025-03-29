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
	"sync"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	v1 "github.com/opendatahub-io/odh-model-controller/api/nim/v1"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
)

type APIKeyManager struct {
	Client       client.Client
	apiKeyStr    string
	apiKeySecret *corev1.Secret
}

func (a *APIKeyManager) APIKeyString(ctx context.Context, account *v1.Account) (string, error) {
	err := a.loadApiKey(ctx, account)()
	return a.apiKeyStr, err
}

func (a *APIKeyManager) APIKeySecret(ctx context.Context, account *v1.Account) (*corev1.Secret, error) {
	err := a.loadApiKey(ctx, account)()
	return a.apiKeySecret, err
}

func (a *APIKeyManager) loadApiKey(ctx context.Context, account *v1.Account) func() error {
	return sync.OnceValue(func() error {
		logger := log.FromContext(ctx)

		// fetch api secret
		secretNs := account.Spec.APIKeySecret.Namespace
		if secretNs == "" {
			secretNs = account.Namespace
		}
		apiKeySecret := &corev1.Secret{}
		apiKeySecretSubject := types.NamespacedName{Name: account.Spec.APIKeySecret.Name, Namespace: secretNs}
		if err := a.Client.Get(ctx, apiKeySecretSubject, apiKeySecret); err != nil {
			var msg string
			if k8serrors.IsNotFound(err) {
				msg = "api key secret not found"
				logger.Info(msg)
			} else {
				msg = "failed to fetch api key secret"
			}

			logger.Info(fmt.Sprintf("%s, cleaning up", msg))
			if cleanErr := utils.CleanupResources(ctx, account, a.Client); cleanErr != nil {
				return cleanErr
			}

			failedStatus := makeCleanStatusForAccountFailure(account, msg)
			if updateErr := utils.UpdateStatus(ctx, client.ObjectKeyFromObject(account), failedStatus, a.Client); updateErr != nil {
				return updateErr
			}

			return err
		}

		a.apiKeySecret = apiKeySecret

		// extract api key from secret
		apiKeyBytes, foundKey := apiKeySecret.Data["api_key"]
		if !foundKey {
			msg := "failed to find api key data in secret"
			logger.Info(fmt.Sprintf("%s, cleaning up", msg))
			if cleanErr := utils.CleanupResources(ctx, account, a.Client); cleanErr != nil {
				return cleanErr
			}

			failedStatus := makeCleanStatusForAccountFailure(account, msg)
			if updateErr := utils.UpdateStatus(ctx, client.ObjectKeyFromObject(account), failedStatus, a.Client); updateErr != nil {
				return updateErr
			}

			return fmt.Errorf("secret %+v has no api_key data", apiKeySecretSubject)
		}

		a.apiKeyStr = strings.TrimSpace(string(apiKeyBytes))
		logger.V(1).Info("got api key")
		return nil
	})
}
