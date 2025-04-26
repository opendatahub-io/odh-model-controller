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
	"encoding/json"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ssacorev1 "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/reference"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	v1 "github.com/opendatahub-io/odh-model-controller/api/nim/v1"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
)

var _ NimHandler = &PullSecretHandler{}

// PullSecretHandler is a NimHandler implementation reconciling the pull secret for NIM
type PullSecretHandler struct {
	Client     client.Client
	Scheme     *runtime.Scheme
	KubeClient *kubernetes.Clientset
	KeyManager *APIKeyManager
}

func (p *PullSecretHandler) Handle(ctx context.Context, account *v1.Account) HandleResponse {
	logger := log.FromContext(ctx)

	var applyCfg *ssacorev1.SecretApplyConfiguration
	var ref *corev1.ObjectReference
	var err error

	if applyCfg, err = p.getApplyConfig(ctx, account); err == nil {
		var secret *corev1.Secret
		if secret, err = p.KubeClient.CoreV1().Secrets(account.Namespace).
			Apply(ctx, applyCfg, metav1.ApplyOptions{FieldManager: constants.NimApplyConfigFieldManager, Force: true}); err == nil {
			ref, err = reference.GetReference(p.Scheme, secret)
		}
	}

	if err != nil {
		msg := "pull secret reconcile failed"

		failedStatus := account.Status
		failedStatus.LastAccountCheck = &metav1.Time{Time: time.Now()}
		meta.SetStatusCondition(&failedStatus.Conditions, utils.AccountFailCondition(account.Generation, msg))
		meta.SetStatusCondition(&failedStatus.Conditions,
			utils.MakeNimCondition(utils.NimConditionSecretUpdate, metav1.ConditionFalse, account.Generation, "SecretNotUpdated", msg))

		if updateErr := utils.UpdateStatus(ctx, client.ObjectKeyFromObject(account), failedStatus, p.Client); updateErr != nil {
			return HandleResponse{Error: updateErr}
		}
		return HandleResponse{Error: err}
	}

	successStatus := account.Status
	updateRef := shouldUpdateReference(successStatus.NIMPullSecret, ref)
	if updateRef {
		successStatus.NIMPullSecret = ref
	}

	pullSecOk := "pull secret reconciled successfully"
	condUpdated := meta.SetStatusCondition(&successStatus.Conditions,
		utils.MakeNimCondition(utils.NimConditionSecretUpdate, metav1.ConditionTrue, account.Generation, "SecretUpdated", pullSecOk))

	if updateRef || condUpdated {
		if updateErr := utils.UpdateStatus(ctx, client.ObjectKeyFromObject(account), successStatus, p.Client); updateErr != nil {
			return HandleResponse{Error: updateErr}
		}
		logger.Info(pullSecOk)

		// requeue after status update for successful pull secret reconciliation
		return HandleResponse{Requeue: true}
	}

	return HandleResponse{Continue: true}
}

func (p *PullSecretHandler) getApplyConfig(ctx context.Context, account *v1.Account) (*ssacorev1.SecretApplyConfiguration, error) {
	logger := log.FromContext(ctx)

	apiKeyStr, kmErr := p.KeyManager.GetAPIKey(ctx, account)
	if kmErr != nil {
		return nil, kmErr
	}

	data, dErr := GetPullSecretData(apiKeyStr)
	if dErr != nil {
		return nil, dErr
	}

	defaultCfg := func() *ssacorev1.SecretApplyConfiguration {
		return ssacorev1.Secret(fmt.Sprintf("%s-pull", account.Name), account.Namespace).
			WithType(corev1.SecretTypeDockerConfigJson).
			WithOwnerReferences(createOwnerReferenceCfg(account, p.Scheme)).
			WithLabels(commonBaseLabels).
			WithData(data)
	}

	if account.Status.NIMPullSecret == nil {
		logger.V(1).Info("no pull secret reference")
		return defaultCfg(), nil
	}

	var secret *corev1.Secret
	var sErr error

	secret, sErr = p.KubeClient.CoreV1().Secrets(account.Status.NIMPullSecret.Namespace).
		Get(ctx, account.Status.NIMPullSecret.Name, metav1.GetOptions{})
	if sErr != nil {
		if k8serrors.IsNotFound(sErr) {
			logger.V(1).Info("existing pull secret not found")
		} else {
			logger.V(1).Error(sErr, "failed to fetch pull secret")
		}
		return defaultCfg(), nil
	}

	secretCfg, cfgErr := ssacorev1.ExtractSecret(secret, constants.NimApplyConfigFieldManager)
	if cfgErr != nil {
		logger.V(1).Error(cfgErr, "failed to create initial pull secret config")
		return defaultCfg(), nil
	}

	_, mergedLabels := mergeStringMaps(commonBaseLabels, secret.Labels)
	secretCfg.WithLabels(mergedLabels).WithData(data)

	return secretCfg, nil
}

func GetPullSecretData(apiKey string) (map[string][]byte, error) {
	creds := map[string]map[string]map[string]string{
		"auths": {
			"nvcr.io": {
				"username": "$oauthtoken",
				"password": apiKey,
			},
		},
	}

	credsJson, marshErr := json.Marshal(creds)
	if marshErr != nil {
		return nil, marshErr
	}

	return map[string][]byte{corev1.DockerConfigJsonKey: credsJson}, nil
}
