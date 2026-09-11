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
	"sort"
	"strings"
	"sync"
	"time"

	"home-finance-planner/backend/internal/domain"
)

// BillExtractor abstracts the AI extraction engine (implemented by
// internal/extractor).
type BillExtractor interface {
	Extract(ctx context.Context, image []byte, mimeType string, provider domain.AIProvider) (domain.BillDraft, error)
}

// BillStore is the persistence contract for bills. Only accepted bills are
// persisted — scan drafts live in bill_scans until confirmed.
type BillStore interface {
	Create(ctx context.Context, b domain.Bill) (domain.Bill, error)
	Update(ctx context.Context, b domain.Bill) (domain.Bill, error)
	SetTransaction(ctx context.Context, billID, txID int64) error
	GetByID(ctx context.Context, id int64) (domain.Bill, error)
	GetByFileHash(ctx context.Context, hash string) (domain.Bill, error)
	List(ctx context.Context, f domain.BillFilters) ([]domain.Bill, error)
	Stats(ctx context.Context, groupBy, month string) ([]domain.BillStatsRow, error)
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
	ClaimRetry(ctx context.Context, token, providerID string) (bool, error)
	Delete(ctx context.Context, token string) (domain.BillScan, error)
	DeleteStale(ctx context.Context, olderThan time.Time) ([]string, error)
}

// BillFilters re-exports the shared domain filter type.
type BillFilters = domain.BillFilters

// MaxBillImageBytes caps uploaded receipt files at 10 MB.
const MaxBillImageBytes = 10 << 20

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

// billScanSource points at the receipt a bill is (or was) built from: the file
// on disk under billsDir, its content hash (duplicate detection) and the
// provider id that produced the draft. Used by both Confirm (from the scan
// row) and Update (from the saved bill).
type billScanSource struct {
	imagePath  string
	providerID string
	fileHash   string
}

// BillService orchestrates the scan-bills workflow.
type BillService struct {
	bills          BillStore
	scans          BillScanStore
	extractor      BillExtractor
	providers      *SettingsService
	accounts       AccountStore
	categories     CategoryStore
	stores         StoreStore
	budgets        BudgetStore
	txStore        TransactionStore
	rates          RateSource
	billsDir       string
	extractTimeout time.Duration
	log            *slog.Logger

	queue     chan string
	wg        sync.WaitGroup
	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex // guards lastSweep only
	lastSweep time.Time
}

// NewBillService wires the bill workflow and starts the background scan
// workers. billsDir is where receipt files are stored; extractTimeout bounds
// one AI extraction; scans still analyzing after a restart are re-enqueued.
func NewBillService(
	bills BillStore,
	scans BillScanStore,
	extractor BillExtractor,
	providers *SettingsService,
	accounts AccountStore,
	categories CategoryStore,
	stores StoreStore,
	budgets BudgetStore,
	txStore TransactionStore,
	rates RateSource,
	billsDir string,
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
		accounts:       accounts,
		categories:     categories,
		stores:         stores,
		budgets:        budgets,
		txStore:        txStore,
		rates:          rates,
		billsDir:       billsDir,
		extractTimeout: extractTimeout,
		log:            log,
		queue:          make(chan string, scanQueueCapacity),
		ctx:            ctx,
		cancel:         cancel,
	}
	s.recoverScans(ctx)
	for i := 1; i <= billScanWorkers; i++ {
		s.wg.Add(1)
		go s.runWorker(ctx, i)
	}
	return s
}

