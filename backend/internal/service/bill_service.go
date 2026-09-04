package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
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
// persisted — scans live in memory until confirmed.
type BillStore interface {
	Create(ctx context.Context, b domain.Bill) (domain.Bill, error)
	Update(ctx context.Context, b domain.Bill) (domain.Bill, error)
	SetTransaction(ctx context.Context, billID, txID int64) error
	GetByID(ctx context.Context, id int64) (domain.Bill, error)
	List(ctx context.Context, f domain.BillFilters) ([]domain.Bill, error)
	Stats(ctx context.Context, groupBy, month string) ([]domain.BillStatsRow, error)
	ListBrands(ctx context.Context) ([]string, error)
}

// BillFilters re-exports the shared domain filter type.
type BillFilters = domain.BillFilters

// MaxBillImageBytes caps uploaded receipt files at 10 MB.
const MaxBillImageBytes = 10 << 20

// scanSessionTTL bounds how long an unconfirmed scan (and its receipt file)
// is kept in memory/disk.
const scanSessionTTL = 2 * time.Hour

// billScanSession holds the not-yet-persisted receipt: the file stays on disk
// under billsDir and is promoted to the accepted bill on confirm.
type billScanSession struct {
	imagePath  string
	providerID string
	createdAt  time.Time
}

// BillService orchestrates the scan-bills workflow.
type BillService struct {
	bills      BillStore
	extractor  BillExtractor
	providers  *SettingsService
	accounts   AccountStore
	categories CategoryStore
	budgets    BudgetStore
	txStore    TransactionStore
	billsDir   string

	mu        sync.Mutex
	scans     map[string]*billScanSession
	lastSweep time.Time
}

// NewBillService wires the bill workflow. billsDir is where receipt files are
// stored.
func NewBillService(
	bills BillStore,
	extractor BillExtractor,
	providers *SettingsService,
	accounts AccountStore,
	categories CategoryStore,
	budgets BudgetStore,
	txStore TransactionStore,
	billsDir string,
) *BillService {
	return &BillService{
		bills:      bills,
		extractor:  extractor,
		providers:  providers,
		accounts:   accounts,
		categories: categories,
		budgets:    budgets,
		txStore:    txStore,
		billsDir:   billsDir,
		scans:      make(map[string]*billScanSession),
	}
}

// allowedMime maps accepted upload types to file extensions.
var allowedMime = map[string]string{
	"image/jpeg":      ".jpg",
	"image/png":       ".png",
	"image/webp":      ".webp",
	"image/heic":      ".heic",
	"image/heif":      ".heif",
	"application/pdf": ".pdf",
}

// Scan stores the receipt file (not yet a bill), runs AI extraction, and
// returns the draft together with a scan token. Nothing is persisted until
// Confirm. providerID is optional; the first configured provider is used
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

	provider, err := s.resolveProvider(ctx, providerID)
	if err != nil {
		return domain.BillScan{}, err
	}

	path, err := s.saveReceipt(ext, file)
	if err != nil {
		return domain.BillScan{}, fmt.Errorf("save receipt: %w", err)
	}

	draft, err := s.extractDraft(ctx, file, strings.ToLower(mimeType), provider)
	if err != nil {
		_ = os.Remove(path) // nothing to keep — the read failed
		return domain.BillScan{}, err
	}

	token, err := s.storeScanSession(path, provider.ID)
	if err != nil {
		_ = os.Remove(path)
		return domain.BillScan{}, err
	}

	assignDraftItemIDs(draft, 0)
	return domain.BillScan{ScanToken: token, ProviderID: provider.ID, Draft: draft}, nil
}

// Reextract re-runs AI extraction on an unconfirmed scan (retry), optionally
// with a different provider. The draft is replaced; any edits are lost.
func (s *BillService) Reextract(ctx context.Context, token, providerID string) (domain.BillScan, error) {
	session, ok := s.scanSession(token)
	if !ok {
		return domain.BillScan{}, validationError("scan %s not found or expired — scan the receipt again", token)
	}

	provider, err := s.resolveProvider(ctx, providerID)
	if err != nil {
		return domain.BillScan{}, err
	}

	file, err := os.ReadFile(session.imagePath)
	if err != nil {
		return domain.BillScan{}, fmt.Errorf("read receipt: %w", err)
	}

	draft, err := s.extractDraft(ctx, file, detectMime(session.imagePath), provider)
	if err != nil {
		return domain.BillScan{}, err
	}

	session.providerID = provider.ID
	assignDraftItemIDs(draft, 0)
	return domain.BillScan{ScanToken: token, ProviderID: provider.ID, Draft: draft}, nil
}

