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

package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/go-logr/logr"
	"github.com/kuadrant/authorino/pkg/log"
	"github.com/opendatahub-io/odh-model-controller/api/nim/v1"
	"github.com/opendatahub-io/odh-model-controller/controllers/constants"
	"github.com/opendatahub-io/odh-model-controller/controllers/utils"
	templatev1 "github.com/openshift/api/template/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/reference"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type (
	NimAccountReconciler struct {
		client.Client
		Log logr.Logger
	}
)

const (
	apiKeySpecPath = "spec.apiKeySecret.name"
)

func (r *NimAccountReconciler) SetupWithManager(mgr ctrl.Manager, ctx context.Context) error {
	if err := mgr.GetFieldIndexer().IndexField(ctx, &v1.Account{}, apiKeySpecPath, func(obj client.Object) []string {
		return []string{obj.(*v1.Account).Spec.APIKeySecret.Name}
	}); err != nil {
		r.Log.Error(err, "failed to set cache index")
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).
		Named("odh-nim-controller").
		For(&v1.Account{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.Secret{}).
		Owns(&templatev1.Template{}).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(
			func(ctx context.Context, obj client.Object) []reconcile.Request {
				var requests []reconcile.Request
				accounts := &v1.AccountList{}
				if err := mgr.GetClient().List(ctx, accounts, client.MatchingFields{apiKeySpecPath: obj.GetName()}); err != nil {
					r.Log.Error(err, "failed to fetch accounts")
					return requests
				}
				for _, item := range accounts.Items {
					requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{
						Name:      item.Name,
						Namespace: item.Namespace,
					}})
				}
				return requests
			})).
		Complete(r)
}

