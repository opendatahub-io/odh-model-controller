//go:build e2e

package e2e

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/opendatahub-io/odh-model-controller/server/test/e2e/testutil"
	"k8s.io/apimachinery/pkg/util/wait"
)

// fetchMetrics polls the metrics endpoint until it returns 200 OK, handling
// transient failures such as route propagation delay.
func fetchMetrics(t *testing.T) string {
	t.Helper()
	var lastBody []byte
	var lastStatus int
	err := wait.PollUntilContextTimeout(context.Background(), testutil.PollInterval, testutil.PollTimeout, true,
		func(ctx context.Context) (bool, error) {
			resp, body := env.HTTPGet(t, env.MetricsURL+"/metrics", "")
			lastBody = body
			lastStatus = resp.StatusCode
			return resp.StatusCode == http.StatusOK, nil
		})
	if err != nil {
		t.Fatalf("timed out waiting for metrics endpoint: status=%d body=%s", lastStatus, lastBody)
	}
	return string(lastBody)
}

func TestMetricsEndpointAccessible(t *testing.T) {
	t.Parallel()

	metrics := fetchMetrics(t)

	if !strings.Contains(metrics, "go_") {
		t.Error("expected Go runtime metrics in /metrics output")
	}
}

func TestMetricsContainHTTPServerMetrics(t *testing.T) {
	t.Parallel()

	// Generate traffic on the main server so otelhttp records metrics.
	env.HTTPGet(t, env.ServerURL+"/healthz", "")

	metrics := fetchMetrics(t)
	required := []string{
		"http_server_request_duration_seconds",
		"http_server_request_body_size_bytes_count",
		"http_server_response_body_size_bytes_count",
	}
	for _, m := range required {
		if !strings.Contains(metrics, m) {
			t.Errorf("expected metric %q not found in /metrics output", m)
		}
	}
}

func TestMetricsRecordRouteAttributes(t *testing.T) {
	t.Parallel()

	// Hit a known route to ensure it's recorded with route attributes.
	env.HTTPGet(t, env.ServerURL+"/healthz", "")

	metrics := fetchMetrics(t)

	// The otelhttp middleware records method and status code attributes.
	if !strings.Contains(metrics, "http_request_method") {
		t.Error("expected http_method attribute in metrics output")
	}
	if !strings.Contains(metrics, "http_response_status_code") {
		t.Error("expected http_status_code attribute in metrics output")
	}
}

func TestMetricsServiceName(t *testing.T) {
	t.Parallel()

	metrics := fetchMetrics(t)
	if !strings.Contains(metrics, `service_name="model-serving-api"`) {
		t.Error("expected target_info to contain service_name=\"model-serving-api\"")
	}
}
