package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/opendatahub-io/odh-model-controller/server/httputil"
)

type contextKey string

const tokenKey contextKey = "bearerToken"

// TokenFromContext retrieves the Bearer token stored by the Auth middleware.
func TokenFromContext(ctx context.Context) string {
	token, _ := ctx.Value(tokenKey).(string)
	return token
}

// Auth extracts the Bearer token from the Authorization header and stores
// it in the request context. Requests without a valid Bearer token receive
// a 401 response.
func Auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			httputil.WriteJSONError(w, http.StatusUnauthorized, "missing or invalid authorization header")
			return
		}

		token := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
		if token == "" {
			httputil.WriteJSONError(w, http.StatusUnauthorized, "missing or invalid authorization header")
			return
		}

		ctx := context.WithValue(r.Context(), tokenKey, token)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
