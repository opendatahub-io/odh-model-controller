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

package v1alpha1

import (
	"fmt"

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	"github.com/kserve/kserve/pkg/constants"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	testutils "github.com/opendatahub-io/odh-model-controller/test/utils"
)

const InferenceServiceConfigPath1 = "../../../controller/serving/testdata/configmaps/inferenceservice-config.yaml"

var _ = Describe("InferenceGraph Webhook", func() {
	Context("When creating InferenceGraph under Defaulting Webhook", func() {
		var defaulter InferenceGraphCustomDefaulter

		BeforeEach(func() {
			defaulter = InferenceGraphCustomDefaulter{client: k8sClient}

			inferenceServiceConfig := &corev1.ConfigMap{}
			Expect(testutils.ConvertToStructuredResource(InferenceServiceConfigPath1, inferenceServiceConfig)).To(Succeed())
			if err := k8sClient.Create(ctx, inferenceServiceConfig); err != nil && !k8sErrors.IsAlreadyExists(err) {
				Fail(err.Error())
			}
		})

		It("Should apply default annotations for Serverless mode", func() {
			ig := kservev1alpha1.InferenceGraph{}
			Expect(defaulter.Default(ctx, &ig)).To(Succeed())

			annotations := ig.GetAnnotations()
			Expect(annotations).To(HaveKeyWithValue("serving.knative.openshift.io/enablePassthrough", "true"))
			Expect(annotations).To(HaveKeyWithValue("sidecar.istio.io/inject", "true"))
			Expect(annotations).To(HaveKeyWithValue("sidecar.istio.io/rewriteAppHTTPProbers", "true"))
		})

		It("Should keep user-specified annotations for Serverless mode", func() {
			ig := kservev1alpha1.InferenceGraph{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ig",
					Namespace: "test-ig",
					Annotations: map[string]string{
						"serving.knative.openshift.io/enablePassthrough": "false",
						"sidecar.istio.io/rewriteAppHTTPProbers":         "false",
						"sidecar.istio.io/inject":                        "false",
					},
				},
			}
			Expect(defaulter.Default(ctx, &ig)).To(Succeed())

			annotations := ig.GetAnnotations()
			Expect(annotations).To(HaveKeyWithValue("serving.knative.openshift.io/enablePassthrough", "false"))
			Expect(annotations).To(HaveKeyWithValue("sidecar.istio.io/inject", "false"))
			Expect(annotations).To(HaveKeyWithValue("sidecar.istio.io/rewriteAppHTTPProbers", "false"))
		})

		It("Should not apply default annotations for Raw mode specified in annotation", func() {
			ig := kservev1alpha1.InferenceGraph{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ig",
					Namespace: "test-ig",
					Annotations: map[string]string{
						constants.DeploymentMode: string(constants.RawDeployment),
					},
				},
			}
			Expect(defaulter.Default(ctx, &ig)).To(Succeed())

			annotations := ig.GetAnnotations()
			Expect(annotations).ToNot(HaveKey("serving.knative.openshift.io/enablePassthrough"))
			Expect(annotations).ToNot(HaveKey("sidecar.istio.io/inject"))
			Expect(annotations).ToNot(HaveKey("sidecar.istio.io/rewriteAppHTTPProbers"))
		})

		It("Should not apply default annotations for Raw mode specified in default configuration", func() {
			inferenceServiceConfig := &corev1.ConfigMap{}
			Expect(testutils.ConvertToStructuredResource(InferenceServiceConfigPath1, inferenceServiceConfig)).To(Succeed())
			inferenceServiceConfig.Data["deploy"] = "{\"defaultDeploymentMode\": \"RawDeployment\"}"
			Expect(k8sClient.Update(ctx, inferenceServiceConfig)).To(Succeed())

			ig := kservev1alpha1.InferenceGraph{}
			Expect(defaulter.Default(ctx, &ig)).To(Succeed())

			annotations := ig.GetAnnotations()
			Expect(annotations).ToNot(HaveKey("serving.knative.openshift.io/enablePassthrough"))
			Expect(annotations).ToNot(HaveKey("sidecar.istio.io/inject"))
			Expect(annotations).ToNot(HaveKey("sidecar.istio.io/rewriteAppHTTPProbers"))
		})
	})

	Context("When creating or updating InferenceGraph under Validating Webhook", func() {
		const testingNamespace = "namespace-ig"
		var validator InferenceGraphCustomValidator

		buildInferenceGraphWithSteps := func(steps []kservev1alpha1.InferenceStep) kservev1alpha1.InferenceGraph {
			return kservev1alpha1.InferenceGraph{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ig",
					Namespace: testingNamespace,
				},
				Spec: kservev1alpha1.InferenceGraphSpec{
					Nodes: map[string]kservev1alpha1.InferenceRouter{
						"root": {
							RouterType: kservev1alpha1.Sequence,
							Steps:      steps,
						},
					},
				},
			}
		}

		BeforeEach(func() {
			validator = InferenceGraphCustomValidator{}
		})

		It("Should allow a reference to another node in the InferenceGraph", func() {
			inferenceGraph := buildInferenceGraphWithSteps([]kservev1alpha1.InferenceStep{
				{
					StepName: "first",
					InferenceTarget: kservev1alpha1.InferenceTarget{
						NodeName: "another-inferencegraph-node",
					},
				},
			})

			Expect(validator.ValidateCreate(ctx, &inferenceGraph)).Error().To(Succeed())
			Expect(validator.ValidateUpdate(ctx, nil, &inferenceGraph)).Error().To(Succeed())
		})

		It("Should allow a reference to an InferenceService by name", func() {
			inferenceGraph := buildInferenceGraphWithSteps([]kservev1alpha1.InferenceStep{
				{
					StepName: "first",
					InferenceTarget: kservev1alpha1.InferenceTarget{
						ServiceName: "isvc-by-name",
					},
				},
			})

			Expect(validator.ValidateCreate(ctx, &inferenceGraph)).Error().ToNot(HaveOccurred())
			Expect(validator.ValidateUpdate(ctx, nil, &inferenceGraph)).Error().ToNot(HaveOccurred())
		})

		It("Should allow using FQDN for the same namespace as the InferenceGraph", func() {
			inferenceGraph := buildInferenceGraphWithSteps([]kservev1alpha1.InferenceStep{
				{
					StepName: "first",
					InferenceTarget: kservev1alpha1.InferenceTarget{
						ServiceURL: fmt.Sprintf("https://isvc-name.%s.svc.cluster.local", testingNamespace),
					},
				},
			})

			Expect(validator.ValidateCreate(ctx, &inferenceGraph)).Error().ToNot(HaveOccurred())
			Expect(validator.ValidateUpdate(ctx, nil, &inferenceGraph)).Error().ToNot(HaveOccurred())
		})

		It("Should allow using short hostname for the same namespace as the InferenceGraph", func() {
			inferenceGraph := buildInferenceGraphWithSteps([]kservev1alpha1.InferenceStep{
				{
					StepName: "first",
					InferenceTarget: kservev1alpha1.InferenceTarget{
						ServiceURL: fmt.Sprintf("https://isvc-name.%s.svc", testingNamespace),
					},
				},
			})

			Expect(validator.ValidateCreate(ctx, &inferenceGraph)).Error().ToNot(HaveOccurred())
			Expect(validator.ValidateUpdate(ctx, nil, &inferenceGraph)).Error().ToNot(HaveOccurred())
		})

		It("Should reject using FQDN for a different namespace than the InferenceGraph", func() {
			inferenceGraph := buildInferenceGraphWithSteps([]kservev1alpha1.InferenceStep{
				{
					StepName: "first",
					InferenceTarget: kservev1alpha1.InferenceTarget{
						ServiceURL: fmt.Sprintf("https://isvc-name.%s.svc.cluster.local", testingNamespace+"-different"),
					},
				},
			})

			Expect(validator.ValidateCreate(ctx, &inferenceGraph)).Error().To(HaveOccurred())
			Expect(validator.ValidateUpdate(ctx, nil, &inferenceGraph)).Error().To(HaveOccurred())
		})

		It("Should reject using short hostname for a different namespace than the InferenceGraph", func() {
			inferenceGraph := buildInferenceGraphWithSteps([]kservev1alpha1.InferenceStep{
				{
					StepName: "first",
					InferenceTarget: kservev1alpha1.InferenceTarget{
						ServiceURL: fmt.Sprintf("https://isvc-name.%s.svc", testingNamespace+"-different"),
					},
				},
			})

			Expect(validator.ValidateCreate(ctx, &inferenceGraph)).Error().To(HaveOccurred())
			Expect(validator.ValidateUpdate(ctx, nil, &inferenceGraph)).Error().To(HaveOccurred())
		})

		It("Should reject using public hostnames", func() {
			inferenceGraph := buildInferenceGraphWithSteps([]kservev1alpha1.InferenceStep{
				{
					StepName: "first",
					InferenceTarget: kservev1alpha1.InferenceTarget{
						ServiceURL: "https://example.com",
					},
				},
			})

			Expect(validator.ValidateCreate(ctx, &inferenceGraph)).Error().To(HaveOccurred())
			Expect(validator.ValidateUpdate(ctx, nil, &inferenceGraph)).Error().To(HaveOccurred())
		})

	})
})
