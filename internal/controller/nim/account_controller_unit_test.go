package nim

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	v1 "github.com/opendatahub-io/odh-model-controller/api/nim/v1"
)

func TestModelListConfigMapKey(t *testing.T) {
	tests := []struct {
		name    string
		account *v1.Account
		want    *types.NamespacedName
	}{
		{
			name:    "missing reference",
			account: &v1.Account{ObjectMeta: metav1.ObjectMeta{Namespace: "account-ns"}},
		},
		{
			name: "defaults reference namespace",
			account: &v1.Account{
				ObjectMeta: metav1.ObjectMeta{Namespace: "account-ns"},
				Spec:       v1.AccountSpec{ModelListConfig: &corev1.ObjectReference{Name: "models"}},
			},
			want: &types.NamespacedName{Name: "models", Namespace: "account-ns"},
		},
		{
			name: "uses referenced namespace",
			account: &v1.Account{
				ObjectMeta: metav1.ObjectMeta{Namespace: "account-ns"},
				Spec:       v1.AccountSpec{ModelListConfig: &corev1.ObjectReference{Name: "models", Namespace: "model-ns"}},
			},
			want: &types.NamespacedName{Name: "models", Namespace: "model-ns"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := modelListConfigMapKey(test.account)
			if got == nil && test.want == nil {
				return
			}
			if got == nil || test.want == nil || *got != *test.want {
				t.Fatalf("modelListConfigMapKey() = %#v, want %#v", got, test.want)
			}
		})
	}
}
