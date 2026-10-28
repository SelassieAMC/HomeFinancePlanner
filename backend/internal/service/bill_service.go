package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	"home-finance-planner/backend/internal/domain"
	"home-finance-planner/backend/internal/fx"
	"home-finance-planner/backend/internal/imageproc"
)

// BillExtractor abstracts the AI extraction engine (implemented by
// internal/extractor). The prompt is resolved by the caller — the managed
// ai_prompts content or the built-in default. One file is a classic
// single-photo receipt; several files are the parts of one long receipt.
type BillExtractor interface {
	Extract(ctx context.Context, files []domain.ReceiptFile, provider domain.AIProvider, prompt string) (domain.BillDraft, error)
}

// PromptResolver supplies the rendered prompt content for a process key
// (implemented by AIPromptService; nil falls back to the built-in defaults,
// which keeps tests light).
type PromptResolver interface {
	ResolvePrompt(ctx context.Context, key string) (string, error)
}

// BillStore is the persistence contract for bills. Only accepted bills are
// persisted — scan drafts live in bill_scans until confirmed.
type BillStore interface {
	Create(ctx context.Context, b domain.Bill) (domain.Bill, error)
	Update(ctx context.Context, b domain.Bill) (domain.Bill, error)
	SetTransaction(ctx context.Context, billID, txID int64) error
	Delete(ctx context.Context, id int64) error
	GetByID(ctx context.Context, id int64) (domain.Bill, error)
	GetByFileHash(ctx context.Context, hash string) (domain.Bill, error)
	List(ctx context.Context, f domain.BillFilters) ([]domain.Bill, error)
	ListBrands(ctx context.Context) ([]string, error)
}

// BillScanStore is the persistence contract for the scan pipeline.
type BillScanStore interface {
	Create(ctx context.Context, s domain.BillScan) (domain.BillScan, error)
	GetByToken(ctx context.Context, token string) (domain.BillScan, error)
	GetByFileHash(ctx context.Context, hash string) (domain.BillScan, error)
	List(ctx context.Context, statuses []domain.BillScanStatus, limit int) ([]domain.BillScan, error)
	MarkDone(ctx context.Context, token string, draft *domain.BillDraft) error
	MarkFailed(ctx context.Context, token, msg string) error
	MarkCancelled(ctx context.Context, token string) (bool, error)
	ClaimRetry(ctx context.Context, token, providerID string) (bool, error)
	Delete(ctx context.Context, token string) (domain.BillScan, error)
	DeleteStale(ctx context.Context, olderThan time.Time) ([]string, error)
}

// BillFilters re-exports the shared domain filter type.
type BillFilters = domain.BillFilters

// MaxBillImageBytes caps uploaded receipt files at 10 MB each.
const MaxBillImageBytes = 10 << 20

// MaxBillScanFiles caps how many files one scan may carry: one receipt
// photographed in at most this many consecutive parts.
const MaxBillScanFiles = 8

const (
	// scanQueueCapacity bounds pending scan tokens; a full queue rejects the
	// upload instead of silently losing it.
	scanQueueCapacity = 64
	// billScanWorkers bounds concurrent AI extractions (SQLite serializes the
	// tiny row updates; the long AI HTTP call holds no DB connection).
	billScanWorkers = 2
	// scanSessionTTL bounds how long an unconfirmed scan draft (and its
	// receipt file) is kept — done/failed scans older than this are swept.
	scanSessionTTL = 24 * time.Hour
	// shutdownDrainWindow caps how long Close waits for workers between
	// iterations; it never waits for a running extraction (recovery re-runs it).
	shutdownDrainWindow = 2 * time.Second
)

// billScanSource points at the receipt a bill is (or was) built from: every
// stored part on disk under billsDir (position order; part 1 is mirrored into
// the legacy single-file columns) and the provider id that produced the draft.
// Used by both Confirm (from the scan row) and Update (from the saved bill).
type billScanSource struct {
	files      []domain.BillFile
	providerID string
}

// BillService orchestrates the scan-bills workflow.
type BillService struct {
	bills      BillStore
	scans      BillScanStore
	extractor  BillExtractor
	providers  *SettingsService
	prompts    PromptResolver
	accounts   AccountStore
	categories CategoryStore
	stores     StoreStore
	products   ProductStore
	mappings   ProductMappingStore
	budgets    BudgetStore
	bags       BagStore
	txStore    TransactionStore
	rates      RateSource
	billsDir   string
	// insights enqueues the deferred price-per-unit analysis after a bill is
	// confirmed or edited. nil disables the layer (tests).
	insights       PurchaseAnalyzer
	extractTimeout time.Duration
	log            *slog.Logger

	queue     chan string
	wg        sync.WaitGroup
	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex // guards lastSweep only
	lastSweep time.Time

	cancelMu   sync.Mutex                    // guards cancellers
	cancellers map[string]context.CancelFunc // aborts an in-flight extraction by scan token
}

