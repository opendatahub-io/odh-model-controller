package comparators

import (
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ResourceComparator interface {
	Compare(deployed client.Object, requested client.Object) bool
}
