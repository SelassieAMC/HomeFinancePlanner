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

// The scan worker runs one AI extraction detached from any HTTP request: the
// DB row is the source of truth, the River queue is only a "please look at
// this" hint. Every token is re-read from the DB before work, and the final
// result write is guarded on status='analyzing' (see
// BillScanRepository.MarkDone/MarkFailed), so a scan the user confirmed or
// discarded mid-run is never resurrected.

// Timeout retries: LLM_TIMEOUT bounds one attempt, but a long receipt can
// legitimately need more model time than that, and a timed-out attempt
// abandons a request the model server may keep generating — an instant retry
// of the same size queues behind it and times out too (the same reason the
// normalization job defers its slow batches). Timeout attempts therefore
// escalate instead of failing right away: the budgets are LLM_TIMEOUT, ×2,
// ×4, each after a short settle pause that lets the model server drain the
// abandoned request. Any other error fails the scan without retrying.
const (
	maxExtractionAttempts = 3
)

// timeoutRetrySettle is a var (not a const) so tests can shrink the pause.
var timeoutRetrySettle = 30 * time.Second

// processScanSafe guarantees a panic inside one extraction never kills the
// River worker: the scan is marked failed instead, so the UI shows the failure
// and offers a retry.
func (s *BillService) processScanSafe(ctx context.Context, token string, log *slog.Logger) {
	defer func() {
		if r := recover(); r != nil {
			log.Error("extraction panicked", "token", token, "panic", r, "stack", string(debug.Stack()))
			s.markPanicked(token, r, log)
		}
	}()
	s.processScan(ctx, token, log)
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

// abortIfNotAnalyzing polls the scan row and cancels runCancel once its status
// leaves 'analyzing'. CancelScan (and Discard/Confirm) run in the API process
// and can only touch the DB row — this poller is how a user abort actually
// reaches the worker's running extraction. It stops itself when runCtx ends.
func (s *BillService) abortIfNotAnalyzing(parent context.Context, runCtx context.Context, runCancel context.CancelFunc, token string) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-runCtx.Done():
			return
		case <-ticker.C:
			cur, err := s.scans.GetByToken(parent, token)
			if err != nil || cur.Status != domain.BillScanAnalyzing {
				runCancel()
				return
			}
		}
	}
}

// processScan extracts one scan's draft and persists the result. All errors
// land in the scan row (status failed) — nothing is propagated, because the
// caller is the River worker. A row still analyzing after a crash is recovered
// by the queue (running jobs re-run) or by RecoverScans on boot.
func (s *BillService) processScan(ctx context.Context, token string, log *slog.Logger) {
	// The run context is the user's Abort button: CancelScan marks the row
	// cancelled and cancels this context, which stops the extraction the worker
	// is inside right now. It derives from the job context so a forced shutdown
	// (StopAndCancel) aborts the extraction too. Registered before the row read
	// so a cancel that lands between the read and the extraction still finds
	// the func; removed with defer so even a panic path leaves nothing behind.
	parent := ctx
	runCtx, runCancel := context.WithCancel(parent)
	defer runCancel()
	s.registerCanceller(token, runCancel)
	defer s.removeCanceller(token)

	// CancelScan runs in the API process, not this one, so it can only write
	// the DB row — it never reaches this worker's in-process cancellers map.
	// Poll the row instead: abort the extraction the moment the status leaves
	// 'analyzing' (user cancelled, discarded or confirmed mid-run). Without
	// this, a cancel only blocked the RESULT write; the AI call kept generating
	// to completion and the River job ran to the end.
	go s.abortIfNotAnalyzing(parent, runCtx, runCancel, token)

	scan, err := s.scans.GetByToken(parent, token)
	if err != nil || scan.Status != domain.BillScanAnalyzing {
		// Confirmed, discarded, cancelled, or swept meanwhile — nothing to do.
		log.Debug("skip vanished scan", "token", token)
		return
	}

	extractCtx, cancel := context.WithTimeout(runCtx, s.extractTimeout)
	defer cancel()

	provider, providerErr := s.providers.GetProvider(extractCtx, scan.ProviderID)
	files, fileErr := readScanFiles(scan)

	var draft *domain.BillDraft
	var extractErr error
	if fileErr == nil && providerErr == nil {
		draft, extractErr = s.extractWithTimeoutRetries(runCtx, token, files, provider, log)
	}

	// The user cancelled this scan mid-run: the row already says 'cancelled'
	// and must not get a failed/done write — report and stop.
	if errors.Is(extractErr, context.Canceled) {
		log.Debug("scan cancelled by user — result not persisted", "token", token)
		return
	}

	// The result write uses a fresh background context so a finished result
	// survives a shutdown that cancelled the worker pool.
	writeCtx, writeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer writeCancel()

	switch {
	case providerErr != nil:
		s.persistResult(writeCtx, token, log, nil, "AI provider unavailable: "+providerErr.Error())
	case fileErr != nil:
		s.persistResult(writeCtx, token, log, nil, fileErr.Error())
	case extractErr != nil:
		s.persistResult(writeCtx, token, log, nil, describeExtractError(extractErr, s.extractTimeout))
	default:
		assignDraftItemIDs(draft, 0)
		s.persistResult(writeCtx, token, log, draft, "")
	}
}