func (r *NimAccountReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := r.Log.WithValues("Account", req.Name, "namespace", req.Namespace)
	ctx = log.IntoContext(ctx, logger)

	account := &v1.Account{}
	if err := r.Client.Get(ctx, req.NamespacedName, account); err != nil {
		if k8serrors.IsNotFound(err) {
			logger.V(1).Info("account deleted")
			r.cleanupResources(ctx, req.Namespace)
		} else {
			logger.V(1).Error(err, "failed to fetch object")
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !account.DeletionTimestamp.IsZero() {
		logger.V(1).Info("account being deleted, cleanups done")
		return ctrl.Result{}, nil
	}

	logger.V(1).Info("account active")

	targetStatus := account.Status.DeepCopy()

	// initial account status condition and set initial api key status condition
	meta.SetStatusCondition(&targetStatus.Conditions, makeAccountFailureCondition(account.Generation, "api key secret not available"))
	meta.SetStatusCondition(&targetStatus.Conditions, makeApiKeyFailureCondition(account.Generation, "api key secret not available"))
	// initial unknown for the resources conditions
	meta.SetStatusCondition(&targetStatus.Conditions, makeConfigMapUnknownCondition(account.Generation, "not reconciled yet"))
	meta.SetStatusCondition(&targetStatus.Conditions, makeTemplateUnknownCondition(account.Generation, "not reconciled yet"))
	meta.SetStatusCondition(&targetStatus.Conditions, makePullSecretUnknownCondition(account.Generation, "not reconciled yet"))

	defer func() {
		r.updateStatus(ctx, req.NamespacedName, *targetStatus)
	}()

	// fetch api secret
	secretNs := account.Spec.APIKeySecret.Namespace
	if secretNs == "" {
		secretNs = account.Namespace
	}
	apiKeySecret := &corev1.Secret{}
	apiKeySecretSubject := types.NamespacedName{Name: account.Spec.APIKeySecret.Name, Namespace: secretNs}
	if err := r.Client.Get(ctx, apiKeySecretSubject, apiKeySecret); err != nil {
		if k8serrors.IsNotFound(err) {
			msg := "api key secret was deleted"
			logger.Info(msg)
			// mark all status conditions for failure
			meta.SetStatusCondition(&targetStatus.Conditions, makeAccountFailureCondition(account.Generation, msg))
			meta.SetStatusCondition(&targetStatus.Conditions, makeApiKeyFailureCondition(account.Generation, msg))
			meta.SetStatusCondition(&targetStatus.Conditions, makeConfigMapFailureCondition(account.Generation, msg))
			meta.SetStatusCondition(&targetStatus.Conditions, makeTemplateFailureCondition(account.Generation, msg))
			meta.SetStatusCondition(&targetStatus.Conditions, makePullSecretFailureCondition(account.Generation, msg))

			r.cleanupResources(ctx, account.Namespace)
		} else {
			logger.V(1).Error(err, "failed to fetch api key secret")
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	apiKeyBytes, foundKey := apiKeySecret.Data["api_key"]
	if !foundKey {
		err := fmt.Errorf("secret %+v has no api_key data", apiKeySecretSubject)
		logger.V(1).Error(err, "failed to find api key data in secret")
		return ctrl.Result{}, err
	}

	apiKeyStr := string(apiKeyBytes)
	logger.V(1).Info("got api key")

	availableRuntimes, runtimesErr := utils.GetAvailableNimRuntimes()
	if runtimesErr != nil {
		logger.V(1).Error(runtimesErr, "failed to fetch NIM available custom runtimes")
		return ctrl.Result{}, runtimesErr
	}
	logger.V(1).Info("got custom runtimes")

	if err := utils.ValidateApiKey(apiKeyStr, availableRuntimes[0]); err != nil {
		logger.Error(err, "api key failed validation")
		return ctrl.Result{}, nil
	}
	logger.V(1).Info("api key validated successfully")

	// set successful api key status condition
	meta.SetStatusCondition(&targetStatus.Conditions, makeApiKeySuccessfulCondition(account.Generation, ""))
	// progress account status condition and set initial configmap status condition
	meta.SetStatusCondition(&targetStatus.Conditions, makeAccountFailureCondition(account.Generation, "configmap not reconciled"))
	meta.SetStatusCondition(&targetStatus.Conditions, makeConfigMapFailureCondition(account.Generation, "configmap not reconciled"))

	if cmap, err := r.reconcileNimConfig(ctx, account, apiKeyStr, availableRuntimes); err != nil {
		logger.V(1).Error(err, "nim configmap reconcile failed")
		return ctrl.Result{}, err
	} else {
		ref, refErr := reference.GetReference(r.Scheme(), cmap)
		if refErr != nil {
			return ctrl.Result{}, refErr
		}
		targetStatus.NIMConfig = ref
	}
	logger.V(1).Info("data config map reconciled successfully")

	// set successful configmap status condition
	meta.SetStatusCondition(&targetStatus.Conditions, makeConfigMapSuccessfulCondition(account.Generation, ""))
	// progress account status condition and set initial template status condition
	meta.SetStatusCondition(&targetStatus.Conditions, makeAccountFailureCondition(account.Generation, "template not reconciled"))
	meta.SetStatusCondition(&targetStatus.Conditions, makeTemplateFailureCondition(account.Generation, "template not reconciled"))

	if template, err := r.reconcileRuntimeTemplate(ctx, account); err != nil {
		logger.V(1).Error(err, "runtime template reconcile failed")
		return ctrl.Result{}, err
	} else {
		ref, refErr := reference.GetReference(r.Scheme(), template)
		if refErr != nil {
			return ctrl.Result{}, refErr
		}
		targetStatus.RuntimeTemplate = ref
	}
	logger.V(1).Info("runtime template reconciled successfully")

	// set successful template status condition
	meta.SetStatusCondition(&targetStatus.Conditions, makeTemplateSuccessfulCondition(account.Generation, ""))
	// progress account status condition and set initial pull secret status condition
	meta.SetStatusCondition(&targetStatus.Conditions, makeAccountFailureCondition(account.Generation, "pull secret not reconciled"))
	meta.SetStatusCondition(&targetStatus.Conditions, makePullSecretFailureCondition(account.Generation, "pull secret not reconciled"))

	if pullSecret, err := r.reconcileNimPullSecret(ctx, account, apiKeyStr); err != nil {
		logger.V(1).Error(err, "pull secret reconcile failed")
		return ctrl.Result{}, err
	} else {
		ref, refErr := reference.GetReference(r.Scheme(), pullSecret)
		if refErr != nil {
			return ctrl.Result{}, refErr
		}
		targetStatus.NIMPullSecret = ref
	}
	logger.V(1).Info("pull secret reconciled successfully")

	// set successful account and pull secret status conditions
	meta.SetStatusCondition(&targetStatus.Conditions, makeAccountSuccessfulCondition(account.Generation, ""))
	meta.SetStatusCondition(&targetStatus.Conditions, makePullSecretSuccessfulCondition(account.Generation, ""))

	return ctrl.Result{}, nil
}

func (r *NimAccountReconciler) reconcileNimConfig(ctx context.Context, account *v1.Account, apiKey string, runtimes []utils.NimRuntime) (*corev1.ConfigMap, error) {
	cmap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      constants.NimDataConfigMapName,
			Namespace: account.Namespace,
		},
	}

	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, cmap, func() error {
		if err := controllerutil.SetControllerReference(account, cmap, r.Scheme()); err != nil {
			return err
		}

		if data, err := utils.GetNimModelData(apiKey, runtimes); err != nil {
			return err
		} else {
			cmap.Data = data
		}

		return nil
	}); err != nil {
		return nil, err
	}

	return cmap, nil
}

func (r *NimAccountReconciler) reconcileRuntimeTemplate(ctx context.Context, account *v1.Account) (*templatev1.Template, error) {
	template := &templatev1.Template{
		ObjectMeta: metav1.ObjectMeta{
			Name:      constants.NimRuntimeTemplateName,
			Namespace: account.Namespace,
		},
	}

	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, template, func() error {
		if err := controllerutil.SetControllerReference(account, template, r.Scheme()); err != nil {
			return err
		}

		template.Annotations = map[string]string{
			"opendatahub.io/apiProtocol":         "REST",
			"opendatahub.io/modelServingSupport": "[\"single\"]",
		}

		template.Objects = []runtime.RawExtension{
			{Object: utils.GetNimServingRuntimeTemplate()},
		}
		return nil
	}); err != nil {
		return nil, err
	}

	return template, nil
}

