package handlers

import (
	"io"
	"net/http"
	"os"
	"strconv"

	"home-finance-planner/backend/internal/service"
)

// ProductHandler handles /api/v1/products. There is no Create or Delete:
// products are find-or-created by the bill workflow and never removed.
type ProductHandler struct {
	Svc *service.ProductService
}

type productRequest struct {
	Name        string `json:"name"`
	Brand       string `json:"brand"`
	Unit        string `json:"unit"`
	CategoryID  *int64 `json:"category_id"`
	Description string `json:"description"`
}

// List returns the paged product list (name/category filters, sort, order).
func (h *ProductHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	f := service.ProductFilters{
		Name:   q.Get("name"),
		Sort:   q.Get("sort"),
		Order:  q.Get("order"),
		Limit:  queryInt(q, "limit", 50),
		Offset: queryInt(q, "offset", 0),
	}
	if v := q.Get("category_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			respondError(w, r, http.StatusBadRequest, "invalid category_id")
			return
		}
		f.CategoryID = &id
	}

	page, err := h.Svc.List(r.Context(), f)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondPage(w, r, page.Items, page.Total, page.Limit, page.Offset)
}

// Get returns one product with its purchase stats.
func (h *ProductHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid id")
		return
	}
	product, err := h.Svc.Get(r.Context(), id)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, product)
}

// StorePrices lists the latest price of the product per store (the details
// modal's "where was it cheapest" breakdown).
func (h *ProductHandler) StorePrices(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid id")
		return
	}
	rows, err := h.Svc.StorePrices(r.Context(), id)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, rows)
}

// Update rewrites a product's editable fields (propagating name/unit/category
// to the linked bill items) and leaves the photo untouched.
func (h *ProductHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid id")
		return
	}
	var req productRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	product, err := h.Svc.Update(r.Context(), id, service.ProductInput{
		Name:        req.Name,
		Brand:       req.Brand,
		Unit:        req.Unit,
		CategoryID:  req.CategoryID,
		Description: req.Description,
	})
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, product)
}

// CheckMerge reports what renaming the product to ?name would do: either no
// match, or the matched product plus the merge plan the UI confirms before
// calling POST /products/{id}/merge.
func (h *ProductHandler) CheckMerge(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid id")
		return
	}
	check, err := h.Svc.CheckMerge(r.Context(), id, r.URL.Query().Get("name"))
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, check)
}

// productMergeRequest is the merge confirmation: which product to fold in,
// which side to keep, and (optionally) the pending edit — applied when the
// source is kept, discarded when the target wins.
type productMergeRequest struct {
	MergeWith int64           `json:"merge_with"`
	Keep      string          `json:"keep"` // "source" | "target"
	Product   *productRequest `json:"product"`
}

// Merge folds the product into another one (merge_with), redirecting all of
// the dropped product's bill items to the keeper.
func (h *ProductHandler) Merge(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid id")
		return
	}
	var req productMergeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.MergeWith == 0 {
		respondError(w, r, http.StatusBadRequest, "merge_with is required")
		return
	}
	var keepTarget bool
	switch req.Keep {
	case "source":
	case "target":
		keepTarget = true
	default:
		respondError(w, r, http.StatusBadRequest, `keep must be "source" or "target"`)
		return
	}
	var input *service.ProductInput
	if req.Product != nil {
		input = &service.ProductInput{
			Name:        req.Product.Name,
			Brand:       req.Product.Brand,
			Unit:        req.Product.Unit,
			CategoryID:  req.Product.CategoryID,
			Description: req.Product.Description,
		}
	}
	product, err := h.Svc.Merge(r.Context(), id, service.ProductMergeInput{
		MergeWith:  req.MergeWith,
		KeepTarget: keepTarget,
		Product:    input,
	})
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, product)
}

// UploadPhoto stores an uploaded product photo (multipart "photo" field).
func (h *ProductHandler) UploadPhoto(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid id")
		return
	}
	if err := r.ParseMultipartForm(service.MaxPhotoBytes); err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid multipart form")
		return
	}
	file, header, err := r.FormFile("photo")
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "missing photo file field")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, service.MaxPhotoBytes+1))
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "could not read photo file")
		return
	}
	mimeType := header.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = "image/jpeg"
	}
	product, err := h.Svc.SetPhoto(r.Context(), id, mimeType, data)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, product)
}

// RemovePhoto clears the product's photo.
func (h *ProductHandler) RemovePhoto(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid id")
		return
	}
	product, err := h.Svc.RemovePhoto(r.Context(), id)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, product)
}

// Photo serves the stored product photo.
func (h *ProductHandler) Photo(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid id")
		return
	}
	path, mimeType, err := h.Svc.PhotoPath(r.Context(), id)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		respondError(w, r, http.StatusNotFound, "photo missing on disk")
		return
	}
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	_, _ = w.Write(data)
}
