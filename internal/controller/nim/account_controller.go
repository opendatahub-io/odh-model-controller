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

package nim

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	templatev1 "github.com/openshift/api/template/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ssacorev1 "k8s.io/client-go/applyconfigurations/core/v1"
	ssametav1 "k8s.io/client-go/applyconfigurations/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/reference"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1 "github.com/opendatahub-io/odh-model-controller/api/nim/v1"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
)

// AccountReconciler reconciles a Account object
type AccountReconciler struct {
	client.Client
	Scheme  *runtime.Scheme
	KClient kubernetes.Interface
}

const (
	apiKeySpecPath = "spec.apiKeySecret.name"
)

var (
	labels = map[string]string{"opendatahub.io/managed": "true"}
)

// +kubebuilder:rbac:groups=nim.opendatahub.io,resources=accounts,verbs=get;list;watch;update
// +kubebuilder:rbac:groups=nim.opendatahub.io,resources=accounts/status,verbs=get;list;watch;update
// +kubebuilder:rbac:groups=nim.opendatahub.io,resources=accounts/finalizers,verbs=update
// +kubebuilder:rbac:groups=template.openshift.io,resources=templates,verbs=get;list;watch;create;update;delete

func (r *AccountReconciler) SetupWithManager(mgr ctrl.Manager, ctx context.Context) error {
	// TODO: Copied from original main.go... Should it be FromContext?
	logger := ctrl.Log.WithName("controllers").WithName("ModelRegistryInferenceService")

	if err := mgr.GetFieldIndexer().IndexField(ctx, &v1.Account{}, apiKeySpecPath, func(obj client.Object) []string {
		return []string{obj.(*v1.Account).Spec.APIKeySecret.Name}
	}); err != nil {
		logger.Error(err, "failed to set cache index")
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
					logger.Error(err, "failed to fetch accounts")
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

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Account object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.1/pkg/reconcile
func (r *AccountReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("Account", req.Name, "namespace", req.Namespace)
	ctx = log.IntoContext(ctx, logger)

	account := &v1.Account{}
	if err := r.Client.Get(ctx, req.NamespacedName, account); err != nil {
		if k8serrors.IsNotFound(err) {
			logger.V(1).Info("account deleted")
		} else {
			logger.V(1).Error(err, "failed to fetch object")
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !account.DeletionTimestamp.IsZero() {
		logger.V(1).Info("account being deleted")
		return ctrl.Result{}, nil
	}

	logger.V(1).Info("account active")

	initialMsg := "not reconciled yet"
	targetStatus := &v1.AccountStatus{
		Conditions: []metav1.Condition{
			// initial unknown status
			makeAccountUnknownCondition(account.Generation, initialMsg),
			makeApiKeyUnknownCondition(account.Generation, initialMsg),
			makeConfigMapUnknownCondition(account.Generation, initialMsg),
			makeTemplateUnknownCondition(account.Generation, initialMsg),
			makePullSecretUnknownCondition(account.Generation, initialMsg),
		},
	}

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
		var msg string
		if k8serrors.IsNotFound(err) {
			msg = "api key secret not found"
			logger.Info(msg)
		} else {
			msg = "failed to fetch api key secret"
			logger.V(1).Error(err, "failed to fetch api key secret")
		}
		meta.SetStatusCondition(&targetStatus.Conditions, makeAccountFailureCondition(account.Generation, msg))
		r.cleanupResources(ctx, account)
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	foundApiKeySec := "found api key secret"
	logger.V(1).Info(foundApiKeySec)
	meta.SetStatusCondition(&targetStatus.Conditions, makeAccountFailureCondition(account.Generation, foundApiKeySec))

	apiKeyBytes, foundKey := apiKeySecret.Data["api_key"]
	if !foundKey {
		err := fmt.Errorf("secret %+v has no api_key data", apiKeySecretSubject)
		msg := "failed to find api key data in secret"
		logger.V(1).Error(err, msg)
		meta.SetStatusCondition(&targetStatus.Conditions, makeAccountFailureCondition(account.Generation, msg))
		r.cleanupResources(ctx, account)
		return ctrl.Result{}, err
	}
	apiKeyStr := strings.TrimSpace(string(apiKeyBytes))
	gotApiKey := "got api key"
	logger.V(1).Info(gotApiKey)
	meta.SetStatusCondition(&targetStatus.Conditions, makeAccountFailureCondition(account.Generation, gotApiKey))

	// fetch available runtimes
	availableRuntimes, runtimesErr := utils.GetAvailableNimRuntimes()
	if runtimesErr != nil {
		msg := "failed to fetch NIM available custom runtimes"
		logger.V(1).Error(runtimesErr, msg)
		meta.SetStatusCondition(&targetStatus.Conditions, makeAccountFailureCondition(account.Generation, msg))
		r.cleanupResources(ctx, account)
		return ctrl.Result{}, runtimesErr
	}
	runtimesOk := "got custom runtimes"
	logger.V(1).Info(runtimesOk)
	meta.SetStatusCondition(&targetStatus.Conditions, makeAccountFailureCondition(account.Generation, runtimesOk))

	// validate api key
	if err := utils.ValidateApiKey(apiKeyStr, availableRuntimes[0]); err != nil {
		msg := "api key failed validation"
		logger.Error(err, msg)
		meta.SetStatusCondition(&targetStatus.Conditions, makeAccountFailureCondition(account.Generation, msg))
		meta.SetStatusCondition(&targetStatus.Conditions, makeApiKeyFailureCondition(account.Generation, msg))
		r.cleanupResources(ctx, account)
		return ctrl.Result{}, nil
	}
	apiKeyOk := "api key validated successfully"
	logger.V(1).Info(apiKeyOk)
	meta.SetStatusCondition(&targetStatus.Conditions, makeAccountFailureCondition(account.Generation, apiKeyOk))
	meta.SetStatusCondition(&targetStatus.Conditions, makeApiKeySuccessfulCondition(account.Generation, apiKeyOk))

	ownerRefCfg := r.createOwnerReferenceCfg(account)

	// reconcile data configmap
	if cm, err := r.reconcileNimConfig(ctx, ownerRefCfg, account.Namespace, apiKeyStr, availableRuntimes); err != nil {
		msg := "nim configmap reconcile failed"
		logger.V(1).Error(err, msg)
		meta.SetStatusCondition(&targetStatus.Conditions, makeAccountFailureCondition(account.Generation, msg))
		meta.SetStatusCondition(&targetStatus.Conditions, makeConfigMapFailureCondition(account.Generation, msg))
		return ctrl.Result{}, err
	} else {
		ref, refErr := reference.GetReference(r.Scheme, cm)
		if refErr != nil {
			return ctrl.Result{}, refErr
		}
		targetStatus.NIMConfig = ref
	}
	dataCmOk := "data config map reconciled successfully"
	logger.V(1).Info(dataCmOk)
	meta.SetStatusCondition(&targetStatus.Conditions, makeAccountFailureCondition(account.Generation, dataCmOk))
	meta.SetStatusCondition(&targetStatus.Conditions, makeConfigMapSuccessfulCondition(account.Generation, dataCmOk))

	// reconcile template
	if template, err := r.reconcileRuntimeTemplate(ctx, account); err != nil {
		msg := "runtime template reconcile failed"
		logger.V(1).Error(err, msg)
		meta.SetStatusCondition(&targetStatus.Conditions, makeAccountFailureCondition(account.Generation, msg))
		meta.SetStatusCondition(&targetStatus.Conditions, makeTemplateFailureCondition(account.Generation, msg))
		return ctrl.Result{}, err
	} else {
		ref, refErr := reference.GetReference(r.Scheme, template)
		if refErr != nil {
			return ctrl.Result{}, refErr
		}
		targetStatus.RuntimeTemplate = ref
	}
	templateOk := "runtime template reconciled successfully"
	logger.V(1).Info(templateOk)
	meta.SetStatusCondition(&targetStatus.Conditions, makeAccountFailureCondition(account.Generation, templateOk))
	meta.SetStatusCondition(&targetStatus.Conditions, makeTemplateSuccessfulCondition(account.Generation, templateOk))

	// reconcile pull secret
	if pullSecret, err := r.reconcileNimPullSecret(ctx, ownerRefCfg, account.Namespace, apiKeyStr); err != nil {
		msg := "pull secret reconcile failed"
		logger.V(1).Error(err, msg)
		meta.SetStatusCondition(&targetStatus.Conditions, makeAccountFailureCondition(account.Generation, msg))
		meta.SetStatusCondition(&targetStatus.Conditions, makePullSecretFailureCondition(account.Generation, msg))
		return ctrl.Result{}, err
	} else {
		ref, refErr := reference.GetReference(r.Scheme, pullSecret)
		if refErr != nil {
			return ctrl.Result{}, refErr
		}
		targetStatus.NIMPullSecret = ref
	}
	pullSecOk := "pull secret reconciled successfully"
	logger.V(1).Info(pullSecOk)
	meta.SetStatusCondition(&targetStatus.Conditions, makeAccountSuccessfulCondition(account.Generation, "reconciled successfully"))
	meta.SetStatusCondition(&targetStatus.Conditions, makePullSecretSuccessfulCondition(account.Generation, pullSecOk))

	return ctrl.Result{}, nil
}

// reconcileNimConfig is used for reconciling the configmap encapsulating the model data used for constructing inference services
func (r *AccountReconciler) reconcileNimConfig(
	ctx context.Context, ownerCfg *ssametav1.OwnerReferenceApplyConfiguration,
	namespace, apiKey string, runtimes []utils.NimRuntime,
) (*corev1.ConfigMap, error) {
	data, dErr := utils.GetNimModelData(apiKey, runtimes)
	if dErr != nil {
		return nil, dErr
	}

	cmCfg := ssacorev1.ConfigMap(fmt.Sprintf("%s-cm", *ownerCfg.Name), namespace).
		WithData(data).
		WithOwnerReferences(ownerCfg).
		WithLabels(labels)

	cm, err := r.KClient.CoreV1().ConfigMaps(namespace).
		Apply(ctx, cmCfg, metav1.ApplyOptions{FieldManager: constants.NimApplyConfigFieldManager, Force: true})
	if err != nil {
		return nil, err
	}

	return cm, nil
}

// reconcileRuntimeTemplate is used for reconciling the template encapsulating the serving runtime
func (r *AccountReconciler) reconcileRuntimeTemplate(ctx context.Context, account *v1.Account) (*templatev1.Template, error) {
	template := &templatev1.Template{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-template", account.Name),
			Namespace: account.Namespace,
		},
	}

	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, template, func() error {
		if err := controllerutil.SetControllerReference(account, template, r.Scheme); err != nil {
			return err
		}

		template.Annotations = map[string]string{
			"opendatahub.io/apiProtocol":         "REST",
			"opendatahub.io/modelServingSupport": "[\"single\"]",
		}

		template.Labels = labels

		sr, srErr := utils.GetNimServingRuntimeTemplate(r.Scheme)
		if srErr != nil {
			return srErr
		}

		template.Objects = []runtime.RawExtension{
			{Object: sr},
		}
		return nil
	}); err != nil {
		return nil, err
	}

	return template, nil
}

// reconcileNimPullSecret is used to reconcile the pull secret for pulling the custom runtime images
func (r *AccountReconciler) reconcileNimPullSecret(
	ctx context.Context, ownerCfg *ssametav1.OwnerReferenceApplyConfiguration, namespace, apiKey string,
) (*corev1.Secret, error) {
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

	secretCfg := ssacorev1.
		Secret(fmt.Sprintf("%s-pull", *ownerCfg.Name), namespace).
		WithData(map[string][]byte{corev1.DockerConfigJsonKey: credsJson}).
		WithType(corev1.SecretTypeDockerConfigJson).
		WithOwnerReferences(ownerCfg).
		WithLabels(labels)

	secret, err := r.KClient.CoreV1().Secrets(namespace).
		Apply(ctx, secretCfg, metav1.ApplyOptions{FieldManager: constants.NimApplyConfigFieldManager, Force: true})
	if err != nil {
		return nil, err
	}
	return secret, nil
}

// updateStatus is used for fetching an updating the status of the account
func (r *AccountReconciler) updateStatus(ctx context.Context, subject types.NamespacedName, status v1.AccountStatus) {
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

// createOwnerReferenceCfg is used to create an owner reference config to use with server side apply
func (r *AccountReconciler) createOwnerReferenceCfg(account *v1.Account) *ssametav1.OwnerReferenceApplyConfiguration {
	// we fetch the gvk instead of getting the kind and apiversion from the object, because of an alleged envtest bug
	// stripping down all objects typemeta. This is the PR comment discussing this:
	// https://github.com/opendatahub-io/odh-model-controller/pull/289#discussion_r1833811970
	gvk, _ := apiutil.GVKForObject(account, r.Scheme)
	return ssametav1.OwnerReference().
		WithKind(gvk.Kind).
		WithName(account.Name).
		WithAPIVersion(gvk.GroupVersion().String()).
		WithUID(account.GetUID()).
		WithBlockOwnerDeletion(true).
		WithController(true)
}

// cleanupResources is used for deleting the integration related resources (configmap, template, pull secret)
func (r *AccountReconciler) cleanupResources(ctx context.Context, account *v1.Account) {
	logger := log.FromContext(ctx)
	logger.V(1).Info("cleaning up")

	var delObjs []client.Object

	if account.Status.NIMPullSecret != nil {
		delObjs = append(delObjs, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      account.Status.NIMPullSecret.Name,
				Namespace: account.Status.NIMPullSecret.Namespace,
			},
		})
	}

	if account.Status.NIMConfig != nil {
		delObjs = append(delObjs, &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      account.Status.NIMConfig.Name,
				Namespace: account.Status.NIMConfig.Namespace,
			},
		})
	}

	if account.Status.RuntimeTemplate != nil {
		delObjs = append(delObjs, &templatev1.Template{
			ObjectMeta: metav1.ObjectMeta{
				Name:      account.Status.RuntimeTemplate.Name,
				Namespace: account.Status.RuntimeTemplate.Namespace,
			},
		})
	}

	for _, obj := range delObjs {
		if err := r.Client.Delete(ctx, obj); err != nil {
			if !k8serrors.IsNotFound(err) {
				logger.Error(err, fmt.Sprintf("failed to delete %s", obj.GetObjectKind()))
			}
		}
	}

}

