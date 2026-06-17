//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"testing"

	"github.com/opendatahub-io/odh-model-controller/server/test/e2e/testutil"
)

var env *testutil.Env

func TestMain(m *testing.M) {
	var err error
	env, err = testutil.NewEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize e2e environment: %v\n", err)
		os.Exit(1)
	}
	if env == nil {
		fmt.Fprintln(os.Stderr, "MODEL_SERVING_API_URL not set, skipping e2e tests")
		os.Exit(0)
	}

	os.Exit(m.Run())
}
