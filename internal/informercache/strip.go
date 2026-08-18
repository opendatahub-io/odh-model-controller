package informercache

import corev1 "k8s.io/api/core/v1"

// StripConfigMapData removes data payloads from cached ConfigMaps to reduce
// memory consumption. Watch events still trigger reconciliation; actual data
// is read via direct API calls (DisableFor).
func StripConfigMapData(i interface{}) (interface{}, error) {
	if cm, ok := i.(*corev1.ConfigMap); ok {
		cm.Data = nil
		cm.BinaryData = nil
		cm.Annotations = nil
		cm.SetManagedFields(nil)
	}
	return i, nil
}

// StripSecretData removes data payloads from cached Secrets to reduce memory
// consumption for ODH-managed secrets. Watch events still trigger reconciliation;
// payload reads go via direct API calls (DisableFor).
func StripSecretData(i interface{}) (interface{}, error) {
	if s, ok := i.(*corev1.Secret); ok {
		s.Data = nil
		s.StringData = nil
		s.Annotations = nil
		s.SetManagedFields(nil)
	}
	return i, nil
}
