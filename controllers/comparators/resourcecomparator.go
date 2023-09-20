package comparators

import (
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ResourceComparator would compare deployed & requested resource. It would retrun `true` if both resource are same else it would return `false`
type ResourceComparator interface {
	Compare(deployed client.Object, requested client.Object) bool
}
