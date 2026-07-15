//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"testing"
)

var authEnv *authPolicyTestEnv

func TestMain(m *testing.M) {
	var err error
	authEnv, err = newTestEnv()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "failed to initialize e2e environment: %v\n", err)
		os.Exit(1)
	}

	code := m.Run()
	authEnv.close()
	os.Exit(code)
}
