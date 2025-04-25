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
	"fmt"
	"slices"
	"time"

	templatev1 "github.com/openshift/api/template/v1"
	templatev1client "github.com/openshift/client-go/template/clientset/versioned"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1 "github.com/opendatahub-io/odh-model-controller/api/nim/v1"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/nim/handlers"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
)

// AccountReconciler reconciles an Account object
type AccountReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	KClient        *kubernetes.Clientset
	TemplateClient *templatev1client.Clientset
}

const (
	apiKeySpecPath    = "spec.apiKeySecret.name"
	modelListSpecPath = "spec.modelListConfig.name"
)

// +kubebuilder:rbac:groups=nim.opendatahub.io,resources=accounts,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=nim.opendatahub.io,resources=accounts/status,verbs=get;list;watch;update
// +kubebuilder:rbac:groups=nim.opendatahub.io,resources=accounts/finalizers,verbs=update
// +kubebuilder:rbac:groups=template.openshift.io,resources=templates,verbs=get;list;watch;create;update;delete;patch

func (r *AccountReconciler) SetupWithManager(mgr ctrl.Manager, ctx context.Context) error {
	logger := log.FromContext(ctx, "Setup", "Account Controller")

	if err := mgr.GetFieldIndexer().IndexField(ctx, &v1.Account{}, apiKeySpecPath, func(obj client.Object) []string {
		return []string{obj.(*v1.Account).Spec.APIKeySecret.Name}
	}); err != nil {
		return fmt.Errorf("failed to set apiKey secret cache index: %w", err)
	}

	if err := mgr.GetFieldIndexer().IndexField(ctx, &v1.Account{}, modelListSpecPath, func(obj client.Object) []string {
		var account = obj.(*v1.Account)
		if account.Spec.ModelListConfig != nil && len(account.Spec.ModelListConfig.Name) > 0 {
			return []string{account.Spec.ModelListConfig.Name}
		}
		return []string{}
	}); err != nil {
		return fmt.Errorf("failed to set modelList configmap cache index: %w", err)
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
					logger.Error(err, "failed to fetch accounts from secret")
					return requests
				}
				for _, item := range accounts.Items {
					requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&item)})
				}
				return requests
			})).
		Watches(&corev1.ConfigMap{}, handler.EnqueueRequestsFromMapFunc(
			func(ctx context.Context, obj client.Object) []reconcile.Request {
				var requests []reconcile.Request
				accounts := &v1.AccountList{}
				if err := mgr.GetClient().List(ctx, accounts, client.MatchingFields{modelListSpecPath: obj.GetName()}); err != nil {
					logger.Error(err, "failed to fetch accounts from configmap")
					return requests
				}
				for _, item := range accounts.Items {
					requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&item)})
				}
				return requests
			})).
		Complete(r)
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *AccountReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("Account", req.Name, "namespace", req.Namespace)
	ctx = log.IntoContext(ctx, logger)

	account := &v1.Account{}
	if err := r.Client.Get(ctx, req.NamespacedName, account); err != nil {
		if k8serrors.IsNotFound(err) {
			logger.V(1).Info("account deleted")
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if account.DeletionTimestamp.IsZero() {
		if controllerutil.AddFinalizer(account, constants.NimCleanupFinalizer) {
			if fErr := r.Client.Update(ctx, account); fErr != nil {
				if k8serrors.IsNotFound(fErr) {
					logger.V(1).Info("account removed before the finalizer was added")
				}
				return ctrl.Result{}, client.IgnoreNotFound(fErr)
			}
			logger.V(1).Info("added finalizer to account")
			return ctrl.Result{Requeue: true}, nil
		}
	} else {
		if controllerutil.ContainsFinalizer(account, constants.NimCleanupFinalizer) {
			logger.Info("account being deleted, cleaning up")
			if cleanErr := utils.CleanupResources(ctx, account, r.Client); cleanErr != nil {
				return ctrl.Result{}, cleanErr
			}
			// https://github.com/kubernetes/kubernetes/issues/124347
			// _ = controllerutil.RemoveFinalizer(account, constants.NimCleanupFinalizer)
			// if fErr := r.Client.Update(ctx, account); fErr != nil {
			//	if k8serrors.IsNotFound(fErr) {
			//		logger.V(1).Info("account removed before the finalizer was removed, probably patched manually")
			//	} else {
			//		logger.Error(fErr, "failed to remove account finalizer")
			//	}
			//	return ctrl.Result{}, client.IgnoreNotFound(fErr)
			// } patching as a workaround:
			idx := slices.Index(account.Finalizers, constants.NimCleanupFinalizer)
			patch := fmt.Sprintf("[{\"op\": \"remove\", \"path\": \"/metadata/finalizers/%d\"}]", idx)
			if pErr := r.Patch(ctx, account, client.RawPatch(types.JSONPatchType, []byte(patch))); pErr != nil {
				if k8serrors.IsNotFound(pErr) {
					logger.V(1).Info("account removed before the finalizer was removed, probably patched manually")
				}
				return ctrl.Result{}, client.IgnoreNotFound(pErr)
			}
			logger.V(1).Info("removed finalizer from account")
		}
		return ctrl.Result{}, nil
	}

	logger.Info("starting account reconciliation")

	keyManager := &handlers.APIKeyManager{Client: r.Client}

	validationHandler := &handlers.ValidationHandler{Client: r.Client, KeyManager: keyManager}
	if response := validationHandler.Handle(ctx, account); response.Error != nil {
		return ctrl.Result{}, response.Error
	} else if response.Requeue {
		return ctrl.Result{Requeue: true}, nil
	} else if !response.Continue {
		return ctrl.Result{}, nil
	}
	logger.Info("api key validation not required")

	configMapHandler := &handlers.ConfigMapHandler{Client: r.Client, Scheme: r.Scheme, KubeClient: r.KClient, KeyManager: keyManager}
	if response := configMapHandler.Handle(ctx, account); response.Error != nil {
		return ctrl.Result{}, response.Error
	} else if response.Requeue {
		return ctrl.Result{Requeue: true}, nil
	} else if !response.Continue {
		return ctrl.Result{}, nil
	}
	logger.Info("config refresh not required")

	templateHandler := &handlers.TemplateHandler{Scheme: r.Scheme, Client: r.Client, TemplateClient: r.TemplateClient}
	if response := templateHandler.Handle(ctx, account); response.Error != nil {
		return ctrl.Result{}, response.Error
	} else if response.Requeue {
		return ctrl.Result{Requeue: true}, nil
	} else if !response.Continue {
		return ctrl.Result{}, nil
	}
	logger.Info("template reconciliation not required")

	pullSecretHandler := &handlers.PullSecretHandler{Client: r.Client, Scheme: r.Scheme, KubeClient: r.KClient, KeyManager: keyManager}
	if response := pullSecretHandler.Handle(ctx, account); response.Error != nil {
		return ctrl.Result{}, response.Error
	} else if response.Requeue {
		return ctrl.Result{Requeue: true}, nil
	} else if !response.Continue {
		return ctrl.Result{}, nil
	}
	logger.Info("pull secret reconciliation not required")

	healthyMsg := "the account is healthy"

	accountSuccess := account.Status
	accountSuccess.LastAccountCheck = &metav1.Time{Time: time.Now()}
	meta.SetStatusCondition(&accountSuccess.Conditions,
		utils.MakeNimCondition(utils.NimConditionAccountStatus, metav1.ConditionTrue, account.Generation, "AccountSuccessful", healthyMsg))
	if updateErr := utils.UpdateStatus(ctx, client.ObjectKeyFromObject(account), accountSuccess, r.Client); updateErr != nil {
		return ctrl.Result{}, updateErr
	}

	logger.Info(healthyMsg)
	return ctrl.Result{}, nil
}
