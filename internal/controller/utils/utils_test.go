package utils

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"
)

var _ = Describe("IsManagedByOdhController", func() {
	It("should return true when managed-by label is set correctly", func() {
		obj := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-resource",
				Namespace: "test-ns",
				Labels: map[string]string{
					"app.kubernetes.io/managed-by": "odh-model-controller",
				},
			},
		}
		Expect(IsManagedByOdhController(obj)).To(BeTrue())
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
		Expect(IsManagedByOdhController(obj)).To(BeFalse())
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
		Expect(IsManagedByOdhController(obj)).To(BeFalse())
	})

	It("should return false when labels are nil", func() {
		obj := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-resource",
				Namespace: "test-ns",
			},
		}
		Expect(IsManagedByOdhController(obj)).To(BeFalse())
	})

	It("should return false when labels are empty", func() {
		obj := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-resource",
				Namespace: "test-ns",
				Labels:    map[string]string{},
			},
		}
		Expect(IsManagedByOdhController(obj)).To(BeFalse())
	})
})

var _ = Describe("IsExplicitlyUnmanaged", func() {
	It("should return true when label is 'false'", func() {
		obj := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-resource",
				Namespace: "test-ns",
				Labels: map[string]string{
					constants.ODHManagedLabel: "false",
				},
			},
		}
		Expect(IsExplicitlyUnmanaged(obj)).To(BeTrue())
	})

	It("should return false when label is 'true'", func() {
		obj := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-resource",
				Namespace: "test-ns",
				Labels: map[string]string{
					constants.ODHManagedLabel: "true",
				},
			},
		}
		Expect(IsExplicitlyUnmanaged(obj)).To(BeFalse())
	})

	It("should return true when label is 'FALSE' (case insensitive)", func() {
		obj := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-resource",
				Namespace: "test-ns",
				Labels: map[string]string{
					constants.ODHManagedLabel: "FALSE",
				},
			},
		}
		Expect(IsExplicitlyUnmanaged(obj)).To(BeTrue())
	})

	It("should return false when label is 'TRUE' (case insensitive)", func() {
		obj := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-resource",
				Namespace: "test-ns",
				Labels: map[string]string{
					constants.ODHManagedLabel: "TRUE",
				},
			},
		}
		Expect(IsExplicitlyUnmanaged(obj)).To(BeFalse())
	})

	It("should return true when label is '  false  ' (with whitespace)", func() {
		obj := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-resource",
				Namespace: "test-ns",
				Labels: map[string]string{
					constants.ODHManagedLabel: "  false  ",
				},
			},
		}
		Expect(IsExplicitlyUnmanaged(obj)).To(BeTrue())
	})

	It("should return false when label doesn't exist", func() {
		obj := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-resource",
				Namespace: "test-ns",
				Labels: map[string]string{
					"some-other-label": "value",
				},
			},
		}
		Expect(IsExplicitlyUnmanaged(obj)).To(BeFalse())
	})

	It("should return false when labels are nil", func() {
		obj := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-resource",
				Namespace: "test-ns",
			},
		}
		Expect(IsExplicitlyUnmanaged(obj)).To(BeFalse())
	})

	It("should return false when label has invalid value", func() {
		obj := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-resource",
				Namespace: "test-ns",
				Labels: map[string]string{
					constants.ODHManagedLabel: "invalid-value",
				},
			},
		}
		Expect(IsExplicitlyUnmanaged(obj)).To(BeFalse())
	})
})

var _ = Describe("IsAuthorinoTLSBootstrapEnabled", func() {
	It("should return true when annotation is 'true'", func() {
		obj := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-gateway",
				Namespace: "test-ns",
				Annotations: map[string]string{
					constants.AuthorinoTLSBootstrapAnnotation: "true",
				},
			},
		}
		Expect(IsAuthorinoTLSBootstrapEnabled(obj)).To(BeTrue())
	})

	It("should return false when annotation is 'false'", func() {
		obj := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-gateway",
				Namespace: "test-ns",
				Annotations: map[string]string{
					constants.AuthorinoTLSBootstrapAnnotation: "false",
				},
			},
		}
		Expect(IsAuthorinoTLSBootstrapEnabled(obj)).To(BeFalse())
	})

	It("should return false when annotation doesn't exist", func() {
		obj := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-gateway",
				Namespace: "test-ns",
				Annotations: map[string]string{
					"some-other-annotation": "value",
				},
			},
		}
		Expect(IsAuthorinoTLSBootstrapEnabled(obj)).To(BeFalse())
	})

	It("should return false when annotations are nil", func() {
		obj := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-gateway",
				Namespace: "test-ns",
			},
		}
		Expect(IsAuthorinoTLSBootstrapEnabled(obj)).To(BeFalse())
	})

	It("should return false when object is nil", func() {
		Expect(IsAuthorinoTLSBootstrapEnabled(nil)).To(BeFalse())
	})

	It("should return false when annotation has invalid value", func() {
		obj := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-gateway",
				Namespace: "test-ns",
				Annotations: map[string]string{
					constants.AuthorinoTLSBootstrapAnnotation: "invalid-value",
				},
			},
		}
		Expect(IsAuthorinoTLSBootstrapEnabled(obj)).To(BeFalse())
	})
})

