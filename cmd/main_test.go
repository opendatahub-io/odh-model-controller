package main

import (
	"fmt"
	"testing"

	pkgtls "github.com/opendatahub-io/odh-model-controller/pkg/tls"
)

// TestTLSStartupError pins the ProfileErr exit path: a recorded profile error
// must prevent manager startup under every adherence mode (the manager honors
// the profile unconditionally), while a clean result starts.
func TestTLSStartupError(t *testing.T) {
	if err := tlsStartupError(pkgtls.Result{ProfileErr: fmt.Errorf("custom profile has no usable ciphers")}); err == nil {
		t.Fatal("tlsStartupError() = nil for a recorded profile error, want fatal")
	}
	if err := tlsStartupError(pkgtls.Result{}); err != nil {
		t.Fatalf("tlsStartupError() = %v for a clean result, want nil", err)
	}
}
