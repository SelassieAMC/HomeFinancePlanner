package handlers

import (
	"net/http"
	"net/url"
	"strconv"

	"home-finance-planner/backend/internal/domain"
	"home-finance-planner/backend/internal/service"
)

// TransactionHandler handles /api/v1/transactions.
type TransactionHandler struct{ Svc *service.TransactionService }

type transactionRequest struct {
	AccountID   int64                  `json:"account_id"`
	CategoryID  *int64                 `json:"category_id"`
	Kind        domain.TransactionKind `json:"kind"`
	AmountCents int64                  `json:"amount_cents"`
	Description string                 `json:"description"`
	Date        string                 `json:"date"`
}

// List returns transactions with optional month/account/category/kind filters
// and limit/offset pagination.
func (h *TransactionHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	f := service.TransactionFilters{
		Month:  q.Get("month"),
		Limit:  queryInt(q, "limit", 100),
		Offset: queryInt(q, "offset", 0),
	}
	if v := q.Get("account_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			respondError(w, r, http.StatusBadRequest, "invalid account_id")
			return
		}
		f.AccountID = id
	}
	if v := q.Get("category_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			respondError(w, r, http.StatusBadRequest, "invalid category_id")
			return
		}
		f.Category = &id
	}
	if v := q.Get("kind"); v != "" {
		f.Kind = domain.TransactionKind(v)
	}

	transactions, err := h.Svc.List(r.Context(), f)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, transactions)
}

// Get returns one transaction by id.
func (h *TransactionHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid id")
		return
	}
	t, err := h.Svc.Get(r.Context(), id)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, t)
}

// Create creates a transaction.
func (h *TransactionHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req transactionRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	t, err := h.Svc.Create(r.Context(), service.TransactionInput{
		AccountID:   req.AccountID,
		CategoryID:  req.CategoryID,
		Kind:        req.Kind,
		AmountCents: req.AmountCents,
		Description: req.Description,
		Date:        req.Date,
	})
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusCreated, t)
}

// Update replaces a transaction.
func (h *TransactionHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid id")
		return
	}
	var req transactionRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	t, err := h.Svc.Update(r.Context(), id, service.TransactionInput{
		AccountID:   req.AccountID,
		CategoryID:  req.CategoryID,
		Kind:        req.Kind,
		AmountCents: req.AmountCents,
		Description: req.Description,
		Date:        req.Date,
	})
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, t)
}

// Delete removes a transaction.
func (h *TransactionHandler) Delete(w http.ResponseWriter, r *http.Request) {
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

func queryInt(q url.Values, key string, fallback int) int {
	if v := q.Get(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
	}
	return fallback
}