// NewBillService wires the bill workflow and starts the background scan
// workers. billsDir is where receipt files are stored; extractTimeout bounds
// one AI extraction; scans still analyzing after a restart are re-enqueued.
func NewBillService(
	bills BillStore,
	scans BillScanStore,
	extractor BillExtractor,
	providers *SettingsService,
	prompts PromptResolver,
	accounts AccountStore,
	categories CategoryStore,
	stores StoreStore,
	products ProductStore,
	mappings ProductMappingStore,
	budgets BudgetStore,
	bags BagStore,
	txStore TransactionStore,
	rates RateSource,
	billsDir string,
	insights PurchaseAnalyzer,
	extractTimeout time.Duration,
	log *slog.Logger,
) *BillService {
	if extractTimeout <= 0 {
		extractTimeout = 5 * time.Minute
	}
	if log == nil {
		log = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &BillService{
		bills:          bills,
		scans:          scans,
		extractor:      extractor,
		providers:      providers,
		prompts:        prompts,
		accounts:       accounts,
		categories:     categories,
		stores:         stores,
		products:       products,
		mappings:       mappings,
		budgets:        budgets,
		bags:           bags,
		txStore:        txStore,
		rates:          rates,
		billsDir:       billsDir,
		insights:       insights,
		extractTimeout: extractTimeout,
		log:            log,
		queue:          make(chan string, scanQueueCapacity),
		ctx:            ctx,
		cancel:         cancel,
		cancellers:     make(map[string]context.CancelFunc),
	}
	s.recoverScans(ctx)
	for i := 1; i <= billScanWorkers; i++ {
		s.wg.Add(1)
		go s.runWorker(ctx, i)
	}
	s.wg.Add(1)
	go s.runSweeper(ctx)
	return s
}

// scanPart is one validated part of an upload, ready to be stored.
type scanPart struct {
	data []byte
	mime string
	ext  string
	hash string
}

// Scan stores the receipt files (not yet a bill), registers a scan row, and
// returns immediately — extraction runs in the background. One file is the
// classic single-photo receipt; several files are the consecutive parts of one
// long receipt, merged into ONE bill by the AI read. The client polls GetScan
// until the status leaves "analyzing". Nothing is persisted as a bill until
// Confirm. providerID is optional; the first configured provider is used
// otherwise.
func (s *BillService) Scan(ctx context.Context, files []domain.ReceiptFile, providerID string) (domain.BillScan, error) {
	if len(files) == 0 {
		return domain.BillScan{}, validationError("no receipt file was uploaded")
	}
	if len(files) > MaxBillScanFiles {
		return domain.BillScan{}, validationError("a receipt can be uploaded in at most %d files — %d were given", MaxBillScanFiles, len(files))
	}

	// Per-part validation: the same rules the single-file upload always had,
	// now naming the offending part. Conversion to the normalized JPEG form
	// happens per file after validation; the hash stays over the ORIGINAL
	// upload bytes so dedup never depends on transcode reproducibility.
	parts := make([]scanPart, 0, len(files))
	seen := make(map[string]bool, len(files))
	for i, f := range files {
		mimeType := strings.ToLower(f.MimeType)
		if _, ok := allowedMime[mimeType]; !ok {
			// Browsers outside Safari often upload .heic/.pdf with a generic
			// content type — sniff the magic bytes before rejecting the upload.
			if sniffed := sniffMime(f.Data); sniffed != "" {
				if _, ok := allowedMime[sniffed]; ok {
					mimeType = sniffed
				}
			}
		}
		if _, ok := allowedMime[mimeType]; !ok {
			return domain.BillScan{}, validationError("unsupported file type %q in file %d of %d (want jpeg, png, webp, heic, heif, gif, tiff, bmp or pdf)", mimeType, i+1, len(files))
		}
		if len(f.Data) == 0 {
			return domain.BillScan{}, validationError("receipt file %d of %d is empty", i+1, len(files))
		}
		if len(f.Data) > MaxBillImageBytes {
			return domain.BillScan{}, validationError("receipt file %d of %d exceeds the %d MB limit", i+1, len(files), MaxBillImageBytes>>20)
		}
		hash := fileHash(f.Data)
		if seen[hash] {
			return domain.BillScan{}, validationError("file %d of %d is a duplicate of an earlier file in this upload", i+1, len(files))
		}
		seen[hash] = true
		// Normalize into JPEG (every format but JPEG itself is decoded,
		// EXIF-oriented, downscaled and re-encoded; a multi-page scanned PDF
		// expands to one part per page). Legacy formats that cannot be
		// converted pass through in their original form.
		outs, err := imageproc.Process(ctx, f.Data, mimeType, imageproc.ReceiptOpts)
		if err != nil {
			return domain.BillScan{}, validationError("file %d of %d: %v", i+1, len(files), err)
		}
		for _, o := range outs {
			parts = append(parts, scanPart{data: o.Data, mime: o.MimeType, ext: allowedMime[o.MimeType], hash: hash})
		}
	}
	if len(parts) > MaxBillScanFiles {
		return domain.BillScan{}, validationError("the %d uploaded files expand to %d receipt parts — at most %d are allowed in one receipt", len(files), len(parts), MaxBillScanFiles)
	}

	// Duplicate protection: the same receipt (any of its parts) must not be
	// processed twice. The content hash of any part matches an active scan
	// (still analyzing / awaiting review) or an already-saved bill.
	for i, p := range parts {
		if _, err := s.scans.GetByFileHash(ctx, p.hash); err == nil {
			return domain.BillScan{}, conflictError("duplicate receipt: file %d of %d is already being analyzed — check the bills view", i+1, len(parts))
		} else if !errors.Is(err, domain.ErrNotFound) {
			return domain.BillScan{}, fmt.Errorf("check scan duplicate: %w", err)
		}
		if existing, err := s.bills.GetByFileHash(ctx, p.hash); err == nil {
			return domain.BillScan{}, conflictError("duplicate receipt: file %d of %d was already saved as bill #%d", i+1, len(parts), existing.ID)
		} else if !errors.Is(err, domain.ErrNotFound) {
			return domain.BillScan{}, fmt.Errorf("check bill duplicate: %w", err)
		}
	}

	provider, err := s.resolveProvider(ctx, providerID)
	if err != nil {
		return domain.BillScan{}, err
	}

	// Save every part; a later failure removes what was already written so no
	// orphan files are left behind.
	paths := make([]string, 0, len(parts))
	removeParts := func() {
		for _, p := range paths {
			_ = os.Remove(p)
		}
	}
	for _, p := range parts {
		path, err := s.saveReceipt(p.ext, p.data)
		if err != nil {
			removeParts()
			return domain.BillScan{}, fmt.Errorf("save receipt: %w", err)
		}
		paths = append(paths, path)
	}

	token, err := newScanToken()
	if err != nil {
		removeParts()
		return domain.BillScan{}, err
	}

	scanFiles := make([]domain.BillScanFile, len(parts))
	for i, p := range parts {
		scanFiles[i] = domain.BillScanFile{
			Position: i + 1,
			Path:     paths[i],
			MimeType: p.mime,
			FileHash: p.hash,
		}
	}

	// The row's own single-file columns mirror part 1 (legacy consumers);
	// bill_scan_files carries every part.
	if _, err := s.scans.Create(ctx, domain.BillScan{
		ScanToken:  token,
		ImagePath:  paths[0],
		MimeType:   parts[0].mime,
		ProviderID: provider.ID,
		FileHash:   parts[0].hash,
		Files:      scanFiles,
	}); err != nil {
		removeParts()
		return domain.BillScan{}, err
	}

	if !s.enqueue(token) {
		// Queue full: be honest instead of losing the upload — drop the row
		// and every part file and tell the client to retry.
		if _, derr := s.scans.Delete(context.Background(), token); derr != nil {
			s.log.Warn("drop scan row after queue-full", "token", token, "error", derr)
		}
		removeParts()
		return domain.BillScan{}, validationError("analysis queue is full — try again in a moment")
	}
	s.sweepStaleScans()
	return domain.BillScan{
		ScanToken:  token,
		Status:     domain.BillScanAnalyzing,
		ProviderID: provider.ID,
		FileCount:  len(parts),
		CreatedAt:  time.Now().UTC(),
	}, nil
}

// GetScan returns one scan's pipeline state, or domain.ErrNotFound for
// unknown/consumed/expired tokens.
func (s *BillService) GetScan(ctx context.Context, token string) (domain.BillScan, error) {
	scan, err := s.scans.GetByToken(ctx, token)
	if err != nil {
		return domain.BillScan{}, err
	}
	if scan.Status == domain.BillScanDone && scan.Draft != nil {
		// Defensive: ids are already 1..n in the stored JSON.
		assignDraftItemIDs(scan.Draft, 0)
	}
	return scan, nil
}

// ListScans returns recent scans (default 50) in the given states — all
// states when none is given.
func (s *BillService) ListScans(ctx context.Context, statuses []domain.BillScanStatus, limit int) ([]domain.BillScan, error) {
	for _, st := range statuses {
		if !st.Valid() {
			return nil, validationError("unknown scan status %q", st)
		}
	}
	return s.scans.List(ctx, statuses, limit)
}

// Reextract re-runs AI extraction on a finished or failed scan (retry),
// optionally with a different provider. The scan returns to the analyzing
// state and the client polls again; any earlier draft edits are lost.
func (s *BillService) Reextract(ctx context.Context, token, providerID string) (domain.BillScan, error) {
	provider, err := s.resolveProvider(ctx, providerID)
	if err != nil {
		return domain.BillScan{}, err
	}

	claimed, err := s.scans.ClaimRetry(ctx, token, provider.ID)
	if err != nil {
		return domain.BillScan{}, err
	}
	if !claimed {
		// Either the token is unknown or the scan is still analyzing.
		if _, gerr := s.scans.GetByToken(ctx, token); gerr != nil {
			return domain.BillScan{}, fmt.Errorf("scan %s not found or expired — scan the receipt again: %w", token, domain.ErrNotFound)
		}
		return domain.BillScan{}, validationError("scan is currently being analyzed — wait for it to finish")
	}

	if !s.enqueue(token) {
		// Queue full: the row stays analyzing and the sweeper re-enqueues it
		// within minutes — but say so, the retry is not immediate.
		s.log.Warn("reextract dropped by full queue", "token", token)
	}
	return domain.BillScan{
		ScanToken:  token,
		Status:     domain.BillScanAnalyzing,
		ProviderID: provider.ID,
	}, nil
}

// RescanBill re-reads a saved bill's receipt the way a fresh scan works: the
// bill (and the expense transaction recorded with it) is removed, and its
// stored receipt parts are handed to a new scan pipeline row — background
// extraction, standard review queue, confirmation as an entirely new bill.
// The receipt files move with the scan row: the bill no longer exists, so the
// scan owns them (confirm keeps them as the new bill's record; discarding
// deletes them, like any scan).
func (s *BillService) RescanBill(ctx context.Context, billID int64, providerID string) (domain.BillScan, error) {
	bill, err := s.bills.GetByID(ctx, billID)
	if err != nil {
		return domain.BillScan{}, fmt.Errorf("load bill %d: %w", billID, err)
	}
	files := billFilesFromBill(bill)
	if len(files) == 0 {
		return domain.BillScan{}, validationError("bill #%d has no stored receipt to re-read", bill.ID)
	}
	provider, err := s.resolveProvider(ctx, providerID)
	if err != nil {
		return domain.BillScan{}, err
	}

	token, err := newScanToken()
	if err != nil {
		return domain.BillScan{}, err
	}
	scanFiles := make([]domain.BillScanFile, len(files))
	for i, f := range files {
		scanFiles[i] = domain.BillScanFile{
			Position: f.Position,
			Path:     f.Path,
			MimeType: f.MimeType,
			FileHash: f.FileHash,
		}
	}
	// A previous rescan of this receipt may still be sitting in the pipeline
	// (analyzing/done) — its hashes would make the new row trip the duplicate
	// upload check. Drop the stale rows; the files stay on disk, this scan
	// now owns them. A worker still generating for a dropped row writes its
	// result guarded on status='analyzing' (0 rows) — it can never resurrect.
	for _, f := range files {
		if old, err := s.scans.GetByFileHash(ctx, f.FileHash); err == nil {
			if _, derr := s.scans.Delete(ctx, old.ScanToken); derr != nil {
				s.log.Warn("drop superseded rescan row", "token", old.ScanToken, "error", derr)
			}
		}
	}
	if _, err := s.scans.Create(ctx, domain.BillScan{
		ScanToken:  token,
		ImagePath:  files[0].Path,
		MimeType:   files[0].MimeType,
		ProviderID: provider.ID,
		FileHash:   files[0].FileHash,
		Files:      scanFiles,
	}); err != nil {
		return domain.BillScan{}, fmt.Errorf("create rescan row: %w", err)
	}
	// The bill rows go now — the review draft replacing them is already saved.
	// The receipt files deliberately stay on disk: with the bill gone the scan
	// row owns them.
	if err := s.bills.Delete(ctx, bill.ID); err != nil {
		// Roll the pipeline row back so no orphan review draft lingers.
		if _, derr := s.scans.Delete(context.Background(), token); derr != nil {
			s.log.Warn("drop rescan row after failed bill delete", "token", token, "error", derr)
		}
		return domain.BillScan{}, fmt.Errorf("delete bill %d for rescan: %w", bill.ID, err)
	}
	if bill.TransactionID != nil {
		// Best-effort like Delete: a lingering transaction only distorts the
		// account balance until re-confirmation — warn, don't block the read.
		if err := s.txStore.Delete(ctx, *bill.TransactionID); err != nil && !errors.Is(err, domain.ErrNotFound) {
			s.log.Warn("rescan deleted bill but its transaction remains",
				"bill_id", bill.ID, "transaction_id", *bill.TransactionID, "error", err)
		}
	}
	if !s.enqueue(token) {
		// Queue-full after the delete is still safe: the row stays analyzing
		// and the sweeper re-enqueues it within minutes.
		s.log.Warn("rescan dropped by full queue", "token", token)
	}
	s.sweepStaleScans()
	return domain.BillScan{
		ScanToken:  token,
		Status:     domain.BillScanAnalyzing,
		ProviderID: provider.ID,
		FileCount:  len(files),
		CreatedAt:  time.Now().UTC(),
	}, nil
}

// Confirm persists the (user-corrected) draft as an accepted bill and records
// one expense transaction for the printed total. A bill confirmed without an
// account was paid with wallet money: it lands on the default wallet account
// instead, so it still shows in the dashboard totals. The scan row is deleted;
// the receipt file stays as the permanent record.
func (s *BillService) Confirm(ctx context.Context, token string, in domain.BillConfirmInput) (domain.Bill, error) {
	scan, err := s.scans.GetByToken(ctx, token)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.Bill{}, fmt.Errorf("scan %s not found or expired — scan the receipt again: %w", token, domain.ErrNotFound)
		}
		return domain.Bill{}, err
	}
	switch {
	case scan.Status == domain.BillScanAnalyzing:
		return domain.Bill{}, validationError("scan is still being analyzed — try again in a few moments")
	case scan.Status != domain.BillScanDone || scan.Draft == nil:
		return domain.Bill{}, validationError("analysis failed — retry or discard the scan")
	}

	account, err := s.resolveBillAccount(ctx, in)
	if err != nil {
		return domain.Bill{}, err
	}
	if err := s.checkBillBagAccount(ctx, in, account.ID); err != nil {
		return domain.Bill{}, err
	}
	applyAccountPaymentRules(&in, account)

	bill, err := s.buildBill(ctx, in, &billScanSource{files: billFilesFromScan(scan), providerID: scan.ProviderID})
	if err != nil {
		return domain.Bill{}, err
	}

	created, err := s.bills.Create(ctx, bill)
	if err != nil {
		return domain.Bill{}, err
	}

	if err := s.recordBillTransaction(ctx, created.ID, bill, account.ID, in.FxRate); err != nil {
		return domain.Bill{}, err
	}

	// Learn the user's standardized-name corrections from the reviewed lines
	// (non-fatal — the bill is already saved).
	s.learnMappingOverrides(ctx, in.Items, priorStandardFromDraft(scan.Draft.Items))
	s.enqueueInsight(ctx, created.ID)

	// The bill exists — consume the scan row. (Deleting after Create keeps a
	// crash between the two a redoable confirm.)
	if _, err := s.scans.Delete(ctx, token); err != nil {
		s.log.Warn("consume confirmed scan row", "token", token, "error", err)
	}
	return created, nil
}

