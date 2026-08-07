//go:build e2e

package e2e

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// These tests exercise the gateway EnvoyFilter that derives the model-routing
// header (x-gateway-model-name) from the JSON request body. Unlike the
// publisher_path tests, which set the header explicitly, these POST a body and
// rely on the filter to extract the `model` field and populate the header before
// auth runs. They also probe the request-body buffer limit boundary.
//
// The filter buffers the entire request body (json_to_metadata + Lua body()) so
// the model field is available at the auth phase regardless of where it sits in
// the payload. Envoy's default per_connection_buffer_limit_bytes is 1 MiB; the
// EnvoyFilter raises it to 32 MiB (tunable via the Gateway annotation
// inference.opendatahub.io/request-body-buffer-limit-bytes).

// chatBody builds a small OpenAI-style chat body carrying the given model id.
func chatBody(model string) []byte {
	return []byte(fmt.Sprintf(`{"messages":[{"role":"user","content":"hi"}],"model":%q}`, model))
}

// chatBodyModelAtEnd builds a chat body of roughly fillerBytes size with the
// `model` field placed at the very END of the JSON object. This proves the
// filter buffers the whole body before extracting the model (rather than reading
// a prefix), which is the realistic case for long prompts / multimodal payloads.
func chatBodyModelAtEnd(model string, fillerBytes int) []byte {
	filler := strings.Repeat("a", fillerBytes)
	return []byte(fmt.Sprintf(`{"messages":[{"role":"user","content":%q}],"model":%q}`, filler, model))
}

// TestBodyModelInjectionAuthorized POSTs a body carrying the model id with NO
// explicit x-gateway-model-name header. The EnvoyFilter must extract `model`
// from the body and set the header, so model-access-header authorizes the caller
// (model-user has model RBAC) and the request routes to the model backend -> 200.
// This is the end-to-end proof that body -> header injection works.
func TestBodyModelInjectionAuthorized(t *testing.T) {
	t.Parallel()
	f := setupPublisherFixture(t)

	body := chatBody(fmt.Sprintf("publishers/%s/models/echo-server", f.ns))
	resp, _ := authEnv.gatewayPost(t, "/v1/chat/completions", f.modelUserToken, body, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 (body model injected -> model-access-header authorizes), got %d", resp.StatusCode)
	}
}

// TestBodyModelInjectionDenied POSTs the same body from a caller WITHOUT model
// RBAC. The filter injects the header, model-access-header runs a model SAR, and
// the caller is denied -> 403. Compare with TestV1PathExcludedFromInferenceAccess:
// a /v1/ request with NO derived header is authn-only (200). Getting 403 here
// proves the header was derived from the body and the model SAR ran.
func TestBodyModelInjectionDenied(t *testing.T) {
	t.Parallel()
	f := setupPublisherFixture(t)

	body := chatBody(fmt.Sprintf("publishers/%s/models/echo-server", f.ns))
	resp, _ := authEnv.gatewayPost(t, "/v1/chat/completions", f.noAccessToken, body, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 (body model injected, no model RBAC), got %d", resp.StatusCode)
	}
}

// TestLargeBodyModelAtEndDenied POSTs a ~4 MiB body with the model field at the
// very end, from a caller without model RBAC -> 403. This is the "large body,
// model at the end" case:
//   - 4 MiB is well over Envoy's 1 MiB default, so a clean 403 (rather than a
//     413) confirms the EnvoyFilter's raised buffer limit is active and the whole
//     body was buffered.
//   - the model sits at the end of the JSON, so a 403 (model SAR ran and denied)
//     proves the filter parsed the entire body, not a prefix.
//   - denied requests are backend-independent (the request never reaches echo)
//     and return a small response body.
func TestLargeBodyModelAtEndDenied(t *testing.T) {
	t.Parallel()
	f := setupPublisherFixture(t)

	const fourMiB = 4 << 20
	body := chatBodyModelAtEnd(fmt.Sprintf("publishers/%s/models/echo-server", f.ns), fourMiB)
	resp, _ := authEnv.gatewayPost(t, "/v1/chat/completions", f.noAccessToken, body, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 (4 MiB body buffered, model parsed at end, no model RBAC), got %d", resp.StatusCode)
	}
}

// TestLargeBodyOverBufferLimitRejected POSTs a body clearly over the 32 MiB
// buffer limit -> Envoy rejects it with 413 before the body is forwarded and
// before auth runs. We use a size well above the limit (40 MiB) rather than just
// over it: per_connection_buffer_limit_bytes is a soft high-watermark, so
// requests near the limit are non-deterministic (the ext_authz path can
// fail-closed with 500 just under the ceiling). A clearly-over body yields a
// deterministic 413.
func TestLargeBodyOverBufferLimitRejected(t *testing.T) {
	t.Parallel()
	f := setupPublisherFixture(t)

	const fortyMiB = 40 << 20
	body := chatBodyModelAtEnd(fmt.Sprintf("publishers/%s/models/echo-server", f.ns), fortyMiB)
	resp, _ := authEnv.gatewayPost(t, "/v1/chat/completions", f.modelUserToken, body, nil)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 (body over per_connection_buffer_limit_bytes), got %d", resp.StatusCode)
	}
}
