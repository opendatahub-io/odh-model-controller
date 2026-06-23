/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package testutil provides helpers for constructing HardwareProfile test fixtures.
package testutil

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var hwpGVK = schema.GroupVersionKind{
	Group:   "infrastructure.opendatahub.io",
	Version: "v1",
	Kind:    "HardwareProfile",
}

// NewHardwareProfile creates an unstructured HardwareProfile CR for use in tests.
//
// The returned object has the correct GVK set and the provided spec embedded under
// the "spec" key. Pass a spec built with ResourceSpec, NodeSpec, or KueueSpec.
//
// Parameters:
//   - name: the CR name
//   - namespace: the CR namespace
//   - spec: the spec map to embed (nil produces a CR with an empty spec)
func NewHardwareProfile(name, namespace string, spec map[string]interface{}) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(hwpGVK)
	obj.SetName(name)
	obj.SetNamespace(namespace)
	if spec != nil {
		obj.Object["spec"] = spec
	}
	return obj
}

// ResourceSpec builds a HardwareProfile spec containing only resource identifiers.
//
// Each identifier argument is a two-element []string{name, defaultCount}, e.g.
// []string{"cpu", "4"} or []string{"nvidia.com/gpu", "2"}.
//
// Parameters:
//   - identifiers: variadic pairs of [resourceName, defaultCount]
//
// Returns a spec map suitable for passing to NewHardwareProfile.
func ResourceSpec(identifiers ...[]string) map[string]interface{} {
	ids := make([]interface{}, 0, len(identifiers))
	for _, id := range identifiers {
		if len(id) == 2 {
			ids = append(ids, map[string]interface{}{
				"identifier":   id[0],
				"defaultCount": id[1],
			})
		}
	}
	return map[string]interface{}{"identifiers": ids}
}

// NodeSpec builds a HardwareProfile spec with node scheduling configuration.
//
// Parameters:
//   - nodeSelector: map of label key → value pairs (may be nil)
//   - tolerations: list of toleration maps built with TolerationMap or TolerationMapWithSeconds (may be nil)
//
// Returns a spec map suitable for passing to NewHardwareProfile.
func NodeSpec(nodeSelector map[string]interface{}, tolerations []interface{}) map[string]interface{} {
	node := map[string]interface{}{}
	if nodeSelector != nil {
		node["nodeSelector"] = nodeSelector
	}
	if tolerations != nil {
		node["tolerations"] = tolerations
	}
	return map[string]interface{}{
		"schedulingSpec": map[string]interface{}{"node": node},
	}
}

// KueueSpec builds a HardwareProfile spec with Kueue scheduling configuration.
//
// Parameters:
//   - localQueueName: the Kueue local queue name to set
//
// Returns a spec map suitable for passing to NewHardwareProfile.
func KueueSpec(localQueueName string) map[string]interface{} {
	return map[string]interface{}{
		"schedulingSpec": map[string]interface{}{
			"kueue": map[string]interface{}{"localQueueName": localQueueName},
		},
	}
}

// TolerationMap creates a toleration map for use in NodeSpec.
//
// Parameters:
//   - key: the toleration key
//   - operator: the toleration operator (e.g. "Exists", "Equal")
//   - effect: the taint effect (e.g. "NoSchedule", "NoExecute")
func TolerationMap(key, operator, effect string) map[string]interface{} {
	return map[string]interface{}{
		"key":      key,
		"operator": operator,
		"effect":   effect,
	}
}

// TolerationMapWithSeconds creates a toleration map that includes a tolerationSeconds field.
//
// Parameters:
//   - key: the toleration key
//   - operator: the toleration operator
//   - effect: the taint effect
//   - tolerationSeconds: the toleration duration in seconds
func TolerationMapWithSeconds(key, operator, effect string, tolerationSeconds int64) map[string]interface{} {
	m := TolerationMap(key, operator, effect)
	m["tolerationSeconds"] = float64(tolerationSeconds)
	return m
}
