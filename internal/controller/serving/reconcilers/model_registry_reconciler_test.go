package reconcilers

import (
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
)

func TestNewModelRegistryInferenceServiceReconcilerDSCVersions(t *testing.T) {
	tests := []struct {
		name           string
		versions       []string
		component      string
		namespaceField string
	}{
		{
			name:           "v3-only DSC uses AI Hub namespace",
			versions:       []string{"v3"},
			component:      "aiHub",
			namespaceField: "instancesNamespace",
		},
		{
			name:           "v3 preferred over v2 uses AI Hub namespace",
			versions:       []string{"v3", "v2"},
			component:      "aiHub",
			namespaceField: "instancesNamespace",
		},
		{
			name:           "v2 DSC uses Model Registry namespace",
			versions:       []string{"v2"},
			component:      "modelregistry",
			namespaceField: "registriesNamespace",
		},
		{
			name:           "v1 DSC uses Model Registry namespace",
			versions:       []string{"v1"},
			component:      "modelregistry",
			namespaceField: "registriesNamespace",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			groupKind := utils.GroupKind.DataScienceCluster
			groupVersions := make([]schema.GroupVersion, 0, len(tt.versions))
			for _, version := range tt.versions {
				groupVersions = append(groupVersions, schema.GroupVersion{Group: groupKind.Group, Version: version})
			}
			mapper := meta.NewDefaultRESTMapper(groupVersions)
			for _, version := range tt.versions {
				mapper.Add(groupKind.WithVersion(version), meta.RESTScopeRoot)
			}

			dsc := &unstructured.Unstructured{}
			dsc.SetGroupVersionKind(groupKind.WithVersion(tt.versions[0]))
			dsc.SetName("default-dsc")
			require.NoError(t, unstructured.SetNestedField(dsc.Object, "model-registries", "spec", "components", tt.component, tt.namespaceField))
			cli := fake.NewClientBuilder().WithRESTMapper(mapper).WithObjects(dsc).Build()

			reconciler, err := NewModelRegistryInferenceServiceReconciler(cli, logr.Discard(), false, "")
			require.NoError(t, err)
			require.NotNil(t, reconciler)
		})
	}
}
