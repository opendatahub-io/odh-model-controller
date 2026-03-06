//go:build e2e

package e2e

import (
	"net/http"
	"strings"
	"testing"
)

func TestMetricsEndpointAccessible(t *testing.T) {
	t.Parallel()

	resp, body := env.HTTPGet(t, env.MetricsURL+"/metrics", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", resp.StatusCode, http.StatusOK, body)
	}

	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(contentType, "text/plain") && !strings.Contains(contentType, "application/openmetrics-text") {
		t.Errorf("Content-Type = %q, want text/plain or application/openmetrics-text", contentType)
	}
}

func TestMetricsContainHTTPServerMetrics(t *testing.T) {
	t.Parallel()

	// Generate traffic on the main server so otelhttp records metrics.
	env.HTTPGet(t, env.ServerURL+"/healthz", "")

	resp, body := env.HTTPGet(t, env.MetricsURL+"/metrics", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", resp.StatusCode, http.StatusOK, body)
	}

	metrics := string(body)
	required := []string{
		"http_server_duration_milliseconds",
		"http_server_request_size_bytes_total",
		"http_server_response_size_bytes_total",
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

	resp, body := env.HTTPGet(t, env.MetricsURL+"/metrics", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", resp.StatusCode, http.StatusOK, body)
	}

	metrics := string(body)

	// The otelhttp middleware records method and status code attributes.
	if !strings.Contains(metrics, "http_method") {
		t.Error("expected http_method attribute in metrics output")
	}
	if !strings.Contains(metrics, "http_status_code") {
		t.Error("expected http_status_code attribute in metrics output")
	}
}

func TestMetricsServiceName(t *testing.T) {
	t.Parallel()

	resp, body := env.HTTPGet(t, env.MetricsURL+"/metrics", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", resp.StatusCode, http.StatusOK, body)
	}

	metrics := string(body)
	if !strings.Contains(metrics, `service_name="model-serving-api"`) {
		t.Error("expected target_info to contain service_name=\"model-serving-api\"")
	}
}