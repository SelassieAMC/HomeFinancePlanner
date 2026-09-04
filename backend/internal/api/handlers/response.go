// Package handlers implements the HTTP transport layer: request decoding,
// response encoding, and error-to-status mapping. No business logic lives here.
package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"home-finance-planner/backend/internal/api/middleware"
	"home-finance-planner/backend/internal/domain"
)

// errorBody is the standard error envelope.
type errorBody struct {
	Error string `json:"error"`
}

// respondJSON writes v as JSON with the given status.
func respondJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v != nil {
		if err := json.NewEncoder(w).Encode(v); err != nil {
			middleware.LoggerFromContext(r.Context()).
				Error("encode response", "error", err)
		}
	}
}

// respondError writes the standard error envelope.
func respondError(w http.ResponseWriter, r *http.Request, status int, message string) {
	respondJSON(w, r, status, errorBody{Error: message})
}

// respondServiceError maps domain errors to HTTP status codes. This is the
// single place where domain errors become HTTP semantics.
func respondServiceError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		respondError(w, r, http.StatusNotFound, err.Error())
	case errors.Is(err, domain.ErrValidation):
		respondError(w, r, http.StatusBadRequest, err.Error())
	case errors.Is(err, domain.ErrConflict):
		respondError(w, r, http.StatusConflict, err.Error())
	default:
		middleware.LoggerFromContext(r.Context()).
			Error("internal error", "error", err)
		respondError(w, r, http.StatusInternalServerError, "internal server error")
	}
}

// decodeJSON decodes the request body into v, rejecting malformed payloads.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}