func (r *NimAccountReconciler) reconcileNimPullSecret(ctx context.Context, account *v1.Account, apiKey string) (*corev1.Secret, error) {
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

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      constants.NimPullSecretName,
			Namespace: account.Namespace,
		},
	}

	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		if err := controllerutil.SetControllerReference(account, secret, r.Scheme()); err != nil {
			return err
		}

		secret.Data = map[string][]byte{
			".dockercfg": credsJson,
		}
		secret.Type = corev1.SecretTypeDockercfg
		return nil
	}); err != nil {
		return nil, err
	}

	return secret, nil
}

// updateStatus is used for fetching an updating the status of the account
func (r *NimAccountReconciler) updateStatus(ctx context.Context, subject types.NamespacedName, status v1.AccountStatus) {
	logger := log.FromContext(ctx)
	logger.V(1).Info("updating status")

	account := &v1.Account{}
	if err := r.Client.Get(ctx, subject, account); err != nil {
		if !k8serrors.IsNotFound(err) {
			logger.Error(err, "failed to fetch account for status update")
		}
	} else {
		account.Status = *status.DeepCopy()
		if err = r.Client.Status().Update(ctx, account); err != nil {
			logger.Error(err, "failed to update account status")
		}
	}
}

// cleanupResources is used for deleting the integration related resources (configmap, template, pull secret)
func (r *NimAccountReconciler) cleanupResources(ctx context.Context, namespace string) {
	logger := log.FromContext(ctx)
	logger.V(1).Info("cleaning up")
	delObjs := []client.Object{
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      constants.NimPullSecretName,
				Namespace: namespace,
			}},
		&templatev1.Template{
			ObjectMeta: metav1.ObjectMeta{
				Name:      constants.NimRuntimeTemplateName,
				Namespace: namespace,
			}},
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      constants.NimDataConfigMapName,
				Namespace: namespace,
			}},
	}

	for _, obj := range delObjs {
		if err := r.Client.Delete(ctx, obj); err != nil {
			if !k8serrors.IsNotFound(err) {
				logger.Error(err, fmt.Sprintf("failed to delete %s", obj.GetObjectKind()))
			}
		}
	}
}

