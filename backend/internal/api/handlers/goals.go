package handlers

import (
	"net/http"

	"home-finance-planner/backend/internal/service"
)

// GoalHandler handles /api/v1/goals.
type GoalHandler struct{ Svc *service.GoalService }

type goalRequest struct {
	Name        string  `json:"name"`
	TargetCents int64   `json:"target_cents"`
	AccountID   int64   `json:"account_id"`
	TargetDate  *string `json:"target_date"` // optional YYYY-MM-DD milestone
}

func (r goalRequest) input() service.GoalInput {
	return service.GoalInput{
		Name:        r.Name,
		TargetCents: r.TargetCents,
		AccountID:   r.AccountID,
		TargetDate:  r.TargetDate,
	}
}

// List returns every goal with its live progress attached.
func (h *GoalHandler) List(w http.ResponseWriter, r *http.Request) {
	goals, err := h.Svc.List(r.Context())
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, goals)
}

// Get returns one goal by id.
func (h *GoalHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid id")
		return
	}
	g, err := h.Svc.Get(r.Context(), id)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, g)
}

// Create creates a goal.
func (h *GoalHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req goalRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	g, err := h.Svc.Create(r.Context(), req.input())
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusCreated, g)
}

// Update replaces a goal's editable fields.
func (h *GoalHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid id")
		return
	}
	var req goalRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	g, err := h.Svc.Update(r.Context(), id, req.input())
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, g)
}

// Delete removes a goal.
func (h *GoalHandler) Delete(w http.ResponseWriter, r *http.Request) {
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