package handlers

import (
	"net/http"
	"time"

	"home-finance-planner/backend/internal/domain"
	"home-finance-planner/backend/internal/service"
)

// BudgetHandler handles /api/v1/budgets.
type BudgetHandler struct{ Svc *service.BudgetService }

type budgetRequest struct {
	CategoryID  int64                `json:"category_id"`
	AmountCents int64                `json:"amount_cents"`
	Cadence     domain.BudgetCadence `json:"cadence"` // empty = envelope
	// CarryOver rides create/update: whether a monthly series rolls its
	// leftover (running carried balance, deficits negative) into the next
	// month. Omitted = default ON for new monthly rows (0043); untouched on
	// update. Envelope rows ignore it.
	CarryOver *bool `json:"carry_over,omitempty"`
}

type budgetStatusRequest struct {
	Status domain.BudgetLifecycle `json:"status"`
}

// budgetTransferRequest is the payload of POST /budgets/transfer: allowance
// moved between two open monthly rows of one month (from_budget_id set), or
// a carried-balance seed riding onto the target row (from_budget_id absent;
// signed amount). Recorded as one auditable row.
type budgetTransferRequest struct {
	FromBudgetID *int64 `json:"from_budget_id,omitempty"`
	ToBudgetID   int64  `json:"to_budget_id"`
	AmountCents  int64  `json:"amount_cents"`
	Note         string `json:"note"`
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
		Cadence:     req.Cadence,
		CarryOver:   req.CarryOver,
	})
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusCreated, b)
}

// Update changes a budget's amount and (monthly rows) the carry_over flag;
// the category is immutable.
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
	b, err := h.Svc.Update(r.Context(), id, req.AmountCents, req.CarryOver)
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

// Transfer moves allowance between two open monthly rows of one month, or
// (from_budget_id absent) seeds the target row's carried balance. Responds
// {from, to} — from is null on a seed.
func (h *BudgetHandler) Transfer(w http.ResponseWriter, r *http.Request) {
	var req budgetTransferRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	from, to, err := h.Svc.Transfer(r.Context(), service.BudgetTransferInput{
		FromBudgetID: req.FromBudgetID,
		ToBudgetID:   req.ToBudgetID,
		AmountCents:  req.AmountCents,
		Note:         req.Note,
	})
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, struct {
		From *domain.Budget `json:"from"`
		To   domain.Budget  `json:"to"`
	}{fromBudgetPtr(from), to})
}

// Transfers lists the auditable transfer history.
func (h *BudgetHandler) Transfers(w http.ResponseWriter, r *http.Request) {
	out, err := h.Svc.Transfers(r.Context())
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, out)
}

// fromBudgetPtr shapes the response envelope's from field (null on a seed).
func fromBudgetPtr(from domain.Budget) *domain.Budget {
	if from.ID == 0 {
		return nil
	}
	return &from
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

// Glance returns the period-independent "at a glance" dashboard row: net
// worth, current-month cash flow, the 30-day burn rate and the
// pending-receipt backlog. No query params — the server date decides today.
func (h *SummaryHandler) Glance(w http.ResponseWriter, r *http.Request) {
	g, err := h.Svc.Glance(r.Context(), time.Now().Format("2006-01-02"))
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, g)
}
