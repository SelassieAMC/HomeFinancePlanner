package handlers

import (
	"io"
	"net/http"
	"os"
	"strings"

	"home-finance-planner/backend/internal/domain"
	"home-finance-planner/backend/internal/service"
)

// BillHandler handles /api/v1/bills.
type BillHandler struct{ Svc *service.BillService }

// maxDrainBody caps how much of an unread request body is drained before
// erroring out, so the client (or vite/nginx proxy) can finish writing it
// instead of dying with EPIPE.
const maxDrainBytes = 64 << 20

// drainBody consumes the remaining request body up to maxDrainBytes.
func drainBody(r *http.Request) {
	_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, maxDrainBytes))
}

// Scan reads a receipt file (multipart field "image", optional "provider_id"),
// registers a scan, and returns immediately with its token and the status
// "analyzing" — extraction runs in the background. The client polls GetScan
// until the draft is ready. Nothing is persisted until the client confirms.
func (h *BillHandler) Scan(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(service.MaxBillImageBytes); err != nil {
		// The client is still uploading; drain what we can before closing.
		drainBody(r)
		respondError(w, r, http.StatusBadRequest, "invalid multipart form: "+err.Error())
		return
	}
	file, header, err := r.FormFile("image")
	if err != nil {
		drainBody(r)
		respondError(w, r, http.StatusBadRequest, "missing image file field")
		return
	}
	defer file.Close()

	mimeType := header.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = "image/jpeg"
	}
	data, err := io.ReadAll(io.LimitReader(file, service.MaxBillImageBytes+1))
	if err != nil {
		drainBody(r)
		respondError(w, r, http.StatusBadRequest, "read receipt: "+err.Error())
		return
	}

	scan, err := h.Svc.Scan(r.Context(), mimeType, data, r.FormValue("provider_id"))
	if err != nil {
		drainBody(r)
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusCreated, scan)
}

// extractRequest pins the provider for a re-extraction; optional.
type extractRequest struct {
	ProviderID string `json:"provider_id,omitempty"`
}

// GetScan returns one scan's pipeline state (analyzing | done | failed with
// the draft); polled by the client while a scan is analyzing.
func (h *BillHandler) GetScan(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	scan, err := h.Svc.GetScan(r.Context(), token)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, scan)
}

// ListScans returns recent scans, optionally filtered by a comma-separated
// ?status=analyzing,failed.
func (h *BillHandler) ListScans(w http.ResponseWriter, r *http.Request) {
	var statuses []domain.BillScanStatus
	if raw := r.URL.Query().Get("status"); raw != "" {
		for _, part := range strings.Split(raw, ",") {
			statuses = append(statuses, domain.BillScanStatus(strings.TrimSpace(part)))
		}
	}
	scans, err := h.Svc.ListScans(r.Context(), statuses, 50)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, scans)
}

// Reextract re-runs AI extraction on an unconfirmed scan.
func (h *BillHandler) Reextract(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	var req extractRequest
	_ = decodeJSON(w, r, &req) // body optional

	scan, err := h.Svc.Reextract(r.Context(), token, req.ProviderID)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, scan)
}

// Confirm persists the confirmed draft as an accepted bill, optionally
// recording the expense transaction.
func (h *BillHandler) Confirm(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	var req domain.BillConfirmInput
	if !decodeJSON(w, r, &req) {
		return
	}
	bill, err := h.Svc.Confirm(r.Context(), token, req)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusCreated, bill)
}

// DiscardScan drops an unconfirmed scan and its receipt file.
func (h *BillHandler) DiscardScan(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if err := h.Svc.DiscardScan(r.Context(), token); err != nil {
		respondServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Image serves the stored receipt for future reference.
func (h *BillHandler) Image(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid id")
		return
	}
	path, mimeType, err := h.Svc.ReceiptImagePath(r.Context(), id)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		respondError(w, r, http.StatusNotFound, "receipt missing on disk")
		return
	}
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	_, _ = w.Write(data)
}

// List returns bills with optional status/month/market filters.
func (h *BillHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	f := domain.BillFilters{}
	if v := q.Get("status"); v != "" {
		f.Status = domain.BillStatus(v)
	}
	if v := q.Get("month"); v != "" {
		f.Month = v
	}
	if v := q.Get("market"); v != "" {
		f.Market = v
	}

	bills, err := h.Svc.List(r.Context(), f)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, bills)
}

// Get returns one bill with its items.
func (h *BillHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid id")
		return
	}
	bill, err := h.Svc.Get(r.Context(), id)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, bill)
}

// Update applies user corrections to a saved bill.
func (h *BillHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid id")
		return
	}
	var req domain.BillConfirmInput
	if !decodeJSON(w, r, &req) {
		return
	}
	bill, err := h.Svc.Update(r.Context(), id, req)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, bill)
}

// Delete removes a confirmed bill for good: the bill with its item lines,
// the linked expense transaction, and the stored receipt file.
func (h *BillHandler) Delete(w http.ResponseWriter, r *http.Request) {
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

// Stats aggregates accepted bills by market, month, week, item or category,
// converted into the user's base currency.
func (h *BillHandler) Stats(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	stats, err := h.Svc.Stats(r.Context(), q.Get("group_by"), q.Get("month"))
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, stats)
}

// Brands lists the distinct brands recorded on bill items (for the review
// dropdown).
func (h *BillHandler) Brands(w http.ResponseWriter, r *http.Request) {
	brands, err := h.Svc.Brands(r.Context())
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, brands)
}
