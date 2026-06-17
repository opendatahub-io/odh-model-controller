package common

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

type errorResponse struct {
	Error string `json:"error"`
}

// WriteJSONError writes a JSON error response with the given status code
// and message. Safe against injection — uses json.Marshal for serialization.
func WriteJSONError(w http.ResponseWriter, status int, msg string) {
	data, err := json.Marshal(errorResponse{Error: msg})
	if err != nil {
		slog.Error("failed to marshal error response", "error", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"internal server error"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

// WriteJSON marshals v as JSON and writes it with the given status code.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		slog.Error("failed to encode response", "error", err)
		WriteJSONError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}
