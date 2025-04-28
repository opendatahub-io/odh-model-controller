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
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ssametav1 "k8s.io/client-go/applyconfigurations/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"

	v1 "github.com/opendatahub-io/odh-model-controller/api/nim/v1"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
)

// HandleResponse is used to encapsulate handle responses. Evaluate as follows:
// If Error != nil, an error occurred - the reconciliation should end with an error which will trigger a requeue.
// If Requeue == true, the Account status was updated - the reconciliation should end and requeue.
// If Continue != true, the reconciliation ended and no requeue is required.
type HandleResponse struct {
	Continue bool
	Requeue  bool
	Error    error
}

// NimHandler is a contract for NIM handlers
type NimHandler interface {
	// Handle reconciles and returns a response indicating the next reconciliation steps
	Handle(ctx context.Context, account *v1.Account) HandleResponse
}

var commonBaseLabels = map[string]string{"opendatahub.io/managed": "true"}

// createOwnerReferenceCfg is a utility functions for creating Account owner reference apply-config
func createOwnerReferenceCfg(account *v1.Account, scheme *runtime.Scheme) *ssametav1.OwnerReferenceApplyConfiguration {
	gvk, _ := apiutil.GVKForObject(account, scheme)
	return ssametav1.OwnerReference().
		WithKind(gvk.Kind).
		WithName(account.Name).
		WithAPIVersion(gvk.GroupVersion().String()).
		WithUID(account.GetUID()).
		WithBlockOwnerDeletion(true).
		WithController(true)
}

func makeCleanStatusForAccountFailure(account *v1.Account, msg string, conditions ...metav1.Condition) v1.AccountStatus {
	retStatus := account.Status
	retStatus.NIMConfig = nil
	retStatus.RuntimeTemplate = nil
	retStatus.NIMPullSecret = nil
	retStatus.LastAccountCheck = &metav1.Time{Time: time.Now()}
	retStatus.Conditions = conditions

	// every condition not specified in the arguments should be set to default
	for _, cond := range utils.NimConditions {
		if found := meta.FindStatusCondition(retStatus.Conditions, cond.String()); found == nil {
			if cond == utils.NimConditionAccountStatus {
				// set failure to the AccountStatus
				meta.SetStatusCondition(&retStatus.Conditions, utils.AccountFailCondition(account.Generation, msg))
			} else {
				// set unknown to anything else
				meta.SetStatusCondition(&retStatus.Conditions, utils.MakeNimCondition(cond, metav1.ConditionUnknown, account.Generation, "NotReconciled", msg))
			}
		}
	}
	return retStatus
}

// mergeStringMaps merged the base on top of the existing map, returns the final map and "true" if it needed to overwrite anything
func mergeStringMaps(baseMap, existing map[string]string) (bool, map[string]string) {
	if existing == nil {
		return true, baseMap
	}
	merged := existing
	changed := false
	for k, v := range baseMap {
		if _, foundK := existing[k]; !foundK {
			changed = true
			merged[k] = v
		}
	}
	return changed, merged
}

// shouldUpdateReference will true if an update to the object reference is required
func shouldUpdateReference(origRef, newRef *corev1.ObjectReference) bool {
	return origRef == nil || newRef == nil || !utils.NimEqualities.DeepEqual(origRef.UID, newRef.UID)
}
