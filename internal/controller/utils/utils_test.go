package utils

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("IsManagedByOpenDataHub", func() {
	It("should return true for managed resources with correct labels", func() {
		obj := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-resource",
				Namespace: "test-ns",
				Labels: map[string]string{
					"app.kubernetes.io/managed-by": "odh-model-controller",
				},
			},
		}
		Expect(IsManagedByOpenDataHub(obj)).To(BeTrue())
	})

	It("should return true for managed resources with opendatahub.io/managed=true", func() {
		obj := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-resource",
				Namespace: "test-ns",
				Labels: map[string]string{
					"app.kubernetes.io/managed-by": "odh-model-controller",
					"opendatahub.io/managed":       "true",
				},
			},
		}
		Expect(IsManagedByOpenDataHub(obj)).To(BeTrue())
	})

	It("should return false when managed-by label is missing", func() {
		obj := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-resource",
				Namespace: "test-ns",
				Labels: map[string]string{
					"some-other-label": "value",
				},
			},
		}
		Expect(IsManagedByOpenDataHub(obj)).To(BeFalse())
	})

	It("should return false when managed-by label has wrong value", func() {
		obj := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-resource",
				Namespace: "test-ns",
				Labels: map[string]string{
					"app.kubernetes.io/managed-by": "other-controller",
				},
			},
		}
		Expect(IsManagedByOpenDataHub(obj)).To(BeFalse())
	})

	It("should return false when opendatahub.io/managed is explicitly false (opt-out)", func() {
		obj := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-resource",
				Namespace: "test-ns",
				Labels: map[string]string{
					"app.kubernetes.io/managed-by": "odh-model-controller",
					"opendatahub.io/managed":       "false",
				},
			},
		}
		Expect(IsManagedByOpenDataHub(obj)).To(BeFalse())
	})

	It("should return false when labels are nil", func() {
		obj := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-resource",
				Namespace: "test-ns",
			},
		}
		Expect(IsManagedByOpenDataHub(obj)).To(BeFalse())
	})

	It("should return false when labels are empty", func() {
		obj := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-resource",
				Namespace: "test-ns",
				Labels:    map[string]string{},
			},
		}
		Expect(IsManagedByOpenDataHub(obj)).To(BeFalse())
	})
})

var _ = Describe("MergeUserLabelsAndAnnotations", func() {
	Context("when merging labels", func() {
		It("should preserve user-defined labels not in desired resource", func() {
			desired := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"template-label": "template-value",
					},
				},
			}
			existing := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"template-label": "template-value",
						"user-label":     "user-value",
					},
				},
			}

			MergeUserLabelsAndAnnotations(desired, existing)

			Expect(desired.GetLabels()).To(HaveKeyWithValue("template-label", "template-value"))
			Expect(desired.GetLabels()).To(HaveKeyWithValue("user-label", "user-value"))
		})

		It("should not overwrite template-defined labels", func() {
			desired := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"shared-label": "desired-value",
					},
				},
			}
			existing := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"shared-label": "existing-value",
					},
				},
			}

			MergeUserLabelsAndAnnotations(desired, existing)

			Expect(desired.GetLabels()).To(HaveKeyWithValue("shared-label", "desired-value"))
		})

		It("should handle nil labels in desired resource", func() {
			desired := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{},
			}
			existing := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"user-label": "user-value",
					},
				},
			}

			MergeUserLabelsAndAnnotations(desired, existing)

			Expect(desired.GetLabels()).To(HaveKeyWithValue("user-label", "user-value"))
		})

		It("should handle nil labels in existing resource", func() {
			desired := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"template-label": "template-value",
					},
				},
			}
			existing := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{},
			}

			MergeUserLabelsAndAnnotations(desired, existing)

			Expect(desired.GetLabels()).To(HaveKeyWithValue("template-label", "template-value"))
			Expect(desired.GetLabels()).To(HaveLen(1))
		})
	})

	Context("when merging annotations", func() {
		It("should preserve user-defined annotations not in desired resource", func() {
			desired := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"template-annotation": "template-value",
					},
				},
			}
			existing := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"template-annotation": "template-value",
						"user-annotation":     "user-value",
					},
				},
			}

			MergeUserLabelsAndAnnotations(desired, existing)

			Expect(desired.GetAnnotations()).To(HaveKeyWithValue("template-annotation", "template-value"))
			Expect(desired.GetAnnotations()).To(HaveKeyWithValue("user-annotation", "user-value"))
		})

		It("should not overwrite template-defined annotations", func() {
			desired := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"shared-annotation": "desired-value",
					},
				},
			}
			existing := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"shared-annotation": "existing-value",
					},
				},
			}

			MergeUserLabelsAndAnnotations(desired, existing)

			Expect(desired.GetAnnotations()).To(HaveKeyWithValue("shared-annotation", "desired-value"))
		})

		It("should handle nil annotations in desired resource", func() {
			desired := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{},
			}
			existing := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"user-annotation": "user-value",
					},
				},
			}

			MergeUserLabelsAndAnnotations(desired, existing)

			Expect(desired.GetAnnotations()).To(HaveKeyWithValue("user-annotation", "user-value"))
		})

		It("should handle nil annotations in existing resource", func() {
			desired := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"template-annotation": "template-value",
					},
				},
			}
			existing := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{},
			}

			MergeUserLabelsAndAnnotations(desired, existing)

			Expect(desired.GetAnnotations()).To(HaveKeyWithValue("template-annotation", "template-value"))
			Expect(desired.GetAnnotations()).To(HaveLen(1))
		})
	})

	Context("when merging both labels and annotations", func() {
		It("should preserve both user-defined labels and annotations", func() {
			desired := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"template-label": "template-value",
					},
					Annotations: map[string]string{
						"template-annotation": "template-value",
					},
				},
			}
			existing := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"template-label": "template-value",
						"user-label":     "user-value",
					},
					Annotations: map[string]string{
						"template-annotation": "template-value",
						"user-annotation":     "user-value",
					},
				},
			}

			MergeUserLabelsAndAnnotations(desired, existing)

			Expect(desired.GetLabels()).To(HaveKeyWithValue("template-label", "template-value"))
			Expect(desired.GetLabels()).To(HaveKeyWithValue("user-label", "user-value"))
			Expect(desired.GetAnnotations()).To(HaveKeyWithValue("template-annotation", "template-value"))
			Expect(desired.GetAnnotations()).To(HaveKeyWithValue("user-annotation", "user-value"))
		})
	})
})
