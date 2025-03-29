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

package utils

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type NimConditionType string

const (
	NimConditionAccountStatus    NimConditionType = "AccountStatus"
	NimConditionTemplateUpdate   NimConditionType = "TemplateUpdate"
	NimConditionSecretUpdate     NimConditionType = "SecretUpdate"
	NimConditionConfigMapUpdate  NimConditionType = "ConfigMapUpdate"
	NimConditionAPIKeyValidation NimConditionType = "APIKeyValidation"
)

var NimConditions = []NimConditionType{
	NimConditionAccountStatus, NimConditionTemplateUpdate, NimConditionSecretUpdate,
	NimConditionConfigMapUpdate, NimConditionAPIKeyValidation}

func (r NimConditionType) String() string {
	return string(r)
}

// MakeNimCondition is used for building a status condition for NIM integration
func MakeNimCondition(condType NimConditionType, status metav1.ConditionStatus, gen int64, reason, msg string) metav1.Condition {
	return metav1.Condition{
		Type:               condType.String(),
		Status:             status,
		ObservedGeneration: gen,
		LastTransitionTime: metav1.Time{Time: time.Now()},
		Reason:             reason,
		Message:            msg,
	}
}

// AccountFailCondition returns an AccountStatus failed condition
func AccountFailCondition(gen int64, msg string) metav1.Condition {
	return MakeNimCondition(NimConditionAccountStatus, metav1.ConditionFalse, gen, "AccountNotSuccessful", msg)
}
