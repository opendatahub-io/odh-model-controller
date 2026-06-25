package handlers

import (
	"errors"
	"io/fs"
	"net/http"
	"strings"

	"github.com/opendatahub-io/odh-model-controller/server/common"
	"github.com/opendatahub-io/odh-model-controller/server/samples"
)

// SamplesHandler handles GET /api/v1/samples/llm-d requests.
type SamplesHandler struct{}

func (h *SamplesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		common.WriteJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	configType := r.URL.Query().Get("type")
	if configType == "" {
		common.WriteJSONError(w, http.StatusBadRequest, "missing required query parameter: type")
		return
	}

	key := configType
	if configType == "router" {
		if topology := r.URL.Query().Get("topology"); strings.HasSuffix(topology, "-pd") {
			key = "router-pd"
		}
	}

	data, err := samples.ByType(key)
	if err != nil {
		if isNotExist(err) {
			common.WriteJSONError(w, http.StatusNotFound, "no sample found for type: "+configType)
			return
		}
		common.WriteJSONError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	w.Header().Set("Content-Type", "application/yaml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func isNotExist(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrInvalid)
}
