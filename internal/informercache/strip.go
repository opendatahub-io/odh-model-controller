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
