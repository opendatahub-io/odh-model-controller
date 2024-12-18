package utils

import "k8s.io/apimachinery/pkg/runtime/schema"

// GVK is a struct that holds the GroupVersionKind for resources we don't have direct dependency on (by means of golang API)
// but we still want to interact with them e.g. through unstructured objects.
var GVK = struct {
	DataScienceClusterInitialization,
	DataScienceCluster schema.GroupVersionKind
}{
	DataScienceCluster: schema.GroupVersionKind{
		Group:   "datasciencecluster.opendatahub.io",
		Version: "v1",
		Kind:    "DataScienceCluster",
	},
	DataScienceClusterInitialization: schema.GroupVersionKind{
		Group:   "dscinitialization.opendatahub.io",
		Version: "v1",
		Kind:    "DSCInitialization",
	},
}
