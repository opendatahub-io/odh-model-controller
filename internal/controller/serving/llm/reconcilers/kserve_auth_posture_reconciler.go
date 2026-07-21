/*
Copyright 2026.

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

package reconcilers

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/go-logr/logr"
	kservev1alpha2 "github.com/kserve/kserve/pkg/apis/serving/v1alpha2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	parentreconcilers "github.com/opendatahub-io/odh-model-controller/internal/controller/serving/reconcilers"
)

var _ parentreconcilers.LLMSubResourceReconciler = (*KserveAuthPostureReconciler)(nil)

type KserveAuthPostureReconciler struct {
	client   client.Client
	recorder record.EventRecorder
}

func NewKserveAuthPostureReconciler(client client.Client, recorder record.EventRecorder) *KserveAuthPostureReconciler {
	return &KserveAuthPostureReconciler{
		client:   client,
		recorder: recorder,
	}
}

func (r *KserveAuthPostureReconciler) Reconcile(ctx context.Context, log logr.Logger, llmisvc *kservev1alpha2.LLMInferenceService) error {
	group := llmisvc.Labels[constants.RoutingGroupLabel]
	if group == "" {
		return nil
	}

	siblings := &kservev1alpha2.LLMInferenceServiceList{}
	if err := r.client.List(ctx, siblings,
		client.InNamespace(llmisvc.Namespace),
		client.MatchingLabels{constants.RoutingGroupLabel: group},
	); err != nil {
		return fmt.Errorf("listing routing group %q members: %w", group, err)
	}

	if len(siblings.Items) < 2 {
		return nil
	}

	var withAuth, withoutAuth []string
	for i := range siblings.Items {
		s := &siblings.Items[i]
		if !s.DeletionTimestamp.IsZero() {
			continue
		}
		if authEnabled(s) {
			withAuth = append(withAuth, s.Name)
		} else {
			withoutAuth = append(withoutAuth, s.Name)
		}
	}

	if len(withAuth) == 0 || len(withoutAuth) == 0 {
		return nil
	}

	sort.Strings(withAuth)
	sort.Strings(withoutAuth)

	msg := fmt.Sprintf(
		"auth posture mismatch in group %q: %s have enable-auth=true, %s have enable-auth=false",
		group, formatNames(withAuth), formatNames(withoutAuth),
	)
	log.Info(msg)
	r.recorder.Event(llmisvc, corev1.EventTypeWarning, "AuthPostureMismatch", msg)

	return nil
}

func (r *KserveAuthPostureReconciler) Delete(_ context.Context, _ logr.Logger, _ *kservev1alpha2.LLMInferenceService) error {
	return nil
}

func (r *KserveAuthPostureReconciler) Cleanup(_ context.Context, _ logr.Logger, _ string) error {
	return nil
}

// authEnabled reports whether the LLMInferenceService has auth enabled.
// Auth is enabled by default; only an explicit "false" annotation disables it.
func authEnabled(svc *kservev1alpha2.LLMInferenceService) bool {
	if value, ok := svc.Annotations[constants.EnableAuthODHAnnotation]; ok {
		return !strings.EqualFold(strings.TrimSpace(value), "false")
	}
	return true
}

func formatNames(names []string) string {
	return "[" + strings.Join(names, ", ") + "]"
}