// ACCOUNT CONDITIONS

func makeAccountFailureCondition(gen int64, msg string) metav1.Condition {
	return utils.MakeNimCondition(utils.NimConditionAccountStatus, metav1.ConditionFalse, gen, "AccountNotSuccessful", msg)
}

func makeAccountSuccessfulCondition(gen int64, msg string) metav1.Condition {
	return utils.MakeNimCondition(utils.NimConditionAccountStatus, metav1.ConditionTrue, gen, "AccountSuccessful", msg)
}

func makeAccountUnknownCondition(gen int64, msg string) metav1.Condition {
	return utils.MakeNimCondition(utils.NimConditionAccountStatus, metav1.ConditionUnknown, gen, "AccountNotReconciled", msg)
}

// API KEY VALIDATION CONDITIONS

func makeApiKeyFailureCondition(gen int64, msg string) metav1.Condition {
	return utils.MakeNimCondition(utils.NimConditionAPIKeyValidation, metav1.ConditionFalse, gen, "ApiKeyNotValidated", msg)
}

func makeApiKeySuccessfulCondition(gen int64, msg string) metav1.Condition {
	return utils.MakeNimCondition(utils.NimConditionAPIKeyValidation, metav1.ConditionTrue, gen, "ApiKeyValidated", msg)
}

