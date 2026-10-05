package handlers

import (
	"net/http"
	"strconv"

	"home-finance-planner/backend/internal/service"
)

// InsightHandler handles /api/v1/insights: the saved price-per-unit nudges
// the insight analysis job wrote. Read-only plus dismiss.
type InsightHandler struct{ Svc *service.InsightService }

// List returns saved insights (newest first). ?unseen=true limits to the
// unread rows the dashboard shows; ?limit= caps the page.
func (h *InsightHandler) List(w http.ResponseWriter, r *http.Request) {
	unseen := r.URL.Query().Get("unseen") == "true"
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			respondError(w, r, http.StatusBadRequest, "limit must be a non-negative integer")
			return
		}
		limit = n
	}
	insights, err := h.Svc.List(r.Context(), unseen, limit)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, insights)
}

// Dismiss acknowledges one insight (removes it from the unread list).
func (h *InsightHandler) Dismiss(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid insight id")
		return
	}
	if err := h.Svc.Dismiss(r.Context(), id); err != nil {
		respondServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
