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

var _ NimHandler = &ConfigMapHandler{}

// ConfigMapHandler is a NimHandler implementation reconciling the config for NIM
type ConfigMapHandler struct {
	Client     client.Client
	Scheme     *runtime.Scheme
	KubeClient *kubernetes.Clientset
	KeyManager *APIKeyManager
}

func (c *ConfigMapHandler) Handle(ctx context.Context, account *v1.Account) HandleResponse {
	logger := log.FromContext(ctx)

	if shouldReconcile := c.shouldReconcile(ctx, account); !shouldReconcile {
		return HandleResponse{Continue: true}
	}

	// fetch available runtimes
	availableRuntimes, runtimesErr := utils.GetAvailableNimRuntimes(logger)
	if runtimesErr != nil {
		msg := "failed to fetch NIM available custom runtimes"

		failedStatus := account.Status
		failedStatus.LastAccountCheck = &metav1.Time{Time: time.Now()}
		meta.SetStatusCondition(&failedStatus.Conditions,
			utils.MakeNimCondition(utils.NimConditionConfigMapUpdate, metav1.ConditionFalse, account.Generation, "ConfigMapNotUpdated", msg))
		meta.SetStatusCondition(&failedStatus.Conditions, utils.AccountFailCondition(account.Generation, msg))

		if updateErr := utils.UpdateStatus(ctx, client.ObjectKeyFromObject(account), failedStatus, c.Client); updateErr != nil {
			return HandleResponse{Error: updateErr}
		}
		return HandleResponse{Error: runtimesErr}
	}

	// check the selected models
	if selectedModelList, err := utils.GetSelectedModelList(ctx, account.Spec.ModelListConfig, account.Namespace, c.Client, logger); err != nil {
		logger.V(1).Error(err, "failed to get the selected model list")
		return HandleResponse{Error: err}
	} else {
		availableRuntimes = utils.FilterAvailableNimRuntimes(availableRuntimes, selectedModelList, logger)
	}

	var applyCfg *ssacorev1.ConfigMapApplyConfiguration
	var ref *corev1.ObjectReference
	var err error

	if applyCfg, err = c.getApplyConfig(ctx, account, availableRuntimes); err == nil {
		var cm *corev1.ConfigMap
		if cm, err = c.KubeClient.CoreV1().ConfigMaps(account.Namespace).
			Apply(ctx, applyCfg, metav1.ApplyOptions{FieldManager: constants.NimApplyConfigFieldManager, Force: true}); err == nil {
			ref, err = reference.GetReference(c.Scheme, cm)
		}
	}

	if err != nil {
		msg := "nim config reconciliation failed"

		failedStatus := account.Status
		failedStatus.LastAccountCheck = &metav1.Time{Time: time.Now()}
		meta.SetStatusCondition(&failedStatus.Conditions, utils.AccountFailCondition(account.Generation, msg))
		meta.SetStatusCondition(&failedStatus.Conditions,
			utils.MakeNimCondition(utils.NimConditionConfigMapUpdate, metav1.ConditionFalse, account.Generation, "ConfigMapNotUpdated", msg))

		if updateErr := utils.UpdateStatus(ctx, client.ObjectKeyFromObject(account), failedStatus, c.Client); updateErr != nil {
			return HandleResponse{Error: updateErr}
		}
		return HandleResponse{Error: err}
	}

	successStatus := account.Status
	successStatus.NIMConfig = ref

	dataCmOk := "nim config reconciled successfully"
	successStatus.LastSuccessfulConfigRefresh = &metav1.Time{Time: time.Now()}
	meta.SetStatusCondition(&successStatus.Conditions,
		utils.MakeNimCondition(utils.NimConditionConfigMapUpdate, metav1.ConditionTrue, account.Generation, "ConfigMapUpdated", dataCmOk))

	if updateErr := utils.UpdateStatus(ctx, client.ObjectKeyFromObject(account), successStatus, c.Client); updateErr != nil {
		return HandleResponse{Error: updateErr}
	}
	logger.Info(dataCmOk)

	// requeue after status update for successful configs refresh
	return HandleResponse{Requeue: true}
}

