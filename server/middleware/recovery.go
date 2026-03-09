package middleware

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/opendatahub-io/odh-model-controller/server/common"
)

// Recovery catches panics in downstream handlers and returns a 500 response.
func Recovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("panic recovered",
					"panic_type", fmt.Sprintf("%T", rec),
					"stack", string(debug.Stack()),
				)
				common.WriteJSONError(w, http.StatusInternalServerError, "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
