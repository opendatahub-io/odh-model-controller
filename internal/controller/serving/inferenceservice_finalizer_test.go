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

package serving

import (
	"context"
	"fmt"

	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	controllerutils "github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
)

var _ = Describe("InferenceService finalizer during deletion", func() {
	var testScheme *runtime.Scheme

	BeforeEach(func() {
		testScheme = runtime.NewScheme()
		controllerutils.RegisterSchemes(testScheme)
	})

	newDeletingISVC := func(namespace, name string) *kservev1beta1.InferenceService {
		now := metav1.Now()
		return &kservev1beta1.InferenceService{
			ObjectMeta: metav1.ObjectMeta{
				Name:              name,
				Namespace:         namespace,
				DeletionTimestamp: &now,
				Finalizers:        []string{constants.InferenceServiceODHFinalizerName},
			},
			Spec: kservev1beta1.InferenceServiceSpec{
				Predictor: kservev1beta1.PredictorSpec{},
			},
		}
	}

	It("should retain the finalizer when deletion cleanup fails", func() {
		isvc := newDeletingISVC("test-ns", "test-isvc")

		fakeClient := fake.NewClientBuilder().
			WithScheme(testScheme).
			WithObjects(isvc).
			WithInterceptorFuncs(interceptor.Funcs{
				List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
					return fmt.Errorf("simulated cleanup failure")
				},
			}).
			Build()

		reconciler := NewInferenceServiceReconciler(
			ctrl.Log.WithName("test"),
			fakeClient,
			testScheme,
			fakeClient,
			false, false, "",
		)

		_, err := reconciler.ReconcileServing(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: "test-isvc", Namespace: "test-ns"},
		})
		Expect(err).To(HaveOccurred(), "reconcile should return the cleanup error")

		updatedIsvc := &kservev1beta1.InferenceService{}
		Expect(fakeClient.Get(ctx, types.NamespacedName{Name: "test-isvc", Namespace: "test-ns"}, updatedIsvc)).To(Succeed())
		Expect(controllerutil.ContainsFinalizer(updatedIsvc, constants.InferenceServiceODHFinalizerName)).To(BeTrue(),
			"finalizer must be retained so the controller can retry cleanup")
	})

	It("should remove the finalizer when deletion cleanup succeeds", func() {
		isvc := newDeletingISVC("test-ns", "test-isvc")

		fakeClient := fake.NewClientBuilder().
			WithScheme(testScheme).
			WithObjects(isvc).
			Build()

		reconciler := NewInferenceServiceReconciler(
			ctrl.Log.WithName("test"),
			fakeClient,
			testScheme,
			fakeClient,
			false, false, "",
		)

		_, err := reconciler.ReconcileServing(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: "test-isvc", Namespace: "test-ns"},
		})
		Expect(err).NotTo(HaveOccurred())

		updatedIsvc := &kservev1beta1.InferenceService{}
		Expect(fakeClient.Get(ctx, types.NamespacedName{Name: "test-isvc", Namespace: "test-ns"}, updatedIsvc)).To(Succeed())
		Expect(controllerutil.ContainsFinalizer(updatedIsvc, constants.InferenceServiceODHFinalizerName)).To(BeFalse(),
			"finalizer should be removed after successful cleanup")
	})
})