var _ = Describe("IsManagedByOpenDataHub", func() {
	It("should return true for resources with managed-by label and without opendatahub.io/managed label", func() {
		obj := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-resource",
				Namespace: "test-ns",
				Labels: map[string]string{
					"app.kubernetes.io/managed-by": "odh-model-controller",
					"some-label":                   "value",
				},
			},
		}
		Expect(IsManagedByOpenDataHub(obj)).To(BeTrue())
	})

	It("should return true when managed-by label exists and opendatahub.io/managed is true", func() {
		obj := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-resource",
				Namespace: "test-ns",
				Labels: map[string]string{
					"app.kubernetes.io/managed-by": "odh-model-controller",
					constants.ODHManagedLabel:      "true",
				},
			},
		}
		Expect(IsManagedByOpenDataHub(obj)).To(BeTrue())
	})

	It("should return false when opendatahub.io/managed is false even if managed-by label exists (opt-out)", func() {
		obj := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-resource",
				Namespace: "test-ns",
				Labels: map[string]string{
					"app.kubernetes.io/managed-by": "odh-model-controller",
					constants.ODHManagedLabel:      "false",
				},
			},
		}
		Expect(IsManagedByOpenDataHub(obj)).To(BeFalse())
	})

	It("should return false when opendatahub.io/managed is FALSE (case insensitive)", func() {
		obj := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-resource",
				Namespace: "test-ns",
				Labels: map[string]string{
					"app.kubernetes.io/managed-by": "odh-model-controller",
					constants.ODHManagedLabel:      "FALSE",
				},
			},
		}
		Expect(IsManagedByOpenDataHub(obj)).To(BeFalse())
	})

	It("should return false when opendatahub.io/managed is '  false  ' (with whitespace)", func() {
		obj := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-resource",
				Namespace: "test-ns",
				Labels: map[string]string{
					"app.kubernetes.io/managed-by": "odh-model-controller",
					constants.ODHManagedLabel:      "  false  ",
				},
			},
		}
		Expect(IsManagedByOpenDataHub(obj)).To(BeFalse())
	})

	It("should return false when managed-by label is missing", func() {
		obj := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-resource",
				Namespace: "test-ns",
				Labels: map[string]string{
					"some-label": "value",
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

var _ = Describe("ShouldCreateEnvoyFilterForGateway", func() {
	It("should return true for managed gateway (no managed label)", func() {
		gateway := &gatewayapiv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-gateway",
				Namespace: "test-ns",
			},
		}
		Expect(ShouldCreateEnvoyFilterForGateway(gateway)).To(BeTrue())
	})

	It("should return true for gateway with managed=true label", func() {
		gateway := &gatewayapiv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-gateway",
				Namespace: "test-ns",
				Labels: map[string]string{
					constants.ODHManagedLabel: "true",
				},
			},
		}
		Expect(ShouldCreateEnvoyFilterForGateway(gateway)).To(BeTrue())
	})

	It("should return false for unmanaged gateway without opt-in annotation", func() {
		gateway := &gatewayapiv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-gateway",
				Namespace: "test-ns",
				Labels: map[string]string{
					constants.ODHManagedLabel: "false",
				},
			},
		}
		Expect(ShouldCreateEnvoyFilterForGateway(gateway)).To(BeFalse())
	})

	It("should return true for unmanaged gateway WITH opt-in annotation set to true", func() {
		gateway := &gatewayapiv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-gateway",
				Namespace: "test-ns",
				Labels: map[string]string{
					constants.ODHManagedLabel: "false",
				},
				Annotations: map[string]string{
					constants.AuthorinoTLSBootstrapAnnotation: "true",
				},
			},
		}
		Expect(ShouldCreateEnvoyFilterForGateway(gateway)).To(BeTrue())
	})

	It("should return false for unmanaged gateway with opt-in annotation set to false", func() {
		gateway := &gatewayapiv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-gateway",
				Namespace: "test-ns",
				Labels: map[string]string{
					constants.ODHManagedLabel: "false",
				},
				Annotations: map[string]string{
					constants.AuthorinoTLSBootstrapAnnotation: "false",
				},
			},
		}
		Expect(ShouldCreateEnvoyFilterForGateway(gateway)).To(BeFalse())
	})
})
