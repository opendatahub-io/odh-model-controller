package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuth(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := TokenFromContext(r.Context())
		_, _ = w.Write([]byte(token))
	})

	handler := Auth(inner)

	tests := []struct {
		name           string
		authHeader     string
		wantStatus     int
		wantBodyPrefix string
	}{
		{
			name:           "valid bearer token",
			authHeader:     "Bearer my-token-123",
			wantStatus:     http.StatusOK,
			wantBodyPrefix: "my-token-123",
		},
		{
			name:           "missing authorization header",
			authHeader:     "",
			wantStatus:     http.StatusUnauthorized,
			wantBodyPrefix: `{"error":`,
		},
		{
			name:           "basic auth instead of bearer",
			authHeader:     "Basic dXNlcjpwYXNz",
			wantStatus:     http.StatusUnauthorized,
			wantBodyPrefix: `{"error":`,
		},
		{
			name:           "bearer prefix with empty token",
			authHeader:     "Bearer ",
			wantStatus:     http.StatusUnauthorized,
			wantBodyPrefix: `{"error":`,
		},
		{
			name:           "bearer without space",
			authHeader:     "Bearertoken",
			wantStatus:     http.StatusUnauthorized,
			wantBodyPrefix: `{"error":`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			body := rec.Body.String()
			if len(body) < len(tt.wantBodyPrefix) || body[:len(tt.wantBodyPrefix)] != tt.wantBodyPrefix {
				t.Errorf("body = %q, want prefix %q", body, tt.wantBodyPrefix)
			}
		})
	}
}

func TestTokenFromContext_NoToken(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	token := TokenFromContext(req.Context())
	if token != "" {
		t.Errorf("expected empty token, got %q", token)
	}
}
