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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("InferenceGraph Webhook", func() {
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
