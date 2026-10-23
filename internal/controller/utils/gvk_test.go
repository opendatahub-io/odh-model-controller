package utils

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestListUnstructuredByGroupKind(t *testing.T) {
	tests := []struct {
		name      string
		groupKind schema.GroupKind
		versions  []string
	}{
		{
			name:      "DataScienceCluster without v1",
			groupKind: GroupKind.DataScienceCluster,
			versions:  []string{"v3", "v2"},
		},
		{
			name:      "legacy DataScienceCluster",
			groupKind: GroupKind.DataScienceCluster,
			versions:  []string{"v1"},
		},
		{
			name:      "DSCInitialization without v1",
			groupKind: GroupKind.DataScienceClusterInitialization,
			versions:  []string{"v2"},
		},
		{
			name:      "legacy DSCInitialization",
			groupKind: GroupKind.DataScienceClusterInitialization,
			versions:  []string{"v1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			groupVersions := make([]schema.GroupVersion, 0, len(tt.versions))
			for _, version := range tt.versions {
				groupVersions = append(groupVersions, schema.GroupVersion{Group: tt.groupKind.Group, Version: version})
			}

			mapper := meta.NewDefaultRESTMapper(groupVersions)
			for _, version := range tt.versions {
				mapper.Add(tt.groupKind.WithVersion(version), meta.RESTScopeRoot)
			}

			preferredVersion := tt.versions[0]
			object := &unstructured.Unstructured{}
			object.SetGroupVersionKind(tt.groupKind.WithVersion(preferredVersion))
			object.SetName("test-resource")
			cli := fake.NewClientBuilder().WithRESTMapper(mapper).WithObjects(object).Build()

			list, err := ListUnstructuredByGroupKind(context.Background(), cli, tt.groupKind)
			require.NoError(t, err)
			require.Equal(t, tt.groupKind.WithVersion(preferredVersion).GroupVersion().String(), list.GetAPIVersion())
			require.Len(t, list.Items, 1)
			require.Equal(t, object.GetName(), list.Items[0].GetName())
		})
	}
}
