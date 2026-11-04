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

package serving

import (
	"context"
	"testing"

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestInferenceGraphURLWatchMapping(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	service := func(name, namespace, owner string) *corev1.Service {
		return &corev1.Service{ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: namespace,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: kservev1beta1.SchemeGroupVersion.String(), Kind: "InferenceService",
				Name: owner, UID: types.UID(owner + "-uid"), Controller: ptr.To(true),
			}},
		}, Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 80, TargetPort: intstr.FromInt32(8080)}}}}
	}
	oldService := service("old-predictor", "models", "old")
	newService := service("new-transformer", "models", "new")
	unowned := service("unowned-predictor", "models", "unowned")
	unowned.OwnerReferences = nil
	external := service("external-predictor", "models", "external")
	external.Spec.Type = corev1.ServiceTypeExternalName
	external.Spec.ExternalName = "example.com"
	reconciler := &InferenceServiceReconciler{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		oldService, newService, unowned, external, service("other-predictor", "other", "other"),
	).Build()}
	graph := func(url string) *kservev1alpha1.InferenceGraph {
		return &kservev1alpha1.InferenceGraph{ObjectMeta: metav1.ObjectMeta{Namespace: "models"},
			Spec: kservev1alpha1.InferenceGraphSpec{Nodes: map[string]kservev1alpha1.InferenceRouter{
				"root": {Steps: []kservev1alpha1.InferenceStep{{InferenceTarget: kservev1alpha1.InferenceTarget{ServiceURL: url}}}},
			}}}
	}
	request := func(name string) []reconcile.Request {
		return []reconcile.Request{{NamespacedName: client.ObjectKey{Name: name, Namespace: "models"}}}
	}
	for _, tc := range []struct {
		name, url, owner string
	}{
		{"old reference", "http://old-predictor.models.svc.cluster.local", "old"},
		{"replacement reference", "http://new-transformer.models.svc/path", "new"},
		{"unowned Service", "http://unowned-predictor.models.svc", ""},
		{"other namespace", "http://other-predictor.other.svc", ""},
		{"external alias", "http://external-predictor.models.svc", ""},
		{"unmapped port", "http://old-predictor.models.svc:9000", ""},
		{"removed reference", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := reconciler.inferenceGraphInferenceServiceRequests(context.Background(), graph(tc.url))
			if tc.owner == "" {
				require.Empty(t, requests)
			} else {
				require.Equal(t, request(tc.owner), requests)
			}
		})
	}
}
