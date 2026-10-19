package utils

import "k8s.io/apimachinery/pkg/runtime/schema"

// GroupKind identifies resources for which we do not have Go API types. Their
// served versions are resolved from the client's RESTMapper before use.
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
