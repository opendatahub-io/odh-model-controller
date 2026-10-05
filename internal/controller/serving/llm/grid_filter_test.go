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
	"testing"

	kservev1alpha2 "github.com/kserve/kserve/pkg/apis/serving/v1alpha2"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func TestGridPreferredAttachmentModelName(t *testing.T) {
	for _, tc := range []struct {
		name      string
		modelName *string
		model     string
	}{
		{"nil model name", nil, "service"},
		{"empty model name", ptr.To(""), "service"},
		{"explicit model name", ptr.To("custom"), "custom"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &kservev1alpha2.LLMInferenceService{ObjectMeta: metav1.ObjectMeta{Namespace: "models", Name: "service"}}
			svc.Spec.Model.Name = tc.modelName
			participant := gridAttachment{Endpoint: "http://gateway/models/service", Model: tc.model}
			root := gridAttachment{Endpoint: "http://gateway/", Model: "publishers/models/models/" + tc.model}
			require.Equal(t, participant, *gridPreferredAttachment([]gridAttachment{root, participant}, svc))
		})
	}
}

func TestGridPreferredAttachmentHTTPS(t *testing.T) {
	for _, tc := range []struct {
		name, first, second, expected string
	}{
		{"participant", "http://a-gateway/models/service", "https://z-gateway/models/service", "https://z-gateway/models/service"},
		{"publisher", "http://gateway/publishers/models/models/service", "https://gateway/publishers/models/models/service", "https://gateway/publishers/models/models/service"},
		{"HTTP fallback", "http://z-gateway/models/service", "http://a-gateway/models/service", "http://a-gateway/models/service"},
		{"HTTPS lexical tie", "https://z-gateway/models/service", "https://a-gateway/models/service", "https://a-gateway/models/service"},
		{"publisher priority", "https://gateway/models/service", "http://gateway/publishers/models/models/service", "http://gateway/publishers/models/models/service"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &kservev1alpha2.LLMInferenceService{ObjectMeta: metav1.ObjectMeta{Namespace: "models", Name: "service"}}
			candidates := []gridAttachment{{Endpoint: tc.first, Model: svc.Name}, {Endpoint: tc.second, Model: svc.Name}}
			require.Equal(t, tc.expected, gridPreferredAttachment(candidates, svc).Endpoint)
		})
	}
}
