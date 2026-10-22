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
	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
)

var (
	aiHubNamespaceField         = []string{"spec", "components", "aiHub", "instancesNamespace"}
	modelRegistryNamespaceField = []string{"spec", "components", "modelregistry", "registriesNamespace"}
)

// newDSC builds a DataScienceCluster of the given version carrying the namespace at the given path.
func newDSC(version string, namespaceField []string, namespace string) *unstructured.Unstructured {
	dsc := &unstructured.Unstructured{}
	dsc.SetGroupVersionKind(utils.GroupKind.DataScienceCluster.WithVersion(version))
	dsc.SetName("default-dsc")

	if namespaceField != nil {
		Expect(unstructured.SetNestedField(dsc.Object, namespace, namespaceField...)).To(Succeed())
	}

	return dsc
}

// newClientServing builds a client whose RESTMapper only knows the given versions, in preference
// order, mimicking a cluster that has retired the versions it does not list.
func newClientServing(versions []string, objects ...client.Object) client.Client {
	groupKind := utils.GroupKind.DataScienceCluster

	groupVersions := make([]schema.GroupVersion, 0, len(versions))
	for _, version := range versions {
		groupVersions = append(groupVersions, schema.GroupVersion{Group: groupKind.Group, Version: version})
	}

	mapper := meta.NewDefaultRESTMapper(groupVersions)
	for _, version := range versions {
		mapper.Add(groupKind.WithVersion(version), meta.RESTScopeRoot)
	}

	return fake.NewClientBuilder().WithRESTMapper(mapper).WithObjects(objects...).Build()
}

var _ = Describe("Model Registry reconciler", func() {
	Describe("resolving the Model Registry namespace from a DataScienceCluster", func() {
		DescribeTable("should read the field belonging to the served version",
			func(namespaceField []string) {
				dsc := newDSC("v3", namespaceField, "model-registries")

				namespace, err := modelRegistryNamespace(dsc)

				Expect(err).NotTo(HaveOccurred())
				Expect(namespace).To(Equal("model-registries"))
			},
			Entry("AI Hub field, as used by DataScienceCluster v3", aiHubNamespaceField),
			Entry("legacy Model Registry field, as used by v1 and v2", modelRegistryNamespaceField),
		)

		DescribeTable("should fail when no namespace is set",
			func(namespaceField []string, namespace string) {
				_, err := modelRegistryNamespace(newDSC("v3", namespaceField, namespace))

				Expect(err).To(MatchError(errGetMRNamespaceFromDSC))
			},
			Entry("neither field present", nil, ""),
			Entry("AI Hub field empty", aiHubNamespaceField, ""),
			Entry("legacy field empty", modelRegistryNamespaceField, ""),
		)
	})

	Describe("constructing the reconciler", func() {
		DescribeTable("should succeed against the versions a cluster serves",
			func(versions []string, namespaceField []string) {
				cli := newClientServing(versions, newDSC(versions[0], namespaceField, "model-registries"))

				reconciler, err := NewModelRegistryInferenceServiceReconciler(cli, logr.Discard(), false, "")

				Expect(err).NotTo(HaveOccurred())
				Expect(reconciler).NotTo(BeNil())
			},
			// RHOAI 3.6 no longer serves v1, which used to be hardcoded here.
			Entry("v3 only", []string{"v3"}, aiHubNamespaceField),
			Entry("v3 preferred, v2 still served", []string{"v3", "v2"}, aiHubNamespaceField),
			Entry("v2 only", []string{"v2"}, modelRegistryNamespaceField),
			Entry("legacy v1", []string{"v1"}, modelRegistryNamespaceField),
		)

		It("should fail when the DataScienceCluster API is not served at all", func() {
			cli := fake.NewClientBuilder().WithRESTMapper(meta.NewDefaultRESTMapper(nil)).Build()

			_, err := NewModelRegistryInferenceServiceReconciler(cli, logr.Discard(), false, "")

			Expect(err).To(MatchError(errGetDSC))
		})

		It("should fail when more than one DataScienceCluster exists", func() {
			first := newDSC("v3", aiHubNamespaceField, "model-registries")
			second := newDSC("v3", aiHubNamespaceField, "other-registries")
			second.SetName("second-dsc")

			cli := newClientServing([]string{"v3"}, first, second)

			_, err := NewModelRegistryInferenceServiceReconciler(cli, logr.Discard(), false, "")

			Expect(err).To(MatchError(errGetDSC))
		})
	})
})