// Update applies user corrections to an already-saved bill. The total is
// recomputed from the edited lines exactly as at confirm time, and the linked
// expense transaction (if any) is kept in sync — including re-pointing it when
// the request carries a new account_id. A bill that never got a transaction
// (e.g. confirmed before accounts existed) gets one created here as soon as an
// account is given.
func (s *BillService) Update(ctx context.Context, id int64, in domain.BillConfirmInput) (domain.Bill, error) {
	existing, err := s.bills.GetByID(ctx, id)
	if err != nil {
		return domain.Bill{}, err
	}

	// nil account_id = "leave the account as it is" (older payloads); an
	// explicit id re-points the expense transaction.
	var account domain.Account
	if in.AccountID != nil && *in.AccountID > 0 {
		if account, err = s.accounts.GetByID(ctx, *in.AccountID); err != nil {
			return domain.Bill{}, fmt.Errorf("validate account_id: %w", err)
		}
		applyAccountPaymentRules(&in, account)
	}

	// nil account_id = "leave the account as it is": the bag tie is checked
	// against the expense account the bill's linked transaction was recorded
	// on. A bill with no account at all cannot be bag-tagged — the ledger tie
	// would be a guess.
	bagAccount := int64(0)
	if account.ID > 0 {
		bagAccount = account.ID
	} else if existing.AccountID != nil {
		bagAccount = *existing.AccountID
	}
	if err := s.checkBillBagAccount(ctx, in, bagAccount); err != nil {
		return domain.Bill{}, err
	}

	source := &billScanSource{files: billFilesFromBill(existing), providerID: existing.ExtractedBy}
	bill, err := s.buildBill(ctx, in, source)
	if err != nil {
		return domain.Bill{}, err
	}
	bill.ID = id
	bill.CreatedAt = existing.CreatedAt
	bill.TransactionID = existing.TransactionID

	if _, err := s.bills.Update(ctx, bill); err != nil {
		return domain.Bill{}, err
	}

	if existing.TransactionID != nil {
		if err := s.syncBillTransaction(ctx, *existing.TransactionID, bill, account.ID, in.FxRate); err != nil {
			return domain.Bill{}, err
		}
	} else if account.ID > 0 {
		// Pre-account bill now knows where the money came from — record the
		// missing expense transaction and link it.
		if err := s.recordBillTransaction(ctx, id, bill, account.ID, in.FxRate); err != nil {
			return domain.Bill{}, err
		}
	}
	// Learn the user's standardized-name corrections from the edited lines
	// (non-fatal — the bill is already saved).
	s.learnMappingOverrides(ctx, in.Items, priorStandardFromItems(existing.Items))
	s.enqueueInsight(ctx, id)

	// Re-read so the response reflects the (possibly re-pointed or newly
	// linked) transaction's account.
	return s.bills.GetByID(ctx, id)
}

// enqueueInsight fires the deferred price-per-unit analysis of the saved
// bill (fire-and-forget: a nil analyzer or an enqueue error is logged and
// dropped — the analysis happens in a River job, away from the request path).
func (s *BillService) enqueueInsight(ctx context.Context, billID int64) {
	if s.insights == nil {
		return
	}
	if err := s.insights.AnalyzeBill(ctx, billID); err != nil {
		s.log.Warn("enqueue bill analysis", "bill_id", billID, "error", err)
	}
}

// resolveBillAccount picks the expense target for a bill: the requested
// account, or the default wallet when the request leaves it open (wallet money
// by convention).
func (s *BillService) resolveBillAccount(ctx context.Context, in domain.BillConfirmInput) (domain.Account, error) {
	if in.AccountID != nil && *in.AccountID > 0 {
		account, err := s.accounts.GetByID(ctx, *in.AccountID)
		if err != nil {
			return domain.Account{}, fmt.Errorf("validate account_id: %w", err)
		}
		return account, nil
	}
	walletID, err := s.ensureWalletAccount(ctx)
	if err != nil {
		return domain.Account{}, fmt.Errorf("ensure default wallet account: %w", err)
	}
	return s.accounts.GetByID(ctx, walletID)
}

// applyAccountPaymentRules couples payment metadata to the account: money that
// left a cash-type account (the wallet) is a cash payment — card digits have no
// business there and would flip the payment back to "card" in buildBill.
func applyAccountPaymentRules(in *domain.BillConfirmInput, account domain.Account) {
	if account.Type != domain.AccountCash {
		return
	}
	in.PaymentMethod = "cash"
	in.CardLastDigits = ""
}

// checkBillBagAccount ties every bag reference (bill-level and per-line) to
// the account the bill's expense is recorded on — a bag lives inside one
// account, so spend tagged into it must leave that same account. accountID <=
// 0 (a bill with no resolvable expense account) refuses any bag tag.
func (s *BillService) checkBillBagAccount(ctx context.Context, in domain.BillConfirmInput, accountID int64) error {
	refs := make([]*int64, 0, len(in.Items)+1)
	if in.BagID != nil {
		refs = append(refs, in.BagID)
	}
	for i := range in.Items {
		if in.Items[i].BagID != nil {
			refs = append(refs, in.Items[i].BagID)
		}
	}
	if len(refs) == 0 {
		return nil
	}
	if accountID <= 0 {
		return validationError("tagging a bag needs the bill's expense account — a bag lives on one account")
	}
	seen := map[int64]bool{}
	for _, ref := range refs {
		if seen[*ref] {
			continue
		}
		seen[*ref] = true
		bag, err := s.bags.GetBagByID(ctx, *ref)
		if err != nil {
			return validationError("validate bag_id: %v", err)
		}
		if bag.AccountID != accountID {
			return validationError("bag %q belongs to another account — record the expense on %q",
				bag.Name, s.accNameOrID(ctx, bag.AccountID))
		}
	}
	return nil
}

