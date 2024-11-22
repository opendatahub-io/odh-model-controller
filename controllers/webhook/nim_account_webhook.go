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

package webhook

import (
	"context"
	"fmt"

	nimv1 "github.com/opendatahub-io/odh-model-controller/api/nim/v1"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// +kubebuilder:webhook:admissionReviewVersions=v1,path=/validate-nim-opendatahub-io-v1-account,mutating=false,failurePolicy=fail,groups=nim.opendatahub.io,resources=accounts,verbs=create;update,versions=v1,name=validating.nim.account.odh-model-controller.opendatahub.io,sideEffects=None

type nimAccountValidator struct {
	client client.Client
}

func NewNimAccountValidator(client client.Client) admission.CustomValidator {
	return &nimAccountValidator{client: client}
}

func (v *nimAccountValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (warnings admission.Warnings, err error) {
	account := obj.(*nimv1.Account)

	log := logf.FromContext(ctx).WithName("NIMAccountValidatingWebhook").
		WithValues("namespace", account.Namespace, "account", account.Name)
	log.Info("Validating NIM Account creation")

	err = v.verifySingletonInNamespace(ctx, account)
	if err != nil {
		log.Error(err, "Rejecting NIM Account creation because checking singleton didn't pass")
		return nil, err
	}

	return nil, nil
}

func (v *nimAccountValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (warnings admission.Warnings, err error) {
	// For update, nothing needs to be validated
	return nil, nil
}

func (v *nimAccountValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (warnings admission.Warnings, err error) {
	// For deletion, nothing needs to be validated
	return nil, nil
}

func (v *nimAccountValidator) verifySingletonInNamespace(ctx context.Context, account *nimv1.Account) error {
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
