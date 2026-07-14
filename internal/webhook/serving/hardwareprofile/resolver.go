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

package hardwareprofile

import (
	"context"
	"fmt"
	"strconv"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// Annotation keys for hardware profile references on workload objects.
const (
	HardwareProfileAnnotationName      = "opendatahub.io/hardware-profile-name"
	HardwareProfileAnnotationNamespace = "opendatahub.io/hardware-profile-namespace"
)

// HardwareProfile CRD group and version.
const (
	HardwareProfileGroup   = "infrastructure.opendatahub.io"
	HardwareProfileVersion = "v1"
)

// KueueQueueNameLabel is the Kubernetes label used by Kueue to route workloads to a local queue.
const KueueQueueNameLabel = "kueue.x-k8s.io/queue-name"

// LLMIsvcMainContainerName is the required name for the primary container in an LLMInferenceService.
const LLMIsvcMainContainerName = "main"

// ResolvedProfile holds the scheduling and resource stanzas extracted from a HardwareProfile CR.
type ResolvedProfile struct {
	// Identifiers is the list of resource identifiers (CPU, memory, accelerators) to inject.
	Identifiers []ResourceIdentifier
	// NodeSelector entries to merge into the workload spec.
	NodeSelector map[string]string
	// Tolerations to prepend to the workload spec.
	Tolerations []corev1.Toleration
	// KueueQueueName is the local Kueue queue name; non-empty when scheduling.kueue is set.
	// When non-empty, node scheduling (NodeSelector/Tolerations) is not applied.
	KueueQueueName string
}

// ResourceIdentifier represents a single hardware resource from the HardwareProfile, with the
// quantity to use for both requests and limits (Guaranteed QoS).
type ResourceIdentifier struct {
	// Identifier is the resource name (e.g. "nvidia.com/gpu", "cpu", "memory").
	Identifier string
	// DefaultCount is the quantity applied to both requests and limits.
	DefaultCount resource.Quantity
}

// ProfileRef extracts the HardwareProfile name and namespace from workload annotations.
//
// Falls back to defaultNamespace when the namespace annotation is absent. Returns empty strings
// when the name annotation is absent.
//
// Parameters:
//   - annotations: the workload object's annotation map
//   - defaultNamespace: fallback namespace (typically the workload's own namespace)
//
// Returns the profile name and resolved namespace.
func ProfileRef(annotations map[string]string, defaultNamespace string) (name, namespace string) {
	name = annotations[HardwareProfileAnnotationName]
	namespace = annotations[HardwareProfileAnnotationNamespace]
	if namespace == "" {
		namespace = defaultNamespace
	}
	return
}

// ProfileChanged reports whether the HardwareProfile reference changed between the old and new
// object in an UPDATE admission request.
//
// Returns false on CREATE (no old object). Returns false when the old object had no HWP annotation
// (initial assignment is treated as a merge, not a profile switch). Returns true when either the
// profile name or namespace differs between old and new.
//
// Parameters:
//   - req: the admission request containing the old object for UPDATE operations
//   - newProfileName: the profile name from the new object's annotations
//   - newProfileNamespace: the resolved profile namespace from the new object's annotations
//
// Returns true when the profile switched (triggers a full clear before re-injection).
func ProfileChanged(req admission.Request, newProfileName, newProfileNamespace string) bool {
	if req.Operation == admissionv1.Create {
		return false
	}
	if req.OldObject.Raw == nil {
		return true
	}

	oldObj := &unstructured.Unstructured{}
	if err := oldObj.UnmarshalJSON(req.OldObject.Raw); err != nil {
		return true
	}

	oldAnnotations := oldObj.GetAnnotations()
	oldProfileName := oldAnnotations[HardwareProfileAnnotationName]
	if oldProfileName == "" {
		// Initial HWP assignment — treat as a merge, not a profile switch.
		return false
	}

	oldProfileNamespace := oldAnnotations[HardwareProfileAnnotationNamespace]
	if oldProfileNamespace == "" {
		oldProfileNamespace = oldObj.GetNamespace()
	}

	return oldProfileName != newProfileName || oldProfileNamespace != newProfileNamespace
}

// Resolve fetches a HardwareProfile CR and returns its parsed scheduling and resource stanzas.
//
// Uses the controller-runtime unstructured client to avoid importing opendatahub-operator types.
// Returns nil, nil when name is empty (no HWP configured). Returns a wrapped error (including
// k8serr.IsNotFound errors) when the CR cannot be fetched.
//
// Parameters:
//   - ctx: request context
//   - c: controller-runtime client with read access to HardwareProfile CRs
//   - name: the HardwareProfile CR name
//   - namespace: the namespace containing the HardwareProfile CR
//
// Returns the parsed ResolvedProfile or an error that callers use to block admission.
func Resolve(ctx context.Context, c client.Client, name, namespace string) (*ResolvedProfile, error) {
	if name == "" {
		return nil, nil
	}

	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   HardwareProfileGroup,
		Version: HardwareProfileVersion,
		Kind:    "HardwareProfile",
	})
	if err := c.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, obj); err != nil {
		return nil, fmt.Errorf("failed fetching HardwareProfile %s/%s: %w", namespace, name, err)
	}

	return parseProfile(obj)
}