// readScanFiles loads every stored part of a scan's receipt from disk, in
// position order. A pre-feature row without parts falls back to its mirrored
// single file; a missing part fails with the part named in the message.
func readScanFiles(scan domain.BillScan) ([]domain.ReceiptFile, error) {
	if len(scan.Files) == 0 {
		// Legacy row: the single file is the whole receipt.
		data, err := os.ReadFile(scan.ImagePath)
		if err != nil {
			return nil, errors.New("receipt file is missing on disk")
		}
		return []domain.ReceiptFile{{Data: data, MimeType: scan.MimeType}}, nil
	}
	files := make([]domain.ReceiptFile, 0, len(scan.Files))
	for i, f := range scan.Files {
		data, err := os.ReadFile(f.Path)
		if err != nil {
			position := f.Position
			if position == 0 {
				position = i + 1
			}
			return nil, fmt.Errorf("receipt part %d of %d is missing on disk", position, len(scan.Files))
		}
		files = append(files, domain.ReceiptFile{Data: data, MimeType: f.MimeType})
	}
	return files, nil
}

// extractWithTimeoutRetries runs the extraction once per escalating budget
// (see the constants at the top of the file). Only a timeout escalates —
// any other error fails the scan immediately. The scan row is not touched
// between attempts: it stays analyzing until the final result is written, and
// the poller keeps showing the in-progress view meanwhile.
func (s *BillService) extractWithTimeoutRetries(parent context.Context, token string, files []domain.ReceiptFile, provider domain.AIProvider, log *slog.Logger) (*domain.BillDraft, error) {
	budget := s.extractTimeout
	for attempt := 1; ; attempt++ {
		ctx, cancel := context.WithTimeout(parent, budget)
		draft, err := s.extractDraft(ctx, files, provider)
		cancel()
		if err == nil || !errors.Is(err, context.DeadlineExceeded) || attempt == maxExtractionAttempts {
			return draft, err
		}
		next := budget * 2
		log.Warn("extraction timed out — retrying with a larger budget",
			"token", token, "finished_attempts", attempt, "budget", budget, "next_budget", next)
		budget = next
		select {
		case <-time.After(timeoutRetrySettle): // let the abandoned request drain
		case <-parent.Done():
			return nil, err
		}
	}
}

// describeExtractError turns raw transport errors into actionable messages.
// It runs on the last attempt, so the timeout branch describes exhausted
// retries rather than an advice to try again (the worker already tried).
func describeExtractError(err error, timeout time.Duration) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Sprintf(
			"extraction timed out after %d attempts with escalating budgets (LLM_TIMEOUT ×1..×%d, up to %s) — this receipt is longer than the model could finish; scan it as several part-photos of one receipt, increase LLM_TIMEOUT, or use a faster AI connector",
			maxExtractionAttempts, 1<<(maxExtractionAttempts-1), timeout*(1<<(maxExtractionAttempts-1)),
		)
	}
	return err.Error()
}

// persistResult writes done/failed to the scan row, logging (not propagating)
// a failure — the row stays analyzing and is re-enqueued by the sweeper. A
// guarded write that matched 0 rows is a benign race (the user confirmed,
// discarded or cancelled the scan mid-run), not a persistence failure.
func (s *BillService) persistResult(ctx context.Context, token string, log *slog.Logger, draft *domain.BillDraft, failure string) {
	var err error
	if draft != nil {
		err = s.scans.MarkDone(ctx, token, draft)
	} else {
		err = s.scans.MarkFailed(ctx, token, failure)
	}
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			log.Warn("scan result superseded (confirmed, discarded or cancelled mid-run)", "token", token)
		} else {
			log.Error("persist scan result", "token", token, "error", err)
		}
	}
}
