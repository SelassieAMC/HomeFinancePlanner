package handlers

import (
	"net/http"
	"strconv"

	"home-finance-planner/backend/internal/domain"
	"home-finance-planner/backend/internal/service"
)

// AccountHandler handles /api/v1/accounts.
type AccountHandler struct{ Svc *service.AccountService }

type accountRequest struct {
	Name           string             `json:"name"`
	Type           domain.AccountType `json:"type"`
	Currency       string             `json:"currency"`
	BalanceCents   int64              `json:"balance_cents"`
	CardLastDigits string             `json:"card_last_digits,omitempty"`
}

// List returns all accounts.
func (h *AccountHandler) List(w http.ResponseWriter, r *http.Request) {
	accounts, err := h.Svc.List(r.Context())
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, accounts)
}

// Get returns one account by id.
func (h *AccountHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid id")
		return
	}
	a, err := h.Svc.Get(r.Context(), id)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, a)
}

// Create creates an account.
func (h *AccountHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req accountRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	a, err := h.Svc.Create(r.Context(), service.AccountInput{
		Name:           req.Name,
		Type:           req.Type,
		Currency:       req.Currency,
		BalanceCents:   req.BalanceCents,
		CardLastDigits: req.CardLastDigits,
	})
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusCreated, a)
}

// Update replaces an account.
func (h *AccountHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid id")
		return
	}
	var req accountRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	a, err := h.Svc.Update(r.Context(), id, service.AccountInput{
		Name:           req.Name,
		Type:           req.Type,
		Currency:       req.Currency,
		BalanceCents:   req.BalanceCents,
		CardLastDigits: req.CardLastDigits,
	})
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, a)
}

// Delete removes an account.
func (h *AccountHandler) Delete(w http.ResponseWriter, r *http.Request) {
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

// CategoryHandler handles /api/v1/categories.
type CategoryHandler struct{ Svc *service.CategoryService }

type categoryRequest struct {
	Name string `json:"name"`
}

// List returns all categories.
func (h *CategoryHandler) List(w http.ResponseWriter, r *http.Request) {
	categories, err := h.Svc.List(r.Context())
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, categories)
}

// Create creates a category.
func (h *CategoryHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req categoryRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	c, err := h.Svc.Create(r.Context(), service.CategoryInput{Name: req.Name})
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusCreated, c)
}

// Delete removes a category.
func (h *CategoryHandler) Delete(w http.ResponseWriter, r *http.Request) {
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

// pathID parses {id} path values.
func pathID(r *http.Request) (int64, error) {
	return strconv.ParseInt(r.PathValue("id"), 10, 64)
}