// parseProfile extracts scheduling and resource stanzas from an unstructured HardwareProfile object.
//
// Reads spec.identifiers, spec.scheduling.kueue.localQueueName, spec.scheduling.node.nodeSelector,
// and spec.scheduling.node.tolerations. Kueue and Node scheduling are mutually exclusive:
// when a Kueue queue name is found, node scheduling fields are not populated.
//
// Parameters:
//   - obj: the HardwareProfile CR as an unstructured object
//
// Returns the parsed ResolvedProfile or an error if a field cannot be parsed.
func parseProfile(obj *unstructured.Unstructured) (*ResolvedProfile, error) {
	profile := &ResolvedProfile{}

	identifiers, _, _ := unstructured.NestedSlice(obj.Object, "spec", "identifiers")
	for _, raw := range identifiers {
		idMap, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		identifier, _, _ := unstructured.NestedString(idMap, "identifier")
		if identifier == "" {
			continue
		}
		qty, err := parseQuantity(idMap["defaultCount"])
		if err != nil {
			return nil, fmt.Errorf("failed parsing defaultCount for identifier %q: %w", identifier, err)
		}
		profile.Identifiers = append(profile.Identifiers, ResourceIdentifier{
			Identifier:   identifier,
			DefaultCount: qty,
		})
	}

	// Kueue scheduling takes precedence; when present, node scheduling is not read.
	// Note: the HardwareProfile CRD field is `spec.scheduling` (JSON tag "scheduling"),
	// not "schedulingSpec" (the Go struct field name).
	queueName, _, _ := unstructured.NestedString(obj.Object, "spec", "scheduling", "kueue", "localQueueName")
	if queueName != "" {
		profile.KueueQueueName = queueName
		return profile, nil
	}

	// Node scheduling.
	nodeSelector, _, _ := unstructured.NestedStringMap(obj.Object, "spec", "scheduling", "node", "nodeSelector")
	profile.NodeSelector = nodeSelector

	rawTols, _, _ := unstructured.NestedSlice(obj.Object, "spec", "scheduling", "node", "tolerations")
	for _, rt := range rawTols {
		tolMap, ok := rt.(map[string]any)
		if !ok {
			continue
		}
		profile.Tolerations = append(profile.Tolerations, parseToleration(tolMap))
	}

	return profile, nil
}

// parseQuantity converts a raw JSON value (float64, int64, or string) to a resource.Quantity.
// JSON numbers are parsed as integer quantities; strings are parsed as quantity strings (e.g. "500m").
func parseQuantity(raw any) (resource.Quantity, error) {
	switch v := raw.(type) {
	case int64:
		return *resource.NewQuantity(v, resource.DecimalSI), nil
	case float64:
		return *resource.NewQuantity(int64(v), resource.DecimalSI), nil
	case string:
		return resource.ParseQuantity(v)
	default:
		return resource.Quantity{}, fmt.Errorf("unexpected type %T for resource quantity", raw)
	}
}

// parseToleration constructs a corev1.Toleration from a raw unstructured map.
func parseToleration(m map[string]any) corev1.Toleration {
	var t corev1.Toleration
	t.Key, _ = m["key"].(string)
	t.Operator = corev1.TolerationOperator(getString(m, "operator"))
	t.Value, _ = m["value"].(string)
	t.Effect = corev1.TaintEffect(getString(m, "effect"))
	if v, ok := m["tolerationSeconds"]; ok {
		switch ts := v.(type) {
		case int64:
			t.TolerationSeconds = &ts
		case float64:
			i := int64(ts)
			t.TolerationSeconds = &i
		}
	}
	return t
}

// getString retrieves a string value from a map by key.
func getString(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

// TolerationKey returns a composite key that uniquely identifies a toleration for deduplication
// and surgical removal. Includes key, operator, value, effect, and tolerationSeconds.
//
// Parameters:
//   - tol: the toleration to generate a key for
//
// Returns a string key that distinguishes tolerations with identical key/operator/effect but
// different values or tolerationSeconds.
func TolerationKey(tol corev1.Toleration) string {
	ts := ""
	if tol.TolerationSeconds != nil {
		ts = strconv.FormatInt(*tol.TolerationSeconds, 10)
	}
	return fmt.Sprintf("%s:%s:%s:%s:%s",
		tol.Key, string(tol.Operator), tol.Value, string(tol.Effect), ts)
}
