package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"
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
			s.processScanSafe(token, log)
		}
	}
}

// processScanSafe guarantees a panic inside one extraction never kills its
// worker: the pool would silently shrink and later scans would sit in
// "analyzing" forever with nothing re-running them. The scan is marked failed
// instead, so the UI shows the failure and offers a retry.
func (s *BillService) processScanSafe(token string, log *slog.Logger) {
	defer func() {
		if r := recover(); r != nil {
			log.Error("extraction panicked", "token", token, "panic", r, "stack", string(debug.Stack()))
			s.markPanicked(token, r, log)
		}
	}()
	s.processScan(token, log)
}

// markPanicked best-effort records the panic on the scan row. It must never
// panic in turn — that would defeat processScanSafe's recovery.
func (s *BillService) markPanicked(token string, r any, log *slog.Logger) {
	defer func() { _ = recover() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.scans.MarkFailed(ctx, token, fmt.Sprintf("internal error during extraction: %v", r)); err != nil {
		log.Warn("mark panicked scan as failed", "token", token, "error", err)
	}
}

// processScan extracts one scan's draft and persists the result. All errors
// land in the scan row (status failed) — nothing is propagated, because the
// caller is a background goroutine. A row still analyzing after a lost write
// is recovered by the sweeper.
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
		s.persistResult(writeCtx, token, log, nil, describeExtractError(extractErr, s.extractTimeout))
	default:
		assignDraftItemIDs(draft, 0)
		s.persistResult(writeCtx, token, log, draft, "")
	}
}

// describeExtractError turns raw transport errors into actionable messages.
// The LLM_TIMEOUT budget covers the whole request, so local models hit it
// even for small receipts — the vision model reload after idle and queueing
// behind an earlier generation both burn the budget before any output.
func describeExtractError(err error, timeout time.Duration) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Sprintf(
			"extraction timed out after %s (LLM_TIMEOUT) — the model may still be loading after idle, or busy finishing an earlier receipt; try again in a moment, scan fewer receipts at once, or increase LLM_TIMEOUT",
			timeout,
		)
	}
	return err.Error()
}

// persistResult writes done/failed to the scan row, logging (not propagating)
// a failure — the row stays analyzing and is re-enqueued by the sweeper.
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
