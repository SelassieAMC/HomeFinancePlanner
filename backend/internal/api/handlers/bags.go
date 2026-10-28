package handlers

import (
	"net/http"

	"home-finance-planner/backend/internal/domain"
	"home-finance-planner/backend/internal/service"
)

// BagHandler handles /api/v1/bags.
type BagHandler struct{ Svc *service.BagService }

type bagRequest struct {
	Name        string `json:"name"`
	AccountID   int64  `json:"account_id"`
	TargetCents *int64 `json:"target_cents"` // optional; positive when set
}

func (h *BagHandler) input(r bagRequest) service.BagInput {
	return service.BagInput{
		Name:        r.Name,
		AccountID:   r.AccountID,
		TargetCents: r.TargetCents,
	}
}

// List returns the whole bags view: accounts with their bags, computed
// balances and each account's unbagged remainder. One read — the grouping is
// the service's job.
func (h *BagHandler) List(w http.ResponseWriter, r *http.Request) {
	overview, err := h.Svc.Overview(r.Context())
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, overview)
}

// Get returns one bag with its computed balance.
func (h *BagHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid id")
		return
	}
	b, err := h.Svc.Get(r.Context(), id)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, b)
}

// Create opens a new bag on one account.
func (h *BagHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req bagRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	b, err := h.Svc.Create(r.Context(), h.input(req))
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusCreated, b)
}

// Update renames a bag or changes its target (the account never moves).
func (h *BagHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid id")
		return
	}
	var req bagRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	b, err := h.Svc.Update(r.Context(), id, h.input(req))
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, b)
}

// SetStatus closes a spent-out bag (balance must be zero) or reopens one.
func (h *BagHandler) SetStatus(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid id")
		return
	}
	var req struct {
		Status string `json:"status"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	b, err := h.Svc.SetStatus(r.Context(), id, domain.BagLifecycle(req.Status))
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, b)
}

// Delete removes a bag outright (allocations cascade, tagged spend falls back
// to untagged).
func (h *BagHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.Svc.Delete(r.Context(), id); err != nil {
		respondServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type allocationRequest struct {
	Kind        string `json:"kind"` // fund | withdraw | adjust
	AmountCents int64  `json:"amount_cents"`
	Note        string `json:"note"`
	Date        string `json:"date"` // YYYY-MM-DD
}

func (h *BagHandler) allocationInput(r allocationRequest) domain.BagAllocationInput {
	return domain.BagAllocationInput{
		Kind:        domain.BagAllocationKind(r.Kind),
		AmountCents: r.AmountCents,
		Note:        r.Note,
		Date:        r.Date,
	}
}

// ListAllocations returns one bag's ledger, newest first.
func (h *BagHandler) ListAllocations(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid id")
		return
	}
	rows, err := h.Svc.AllocationHistory(r.Context(), id)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, rows)
}

// Allocate records a manual ledger entry (fund / withdraw / adjust).
func (h *BagHandler) Allocate(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid id")
		return
	}
	var req allocationRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	a, err := h.Svc.Allocate(r.Context(), id, h.allocationInput(req))
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusCreated, a)
}

// Rebalance moves an amount between two bags of one account (both legs
// returned).
func (h *BagHandler) Rebalance(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FromBagID   int64  `json:"from_bag_id"`
		ToBagID     int64  `json:"to_bag_id"`
		AmountCents int64  `json:"amount_cents"`
		Note        string `json:"note"`
		Date        string `json:"date"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	from, to, err := h.Svc.Rebalance(r.Context(), service.RebalanceInput{
		FromBagID:   req.FromBagID,
		ToBagID:     req.ToBagID,
		AmountCents: req.AmountCents,
		Note:        req.Note,
		Date:        req.Date,
	})
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusCreated, map[string]any{
		"from": from,
		"to":   to,
	})
}

// YearClose previews the open bags' year-end balances and shortfalls.
func (h *BagHandler) YearClose(w http.ResponseWriter, r *http.Request) {
	year := r.URL.Query().Get("year")
	preview, err := h.Svc.YearClose(r.Context(), year)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, preview)
}
