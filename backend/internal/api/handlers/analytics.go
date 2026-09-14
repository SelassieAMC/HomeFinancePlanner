package handlers

import (
	"net/http"

	"home-finance-planner/backend/internal/service"
)

// AnalyticsHandler serves the six dashboard chart payloads.
type AnalyticsHandler struct{ Svc *service.AnalyticsService }

// StorePriceIndex serves GET /api/v1/analytics/store-price-index?from&to.
func (h *AnalyticsHandler) StorePriceIndex(w http.ResponseWriter, r *http.Request) {
	from, to, ok := rangeParams(w, r)
	if !ok {
		return
	}
	out, err := h.Svc.StorePriceIndex(r.Context(), from, to)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, out)
}

// CategorySunburst serves GET /api/v1/analytics/category-sunburst?from&to.
func (h *AnalyticsHandler) CategorySunburst(w http.ResponseWriter, r *http.Request) {
	from, to, ok := rangeParams(w, r)
	if !ok {
		return
	}
	out, err := h.Svc.CategorySunburst(r.Context(), from, to)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, out)
}

// RunRate serves GET /api/v1/analytics/run-rate?month=YYYY-MM.
func (h *AnalyticsHandler) RunRate(w http.ResponseWriter, r *http.Request) {
	out, err := h.Svc.RunRate(r.Context(), r.URL.Query().Get("month"))
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, out)
}

// PriceIndex serves GET /api/v1/analytics/price-index?from&to.
func (h *AnalyticsHandler) PriceIndex(w http.ResponseWriter, r *http.Request) {
	from, to, ok := rangeParams(w, r)
	if !ok {
		return
	}
	out, err := h.Svc.PriceIndex(r.Context(), from, to)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, out)
}

// SpendHeatmap serves GET /api/v1/analytics/spend-heatmap?from&to.
func (h *AnalyticsHandler) SpendHeatmap(w http.ResponseWriter, r *http.Request) {
	from, to, ok := rangeParams(w, r)
	if !ok {
		return
	}
	out, err := h.Svc.SpendHeatmap(r.Context(), from, to)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, out)
}

// FixedSplit serves GET /api/v1/analytics/fixed-split?from&to.
func (h *AnalyticsHandler) FixedSplit(w http.ResponseWriter, r *http.Request) {
	from, to, ok := rangeParams(w, r)
	if !ok {
		return
	}
	out, err := h.Svc.FixedSplit(r.Context(), from, to)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, out)
}

// rangeParams decodes the shared from/to query params, responding 400 itself
// when either date is invalid or the range is inverted.
func rangeParams(w http.ResponseWriter, r *http.Request) (from, to string, ok bool) {
	q := r.URL.Query()
	from, to = q.Get("from"), q.Get("to")
	if err := service.ValidateRange(from, to); err != nil {
		respondServiceError(w, r, err)
		return "", "", false
	}
	return from, to, true
}
