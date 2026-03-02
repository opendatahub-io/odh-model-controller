package gateway

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestExtractStatus(t *testing.T) {
	tests := []struct {
		name       string
		conditions []metav1.Condition
		want       string
	}{
		{
			name: "both Accepted and Programmed true returns Ready",
			conditions: []metav1.Condition{
				{Type: "Accepted", Status: metav1.ConditionTrue},
				{Type: "Programmed", Status: metav1.ConditionTrue},
			},
			want: StatusReady,
		},
		{
			name: "Accepted false returns NotReady",
			conditions: []metav1.Condition{
				{Type: "Accepted", Status: metav1.ConditionFalse},
				{Type: "Programmed", Status: metav1.ConditionTrue},
			},
			want: StatusNotReady,
		},
		{
			name: "Programmed false returns NotReady",
			conditions: []metav1.Condition{
				{Type: "Accepted", Status: metav1.ConditionTrue},
				{Type: "Programmed", Status: metav1.ConditionFalse},
			},
			want: StatusNotReady,
		},
		{
			name: "both false returns NotReady",
			conditions: []metav1.Condition{
				{Type: "Accepted", Status: metav1.ConditionFalse},
				{Type: "Programmed", Status: metav1.ConditionFalse},
			},
			want: StatusNotReady,
		},
		{
			name:       "no conditions returns Unknown",
			conditions: nil,
			want:       StatusUnknown,
		},
		{
			name: "only Accepted true returns Unknown",
			conditions: []metav1.Condition{
				{Type: "Accepted", Status: metav1.ConditionTrue},
			},
			want: "Unknown",
		},
		{
			name: "only Programmed true returns Unknown",
			conditions: []metav1.Condition{
				{Type: "Programmed", Status: metav1.ConditionTrue},
			},
			want: "Unknown",
		},
		{
			name: "Accepted unknown status returns Unknown",
			conditions: []metav1.Condition{
				{Type: "Accepted", Status: metav1.ConditionUnknown},
				{Type: "Programmed", Status: metav1.ConditionTrue},
			},
			want: "Unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gw := &gatewayapiv1.Gateway{
				Status: gatewayapiv1.GatewayStatus{
					Conditions: tt.conditions,
				},
			}
			got := ExtractStatus(gw)
			if got != tt.want {
				t.Errorf("ExtractStatus() = %q, want %q", got, tt.want)
			}
		})
	}
}