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
			Expect(err.Error()).To(ContainSubstring("failed to parse tier configuration"))
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
					Labels: map[string]string{
						TierMappingLabel: TierMappingLabelValue,
					},
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

	Describe("validateTierNames", func() {
		It("should pass validation when tier names are valid DNS-1123 labels", func() {
			configMap := &corev1.ConfigMap{
				Data: map[string]string{
					"tiers": `
- name: free
  level: 10
  groups: ["free-users"]
- name: premium-tier
  level: 20
  groups: ["premium-users"]
- name: enterprise123
  level: 30
  groups: ["enterprise-users"]
`,
				},
			}

			err := validateTierNames(configMap)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should fail validation when tier name contains uppercase letters", func() {
			configMap := &corev1.ConfigMap{
				Data: map[string]string{
					"tiers": `
- name: Free
  level: 10
  groups: ["free-users"]
`,
				},
			}

			err := validateTierNames(configMap)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid tier name 'Free'"))
		})

		It("should fail validation when tier name contains special characters", func() {
			configMap := &corev1.ConfigMap{
				Data: map[string]string{
					"tiers": `
- name: free_tier
  level: 10
  groups: ["free-users"]
`,
				},
			}

			err := validateTierNames(configMap)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid tier name 'free_tier'"))
		})

		It("should fail validation when tier name starts with a hyphen", func() {
			configMap := &corev1.ConfigMap{
				Data: map[string]string{
					"tiers": `
- name: -free
  level: 10
  groups: ["free-users"]
`,
				},
			}

			err := validateTierNames(configMap)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid tier name '-free'"))
		})

		It("should fail validation when tier name ends with a hyphen", func() {
			configMap := &corev1.ConfigMap{
				Data: map[string]string{
					"tiers": `
- name: free-
  level: 10
  groups: ["free-users"]
`,
				},
			}

			err := validateTierNames(configMap)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid tier name 'free-'"))
		})

		It("should fail validation when tier name is too long (>63 characters)", func() {
			configMap := &corev1.ConfigMap{
				Data: map[string]string{
					"tiers": `
- name: this-tier-name-is-way-too-long-and-exceeds-the-sixty-three-character-limit
  level: 10
  groups: ["free-users"]
`,
				},
			}

			err := validateTierNames(configMap)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid tier name"))
		})

		It("should fail validation when tier name is empty", func() {
			configMap := &corev1.ConfigMap{
				Data: map[string]string{
					"tiers": `
- name: ""
  level: 10
  groups: ["free-users"]
`,
				},
			}

			err := validateTierNames(configMap)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid tier name"))
		})

		It("should pass validation when tiers key is missing", func() {
			configMap := &corev1.ConfigMap{
				Data: map[string]string{
					"other-key": "some-value",
				},
			}

			err := validateTierNames(configMap)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("validateTierNamesNotRemoved", func() {
		It("should pass validation when tiers are unchanged", func() {
			oldConfigMap := &corev1.ConfigMap{
				Data: map[string]string{
					"tiers": `
- name: free
  level: 10
- name: premium
  level: 20
`,
				},
			}
			newConfigMap := &corev1.ConfigMap{
				Data: map[string]string{
					"tiers": `
- name: free
  level: 10
- name: premium
  level: 20
`,
				},
			}

			err := validateTierNamesNotRemoved(oldConfigMap, newConfigMap)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should pass validation when new tiers are added", func() {
			oldConfigMap := &corev1.ConfigMap{
				Data: map[string]string{
					"tiers": `
- name: free
  level: 10
`,
				},
			}
			newConfigMap := &corev1.ConfigMap{
				Data: map[string]string{
					"tiers": `
- name: free
  level: 10
- name: premium
  level: 20
`,
				},
			}

			err := validateTierNamesNotRemoved(oldConfigMap, newConfigMap)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should fail validation when a tier is removed", func() {
			oldConfigMap := &corev1.ConfigMap{
				Data: map[string]string{
					"tiers": `
- name: free
  level: 10
- name: premium
  level: 20
`,
				},
			}
			newConfigMap := &corev1.ConfigMap{
				Data: map[string]string{
					"tiers": `
- name: free
  level: 10
`,
				},
			}

			err := validateTierNamesNotRemoved(oldConfigMap, newConfigMap)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("tier 'premium' cannot be removed or renamed"))
		})

		It("should fail validation when a tier is renamed", func() {
			oldConfigMap := &corev1.ConfigMap{
				Data: map[string]string{
					"tiers": `
- name: free
  level: 10
`,
				},
			}
			newConfigMap := &corev1.ConfigMap{
				Data: map[string]string{
					"tiers": `
- name: basic
  level: 10
`,
				},
			}

			err := validateTierNamesNotRemoved(oldConfigMap, newConfigMap)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("tier 'free' cannot be removed or renamed"))
		})

		It("should pass validation when old ConfigMap has no tiers key", func() {
			oldConfigMap := &corev1.ConfigMap{
				Data: map[string]string{
					"other-key": "value",
				},
			}
			newConfigMap := &corev1.ConfigMap{
				Data: map[string]string{
					"tiers": `
- name: free
  level: 10
`,
				},
			}

			err := validateTierNamesNotRemoved(oldConfigMap, newConfigMap)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should pass validation when both ConfigMaps have no tiers key", func() {
			oldConfigMap := &corev1.ConfigMap{
				Data: map[string]string{
					"other-key": "value",
				},
			}
			newConfigMap := &corev1.ConfigMap{
				Data: map[string]string{
					"other-key": "new-value",
				},
			}

			err := validateTierNamesNotRemoved(oldConfigMap, newConfigMap)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("ValidateUpdate integration", func() {
		It("should reject tier removal on update for tier ConfigMap", func() {
			oldConfigMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      reconcilers.TierConfigMapName,
					Namespace: maasNamespace,
					Labels: map[string]string{
						TierMappingLabel: TierMappingLabelValue,
					},
				},
				Data: map[string]string{
					"tiers": `
- name: free
  level: 10
- name: premium
  level: 20
`,
				},
			}
			newConfigMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      reconcilers.TierConfigMapName,
					Namespace: maasNamespace,
					Labels: map[string]string{
						TierMappingLabel: TierMappingLabelValue,
					},
				},
				Data: map[string]string{
					"tiers": `
- name: free
  level: 10
`,
				},
			}

			warnings, err := validator.ValidateUpdate(ctx, oldConfigMap, newConfigMap)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("tier 'premium' cannot be removed or renamed"))
			Expect(warnings).To(BeNil())
		})

		It("should allow tier addition on update", func() {
			oldConfigMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      reconcilers.TierConfigMapName,
					Namespace: maasNamespace,
					Labels: map[string]string{
						TierMappingLabel: TierMappingLabelValue,
					},
				},
				Data: map[string]string{
					"tiers": `
- name: free
  level: 10
`,
				},
			}
			newConfigMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      reconcilers.TierConfigMapName,
					Namespace: maasNamespace,
					Labels: map[string]string{
						TierMappingLabel: TierMappingLabelValue,
					},
				},
				Data: map[string]string{
					"tiers": `
- name: free
  level: 10
- name: premium
  level: 20
`,
				},
			}

			warnings, err := validator.ValidateUpdate(ctx, oldConfigMap, newConfigMap)
			Expect(err).NotTo(HaveOccurred())
			Expect(warnings).To(BeNil())
		})

	})

	Describe("ValidateCreate with name validation", func() {
		It("should reject invalid tier names on create", func() {
			configMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      reconcilers.TierConfigMapName,
					Namespace: maasNamespace,
					Labels: map[string]string{
						TierMappingLabel: TierMappingLabelValue,
					},
				},
				Data: map[string]string{
					"tiers": `
- name: Invalid_Name
  level: 10
`,
				},
			}

			warnings, err := validator.ValidateCreate(ctx, configMap)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid tier name 'Invalid_Name'"))
			Expect(warnings).To(BeNil())
		})

		It("should accept valid tier names on create", func() {
			configMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      reconcilers.TierConfigMapName,
					Namespace: maasNamespace,
					Labels: map[string]string{
						TierMappingLabel: TierMappingLabelValue,
					},
				},
				Data: map[string]string{
					"tiers": `
- name: valid-name
  level: 10
`,
				},
			}

			warnings, err := validator.ValidateCreate(ctx, configMap)
			Expect(err).NotTo(HaveOccurred())
			Expect(warnings).To(BeNil())
		})
	})
})
