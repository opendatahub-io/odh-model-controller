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

package llm

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	kservev1alpha2 "github.com/kserve/kserve/pkg/apis/serving/v1alpha2"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type gridResyncClient struct {
	client.Client
	services      []kservev1alpha2.LLMInferenceService
	listNamespace string
}

func (c *gridResyncClient) List(_ context.Context, list client.ObjectList, options ...client.ListOption) error {
	opts := (&client.ListOptions{}).ApplyOptions(options)
	c.listNamespace = opts.Namespace
	services := list.(*kservev1alpha2.LLMInferenceServiceList)
	for _, svc := range c.services {
		if opts.Namespace == "" || svc.Namespace == opts.Namespace {
			services.Items = append(services.Items, svc)
		}
	}
	return nil
}

func TestGridResyncScope(t *testing.T) {
	for _, name := range []string{"namespace", "config"} {
		t.Run(name, func(t *testing.T) {
			reader := &gridResyncClient{services: []kservev1alpha2.LLMInferenceService{
				{ObjectMeta: metav1.ObjectMeta{Namespace: "tenant-a", Name: "enabled", Annotations: map[string]string{GridEnabledAnnotation: "true"}}},
				{ObjectMeta: metav1.ObjectMeta{Namespace: "tenant-a", Name: "cleanup", Finalizers: []string{gridFinalizer}}},
				{ObjectMeta: metav1.ObjectMeta{Namespace: "tenant-a", Name: "excluded"}},
				{ObjectMeta: metav1.ObjectMeta{Namespace: "tenant-b", Name: "enabled", Annotations: map[string]string{GridEnabledAnnotation: "true"}}},
			}}
			r := &GridInferenceProviderReconciler{Client: reader}
			mapper := r.resync(logr.Discard())
			var object client.Object = &kservev1alpha2.LLMInferenceServiceConfig{ObjectMeta: metav1.ObjectMeta{Name: "config", Namespace: "tenant-a"}}
			expectedNamespace := ""
			expected := []reconcile.Request{
				{NamespacedName: client.ObjectKey{Namespace: "tenant-a", Name: "enabled"}},
				{NamespacedName: client.ObjectKey{Namespace: "tenant-a", Name: "cleanup"}},
			}
			switch name {
			case "namespace":
				mapper = r.namespaceResync(logr.Discard())
				object = &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "tenant-a"}}
				expectedNamespace = "tenant-a"
			default:
				expected = append(expected, reconcile.Request{NamespacedName: client.ObjectKey{Namespace: "tenant-b", Name: "enabled"}})
			}
			queue := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())
			defer queue.ShutDown()
			mapper.Create(context.Background(), event.CreateEvent{Object: object}, queue)
			var actual []reconcile.Request
			for queue.Len() > 0 {
				request, shutdown := queue.Get()
				require.False(t, shutdown)
				actual = append(actual, request)
				queue.Done(request)
			}
			require.Equal(t, expectedNamespace, reader.listNamespace)
			require.ElementsMatch(t, expected, actual)
		})
	}
}

func TestGridServiceAccountPredicate(t *testing.T) {
	r := &GridInferenceProviderReconciler{Config: GridConfig{Namespace: "grid-system", ServiceAccount: "grid-client"}}
	filter := r.serviceAccountPredicate()
	for _, tc := range []struct {
		name, namespace string
		accepted        bool
	}{
		{"grid-client", "grid-system", true},
		{"other-client", "grid-system", false},
		{"grid-client", "other-system", false},
	} {
		t.Run(tc.namespace+"/"+tc.name, func(t *testing.T) {
			account := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: tc.name, Namespace: tc.namespace}}
			require.Equal(t, tc.accepted, filter.Create(event.CreateEvent{Object: account}))
			require.Equal(t, tc.accepted, filter.Update(event.UpdateEvent{ObjectOld: account, ObjectNew: account}))
			require.Equal(t, tc.accepted, filter.Delete(event.DeleteEvent{Object: account}))
			require.Equal(t, tc.accepted, filter.Generic(event.GenericEvent{Object: account}))
		})
	}
}
