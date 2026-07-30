package reconcilers

import (
	"fmt"

	kuadrantv1 "github.com/kuadrant/kuadrant-operator/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var _ = Describe("isAuthPolicyNoMatchError", func() {
	It("should return true for NoKindMatchError with AuthPolicy GVK", func() {
		err := &meta.NoKindMatchError{
			GroupKind:        schema.GroupKind{Group: kuadrantv1.GroupVersion.Group, Kind: "AuthPolicy"},
			SearchedVersions: []string{"v1"},
		}
		Expect(isAuthPolicyNoMatchError(err)).To(BeTrue())
	})

	It("should return true for NoResourceMatchError with AuthPolicy GVR", func() {
		err := &meta.NoResourceMatchError{
			PartialResource: schema.GroupVersionResource{Group: kuadrantv1.GroupVersion.Group, Resource: "authpolicies"},
		}
		Expect(isAuthPolicyNoMatchError(err)).To(BeTrue())
	})

	It("should return true for wrapped NoKindMatchError", func() {
		inner := &meta.NoKindMatchError{
			GroupKind:        schema.GroupKind{Group: kuadrantv1.GroupVersion.Group, Kind: "AuthPolicy"},
			SearchedVersions: []string{"v1"},
		}
		err := fmt.Errorf("reconciling auth policy: %w", inner)
		Expect(isAuthPolicyNoMatchError(err)).To(BeTrue())
	})

	It("should return false for NoKindMatchError with different group", func() {
		err := &meta.NoKindMatchError{
			GroupKind:        schema.GroupKind{Group: "monitoring.coreos.com", Kind: "ServiceMonitor"},
			SearchedVersions: []string{"v1"},
		}
		Expect(isAuthPolicyNoMatchError(err)).To(BeFalse())
	})

	It("should return false for NoKindMatchError with same group but different kind", func() {
		err := &meta.NoKindMatchError{
			GroupKind:        schema.GroupKind{Group: kuadrantv1.GroupVersion.Group, Kind: "RateLimitPolicy"},
			SearchedVersions: []string{"v1"},
		}
		Expect(isAuthPolicyNoMatchError(err)).To(BeFalse())
	})

	It("should return false for unrelated errors", func() {
		Expect(isAuthPolicyNoMatchError(fmt.Errorf("connection refused"))).To(BeFalse())
	})
})