// Scan stores the receipt file (not yet a bill), registers a scan row, and
// returns immediately — extraction runs in the background. The client polls
// GetScan until the status leaves "analyzing". Nothing is persisted as a bill
// until Confirm. providerID is optional; the first configured provider is used
// otherwise.
func (s *BillService) Scan(ctx context.Context, mimeType string, file []byte, providerID string) (domain.BillScan, error) {
	mimeType = strings.ToLower(mimeType)
	ext, ok := allowedMime[mimeType]
	if !ok {
		// Browsers outside Safari often upload .heic/.pdf with a generic
		// content type — sniff the magic bytes before rejecting the upload.
		if sniffed := sniffMime(file); sniffed != "" {
			mimeType = sniffed
			ext, ok = allowedMime[sniffed], true
		}
	}
	if !ok {
		return domain.BillScan{}, validationError("unsupported file type %q (want jpeg, png, webp, heic, heif or pdf)", mimeType)
	}
	if len(file) == 0 {
		return domain.BillScan{}, validationError("receipt file is empty")
	}
	if len(file) > MaxBillImageBytes {
		return domain.BillScan{}, validationError("receipt file exceeds %d MB limit", MaxBillImageBytes>>20)
	}

	// Duplicate protection: the same receipt must not be processed twice.
	// The content hash matches an active scan (still analyzing / awaiting
	// review) or an already-saved bill.
	hash := fileHash(file)
	if _, err := s.scans.GetByFileHash(ctx, hash); err == nil {
		return domain.BillScan{}, conflictError("duplicate receipt: this image is already being analyzed — check the bills view")
	} else if !errors.Is(err, domain.ErrNotFound) {
		return domain.BillScan{}, fmt.Errorf("check scan duplicate: %w", err)
	}
	if existing, err := s.bills.GetByFileHash(ctx, hash); err == nil {
		return domain.BillScan{}, conflictError("duplicate receipt: this image was already saved as bill #%d", existing.ID)
	} else if !errors.Is(err, domain.ErrNotFound) {
		return domain.BillScan{}, fmt.Errorf("check bill duplicate: %w", err)
	}

	provider, err := s.resolveProvider(ctx, providerID)
	if err != nil {
		return domain.BillScan{}, err
	}

	path, err := s.saveReceipt(ext, file)
	if err != nil {
		return domain.BillScan{}, fmt.Errorf("save receipt: %w", err)
	}

	token, err := newScanToken()
	if err != nil {
		_ = os.Remove(path)
		return domain.BillScan{}, err
	}

	if _, err := s.scans.Create(ctx, domain.BillScan{
		ScanToken:  token,
		ImagePath:  path,
		MimeType:   strings.ToLower(mimeType),
		ProviderID: provider.ID,
		FileHash:   hash,
	}); err != nil {
		_ = os.Remove(path)
		return domain.BillScan{}, err
	}

	if !s.enqueue(token) {
		// Queue full: be honest instead of losing the upload — drop the row
		// and the file and tell the client to retry.
		if _, derr := s.scans.Delete(context.Background(), token); derr != nil {
			s.log.Warn("drop scan row after queue-full", "token", token, "error", derr)
		}
		_ = os.Remove(path)
		return domain.BillScan{}, validationError("analysis queue is full — try again in a moment")
	}
	s.sweepStaleScans()
	return domain.BillScan{
		ScanToken:  token,
		Status:     domain.BillScanAnalyzing,
		ProviderID: provider.ID,
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

	s.enqueue(token)
	return domain.BillScan{
		ScanToken:  token,
		Status:     domain.BillScanAnalyzing,
		ProviderID: provider.ID,
	}, nil
}

// Confirm persists the (user-corrected) draft as an accepted bill and, with
// an AccountID, records one expense transaction for the printed total. The
// scan row is deleted; the receipt file stays as the permanent record.
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

	bill, err := s.buildBill(ctx, in, &billScanSource{imagePath: scan.ImagePath, providerID: scan.ProviderID, fileHash: scan.FileHash})
	if err != nil {
		return domain.Bill{}, err
	}

	created, err := s.bills.Create(ctx, bill)
	if err != nil {
		return domain.Bill{}, err
	}

	if in.AccountID != nil && *in.AccountID > 0 {
		if _, err := s.accounts.GetByID(ctx, *in.AccountID); err != nil {
			return domain.Bill{}, fmt.Errorf("validate account_id: %w", err)
		}
		description := billDescription(bill)
		txRow, err := s.txStore.Create(ctx, domain.Transaction{
			AccountID:   *in.AccountID,
			Kind:        domain.TransactionExpense,
			AmountCents: bill.TotalCents,
			Currency:    bill.Currency,
			Description: description,
			Date:        nonEmptyOr(bill.Date, time.Now().Format("2006-01-02")),
		})
		if err != nil {
			return domain.Bill{}, fmt.Errorf("record bill transaction: %w", err)
		}
		// Link the transaction so later bill edits keep it in sync.
		if err := s.bills.SetTransaction(ctx, created.ID, txRow.ID); err != nil {
			return domain.Bill{}, err
		}
	}

	// The bill exists — consume the scan row. (Deleting after Create keeps a
	// crash between the two a redoable confirm.)
	if _, err := s.scans.Delete(ctx, token); err != nil {
		s.log.Warn("consume confirmed scan row", "token", token, "error", err)
	}
	return created, nil
}

