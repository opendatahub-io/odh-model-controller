package handlers

import (
	"log/slog"
	"net/http"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/opendatahub-io/odh-model-controller/server/common"
	"github.com/opendatahub-io/odh-model-controller/server/gateway"
	"github.com/opendatahub-io/odh-model-controller/server/middleware"
)

// GatewayHandler handles GET /api/v1/gateways requests.
type GatewayHandler struct {
	Discoverer gateway.Discoverer
}

func (h *GatewayHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		common.WriteJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	namespace := r.URL.Query().Get("namespace")
	if namespace == "" {
		common.WriteJSONError(w, http.StatusBadRequest, "missing required query parameter: namespace")
		return
	}
	if errs := validation.IsDNS1123Label(namespace); len(errs) > 0 {
		common.WriteJSONError(w, http.StatusBadRequest, "invalid namespace: must match RFC 1123")
		return
	}

	userToken := middleware.TokenFromContext(r.Context())
	if strings.TrimSpace(userToken) == "" {
		common.WriteJSONError(w, http.StatusUnauthorized, "missing or invalid authorization header")
		return
	}

	refs, err := h.Discoverer.Discover(r.Context(), userToken, namespace)
	if err != nil {
		slog.Error("gateway discovery failed", "error", err)
		common.WriteJSONError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	common.WriteJSON(w, http.StatusOK, gateway.GatewaysResponse{Gateways: refs})
}
