package service

import (
	"context"

	"github.com/riverqueue/river"
)

// River job kinds and workers for the three pipeline jobs that used to run on
// the services' own goroutine pools. The workers live here (not in
// internal/jobs) so the business logic — the timeout escalation, the per-scan
// cancellation, the batch deferral — stays with the service that owns it;
// main registers each worker and routes its kind to a dedicated queue.

// BillScanArgs carries one receipt-scan token through the queue. The DB row is
// still the source of truth; the token is only a "please look at this" hint.
type BillScanArgs struct {
	Token string `json:"token"`
}

func (BillScanArgs) Kind() string { return "bill_scan" }

// BillScanWorker runs one scan's AI extraction. It returns nil on every
// outcome — success and failure alike are recorded on the scan row, matching
// the old in-process worker's contract (a failed scan is retried by the user,
// never auto-retried by the queue; the ×1/×2/×4 timeout escalation happens
// inside processScan).
type BillScanWorker struct {
	river.WorkerDefaults[BillScanArgs]
	svc *BillService
}

func NewBillScanWorker(svc *BillService) *BillScanWorker {
	return &BillScanWorker{svc: svc}
}

func (w *BillScanWorker) Work(ctx context.Context, job *river.Job[BillScanArgs]) error {
	log := w.svc.log.With("component", "bill_scan_worker", "job_id", job.ID, "token", job.Args.Token)
	w.svc.processScanSafe(ctx, job.Args.Token, log)
	return nil
}

// OfferSearchArgs carries one offer-search token through the queue.
type OfferSearchArgs struct {
	Token string `json:"token"`
}

func (OfferSearchArgs) Kind() string { return "offer_search" }

// OfferSearchWorker runs one cart's offer search. Like the scan worker it
// never returns an error: the outcome lives on the search row.
type OfferSearchWorker struct {
	river.WorkerDefaults[OfferSearchArgs]
	svc *OfferSearchService
}

func NewOfferSearchWorker(svc *OfferSearchService) *OfferSearchWorker {
	return &OfferSearchWorker{svc: svc}
}

func (w *OfferSearchWorker) Work(ctx context.Context, job *river.Job[OfferSearchArgs]) error {
	log := w.svc.log.With("component", "offer_search_worker", "job_id", job.ID, "token", job.Args.Token)
	w.svc.processSearchSafe(ctx, job.Args.Token, log)
	return nil
}

// ProductNormalizationArgs is the singleton product-name normalization job (no
// payload — single-flight is enforced by the DB status row plus River's unique
// options on the fixed unique key).
type ProductNormalizationArgs struct{}

func (ProductNormalizationArgs) Kind() string { return "product_normalization" }

// ProductNormalizationWorker runs the whole normalization backfill to
// completion (or a recorded failure) in one job. It never returns an error:
// the job's business status lives on the single product_normalization_jobs
// row, and a failed run is re-triggered by the user, not auto-retried.
type ProductNormalizationWorker struct {
	river.WorkerDefaults[ProductNormalizationArgs]
	svc *ProductService
}

func NewProductNormalizationWorker(svc *ProductService) *ProductNormalizationWorker {
	return &ProductNormalizationWorker{svc: svc}
}

func (w *ProductNormalizationWorker) Work(ctx context.Context, job *river.Job[ProductNormalizationArgs]) error {
	w.svc.runNormalizationJob(ctx)
	return nil
}
