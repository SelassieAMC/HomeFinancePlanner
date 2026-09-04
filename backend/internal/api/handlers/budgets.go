package handlers

import (
	"net/http"

	"home-finance-planner/backend/internal/service"
)

// BudgetHandler handles /api/v1/budgets.
type BudgetHandler struct{ Svc *service.BudgetService }

type budgetRequest struct {
	CategoryID  int64  `json:"category_id"`
	Month       string `json:"month"`
	AmountCents int64  `json:"amount_cents"`
}

// List returns budgets for a month (month query param required, YYYY-MM).
func (h *BudgetHandler) List(w http.ResponseWriter, r *http.Request) {
	month := r.URL.Query().Get("month")
	budgets, err := h.Svc.ListByMonth(r.Context(), month)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, budgets)
}

// Create creates a budget for a category+month.
func (h *BudgetHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req budgetRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	b, err := h.Svc.Create(r.Context(), service.BudgetInput{
		CategoryID:  req.CategoryID,
		Month:       req.Month,
		AmountCents: req.AmountCents,
	})
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusCreated, b)
}

// Update changes only a budget's amount (month/category are immutable).
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

// Month returns the dashboard aggregate for a month (YYYY-MM).
func (h *SummaryHandler) Month(w http.ResponseWriter, r *http.Request) {
	s, err := h.Svc.MonthSummary(r.Context(), r.URL.Query().Get("month"))
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, s)
}
