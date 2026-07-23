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
	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

var _ = Describe("injectNimServedModelName", func() {
	var scheme *runtime.Scheme

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(kservev1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(kservev1beta1.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
	})

	When("ISVC uses NIM runtime with no existing passthrough args", func() {
		It("should inject NIM_PASSTHROUGH_ARGS with --served-model-name", func(ctx SpecContext) {
			sr := createServingRuntime("nim-runtime", map[string]string{
				utils.IsNimRuntimeAnnotation: "true",
			})
			isvc := &kservev1beta1.InferenceService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-nim-model",
					Namespace: "test-namespace",
				},
				Spec: kservev1beta1.InferenceServiceSpec{
					Predictor: kservev1beta1.PredictorSpec{
						Model: &kservev1beta1.ModelSpec{
							ModelFormat: kservev1beta1.ModelFormat{Name: "test-format"},
							Runtime:     ptr.To("nim-runtime"),
						},
					},
				},
			}

			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sr, isvc).Build()
			reconciler := NewKServeRawInferenceServiceReconciler(client)

			err := reconciler.injectNimServedModelName(ctx, log.Log, isvc)
			Expect(err).NotTo(HaveOccurred())

			updated := &kservev1beta1.InferenceService{}
			err = client.Get(ctx, k8stypes.NamespacedName{Name: "my-nim-model", Namespace: "test-namespace"}, updated)
			Expect(err).NotTo(HaveOccurred())

			env := updated.Spec.Predictor.Model.Container.Env
			Expect(env).To(ContainElement(corev1.EnvVar{
				Name:  utils.NimPassthroughArgsEnv,
				Value: "--served-model-name my-nim-model",
			}))
		})
	})

	When("ISVC uses NIM runtime with existing passthrough args", func() {
		It("should prepend --served-model-name to existing args", func(ctx SpecContext) {
			sr := createServingRuntime("nim-runtime", map[string]string{
				utils.IsNimRuntimeAnnotation: "true",
			})
			isvc := &kservev1beta1.InferenceService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-nim-model",
					Namespace: "test-namespace",
				},
				Spec: kservev1beta1.InferenceServiceSpec{
					Predictor: kservev1beta1.PredictorSpec{
						Model: &kservev1beta1.ModelSpec{
							ModelFormat: kservev1beta1.ModelFormat{Name: "test-format"},
							Runtime:     ptr.To("nim-runtime"),
							PredictorExtensionSpec: kservev1beta1.PredictorExtensionSpec{
								Container: corev1.Container{
									Env: []corev1.EnvVar{
										{Name: utils.NimPassthroughArgsEnv, Value: "--gpu-memory-utilization 0.80 --max-num-seqs 8"},
									},
								},
							},
						},
					},
				},
			}

			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sr, isvc).Build()
			reconciler := NewKServeRawInferenceServiceReconciler(client)

			err := reconciler.injectNimServedModelName(ctx, log.Log, isvc)
			Expect(err).NotTo(HaveOccurred())

			updated := &kservev1beta1.InferenceService{}
			err = client.Get(ctx, k8stypes.NamespacedName{Name: "my-nim-model", Namespace: "test-namespace"}, updated)
			Expect(err).NotTo(HaveOccurred())

			env := updated.Spec.Predictor.Model.Container.Env
			Expect(env).To(ContainElement(corev1.EnvVar{
				Name:  utils.NimPassthroughArgsEnv,
				Value: "--served-model-name my-nim-model --gpu-memory-utilization 0.80 --max-num-seqs 8",
			}))
		})
	})

	When("ISVC already has --served-model-name", func() {
		It("should not modify the ISVC", func(ctx SpecContext) {
			sr := createServingRuntime("nim-runtime", map[string]string{
				utils.IsNimRuntimeAnnotation: "true",
			})
			isvc := &kservev1beta1.InferenceService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-nim-model",
					Namespace: "test-namespace",
				},
				Spec: kservev1beta1.InferenceServiceSpec{
					Predictor: kservev1beta1.PredictorSpec{
						Model: &kservev1beta1.ModelSpec{
							ModelFormat: kservev1beta1.ModelFormat{Name: "test-format"},
							Runtime:     ptr.To("nim-runtime"),
							PredictorExtensionSpec: kservev1beta1.PredictorExtensionSpec{
								Container: corev1.Container{
									Env: []corev1.EnvVar{
										{Name: utils.NimPassthroughArgsEnv, Value: "--served-model-name custom-name"},
									},
								},
							},
						},
					},
				},
			}

			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sr, isvc).Build()
			reconciler := NewKServeRawInferenceServiceReconciler(client)

			err := reconciler.injectNimServedModelName(ctx, log.Log, isvc)
			Expect(err).NotTo(HaveOccurred())

			updated := &kservev1beta1.InferenceService{}
			err = client.Get(ctx, k8stypes.NamespacedName{Name: "my-nim-model", Namespace: "test-namespace"}, updated)
			Expect(err).NotTo(HaveOccurred())

			env := updated.Spec.Predictor.Model.Container.Env
			Expect(env).To(ContainElement(corev1.EnvVar{
				Name:  utils.NimPassthroughArgsEnv,
				Value: "--served-model-name custom-name",
			}))
		})
	})

	When("ISVC has NIM_PASSTHROUGH_ARGS with ValueFrom", func() {
		It("should not modify the ISVC", func(ctx SpecContext) {
			sr := createServingRuntime("nim-runtime", map[string]string{
				utils.IsNimRuntimeAnnotation: "true",
			})
			isvc := &kservev1beta1.InferenceService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-nim-model",
					Namespace: "test-namespace",
				},
				Spec: kservev1beta1.InferenceServiceSpec{
					Predictor: kservev1beta1.PredictorSpec{
						Model: &kservev1beta1.ModelSpec{
							ModelFormat: kservev1beta1.ModelFormat{Name: "test-format"},
							Runtime:     ptr.To("nim-runtime"),
							PredictorExtensionSpec: kservev1beta1.PredictorExtensionSpec{
								Container: corev1.Container{
									Env: []corev1.EnvVar{
										{
											Name: utils.NimPassthroughArgsEnv,
											ValueFrom: &corev1.EnvVarSource{
												SecretKeyRef: &corev1.SecretKeySelector{
													LocalObjectReference: corev1.LocalObjectReference{Name: "nim-secret"},
													Key:                  "passthrough-args",
												},
											},
										},
									},
								},
							},
						},
					},
				},
			}

			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sr, isvc).Build()
			reconciler := NewKServeRawInferenceServiceReconciler(client)

			err := reconciler.injectNimServedModelName(ctx, log.Log, isvc)
			Expect(err).NotTo(HaveOccurred())

			updated := &kservev1beta1.InferenceService{}
			err = client.Get(ctx, k8stypes.NamespacedName{Name: "my-nim-model", Namespace: "test-namespace"}, updated)
			Expect(err).NotTo(HaveOccurred())

			env := updated.Spec.Predictor.Model.Container.Env
			Expect(env).To(HaveLen(1))
			Expect(env[0].Value).To(BeEmpty())
			Expect(env[0].ValueFrom).NotTo(BeNil())
		})
	})

	When("ISVC uses non-NIM runtime", func() {
		It("should not modify the ISVC", func(ctx SpecContext) {
			sr := createServingRuntime("vllm-runtime", map[string]string{})
			isvc := &kservev1beta1.InferenceService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-vllm-model",
					Namespace: "test-namespace",
				},
				Spec: kservev1beta1.InferenceServiceSpec{
					Predictor: kservev1beta1.PredictorSpec{
						Model: &kservev1beta1.ModelSpec{
							ModelFormat: kservev1beta1.ModelFormat{Name: "test-format"},
							Runtime:     ptr.To("vllm-runtime"),
						},
					},
				},
			}

			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sr, isvc).Build()
			reconciler := NewKServeRawInferenceServiceReconciler(client)

			err := reconciler.injectNimServedModelName(ctx, log.Log, isvc)
			Expect(err).NotTo(HaveOccurred())

			updated := &kservev1beta1.InferenceService{}
			err = client.Get(ctx, k8stypes.NamespacedName{Name: "my-vllm-model", Namespace: "test-namespace"}, updated)
			Expect(err).NotTo(HaveOccurred())

			Expect(updated.Spec.Predictor.Model.Container.Env).To(BeEmpty())
		})
	})

	When("ISVC references a runtime that does not exist", func() {
		It("should not modify the ISVC and not return an error", func(ctx SpecContext) {
			isvc := &kservev1beta1.InferenceService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-missing-runtime-model",
					Namespace: "test-namespace",
				},
				Spec: kservev1beta1.InferenceServiceSpec{
					Predictor: kservev1beta1.PredictorSpec{
						Model: &kservev1beta1.ModelSpec{
							ModelFormat: kservev1beta1.ModelFormat{Name: "test-format"},
							Runtime:     ptr.To("nonexistent-runtime"),
						},
					},
				},
			}

			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(isvc).Build()
			reconciler := NewKServeRawInferenceServiceReconciler(client)

			err := reconciler.injectNimServedModelName(ctx, log.Log, isvc)
			Expect(err).NotTo(HaveOccurred())

			updated := &kservev1beta1.InferenceService{}
			err = client.Get(ctx, k8stypes.NamespacedName{Name: "my-missing-runtime-model", Namespace: "test-namespace"}, updated)
			Expect(err).NotTo(HaveOccurred())

			Expect(updated.Spec.Predictor.Model.Container.Env).To(BeEmpty())
		})
	})
})
