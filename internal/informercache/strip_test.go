package informercache

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestStripConfigMapData(t *testing.T) {
	t.Run("strips data payloads and annotations", func(t *testing.T) {
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cm",
				Namespace: "test-ns",
				Annotations: map[string]string{
					"example.com/large": "payload",
				},
				ManagedFields: []metav1.ManagedFieldsEntry{{Manager: "kubectl"}},
			},
			Data: map[string]string{
				"key": "value",
			},
			BinaryData: map[string][]byte{
				"bin": []byte("data"),
			},
		}

		out, err := StripConfigMapData(cm)
		if err != nil {
			t.Fatalf("StripConfigMapData() error = %v", err)
		}

		stripped, ok := out.(*corev1.ConfigMap)
		if !ok {
			t.Fatalf("StripConfigMapData() returned %T, want *corev1.ConfigMap", out)
		}
		if stripped.Data != nil {
			t.Fatal("expected Data to be nil")
		}
		if stripped.BinaryData != nil {
			t.Fatal("expected BinaryData to be nil")
		}
		if stripped.Annotations != nil {
			t.Fatal("expected Annotations to be nil")
		}
		if stripped.GetManagedFields() != nil {
			t.Fatal("expected ManagedFields to be nil")
		}
		if stripped.Name != "test-cm" || stripped.Namespace != "test-ns" {
			t.Fatalf("metadata preserved: got %s/%s", stripped.Namespace, stripped.Name)
		}
	})

	t.Run("strips secret data payloads and annotations", func(t *testing.T) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-secret",
				Namespace: "test-ns",
				Annotations: map[string]string{
					"example.com/large": "payload",
				},
				ManagedFields: []metav1.ManagedFieldsEntry{{Manager: "kubectl"}},
			},
			Data: map[string][]byte{
				"key": []byte("value"),
			},
			StringData: map[string]string{
				"plain": "text",
			},
		}

		out, err := StripSecretData(secret)
		if err != nil {
			t.Fatalf("StripSecretData() error = %v", err)
		}

		stripped, ok := out.(*corev1.Secret)
		if !ok {
			t.Fatalf("StripSecretData() returned %T, want *corev1.Secret", out)
		}
		if stripped.Data != nil || stripped.StringData != nil || stripped.Annotations != nil {
			t.Fatal("expected secret payloads and annotations to be nil")
		}
		if stripped.GetManagedFields() != nil {
			t.Fatal("expected ManagedFields to be nil")
		}
	})

	t.Run("passes through non-ConfigMap objects", func(t *testing.T) {
		secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "s"}}
		out, err := StripConfigMapData(secret)
		if err != nil {
			t.Fatalf("StripConfigMapData() error = %v", err)
		}
		if out != secret {
			t.Fatal("expected original object to be returned unchanged")
		}
	})
}