// accNameOrID names an account for an error message, falling back to its id
// (the account was already validated, so this is display only).
func (s *BillService) accNameOrID(ctx context.Context, id int64) string {
	if a, err := s.accounts.GetByID(ctx, id); err == nil {
		return a.Name
	}
	return fmt.Sprintf("#%d", id)
}

// recordBillTransaction writes the expense transaction for a bill and links it,
// so later bill edits keep it in sync.
func (s *BillService) recordBillTransaction(ctx context.Context, billID int64, bill domain.Bill, accountID int64, fxRate *float64) error {
	// BillID marks the row as bill-linked (readonly in the activity view, the
	// UI links back to the bill); it is round-tripped by every later
	// transaction Update and never cleared while the bill lives. BagID mirrors
	// the bill-level tag display-only: the bag spend sums come from the bill
	// itself (per-line COALESCE), the transaction row is excluded by its
	// bill_id while bag_name travels on it for the activity list.
	accountAmount, rate, err := s.accountLedgerAmount(ctx, bill, accountID, fxRate)
	if err != nil {
		return err
	}
	txRow, err := s.txStore.Create(ctx, domain.Transaction{
		AccountID:          accountID,
		Kind:               domain.TransactionExpense,
		AmountCents:        bill.TotalCents,
		Currency:           bill.Currency,
		Description:        billDescription(bill),
		Date:               nonEmptyOr(bill.Date, time.Now().Format("2006-01-02")),
		BillID:             &billID,
		BagID:              bill.BagID,
		AccountAmountCents: accountAmount,
		FxRate:             rate,
	})
	if err != nil {
		return fmt.Errorf("record bill transaction: %w", err)
	}
	if err := s.bills.SetTransaction(ctx, billID, txRow.ID); err != nil {
		return err
	}
	return nil
}

// accountLedgerAmount converts a bill total in the bill's currency into the
// paying account's currency when the two differ — a bill in one currency paid
// by a card in another deducts the ACCOUNT's currency from its balance. The
// user's rate wins over the FX snapshot (banks don't always follow the public
// reference); nil rate with no snapshot hit degrades to NULL (the balance read
// folds 1:1, the accepted degradation). FxRate given while both currencies are
// the same is refused, not silently dropped.
func (s *BillService) accountLedgerAmount(ctx context.Context, bill domain.Bill, accountID int64, fxRate *float64) (*int64, *float64, error) {
	if fxRate != nil && *fxRate <= 0 {
		return nil, nil, validationError("fx_rate must be positive when given")
	}
	if accountID <= 0 {
		return nil, nil, nil
	}
	account, err := s.accounts.GetByID(ctx, accountID)
	if errors.Is(err, domain.ErrNotFound) {
		// Stale linkage (the account vanished mid-flight): degrade to 1:1, the
		// row keeps its own currency and no conversion is recorded.
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("load bill expense account: %w", err)
	}
	if account.Currency == bill.Currency {
		if fxRate != nil {
			return nil, nil, validationError(
				"fx_rate is only valid when the bill's currency differs from the paying account's")
		}
		return nil, nil, nil
	}
	if fxRate != nil {
		converted := fx.ConvertCents(bill.TotalCents, *fxRate)
		return &converted, fxRate, nil
	}
	if s.rates != nil {
		if snap, err := s.rates.Snapshot(ctx); err == nil {
			if rate, ok := snap.Rate(bill.Currency, account.Currency); ok {
				converted := fx.ConvertCents(bill.TotalCents, rate)
				return &converted, &rate, nil
			}
			s.log.WarnContext(ctx, "bill currency conversion degraded to 1:1 — no rate for currency pair",
				"from", bill.Currency, "to", account.Currency)
		} else {
			s.log.WarnContext(ctx, "bill currency conversion degraded to 1:1 — rate fetch failed", "error", err)
		}
	}
	return nil, nil, nil
}

// billDescription is the description used for the expense transaction a bill
// creates ("Bill — <market>").
func billDescription(b domain.Bill) string {
	description := "Bill"
	if b.MarketName != "" {
		description += " — " + b.MarketName
	}
	return description
}

// syncBillTransaction refreshes the expense transaction recorded at confirm
// time so reports reflect the edited bill. accountID > 0 re-points the
// transaction to another account (a bill whose account was edited); 0 keeps
// the account the row carries. The currency conversion (bill vs account) is
// recomputed on every sync — an edited bill or a re-pointed account refreshes
// account_amount_cents, and a same-currency pair clears it.
func (s *BillService) syncBillTransaction(ctx context.Context, txID int64, bill domain.Bill, accountID int64, fxRate *float64) error {
	tx, err := s.txStore.GetByID(ctx, txID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil // transaction was deleted — nothing to sync
	}
	if err != nil {
		return fmt.Errorf("load bill transaction: %w", err)
	}
	if accountID <= 0 {
		accountID = tx.AccountID
	}
	accountAmount, rate, err := s.accountLedgerAmount(ctx, bill, accountID, fxRate)
	if err != nil {
		return err
	}
	tx.AmountCents = bill.TotalCents
	tx.Currency = bill.Currency
	tx.Description = billDescription(bill)
	if bill.Date != "" {
		tx.Date = bill.Date
	}
	if accountID > 0 {
		tx.AccountID = accountID
	}
	tx.BagID = bill.BagID // display-only mirror; bill lines stay the bag truth
	tx.AccountAmountCents = accountAmount
	tx.FxRate = rate
	if _, err := s.txStore.Update(ctx, tx); err != nil {
		return fmt.Errorf("sync bill transaction: %w", err)
	}
	return nil
}

// CancelScan aborts an in-progress analysis: the scan moves from analyzing to
// the cancelled state and the running AI extraction is signalled to stop. The
// receipt files are kept — the request can be deleted (DiscardScan), re-run
// (Reextract claims the cancelled scan too) or simply swept with the session
// TTL. A scan that already finished (or was never analyzing) is not an error
// the caller can act on, but an unknown token is not found.
func (s *BillService) CancelScan(ctx context.Context, token string) (domain.BillScan, error) {
	scan, err := s.scans.GetByToken(ctx, token)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.BillScan{}, fmt.Errorf("scan %s not found or expired — scan the receipt again: %w", token, domain.ErrNotFound)
		}
		return domain.BillScan{}, err
	}

	// Mark first: once the row is cancelled, a worker result write (which only
	// fires from 'analyzing') can no longer resurrect it, whatever the abort
	// signal's timing.
	marked, err := s.scans.MarkCancelled(ctx, token)
	if err != nil {
		return domain.BillScan{}, err
	}
	if marked {
		// The worker is mid-extraction: cancel its context so the model call
		// stops instead of being left generating for an abandoned scan. When no
		// cancel func is registered the scan is still queued — the worker skips
		// non-analyzing rows on pickup, so nothing else is needed.
		if cancel := s.lookupCanceller(token); cancel != nil {
			cancel()
		}
		s.log.Info("scan cancelled by user", "token", token)
	}

	// Re-read so the caller sees the resulting state (including a scan that
	// finished the moment before the cancel landed).
	fresh, err := s.scans.GetByToken(ctx, token)
	if err != nil {
		return scan, nil // swept/confirmed in between — the earlier state stands
	}
	return fresh, nil
}

// registerCanceller records the context cancel func able to abort the
// extraction currently running for a scan token.
func (s *BillService) registerCanceller(token string, cancel context.CancelFunc) {
	s.cancelMu.Lock()
	defer s.cancelMu.Unlock()
	s.cancellers[token] = cancel
}

// removeCanceller drops the entry when the extraction is over (called with
// defer — the entry must leave the map even on a panic path).
func (s *BillService) removeCanceller(token string) {
	s.cancelMu.Lock()
	defer s.cancelMu.Unlock()
	delete(s.cancellers, token)
}

// lookupCanceller returns the registered cancel func for a token (nil = the
// extraction has not started or is already over).
func (s *BillService) lookupCanceller(token string) context.CancelFunc {
	s.cancelMu.Lock()
	defer s.cancelMu.Unlock()
	return s.cancellers[token]
}

// DiscardScan drops an unconfirmed scan and deletes its receipt files (every
// part). A worker that is still extracting it matches 0 rows when writing its
// result, so a discarded scan is never resurrected.
func (s *BillService) DiscardScan(ctx context.Context, token string) error {
	scan, err := s.scans.Delete(ctx, token)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("scan %s not found or expired: %w", token, domain.ErrNotFound)
		}
		return err
	}
	for _, path := range scanFilePaths(scan) {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("delete receipt file: %w", err)
		}
	}
	return nil
}

// scanFilePaths lists every file of a removed scan (all parts, or the
// mirrored single file of a pre-feature row).
func scanFilePaths(scan domain.BillScan) []string {
	if len(scan.Files) > 0 {
		paths := make([]string, 0, len(scan.Files))
		for _, f := range scan.Files {
			if f.Path != "" {
				paths = append(paths, f.Path)
			}
		}
		return paths
	}
	if scan.ImagePath != "" {
		return []string{scan.ImagePath}
	}
	return nil
}

func (s *BillService) Get(ctx context.Context, id int64) (domain.Bill, error) {
	return s.bills.GetByID(ctx, id)
}

