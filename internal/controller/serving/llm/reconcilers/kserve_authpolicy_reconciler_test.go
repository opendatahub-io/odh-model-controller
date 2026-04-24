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

package reconcilers

import (
	"context"
	"fmt"
	"testing"

	"github.com/go-logr/logr"
	kservev1alpha2 "github.com/kserve/kserve/pkg/apis/serving/v1alpha2"
	kuadrantv1 "github.com/kuadrant/kuadrant-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/resources"
)

type fakeAuthPolicyStore struct {
	getErr    error
	removeErr error
	createErr error
	updateErr error
}

func (f *fakeAuthPolicyStore) Get(_ context.Context, _ types.NamespacedName) (*kuadrantv1.AuthPolicy, error) {
	return nil, f.getErr
}

func (f *fakeAuthPolicyStore) Remove(_ context.Context, _ types.NamespacedName) error {
	return f.removeErr
}

func (f *fakeAuthPolicyStore) Create(_ context.Context, _ *kuadrantv1.AuthPolicy) error {
	return f.createErr
}

func (f *fakeAuthPolicyStore) Update(_ context.Context, _ *kuadrantv1.AuthPolicy) error {
	return f.updateErr
}

type fakeAuthPolicyDetector struct {
	authType constants.AuthType
}

func (f *fakeAuthPolicyDetector) Detect(_ context.Context, _ map[string]string) constants.AuthType {
	return f.authType
}

type fakeAuthPolicyTemplateLoader struct {
	policy *kuadrantv1.AuthPolicy
	err    error
}

func (f *fakeAuthPolicyTemplateLoader) Load(_ context.Context, target resources.AuthPolicyTarget, _ ...resources.ObjectOption) (*kuadrantv1.AuthPolicy, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.policy != nil {
		return f.policy, nil
	}
	return &kuadrantv1.AuthPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      target.Name + "-auth",
			Namespace: target.Namespace,
		},
	}, nil
}

func newTestLLMInferenceService(name, namespace string) *kservev1alpha2.LLMInferenceService {
	return &kservev1alpha2.LLMInferenceService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
}

func TestReconcile_AuthDisabled_NoMatchError_Skipped(t *testing.T) {
	noMatchErr := &meta.NoResourceMatchError{
		PartialResource: schema.GroupVersionResource{
			Group:    "kuadrant.io",
			Version:  "v1",
			Resource: "authpolicies",
		},
	}

	reconciler := &KserveAuthPolicyReconciler{
		store: &fakeAuthPolicyStore{
			getErr: fmt.Errorf("could not GET AuthPolicy: %w", noMatchErr),
		},
		detector:       &fakeAuthPolicyDetector{authType: constants.Anonymous},
		templateLoader: &fakeAuthPolicyTemplateLoader{},
	}

	llmisvc := newTestLLMInferenceService("test-svc", "test-ns")
	err := reconciler.Reconcile(context.Background(), logr.Discard(), llmisvc)
	if err != nil {
		t.Errorf("expected nil error when AuthPolicy CRD is not available, got: %v", err)
	}
}

func TestReconcile_AuthDisabled_OtherError_Propagated(t *testing.T) {
	reconciler := &KserveAuthPolicyReconciler{
		store: &fakeAuthPolicyStore{
			getErr: fmt.Errorf("connection refused"),
		},
		detector:       &fakeAuthPolicyDetector{authType: constants.Anonymous},
		templateLoader: &fakeAuthPolicyTemplateLoader{},
	}

	llmisvc := newTestLLMInferenceService("test-svc", "test-ns")
	err := reconciler.Reconcile(context.Background(), logr.Discard(), llmisvc)
	if err == nil {
		t.Error("expected error to be propagated, got nil")
	}
}

func TestDelete_NoMatchError_Skipped(t *testing.T) {
	noMatchErr := &meta.NoResourceMatchError{
		PartialResource: schema.GroupVersionResource{
			Group:    "kuadrant.io",
			Version:  "v1",
			Resource: "authpolicies",
		},
	}

	reconciler := &KserveAuthPolicyReconciler{
		store: &fakeAuthPolicyStore{
			removeErr: fmt.Errorf("could not DELETE AuthPolicy: %w", noMatchErr),
		},
	}

	llmisvc := newTestLLMInferenceService("test-svc", "test-ns")
	err := reconciler.Delete(context.Background(), logr.Discard(), llmisvc)
	if err != nil {
		t.Errorf("expected nil error when AuthPolicy CRD is not available during delete, got: %v", err)
	}
}

func TestDelete_OtherError_Propagated(t *testing.T) {
	reconciler := &KserveAuthPolicyReconciler{
		store: &fakeAuthPolicyStore{
			removeErr: fmt.Errorf("connection refused"),
		},
	}

	llmisvc := newTestLLMInferenceService("test-svc", "test-ns")
	err := reconciler.Delete(context.Background(), logr.Discard(), llmisvc)
	if err == nil {
		t.Error("expected error to be propagated, got nil")
	}
}

func TestReconcile_NoSetEnableAuthAnnotation_NoMatchError_Skipped(t *testing.T) {
	noMatchErr := &meta.NoResourceMatchError{
		PartialResource: schema.GroupVersionResource{
			Group:    "kuadrant.io",
			Version:  "v1",
			Resource: "authpolicies",
		},
	}

	reconciler := &KserveAuthPolicyReconciler{
		store: &fakeAuthPolicyStore{
			getErr: fmt.Errorf("could not GET AuthPolicy: %w", noMatchErr),
		},
		detector:       &fakeAuthPolicyDetector{authType: constants.UserDefined},
		templateLoader: &fakeAuthPolicyTemplateLoader{},
	}

	llmisvc := newTestLLMInferenceService("test-svc", "test-ns")
	err := reconciler.Reconcile(context.Background(), logr.Discard(), llmisvc)
	if err != nil {
		t.Errorf("expected nil error when enable-auth annotation is not set and AuthPolicy CRD is not available, got: %v", err)
	}
}
