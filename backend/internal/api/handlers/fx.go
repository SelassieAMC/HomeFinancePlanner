package handlers

import (
	"net/http"

	"home-finance-planner/backend/internal/service"
)

// FxHandler exposes the public exchange-rate snapshot (Frankfurter/ECB data,
// cached 24h). The frontend uses it to show the reference rate a transfer or
// card payment will be converted at — the TRM the user sees before deciding
// to override it with the bank's own rate.
type FxHandler struct{ Svc *service.FXService }

// Rates returns the freshest rate snapshot: pivot currency, per-currency
// table, reference date and fetched-at. Offline tolerance is the service's —
// a failed refresh serves the stale cache (an empty snapshot means no usable
// rates; the frontend then offers no rate defaults).
func (h *FxHandler) Rates(w http.ResponseWriter, r *http.Request) {
	snap, err := h.Svc.Snapshot(r.Context())
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, snap)
}
