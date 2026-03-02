package gateway

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// ExtractStatus derives a human-readable status from Gateway conditions.
// Returns "Ready" if both Accepted and Programmed are True,
// "NotReady" if either is False, or "Unknown" otherwise.
func ExtractStatus(gw *gatewayapiv1.Gateway) string {
	conditions := gw.Status.Conditions
	accepted := findCondition(conditions, "Accepted")
	programmed := findCondition(conditions, "Programmed")

	if accepted != nil && accepted.Status == metav1.ConditionTrue &&
		programmed != nil && programmed.Status == metav1.ConditionTrue {
		return "Ready"
	}
	if (accepted != nil && accepted.Status == metav1.ConditionFalse) ||
		(programmed != nil && programmed.Status == metav1.ConditionFalse) {
		return "NotReady"
	}
	return "Unknown"
}

func findCondition(conditions []metav1.Condition, condType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == condType {
			return &conditions[i]
		}
	}
	return nil
}