// Confirm persists the (user-corrected) draft as an accepted bill and, with
// an AccountID, records one expense transaction for the printed total. The
// scan session is consumed; the receipt file stays as the permanent record.
func (s *BillService) Confirm(ctx context.Context, token string, in domain.BillConfirmInput) (domain.Bill, error) {
	session, ok := s.scanSession(token)
	if !ok {
		return domain.Bill{}, validationError("scan %s not found or expired — scan the receipt again", token)
	}

	bill, err := s.buildBill(ctx, in, session)
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

	s.removeScanSession(token)
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

	session := &billScanSession{imagePath: existing.ImagePath, providerID: existing.ExtractedBy}
	bill, err := s.buildBill(ctx, in, session)
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
	tx.Description = billDescription(bill)
	if bill.Date != "" {
		tx.Date = bill.Date
	}
	if _, err := s.txStore.Update(ctx, tx); err != nil {
		return fmt.Errorf("sync bill transaction: %w", err)
	}
	return nil
}

// DiscardScan drops an unconfirmed scan and deletes its receipt file.
func (s *BillService) DiscardScan(ctx context.Context, token string) error {
	session, ok := s.removeScanSession(token)
	if !ok {
		return validationError("scan %s not found or expired", token)
	}
	if err := os.Remove(session.imagePath); err != nil && !os.IsNotExist(err) {
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

// Stats aggregates accepted bills by market, month, week, item, or category.
func (s *BillService) Stats(ctx context.Context, groupBy, month string) ([]domain.BillStatsRow, error) {
	switch groupBy {
	case "market", "month", "week", "item", "category":
	default:
		return nil, validationError("group_by must be market, month, week, item or category")
	}
	if month != "" {
		if err := validateMonth(month, "month"); err != nil {
			return nil, err
		}
	}
	return s.bills.Stats(ctx, groupBy, month)
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
func (s *BillService) buildBill(ctx context.Context, in domain.BillConfirmInput, session *billScanSession) (domain.Bill, error) {
	if in.VATCents < 0 || in.DiscountCents < 0 || in.PrintedTotalCents < 0 {
		return domain.Bill{}, validationError("totals must not be negative")
	}
	date := strings.TrimSpace(in.Date)
	if date != "" {
		if _, err := time.Parse("2006-01-02", date); err != nil {
			return domain.Bill{}, validationError("date must be YYYY-MM-DD")
		}
	}
	currency := strings.TrimSpace(strings.ToUpper(in.Currency))
	if currency == "" {
		currency = "USD"
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
		// line total may be negative, and they reduce the bill total.
		isReturn := isDepositReturn(name)
		if it.Quantity <= 0 {
			return domain.Bill{}, validationError("quantity for %q must be positive", name)
		}
		if it.DiscountCents < 0 || (!isReturn && it.UnitPriceCents < 0) {
			return domain.Bill{}, validationError("prices for %q must not be negative", name)
		}
		if it.CategoryID != nil {
			if _, err := s.categories.GetByID(ctx, *it.CategoryID); err != nil {
				return domain.Bill{}, fmt.Errorf("validate category for %q: %w", name, err)
			}
		}
		if it.BudgetID != nil && (in.BudgetID == nil || *it.BudgetID != *in.BudgetID) {
			if _, err := s.budgets.GetByID(ctx, *it.BudgetID); err != nil {
				return domain.Bill{}, fmt.Errorf("validate budget for %q: %w", name, err)
			}
		}
		line := it.Quantity*float64(it.UnitPriceCents) - float64(it.DiscountCents)
		lineCents := int64(math.Round(line))
		if lineCents < 0 && !isReturn {
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

	return domain.Bill{
		MarketName:         strings.TrimSpace(in.MarketName),
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
		ImagePath:          session.imagePath,
		ExtractedBy:        session.providerID,
		BudgetID:           in.BudgetID,
		Items:              items,
	}, nil
}

// extractDraft runs the connector and resolves the AI's category names
// against the existing categories, creating the missing ones.
func (s *BillService) extractDraft(ctx context.Context, file []byte, mimeType string, provider domain.AIProvider) (*domain.BillDraft, error) {
	draft, err := s.extractor.Extract(ctx, file, mimeType, provider)
	if err != nil {
		return nil, fmt.Errorf("extraction failed: %w", err)
	}
	if draft.Currency == "" {
		draft.Currency = "USD"
	}
	if err := s.resolveDraftCategories(ctx, &draft); err != nil {
		return nil, err
	}
	return &draft, nil
}

// categoryAliases maps loose AI category names onto the fixed storage
// taxonomy. Anything unmatched stays unset — the user picks it in review.
var categoryAliases = map[string]string{
	"dairy":      "dairy & eggs",
	"produce":    "produce",
	"vegetables": "produce",
	"vegetable":  "produce",
	"fruits":     "produce",
	"fruit":      "produce",
	"herbs":      "produce",
	"meat":       "meats & seafood",
	"meats":      "meats & seafood",
	"seafood":    "meats & seafood",
	"fish":       "meats & seafood",
	"beverages":  "beverages",
	"drinks":     "beverages",
	"snacks":     "snacks & treats",
	"sweets":     "snacks & treats",
	"cleaning":   "cleaning & dish",
	"household":  "cleaning & dish",
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

// --- scan session store ------------------------------------------------------

// storeScanSession registers a receipt file under a fresh random token and
// sweeps expired sessions.
func (s *BillService) storeScanSession(path, providerID string) (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate scan token: %w", err)
	}
	token := hex.EncodeToString(buf)

	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if now.Sub(s.lastSweep) > time.Minute {
		for t, sess := range s.scans {
			if now.Sub(sess.createdAt) > scanSessionTTL {
				_ = os.Remove(sess.imagePath)
				delete(s.scans, t)
			}
		}
		s.lastSweep = now
	}
	s.scans[token] = &billScanSession{imagePath: path, providerID: providerID, createdAt: now}
	return token, nil
}

func (s *BillService) scanSession(token string) (*billScanSession, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.scans[token]
	if !ok || time.Since(sess.createdAt) > scanSessionTTL {
		return nil, false
	}
	return sess, true
}

// removeScanSession deletes a session and returns the removed one (nil when
// the token was unknown or already consumed). The receipt file itself is left
// in place: confirm keeps it as the bill's record.
func (s *BillService) removeScanSession(token string) (*billScanSession, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.scans[token]
	if ok {
		delete(s.scans, token)
	}
	return sess, ok
}

// --- helpers -----------------------------------------------------------------

// saveReceipt writes the receipt under billsDir with a random, unguessable
// name. The same file later becomes the accepted bill's stored receipt.
func (s *BillService) saveReceipt(ext string, file []byte) (string, error) {
	if err := os.MkdirAll(s.billsDir, 0o755); err != nil {
		return "", fmt.Errorf("create bills dir: %w", err)
	}
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate receipt name: %w", err)
	}
	name := fmt.Sprintf("%s-%s%s", time.Now().Format("20060102"), hex.EncodeToString(buf), ext)
	path := filepath.Join(s.billsDir, name)
	if err := os.WriteFile(path, file, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// detectMime infers the file mime from the stored extension.
func detectMime(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".heic":
		return "image/heic"
	case ".heif":
		return "image/heif"
	case ".pdf":
		return "application/pdf"
	default:
		return "image/jpeg"
	}
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

// sniffMime detects HEIC/HEIF photos and PDF documents from their magic
// bytes, for uploads that arrive with a generic content type.
func sniffMime(file []byte) string {
	if len(file) >= 12 && string(file[4:8]) == "ftyp" {
		brand := string(file[8:12])
		switch {
		case strings.HasPrefix(brand, "heic"), strings.HasPrefix(brand, "heix"),
			strings.HasPrefix(brand, "hevc"), strings.HasPrefix(brand, "hevx"):
			return "image/heic"
		case strings.HasPrefix(brand, "mif1"), strings.HasPrefix(brand, "msf1"):
			return "image/heif"
		}
	}
	if len(file) >= 5 && string(file[:4]) == "%PDF" {
		return "application/pdf"
	}
	return ""
}

func nonEmptyOr(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}