// Delete removes a confirmed bill for good: the bill with its item lines, the
// expense transaction recorded at confirm time, and the stored receipt file.
// The bill row is deleted first; the transaction and file cleanup afterwards
// are best-effort (warn on failure) so a retry never hits a 404 on an already
// deleted bill.
func (s *BillService) Delete(ctx context.Context, id int64) error {
	bill, err := s.bills.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if err := s.bills.Delete(ctx, id); err != nil {
		return err
	}

	if bill.TransactionID != nil {
		if err := s.txStore.Delete(ctx, *bill.TransactionID); err != nil && !errors.Is(err, domain.ErrNotFound) {
			s.log.Warn("bill deleted but its transaction remains",
				"bill_id", id, "transaction_id", *bill.TransactionID, "error", err)
		}
	}

	files := billFilesFromBill(bill)
	for _, f := range files {
		if f.Path == "" {
			continue
		}
		if err := os.Remove(f.Path); err != nil && !os.IsNotExist(err) {
			s.log.Warn("bill deleted but a receipt file remains",
				"bill_id", id, "path", f.Path, "error", err)
		}
	}
	return nil
}

func (s *BillService) List(ctx context.Context, f domain.BillFilters) ([]domain.Bill, error) {
	if f.Month != "" {
		if err := validateMonth(f.Month, "month"); err != nil {
			return nil, err
		}
	}
	return s.bills.List(ctx, f)
}

// Brands lists the distinct brands already recorded on bill items, for the
// review dropdown.
func (s *BillService) Brands(ctx context.Context) ([]string, error) {
	return s.bills.ListBrands(ctx)
}

// ReceiptImagePath resolves one stored receipt part for serving. part is
// 1-based; part <= 0 means part 1 (the classic single-receipt request).
func (s *BillService) ReceiptImagePath(ctx context.Context, billID int64, part int) (string, string, error) {
	bill, err := s.bills.GetByID(ctx, billID)
	if err != nil {
		return "", "", err
	}
	if part <= 0 {
		part = 1
	}
	files := billFilesFromBill(bill)
	if part > len(files) {
		return "", "", validationError("bill %d has no receipt part %d", billID, part)
	}
	f := files[part-1]
	if f.Path == "" {
		return "", "", validationError("bill %d has no stored receipt", billID)
	}
	mimeType := f.MimeType
	if mimeType == "" {
		mimeType = detectMime(f.Path)
	}
	return f.Path, mimeType, nil
}

// resolveProvider picks the requested provider or falls back to the connector
// flagged as the default for bill reads (any configured one when none is).
func (s *BillService) resolveProvider(ctx context.Context, providerID string) (domain.AIProvider, error) {
	if strings.TrimSpace(providerID) != "" {
		return s.providers.GetProvider(ctx, providerID)
	}
	return s.providers.DefaultBillProvider(ctx)
}

// buildBill validates the confirmed draft and turns it into a Bill ready for
// persistence. The total is always computed from the edited lines minus the
// bill-level global discount (VAT is already included in the prices); the
// receipt's printed amount is carried through for the mismatch warning.
func (s *BillService) buildBill(ctx context.Context, in domain.BillConfirmInput, source *billScanSource) (domain.Bill, error) {
	if in.VATCents < 0 || in.DiscountCents < 0 || in.PrintedTotalCents < 0 {
		return domain.Bill{}, validationError("totals must not be negative")
	}
	date := strings.TrimSpace(in.Date)
	if date != "" {
		if _, err := time.Parse("2006-01-02", date); err != nil {
			return domain.Bill{}, validationError("date must be YYYY-MM-DD")
		}
	} else {
		// An absent/unreadable receipt date must not save as an empty string —
		// an empty date matches no month, so the bill silently disappears from
		// the month-filtered bills list (substr('', 1, 7) never equals 'YYYY-MM').
		// The receipt was scanned today, so today is the honest fallback — the
		// same rule the linked transaction already applies to its own date.
		date = time.Now().Format("2006-01-02")
	}
	currency := strings.ToUpper(strings.TrimSpace(in.Currency))
	if currency == "" {
		// Undetected/unset currency: the user's base currency is the best
		// guess — never silently USD.
		base, err := s.providers.BaseCurrency(ctx)
		if err != nil {
			return domain.Bill{}, err
		}
		currency = base
	} else if cur, err := normalizeCurrency(in.Currency); err != nil {
		return domain.Bill{}, err
	} else {
		currency = cur
	}

	cardDigits := normalizeDigits(in.CardLastDigits)
	// The bill-level budget is the default attribution for every line; items
	// may override it per line (nil = inherit). Closed envelopes no longer
	// accept spend. The same rule applies to bag tagging (existence + open
	// state here; the account tie is checked by the caller, which knows the
	// bill's expense account).
	if in.BudgetID != nil {
		b, err := s.budgets.GetByID(ctx, *in.BudgetID)
		if err != nil {
			return domain.Bill{}, fmt.Errorf("validate budget_id: %w", err)
		}
		if b.Status == domain.BudgetClosed {
			return domain.Bill{}, validationError("budget %d is closed and no longer accepts spend", b.ID)
		}
	}
	if in.BagID != nil {
		if s.bags == nil {
			return domain.Bill{}, validationError("bag tagging is unavailable")
		}
		bag, err := s.bags.GetBagByID(ctx, *in.BagID)
		if err != nil {
			return domain.Bill{}, fmt.Errorf("validate bag_id: %w", err)
		}
		if bag.Status == domain.BagClosed {
			return domain.Bill{}, validationError("bag %q is closed and no longer accepts spend", bag.Name)
		}
	}
	items := make([]domain.BillItem, 0, len(in.Items))
	itemsSubtotal := int64(0)
	for _, it := range in.Items {
		name := strings.TrimSpace(it.Name)
		if name == "" {
			return domain.Bill{}, validationError("every item needs a name")
		}
		// Deposit returns ("Leergut") are money back: their unit price and
		// line total may be negative, and they reduce the bill total. The
		// same applies to any line filed under a category that allows
		// negatives (the seeded "Deposit & Returns" / Pfand family).
		// Deposit/coupon artifact lines generally ("Pfand", "Rabatt",
		// "Gutschein", …) carry money-back amounts too — however their AI or
		// user filed the category, a negative line total must survive (the
		// reviewed total is honored as-is; a clamp here would silently drift
		// the registered bill away from the confirmed one).
		isReturn := domain.IsDepositReturn(name)
		allowsNegative := isReturn || domain.IsDepositArtifact(name)
		if it.CategoryID != nil {
			cat, err := s.categories.GetByID(ctx, *it.CategoryID)
			if err != nil {
				return domain.Bill{}, fmt.Errorf("validate category for %q: %w", name, err)
			}
			allowsNegative = allowsNegative || cat.AllowsNegative
		}
		if it.Quantity <= 0 {
			return domain.Bill{}, validationError("quantity for %q must be positive", name)
		}
		if (!allowsNegative && it.UnitPriceCents < 0) || (!allowsNegative && it.DiscountCents < 0) {
			return domain.Bill{}, validationError("prices for %q must not be negative", name)
		}
		if it.BudgetID != nil && (in.BudgetID == nil || *it.BudgetID != *in.BudgetID) {
			b, err := s.budgets.GetByID(ctx, *it.BudgetID)
			if err != nil {
				return domain.Bill{}, fmt.Errorf("validate budget for %q: %w", name, err)
			}
			if b.Status == domain.BudgetClosed {
				return domain.Bill{}, validationError("budget %d is closed and no longer accepts spend", b.ID)
			}
		}
		// Per-line bag override (nil = the bill's bag): existence + open state.
		if it.BagID != nil && (in.BagID == nil || *it.BagID != *in.BagID) {
			if s.bags == nil {
				return domain.Bill{}, validationError("bag tagging is unavailable")
			}
			bag, err := s.bags.GetBagByID(ctx, *it.BagID)
			if err != nil {
				return domain.Bill{}, fmt.Errorf("validate bag for %q: %w", name, err)
			}
			if bag.Status == domain.BagClosed {
				return domain.Bill{}, validationError("bag %q is closed and no longer accepts spend", bag.Name)
			}
		}
		// The reviewed line total is the confirmed truth. The AI's line total
		// may legitimately differ from qty×unit−discount — e.g. a coupon the
		// model filed with unit price 0.00 but the printed −0,60 in its line
		// total — and recomputing silently rewrote exactly what the user had
		// reviewed in the editor (a 93.80 receipt registered as 94.40). The
		// recompute is only the fallback for payloads that omit the line total
		// (legacy callers; all-zero line totals with a non-0 recompute read as
		// "absent" so omitting the field never erases real lines).
		lineCents := it.LineTotalCents
		if recomputed := it.Quantity*float64(it.UnitPriceCents) - float64(it.DiscountCents); lineCents == 0 && int64(
			math.Round(recomputed),
		) != 0 {
			lineCents = int64(math.Round(recomputed))
		}
		if lineCents < 0 && !allowsNegative {
			lineCents = 0
		}
		itemsSubtotal += lineCents
		items = append(items, domain.BillItem{
			Name:           name,
			Brand:          strings.TrimSpace(it.Brand),
			Unit:           strings.ToLower(strings.TrimSpace(it.Unit)),
			UnitValue:      sanitizeUnitValue(it.UnitValue),
			CategoryID:     it.CategoryID,
			Quantity:       it.Quantity,
			UnitPriceCents: it.UnitPriceCents,
			DiscountCents:  it.DiscountCents,
			LineTotalCents: lineCents,
			IsReturn:       isReturn,
			BudgetID:       it.BudgetID,
			BagID:          it.BagID,
		})
	}

	// The total is never taken from the client: it is always recomputed as the
	// sum of the lines minus the bill-level discount — the receipt-wide rebate
	// printed after the article lines (e.g. "10% Rabatt"), which the lines
	// themselves do not carry. VAT is already included in each item's price,
	// so VAT must not be added again. Card digits imply card payment.
	total := itemsSubtotal - in.DiscountCents
	printed := in.PrintedTotalCents
	if printed <= 0 {
		printed = total // nothing printed → no mismatch warning
	}
	payment := strings.ToLower(strings.TrimSpace(in.PaymentMethod))
	if cardDigits != "" {
		payment = "card"
	}

	storeID, canonicalMarket, err := s.resolveStore(ctx, in.MarketName)
	if err != nil {
		return domain.Bill{}, err
	}

	// Link each line to its catalogue product, re-resolved on every build so a
	// renamed/edited line re-points (BillRepository.Update re-inserts lines).
	for i := range items {
		items[i].ProductID = s.resolveProduct(ctx, items[i])
	}

	// Part 1 mirrors the legacy single-file columns; every part is persisted as
	// the bill's receipt record.
	imagePath, fileHash := "", ""
	if len(source.files) > 0 {
		imagePath, fileHash = source.files[0].Path, source.files[0].FileHash
	}

	return domain.Bill{
		MarketName:         canonicalMarket,
		Date:               date,
		PaymentMethod:      payment,
		CardLastDigits:     cardDigits,
		Currency:           currency,
		ItemsSubtotalCents: itemsSubtotal,
		DiscountCents:      in.DiscountCents,
		VATCents:           in.VATCents,
		TotalCents:         total,
		PrintedTotalCents:  printed,
		Status:             domain.BillStatusAccepted,
		ImagePath:          imagePath,
		FileHash:           fileHash,
		Files:              source.files,
		ExtractedBy:        source.providerID,
		BudgetID:           in.BudgetID,
		BagID:              in.BagID,
		StoreID:            storeID,
		Items:              items,
	}, nil
}

