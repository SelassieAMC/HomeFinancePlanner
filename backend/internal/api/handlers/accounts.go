package handlers

import (
	"net/http"
	"strconv"

	"home-finance-planner/backend/internal/domain"
	"home-finance-planner/backend/internal/service"
)

// AccountHandler handles /api/v1/accounts.
type AccountHandler struct{ Svc *service.AccountService }

// accountRequest is the account create/update payload. The money field is the
// OPENING balance (the pre-ledger seed) — balance_cents is derived per read
// and never writable here; drift is fixed through the reconcile endpoint.
type accountRequest struct {
	Name                string             `json:"name"`
	Type                domain.AccountType `json:"type"`
	Currency            string             `json:"currency"`
	OpeningBalanceCents int64              `json:"opening_balance_cents"`
	CardLastDigits      string             `json:"card_last_digits,omitempty"`
	GroupID             int64              `json:"group_id,omitempty"` // 0 = ungrouped
}

// accountReconcileRequest is the payload of POST /accounts/{id}/reconcile:
// the balance the bank actually reports for that account right now.
type accountReconcileRequest struct {
	BalanceCents int64 `json:"balance_cents"`
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
		Name:                req.Name,
		Type:                req.Type,
		Currency:            req.Currency,
		OpeningBalanceCents: req.OpeningBalanceCents,
		CardLastDigits:      req.CardLastDigits,
		GroupID:             req.GroupID,
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
		Name:                req.Name,
		Type:                req.Type,
		Currency:            req.Currency,
		OpeningBalanceCents: req.OpeningBalanceCents,
		CardLastDigits:      req.CardLastDigits,
		GroupID:             req.GroupID,
	})
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, a)
}

// Reconcile aligns the derived balance with what the bank reports: POST
// {balance_cents} (any integer — credit accounts run negative).
func (h *AccountHandler) Reconcile(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid id")
		return
	}
	var req accountReconcileRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	a, err := h.Svc.Reconcile(r.Context(), id, req.BalanceCents)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, a)
}

// Ledger serves the account register (GET /accounts/{id}/ledger?limit=&offset=):
// the account plus its transaction thread, newest first, every row carrying
// the balance as it stood after it (folded like the derived balance). The
// response {account, entries} — entries are transactions plus change_cents
// (this row's effect) and running_balance_cents. Limits the page to a sane
// size; the register pages backwards with offset.
func (h *AccountHandler) Ledger(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid id")
		return
	}
	limit, offset := 0, 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if limit, err = strconv.Atoi(v); err != nil || limit < 0 || limit > 200 {
			respondError(w, r, http.StatusBadRequest, "invalid limit")
			return
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if offset, err = strconv.Atoi(v); err != nil || offset < 0 {
			respondError(w, r, http.StatusBadRequest, "invalid offset")
			return
		}
	}
	out, err := h.Svc.Ledger(r.Context(), id, limit, offset)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, out)
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

// AccountGroupHandler handles /api/v1/accounts/groups — the presentation-only
// account folders. The literal /groups route must register BEFORE the
// {id} wildcards in the router.
type AccountGroupHandler struct{ Svc *service.AccountGroupService }

type accountGroupRequest struct {
	Name string `json:"name"`
}

// List returns all account groups.
func (h *AccountGroupHandler) List(w http.ResponseWriter, r *http.Request) {
	groups, err := h.Svc.List(r.Context())
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, groups)
}

// Create creates an account group.
func (h *AccountGroupHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req accountGroupRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	g, err := h.Svc.Create(r.Context(), service.AccountGroupInput{Name: req.Name})
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusCreated, g)
}

// Update renames an account group.
func (h *AccountGroupHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid id")
		return
	}
	var req accountGroupRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	g, err := h.Svc.Update(r.Context(), id, service.AccountGroupInput{Name: req.Name})
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, g)
}

// Delete removes the account group (its accounts become ungrouped).
func (h *AccountGroupHandler) Delete(w http.ResponseWriter, r *http.Request) {
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
