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

package llm

import (
	"context"
	"encoding/json"
	"testing"

	jsonpatch "github.com/evanphx/json-patch/v5"
	kservev1alpha2 "github.com/kserve/kserve/pkg/apis/serving/v1alpha2"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type gridFinalizerClient struct {
	client.Client
	patch     []byte
	patchType types.PatchType
}

func (c *gridFinalizerClient) Patch(_ context.Context, object client.Object, patch client.Patch, _ ...client.PatchOption) error {
	var err error
	c.patchType = patch.Type()
	c.patch, err = patch.Data(object)
	return err
}

func TestGridFinalizerPreservesUnknownFields(t *testing.T) {
	for _, add := range []bool{true, false} {
		name := "remove"
		if add {
			name = "add"
		}
		t.Run(name, func(t *testing.T) {
			otherFinalizer := "other.example.com/cleanup"
			finalizers := []string{otherFinalizer}
			if !add {
				finalizers = append(finalizers, gridFinalizer)
			}
			// Simulate an installed KServe version with fields absent from our Go types.
			stored := map[string]interface{}{
				"apiVersion": "serving.kserve.io/v1alpha2", "kind": "LLMInferenceService",
				"metadata": map[string]interface{}{"name": "model", "namespace": "tenant", "resourceVersion": "22", "finalizers": finalizers},
				"spec":     map[string]interface{}{"model": map[string]interface{}{"name": "model", "futureModelOption": "preserve"}, "futureSpec": map[string]interface{}{"enabled": true}},
				"status":   map[string]interface{}{"futureStatus": []interface{}{"preserve"}},
			}
			raw, err := json.Marshal(stored)
			require.NoError(t, err)
			svc := &kservev1alpha2.LLMInferenceService{}
			require.NoError(t, json.Unmarshal(raw, svc))
			patchClient := &gridFinalizerClient{}
			r := &GridInferenceProviderReconciler{Client: patchClient}
			require.NoError(t, r.setFinalizer(context.Background(), svc, add))
			require.Equal(t, types.MergePatchType, patchClient.patchType)
			expectedFinalizers := []string{otherFinalizer}
			if add {
				expectedFinalizers = append(expectedFinalizers, gridFinalizer)
			}
			expectedPatch, err := json.Marshal(map[string]interface{}{"metadata": map[string]interface{}{"resourceVersion": "22", "finalizers": expectedFinalizers}})
			require.NoError(t, err)
			require.JSONEq(t, string(expectedPatch), string(patchClient.patch))
			updated, err := jsonpatch.MergePatch(raw, patchClient.patch)
			require.NoError(t, err)
			stored["metadata"].(map[string]interface{})["finalizers"] = expectedFinalizers
			expected, err := json.Marshal(stored)
			require.NoError(t, err)
			require.JSONEq(t, string(expected), string(updated))
		})
	}
}
