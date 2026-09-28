package handlers

import (
	"net/http"

	"home-finance-planner/backend/internal/service"
)

// PromptHandler handles /api/v1/prompts (managed AI prompt texts).
type PromptHandler struct {
	Svc *service.AIPromptService
}

type promptRequest struct {
	Key         string `json:"key"` // create only; immutable afterwards
	Name        string `json:"name"`
	Description string `json:"description"`
	Content     string `json:"content"`
}

// List returns every prompt as a view (with its built-in default and the
// uses-default flag).
func (h *PromptHandler) List(w http.ResponseWriter, r *http.Request) {
	prompts, err := h.Svc.List(r.Context())
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, prompts)
}

// Get returns one prompt.
func (h *PromptHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid id")
		return
	}
	prompt, err := h.Svc.Get(r.Context(), id)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, prompt)
}

// Create adds a prompt.
func (h *PromptHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req promptRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	prompt, err := h.Svc.Create(r.Context(), service.AIPromptCreateInput{
		Key:         req.Key,
		Name:        req.Name,
		Description: req.Description,
		Content:     req.Content,
	})
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusCreated, prompt)
}

// Update rewrites a prompt's editable fields (the key is immutable —
// processes resolve prompts by it).
func (h *PromptHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid id")
		return
	}
	var req promptRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	prompt, err := h.Svc.Update(r.Context(), id, service.AIPromptInput{
		Name:        req.Name,
		Description: req.Description,
		Content:     req.Content,
	})
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, prompt)
}

// Delete removes a prompt (a seeded process falls back to the built-in
// default).
func (h *PromptHandler) Delete(w http.ResponseWriter, r *http.Request) {
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
