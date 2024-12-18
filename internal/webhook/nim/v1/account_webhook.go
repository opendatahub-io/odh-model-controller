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

package v1

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	nimv1 "github.com/opendatahub-io/odh-model-controller/api/nim/v1"
)

// nolint:unused
// log is for logging in this package.
var accountlog = logf.Log.WithName("NIMAccountValidatingWebhook")

// SetupAccountWebhookWithManager registers the webhook for Account in the manager.
func SetupAccountWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).For(&nimv1.Account{}).
		WithValidator(&AccountCustomValidator{client: mgr.GetClient()}).
		Complete()
}

// TODO(user): EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!

// NOTE: The 'path' attribute must follow a specific pattern and should not be modified directly here.
// Modifying the path for an invalid path can cause API server errors; failing to locate the webhook.
// +kubebuilder:webhook:path=/validate-nim-opendatahub-io-v1-account,mutating=false,failurePolicy=fail,sideEffects=None,groups=nim.opendatahub.io,resources=accounts,verbs=create;update,versions=v1,name=validating.nim.account.odh-model-controller.opendatahub.io,admissionReviewVersions=v1

// AccountCustomValidator struct is responsible for validating the Account resource
// when it is created, updated, or deleted.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as this struct is used only for temporary operations and does not need to be deeply copied.
type AccountCustomValidator struct {
	client client.Client
}

var _ webhook.CustomValidator = &AccountCustomValidator{}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type Account.
func (v *AccountCustomValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (warnings admission.Warnings, err error) {
	account, ok := obj.(*nimv1.Account)
	if !ok {
		return nil, fmt.Errorf("expected a Account object but got %T", obj)
	}

	log := accountlog.WithValues("namespace", account.Namespace, "account", account.Name)
	log.Info("Validating NIM Account creation")

	err = v.verifySingletonInNamespace(ctx, account)
	if err != nil {
		log.Error(err, "Rejecting NIM Account creation because checking singleton didn't pass")
		return nil, err
	}

	return nil, nil
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type Account.
func (v *AccountCustomValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	// For update, nothing needs to be validated
	return nil, nil
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type Account.
func (v *AccountCustomValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	// For deletion, nothing needs to be validated
	return nil, nil
}

func (v *AccountCustomValidator) verifySingletonInNamespace(ctx context.Context, account *nimv1.Account) error {
	accountList := nimv1.AccountList{}
	err := v.client.List(ctx,
		&accountList,
		client.InNamespace(account.Namespace))
	if err != nil {
		return fmt.Errorf("failed to verify if there are existing Accounts with err: %s", err.Error())
	}

	if len(accountList.Items) > 0 {
		return fmt.Errorf("rejecting creation of Account %s in namespace %s because there is already an Account created in the namespace", account.Name, account.Namespace)
	}
	return nil
}
