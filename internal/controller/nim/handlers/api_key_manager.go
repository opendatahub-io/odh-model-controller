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
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/hashicorp/go-multierror"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	v1 "github.com/opendatahub-io/odh-model-controller/api/nim/v1"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
)

// APIKeyManager is used for maintaining a lazy singleton for the api key
type APIKeyManager struct {
	Client            client.Client
	_apiKeyStr        string
	_apiKeyStrOnce    sync.Once
	_apiKeySecret     *corev1.Secret
	_apiKeySecretOnce sync.Once
	_errs             *multierror.Error
}

// GetAPIKey will return the api key string
func (a *APIKeyManager) GetAPIKey(ctx context.Context, account *v1.Account) (string, error) {
	a._apiKeySecretOnce.Do(a.loadSecret(ctx, account))
	if a._errs.ErrorOrNil() == nil {
		a._apiKeyStrOnce.Do(a.loadKey(ctx, account))
	}
	return a._apiKeyStr, a._errs.ErrorOrNil()
}

// GetAPIKeySecret will return the api key Secret object
func (a *APIKeyManager) GetAPIKeySecret(ctx context.Context, account *v1.Account) (*corev1.Secret, error) {
	a._apiKeySecretOnce.Do(a.loadSecret(ctx, account))
	return a._apiKeySecret, a._errs.ErrorOrNil()
}

func (a *APIKeyManager) loadKey(ctx context.Context, account *v1.Account) func() {
	return func() {
		if a._apiKeySecret == nil {
			a._errs = multierror.Append(a._errs, errors.New("api key secret not fetched yet"))
			return
		}
		logger := log.FromContext(ctx)

		// extract api key from secret
		apiKeyBytes, foundKey := a._apiKeySecret.Data["api_key"]
		if !foundKey {
			msg := "failed to find api key data in secret"
			a._errs = multierror.Append(a._errs, fmt.Errorf("secret %+v has no api_key data", a._apiKeySecret.Name))

			logger.Info(fmt.Sprintf("%s, cleaning up", msg))
			if cleanErr := utils.CleanupResources(ctx, account, a.Client); cleanErr != nil {
				a._errs = multierror.Append(a._errs, cleanErr)
			}

			failedStatus := makeCleanStatusForAccountFailure(account, msg)
			if updateErr := utils.UpdateStatus(ctx, client.ObjectKeyFromObject(account), failedStatus, a.Client); updateErr != nil {
				a._errs = multierror.Append(a._errs, updateErr)
			}
		} else {
			a._apiKeyStr = strings.TrimSpace(string(apiKeyBytes))
			logger.V(1).Info("got api key")
		}
	}
}

func (a *APIKeyManager) loadSecret(ctx context.Context, account *v1.Account) func() {
	return func() {
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
			} else {
				msg = "failed to fetch api key secret"
				a._errs = multierror.Append(a._errs, err)
			}

			logger.Info(fmt.Sprintf("%s, cleaning up", msg))
			if cleanErr := utils.CleanupResources(ctx, account, a.Client); cleanErr != nil {
				a._errs = multierror.Append(a._errs, cleanErr)
			}

			failedStatus := makeCleanStatusForAccountFailure(account, msg)
			if updateErr := utils.UpdateStatus(ctx, client.ObjectKeyFromObject(account), failedStatus, a.Client); updateErr != nil {
				a._errs = multierror.Append(a._errs, updateErr)
			}
			a._errs = multierror.Append(a._errs, err)
		} else {
			a._apiKeySecret = apiKeySecret
			logger.V(1).Info("got api key secret")
		}
	}
}
