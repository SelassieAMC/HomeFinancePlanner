package handlers

import (
	"net/http"

	"home-finance-planner/backend/internal/domain"
	"home-finance-planner/backend/internal/service"
)

// BudgetHandler handles /api/v1/budgets.
type BudgetHandler struct{ Svc *service.BudgetService }

type budgetRequest struct {
	CategoryID  int64 `json:"category_id"`
	AmountCents int64 `json:"amount_cents"`
}

type budgetStatusRequest struct {
	Status domain.BudgetLifecycle `json:"status"`
}

// List returns budget envelopes; the optional status query param filters by
// lifecycle ("open" / "closed", default all).
func (h *BudgetHandler) List(w http.ResponseWriter, r *http.Request) {
	budgets, err := h.Svc.List(r.Context(), r.URL.Query().Get("status"))
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, budgets)
}

// Create opens a new budget envelope for a category.
func (h *BudgetHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req budgetRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	b, err := h.Svc.Create(r.Context(), service.BudgetInput{
		CategoryID:  req.CategoryID,
		AmountCents: req.AmountCents,
	})
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusCreated, b)
}

// Update changes only a budget's amount (category is immutable).
func (h *BudgetHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid id")
		return
	}
	var req budgetRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	b, err := h.Svc.Update(r.Context(), id, req.AmountCents)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, b)
}

// SetStatus marks an envelope finished (closed) or reopens it.
func (h *BudgetHandler) SetStatus(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid id")
		return
	}
	var req budgetStatusRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	b, err := h.Svc.SetStatus(r.Context(), id, req.Status)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, b)
}

// Delete removes a budget.
func (h *BudgetHandler) Delete(w http.ResponseWriter, r *http.Request) {
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

// SummaryHandler handles /api/v1/summary.
type SummaryHandler struct{ Svc *service.SummaryService }

// Get returns the dashboard aggregate for an inclusive date range. Either
// month (YYYY-MM) or both from and to (YYYY-MM-DD) must be given.
func (h *SummaryHandler) Get(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	month, from, to := q.Get("month"), q.Get("from"), q.Get("to")
	var (
		s   domain.Summary
		err error
	)
	switch {
	case month != "":
		if from != "" || to != "" {
			respondError(w, r, http.StatusBadRequest, "month and from/to are mutually exclusive")
			return
		}
		s, err = h.Svc.MonthSummary(r.Context(), month)
	case from != "" && to != "":
		s, err = h.Svc.RangeSummary(r.Context(), from, to)
	default:
		respondError(w, r, http.StatusBadRequest, "month or from and to query params are required")
		return
	}
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, s)
}
