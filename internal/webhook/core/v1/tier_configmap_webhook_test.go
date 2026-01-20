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

package v1

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/serving/reconcilers"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Tier ConfigMap Validator Webhook", func() {
	var validator TierConfigMapValidator
	var maasNamespace string

	BeforeEach(func() {
		validator = TierConfigMapValidator{}
		maasNamespace = reconcilers.GetMaasNamespace()
	})

	Describe("validateTierLevels", func() {
		It("should pass validation when tiers have unique levels", func() {
			configMap := &corev1.ConfigMap{
				Data: map[string]string{
					"tiers": `
- name: "free"
  level: 10
  groups: ["free-users"]
- name: "premium"
  level: 20
  groups: ["premium-users"]
`,
				},
			}

			err := validateTierLevels(configMap)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should fail validation when tiers have duplicate levels", func() {
			configMap := &corev1.ConfigMap{
				Data: map[string]string{
					"tiers": `
- name: "free"
  level: 10
  groups: ["free-users"]
- name: "premium"
  level: 10
  groups: ["premium-users"]
`,
				},
			}

			err := validateTierLevels(configMap)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("duplicate levels found"))
		})

		It("should fail validation when 3 tiers have 2 duplicate levels and 1 unique", func() {
			configMap := &corev1.ConfigMap{
				Data: map[string]string{
					"tiers": `
- name: "free"
  level: 10
  groups: ["free-users"]
- name: "basic"
  level: 20
  groups: ["basic-users"]
- name: "premium"
  level: 10
  groups: ["premium-users"]
`,
				},
			}

			err := validateTierLevels(configMap)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("duplicate levels found"))
		})

		It("should pass validation when tiers key is missing", func() {
			configMap := &corev1.ConfigMap{
				Data: map[string]string{
					"other-key": "some-value",
				},
			}

			err := validateTierLevels(configMap)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should fail validation when tiers YAML is malformed", func() {
			configMap := &corev1.ConfigMap{
				Data: map[string]string{
					"tiers": `this is not valid yaml [[[`,
				},
			}

			err := validateTierLevels(configMap)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to parse tiers configuration"))
		})

		It("should detect duplicate level 0 when tier omits level field", func() {
			configMap := &corev1.ConfigMap{
				Data: map[string]string{
					"tiers": `
- name: "tier-without-level"
  groups: ["group-a"]
- name: "tier-with-explicit-zero"
  level: 0
  groups: ["group-b"]
`,
				},
			}

			err := validateTierLevels(configMap)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("duplicate levels found"))
		})
	})

	Describe("validate method filtering", func() {
		It("should skip validation for non-tier ConfigMaps", func() {
			configMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "some-other-configmap",
					Namespace: maasNamespace,
				},
				Data: map[string]string{
					"tiers": `
- name: "free"
  level: 10
- name: "premium"
  level: 10
`,
				},
			}

			warnings, err := validator.validate(configMap)
			Expect(err).NotTo(HaveOccurred())
			Expect(warnings).To(BeNil())
		})

		It("should skip validation for ConfigMaps in wrong namespace", func() {
			configMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      reconcilers.TierConfigMapName,
					Namespace: "some-other-namespace",
				},
				Data: map[string]string{
					"tiers": `
- name: "free"
  level: 10
- name: "premium"
  level: 10
`,
				},
			}

			warnings, err := validator.validate(configMap)
			Expect(err).NotTo(HaveOccurred())
			Expect(warnings).To(BeNil())
		})
	})

	Describe("ValidateDelete", func() {
		It("should allow deletion without validation", func() {
			configMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      reconcilers.TierConfigMapName,
					Namespace: maasNamespace,
				},
			}

			warnings, err := validator.ValidateDelete(ctx, configMap)
			Expect(err).NotTo(HaveOccurred())
			Expect(warnings).To(BeNil())
		})
	})

	Describe("MAAS_NAMESPACE environment variable", func() {
		It("should use custom namespace from environment variable", func() {
			customNamespace := "custom-maas-namespace"
			GinkgoT().Setenv(reconcilers.MaasNamespaceEnvVar, customNamespace)

			// ConfigMap in custom namespace with duplicate levels should be validated
			configMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      reconcilers.TierConfigMapName,
					Namespace: customNamespace,
				},
				Data: map[string]string{
					"tiers": `
- name: "free"
  level: 10
- name: "premium"
  level: 10
`,
				},
			}

			warnings, err := validator.validate(configMap)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("duplicate levels found"))
			Expect(warnings).To(BeNil())
		})
	})
})
