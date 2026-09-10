package handlers

import (
	"io"
	"net/http"
	"os"

	"home-finance-planner/backend/internal/service"
)

// StoreHandler handles /api/v1/stores.
type StoreHandler struct {
	Svc *service.StoreService
}

type storeRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Location    string `json:"location"`
}

// List returns every store (with its display-only bill count).
func (h *StoreHandler) List(w http.ResponseWriter, r *http.Request) {
	stores, err := h.Svc.List(r.Context())
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, stores)
}

// Get returns one store.
func (h *StoreHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid id")
		return
	}
	store, err := h.Svc.Get(r.Context(), id)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, store)
}

// Create adds a store.
func (h *StoreHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req storeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	store, err := h.Svc.Create(r.Context(), service.StoreInput{
		Name:        req.Name,
		Description: req.Description,
		Location:    req.Location,
	})
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusCreated, store)
}

// Update rewrites a store's editable fields (logo untouched).
func (h *StoreHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid id")
		return
	}
	var req storeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	store, err := h.Svc.Update(r.Context(), id, service.StoreInput{
		Name:        req.Name,
		Description: req.Description,
		Location:    req.Location,
	})
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, store)
}

// Delete removes a store (bills keep their market_name snapshot).
func (h *StoreHandler) Delete(w http.ResponseWriter, r *http.Request) {
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

// UploadLogo stores an uploaded logo image (multipart "logo" field).
func (h *StoreHandler) UploadLogo(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid id")
		return
	}
	if err := r.ParseMultipartForm(service.MaxLogoBytes); err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid multipart form")
		return
	}
	file, header, err := r.FormFile("logo")
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "missing logo file field")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, service.MaxLogoBytes+1))
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "could not read logo file")
		return
	}
	mimeType := header.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = "image/jpeg"
	}
	store, err := h.Svc.SetLogo(r.Context(), id, mimeType, data)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, store)
}

// RemoveLogo clears the store's logo.
func (h *StoreHandler) RemoveLogo(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid id")
		return
	}
	store, err := h.Svc.RemoveLogo(r.Context(), id)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, store)
}

// Logo serves the stored logo image.
func (h *StoreHandler) Logo(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid id")
		return
	}
	path, mimeType, err := h.Svc.LogoPath(r.Context(), id)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		respondError(w, r, http.StatusNotFound, "logo missing on disk")
		return
	}
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	_, _ = w.Write(data)
}