// Update applies user corrections to an already-saved bill. The total is
// recomputed from the edited lines exactly as at confirm time, and the linked
// expense transaction (if any) is kept in sync.
func (s *BillService) Update(ctx context.Context, id int64, in domain.BillConfirmInput) (domain.Bill, error) {
	existing, err := s.bills.GetByID(ctx, id)
	if err != nil {
		return domain.Bill{}, err
	}

	source := &billScanSource{imagePath: existing.ImagePath, providerID: existing.ExtractedBy, fileHash: existing.FileHash}
	bill, err := s.buildBill(ctx, in, source)
	if err != nil {
		return domain.Bill{}, err
	}
	bill.ID = id
	bill.CreatedAt = existing.CreatedAt
	bill.TransactionID = existing.TransactionID

	updated, err := s.bills.Update(ctx, bill)
	if err != nil {
		return domain.Bill{}, err
	}

	if existing.TransactionID != nil {
		if err := s.syncBillTransaction(ctx, *existing.TransactionID, bill); err != nil {
			return domain.Bill{}, err
		}
	}
	return updated, nil
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
// time so reports reflect the edited bill.
func (s *BillService) syncBillTransaction(ctx context.Context, txID int64, bill domain.Bill) error {
	tx, err := s.txStore.GetByID(ctx, txID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil // transaction was deleted — nothing to sync
	}
	if err != nil {
		return fmt.Errorf("load bill transaction: %w", err)
	}
	tx.AmountCents = bill.TotalCents
	tx.Currency = bill.Currency
	tx.Description = billDescription(bill)
	if bill.Date != "" {
		tx.Date = bill.Date
	}
	if _, err := s.txStore.Update(ctx, tx); err != nil {
		return fmt.Errorf("sync bill transaction: %w", err)
	}
	return nil
}

// DiscardScan drops an unconfirmed scan and deletes its receipt file. A
// worker that is still extracting it matches 0 rows when writing its result,
// so a discarded scan is never resurrected.
func (s *BillService) DiscardScan(ctx context.Context, token string) error {
	scan, err := s.scans.Delete(ctx, token)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("scan %s not found or expired: %w", token, domain.ErrNotFound)
		}
		return err
	}
	if err := os.Remove(scan.ImagePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete receipt file: %w", err)
	}
	return nil
}

func (s *BillService) Get(ctx context.Context, id int64) (domain.Bill, error) {
	return s.bills.GetByID(ctx, id)
}

func (s *BillService) List(ctx context.Context, f domain.BillFilters) ([]domain.Bill, error) {
	if f.Month != "" {
		if err := validateMonth(f.Month, "month"); err != nil {
			return nil, err
		}
	}
	return s.bills.List(ctx, f)
}

// BillStats is the bill-analysis envelope: per-label totals merged across
// currencies and converted into the user's base Currency.
type BillStats struct {
	Currency           string                `json:"currency"`
	ConversionWarnings []string              `json:"conversion_warnings"`
	Rows               []domain.BillStatsRow `json:"rows"`
}

// Stats aggregates accepted bills by market, month, week, item, or category,
// merging per-currency rows into the base currency.
func (s *BillService) Stats(ctx context.Context, groupBy, month string) (BillStats, error) {
	switch groupBy {
	case "market", "month", "week", "item", "category":
	default:
		return BillStats{}, validationError("group_by must be market, month, week, item or category")
	}
	if month != "" {
		if err := validateMonth(month, "month"); err != nil {
			return BillStats{}, err
		}
	}
	base, err := s.providers.BaseCurrency(ctx)
	if err != nil {
		return BillStats{}, err
	}
	snap, err := s.rates.Snapshot(ctx)
	if err != nil {
		return BillStats{}, err
	}
	rows, err := s.bills.Stats(ctx, groupBy, month)
	if err != nil {
		return BillStats{}, err
	}

	conv := newConverter(base, snap)
	merged := map[string]*domain.BillStatsRow{}
	for _, row := range rows {
		m, ok := merged[row.Label]
		if !ok {
			m = &domain.BillStatsRow{Label: row.Label}
			merged[row.Label] = m
		}
		m.BillCount += row.BillCount
		m.Quantity += row.Quantity
		m.TotalCents += conv.add(row.Currency, row.TotalCents)
	}
	out := make([]domain.BillStatsRow, 0, len(merged))
	for _, m := range merged {
		out = append(out, *m)
	}
	// Time groupings stay chronological (recent first); the rest rank by spend.
	if groupBy == "month" || groupBy == "week" {
		sort.Slice(out, func(i, j int) bool { return out[i].Label > out[j].Label })
	} else {
		sort.Slice(out, func(i, j int) bool { return out[i].TotalCents > out[j].TotalCents })
		if len(out) > 50 {
			out = out[:50]
		}
	}
	return BillStats{
		Currency:           base,
		ConversionWarnings: conv.warnings(),
		Rows:               out,
	}, nil
}