func makeAccountFailureCondition(gen int64, msg string) metav1.Condition {
	return utils.MakeNimCondition(utils.NimConditionAccountStatus, metav1.ConditionFalse, gen, "AccountNotSuccessful", msg)
}

func makeAccountSuccessfulCondition(gen int64, msg string) metav1.Condition {
	return utils.MakeNimCondition(utils.NimConditionAccountStatus, metav1.ConditionTrue, gen, "AccountSuccessful", msg)
}

func makeApiKeyFailureCondition(gen int64, msg string) metav1.Condition {
	return utils.MakeNimCondition(utils.NimConditionAPIKeyValidation, metav1.ConditionFalse, gen, "ApiKeyNotValidated", msg)
}

func makeApiKeySuccessfulCondition(gen int64, msg string) metav1.Condition {
	return utils.MakeNimCondition(utils.NimConditionAPIKeyValidation, metav1.ConditionTrue, gen, "ApiKeyValidated", msg)
}

func makeConfigMapFailureCondition(gen int64, msg string) metav1.Condition {
	return utils.MakeNimCondition(utils.NimConditionConfigMapUpdate, metav1.ConditionFalse, gen, "ConfigMapNotUpdated", msg)
}

func makeConfigMapSuccessfulCondition(gen int64, msg string) metav1.Condition {
	return utils.MakeNimCondition(utils.NimConditionConfigMapUpdate, metav1.ConditionTrue, gen, "ConfigMapUpdated", msg)
}

func makeConfigMapUnknownCondition(gen int64, msg string) metav1.Condition {
	return utils.MakeNimCondition(utils.NimConditionConfigMapUpdate, metav1.ConditionUnknown, gen, "ConfigMapUpdated", msg)
}

func makeTemplateFailureCondition(gen int64, msg string) metav1.Condition {
	return utils.MakeNimCondition(utils.NimConditionTemplateUpdate, metav1.ConditionFalse, gen, "TemplateNotUpdated", msg)
}

func makeTemplateSuccessfulCondition(gen int64, msg string) metav1.Condition {
	return utils.MakeNimCondition(utils.NimConditionTemplateUpdate, metav1.ConditionTrue, gen, "TemplateUpdated", msg)
}

func makeTemplateUnknownCondition(gen int64, msg string) metav1.Condition {
	return utils.MakeNimCondition(utils.NimConditionTemplateUpdate, metav1.ConditionUnknown, gen, "TemplateUpdated", msg)
}

func makePullSecretFailureCondition(gen int64, msg string) metav1.Condition {
	return utils.MakeNimCondition(utils.NimConditionSecretUpdate, metav1.ConditionFalse, gen, "SecretNotUpdated", msg)
}

func makePullSecretSuccessfulCondition(gen int64, msg string) metav1.Condition {
	return utils.MakeNimCondition(utils.NimConditionSecretUpdate, metav1.ConditionTrue, gen, "SecretUpdated", msg)
}

func makePullSecretUnknownCondition(gen int64, msg string) metav1.Condition {
	return utils.MakeNimCondition(utils.NimConditionSecretUpdate, metav1.ConditionUnknown, gen, "SecretUpdated", msg)
}
