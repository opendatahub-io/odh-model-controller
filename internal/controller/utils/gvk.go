package utils

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// GroupKind holds the GroupKind for resources we don't have direct dependency on (by means of golang API)
// but we still want to interact with them e.g. through unstructured objects.
//
// These are owned by the Open Data Hub operator, which serves several versions of them at once and
// retires old ones over time (DataScienceCluster v1 is already gone, v2 is deprecated in favour of v3).
// The version is therefore deliberately not part of this struct: it is resolved against the cluster at
// call time by ListUnstructuredByGroupKind.
var GroupKind = struct {
	DataScienceClusterInitialization,
	DataScienceCluster schema.GroupKind
}{
	DataScienceCluster: schema.GroupKind{
		Group: "datasciencecluster.opendatahub.io",
		Kind:  "DataScienceCluster",
	},
	DataScienceClusterInitialization: schema.GroupKind{
		Group: "dscinitialization.opendatahub.io",
		Kind:  "DSCInitialization",
	},
}

// ListUnstructuredByGroupKind lists resources of the given GroupKind using the version the API server
// prefers, instead of a version hardcoded at build time. This keeps the controller working against
// clusters that no longer serve older versions of an API.
//
// The returned list carries the resolved GroupVersionKind, so callers that need to read
// version-specific fields can inspect it.
func ListUnstructuredByGroupKind(ctx context.Context, cli client.Client, groupKind schema.GroupKind) (*unstructured.UnstructuredList, error) {
	// Passing no version asks for the one the API server prefers. The manager's RESTMapper
	// re-runs discovery for the group whenever it does not recognise the kind, so a version
	// introduced after start-up is picked up without restarting the controller.
	mapping, err := cli.RESTMapper().RESTMapping(groupKind)
	if err != nil {
		return nil, fmt.Errorf("not able to resolve the served API version of %s: %w", groupKind, err)
	}

	objectList := &unstructured.UnstructuredList{}
	objectList.SetGroupVersionKind(mapping.GroupVersionKind)

	if err := cli.List(ctx, objectList); err != nil {
		return nil, fmt.Errorf("not able to read %s: %w", mapping.GroupVersionKind, err)
	}

	return objectList, nil
}