// Brands lists the distinct brands already recorded on bill items, for the
// review dropdown.
func (s *BillService) Brands(ctx context.Context) ([]string, error) {
	return s.bills.ListBrands(ctx)
}

// ReceiptImagePath resolves the stored receipt for serving.
func (s *BillService) ReceiptImagePath(ctx context.Context, billID int64) (string, string, error) {
	bill, err := s.bills.GetByID(ctx, billID)
	if err != nil {
		return "", "", err
	}
	if bill.ImagePath == "" {
		return "", "", validationError("bill %d has no stored receipt", billID)
	}
	return bill.ImagePath, detectMime(bill.ImagePath), nil
}

// resolveProvider picks the requested provider or falls back to the first.
func (s *BillService) resolveProvider(ctx context.Context, providerID string) (domain.AIProvider, error) {
	if strings.TrimSpace(providerID) != "" {
		return s.providers.GetProvider(ctx, providerID)
	}
	return s.providers.FirstProvider(ctx)
}

// buildBill validates the confirmed draft and turns it into a Bill ready for
// persistence. The total is always computed from the edited lines + VAT; the
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
	// may override it per line (nil = inherit).
	if in.BudgetID != nil {
		if _, err := s.budgets.GetByID(ctx, *in.BudgetID); err != nil {
			return domain.Bill{}, fmt.Errorf("validate budget_id: %w", err)
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
		isReturn := isDepositReturn(name)
		allowsNegative := isReturn
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
			if _, err := s.budgets.GetByID(ctx, *it.BudgetID); err != nil {
				return domain.Bill{}, fmt.Errorf("validate budget for %q: %w", name, err)
			}
		}
		line := it.Quantity*float64(it.UnitPriceCents) - float64(it.DiscountCents)
		lineCents := int64(math.Round(line))
		if lineCents < 0 && !allowsNegative {
			lineCents = 0
		}
		itemsSubtotal += lineCents
		items = append(items, domain.BillItem{
			Name:           name,
			Brand:          strings.TrimSpace(it.Brand),
			Unit:           strings.ToLower(strings.TrimSpace(it.Unit)),
			CategoryID:     it.CategoryID,
			Quantity:       it.Quantity,
			UnitPriceCents: it.UnitPriceCents,
			DiscountCents:  it.DiscountCents,
			LineTotalCents: lineCents,
			IsReturn:       isReturn,
		})
	}

	// The total is never taken from the client: it is always recomputed as the
	// sum of the lines (VAT is already included in each item's price, so VAT
	// must not be added again). Card digits imply card payment.
	total := itemsSubtotal
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
		ImagePath:          source.imagePath,
		FileHash:           source.fileHash,
		ExtractedBy:        source.providerID,
		BudgetID:           in.BudgetID,
		StoreID:            storeID,
		Items:              items,
	}, nil
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

// extractDraft runs the connector and resolves the AI's category names
// against the existing categories, creating the missing ones.
func (s *BillService) extractDraft(ctx context.Context, file []byte, mimeType string, provider domain.AIProvider) (*domain.BillDraft, error) {
	draft, err := s.extractor.Extract(ctx, file, mimeType, provider)
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
	return &draft, nil
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
// the same receipt — re-uploading it must not create a second analysis or bill.
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

	// A stranded analyzing row gets re-enqueued once it is older than two
	// extraction timeouts (never while an in-flight extraction can still be
	// running).
	staleBefore := time.Now().Add(-2 * s.extractTimeout)
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

// isDepositReturn reports whether an article line is a bottle/crate deposit
// return (e.g. German "Leergut"). Returns are money back: negative prices are
// allowed and they reduce the bill total. Mirrored in the frontend editor.
func isDepositReturn(name string) bool {
	return strings.Contains(strings.ToLower(name), "leergut")
}

func nonEmptyOr(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}
