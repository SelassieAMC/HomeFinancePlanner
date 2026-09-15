package handlers

import (
	"net/http"
	"strings"

	"home-finance-planner/backend/internal/domain"
	"home-finance-planner/backend/internal/service"
)

// OfferSearchHandler handles /api/v1/cart-searches: the persisted
// offer-search pipeline behind the purchase cart.
type OfferSearchHandler struct{ Svc *service.OfferSearchService }

// Create snapshots the cart and starts the offer search, returning
// immediately with status "searching". The client polls Get until the status
// leaves "searching".
func (h *OfferSearchHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req domain.OfferSearchInput
	if !decodeJSON(w, r, &req) {
		return
	}
	search, err := h.Svc.Search(r.Context(), req)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusCreated, search)
}

// Get returns one search's pipeline state (searching | done with the result |
// failed with the error); polled by the client while a search is running.
func (h *OfferSearchHandler) Get(w http.ResponseWriter, r *http.Request) {
	search, err := h.Svc.Get(r.Context(), r.PathValue("token"))
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, search)
}

// List returns recent searches, optionally filtered by a comma-separated
// ?status=searching,failed.
func (h *OfferSearchHandler) List(w http.ResponseWriter, r *http.Request) {
	var statuses []domain.OfferSearchStatus
	if raw := r.URL.Query().Get("status"); raw != "" {
		for _, part := range strings.Split(raw, ",") {
			statuses = append(statuses, domain.OfferSearchStatus(strings.TrimSpace(part)))
		}
	}
	searches, err := h.Svc.ListSearches(r.Context(), statuses, 50)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, searches)
}

// Retry re-runs the search, optionally with a different provider.
func (h *OfferSearchHandler) Retry(w http.ResponseWriter, r *http.Request) {
	var req extractRequest
	_ = decodeJSON(w, r, &req) // body optional

	search, err := h.Svc.Retry(r.Context(), r.PathValue("token"), req.ProviderID)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, search)
}

// Delete discards a search row (and its persisted result).
func (h *OfferSearchHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if err := h.Svc.Delete(r.Context(), r.PathValue("token")); err != nil {
		respondServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
