package comparators

import (
	v1 "github.com/openshift/api/route/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type RouteComparator struct {
}

func (c *RouteComparator) Compare(deployed client.Object, requested client.Object) bool {
	deployedRoute := deployed.(*v1.Route)
	requestedRoute := requested.(*v1.Route)
	return equality.Semantic.DeepEqual(deployedRoute.Spec, requestedRoute.Spec) &&
		equality.Semantic.DeepEqual(deployedRoute.Annotations, requestedRoute.Annotations) &&
		equality.Semantic.DeepEqual(deployedRoute.Labels, requestedRoute.Labels)
}