func (c *ConfigMapHandler) getApplyConfig(ctx context.Context, account *v1.Account, availableRuntimes []utils.NimRuntime) (*ssacorev1.ConfigMapApplyConfiguration, error) {
	logger := log.FromContext(ctx)

	apiKeyStr, kmErr := c.KeyManager.GetAPIKey(ctx, account)
	if kmErr != nil {
		return nil, kmErr
	}

	data, dErr := utils.GetNimModelData(logger, apiKeyStr, availableRuntimes)
	if dErr != nil {
		return nil, dErr
	}

	defaultCfg := func() *ssacorev1.ConfigMapApplyConfiguration {
		return ssacorev1.ConfigMap(fmt.Sprintf("%s-cm", account.Name), account.Namespace).
			WithOwnerReferences(createOwnerReferenceCfg(account, c.Scheme)).
			WithLabels(commonBaseLabels).
			WithData(data)
	}

	if account.Status.NIMConfig == nil {
		logger.V(1).Info("no configmap reference")
		return defaultCfg(), nil
	}

	var cm *corev1.ConfigMap
	var cErr error
	cm, cErr = c.KubeClient.CoreV1().ConfigMaps(account.Status.NIMConfig.Namespace).
		Get(ctx, account.Status.NIMConfig.Name, metav1.GetOptions{})
	if cErr != nil {
		if k8serrors.IsNotFound(cErr) {
			logger.V(1).Info("existing configmap not found")
		} else {
			logger.V(1).Error(cErr, "failed to fetch configmap")
		}
		return defaultCfg(), nil
	}

	cmCfg, cfgErr := ssacorev1.ExtractConfigMap(cm, constants.NimApplyConfigFieldManager)
	if cfgErr != nil {
		logger.V(1).Error(cfgErr, "failed to construct initial configmap config")
		return defaultCfg(), nil
	}

	_, mergedLabels := mergeStringMaps(commonBaseLabels, cm.Labels)
	cmCfg.WithLabels(mergedLabels)
	// overwrite the data completely including the keys
	cmCfg.Data = data

	return cmCfg, nil
}

func (c *ConfigMapHandler) shouldReconcile(ctx context.Context, account *v1.Account) bool {
	logger := log.FromContext(ctx)

	validCond := meta.FindStatusCondition(account.Status.Conditions, utils.NimConditionAPIKeyValidation.String())
	if validCond == nil || validCond.Status != metav1.ConditionTrue {
		logger.Info("refusing to refresh configmap for invalidated api key")
		return false
	}

	if account.Status.NIMConfig == nil {
		logger.V(1).Info("no configmap reference")
		return true
	}

	existingMeta := &metav1.PartialObjectMetadata{}
	existingMeta.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("configmap"))
	if err := c.Client.Get(ctx, utils.ObjectKeyFromReference(account.Status.NIMConfig), existingMeta); err != nil {
		if k8serrors.IsNotFound(err) {
			logger.V(1).Info("existing configmap not found")
		} else {
			logger.V(1).Error(err, "failed to fetch existing configmap for reconciliation")
		}
		return true
	}

	cmCond := meta.FindStatusCondition(account.Status.Conditions, utils.NimConditionConfigMapUpdate.String())
	if cmCond != nil && cmCond.Status == metav1.ConditionTrue && account.Status.LastSuccessfulConfigRefresh != nil {
		if account.Status.LastSuccessfulValidation.After(account.Status.LastSuccessfulConfigRefresh.Time) {
			logger.V(1).Info("api key was validated recently")
			return true
		}

		dur, durErr := time.ParseDuration(account.Spec.NIMConfigRefreshRate)
		if durErr != nil {
			logger.V(1).Error(durErr, "failed to parse config refresh rate, using default")
			dur = constants.NimConfigRefreshRate
		}

		shouldReconcileDuration := time.Since(account.Status.LastSuccessfulConfigRefresh.Time) > dur
		if shouldReconcileDuration {
			logger.V(1).Info("evaluating configmap refresh rate")
			return true
		}

		shouldReconcileLabels, _ := mergeStringMaps(commonBaseLabels, existingMeta.Labels)
		if shouldReconcileLabels {
			logger.V(1).Info("existing configmap is missing required labels")
		}
		return shouldReconcileLabels
	}

	logger.V(1).Info("previous configmap reconciliation failed or didn't occur")
	return true
}