// billFilesFromScan adapts a scan's stored parts for a confirming bill. A scan
// without parts (pre-feature row) falls back to its mirrored single file.
func billFilesFromScan(scan domain.BillScan) []domain.BillFile {
	if len(scan.Files) == 0 {
		if scan.ImagePath == "" {
			return nil
		}
		return []domain.BillFile{{Position: 1, Path: scan.ImagePath, MimeType: scan.MimeType, FileHash: scan.FileHash}}
	}
	out := make([]domain.BillFile, len(scan.Files))
	for i, f := range scan.Files {
		out[i] = domain.BillFile{Position: f.Position, Path: f.Path, MimeType: f.MimeType, FileHash: f.FileHash}
	}
	return out
}

// billFilesFromBill re-reads a saved bill's parts for the update flow, with
// the same pre-feature fallback.
func billFilesFromBill(bill domain.Bill) []domain.BillFile {
	if len(bill.Files) == 0 {
		if bill.ImagePath == "" {
			return nil
		}
		return []domain.BillFile{{Position: 1, Path: bill.ImagePath, FileHash: bill.FileHash}}
	}
	return bill.Files
}

// walletAccountName is the default cash account bills are recorded against
// when the user doesn't pick one — it represents wallet money.
const walletAccountName = "Wallet"

// ensureWalletAccount finds the default wallet account (case-insensitive) or
// lazily creates it as a cash account in the base currency.
func (s *BillService) ensureWalletAccount(ctx context.Context) (int64, error) {
	accounts, err := s.accounts.List(ctx)
	if err != nil {
		return 0, fmt.Errorf("load accounts: %w", err)
	}
	for _, a := range accounts {
		if strings.EqualFold(a.Name, walletAccountName) {
			return a.ID, nil
		}
	}
	base, err := s.providers.BaseCurrency(ctx)
	if err != nil {
		return 0, err
	}
	created, err := s.accounts.Create(ctx, domain.Account{
		Name:     walletAccountName,
		Type:     domain.AccountCash,
		Currency: base,
	})
	if err != nil {
		return 0, fmt.Errorf("create wallet account: %w", err)
	}
	return created.ID, nil
}

// resolveStore links the bill to a store matched case-insensitively on the
// (trimmed) market name, creating the store on first use. The canonical store
// name is returned so the bill's market_name snapshot normalizes casing.
// Empty market names (or a nil store backend in tests) link nothing.
func (s *BillService) resolveStore(ctx context.Context, market string) (*int64, string, error) {
	name := strings.TrimSpace(market)
	if name == "" || s.stores == nil {
		return nil, name, nil
	}
	store, err := s.stores.FindByName(ctx, name)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		store, err = s.stores.Create(ctx, domain.Store{Name: name})
		if errors.Is(err, domain.ErrConflict) {
			// Lost a race against a concurrent confirm — re-read the winner.
			store, err = s.stores.FindByName(ctx, name)
		}
		if err != nil {
			return nil, "", fmt.Errorf("create store %q: %w", name, err)
		}
	case err != nil:
		return nil, "", fmt.Errorf("find store: %w", err)
	}
	id := store.ID
	return &id, store.Name, nil
}

// resolveProduct links a bill item to its catalogue product, matched
// case-insensitively on the (trimmed) item name and created on first use —
// the same find-or-create shape as resolveStore, race-safe through the
// unique name index. Deposit lines are skipped: returns ("Leergut") are money
// back, and deposit charges ("Pfand") are the refund deposit itself — neither
// is a purchasable product (domain.IsDepositArtifact), whatever the AI read
// printed on the line. Failures are non-fatal (logged, link left nil) — a
// product-link problem must not block confirming an otherwise valid receipt;
// the next confirm of the same item name retries.
func (s *BillService) resolveProduct(ctx context.Context, it domain.BillItem) *int64 {
	if s.products == nil || it.IsReturn || domain.IsDepositArtifact(it.Name) ||
		domain.IsDepositArtifact(it.StandardName) {
		return nil
	}
	name := strings.TrimSpace(it.Name)
	if name == "" {
		return nil
	}
	// Mapping-first: a raw text the normalization memory links to a product
	// joins that product's history, even when the memory's standard name no
	// longer matches the product's raw name.
	if p := mappingProduct(ctx, s.mappings, s.products, s.log, name); p != nil {
		learnProductUnitValue(ctx, s.products, s.log, p, it.UnitValue)
		id := p.ID
		return &id
	}
	product, err := s.products.FindByName(ctx, name)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		// New product: seed it with the line's descriptor fields, magnitude
		// included — the line is the only evidence for it right now.
		product, err = s.products.Create(ctx, domain.Product{
			Name:       name,
			Brand:      it.Brand,
			Unit:       it.Unit,
			UnitValue:  it.UnitValue,
			CategoryID: it.CategoryID,
		})
		if errors.Is(err, domain.ErrConflict) {
			// Lost a race against a concurrent confirm — re-read the winner.
			product, err = s.products.FindByName(ctx, name)
		}
		if err != nil {
			s.log.Warn("create product from bill item", "name", name, "error", err)
			return nil
		}
	case err != nil:
		s.log.Warn("find product for bill item", "name", name, "error", err)
		return nil
	default:
		// Existing product: learn the line's size magnitude only when the
		// catalogue knows none yet — a value someone (or an earlier scan)
		// decided on is never overwritten, mirroring how mapping memory
		// preserves reviewed standard names.
		learnProductUnitValue(ctx, s.products, s.log, &product, it.UnitValue)
	}
	linkProductMapping(ctx, s.mappings, s.log, name, product.ID)
	id := product.ID
	return &id
}

// sanitizeUnitValue keeps a real, positive size magnitude and collapses
// anything else (nil, 0, negative, NaN/Inf) to unknown. Lenient on purpose:
// a draft echoing 0 or garbage must never fail a confirm.
func sanitizeUnitValue(v *float64) *float64 {
	if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) || *v <= 0 {
		return nil
	}
	return v
}

// learnProductUnitValue fills an existing product's missing size magnitude
// from a confirmed purchase line (bill or manual); a magnitude the user, an
// earlier scan or a manual entry decided on is never overwritten. Non-fatal:
// a failed write only loses the magnitude; the next purchase retries.
func learnProductUnitValue(ctx context.Context, store ProductStore, log *slog.Logger, p *domain.Product, v *float64) {
	v = sanitizeUnitValue(v)
	if v == nil || p.UnitValue != nil {
		return
	}
	prev := *p
	p.UnitValue = v
	if _, err := store.Update(ctx, *p); err != nil {
		log.Warn("learn product unit value", "product", p.ID, "error", err)
		*p = prev
	}
}

