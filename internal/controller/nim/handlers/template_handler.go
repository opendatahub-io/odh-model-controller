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
	"slices"
	"testing"
	"time"

	"github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	templatev1 "github.com/openshift/api/template/v1"
	templatev1config "github.com/openshift/client-go/template/applyconfigurations/template/v1"
	templatev1client "github.com/openshift/client-go/template/clientset/versioned"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/tools/reference"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	v1 "github.com/opendatahub-io/odh-model-controller/api/nim/v1"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
)

var _ NimHandler = &TemplateHandler{}

var templateBaseAnnotations = map[string]string{
	"opendatahub.io/apiProtocol":         "REST",
	"opendatahub.io/modelServingSupport": "[\"single\"]",
}

// TemplateHandler is a NimHandler implementation reconciling the template for NIM
type TemplateHandler struct {
	Client         client.Client
	Scheme         *runtime.Scheme
	TemplateClient *templatev1client.Clientset
}

func (t *TemplateHandler) Handle(ctx context.Context, account *v1.Account) HandleResponse {
	logger := log.FromContext(ctx)

	var applyCfg *templatev1config.TemplateApplyConfiguration
	var ref *corev1.ObjectReference
	var err error

	if applyCfg, err = t.getApplyConfig(ctx, account); err == nil {
		var template *templatev1.Template
		if template, err = t.TemplateClient.TemplateV1().Templates(account.Namespace).
			Apply(ctx, applyCfg, metav1.ApplyOptions{FieldManager: constants.NimApplyConfigFieldManager, Force: true}); err == nil {
			ref, err = reference.GetReference(t.Scheme, template)
		}
	}

	if err != nil {
		msg := "runtime template reconcile failed"

		failedStatus := account.Status
		failedStatus.LastAccountCheck = &metav1.Time{Time: time.Now()}
		meta.SetStatusCondition(&failedStatus.Conditions, utils.AccountFailCondition(account.Generation, msg))
		meta.SetStatusCondition(&failedStatus.Conditions,
			utils.MakeNimCondition(utils.NimConditionTemplateUpdate, metav1.ConditionFalse, account.Generation, "TemplateNotUpdated", msg))

		if updateErr := utils.UpdateStatus(ctx, client.ObjectKeyFromObject(account), failedStatus, t.Client); updateErr != nil {
			return HandleResponse{Error: updateErr}
		}
		return HandleResponse{Error: err}
	}

	successStatus := account.Status
	updateRef := shouldUpdateReference(successStatus.RuntimeTemplate, ref)
	if updateRef {
		successStatus.RuntimeTemplate = ref
	}

	templateOk := "runtime template reconciled successfully"
	condUpdated := meta.SetStatusCondition(&successStatus.Conditions,
		utils.MakeNimCondition(utils.NimConditionTemplateUpdate, metav1.ConditionTrue, account.Generation, "TemplateUpdated", templateOk))

	if updateRef || condUpdated {
		if updateErr := utils.UpdateStatus(ctx, client.ObjectKeyFromObject(account), successStatus, t.Client); updateErr != nil {
			return HandleResponse{Error: updateErr}
		}
		logger.Info(templateOk)

		// requeue after status update for successful template reconciliation
		return HandleResponse{Requeue: true}
	}

	return HandleResponse{Continue: true}
}

func (t *TemplateHandler) getApplyConfig(ctx context.Context, account *v1.Account) (*templatev1config.TemplateApplyConfiguration, error) {
	logger := log.FromContext(ctx)

	servingRuntime, srErr := utils.GetNimServingRuntimeTemplate(t.Scheme)
	if srErr != nil {
		return nil, srErr
	}

	defaultCfg := func() *templatev1config.TemplateApplyConfiguration {
		return templatev1config.Template(fmt.Sprintf("%s-template", account.Name), account.Namespace).
			WithOwnerReferences(createOwnerReferenceCfg(account, t.Scheme)).
			WithAnnotations(templateBaseAnnotations).
			WithLabels(commonBaseLabels).
			WithObjects(runtime.RawExtension{Object: servingRuntime})
	}

	if account.Status.RuntimeTemplate == nil {
		logger.V(1).Info("no template reference")
		return defaultCfg(), nil
	}

	var template *templatev1.Template
	var tErr error
	template, tErr = t.TemplateClient.TemplateV1().Templates(account.Status.RuntimeTemplate.Namespace).
		Get(ctx, account.Status.RuntimeTemplate.Name, metav1.GetOptions{})
	if tErr != nil {
		if k8serrors.IsNotFound(tErr) {
			logger.V(1).Info("existing template not found")
		} else {
			logger.V(1).Error(tErr, "failed to fetch template")
		}
		return defaultCfg(), nil
	}

	templateCfg, cfgErr := templatev1config.ExtractTemplate(template, constants.NimApplyConfigFieldManager)
	if cfgErr != nil {
		logger.V(1).Error(cfgErr, "failed to construct initial template config")
		return defaultCfg(), nil
	}

	_, mergedAnnotations := mergeStringMaps(templateBaseAnnotations, template.Annotations)
	_, mergedLabels := mergeStringMaps(commonBaseLabels, template.Labels)
	templateCfg.WithAnnotations(mergedAnnotations).WithLabels(mergedLabels)

	if !meta.IsStatusConditionPresentAndEqual(account.Status.Conditions, utils.NimConditionTemplateUpdate.String(), metav1.ConditionTrue) {
		logger.V(1).Info("previous template not successful")
		templateCfg.WithObjects(runtime.RawExtension{Object: servingRuntime})
		return templateCfg, nil
	}

	expectedGvk, _ := apiutil.GVKForObject(&v1alpha1.ServingRuntime{}, t.Scheme)
	if srExist := slices.ContainsFunc(template.Objects, func(ext runtime.RawExtension) bool {
		if target, dErr := runtime.Decode(serializer.NewCodecFactory(t.Scheme).UniversalDeserializer(), ext.Raw); dErr != nil {
			logger.V(1).Error(dErr, "failed to decode raw template object")
		} else {
			if target.GetObjectKind() != nil && target.GetObjectKind().GroupVersionKind() == expectedGvk {
				return true
			}
		}
		return testing.Testing() // we can't set objects with envtest
	}); !srExist {
		logger.V(1).Info("servingruntime missing from template's objects")
		templateCfg.WithObjects(runtime.RawExtension{Object: servingRuntime})
	}

	return templateCfg, nil
}
