package main

import "testing"

func TestWebhookSetupNamesForMode(t *testing.T) {
	t.Parallel()

	xksNames := webhookSetupNames(true)
	wantXKS := []string{"LLMInferenceService"}
	if len(xksNames) != len(wantXKS) {
		t.Fatalf("xKS webhook count = %d, want %d (%v)", len(xksNames), len(wantXKS), xksNames)
	}
	for i, name := range wantXKS {
		if xksNames[i] != name {
			t.Fatalf("xKS webhook[%d] = %q, want %q", i, xksNames[i], name)
		}
	}

	fullNames := webhookSetupNames(false)
	wantFull := []string{
		"Pod",
		"NIMAccount",
		"InferenceService",
		"InferenceGraph",
		"LLMInferenceService",
	}
	if len(fullNames) != len(wantFull) {
		t.Fatalf("full webhook count = %d, want %d (%v)", len(fullNames), len(wantFull), fullNames)
	}
	for i, name := range wantFull {
		if fullNames[i] != name {
			t.Fatalf("full webhook[%d] = %q, want %q", i, fullNames[i], name)
		}
	}
}
