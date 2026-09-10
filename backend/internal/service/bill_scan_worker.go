package service

import (
	"context"
	"log/slog"
	"os"
	"time"

	"home-finance-planner/backend/internal/domain"
)

// The scan workers drain the token queue and run AI extraction detached from
// any HTTP request: the DB row is the source of truth, the queue is only a
// "please look at this" hint. Every token is re-read from the DB before work,
// and the final result write is guarded on status='analyzing' (see
// BillScanRepository.MarkDone/MarkFailed), so a scan the user confirmed or
// discarded mid-run is never resurrected.

func (s *BillService) runWorker(ctx context.Context, n int) {
	defer s.wg.Done()
	log := s.log.With("component", "bill_scan_worker", "worker", n)
	for {
		select {
		case <-ctx.Done():
			return
		case token := <-s.queue:
			s.processScan(token, log)
		}
	}
}

// processScan extracts one scan's draft and persists the result. All errors
// land in the scan row (status failed) — nothing is propagated, because the
// caller is a background goroutine. A row still analyzing after a lost write
// is recovered on the next startup.
func (s *BillService) processScan(token string, log *slog.Logger) {
	// Detached from any HTTP request: the upload may be long gone.
	parent := context.Background()

	scan, err := s.scans.GetByToken(parent, token)
	if err != nil || scan.Status != domain.BillScanAnalyzing {
		// Confirmed, discarded, or swept meanwhile — nothing to do.
		log.Debug("skip vanished scan", "token", token)
		return
	}

	extractCtx, cancel := context.WithTimeout(parent, s.extractTimeout)
	defer cancel()

	provider, providerErr := s.providers.GetProvider(extractCtx, scan.ProviderID)
	file, fileErr := os.ReadFile(scan.ImagePath)
	draft, extractErr := s.extractDraft(extractCtx, file, scan.MimeType, provider)

	// The result write uses a fresh background context so a finished result
	// survives a shutdown that cancelled the worker pool.
	writeCtx, writeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer writeCancel()

	switch {
	case providerErr != nil:
		s.persistResult(writeCtx, token, log, nil, "AI provider unavailable: "+providerErr.Error())
	case fileErr != nil:
		s.persistResult(writeCtx, token, log, nil, "receipt file is missing on disk")
	case extractErr != nil:
		s.persistResult(writeCtx, token, log, nil, extractErr.Error())
	default:
		assignDraftItemIDs(draft, 0)
		s.persistResult(writeCtx, token, log, draft, "")
	}
}

// persistResult writes done/failed to the scan row, logging (not propagating)
// a failure — the row stays analyzing and is re-run on next startup.
func (s *BillService) persistResult(ctx context.Context, token string, log *slog.Logger, draft *domain.BillDraft, failure string) {
	var err error
	if draft != nil {
		err = s.scans.MarkDone(ctx, token, draft)
	} else {
		err = s.scans.MarkFailed(ctx, token, failure)
	}
	if err != nil {
		log.Error("persist scan result", "token", token, "error", err)
	}
}