// linkProductMapping points the normalization memory's row for a raw text at
// the product that text resolves to (opportunistic, non-fatal — lookups stay
// name-based; a missing link only removes a display shortcut).
func linkProductMapping(ctx context.Context, mappings ProductMappingStore, log *slog.Logger, raw string, productID int64) {
	if mappings == nil {
		return
	}
	if err := mappings.LinkProduct(ctx, raw, productID); err != nil {
		log.Warn("link product mapping", "raw", raw, "error", err)
	}
}

// mappingProduct returns the catalogue product the normalization memory
// links a raw text to, when that link still resolves — the mapping-first
// rule: the memory's product_id is what earlier purchase lines decided, so a
// printed or typed name resolvable through it joins that product's purchase
// history instead of find-or-creating a near-duplicate by raw name. A stale
// link (merged or deleted product leaves a nil/dangling id) falls through and
// the caller re-points the memory at what it resolves to this time.
func mappingProduct(ctx context.Context, mappings ProductMappingStore, products ProductStore, log *slog.Logger, name string) *domain.Product {
	if mappings == nil || products == nil {
		return nil
	}
	m, err := mappings.FindByRawName(ctx, name)
	if err != nil || m.ProductID == nil {
		return nil
	}
	p, err := products.GetByID(ctx, *m.ProductID)
	if err != nil {
		log.Warn("resolve mapped product", "raw", name, "product", *m.ProductID, "error", err)
		return nil
	}
	return &p
}

// extractDraft resolves the managed extraction prompt (built-in default when
// the row is missing), runs the connector on the receipt parts and resolves
// the AI's category names against the existing categories, creating the
// missing ones.
func (s *BillService) extractDraft(ctx context.Context, files []domain.ReceiptFile, provider domain.AIProvider) (*domain.BillDraft, error) {
	prompt, err := s.resolveExtractionPrompt(ctx)
	if err != nil {
		return nil, err
	}
	draft, err := s.extractor.Extract(ctx, files, provider, prompt)
	if err != nil {
		return nil, fmt.Errorf("extraction failed: %w", err)
	}
	if draft.Currency == "" {
		// The model could not determine the receipt's currency — default to
		// the user's base currency (the editor can override it in review).
		base, err := s.providers.BaseCurrency(ctx)
		if err != nil {
			return nil, err
		}
		draft.Currency = base
	}
	if err := s.resolveDraftCategories(ctx, &draft); err != nil {
		return nil, err
	}
	s.applyMappingMemory(ctx, &draft)
	return &draft, nil
}

// applyMappingMemory normalizes a freshly extracted draft against the
// product-name normalization memory: a raw text that was already decided
// keeps its remembered standard name, generic name and category (memory wins
// over a fresh AI suggestion), and a raw text seen for the first time
// records the AI's suggestion (source 'ai') so the next scan of the same
// text stays stable without re-asking. A remembered empty generic name does
// NOT override the fresh suggestion — "no family known yet" is not a
// decision, and the backfill job fills the gap later. Return/deposit lines
// are skipped — they are not products (return lines and, whatever the AI
// classified, names carrying deposit/gratis wording never enter the memory).
// Failures are logged and non-fatal,
// like resolveProduct: a memory problem must never fail an extraction.
func (s *BillService) applyMappingMemory(ctx context.Context, draft *domain.BillDraft) {
	if s.mappings == nil {
		return
	}
	for i := range draft.Items {
		it := &draft.Items[i]
		raw := strings.TrimSpace(it.Name)
		if raw == "" || it.IsReturn || domain.IsDepositArtifact(raw) {
			continue
		}
		standard := strings.TrimSpace(it.StandardName)
		if standard == "" {
			standard = raw
		}
		it.StandardName = standard
		it.GenericName = strings.TrimSpace(it.GenericName)
		m, err := s.mappings.FindByRawName(ctx, raw)
		switch {
		case errors.Is(err, domain.ErrNotFound):
			if _, cerr := s.mappings.Create(ctx, domain.ProductNameMapping{
				RawName:      raw,
				StandardName: standard,
				GenericName:  it.GenericName,
				CategoryID:   it.CategoryID,
				Source:       domain.MappingSourceAI,
			}); cerr != nil && !errors.Is(cerr, domain.ErrConflict) {
				s.log.Warn("record product mapping", "raw", raw, "error", cerr)
			}
		case err != nil:
			s.log.Warn("lookup product mapping", "raw", raw, "error", err)
		default:
			// Memory wins: the remembered decision is what the user reviewed
			// (or corrected) last time.
			it.StandardName = m.StandardName
			if m.GenericName != "" {
				it.GenericName = m.GenericName
			}
			if m.CategoryID != nil {
				it.CategoryID = m.CategoryID
			}
		}
	}
}

// priorStandardLine is the pre-edit state of one raw text — the standard
// name, generic name and category the review UI showed before the user's
// corrections.
type priorStandardLine struct {
	raw        string
	standard   string
	generic    string
	categoryID *int64
}

// priorStandardFromDraft captures the scan draft's lines (confirm flow).
func priorStandardFromDraft(items []domain.BillItemDraft) []priorStandardLine {
	prior := make([]priorStandardLine, 0, len(items))
	for _, it := range items {
		raw := strings.TrimSpace(it.Name)
		if raw == "" || it.IsReturn {
			continue
		}
		standard := strings.TrimSpace(it.StandardName)
		if standard == "" {
			standard = raw // pre-feature drafts carry no suggestion
		}
		prior = append(prior, priorStandardLine{raw: raw, standard: standard, generic: strings.TrimSpace(it.GenericName), categoryID: it.CategoryID})
	}
	return prior
}

// priorStandardFromItems captures a saved bill's lines (update flow); the
// standard and generic names come from the mapping join, which is exactly
// the state the user saw when editing.
func priorStandardFromItems(items []domain.BillItem) []priorStandardLine {
	prior := make([]priorStandardLine, 0, len(items))
	for _, it := range items {
		raw := strings.TrimSpace(it.Name)
		if raw == "" || it.IsReturn {
			continue
		}
		standard := strings.TrimSpace(it.StandardName)
		if standard == "" {
			standard = raw
		}
		prior = append(prior, priorStandardLine{raw: raw, standard: standard, generic: strings.TrimSpace(it.GenericName), categoryID: it.CategoryID})
	}
	return prior
}

func findPriorStandardLine(prior []priorStandardLine, raw string) (priorStandardLine, bool) {
	for _, p := range prior {
		if strings.EqualFold(p.raw, raw) {
			return p, true
		}
	}
	return priorStandardLine{}, false
}

