package gateway

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// FilterListeners returns GatewayRefs for each gateway+listener pair
// whose listener allows routes from the target namespace.
func FilterListeners(gateways []gatewayapiv1.Gateway, targetNS string, nsLabels map[string]string) []GatewayRef {
	var refs []GatewayRef
	for i := range gateways {
		gw := &gateways[i]
		status := ExtractStatus(gw)
		for _, listener := range gw.Spec.Listeners {
			if listenerAllowsNamespace(listener, gw.Namespace, targetNS, nsLabels) {
				refs = append(refs, GatewayRef{
					Name:        gw.Name,
					Namespace:   gw.Namespace,
					Listener:    string(listener.Name),
					Status:      status,
					DisplayName: gw.Annotations[AnnotationDisplayName],
					Description: gw.Annotations[AnnotationDescription],
				})
			}
		}
	}
	return refs
}

// NeedsNamespaceLabels returns true if any listener in any gateway uses
// a namespace selector, meaning we need to fetch the target namespace's labels.
func NeedsNamespaceLabels(gateways []gatewayapiv1.Gateway) bool {
	for i := range gateways {
		for _, listener := range gateways[i].Spec.Listeners {
			if listener.AllowedRoutes != nil &&
				listener.AllowedRoutes.Namespaces != nil &&
				listener.AllowedRoutes.Namespaces.From != nil &&
				*listener.AllowedRoutes.Namespaces.From == gatewayapiv1.NamespacesFromSelector {
				return true
			}
		}
	}
	return false
}

func listenerAllowsNamespace(l gatewayapiv1.Listener, gwNamespace, targetNS string, nsLabels map[string]string) bool {
	if l.AllowedRoutes == nil || l.AllowedRoutes.Namespaces == nil || l.AllowedRoutes.Namespaces.From == nil {
		// Default is "Same" per Gateway API spec
		return gwNamespace == targetNS
	}

	switch *l.AllowedRoutes.Namespaces.From {
	case gatewayapiv1.NamespacesFromAll:
		return true
	case gatewayapiv1.NamespacesFromSame:
		return gwNamespace == targetNS
	case gatewayapiv1.NamespacesFromSelector:
		if l.AllowedRoutes.Namespaces.Selector == nil {
			return false
		}
		selector, err := metav1.LabelSelectorAsSelector(l.AllowedRoutes.Namespaces.Selector)
		if err != nil {
			return false
		}
		return selector.Matches(labels.Set(nsLabels))
	default:
		return false
	}
}