func makeApiKeyUnknownCondition(gen int64, msg string) metav1.Condition {
	return utils.MakeNimCondition(utils.NimConditionAPIKeyValidation, metav1.ConditionUnknown, gen, "ApiKeyNotReconciled", msg)
}

// CONFIGMAP CONDITIONS

func makeConfigMapFailureCondition(gen int64, msg string) metav1.Condition {
	return utils.MakeNimCondition(utils.NimConditionConfigMapUpdate, metav1.ConditionFalse, gen, "ConfigMapNotUpdated", msg)
}

func makeConfigMapSuccessfulCondition(gen int64, msg string) metav1.Condition {
	return utils.MakeNimCondition(utils.NimConditionConfigMapUpdate, metav1.ConditionTrue, gen, "ConfigMapUpdated", msg)
}

func makeConfigMapUnknownCondition(gen int64, msg string) metav1.Condition {
	return utils.MakeNimCondition(utils.NimConditionConfigMapUpdate, metav1.ConditionUnknown, gen, "ConfigMapNotReconciled", msg)
}

// TEMPLATE CONDITIONS

func makeTemplateFailureCondition(gen int64, msg string) metav1.Condition {
	return utils.MakeNimCondition(utils.NimConditionTemplateUpdate, metav1.ConditionFalse, gen, "TemplateNotUpdated", msg)
}

func makeTemplateSuccessfulCondition(gen int64, msg string) metav1.Condition {
	return utils.MakeNimCondition(utils.NimConditionTemplateUpdate, metav1.ConditionTrue, gen, "TemplateUpdated", msg)
}

func makeTemplateUnknownCondition(gen int64, msg string) metav1.Condition {
	return utils.MakeNimCondition(utils.NimConditionTemplateUpdate, metav1.ConditionUnknown, gen, "TemplateNotReconciled", msg)
}

// PULL SECRET CONDITIONS

func makePullSecretFailureCondition(gen int64, msg string) metav1.Condition {
	return utils.MakeNimCondition(utils.NimConditionSecretUpdate, metav1.ConditionFalse, gen, "SecretNotUpdated", msg)
}

func makePullSecretSuccessfulCondition(gen int64, msg string) metav1.Condition {
	return utils.MakeNimCondition(utils.NimConditionSecretUpdate, metav1.ConditionTrue, gen, "SecretUpdated", msg)
}

func makePullSecretUnknownCondition(gen int64, msg string) metav1.Condition {
	return utils.MakeNimCondition(utils.NimConditionSecretUpdate, metav1.ConditionUnknown, gen, "SecretNotReconciled", msg)
}