func sameInt64Ptr(a, b *int64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// learnMappingOverrides records the user's standardized-name, generic-name
// and category corrections from a confirmed/updated bill into the
// normalization memory (source 'user'), so the next scan of the same raw
// text starts from the corrected decision. Lines whose standard name,
// generic name and category are unchanged never write; lines without a
// prior (manually added in the editor) record their submitted values.
// Unlike the standard name, an empty generic name is a real opinion ("no
// broader family") and is learned as-is. Non-fatal — a failed write never
// fails the bill.
func (s *BillService) learnMappingOverrides(ctx context.Context, items []domain.BillItemDraft, prior []priorStandardLine) {
	if s.mappings == nil {
		return
	}
	for _, it := range items {
		raw := strings.TrimSpace(it.Name)
		if raw == "" || it.IsReturn || domain.IsDepositArtifact(raw) {
			continue
		}
		standard := strings.TrimSpace(it.StandardName)
		if standard == "" {
			standard = raw
		}
		generic := strings.TrimSpace(it.GenericName)
		p, found := findPriorStandardLine(prior, raw)
		if found && strings.EqualFold(p.standard, standard) && strings.EqualFold(p.generic, generic) && sameInt64Ptr(p.categoryID, it.CategoryID) {
			continue
		}
		if _, err := s.mappings.Upsert(ctx, domain.ProductNameMapping{
			RawName:      raw,
			StandardName: standard,
			GenericName:  generic,
			CategoryID:   it.CategoryID,
			Source:       domain.MappingSourceUser,
		}); err != nil {
			s.log.Warn("learn product mapping override", "raw", raw, "error", err)
		}
	}
}

// resolveExtractionPrompt returns the bill-extraction prompt content: the
// managed ai_prompts row when the resolver is wired, the built-in default
// otherwise (tests). The {{categories}} placeholder is expanded either way.
func (s *BillService) resolveExtractionPrompt(ctx context.Context) (string, error) {
	if s.prompts != nil {
		prompt, err := s.prompts.ResolvePrompt(ctx, domain.PromptKeyBillExtraction)
		if err != nil {
			return "", fmt.Errorf("resolve extraction prompt: %w", err)
		}
		return prompt, nil
	}
	return renderPrompt(ctx, s.categories, defaultPrompt(domain.PromptKeyBillExtraction))
}

// categoryAliases maps loose AI category names onto the fixed product
// taxonomy. Anything unmatched stays unset — the user picks it in review.
var categoryAliases = map[string]string{
	// fresh produce
	"produce":   "vegetables",
	"vegetable": "vegetables",
	"veggies":   "vegetables",
	"herbs":     "vegetables",
	"fruit":     "fruits",
	// dairy
	"dairy": "dairy & eggs",
	"eggs":  "dairy & eggs",
	"milk":  "dairy & eggs",
	// proteins
	"meat":        "meats",
	"meats":       "meats",
	"poultry":     "meats",
	"fish":        "seafood",
	"deli":        "deli & ready-to-eat",
	"charcuterie": "deli & ready-to-eat",
	// pantry
	"grains":    "pasta, rice & grains",
	"pasta":     "pasta, rice & grains",
	"rice":      "pasta, rice & grains",
	"canned":    "canned & jarred",
	"jarred":    "canned & jarred",
	"spices":    "spices & baking",
	"baking":    "spices & baking",
	"breakfast": "breakfast & spreads",
	"spreads":   "breakfast & spreads",
	"snacks":    "salty snacks",
	"chips":     "salty snacks",
	"sweets":    "sweets & chocolate",
	"candy":     "sweets & chocolate",
	"chocolate": "sweets & chocolate",
	"cookies":   "sweets & chocolate",
	"nuts":      "nuts & dried fruits",
	// drinks (loose drink names stay unmapped — no generic bucket anymore)
	"soda":        "cola & soda",
	"cola":        "cola & soda",
	"soft drinks": "cola & soda",
	"juices":      "juice",
	"water":       "water & iced tea",
	"coffee":      "coffee & tea",
	"tea":         "coffee & tea",
	"alcohol":     "spirits & liqueurs",
	"spirits":     "spirits & liqueurs",
	// household
	"cleaning":  "cleaning & dish",
	"household": "cleaning & dish",
	"paper":     "paper goods",
	// vehicle & fuel (gas-station and car-shop receipts)
	"fuel":       "fuel & gasoline",
	"gasoline":   "fuel & gasoline",
	"petrol":     "fuel & gasoline",
	"diesel":     "fuel & gasoline",
	"adblue":     "fuel & gasoline",
	"motor oil":  "car oils & fluids",
	"engine oil": "car oils & fluids",
	"car oil":    "car oils & fluids",
	"car parts":  "car parts & care",
	"wipers":     "car parts & care",
	"wiper":      "car parts & care",
	"car care":   "car parts & care",
	// pharmacy, beauty & personal care
	"medicine":    "medicines",
	"pharmacy":    "medicines",
	"drugs":       "medicines",
	"vitamins":    "vitamins & supplements",
	"supplements": "vitamins & supplements",
	"first aid":   "first aid",
	"cosmetics":   "cosmetics",
	"makeup":      "cosmetics",
	"skincare":    "cosmetics",
	"shampoo":     "hair & body care",
	"soap":        "hair & body care",
	"deodorant":   "hair & body care",
	"hygiene":     "hair & body care",
	// pets, baby & kids
	"pet food":     "pet supplies",
	"pet supplies": "pet supplies",
	"pets":         "pet supplies",
	"diapers":      "baby care",
	"baby":         "baby care",
	"toys":         "toys & games",
	"games":        "toys & games",
	// home & hardware
	"tools":    "hardware & tools",
	"hardware": "hardware & tools",
	"garden":   "garden & outdoor",
	// electronics, office & clothing
	"electronics": "electronics & accessories",
	"chargers":    "electronics & accessories",
	"cables":      "electronics & accessories",
	"batteries":   "electronics & accessories",
	"office":      "stationery & office",
	"stationery":  "stationery & office",
	"books":       "books & media",
	"clothing":    "clothing & footwear",
	"shoes":       "clothing & footwear",
}

// resolveDraftCategories classifies each item against the fixed storage
// taxonomy seeded in the categories table (case-insensitive, with a small
// alias map). Nothing is auto-created anymore: the taxonomy is fixed.
func (s *BillService) resolveDraftCategories(ctx context.Context, draft *domain.BillDraft) error {
	if len(draft.Items) == 0 {
		return nil
	}
	existing, err := s.categories.List(ctx)
	if err != nil {
		return fmt.Errorf("load categories: %w", err)
	}
	byName := make(map[string]domain.Category, len(existing))
	for _, c := range existing {
		byName[strings.ToLower(c.Name)] = c
	}

	for i, item := range draft.Items {
		name := strings.ToLower(strings.TrimSpace(item.CategoryName))
		if name == "" {
			continue
		}
		if alias, ok := categoryAliases[name]; ok {
			name = alias
		}
		if cat, ok := byName[name]; ok {
			id := cat.ID
			draft.Items[i].CategoryID = &id
		}
		// Unmatched: the category stays unset and the user picks one.
	}
	return nil
}

// --- scan queue / maintenance -------------------------------------------------

// newScanToken returns a fresh 32-char random hex token.
func newScanToken() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate scan token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// fileHash identifies a receipt by its content (sha256 hex). Equal bytes mean
// the same receipt — re-uploading it must not create a second analysis or
// bill. The hash is computed over the upload's ORIGINAL bytes, before
// conversion to JPEG, so dedup never depends on transcode reproducibility;
// every converted part derived from one original (a multi-page scanned PDF)
// carries that original's hash.
func fileHash(file []byte) string {
	sum := sha256.Sum256(file)
	return hex.EncodeToString(sum[:])
}

// enqueue hands a token to the workers without ever blocking: a full queue
// leaves the row in the DB for the next sweep to re-enqueue.
func (s *BillService) enqueue(token string) bool {
	select {
	case s.queue <- token:
		return true
	default:
		return false
	}
}

// recoverScans re-enqueues scans still in the analyzing state — a restart or
// crash mid-extraction leaves them there and they resume on boot.
func (s *BillService) recoverScans(ctx context.Context) {
	pending, err := s.scans.List(ctx, []domain.BillScanStatus{domain.BillScanAnalyzing}, scanQueueCapacity)
	if err != nil {
		s.log.Error("recover analyzing scans", "error", err)
		return
	}
	for _, scan := range pending {
		if !s.enqueue(scan.ScanToken) {
			s.log.Warn("recovery queue full", "token", scan.ScanToken)
		}
	}
	if len(pending) > 0 {
		s.log.Info("recovered analyzing scans", "count", len(pending))
	}
}

// runSweeper periodically runs the stale-scan maintenance so recovery does not
// depend on someone uploading a new receipt: a stranded "analyzing" row (lost
// enqueue, lost worker) is re-enqueued even on a quiet instance.
func (s *BillService) runSweeper(ctx context.Context) {
	defer s.wg.Done()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sweepStaleScans()
		}
	}
}

// sweepStaleScans lazily (at most once a minute) deletes done/failed scans
// past the TTL (with their receipt files) and re-enqueues analyzing scans that
// stopped making progress (e.g. a queue-full enqueue or a lost worker).
func (s *BillService) sweepStaleScans() {
	s.mu.Lock()
	if time.Since(s.lastSweep) <= time.Minute {
		s.mu.Unlock()
		return
	}
	s.lastSweep = time.Now()
	s.mu.Unlock()

	cutoff := time.Now().Add(-scanSessionTTL)
	paths, err := s.scans.DeleteStale(s.ctx, cutoff)
	if err != nil {
		s.log.Error("sweep stale scans", "error", err)
	}
	for _, path := range paths {
		_ = os.Remove(path)
	}

	// A stranded analyzing row gets re-enqueued once it is older than the
	// worst possible runtime of one scan (never while an in-flight extraction
	// — possibly on a widened timeout retry — can still be running).
	staleBefore := time.Now().Add(-worstScanRuntime(s.extractTimeout))
	if staleAnalyzing, err := s.scans.List(s.ctx, []domain.BillScanStatus{domain.BillScanAnalyzing}, scanQueueCapacity); err == nil {
		for _, scan := range staleAnalyzing {
			if scan.UpdatedAt.Before(staleBefore) {
				if !s.enqueue(scan.ScanToken) {
					break // queue still full — next sweep retries
				}
			}
		}
	}
}

// Close stops the scan workers. It cancels the pool and waits at most
// shutdownDrainWindow for workers between iterations — it never waits for a
// running extraction; the scan stays analyzing and is recovered on next boot.
func (s *BillService) Close() {
	s.cancel()
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(shutdownDrainWindow):
	}
}

// --- helpers -----------------------------------------------------------------

// saveReceipt writes the receipt under billsDir with a random, unguessable
// name. The same file later becomes the accepted bill's stored receipt.
func (s *BillService) saveReceipt(ext string, file []byte) (string, error) {
	return writeFileRandom(s.billsDir, ext, file)
}

// assignDraftItemIDs numbers draft lines 1..n so the UI can key rows.
func assignDraftItemIDs(draft *domain.BillDraft, start int64) {
	for i := range draft.Items {
		start++
		draft.Items[i].ID = start
	}
}

// normalizeDigits strips non-digits and keeps at most the last 4 (card
// identifiers like "****4321" become "4321").
func normalizeDigits(raw string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(raw) {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	digits := b.String()
	if len(digits) > 4 {
		digits = digits[len(digits)-4:]
	}
	return digits
}

func nonEmptyOr(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}
