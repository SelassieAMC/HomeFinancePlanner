package handlers

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"home-finance-planner/backend/internal/domain"
	"home-finance-planner/backend/internal/service"
)

// BillHandler handles /api/v1/bills.
type BillHandler struct{ Svc *service.BillService }

// maxDrainBody caps how much of an unread request body is drained before
// erroring out, so the client (or vite/nginx proxy) can finish writing it
// instead of dying with EPIPE. Grouped uploads carry up to
// service.MaxBillScanFiles receipt parts of service.MaxBillImageBytes each.
const maxDrainBytes = 96 << 20

// drainBody consumes the remaining request body up to maxDrainBytes.
func drainBody(r *http.Request) {
	_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, maxDrainBytes))
}

// Scan reads the receipt file(s) — repeated multipart field "image", optional
// "provider_id" — registers a scan, and returns immediately with its token and
// the status "analyzing"; extraction runs in the background. One file is a
// single-photo receipt; several files are the consecutive parts of one long
// receipt merged into a single bill. The client polls GetScan until the draft
// is ready. Nothing is persisted until the client confirms.
func (h *BillHandler) Scan(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(service.MaxBillImageBytes); err != nil {
		// The client is still uploading; drain what we can before closing.
		drainBody(r)
		respondError(w, r, http.StatusBadRequest, "invalid multipart form: "+err.Error())
		return
	}
	parts := r.MultipartForm.File["image"]
	if len(parts) == 0 {
		drainBody(r)
		respondError(w, r, http.StatusBadRequest, "missing image file field")
		return
	}
	if len(parts) > service.MaxBillScanFiles {
		drainBody(r)
		respondError(w, r, http.StatusBadRequest,
			fmt.Sprintf("a receipt can be uploaded in at most %d files — %d were given", service.MaxBillScanFiles, len(parts)))
		return
	}

	files := make([]domain.ReceiptFile, 0, len(parts))
	for _, header := range parts {
		file, err := header.Open()
		if err != nil {
			drainBody(r)
			respondError(w, r, http.StatusBadRequest, "read receipt: "+err.Error())
			return
		}
		mimeType := header.Header.Get("Content-Type")
		if mimeType == "" {
			mimeType = "image/jpeg"
		}
		data, err := io.ReadAll(io.LimitReader(file, service.MaxBillImageBytes+1))
		file.Close()
		if err != nil {
			drainBody(r)
			respondError(w, r, http.StatusBadRequest, "read receipt: "+err.Error())
			return
		}
		files = append(files, domain.ReceiptFile{Data: data, MimeType: mimeType})
	}

	scan, err := h.Svc.Scan(r.Context(), files, r.FormValue("provider_id"))
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

// GetScan returns one scan's pipeline state (analyzing | done | failed |
// cancelled, with the draft on done); polled by the client while a scan is
// analyzing.
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

// CancelScan aborts an in-progress analysis: the scan is marked cancelled and
// the running AI extraction is signalled to stop, returning the resulting
// scan state.
func (h *BillHandler) CancelScan(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	scan, err := h.Svc.CancelScan(r.Context(), token)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, scan)
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

// Image serves the stored receipt for future reference. Multi-part receipts
// take ?part=N (1-based; default 1).
func (h *BillHandler) Image(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid id")
		return
	}
	part := 1
	if raw := r.URL.Query().Get("part"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			respondError(w, r, http.StatusBadRequest, "invalid part")
			return
		}
		part = n
	}
	path, mimeType, err := h.Svc.ReceiptImagePath(r.Context(), id, part)
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
// converted into the user's base currency. Scope is set by month (YYYY-MM)
// or an inclusive from/to (YYYY-MM-DD) range.
func (h *BillHandler) Stats(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	stats, err := h.Svc.Stats(r.Context(), service.BillStatsQuery{
		GroupBy: q.Get("group_by"),
		Month:   q.Get("month"),
		From:    q.Get("from"),
		To:      q.Get("to"),
	})
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
