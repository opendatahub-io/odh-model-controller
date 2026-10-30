package utils

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("ListUnstructuredByGroupKind", func() {
	ctx := context.Background()

	// serverPreferredVersion asks the API server which version of a group it prefers, which is
	// the version a client is expected to use. It is deliberately not hardcoded here: the Open
	// Data Hub operator retires DataScienceCluster and DSCInitialization versions over time.
	serverPreferredVersion := func(group string) string {
		discoveryClient, err := discovery.NewDiscoveryClientForConfig(testEnv.Config)
		Expect(err).NotTo(HaveOccurred())

		apiGroups, err := discoveryClient.ServerGroups()
		Expect(err).NotTo(HaveOccurred())

		for _, apiGroup := range apiGroups.Groups {
			if apiGroup.Name == group {
				return apiGroup.PreferredVersion.Version
			}
		}

		Fail(fmt.Sprintf("group %s is not served by the test API server", group))

		return ""
	}

	DescribeTable("lists resources using the version the cluster serves",
		func(groupKind schema.GroupKind, name string) {
			expectedVersion := serverPreferredVersion(groupKind.Group)

			object := &unstructured.Unstructured{}
			object.SetGroupVersionKind(groupKind.WithVersion(expectedVersion))
			object.SetName(name)
			Expect(testClient.Create(ctx, object)).To(Succeed())
			DeferCleanup(func() {
				Expect(client.IgnoreNotFound(testClient.Delete(ctx, object))).To(Succeed())
			})

			list, err := ListUnstructuredByGroupKind(ctx, testClient, groupKind)

			Expect(err).NotTo(HaveOccurred())
			Expect(list.GroupVersionKind().Version).To(Equal(expectedVersion))
			Expect(list.Items).To(HaveLen(1))
			Expect(list.Items[0].GetName()).To(Equal(name))
		},
		Entry("DataScienceCluster", GroupKind.DataScienceCluster, "test-dsc"),
		Entry("DSCInitialization", GroupKind.DataScienceClusterInitialization, "test-dsci"),
	)

	It("should fail when the group is not served by the cluster", func() {
		notServed := schema.GroupKind{Group: "missing.opendatahub.io", Kind: "Missing"}

		_, err := ListUnstructuredByGroupKind(ctx, testClient, notServed)

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("not able to resolve the served API version"))
	})
})
