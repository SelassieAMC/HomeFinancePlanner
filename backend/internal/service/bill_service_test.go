package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"home-finance-planner/backend/internal/domain"
)

// --- fakes -------------------------------------------------------------------

// fakeBillScanStore is an in-memory BillScanStore mirroring the SQLite
// repository's guard semantics (result writes only from 'analyzing', retries
// only from 'done'/'failed').
type fakeBillScanStore struct {
	mu    sync.Mutex
	items map[string]domain.BillScan
}

func newFakeBillScanStore() *fakeBillScanStore {
	return &fakeBillScanStore{items: map[string]domain.BillScan{}}
}

func (f *fakeBillScanStore) Create(_ context.Context, s domain.BillScan) (domain.BillScan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.items[s.ScanToken]; ok {
		return domain.BillScan{}, errors.New("duplicate token")
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now().UTC()
	}
	s.Status = domain.BillScanAnalyzing
	s.UpdatedAt = time.Now().UTC()
	if len(s.Files) > 0 {
		s.FileCount = len(s.Files) // the SQLite repo computes the same count
	}
	f.items[s.ScanToken] = s
	return s, nil
}

func (f *fakeBillScanStore) GetByToken(_ context.Context, token string) (domain.BillScan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.items[token]
	if !ok {
		return domain.BillScan{}, domain.ErrNotFound
	}
	return s, nil
}

func (f *fakeBillScanStore) GetByFileHash(_ context.Context, hash string) (domain.BillScan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.items {
		// Failed/cancelled scans don't match, mirroring the repository's
		// dedup query — those receipts are re-uploadable.
		if s.Status == domain.BillScanFailed || s.Status == domain.BillScanCancelled {
			continue
		}
		if s.FileHash == hash {
			return s, nil
		}
		// Every stored part counts, mirroring the child-table dedup.
		for _, part := range s.Files {
			if part.FileHash == hash {
				return s, nil
			}
		}
	}
	return domain.BillScan{}, domain.ErrNotFound
}

func (f *fakeBillScanStore) List(_ context.Context, statuses []domain.BillScanStatus, limit int) ([]domain.BillScan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []domain.BillScan{}
	for _, s := range f.items {
		if len(statuses) == 0 {
			out = append(out, s)
			continue
		}
		for _, st := range statuses {
			if s.Status == st {
				out = append(out, s)
				break
			}
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeBillScanStore) MarkDone(_ context.Context, token string, draft *domain.BillDraft) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.items[token]
	if !ok || s.Status != domain.BillScanAnalyzing {
		return fmt.Errorf("mark done: %w", domain.ErrNotFound)
	}
	s.Status = domain.BillScanDone
	s.Draft = draft
	s.Error = ""
	s.UpdatedAt = time.Now().UTC()
	f.items[token] = s
	return nil
}

func (f *fakeBillScanStore) MarkFailed(_ context.Context, token, msg string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.items[token]
	if !ok || s.Status != domain.BillScanAnalyzing {
		return fmt.Errorf("mark failed: %w", domain.ErrNotFound)
	}
	s.Status = domain.BillScanFailed
	s.Draft = nil
	s.Error = msg
	s.UpdatedAt = time.Now().UTC()
	f.items[token] = s
	return nil
}

func (f *fakeBillScanStore) MarkCancelled(_ context.Context, token string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.items[token]
	if !ok || s.Status != domain.BillScanAnalyzing {
		return false, nil
	}
	s.Status = domain.BillScanCancelled
	s.Draft = nil
	s.Error = ""
	s.UpdatedAt = time.Now().UTC()
	f.items[token] = s
	return true, nil
}

func (f *fakeBillScanStore) ClaimRetry(_ context.Context, token, providerID string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.items[token]
	if !ok || (s.Status != domain.BillScanDone && s.Status != domain.BillScanFailed && s.Status != domain.BillScanCancelled) {
		return false, nil
	}
	s.Status = domain.BillScanAnalyzing
	s.Draft = nil
	s.Error = ""
	s.ProviderID = providerID
	s.UpdatedAt = time.Now().UTC()
	f.items[token] = s
	return true, nil
}

func (f *fakeBillScanStore) Delete(_ context.Context, token string) (domain.BillScan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.items[token]
	if !ok {
		return domain.BillScan{}, domain.ErrNotFound
	}
	delete(f.items, token)
	return s, nil
}

func (f *fakeBillScanStore) DeleteStale(_ context.Context, olderThan time.Time) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var paths []string
	for token, s := range f.items {
		if (s.Status == domain.BillScanDone || s.Status == domain.BillScanFailed || s.Status == domain.BillScanCancelled) &&
			s.UpdatedAt.Before(olderThan) {
			if len(s.Files) > 0 {
				for _, part := range s.Files {
					paths = append(paths, part.Path)
				}
			} else if s.ImagePath != "" {
				paths = append(paths, s.ImagePath)
			}
			delete(f.items, token)
		}
	}
	return paths, nil
}

// fakeBillStore is a BillStore stub; Create is exercised by Confirm, Stats
// returns whatever rows the test stages.
type fakeBillStore struct {
	mu    sync.Mutex
	items []domain.Bill
	stats []domain.BillStatsRow
}

func (f *fakeBillStore) Create(_ context.Context, b domain.Bill) (domain.Bill, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b.ID = int64(len(f.items) + 1)
	if len(b.Files) > 0 {
		b.FileCount = len(b.Files) // the SQLite repo computes the same count
	}
	items := b.Items
	b.Items = nil
	f.items = append(f.items, b)
	b.Items = items // stored without lines; the caller's copy keeps them
	return b, nil
}

func (f *fakeBillStore) Update(_ context.Context, b domain.Bill) (domain.Bill, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.items {
		if f.items[i].ID == b.ID {
			items := b.Items
			b.Items = nil
			f.items[i] = b
			b.Items = items
			return b, nil
		}
	}
	return domain.Bill{}, domain.ErrNotFound
}
func (f *fakeBillStore) SetTransaction(_ context.Context, billID, txID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.items {
		if f.items[i].ID == billID {
			id := txID
			f.items[i].TransactionID = &id
			return nil
		}
	}
	return domain.ErrNotFound
}
func (f *fakeBillStore) Delete(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.items {
		if f.items[i].ID == id {
			f.items = append(f.items[:i], f.items[i+1:]...)
			return nil
		}
	}
	return domain.ErrNotFound
}
func (f *fakeBillStore) GetByID(_ context.Context, id int64) (domain.Bill, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, b := range f.items {
		if b.ID == id {
			return b, nil
		}
	}
	return domain.Bill{}, domain.ErrNotFound
}
func (f *fakeBillStore) GetByFileHash(_ context.Context, hash string) (domain.Bill, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, b := range f.items {
		if b.FileHash == hash {
			return b, nil
		}
		// Every stored part counts, mirroring the child-table dedup.
		for _, part := range b.Files {
			if part.FileHash == hash {
				return b, nil
			}
		}
	}
	return domain.Bill{}, domain.ErrNotFound
}
func (f *fakeBillStore) List(context.Context, BillFilters) ([]domain.Bill, error) {
	return nil, nil
}
func (f *fakeBillStore) Stats(context.Context, string, string, string, string) ([]domain.BillStatsRow, error) {
	return f.stats, nil
}
func (f *fakeBillStore) ListBrands(context.Context) ([]string, error) { return nil, nil }

// fakeBillExtractor delegates to a function so tests can stage results. The
// last prompt the service resolved is captured for assertions.
type fakeBillExtractor struct {
	fn         func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error)
	lastPrompt string
}

func (f *fakeBillExtractor) Extract(ctx context.Context, files []domain.ReceiptFile, p domain.AIProvider, prompt string) (domain.BillDraft, error) {
	f.lastPrompt = prompt
	return f.fn(ctx, files, p, prompt)
}

// TestConnection satisfies the ProviderTester interface SettingsService needs.
func (f *fakeBillExtractor) TestConnection(context.Context, domain.AIProvider) error {
	return nil
}

// fakeCategoryStore is a CategoryStore stub; the worker's category resolution
// only needs List (returning an empty taxonomy).
// fakeCategoryStore is a CategoryStore stub seeded from a map (nil = empty
// taxonomy; GetByID then misses for every id).
type fakeCategoryStore struct {
	cats map[int64]domain.Category
	list []domain.Category // returned by List (nil = empty taxonomy)
}

func (f fakeCategoryStore) List(context.Context) ([]domain.Category, error) { return f.list, nil }
func (f fakeCategoryStore) GetByID(_ context.Context, id int64) (domain.Category, error) {
	if c, ok := f.cats[id]; ok {
		return c, nil
	}
	return domain.Category{}, domain.ErrNotFound
}
func (f fakeCategoryStore) Create(_ context.Context, c domain.Category) (domain.Category, error) {
	return c, nil
}
func (f fakeCategoryStore) Delete(context.Context, int64) error { return nil }

// fakeProductStore is an in-memory ProductStore mirroring the SQLite
// semantics: names are unique case-insensitively (Create returns ErrConflict
// on a NOCASE duplicate) and conflictOnce forces one Create to lose the
// find-or-create race, exercising the re-read path in resolveProduct.
type fakeProductStore struct {
	mu           sync.Mutex
	items        map[int64]domain.Product
	next         int64
	failConflict bool
	// storeRows holds per-store purchase summaries seeded by merge-check tests.
	storeRows map[int64][]domain.ProductStorePrice
}

func newFakeProductStore() *fakeProductStore {
	return &fakeProductStore{
		items:     map[int64]domain.Product{},
		next:      1,
		storeRows: map[int64][]domain.ProductStorePrice{},
	}
}

func (f *fakeProductStore) List(context.Context, domain.ProductFilters) (domain.ProductPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []domain.Product{}
	for _, p := range f.items {
		out = append(out, p)
	}
	return domain.ProductPage{Items: out}, nil
}

// ListGrouped folds the stored rows into one group per generic name (raw
// name when no family), the same rule the real repository applies. No
// service test filters or pages the grouped list — the shape is what matters.
func (f *fakeProductStore) ListGrouped(context.Context, domain.ProductFilters) (domain.ProductGroupPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	groups := []domain.ProductGroup{}
	byKey := map[string]int{}
	for _, p := range f.items {
		key := p.GenericName
		if key == "" {
			key = p.Name
		}
		key = strings.ToLower(key)
		idx, ok := byKey[key]
		if !ok {
			idx = len(groups)
			byKey[key] = idx
			groups = append(groups, domain.ProductGroup{GenericName: key, Items: []domain.Product{}})
		}
		groups[idx].Items = append(groups[idx].Items, p)
		groups[idx].ProductCount++
	}
	return domain.ProductGroupPage{Items: groups}, nil
}

func (f *fakeProductStore) GetByID(_ context.Context, id int64) (domain.Product, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.items[id]
	if !ok {
		return domain.Product{}, domain.ErrNotFound
	}
	return p, nil
}

func (f *fakeProductStore) FindByName(_ context.Context, name string) (domain.Product, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.items {
		if strings.EqualFold(p.Name, name) {
			return p, nil
		}
	}
	return domain.Product{}, domain.ErrNotFound
}

func (f *fakeProductStore) Create(_ context.Context, p domain.Product) (domain.Product, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failConflict {
		// Simulate a concurrent confirm winning the find-or-create: the row
		// exists by the time the loser re-reads, which is what makes the
		// ErrConflict → re-FindByName path observable.
		f.failConflict = false
		f.insert(p)
		return domain.Product{}, domain.ErrConflict
	}
	for _, existing := range f.items {
		if strings.EqualFold(existing.Name, p.Name) {
			return domain.Product{}, domain.ErrConflict
		}
	}
	p.ID = f.next
	f.next++
	f.items[p.ID] = p
	return p, nil
}

// insert assigns the next id and stores p (caller holds the lock).
func (f *fakeProductStore) insert(p domain.Product) {
	p.ID = f.next
	f.next++
	f.items[p.ID] = p
}

func (f *fakeProductStore) Update(_ context.Context, p domain.Product) (domain.Product, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.items[p.ID]; !ok {
		return domain.Product{}, domain.ErrNotFound
	}
	f.items[p.ID] = p
	return p, nil
}

// Merge folds drop into keep: the fake mirrors the observable product-table
// outcome (drop gone, keep's editable fields rewritten, photo untouched — the
// bill-item redirect itself is the repository's job and isn't modeled here).
func (f *fakeProductStore) Merge(_ context.Context, keepID, dropID int64, final domain.Product) (domain.Product, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	keep, ok := f.items[keepID]
	if !ok {
		return domain.Product{}, domain.ErrNotFound
	}
	if _, ok := f.items[dropID]; !ok {
		return domain.Product{}, domain.ErrNotFound
	}
	keep.Name = final.Name
	keep.Brand = final.Brand
	keep.Unit = final.Unit
	keep.CategoryID = final.CategoryID
	keep.Description = final.Description
	delete(f.items, dropID)
	f.items[keepID] = keep
	return keep, nil
}

func (f *fakeProductStore) SetPhoto(_ context.Context, id int64, photoPath string) (domain.Product, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.items[id]
	if !ok {
		return domain.Product{}, domain.ErrNotFound
	}
	p.ImagePath = photoPath
	f.items[id] = p
	return p, nil
}

// StorePrices is unused by the bill workflow tests; it satisfies the interface.
func (f *fakeProductStore) StorePrices(context.Context, int64, string) ([]domain.ProductStorePrice, error) {
	return nil, nil
}

// StorePurchaseSummary returns the per-store rows a merge-check test seeded
// via seedStoreRows (the fake doesn't model bills/bill_items).
func (f *fakeProductStore) StorePurchaseSummary(_ context.Context, id int64) ([]domain.ProductStorePrice, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.items[id]; !ok {
		return nil, domain.ErrNotFound
	}
	return f.storeRows[id], nil
}

// seedStoreRows attaches per-store purchase summary rows to a product for
// merge-check tests.
func (f *fakeProductStore) seedStoreRows(id int64, rows ...domain.ProductStorePrice) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.storeRows[id] = append([]domain.ProductStorePrice{}, rows...)
}

// fakeSettingsStore + passthrough box feed provider resolution.
type fakeSettingsStore struct{ data map[string]string }

func (f *fakeSettingsStore) Get(_ context.Context, key string) (string, error) {
	v, ok := f.data[key]
	if !ok {
		return "", domain.ErrNotFound
	}
	return v, nil
}
func (f *fakeSettingsStore) Put(_ context.Context, key, value string) error {
	f.data[key] = value
	return nil
}

type passthroughBox struct{}

func (passthroughBox) EncryptString(s string) (string, error) { return s, nil }
func (passthroughBox) DecryptString(s string) (string, error) { return s, nil }

func testProviderJSON() string {
	return `[{"id":"p1","type":"ollama","model":"test-vision"}]`
}

func newTestBillService(t *testing.T, extractFn func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error)) (*BillService, *fakeBillScanStore, *fakeBillStore, *fakeStoreStore, *fakeCategoryStore) {
	t.Helper()
	svc, scanStore, billStore, storeStore, catStore, products := newTestBillServiceWithProducts(t, extractFn)
	_ = products
	return svc, scanStore, billStore, storeStore, catStore
}

// newTestBillServiceWithProducts also exposes the product store, for tests
// that assert on the catalogue built from confirmed bills.
func newTestBillServiceWithProducts(t *testing.T, extractFn func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error)) (*BillService, *fakeBillScanStore, *fakeBillStore, *fakeStoreStore, *fakeCategoryStore, *fakeProductStore) {
	t.Helper()
	scanStore := newFakeBillScanStore()
	billStore := &fakeBillStore{}
	storeStore := newFakeStoreStore()
	catStore := &fakeCategoryStore{cats: map[int64]domain.Category{}}
	products := newFakeProductStore()
	extractor := &fakeBillExtractor{fn: extractFn}
	settings := NewSettingsService(
		&fakeSettingsStore{data: map[string]string{settingsKeyAIProviders: testProviderJSON()}},
		passthroughBox{}, extractor)
	svc := NewBillService(billStore, scanStore, extractor, settings,
		nil, newFakeAccountStore(), catStore, storeStore, products, nil, nil, newFakeTxStore(), nil, t.TempDir(), nil, 5*time.Second, nil)
	t.Cleanup(svc.Close)
	return svc, scanStore, billStore, storeStore, catStore, products
}

// waitFor polls until cond passes or the deadline hits (fail via t.Fatal).
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met within", timeout)
}

func testImage() []byte { return []byte("fake-jpeg-bytes") }

// scanFile wraps raw image bytes as the single part of a receipt upload.
func scanFile(data []byte) []domain.ReceiptFile {
	return []domain.ReceiptFile{{Data: data, MimeType: "image/jpeg"}}
}

// scanFiles wraps raw image parts (in order) as one multi-part upload.
func scanFiles(parts ...[]byte) []domain.ReceiptFile {
	files := make([]domain.ReceiptFile, len(parts))
	for i, p := range parts {
		files[i] = domain.ReceiptFile{Data: p, MimeType: "image/jpeg"}
	}
	return files
}

// --- tests -------------------------------------------------------------------

func TestScanReturnsAnalyzingImmediately(t *testing.T) {
	blocked := make(chan struct{}) // extractor never returns
	svc, scanStore, _, _, _ := newTestBillService(t, func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
		<-blocked
		return domain.BillDraft{}, errors.New("unreachable")
	})

	res, err := svc.Scan(context.Background(), scanFile(testImage()), "")
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if res.Status != domain.BillScanAnalyzing || res.Draft != nil || res.ScanToken == "" {
		t.Fatalf("expected analyzing scan with token, got %+v", res)
	}
	row, err := scanStore.GetByToken(context.Background(), res.ScanToken)
	if err != nil || row.Status != domain.BillScanAnalyzing {
		t.Fatalf("scan row not persisted as analyzing: %v %+v", err, row)
	}
}

func TestWorkerCompletesExtraction(t *testing.T) {
	svc, scanStore, _, _, _ := newTestBillService(t, func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
		return domain.BillDraft{
			MarketName: "Test Market",
			Items: []domain.BillItemDraft{
				{Name: "Milk", Quantity: 1, UnitPriceCents: 200, LineTotalCents: 200},
				{Name: "Bread", Quantity: 1, UnitPriceCents: 150, LineTotalCents: 150},
			},
			TotalCents: 350, Currency: "USD",
		}, nil
	})

	res, err := svc.Scan(context.Background(), scanFile(testImage()), "")
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	waitFor(t, 2*time.Second, func() bool {
		row, err := scanStore.GetByToken(context.Background(), res.ScanToken)
		return err == nil && row.Status == domain.BillScanDone
	})
	row, _ := scanStore.GetByToken(context.Background(), res.ScanToken)
	if row.Draft == nil || len(row.Draft.Items) != 2 || row.Draft.Items[0].ID != 1 || row.Draft.Items[1].ID != 2 {
		t.Fatalf("draft not stored with numbered lines: %+v", row.Draft)
	}
}

func TestWorkerMarksFailure(t *testing.T) {
	svc, scanStore, _, _, _ := newTestBillService(t, func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
		return domain.BillDraft{}, errors.New("ollama is down")
	})

	res, err := svc.Scan(context.Background(), scanFile(testImage()), "")
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	waitFor(t, 2*time.Second, func() bool {
		row, err := scanStore.GetByToken(context.Background(), res.ScanToken)
		return err == nil && row.Status == domain.BillScanFailed
	})
	row, _ := scanStore.GetByToken(context.Background(), res.ScanToken)
	if !strings.Contains(row.Error, "ollama is down") {
		t.Fatalf("failure message not recorded: %q", row.Error)
	}
}

func TestWorkerRetriesTimeoutsWithEscalatingBudgets(t *testing.T) {
	oldSettle := timeoutRetrySettle
	timeoutRetrySettle = 10 * time.Millisecond
	t.Cleanup(func() { timeoutRetrySettle = oldSettle })

	var mu sync.Mutex
	var deadlines []time.Duration
	attempt := 0
	svc, scanStore, _, _, _ := newTestBillService(t, func(ctx context.Context, _ []domain.ReceiptFile, _ domain.AIProvider, _ string) (domain.BillDraft, error) {
		mu.Lock()
		attempt++
		n := attempt
		if dl, ok := ctx.Deadline(); ok {
			deadlines = append(deadlines, time.Until(dl))
		}
		mu.Unlock()
		if n < 3 {
			return domain.BillDraft{}, context.DeadlineExceeded
		}
		return domain.BillDraft{MarketName: "M", TotalCents: 100, Currency: "USD"}, nil
	})
	// The service harness passes extractTimeout = 5s, so the escalating
	// budgets are 5s → 10s → 20s.
	wantMin := []time.Duration{4 * time.Second, 9 * time.Second, 19 * time.Second}
	wantMax := []time.Duration{6 * time.Second, 11 * time.Second, 21 * time.Second}

	res, err := svc.Scan(context.Background(), scanFile(testImage()), "")
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		row, err := scanStore.GetByToken(context.Background(), res.ScanToken)
		return err == nil && row.Status == domain.BillScanDone
	})
	mu.Lock()
	defer mu.Unlock()
	if len(deadlines) != 3 {
		t.Fatalf("expected 3 extraction attempts, got %d", len(deadlines))
	}
	for i, got := range deadlines {
		if got < wantMin[i] || got > wantMax[i] {
			t.Fatalf("attempt %d budget = %s, want ~%s", i+1, got, wantMin[i]+time.Second)
		}
	}
}

func TestWorkerFailsAfterMaxTimeoutRetries(t *testing.T) {
	oldSettle := timeoutRetrySettle
	timeoutRetrySettle = 10 * time.Millisecond
	t.Cleanup(func() { timeoutRetrySettle = oldSettle })

	svc, scanStore, _, _, _ := newTestBillService(t, func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
		return domain.BillDraft{}, context.DeadlineExceeded
	})

	res, err := svc.Scan(context.Background(), scanFile(testImage()), "")
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		row, err := scanStore.GetByToken(context.Background(), res.ScanToken)
		return err == nil && row.Status == domain.BillScanFailed
	})
	row, _ := scanStore.GetByToken(context.Background(), res.ScanToken)
	if !strings.Contains(row.Error, "timed out after 3 attempts") || !strings.Contains(row.Error, "LLM_TIMEOUT") {
		t.Fatalf("exhausted-retry message not recorded: %q", row.Error)
	}
}

func TestConfirmGuardsScanState(t *testing.T) {
	svc, scanStore, billStore, _, _ := newTestBillService(t, func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
		return domain.BillDraft{MarketName: "M", TotalCents: 100, Currency: "USD"}, nil
	})
	ctx := context.Background()

	// Confirm before the analysis finishes → validation error.
	early, err := svc.Scan(ctx, scanFile(testImage()), "")
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if _, err := svc.Confirm(ctx, early.ScanToken, domain.BillConfirmInput{Items: []domain.BillItemDraft{}}); err == nil {
		t.Fatal("expected confirm-while-analyzing to fail")
	}

	waitFor(t, 2*time.Second, func() bool {
		row, err := scanStore.GetByToken(ctx, early.ScanToken)
		return err == nil && row.Status == domain.BillScanDone
	})

	bill, err := svc.Confirm(ctx, early.ScanToken, domain.BillConfirmInput{MarketName: "M", Currency: "USD"})
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if bill.Status != domain.BillStatusAccepted {
		t.Fatalf("expected accepted bill, got %s", bill.Status)
	}
	if _, err := scanStore.GetByToken(ctx, early.ScanToken); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("scan row should be deleted after confirm, got %v", err)
	}
	if len(billStore.items) != 1 {
		t.Fatalf("expected one persisted bill, got %d", len(billStore.items))
	}
	// Token is consumed: further confirms 404.
	if _, err := svc.Confirm(ctx, early.ScanToken, domain.BillConfirmInput{}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for consumed token, got %v", err)
	}
}

func TestReextractGuardsAnalyzingScans(t *testing.T) {
	release := make(chan struct{})
	var calls atomic.Int64
	svc, scanStore, _, _, _ := newTestBillService(t, func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
		calls.Add(1)
		<-release
		return domain.BillDraft{MarketName: "M"}, nil
	})
	ctx := context.Background()

	res, err := svc.Scan(ctx, scanFile(testImage()), "")
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	// Retry while still analyzing → rejected, not double-enqueued.
	if _, err := svc.Reextract(ctx, res.ScanToken, ""); err == nil {
		t.Fatal("expected reextract-while-analyzing to fail")
	}
	close(release)
	waitFor(t, 2*time.Second, func() bool {
		row, err := scanStore.GetByToken(ctx, res.ScanToken)
		return err == nil && row.Status == domain.BillScanDone
	})

	// After done: retry claims and re-enqueues.
	again, err := svc.Reextract(ctx, res.ScanToken, "p1")
	if err != nil {
		t.Fatalf("Reextract: %v", err)
	}
	if again.Status != domain.BillScanAnalyzing {
		t.Fatalf("expected analyzing after retry, got %s", again.Status)
	}
	waitFor(t, 2*time.Second, func() bool {
		row, err := scanStore.GetByToken(ctx, res.ScanToken)
		return err == nil && row.Status == domain.BillScanDone && row.ProviderID == "p1"
	})
	if calls.Load() < 2 {
		t.Fatalf("expected the extractor to run twice, ran %d times", calls.Load())
	}
}

func TestDiscardScanRemovesRow(t *testing.T) {
	svc, scanStore, _, _, _ := newTestBillService(t, func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
		return domain.BillDraft{MarketName: "M"}, nil
	})
	ctx := context.Background()

	res, err := svc.Scan(ctx, scanFile(testImage()), "")
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if err := svc.DiscardScan(ctx, res.ScanToken); err != nil {
		t.Fatalf("DiscardScan: %v", err)
	}
	if _, err := scanStore.GetByToken(ctx, res.ScanToken); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected row deleted, got %v", err)
	}
}

// Cancelling an in-progress analysis stops the model call (the extraction
// context is cancelled), the row lands as cancelled, the worker never writes
// its aborted result over the cancellation, and the receipt can be read again
// afterwards (Reextract claims the cancelled scan).
func TestCancelScanAbortsExtraction(t *testing.T) {
	draft := domain.BillDraft{MarketName: "M", TotalCents: 100, Currency: "USD"}
	var calls atomic.Int64
	svc, scanStore, _, _, _ := newTestBillService(t, func(ctx context.Context, _ []domain.ReceiptFile, _ domain.AIProvider, _ string) (domain.BillDraft, error) {
		if calls.Add(1) == 1 {
			// First attempt: model call in flight — it returns only when the
			// user's cancel reaches this context.
			<-ctx.Done()
			return domain.BillDraft{}, ctx.Err()
		}
		return draft, nil
	})
	ctx := context.Background()

	res, err := svc.Scan(ctx, scanFile(testImage()), "")
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	// Wait until the extractor is inside the model call → its canceller is
	// registered, so CancelScan deterministically aborts the run.
	waitFor(t, 2*time.Second, func() bool { return calls.Load() == 1 })

	cancelled, err := svc.CancelScan(ctx, res.ScanToken)
	if err != nil {
		t.Fatalf("CancelScan: %v", err)
	}
	if cancelled.Status != domain.BillScanCancelled {
		t.Fatalf("cancelled status = %s, want cancelled", cancelled.Status)
	}
	row, err := scanStore.GetByToken(ctx, res.ScanToken)
	if err != nil || row.Status != domain.BillScanCancelled {
		t.Fatalf("row = %v, %v; want cancelled", row.Status, err)
	}

	// The aborted worker must not resurrect the scan (failed/done writes are
	// guarded on analyzing): give it time to notice, then assert it stayed.
	waitFor(t, 500*time.Millisecond, func() bool { return svc.lookupCanceller(res.ScanToken) == nil })
	if row, _ := scanStore.GetByToken(ctx, res.ScanToken); row.Status != domain.BillScanCancelled {
		t.Fatalf("status after worker unwind = %s, want still cancelled", row.Status)
	}

	// Re-extraction claims the cancelled scan and completes it.
	again, err := svc.Reextract(ctx, res.ScanToken, "")
	if err != nil {
		t.Fatalf("Reextract after cancel: %v", err)
	}
	if again.Status != domain.BillScanAnalyzing {
		t.Fatalf("claimed scan status = %s, want analyzing", again.Status)
	}
	waitFor(t, 2*time.Second, func() bool {
		row, err := scanStore.GetByToken(ctx, res.ScanToken)
		return err == nil && row.Status == domain.BillScanDone
	})
	if calls.Load() != 2 {
		t.Fatalf("extractor ran %d times, want 2", calls.Load())
	}
}

// Cancelling a scan that already finished (or an unknown token): a done scan
// comes back untouched, a consumed/expired token is not found.
func TestCancelScanNotAnalyzing(t *testing.T) {
	svc, scanStore, _, _, _ := newTestBillService(t, func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
		return domain.BillDraft{MarketName: "M", TotalCents: 100, Currency: "USD"}, nil
	})
	ctx := context.Background()

	res, err := svc.Scan(ctx, scanFile(testImage()), "")
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		row, err := scanStore.GetByToken(ctx, res.ScanToken)
		return err == nil && row.Status == domain.BillScanDone
	})

	done, err := svc.CancelScan(ctx, res.ScanToken)
	if err != nil {
		t.Fatalf("CancelScan on a done scan: %v", err)
	}
	if done.Status != domain.BillScanDone {
		t.Fatalf("status = %s, want done (cancel must not harm finished scans)", done.Status)
	}

	if _, err := svc.CancelScan(ctx, "deadbeef"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown token: err = %v, want ErrNotFound", err)
	}

	// A discarded scan is gone too — cancel after discard is not found.
	if err := svc.DiscardScan(ctx, res.ScanToken); err != nil {
		t.Fatalf("DiscardScan: %v", err)
	}
	if _, err := svc.CancelScan(ctx, res.ScanToken); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cancelled-after-discard: err = %v, want ErrNotFound", err)
	}
}

func TestCloseReturnsWhileExtractionBlocked(t *testing.T) {
	blocked := make(chan struct{})
	svc, _, _, _, _ := newTestBillService(t, func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
		<-blocked
		return domain.BillDraft{}, errors.New("unreachable")
	})
	if _, err := svc.Scan(context.Background(), scanFile(testImage()), ""); err != nil {
		t.Fatalf("Scan: %v", err)
	}

	start := time.Now()
	svc.Close()
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("Close waited %v; it must not wait for the extraction", elapsed)
	}
}

func TestGetScanUnknownTokenIsNotFound(t *testing.T) {
	svc, _, _, _, _ := newTestBillService(t, func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
		return domain.BillDraft{}, errors.New("not called")
	})
	if _, err := svc.GetScan(context.Background(), "deadbeef"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// --- worker resilience ---------------------------------------------------------

func TestWorkerSurvivesExtractorPanic(t *testing.T) {
	var calls atomic.Int64
	svc, scanStore, _, _, _ := newTestBillService(t, func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
		if calls.Add(1) == 1 {
			panic("boom in extraction")
		}
		return domain.BillDraft{MarketName: "M", TotalCents: 1, Currency: "EUR"}, nil
	})
	ctx := context.Background()

	first, err := svc.Scan(ctx, scanFile(testImage()), "")
	if err != nil {
		t.Fatalf("Scan 1: %v", err)
	}
	// The panicking extraction must land as a failed scan the user can retry…
	waitFor(t, 2*time.Second, func() bool {
		row, err := scanStore.GetByToken(ctx, first.ScanToken)
		return err == nil && row.Status == domain.BillScanFailed
	})
	row, _ := scanStore.GetByToken(ctx, first.ScanToken)
	if !strings.Contains(row.Error, "internal error") {
		t.Fatalf("expected the panic to be recorded as an internal error, got %q", row.Error)
	}

	// …and the worker must survive to process the next upload.
	second, err := svc.Scan(ctx, scanFile([]byte("another-fake-jpeg")), "")
	if err != nil {
		t.Fatalf("Scan 2: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		row, err := scanStore.GetByToken(ctx, second.ScanToken)
		return err == nil && row.Status == domain.BillScanDone
	})
	if calls.Load() != 2 {
		t.Fatalf("expected the extractor to run twice, ran %d times", calls.Load())
	}
}

// --- store find-or-create on confirm/update ----------------------------------

func TestConfirmFindOrCreatesStore(t *testing.T) {
	svc, scanStore, _, storeStore, _ := newTestBillService(t, func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
		return domain.BillDraft{MarketName: "M"}, nil
	})
	ctx := context.Background()

	res, err := svc.Scan(ctx, scanFile(testImage()), "")
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		row, err := scanStore.GetByToken(ctx, res.ScanToken)
		return err == nil && row.Status == domain.BillScanDone
	})

	bill, err := svc.Confirm(ctx, res.ScanToken, domain.BillConfirmInput{MarketName: "  REWE  ", Currency: "USD"})
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if bill.StoreID == nil || bill.MarketName != "REWE" {
		t.Fatalf("expected linked store + canonical name, got store=%v market=%q", bill.StoreID, bill.MarketName)
	}
	stores, _ := storeStore.List(ctx)
	if len(stores) != 1 || stores[0].ID != *bill.StoreID || stores[0].Name != "REWE" {
		t.Fatalf("expected exactly one store REWE, got %+v", stores)
	}
}

func TestConfirmReusesStoreCaseInsensitive(t *testing.T) {
	svc, scanStore, _, storeStore, _ := newTestBillService(t, func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
		return domain.BillDraft{MarketName: "M"}, nil
	})
	ctx := context.Background()

	first, err := svc.Scan(ctx, scanFile(testImage()), "")
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		row, err := scanStore.GetByToken(ctx, first.ScanToken)
		return err == nil && row.Status == domain.BillScanDone
	})
	bill1, err := svc.Confirm(ctx, first.ScanToken, domain.BillConfirmInput{MarketName: "REWE", Currency: "USD"})
	if err != nil {
		t.Fatalf("Confirm 1: %v", err)
	}

	// A second bill naming the same store in another casing reuses it (the
	// second receipt must be a different image — same bytes are a duplicate).
	second, err := svc.Scan(ctx, scanFile([]byte("another-fake-jpeg")), "")
	if err != nil {
		t.Fatalf("Scan 2: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		row, err := scanStore.GetByToken(ctx, second.ScanToken)
		return err == nil && row.Status == domain.BillScanDone
	})
	bill2, err := svc.Confirm(ctx, second.ScanToken, domain.BillConfirmInput{MarketName: "rewe", Currency: "USD"})
	if err != nil {
		t.Fatalf("Confirm 2: %v", err)
	}

	if bill1.StoreID == nil || bill2.StoreID == nil || *bill1.StoreID != *bill2.StoreID {
		t.Fatalf("expected both bills to link the same store, got %v and %v", bill1.StoreID, bill2.StoreID)
	}
	if bill2.MarketName != "REWE" {
		t.Fatalf("expected canonical casing, got %q", bill2.MarketName)
	}
	stores, _ := storeStore.List(ctx)
	if len(stores) != 1 {
		t.Fatalf("expected one store, got %d", len(stores))
	}
}

func TestConfirmEmptyMarketHasNoStore(t *testing.T) {
	svc, scanStore, _, storeStore, _ := newTestBillService(t, func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
		return domain.BillDraft{MarketName: "M"}, nil
	})
	ctx := context.Background()

	res, err := svc.Scan(ctx, scanFile(testImage()), "")
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		row, err := scanStore.GetByToken(ctx, res.ScanToken)
		return err == nil && row.Status == domain.BillScanDone
	})

	bill, err := svc.Confirm(ctx, res.ScanToken, domain.BillConfirmInput{Currency: "USD"})
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if bill.StoreID != nil {
		t.Fatalf("expected no store for empty market, got %v", bill.StoreID)
	}
	stores, _ := storeStore.List(ctx)
	if len(stores) != 0 {
		t.Fatalf("expected no store created, got %+v", stores)
	}
}

func TestBillUpdateRelinksStore(t *testing.T) {
	svc, scanStore, _, storeStore, _ := newTestBillService(t, func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
		return domain.BillDraft{MarketName: "M"}, nil
	})
	ctx := context.Background()

	res, err := svc.Scan(ctx, scanFile(testImage()), "")
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		row, err := scanStore.GetByToken(ctx, res.ScanToken)
		return err == nil && row.Status == domain.BillScanDone
	})
	bill, err := svc.Confirm(ctx, res.ScanToken, domain.BillConfirmInput{MarketName: "Aldi", Currency: "USD"})
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	updated, err := svc.Update(ctx, bill.ID, domain.BillConfirmInput{MarketName: "Lidl", Currency: "USD"})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.StoreID != nil && bill.StoreID != nil && *updated.StoreID == *bill.StoreID {
		t.Fatal("expected the bill's store to move, got the same id")
	}
	if updated.MarketName != "Lidl" {
		t.Fatalf("expected market_name to follow, got %q", updated.MarketName)
	}
	stores, _ := storeStore.List(ctx)
	if len(stores) != 2 {
		t.Fatalf("expected two stores after relink, got %d", len(stores))
	}
}

func TestResolveStoreRetriesAfterConflict(t *testing.T) {
	svc, _, _, storeStore, _ := newTestBillService(t, func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
		return domain.BillDraft{MarketName: "M"}, nil
	})
	ctx := context.Background()

	// Pre-create the store, then make the next Create lose the race anyway.
	storeStore.mu.Lock()
	storeStore.conflictOnce = true
	storeStore.mu.Unlock()

	id, name, err := svc.resolveStore(ctx, "Rewe")
	if err != nil {
		t.Fatalf("resolveStore: %v", err)
	}
	if name != "REWE" || id == nil {
		t.Fatalf("expected re-read winner, got id=%v name=%q", id, name)
	}
}

// scanUntilDone runs a Scan and waits for its worker to finish extraction.
func scanUntilDone(t *testing.T, svc *BillService, scanStore *fakeBillScanStore) domain.BillScan {
	t.Helper()
	res, err := svc.Scan(context.Background(), scanFile(testImage()), "")
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		row, err := scanStore.GetByToken(context.Background(), res.ScanToken)
		return err == nil && row.Status == domain.BillScanDone
	})
	return res
}

func TestScanRejectsDuplicateUpload(t *testing.T) {
	svc, scanStore, _, _, _ := newTestBillService(t, func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
		return domain.BillDraft{MarketName: "M"}, nil
	})

	// Same bytes uploaded twice while the first scan is still pending → 409.
	first := scanUntilDone(t, svc, scanStore)
	if _, err := svc.Scan(context.Background(), scanFile(testImage()), ""); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("expected duplicate scan to conflict, got %v", err)
	}

	// Confirmed bills block re-uploads too.
	bill, err := svc.Confirm(context.Background(), first.ScanToken, domain.BillConfirmInput{MarketName: "REWE"})
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if _, err := svc.Scan(context.Background(), scanFile(testImage()), ""); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("expected duplicate after confirm to conflict, got %v", err)
	}

	// The bill carries the upload's content hash for that check.
	if bill.FileHash == "" {
		t.Fatal("expected confirmed bill to store the file hash")
	}

	// A different image uploads fine after the first scan was consumed.
	if _, err := svc.Scan(context.Background(), scanFile([]byte("other-receipt-bytes")), ""); err != nil {
		t.Fatalf("Scan (different image): %v", err)
	}
	scanStore.mu.Lock()
	tokens := len(scanStore.items)
	scanStore.mu.Unlock()
	if tokens != 1 {
		t.Fatalf("expected 1 active scan after confirm consumed the first, got %d", tokens)
	}
}

// --- multi-part receipts (one receipt split across several files) -------------

// threeReceiptParts returns three distinct images standing in for the
// top/middle/bottom photos of one long receipt.
func threeReceiptParts() [][]byte {
	return [][]byte{[]byte("receipt-top"), []byte("receipt-middle"), []byte("receipt-bottom")}
}

// scanPartsUntilDone uploads the given parts as ONE grouped receipt and waits
// for the done draft.
func scanPartsUntilDone(t *testing.T, svc *BillService, scanStore *fakeBillScanStore, parts ...[]byte) domain.BillScan {
	t.Helper()
	res, err := svc.Scan(context.Background(), scanFiles(parts...), "")
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		row, err := scanStore.GetByToken(context.Background(), res.ScanToken)
		return err == nil && row.Status == domain.BillScanDone
	})
	return res
}

// One grouped upload of several parts becomes a single scan token: one AI read
// handed every part in order, every part stored on disk, one row.
func TestScanGroupsMultiFileReceipt(t *testing.T) {
	var gotFiles []domain.ReceiptFile
	svc, scanStore, _, _, _ := newTestBillService(t, func(_ context.Context, files []domain.ReceiptFile, _ domain.AIProvider, _ string) (domain.BillDraft, error) {
		gotFiles = files
		return domain.BillDraft{MarketName: "M", TotalCents: 100, Currency: "EUR"}, nil
	})
	parts := threeReceiptParts()

	res, err := svc.Scan(context.Background(), scanFiles(parts...), "")
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if res.FileCount != 3 {
		t.Fatalf("response file_count = %d, want 3", res.FileCount)
	}

	waitFor(t, 2*time.Second, func() bool {
		row, err := scanStore.GetByToken(context.Background(), res.ScanToken)
		return err == nil && row.Status == domain.BillScanDone
	})
	row, _ := scanStore.GetByToken(context.Background(), res.ScanToken)

	// The one AI read saw every part, in upload order.
	if len(gotFiles) != 3 {
		t.Fatalf("extractor saw %d files, want 3", len(gotFiles))
	}
	for i, f := range gotFiles {
		if string(f.Data) != string(parts[i]) {
			t.Errorf("extractor file %d = %q, want %q", i+1, f.Data, parts[i])
		}
	}

	// The row carries every part with its position; part 1 mirrors the
	// legacy single-file columns.
	if len(row.Files) != 3 || row.FileCount != 3 {
		t.Fatalf("row files/count = %d/%d, want 3/3", len(row.Files), row.FileCount)
	}
	for i, f := range row.Files {
		if f.Position != i+1 {
			t.Errorf("file %d position = %d", i+1, f.Position)
		}
		if f.Path == "" || f.FileHash == "" {
			t.Errorf("file %d missing path/hash: %+v", i+1, f)
		}
	}
	if row.ImagePath != row.Files[0].Path || row.FileHash != row.Files[0].FileHash {
		t.Error("legacy columns must mirror part 1")
	}

	// Every part is on disk.
	for _, f := range row.Files {
		if _, err := os.Stat(f.Path); err != nil {
			t.Errorf("part %d not stored: %v", f.Position, err)
		}
	}

	// One token — the parts were not scanned separately.
	scanStore.mu.Lock()
	n := len(scanStore.items)
	scanStore.mu.Unlock()
	if n != 1 {
		t.Fatalf("expected a single scan row for the grouped upload, got %d", n)
	}
}

func TestScanRejectsTooManyFiles(t *testing.T) {
	svc, _, _, _, _ := newTestBillService(t, func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
		return domain.BillDraft{}, errors.New("not called")
	})
	ctx := context.Background()

	if _, err := svc.Scan(ctx, nil, ""); err == nil || !strings.Contains(err.Error(), "no receipt file") {
		t.Fatalf("empty upload: %v", err)
	}

	parts := make([][]byte, MaxBillScanFiles+1)
	for i := range parts {
		parts[i] = []byte(fmt.Sprintf("part-%d", i))
	}
	_, err := svc.Scan(ctx, scanFiles(parts...), "")
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("at most %d files", MaxBillScanFiles)) {
		t.Fatalf("expected the cap to reject %d files, got %v", len(parts), err)
	}

	// The cap itself is fine.
	parts = parts[:MaxBillScanFiles]
	if _, err := svc.Scan(ctx, scanFiles(parts...), ""); err != nil {
		t.Fatalf("Scan at the cap: %v", err)
	}
}

// The same photo picked twice in one grouped upload is rejected up front.
func TestScanRejectsDuplicatePartWithinUpload(t *testing.T) {
	svc, _, _, _, _ := newTestBillService(t, func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
		return domain.BillDraft{}, errors.New("not called")
	})

	_, err := svc.Scan(context.Background(), scanFiles([]byte("top"), []byte("bottom"), []byte("top")), "")
	if err == nil || !strings.Contains(err.Error(), "file 3 of 3 is a duplicate of an earlier file") {
		t.Fatalf("expected the repeated part to be named, got %v", err)
	}
}

// Re-uploading any part of an existing receipt conflicts, and the error names
// the offending part — whether the earlier receipt is still analyzing or was
// already saved as a bill.
func TestScanDedupNamesOffendingPart(t *testing.T) {
	svc, scanStore, _, _, _ := newTestBillService(t, func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
		return domain.BillDraft{MarketName: "M", TotalCents: 100, Currency: "EUR"}, nil
	})
	ctx := context.Background()
	first := scanPartsUntilDone(t, svc, scanStore, threeReceiptParts()...)

	// Part 2 of the old receipt re-appears as part 1 of the new upload.
	_, err := svc.Scan(ctx, scanFiles([]byte("receipt-middle"), []byte("a-different-photo")), "")
	if err == nil || !strings.Contains(err.Error(), "file 1 of 2 is already being analyzed") {
		t.Fatalf("expected the reused scan part to be named, got %v", err)
	}

	// …and as part 2 of a new upload after the original became a bill.
	bill, err := svc.Confirm(ctx, first.ScanToken, domain.BillConfirmInput{MarketName: "REWE", Currency: "EUR"})
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	_, err = svc.Scan(ctx, scanFiles([]byte("a-different-photo"), []byte("receipt-bottom")), "")
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("file 2 of 2 was already saved as bill #%d", bill.ID)) {
		t.Fatalf("expected the reused bill part to be named, got %v", err)
	}
}

// The worker reads every part from disk before extraction; a vanished part
// fails the scan with an actionable message instead of a panic.
func TestWorkerFailsWhenPartMissing(t *testing.T) {
	svc, scanStore, _, _, _ := newTestBillService(t, func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
		return domain.BillDraft{}, errors.New("extractor must not run when a part is unreadable")
	})
	ctx := context.Background()

	res, err := svc.Scan(ctx, scanFiles(threeReceiptParts()...), "")
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	row, err := scanStore.GetByToken(ctx, res.ScanToken)
	if err != nil {
		t.Fatalf("GetByToken: %v", err)
	}
	_ = os.Remove(row.Files[1].Path) // part 2 vanished on disk

	svc.processScan(res.ScanToken, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	row, _ = scanStore.GetByToken(ctx, res.ScanToken)
	if row.Status != domain.BillScanFailed {
		t.Fatalf("status = %q, want failed", row.Status)
	}
	if !strings.Contains(row.Error, "receipt part 2 of 3 is missing on disk") {
		t.Fatalf("failure should name the missing part, got %q", row.Error)
	}
}

// Discarding a grouped scan removes every stored part, not just part 1.
func TestDiscardRemovesAllParts(t *testing.T) {
	blocked := make(chan struct{})
	svc, scanStore, _, _, _ := newTestBillService(t, func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
		<-blocked
		return domain.BillDraft{}, errors.New("unreachable")
	})
	ctx := context.Background()

	res, err := svc.Scan(ctx, scanFiles(threeReceiptParts()...), "")
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	row, err := scanStore.GetByToken(ctx, res.ScanToken)
	if err != nil {
		t.Fatalf("GetByToken: %v", err)
	}
	paths := scanFilePaths(row)
	if len(paths) != 3 {
		t.Fatalf("expected 3 stored paths, got %d", len(paths))
	}

	if err := svc.DiscardScan(ctx, res.ScanToken); err != nil {
		t.Fatalf("DiscardScan: %v", err)
	}
	for _, p := range paths {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("part %s still on disk after discard (%v)", filepath.Base(p), err)
		}
	}
	close(blocked)
}

// Confirm carries every part from the scan into the saved bill.
func TestConfirmCreatesBillFiles(t *testing.T) {
	svc, scanStore, billStore, _, _ := newTestBillService(t, func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
		return domain.BillDraft{MarketName: "REWE", TotalCents: 100, Currency: "EUR"}, nil
	})
	ctx := context.Background()
	first := scanPartsUntilDone(t, svc, scanStore, threeReceiptParts()...)

	bill, err := svc.Confirm(ctx, first.ScanToken, domain.BillConfirmInput{MarketName: "REWE", Currency: "EUR"})
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if len(bill.Files) != 3 || bill.FileCount != 3 {
		t.Fatalf("bill files/count = %d/%d, want 3/3", len(bill.Files), bill.FileCount)
	}
	for i, f := range bill.Files {
		if f.Position != i+1 {
			t.Errorf("bill file %d position = %d", i+1, f.Position)
		}
		if _, err := os.Stat(f.Path); err != nil {
			t.Errorf("bill part %d not on disk: %v", f.Position, err)
		}
	}
	if bill.ImagePath != bill.Files[0].Path {
		t.Error("bill legacy column must mirror part 1")
	}

	// The store keeps the parts for the saved-bill read path.
	billStore.mu.Lock()
	stored := billStore.items[0]
	billStore.mu.Unlock()
	if len(stored.Files) != 3 {
		t.Fatalf("stored bill parts = %d, want 3", len(stored.Files))
	}
}

func TestReceiptImagePathServesRequestedPart(t *testing.T) {
	svc, scanStore, _, _, _ := newTestBillService(t, func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
		return domain.BillDraft{MarketName: "REWE", TotalCents: 100, Currency: "EUR"}, nil
	})
	ctx := context.Background()
	first := scanPartsUntilDone(t, svc, scanStore, threeReceiptParts()...)
	bill, err := svc.Confirm(ctx, first.ScanToken, domain.BillConfirmInput{MarketName: "REWE", Currency: "EUR"})
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	for part := 1; part <= 3; part++ {
		path, mime, err := svc.ReceiptImagePath(ctx, bill.ID, part)
		if err != nil {
			t.Fatalf("ReceiptImagePath(%d): %v", part, err)
		}
		if path != bill.Files[part-1].Path {
			t.Errorf("part %d served %s, want %s", part, filepath.Base(path), filepath.Base(bill.Files[part-1].Path))
		}
		if mime != "image/jpeg" {
			t.Errorf("part %d mime = %q", part, mime)
		}
	}

	// part 0 means part 1 (the classic single-image link); parts beyond the
	// end are a validation error, not a 500.
	if path, _, _ := svc.ReceiptImagePath(ctx, bill.ID, 0); path != bill.Files[0].Path {
		t.Errorf("part 0 should serve part 1")
	}
	if _, _, err := svc.ReceiptImagePath(ctx, bill.ID, 4); err == nil || !strings.Contains(err.Error(), "no receipt part 4") {
		t.Fatalf("expected out-of-range part to fail, got %v", err)
	}
}

func TestBuildBillNegativeDepositCategory(t *testing.T) {
	svc, _, _, _, catStore := newTestBillService(t, func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
		return domain.BillDraft{MarketName: "M"}, nil
	})
	catStore.cats[7] = domain.Category{ID: 7, Name: "Deposit & Returns", AllowsNegative: true}
	ctx := context.Background()

	bill, err := svc.buildBill(ctx, domain.BillConfirmInput{
		MarketName: "REWE",
		Currency:   "EUR",
		Items: []domain.BillItemDraft{
			{Name: "Leergut 8+16", CategoryID: ptrInt64(7), Quantity: 1, UnitPriceCents: -180, LineTotalCents: -180},
			{Name: "Cola", CategoryID: nil, Quantity: 1, UnitPriceCents: 200, LineTotalCents: 200},
		},
	}, &billScanSource{})
	if err != nil {
		t.Fatalf("buildBill: %v", err)
	}
	if bill.TotalCents != 20 {
		t.Fatalf("expected deposit refund to reduce the total to 0.20, got %d", bill.TotalCents)
	}
	if bill.Items[0].LineTotalCents != -180 {
		t.Fatalf("expected negative line total kept, got %d", bill.Items[0].LineTotalCents)
	}
}

func TestBuildBillRejectsNegativeForNormalCategory(t *testing.T) {
	svc, _, _, _, catStore := newTestBillService(t, func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
		return domain.BillDraft{MarketName: "M"}, nil
	})
	catStore.cats[3] = domain.Category{ID: 3, Name: "Cola & Soda"}
	ctx := context.Background()

	// Negative price on an ordinary product category stays forbidden…
	_, err := svc.buildBill(ctx, domain.BillConfirmInput{
		MarketName: "REWE",
		Items:      []domain.BillItemDraft{{Name: "Cola", CategoryID: ptrInt64(3), Quantity: 1, UnitPriceCents: -100}},
	}, &billScanSource{})
	if err == nil {
		t.Fatal("expected negative price on a normal category to be rejected")
	}
	// …and so does a negative discount there.
	_, err = svc.buildBill(ctx, domain.BillConfirmInput{
		MarketName: "REWE",
		Items:      []domain.BillItemDraft{{Name: "Cola", CategoryID: ptrInt64(3), Quantity: 1, UnitPriceCents: 200, DiscountCents: -50}},
	}, &billScanSource{})
	if err == nil {
		t.Fatal("expected negative discount on a normal category to be rejected")
	}
}

// The bill-level discount is the receipt-wide rebate printed after the article
// lines (e.g. "10% Rabatt") — it reduces the total below the item sum, so a
// discounted receipt reconciles with its printed amount instead of showing a
// false mismatch.
func TestBuildBillGlobalDiscountReducesTotal(t *testing.T) {
	svc, _, _, _, _ := newTestBillService(t, func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
		return domain.BillDraft{MarketName: "M"}, nil
	})
	ctx := context.Background()

	bill, err := svc.buildBill(ctx, domain.BillConfirmInput{
		MarketName:        "REWE",
		Currency:          "EUR",
		DiscountCents:     200,
		PrintedTotalCents: 800,
		Items: []domain.BillItemDraft{
			{Name: "Cola 1.5L", Quantity: 1, UnitPriceCents: 600, LineTotalCents: 600},
			{Name: "Chips", Quantity: 1, UnitPriceCents: 400, LineTotalCents: 400},
		},
	}, &billScanSource{})
	if err != nil {
		t.Fatalf("buildBill: %v", err)
	}
	if bill.ItemsSubtotalCents != 1000 {
		t.Fatalf("item sum must stay 1000, got %d", bill.ItemsSubtotalCents)
	}
	if bill.TotalCents != 800 {
		t.Fatalf("global discount must reduce the total to 800, got %d", bill.TotalCents)
	}
	if bill.PrintedTotalCents != 800 {
		t.Fatalf("printed total: %d", bill.PrintedTotalCents)
	}

	// A negative bill-level discount stays rejected (totals must not be
	// negative).
	_, err = svc.buildBill(ctx, domain.BillConfirmInput{
		MarketName:    "REWE",
		DiscountCents: -100,
		Items:         []domain.BillItemDraft{{Name: "Cola", Quantity: 1, UnitPriceCents: 200}},
	}, &billScanSource{})
	if err == nil {
		t.Fatal("expected negative bill-level discount to be rejected")
	}
}

func ptrInt64(v int64) *int64 { return &v }

func TestResolveDraftCategoriesNewTaxonomyAliases(t *testing.T) {
	svc, _, _, _, catStore := newTestBillService(t, func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
		return domain.BillDraft{MarketName: "M"}, nil
	})
	catStore.list = []domain.Category{
		{ID: 1, Name: "Fuel & Gasoline", Kind: "product"},
		{ID: 2, Name: "Car Oils & Fluids", Kind: "product"},
		{ID: 3, Name: "Oils & Vinegars", Kind: "product"},
	}
	ctx := context.Background()

	draft := domain.BillDraft{Items: []domain.BillItemDraft{
		{Name: "Super E10", CategoryName: "Petrol"},
		{Name: "Motor oil 5W30", CategoryName: "Motor Oil"},
		{Name: "Olive oil", CategoryName: "oil"}, // cooking oil: 'oil' is NOT a car alias
		{Name: "Mystery line", CategoryName: "Unmapped Thing"},
	}}
	if err := svc.resolveDraftCategories(ctx, &draft); err != nil {
		t.Fatalf("resolveDraftCategories: %v", err)
	}
	if got := draft.Items[0].CategoryID; got == nil || *got != 1 {
		t.Fatalf("expected alias 'Petrol' to resolve to Fuel & Gasoline (1), got %v", got)
	}
	if got := draft.Items[1].CategoryID; got == nil || *got != 2 {
		t.Fatalf("expected alias 'Motor Oil' to resolve to Car Oils & Fluids (2), got %v", got)
	}
	if draft.Items[2].CategoryID != nil {
		t.Fatalf("expected bare 'oil' to stay unmatched (cooking oil), got %v", *draft.Items[2].CategoryID)
	}
	if draft.Items[3].CategoryID != nil {
		t.Fatalf("expected unknown category to stay unmatched, got %v", *draft.Items[3].CategoryID)
	}
}

// --- currency handling --------------------------------------------------------

// fakeTxStore is an in-memory TransactionStore for confirm/sync tests.
type fakeTxStore struct {
	mu    sync.Mutex
	items map[int64]domain.Transaction
	next  int64
}

func newFakeTxStore() *fakeTxStore {
	return &fakeTxStore{items: map[int64]domain.Transaction{}}
}

func (f *fakeTxStore) Create(_ context.Context, t domain.Transaction) (domain.Transaction, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.next == 0 {
		f.next = 1
	}
	t.ID = f.next
	f.next++
	f.items[t.ID] = t
	return t, nil
}

// CreateWithItems mirrors Create — item lines ride on the transaction in the
// fake (the real store keeps them in a separate table).
func (f *fakeTxStore) CreateWithItems(_ context.Context, t domain.Transaction, items []domain.TransactionItem) (domain.Transaction, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.next == 0 {
		f.next = 1
	}
	t.ID = f.next
	f.next++
	for i := range items {
		items[i].TransactionID = t.ID
	}
	t.Items = items
	f.items[t.ID] = t
	return t, nil
}

// UpdateWithItems mirrors Update — item lines ride on the transaction.
func (f *fakeTxStore) UpdateWithItems(_ context.Context, t domain.Transaction, items []domain.TransactionItem) (domain.Transaction, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.items[t.ID]; !ok {
		return domain.Transaction{}, domain.ErrNotFound
	}
	for i := range items {
		items[i].TransactionID = t.ID
	}
	t.Items = items
	f.items[t.ID] = t
	return t, nil
}

func (f *fakeTxStore) GetByID(_ context.Context, id int64) (domain.Transaction, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.items[id]
	if !ok {
		return domain.Transaction{}, domain.ErrNotFound
	}
	return t, nil
}

func (f *fakeTxStore) Update(_ context.Context, t domain.Transaction) (domain.Transaction, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.items[t.ID]; !ok {
		return domain.Transaction{}, domain.ErrNotFound
	}
	f.items[t.ID] = t
	return t, nil
}

func (f *fakeTxStore) Delete(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.items[id]; !ok {
		return domain.ErrNotFound
	}
	delete(f.items, id)
	return nil
}

func (f *fakeTxStore) List(context.Context, domain.TransactionFilters) ([]domain.Transaction, error) {
	return nil, nil
}

// newTestBillServiceCustom wires a BillService with a seeded base currency,
// rate source, account store and transaction store (nil = default empty).
func newTestBillServiceCustom(t *testing.T, settingsData map[string]string, rates RateSource, accounts AccountStore, txs TransactionStore, extractFn func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error)) (*BillService, *fakeBillScanStore, *fakeBillStore, *fakeSettingsStore) {
	t.Helper()
	scanStore := newFakeBillScanStore()
	billStore := &fakeBillStore{}
	storeStore := newFakeStoreStore()
	extractor := &fakeBillExtractor{fn: extractFn}
	store := &fakeSettingsStore{data: settingsData}
	if store.data == nil {
		store.data = map[string]string{}
	}
	store.data[settingsKeyAIProviders] = testProviderJSON()
	settings := NewSettingsService(store, passthroughBox{}, extractor)
	if accounts == nil {
		accounts = newFakeAccountStore()
	}
	if txs == nil {
		txs = newFakeTxStore()
	}
	svc := NewBillService(billStore, scanStore, extractor, settings,
		nil, accounts, &fakeCategoryStore{cats: map[int64]domain.Category{}}, storeStore,
		nil, nil, nil, txs, rates, t.TempDir(), nil, 5*time.Second, nil)
	t.Cleanup(svc.Close)
	return svc, scanStore, billStore, store
}

func eurSnapshot() domain.RateSnapshot {
	return domain.RateSnapshot{
		Pivot:     "EUR",
		Date:      "2026-09-10",
		FetchedAt: time.Now().UTC(),
		Rates:     map[string]float64{"USD": 1.1},
	}
}

func TestExtractDraftFallsBackToBaseCurrency(t *testing.T) {
	svc, scanStore, _, _ := newTestBillServiceCustom(t,
		map[string]string{settingsKeyBaseCurrency: "EUR"}, nil, nil, nil,
		func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
			return domain.BillDraft{MarketName: "REWE", TotalCents: 100}, nil // no currency detected
		})

	res, err := svc.Scan(context.Background(), scanFile(testImage()), "")
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		row, err := scanStore.GetByToken(context.Background(), res.ScanToken)
		return err == nil && row.Status == domain.BillScanDone
	})
	row, _ := scanStore.GetByToken(context.Background(), res.ScanToken)
	if row.Draft.Currency != "EUR" {
		t.Fatalf("expected undetected currency to default to base EUR, got %q", row.Draft.Currency)
	}
}

func TestExtractDraftKeepsDetectedCurrency(t *testing.T) {
	svc, scanStore, _, _ := newTestBillServiceCustom(t,
		map[string]string{settingsKeyBaseCurrency: "USD"}, nil, nil, nil,
		func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
			return domain.BillDraft{MarketName: "REWE", TotalCents: 100, Currency: "EUR"}, nil
		})

	res, err := svc.Scan(context.Background(), scanFile(testImage()), "")
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		row, err := scanStore.GetByToken(context.Background(), res.ScanToken)
		return err == nil && row.Status == domain.BillScanDone
	})
	row, _ := scanStore.GetByToken(context.Background(), res.ScanToken)
	if row.Draft.Currency != "EUR" {
		t.Fatalf("expected detected EUR to survive a USD base, got %q", row.Draft.Currency)
	}
}

// TestExtractDraftUsesResolvedPrompt asserts the extraction prompt handed to
// the connector is the resolved managed prompt — here the built-in default
// (no resolver wired), with {{categories}} expanded (the empty test taxonomy
// renders the placeholder away).
func TestExtractDraftUsesResolvedPrompt(t *testing.T) {
	var gotPrompt string
	svc, scanStore, _, _ := newTestBillServiceCustom(t,
		map[string]string{settingsKeyBaseCurrency: "EUR"}, nil, nil, nil,
		func(_ context.Context, _ []domain.ReceiptFile, _ domain.AIProvider, prompt string) (domain.BillDraft, error) {
			gotPrompt = prompt
			return domain.BillDraft{MarketName: "REWE", TotalCents: 100, Currency: "EUR"}, nil
		})

	res, err := svc.Scan(context.Background(), scanFile(testImage()), "")
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		row, err := scanStore.GetByToken(context.Background(), res.ScanToken)
		return err == nil && row.Status == domain.BillScanDone
	})
	if !strings.Contains(gotPrompt, "You are a receipt-parsing engine") {
		t.Fatalf("extractor must receive the resolved extraction prompt, got %.80q", gotPrompt)
	}
	if strings.Contains(gotPrompt, "{{categories}}") {
		t.Fatalf("{{categories}} placeholder must be expanded before extraction")
	}
}

func TestBuildBillCurrency(t *testing.T) {
	svc, _, _, _ := newTestBillServiceCustom(t,
		map[string]string{settingsKeyBaseCurrency: "EUR"}, nil, nil, nil,
		func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
			return domain.BillDraft{}, errors.New("not called")
		})
	ctx := context.Background()

	// Empty defaults to the base currency, messy input normalizes.
	bill, err := svc.buildBill(ctx, domain.BillConfirmInput{MarketName: "REWE"}, &billScanSource{})
	if err != nil {
		t.Fatalf("buildBill (empty): %v", err)
	}
	if bill.Currency != "EUR" {
		t.Errorf("empty currency = %q, want base EUR", bill.Currency)
	}
	bill, err = svc.buildBill(ctx, domain.BillConfirmInput{MarketName: "REWE", Currency: " eur "}, &billScanSource{})
	if err != nil {
		t.Fatalf("buildBill (messy): %v", err)
	}
	if bill.Currency != "EUR" {
		t.Errorf(`" eur " = %q, want EUR`, bill.Currency)
	}
	for _, bad := range []string{"EU", "EURO", "12", "€"} {
		if _, err := svc.buildBill(ctx, domain.BillConfirmInput{MarketName: "REWE", Currency: bad}, &billScanSource{}); !errors.Is(err, domain.ErrValidation) {
			t.Errorf("buildBill(currency=%q) error = %v, want domain.ErrValidation", bad, err)
		}
	}
}

func TestConfirmWithoutAccountRecordsOnWallet(t *testing.T) {
	accounts := newFakeAccountStore()
	txs := newFakeTxStore()
	svc, scanStore, _, _ := newTestBillServiceCustom(t,
		map[string]string{settingsKeyBaseCurrency: "EUR"}, nil, accounts, txs,
		func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
			return domain.BillDraft{MarketName: "REWE"}, nil
		})
	ctx := context.Background()

	first := scanUntilDone(t, svc, scanStore)
	bill, err := svc.Confirm(ctx, first.ScanToken, domain.BillConfirmInput{MarketName: "REWE", Currency: "EUR"})
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	// The wallet account is created lazily: cash, base currency.
	wallets, _ := accounts.List(ctx)
	if len(wallets) != 1 {
		t.Fatalf("expected one lazily created wallet account, got %d", len(wallets))
	}
	w := wallets[0]
	if w.Name != walletAccountName || w.Type != domain.AccountCash || w.Currency != "EUR" {
		t.Fatalf("wallet account = %+v, want Wallet/cash/EUR", w)
	}
	// The bill's expense is recorded against it, in the bill's currency.
	tx := txs.items[1]
	if tx.AccountID != w.ID || tx.Currency != "EUR" || tx.AmountCents != bill.TotalCents {
		t.Fatalf("wallet transaction = %+v, want account %d EUR %d", tx, w.ID, bill.TotalCents)
	}

	// A second account-less confirm reuses the wallet, not a new account.
	secondImage := []byte("another-fake-jpeg")
	res, err := svc.Scan(ctx, scanFile(secondImage), "")
	if err != nil {
		t.Fatalf("Scan 2: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		row, err := scanStore.GetByToken(ctx, res.ScanToken)
		return err == nil && row.Status == domain.BillScanDone
	})
	if _, err := svc.Confirm(ctx, res.ScanToken, domain.BillConfirmInput{MarketName: "Netto", Currency: "EUR"}); err != nil {
		t.Fatalf("Confirm 2: %v", err)
	}
	wallets, _ = accounts.List(ctx)
	if len(wallets) != 1 {
		t.Fatalf("expected the wallet to be reused, got %d accounts", len(wallets))
	}
	if len(txs.items) != 2 {
		t.Fatalf("expected two wallet transactions, got %d", len(txs.items))
	}
}

func TestConfirmTransactionCarriesBillCurrency(t *testing.T) {
	accounts := newFakeAccountStore()
	acc, err := accounts.Create(context.Background(), domain.Account{Name: "Giro", Currency: "EUR"})
	if err != nil {
		t.Fatal(err)
	}
	txs := newFakeTxStore()
	svc, scanStore, _, _ := newTestBillServiceCustom(t,
		map[string]string{settingsKeyBaseCurrency: "EUR"}, nil, accounts, txs,
		func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
			return domain.BillDraft{
				MarketName: "REWE",
				Items:      []domain.BillItemDraft{{Name: "Milk", Quantity: 1, UnitPriceCents: 250, LineTotalCents: 250}},
			}, nil
		})
	ctx := context.Background()

	res, err := svc.Scan(ctx, scanFile(testImage()), "")
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		row, err := scanStore.GetByToken(ctx, res.ScanToken)
		return err == nil && row.Status == domain.BillScanDone
	})

	bill, err := svc.Confirm(ctx, res.ScanToken, domain.BillConfirmInput{
		MarketName: "REWE",
		Currency:   "EUR",
		AccountID:  &acc.ID,
		Items:      []domain.BillItemDraft{{Name: "Milk", Quantity: 1, UnitPriceCents: 250, LineTotalCents: 250}},
	})
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if len(txs.items) != 1 {
		t.Fatalf("expected one recorded transaction, got %d", len(txs.items))
	}
	tx := txs.items[1]
	if tx.Currency != bill.Currency || tx.Currency != "EUR" {
		t.Errorf("transaction currency = %q, want bill currency EUR", tx.Currency)
	}
	if tx.AmountCents != bill.TotalCents || tx.AmountCents != 250 {
		t.Errorf("transaction amount = %d, want bill total %d", tx.AmountCents, bill.TotalCents)
	}
	if tx.Kind != domain.TransactionExpense {
		t.Errorf("transaction kind = %q, want expense", tx.Kind)
	}
}

func TestSyncBillTransactionUpdatesCurrency(t *testing.T) {
	txs := newFakeTxStore()
	created, err := txs.Create(context.Background(), domain.Transaction{
		AccountID: 1, Kind: domain.TransactionExpense, AmountCents: 250, Currency: "USD",
	})
	if err != nil {
		t.Fatal(err)
	}
	svc, _, _, _ := newTestBillServiceCustom(t,
		map[string]string{settingsKeyBaseCurrency: "EUR"}, nil, nil, txs,
		func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
			return domain.BillDraft{}, errors.New("not called")
		})

	bill := domain.Bill{MarketName: "REWE", TotalCents: 300, Currency: "EUR"}
	if err := svc.syncBillTransaction(context.Background(), created.ID, bill, 0); err != nil {
		t.Fatalf("syncBillTransaction: %v", err)
	}
	tx, _ := txs.GetByID(context.Background(), created.ID)
	if tx.Currency != "EUR" || tx.AmountCents != 300 {
		t.Errorf("synced transaction = %+v, want EUR/300", tx)
	}
}

func TestSyncBillTransactionRepointsAccount(t *testing.T) {
	txs := newFakeTxStore()
	created, err := txs.Create(context.Background(), domain.Transaction{
		AccountID: 1, Kind: domain.TransactionExpense, AmountCents: 250, Currency: "EUR",
	})
	if err != nil {
		t.Fatal(err)
	}
	svc, _, _, _ := newTestBillServiceCustom(t,
		map[string]string{settingsKeyBaseCurrency: "EUR"}, nil, nil, txs,
		func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
			return domain.BillDraft{}, errors.New("not called")
		})

	bill := domain.Bill{MarketName: "REWE", TotalCents: 250, Currency: "EUR"}
	if err := svc.syncBillTransaction(context.Background(), created.ID, bill, 7); err != nil {
		t.Fatalf("syncBillTransaction: %v", err)
	}
	tx, _ := txs.GetByID(context.Background(), created.ID)
	if tx.AccountID != 7 {
		t.Errorf("transaction account = %d, want 7", tx.AccountID)
	}
}

func TestConfirmCashAccountForcesCashPayment(t *testing.T) {
	accounts := newFakeAccountStore()
	txs := newFakeTxStore()
	svc, scanStore, billStore, _ := newTestBillServiceCustom(t,
		map[string]string{settingsKeyBaseCurrency: "EUR"}, nil, accounts, txs,
		func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
			return domain.BillDraft{MarketName: "ALDI"}, nil
		})
	ctx := context.Background()

	res := scanUntilDone(t, svc, scanStore)
	// Wallet money, but the extracted draft claims card payment with digits.
	_, err := svc.Confirm(ctx, res.ScanToken, domain.BillConfirmInput{
		MarketName:     "ALDI",
		Currency:       "EUR",
		PaymentMethod:  "card",
		CardLastDigits: "9746",
		Items:          []domain.BillItemDraft{{Name: "Milk", Quantity: 1, UnitPriceCents: 250, LineTotalCents: 250}},
	})
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	stored := billStore.items[0]
	if stored.PaymentMethod != "cash" {
		t.Errorf("payment_method = %q, want cash (wallet money)", stored.PaymentMethod)
	}
	if stored.CardLastDigits != "" {
		t.Errorf("card_last_digits = %q, want cleared for a cash account", stored.CardLastDigits)
	}
	wallets, _ := accounts.List(ctx)
	if len(wallets) != 1 {
		t.Fatalf("expected one wallet account, got %d", len(wallets))
	}
	if txs.items[1].AccountID != wallets[0].ID {
		t.Errorf("transaction account = %d, want wallet %d", txs.items[1].AccountID, wallets[0].ID)
	}
}

func TestConfirmCardAccountKeepsCardPayment(t *testing.T) {
	accounts := newFakeAccountStore()
	card, err := accounts.Create(context.Background(), domain.Account{Name: "Card •9746", Type: domain.AccountCredit, Currency: "EUR"})
	if err != nil {
		t.Fatal(err)
	}
	txs := newFakeTxStore()
	svc, scanStore, billStore, _ := newTestBillServiceCustom(t,
		map[string]string{settingsKeyBaseCurrency: "EUR"}, nil, accounts, txs,
		func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
			return domain.BillDraft{MarketName: "ALDI"}, nil
		})
	ctx := context.Background()

	res := scanUntilDone(t, svc, scanStore)
	_, err = svc.Confirm(ctx, res.ScanToken, domain.BillConfirmInput{
		MarketName:     "ALDI",
		Currency:       "EUR",
		PaymentMethod:  "card",
		CardLastDigits: "9746",
		AccountID:      &card.ID,
		Items:          []domain.BillItemDraft{{Name: "Milk", Quantity: 1, UnitPriceCents: 250, LineTotalCents: 250}},
	})
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	stored := billStore.items[0]
	if stored.PaymentMethod != "card" || stored.CardLastDigits != "9746" {
		t.Errorf("bill = %+v, want card payment with digits kept", stored)
	}
	if txs.items[1].AccountID != card.ID {
		t.Errorf("transaction account = %d, want card account %d", txs.items[1].AccountID, card.ID)
	}
}

func TestUpdateRepointsTransactionToCashAccount(t *testing.T) {
	accounts := newFakeAccountStore()
	card, err := accounts.Create(context.Background(), domain.Account{Name: "Card •9746", Type: domain.AccountCredit, Currency: "EUR"})
	if err != nil {
		t.Fatal(err)
	}
	wallet, err := accounts.Create(context.Background(), domain.Account{Name: walletAccountName, Type: domain.AccountCash, Currency: "EUR"})
	if err != nil {
		t.Fatal(err)
	}
	txs := newFakeTxStore()
	existing, err := txs.Create(context.Background(), domain.Transaction{
		AccountID: card.ID, Kind: domain.TransactionExpense, AmountCents: 250, Currency: "EUR",
	})
	if err != nil {
		t.Fatal(err)
	}
	svc, _, billStore, _ := newTestBillServiceCustom(t,
		map[string]string{settingsKeyBaseCurrency: "EUR"}, nil, accounts, txs,
		func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
			return domain.BillDraft{}, errors.New("not called")
		})
	// A confirmed bill whose expense sits on the card account.
	confirmed, err := billStore.Create(context.Background(), domain.Bill{
		MarketName: "ALDI", Currency: "EUR", TotalCents: 250, Status: domain.BillStatusAccepted,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := billStore.SetTransaction(context.Background(), confirmed.ID, existing.ID); err != nil {
		t.Fatal(err)
	}

	// User edits the saved bill and moves it to the wallet.
	_, err = svc.Update(context.Background(), confirmed.ID, domain.BillConfirmInput{
		MarketName:     "ALDI",
		Currency:       "EUR",
		PaymentMethod:  "card",
		CardLastDigits: "9746",
		AccountID:      &wallet.ID,
		Items:          []domain.BillItemDraft{{Name: "Milk", Quantity: 1, UnitPriceCents: 250, LineTotalCents: 250}},
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	tx, _ := txs.GetByID(context.Background(), existing.ID)
	if tx.AccountID != wallet.ID {
		t.Errorf("transaction account = %d, want wallet %d", tx.AccountID, wallet.ID)
	}
	stored, _ := billStore.GetByID(context.Background(), confirmed.ID)
	if stored.PaymentMethod != "cash" || stored.CardLastDigits != "" {
		t.Errorf("updated bill = %+v, want cash payment with digits cleared", stored)
	}
}

func TestUpdateWithoutTransactionCreatesOne(t *testing.T) {
	accounts := newFakeAccountStore()
	card, err := accounts.Create(context.Background(), domain.Account{Name: "Card •9746", Type: domain.AccountCredit, Currency: "EUR"})
	if err != nil {
		t.Fatal(err)
	}
	txs := newFakeTxStore()
	svc, _, billStore, _ := newTestBillServiceCustom(t,
		map[string]string{settingsKeyBaseCurrency: "EUR"}, nil, accounts, txs,
		func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
			return domain.BillDraft{}, errors.New("not called")
		})
	// A pre-account bill: no transaction ever recorded.
	orphan, err := billStore.Create(context.Background(), domain.Bill{
		MarketName: "ALDI", Currency: "EUR", TotalCents: 250, Status: domain.BillStatusAccepted,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = svc.Update(context.Background(), orphan.ID, domain.BillConfirmInput{
		MarketName:    "ALDI",
		Currency:      "EUR",
		PaymentMethod: "card",
		AccountID:     &card.ID,
		Items:         []domain.BillItemDraft{{Name: "Milk", Quantity: 1, UnitPriceCents: 250, LineTotalCents: 250}},
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	if len(txs.items) != 1 {
		t.Fatalf("expected one created transaction, got %d", len(txs.items))
	}
	tx := txs.items[1]
	if tx.AccountID != card.ID || tx.AmountCents != 250 || tx.Kind != domain.TransactionExpense {
		t.Errorf("created transaction = %+v, want expense 250 on account %d", tx, card.ID)
	}
	stored, _ := billStore.GetByID(context.Background(), orphan.ID)
	if stored.TransactionID == nil || *stored.TransactionID != tx.ID {
		t.Errorf("bill transaction_id = %v, want link to new transaction %d", stored.TransactionID, tx.ID)
	}
}

func TestUpdateNilAccountKeepsTransactionAccount(t *testing.T) {
	accounts := newFakeAccountStore()
	card, err := accounts.Create(context.Background(), domain.Account{Name: "Card •9746", Type: domain.AccountCredit, Currency: "EUR"})
	if err != nil {
		t.Fatal(err)
	}
	txs := newFakeTxStore()
	existing, err := txs.Create(context.Background(), domain.Transaction{
		AccountID: card.ID, Kind: domain.TransactionExpense, AmountCents: 250, Currency: "EUR",
	})
	if err != nil {
		t.Fatal(err)
	}
	svc, _, billStore, _ := newTestBillServiceCustom(t,
		map[string]string{settingsKeyBaseCurrency: "EUR"}, nil, accounts, txs,
		func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
			return domain.BillDraft{}, errors.New("not called")
		})
	confirmed, err := billStore.Create(context.Background(), domain.Bill{
		MarketName: "ALDI", Currency: "EUR", TotalCents: 250, Status: domain.BillStatusAccepted,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := billStore.SetTransaction(context.Background(), confirmed.ID, existing.ID); err != nil {
		t.Fatal(err)
	}

	// No account_id in the payload — the transaction must stay on the card.
	_, err = svc.Update(context.Background(), confirmed.ID, domain.BillConfirmInput{
		MarketName: "ALDI",
		Currency:   "EUR",
		Items:      []domain.BillItemDraft{{Name: "Milk", Quantity: 1, UnitPriceCents: 300, LineTotalCents: 300}},
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	tx, _ := txs.GetByID(context.Background(), existing.ID)
	if tx.AccountID != card.ID {
		t.Errorf("transaction account = %d, want unchanged card %d", tx.AccountID, card.ID)
	}
	if tx.AmountCents != 300 {
		t.Errorf("transaction amount = %d, want synced 300", tx.AmountCents)
	}
}

func TestStatsMergesCurrenciesIntoBase(t *testing.T) {
	svc, _, billStore, _ := newTestBillServiceCustom(t,
		map[string]string{settingsKeyBaseCurrency: "EUR"},
		stubRateSource{snap: eurSnapshot()}, nil, nil,
		func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
			return domain.BillDraft{}, errors.New("not called")
		})
	billStore.stats = []domain.BillStatsRow{
		{Label: "REWE", Currency: "EUR", BillCount: 1, TotalCents: 1_000},
		{Label: "REWE", Currency: "USD", BillCount: 2, Quantity: 3, TotalCents: 1_100}, // → 1_000 EUR
		{Label: "ALDI", Currency: "JPY", BillCount: 1, TotalCents: 500},                // no rate → 1:1
	}

	stats, err := svc.Stats(context.Background(), BillStatsQuery{GroupBy: "market"})
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Currency != "EUR" {
		t.Errorf("Currency = %q, want EUR", stats.Currency)
	}
	if len(stats.Rows) != 2 || stats.Rows[0].Label != "REWE" {
		t.Fatalf("rows = %+v, want REWE first of two", stats.Rows)
	}
	rewe := stats.Rows[0]
	if rewe.TotalCents != 2_000 || rewe.BillCount != 3 || rewe.Quantity != 3 {
		t.Errorf("REWE row = %+v, want total 2000 count 3 qty 3", rewe)
	}
	if stats.Rows[0].Currency != "" || stats.Rows[1].Currency != "" {
		t.Errorf("per-row currency must be zeroed before responding, got %q/%q",
			stats.Rows[0].Currency, stats.Rows[1].Currency)
	}
	if len(stats.ConversionWarnings) != 1 || stats.ConversionWarnings[0] != "JPY" {
		t.Errorf("ConversionWarnings = %v, want [JPY]", stats.ConversionWarnings)
	}
}

func TestStatsQueryValidation(t *testing.T) {
	svc, _, _, _ := newTestBillServiceCustom(t,
		map[string]string{settingsKeyBaseCurrency: "EUR"}, nil, nil, nil,
		func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
			return domain.BillDraft{}, nil
		})
	ctx := context.Background()

	cases := []struct {
		name string
		q    BillStatsQuery
	}{
		{"month and range are exclusive", BillStatsQuery{GroupBy: "market", Month: "2026-09", From: "2026-09-01"}},
		{"from without to", BillStatsQuery{GroupBy: "market", From: "2026-09-01"}},
		{"to without from", BillStatsQuery{GroupBy: "market", To: "2026-09-30"}},
		{"to before from", BillStatsQuery{GroupBy: "market", From: "2026-09-30", To: "2026-09-01"}},
		{"bad range date", BillStatsQuery{GroupBy: "market", From: "2026-02-31", To: "2026-03-01"}},
		{"bad group_by", BillStatsQuery{GroupBy: "quarter", From: "2026-09-01", To: "2026-09-30"}},
	}
	for _, tc := range cases {
		if _, err := svc.Stats(ctx, tc.q); err == nil {
			t.Errorf("%s: expected validation error, got nil", tc.name)
		}
	}
}

func TestDeleteBillRemovesTransactionAndImage(t *testing.T) {
	accounts := newFakeAccountStore()
	txs := newFakeTxStore()
	svc, _, billStore, _ := newTestBillServiceCustom(t,
		map[string]string{settingsKeyBaseCurrency: "EUR"}, nil, accounts, txs,
		func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
			return domain.BillDraft{}, nil
		})
	ctx := context.Background()

	created, err := txs.Create(ctx, domain.Transaction{Description: "ALDI", Currency: "EUR"})
	if err != nil {
		t.Fatal(err)
	}
	receipt := filepath.Join(t.TempDir(), "receipt.jpg")
	if err := os.WriteFile(receipt, []byte("fake jpeg"), 0o644); err != nil {
		t.Fatal(err)
	}
	billStore.items = []domain.Bill{{
		ID: 7, MarketName: "ALDI", Currency: "EUR", Status: domain.BillStatusAccepted,
		TransactionID: &created.ID, ImagePath: receipt,
	}}

	if err := svc.Delete(ctx, 7); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(billStore.items) != 0 {
		t.Errorf("billStore.items = %+v, want empty", billStore.items)
	}
	if _, err := txs.GetByID(ctx, created.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("linked transaction still present: %v", err)
	}
	if _, err := os.Stat(receipt); !os.IsNotExist(err) {
		t.Errorf("receipt file still present (stat err = %v)", err)
	}
}

func TestDeleteMissingBillFails(t *testing.T) {
	svc, _, _, _ := newTestBillServiceCustom(t, nil, nil, nil, nil,
		func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
			return domain.BillDraft{}, nil
		})
	if err := svc.Delete(context.Background(), 999); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Delete of unknown bill = %v, want ErrNotFound", err)
	}
}

func TestDeleteBillToleratesVanishedTransaction(t *testing.T) {
	accounts := newFakeAccountStore()
	txs := newFakeTxStore()
	svc, _, billStore, _ := newTestBillServiceCustom(t,
		map[string]string{settingsKeyBaseCurrency: "EUR"}, nil, accounts, txs,
		func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
			return domain.BillDraft{}, nil
		})
	ctx := context.Background()

	// Linked to a transaction that was already deleted by hand.
	txID := int64(42)
	billStore.items = []domain.Bill{{
		ID: 3, MarketName: "ALDI", Currency: "EUR", Status: domain.BillStatusAccepted,
		TransactionID: &txID,
	}}

	if err := svc.Delete(ctx, 3); err != nil {
		t.Fatalf("Delete with vanished transaction: %v", err)
	}
	if len(billStore.items) != 0 {
		t.Errorf("billStore.items = %+v, want empty", billStore.items)
	}
}

// confirmDraft scans a receipt, waits for the draft and confirms it — the
// common scaffolding of the product-linking tests. image is caller-chosen:
// identical bytes are rejected as duplicate receipts.
func confirmDraft(t *testing.T, svc *BillService, scanStore *fakeBillScanStore, image []byte, in domain.BillConfirmInput) domain.Bill {
	t.Helper()
	ctx := context.Background()
	res, err := svc.Scan(ctx, scanFile(image), "")
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		row, err := scanStore.GetByToken(ctx, res.ScanToken)
		return err == nil && row.Status == domain.BillScanDone
	})
	bill, err := svc.Confirm(ctx, res.ScanToken, in)
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	return bill
}

func TestConfirmFindOrCreatesProducts(t *testing.T) {
	draft := domain.BillDraft{
		MarketName: "ALDI", Currency: "USD",
		Items: []domain.BillItemDraft{
			{Name: "Milk", Brand: "Weihenstephan", Unit: "l", Quantity: 1, UnitPriceCents: 200, LineTotalCents: 200},
			{Name: "MILK", Quantity: 2, UnitPriceCents: 200, LineTotalCents: 400},     // same product, different casing
			{Name: "Leergut", Quantity: 8, UnitPriceCents: -25, LineTotalCents: -200}, // deposit return: no product
		},
	}
	svc, scanStore, billStore, _, catStore, products := newTestBillServiceWithProducts(t,
		func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
			return draft, nil
		})

	confirmDraft(t, svc, scanStore, []byte("receipt-1"), domain.BillConfirmInput{MarketName: "ALDI", Currency: "USD", Items: draft.Items})
	if len(billStore.items) != 1 {
		t.Fatalf("expected one persisted bill, got %d", len(billStore.items))
	}

	if len(products.items) != 1 {
		t.Fatalf("expected one product from two same-named lines, got %d: %+v", len(products.items), products.items)
	}
	milk, err := products.FindByName(context.Background(), "milk")
	if err != nil {
		t.Fatalf("product not findable case-insensitively: %v", err)
	}
	if milk.Name != "Milk" || milk.Brand != "Weihenstephan" || milk.Unit != "l" {
		t.Fatalf("product not seeded from the first line: %+v", milk)
	}

	// Both non-return lines are linked; the Leergut line is not.
	bill := billStore.items[0]
	for _, it := range bill.Items {
		if it.IsReturn {
			if it.ProductID != nil {
				t.Errorf("return line %q must not link to a product", it.Name)
			}
			continue
		}
		if it.ProductID == nil || *it.ProductID != milk.ID {
			t.Errorf("line %q not linked to its product: %v", it.Name, it.ProductID)
		}
	}

	// A second confirm of the same names reuses the product (no duplicates).
	confirmDraft(t, svc, scanStore, []byte("receipt-2"), domain.BillConfirmInput{MarketName: "ALDI", Currency: "USD", Items: draft.Items})
	if len(products.items) != 1 {
		t.Fatalf("expected product reuse across confirms, got %d products", len(products.items))
	}

	// The item's category, when set, seeds the product's category.
	catStore.cats = map[int64]domain.Category{7: {ID: 7, Name: "Fruits", Kind: "product"}}
	cid := int64(7)
	confirmDraft(t, svc, scanStore, []byte("receipt-3"), domain.BillConfirmInput{MarketName: "ALDI", Currency: "USD",
		Items: []domain.BillItemDraft{{Name: "Banana", CategoryID: &cid, Quantity: 1, UnitPriceCents: 99, LineTotalCents: 99}}})
	banana, err := products.FindByName(context.Background(), "Banana")
	if err != nil {
		t.Fatalf("Banana product missing: %v", err)
	}
	if banana.CategoryID == nil || *banana.CategoryID != 7 {
		t.Fatalf("product category not seeded from the item: %+v", banana)
	}
}

// fakeProductMappingStore is an in-memory ProductMappingStore mirroring the
// SQLite semantics: raw_name is unique case-insensitively (Create reports
// domain.ErrConflict on a duplicate, Upsert overwrites), and the single job
// row starts idle like the migration-seeded database. products feeds the
// unmapped-name queries of the backfill job; creates/upserts/lookups record
// every call for assertions; findErr/createErr/upsertErr inject store
// failures so the tolerance paths can be exercised.
type fakeProductMappingStore struct {
	mu       sync.Mutex
	items    map[string]domain.ProductNameMapping // keyed by lower(raw_name)
	next     int64
	products []domain.Product
	job      domain.ProductNormalizationJob

	creates   []domain.ProductNameMapping
	upserts   []domain.ProductNameMapping
	links     [][2]any // raw, product id
	lookups   []string
	findErr   error
	createErr error
	upsertErr error
}

func newFakeProductMappingStore() *fakeProductMappingStore {
	return &fakeProductMappingStore{
		items: map[string]domain.ProductNameMapping{},
		job:   domain.ProductNormalizationJob{Status: domain.ProductNormalizationIdle},
	}
}

// seedMapping stores a mapping decision directly (bypasses the call records).
func (f *fakeProductMappingStore) seedMapping(m domain.ProductNameMapping) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	m.ID = f.next
	f.items[strings.ToLower(m.RawName)] = m
}

func (f *fakeProductMappingStore) FindByRawName(_ context.Context, raw string) (domain.ProductNameMapping, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookups = append(f.lookups, raw)
	if f.findErr != nil {
		return domain.ProductNameMapping{}, f.findErr
	}
	m, ok := f.items[strings.ToLower(raw)]
	if !ok {
		return domain.ProductNameMapping{}, domain.ErrNotFound
	}
	return m, nil
}

func (f *fakeProductMappingStore) Create(_ context.Context, m domain.ProductNameMapping) (domain.ProductNameMapping, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.creates = append(f.creates, m)
	if f.createErr != nil {
		return domain.ProductNameMapping{}, f.createErr
	}
	key := strings.ToLower(m.RawName)
	if _, ok := f.items[key]; ok {
		return domain.ProductNameMapping{}, fmt.Errorf("create product mapping: unique constraint failed: %w", domain.ErrConflict)
	}
	f.next++
	m.ID = f.next
	f.items[key] = m
	return m, nil
}

func (f *fakeProductMappingStore) Upsert(_ context.Context, m domain.ProductNameMapping) (domain.ProductNameMapping, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upserts = append(f.upserts, m)
	if f.upsertErr != nil {
		return domain.ProductNameMapping{}, f.upsertErr
	}
	key := strings.ToLower(m.RawName)
	if old, ok := f.items[key]; ok {
		// Mirror the repository's COALESCE: a payload without a product id
		// keeps the stored link.
		if m.ProductID == nil {
			m.ProductID = old.ProductID
		}
	} else {
		f.next++
		m.ID = f.next
	}
	m.UpdatedAt = time.Now().UTC()
	f.items[key] = m
	return m, nil
}

// LinkProduct records the call and mirrors the update's guard (only a
// missing or stale link is written).
func (f *fakeProductMappingStore) LinkProduct(_ context.Context, raw string, productID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.links = append(f.links, [2]any{raw, productID})
	m, ok := f.items[strings.ToLower(raw)]
	if !ok {
		return nil // nothing to link — non-fatal by contract
	}
	if m.ProductID == nil || *m.ProductID != productID {
		id := productID
		m.ProductID = &id
		f.items[strings.ToLower(raw)] = m
	}
	return nil
}

// UnmappedProductNames lists the seeded products whose name has no mapping
// yet — or whose mapping has no generic name — in seed order, mirroring the
// repository's id-ordered query.
func (f *fakeProductMappingStore) UnmappedProductNames(_ context.Context, limit int) ([]domain.Product, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []domain.Product{}
	for _, p := range f.products {
		if m, ok := f.items[strings.ToLower(p.Name)]; ok && m.GenericName != "" {
			continue
		}
		if len(out) == limit {
			break
		}
		out = append(out, p)
	}
	return out, nil
}

func (f *fakeProductMappingStore) CountUnmappedProductNames(context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	for _, p := range f.products {
		if m, ok := f.items[strings.ToLower(p.Name)]; ok && m.GenericName != "" {
			continue
		}
		n++
	}
	return n, nil
}

func (f *fakeProductMappingStore) GetJob(context.Context) (domain.ProductNormalizationJob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.job, nil
}

func (f *fakeProductMappingStore) UpdateJob(_ context.Context, j domain.ProductNormalizationJob) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.job = j
	return nil
}

// --- product-name normalization memory ----------------------------------------

// newTestBillServiceWithMappings wires a BillService with the normalization
// memory attached, for mapping behavior exercised through the scan pipeline.
func newTestBillServiceWithMappings(t *testing.T, mappings ProductMappingStore, extractFn func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error)) (*BillService, *fakeBillScanStore) {
	t.Helper()
	scanStore := newFakeBillScanStore()
	extractor := &fakeBillExtractor{fn: extractFn}
	settings := NewSettingsService(
		&fakeSettingsStore{data: map[string]string{settingsKeyAIProviders: testProviderJSON()}},
		passthroughBox{}, extractor)
	svc := NewBillService(&fakeBillStore{}, scanStore, extractor, settings,
		nil, newFakeAccountStore(), &fakeCategoryStore{cats: map[int64]domain.Category{}},
		newFakeStoreStore(), newFakeProductStore(), mappings, nil, newFakeTxStore(), nil,
		t.TempDir(), nil, 5*time.Second, nil)
	t.Cleanup(svc.Close)
	return svc, scanStore
}

// TestScanAppliesMappingMemoryToDraft drives the memory through the public
// scan pipeline: a mapped raw text keeps the remembered standard name,
// generic name and category (memory wins over the fresh AI suggestion), an
// unmapped raw text records the AI's suggestion with source 'ai', a mapped
// raw text whose remembered generic name is empty keeps the fresh family
// suggestion (the backfill job fills the gap later), and deposit-return
// lines are skipped entirely.
func TestScanAppliesMappingMemoryToDraft(t *testing.T) {
	mappings := newFakeProductMappingStore()
	mappings.seedMapping(domain.ProductNameMapping{
		RawName: "WHL MLK 1L", StandardName: "Milk 1L", GenericName: "Fresh Milk",
		CategoryID: ptrInt64(7), Source: domain.MappingSourceUser,
	})
	// Mapped but with no remembered family: the fresh AI suggestion stays.
	mappings.seedMapping(domain.ProductNameMapping{
		RawName: "OATS", StandardName: "Oats",
		CategoryID: ptrInt64(9), Source: domain.MappingSourceAI,
	})
	draft := domain.BillDraft{
		MarketName: "REWE", Currency: "EUR",
		Items: []domain.BillItemDraft{
			// Already mapped: the memory's decision replaces the suggestion.
			{Name: " WHL MLK 1L ", StandardName: "Whole Milk 1L", GenericName: "Milk", Quantity: 1, UnitPriceCents: 189, LineTotalCents: 189},
			// First sight: the AI suggestion is kept and recorded as 'ai'.
			{Name: "TOMATOS", StandardName: "Tomatoes", GenericName: "Tomatoes", Quantity: 1, UnitPriceCents: 99, LineTotalCents: 99},
			// Mapped with an empty generic name: the fresh family suggestion
			// survives (memory wins only on non-empty values).
			{Name: "OATS", StandardName: "Oat Flakes", GenericName: "Oat Flakes", Quantity: 1, UnitPriceCents: 49, LineTotalCents: 49},
			// Return line: never touches the memory.
			{Name: "LEERGUT 0.25", StandardName: "Leergut 0.25", IsReturn: true, Quantity: 1, UnitPriceCents: -25, LineTotalCents: -25},
		},
	}
	svc, scanStore := newTestBillServiceWithMappings(t, mappings,
		func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
			return draft, nil
		})
	ctx := context.Background()

	res, err := svc.Scan(ctx, scanFile(testImage()), "")
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		row, err := scanStore.GetByToken(ctx, res.ScanToken)
		return err == nil && row.Status == domain.BillScanDone
	})
	row, _ := scanStore.GetByToken(ctx, res.ScanToken)

	items := row.Draft.Items
	if items[0].StandardName != "Milk 1L" {
		t.Errorf("mapped raw: standard name = %q, want the memory's %q", items[0].StandardName, "Milk 1L")
	}
	if items[0].GenericName != "Fresh Milk" {
		t.Errorf("mapped raw: generic name = %q, want the memory's %q", items[0].GenericName, "Fresh Milk")
	}
	if items[0].CategoryID == nil || *items[0].CategoryID != 7 {
		t.Errorf("mapped raw: category = %v, want the memory's 7", items[0].CategoryID)
	}
	if items[1].StandardName != "Tomatoes" {
		t.Errorf("unmapped raw: standard name = %q, want the AI suggestion kept", items[1].StandardName)
	}
	if items[2].StandardName != "Oats" {
		t.Errorf("mapped raw without family: standard name = %q, want the memory's %q", items[2].StandardName, "Oats")
	}
	if items[2].GenericName != "Oat Flakes" {
		t.Errorf("mapped raw without family: generic name = %q, want the fresh suggestion %q kept", items[2].GenericName, "Oat Flakes")
	}
	if items[2].CategoryID == nil || *items[2].CategoryID != 9 {
		t.Errorf("mapped raw without family: category = %v, want the memory's 9", items[2].CategoryID)
	}

	mappings.mu.Lock()
	defer mappings.mu.Unlock()
	if len(mappings.creates) != 1 {
		t.Fatalf("creates = %+v, want exactly the TOMATOS suggestion recorded", mappings.creates)
	}
	created := mappings.creates[0]
	if created.RawName != "TOMATOS" || created.StandardName != "Tomatoes" || created.GenericName != "Tomatoes" || created.Source != domain.MappingSourceAI {
		t.Fatalf("recorded mapping = %+v, want TOMATOS → Tomatoes (ai, family Tomatoes)", created)
	}
	for _, looked := range mappings.lookups {
		if strings.Contains(strings.ToLower(looked), "leergut") {
			t.Errorf("return line %q must not be looked up in the memory", looked)
		}
	}
}

// TestApplyMappingMemoryFallbacksAndTolerance covers the unexported method
// directly: a draft line without a suggestion falls back to its raw name, and
// store failures (duplicate Create from a concurrent confirm, a locked db on
// lookup or write) are logged and non-fatal.
func TestApplyMappingMemoryFallbacksAndTolerance(t *testing.T) {
	ctx := context.Background()

	// No AI suggestion → the trimmed raw name becomes the standard name and
	// is recorded with source 'ai'.
	mappings := newFakeProductMappingStore()
	svc := &BillService{mappings: mappings, log: slog.Default()}
	draft := domain.BillDraft{Items: []domain.BillItemDraft{
		{Name: "  BANANE  ", StandardName: ""},
	}}
	svc.applyMappingMemory(ctx, &draft)
	if draft.Items[0].StandardName != "BANANE" {
		t.Fatalf("standard name = %q, want raw fallback %q", draft.Items[0].StandardName, "BANANE")
	}
	mappings.mu.Lock()
	if len(mappings.creates) != 1 || mappings.creates[0].StandardName != "BANANE" ||
		mappings.creates[0].Source != domain.MappingSourceAI {
		t.Fatalf("creates = %+v, want the BANANE identity recorded as 'ai'", mappings.creates)
	}
	mappings.mu.Unlock()

	// A concurrent Create (ErrConflict) and a hard store failure on Create
	// are both tolerated — the draft keeps its suggestion either way.
	mappings.createErr = fmt.Errorf("create product mapping: unique constraint failed: %w", domain.ErrConflict)
	draft = domain.BillDraft{Items: []domain.BillItemDraft{
		{Name: "TOMATOS", StandardName: "Tomatoes"},
	}}
	svc.applyMappingMemory(ctx, &draft)
	if draft.Items[0].StandardName != "Tomatoes" {
		t.Fatalf("standard name after Create conflict = %q, want the suggestion kept", draft.Items[0].StandardName)
	}

	mappings.createErr = errors.New("database is locked")
	draft = domain.BillDraft{Items: []domain.BillItemDraft{
		{Name: "TOMATOS", StandardName: "Tomatoes"},
	}}
	svc.applyMappingMemory(ctx, &draft)
	if draft.Items[0].StandardName != "Tomatoes" {
		t.Fatalf("standard name after Create failure = %q, want the suggestion kept", draft.Items[0].StandardName)
	}

	// A lookup failure is non-fatal too: the suggestion stands, nothing is
	// recorded.
	mappings.createErr = nil
	mappings.findErr = errors.New("database is locked")
	draft = domain.BillDraft{Items: []domain.BillItemDraft{
		{Name: "TOMATOS", StandardName: "Tomatoes"},
	}}
	svc.applyMappingMemory(ctx, &draft)
	if draft.Items[0].StandardName != "Tomatoes" {
		t.Fatalf("standard name after lookup failure = %q, want the suggestion kept", draft.Items[0].StandardName)
	}
}

// TestLearnMappingOverrides records the review corrections of a confirmed
// bill: a changed standard name, a changed generic name, a changed category,
// and a manually added line are upserted with source 'user' (an added line
// without a suggestion falls back to its raw name); unchanged lines never
// write; clearing the generic name is a real opinion and is learned as "";
// return lines are skipped; a failing Upsert never fails the bill.
func TestLearnMappingOverrides(t *testing.T) {
	ctx := context.Background()
	mappings := newFakeProductMappingStore()
	svc := &BillService{mappings: mappings, log: slog.Default()}

	prior := []priorStandardLine{
		{raw: "WHL MLK 1L", standard: "Whole Milk 1L", generic: "Whole Milk Family", categoryID: nil},
		{raw: "TOMATOS", standard: "Tomatoes", generic: "Tomato Family", categoryID: nil},
		{raw: "OATS", standard: "Oats", generic: "Oat Flakes", categoryID: nil},
		{raw: "POTATO MINIONS 450G", standard: "Potato Minions 450g", generic: "Frozen Potato Shapes", categoryID: nil},
		{raw: "BANANE", standard: "Bananas", generic: "Bananas", categoryID: nil},
		{raw: "LEERGUT PALETTE", standard: "Leergut Palette", categoryID: nil},
	}
	items := []domain.BillItemDraft{
		// Standard and generic names corrected by the user.
		{Name: "WHL MLK 1L", StandardName: "Milk 1L", GenericName: "Fresh Milk", CategoryID: ptrInt64(7), Quantity: 1, UnitPriceCents: 189, LineTotalCents: 189},
		// Unchanged line (case-insensitive on both names) — must not write.
		{Name: "TOMATOS", StandardName: "tomatoes", GenericName: "tomato family", Quantity: 1, UnitPriceCents: 99, LineTotalCents: 99},
		// Category-only correction — the unchanged generic rides along.
		{Name: "OATS", StandardName: "Oats", GenericName: "Oat Flakes", CategoryID: ptrInt64(8), Quantity: 1, UnitPriceCents: 49, LineTotalCents: 49},
		// Generic-only correction.
		{Name: "POTATO MINIONS 450G", StandardName: "Potato Minions 450g", GenericName: "Frozen Shaped Potatoes", Quantity: 1, UnitPriceCents: 349, LineTotalCents: 349},
		// Cleared generic name — a real "no family" opinion, learned as "".
		{Name: "BANANE", StandardName: "Bananas", GenericName: "", Quantity: 1, UnitPriceCents: 119, LineTotalCents: 119},
		// Manually added line with a submitted standard name.
		{Name: "Butter 250g", StandardName: "Butter", Quantity: 1, UnitPriceCents: 289, LineTotalCents: 289},
		// Manually added line without a suggestion — raw name fallback.
		{Name: "  Eggs  ", StandardName: "", Quantity: 1, UnitPriceCents: 259, LineTotalCents: 259},
		// Return line with a "changed" name — skipped.
		{Name: "LEERGUT PALETTE", StandardName: "Crate Return", IsReturn: true, Quantity: 1, UnitPriceCents: -300, LineTotalCents: -300},
	}
	svc.learnMappingOverrides(ctx, items, prior)

	mappings.mu.Lock()
	defer mappings.mu.Unlock()
	if len(mappings.upserts) != 6 {
		t.Fatalf("upserts = %+v, want exactly the 6 corrected/added lines", mappings.upserts)
	}
	want := map[string]domain.ProductNameMapping{
		"WHL MLK 1L":          {RawName: "WHL MLK 1L", StandardName: "Milk 1L", GenericName: "Fresh Milk", CategoryID: ptrInt64(7)},
		"OATS":                {RawName: "OATS", StandardName: "Oats", GenericName: "Oat Flakes", CategoryID: ptrInt64(8)},
		"POTATO MINIONS 450G": {RawName: "POTATO MINIONS 450G", StandardName: "Potato Minions 450g", GenericName: "Frozen Shaped Potatoes"},
		"BANANE":              {RawName: "BANANE", StandardName: "Bananas"},
		"Butter 250g":         {RawName: "Butter 250g", StandardName: "Butter"},
		"Eggs":                {RawName: "Eggs", StandardName: "Eggs"},
	}
	for _, up := range mappings.upserts {
		if up.Source != domain.MappingSourceUser {
			t.Errorf("upsert %+v: source = %q, want 'user'", up, up.Source)
		}
		w, ok := want[up.RawName]
		if !ok {
			t.Errorf("unexpected upsert for raw %q", up.RawName)
			continue
		}
		if up.StandardName != w.StandardName {
			t.Errorf("raw %q: standard = %q, want %q", up.RawName, up.StandardName, w.StandardName)
		}
		if up.GenericName != w.GenericName {
			t.Errorf("raw %q: generic = %q, want %q", up.RawName, up.GenericName, w.GenericName)
		}
		if !sameInt64Ptr(up.CategoryID, w.CategoryID) {
			t.Errorf("raw %q: category = %v, want %v", up.RawName, up.CategoryID, w.CategoryID)
		}
	}

	// A failing Upsert is tolerated (logged, never fails the bill).
	mappings.upsertErr = errors.New("database is locked")
	mappings.mu.Unlock()
	svc.learnMappingOverrides(ctx, items, prior)
	mappings.mu.Lock()
	mappings.upsertErr = nil
}

func TestConfirmProductRaceReFindsWinner(t *testing.T) {
	svc, scanStore, _, _, _, products := newTestBillServiceWithProducts(t,
		func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
			return domain.BillDraft{
				MarketName: "ALDI", Currency: "USD",
				Items: []domain.BillItemDraft{{Name: "Milk", Quantity: 1, UnitPriceCents: 200, LineTotalCents: 200}},
			}, nil
		})

	// The first Create of this confirm loses the (simulated) race against a
	// concurrent confirm — resolveProduct must re-read the winner instead of
	// leaving the line unlinked.
	products.failConflict = true
	bill := confirmDraft(t, svc, scanStore, []byte("receipt-race"), domain.BillConfirmInput{MarketName: "ALDI", Currency: "USD",
		Items: []domain.BillItemDraft{{Name: "Milk", Quantity: 1, UnitPriceCents: 200, LineTotalCents: 200}}})

	if len(products.items) != 1 {
		t.Fatalf("expected exactly one product after the race, got %d", len(products.items))
	}
	var productID int64
	for _, p := range products.items {
		productID = p.ID
	}
	if bill.Items[0].ProductID == nil || *bill.Items[0].ProductID != productID {
		t.Fatalf("line not linked to the raced product: %v", bill.Items[0].ProductID)
	}
}

// floatPtr is a small helper for building *float64 magnitudes in tests.
func floatPtr(v float64) *float64 { return &v }

// TestConfirmUnitValueLearnsOnConfirm covers the printed size magnitude in the
// confirm flow: lines carry it, new products are seeded with it, an existing
// product's decided magnitude is never overwritten, a NULL one is learned, and
// the raw text's mapping gains the convenience product link.
func TestConfirmUnitValueLearnsAndCarries(t *testing.T) {
	// A product with a decided magnitude and one with none yet — the draft
	// reprints both (NOCASE) plus one brand-new line.
	half := 0.5
	draft := domain.BillDraft{
		MarketName: "ALDI", Currency: "USD",
		Items: []domain.BillItemDraft{
			{Name: "WATER 500ML", Unit: "ml", UnitValue: floatPtr(1500), Quantity: 1, UnitPriceCents: 45, LineTotalCents: 45}, // decided 0.5 stays
			{Name: "BREAD", Unit: "pcs", UnitValue: nil, Quantity: 1, UnitPriceCents: 150, LineTotalCents: 150},               // NULL → learns
			{Name: "COLA ZERO 1.5L", Unit: "l", UnitValue: floatPtr(1.5), Quantity: 1, UnitPriceCents: 210, LineTotalCents: 210},
		},
	}
	svc, scanStore, _, _, _, products := newTestBillServiceWithProducts(t,
		func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
			return draft, nil
		})
	products.insert(domain.Product{Name: "Water 500ml", Unit: "ml", UnitValue: &half})
	products.insert(domain.Product{Name: "Bread", Unit: "pcs"})

	bill := confirmDraft(t, svc, scanStore, []byte("receipt-1"), domain.BillConfirmInput{
		MarketName: "ALDI", Currency: "USD", Items: draft.Items,
	})

	// Lines keep the magnitudes they were confirmed with.
	if got := bill.Items[0].UnitValue; got == nil || *got != 1500 {
		t.Errorf("water line unit_value = %v; want 1500", got)
	}
	if got := bill.Items[2].UnitValue; got == nil || *got != 1.5 {
		t.Errorf("cola line unit_value = %v; want 1.5", got)
	}

	water, err := products.FindByName(context.Background(), "water 500ml")
	if err != nil {
		t.Fatalf("water product missing: %v", err)
	}
	if water.UnitValue == nil || *water.UnitValue != 0.5 {
		t.Errorf("decided magnitude overwritten: %v; want 0.5", water.UnitValue)
	}
	bread, err := products.FindByName(context.Background(), "bread")
	if err != nil {
		t.Fatalf("bread product missing: %v", err)
	}
	if bread.UnitValue != nil {
		t.Errorf("unneeded learn on a NULL-capable product: got %v; want NULL", bread.UnitValue)
	}
	cola, err := products.FindByName(context.Background(), "cola zero 1.5l")
	if err != nil {
		t.Fatalf("cola product missing: %v", err)
	}
	if cola.UnitValue == nil || *cola.UnitValue != 1.5 {
		t.Errorf("new product not seeded with the magnitude: %v; want 1.5", cola.UnitValue)
	}

	// A magnitude someone cleared (0) must never win over a set one: a second
	// confirm of water with unit_value 0 leaves the decided 0.5 in place.
	draft.Items[0].UnitValue = floatPtr(0)
	confirmDraft(t, svc, scanStore, []byte("receipt-2"), domain.BillConfirmInput{
		MarketName: "ALDI", Currency: "USD", Items: draft.Items,
	})
	water2, _ := products.FindByName(context.Background(), "water 500ml")
	if water2.UnitValue == nil || *water2.UnitValue != 0.5 {
		t.Errorf("0-magnitude line erased the decided value: %v; want 0.5", water2.UnitValue)
	}
}

// TestConfirmLinksMappingToProduct asserts the opportunistic convenience link:
// confirming a line calls LinkProduct with the raw text and the resolved
// (find-or-created) product id; deposit returns and unmapped names stay silent.
func TestConfirmLinksMappingToProduct(t *testing.T) {
	draft := domain.BillDraft{
		MarketName: "ALDI", Currency: "USD",
		Items: []domain.BillItemDraft{
			{Name: "WHL MLK 1L", Unit: "l", Quantity: 1, UnitPriceCents: 120, LineTotalCents: 120},
			{Name: "LEERGUT 8", Quantity: 1, UnitPriceCents: -25, LineTotalCents: -25},
		},
	}
	mappings := newFakeProductMappingStore()
	mappings.seedMapping(domain.ProductNameMapping{
		RawName: "WHL MLK 1L", StandardName: "Whole Milk 1L", Source: domain.MappingSourceAI,
	})
	svc, scanStore := newTestBillServiceWithMappings(t, mappings,
		func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
			return draft, nil
		})

	confirmDraft(t, svc, scanStore, []byte("receipt-1"), domain.BillConfirmInput{
		MarketName: "ALDI", Currency: "USD", Items: draft.Items,
	})

	if len(mappings.links) != 1 {
		t.Fatalf("links recorded: %v; want exactly one (the purchase line)", mappings.links)
	}
	raw, id := mappings.links[0][0], mappings.links[0][1]
	if raw != "WHL MLK 1L" || id.(int64) <= 0 {
		t.Fatalf("link = %v %v; want raw text + a positive product id", raw, id)
	}
}

// TestConfirmNeverLinksDepositArtifacts pins the future-analysis guard:
// positive Pfand/Mehrweg deposit charges and a bare Gratis marker keep their
// real money in the bill, but (like the Leergut returns) never become
// catalogue products — the AI prompt asks for the same, and the
// domain.IsDepositArtifact code guard holds even when a custom prompt still
// misclassifies.
func TestConfirmNeverLinksDepositArtifacts(t *testing.T) {
	draft := domain.BillDraft{
		MarketName: "REWE", Currency: "EUR",
		Items: []domain.BillItemDraft{
			{Name: "PFAND 0,25", Quantity: 2, UnitPriceCents: 25, LineTotalCents: 50},
			{Name: "MEHRWEG-PFAND 1,50", Quantity: 1, UnitPriceCents: 150, LineTotalCents: 150},
			{Name: "GRATIS", Quantity: 1, UnitPriceCents: 0, LineTotalCents: 0},
		},
	}
	svc, scanStore, billStore, _, _, products := newTestBillServiceWithProducts(t,
		func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
			return draft, nil
		})

	confirmDraft(t, svc, scanStore, []byte("receipt-1"), domain.BillConfirmInput{MarketName: "REWE", Currency: "EUR", Items: draft.Items})

	if len(products.items) != 0 {
		t.Fatalf("deposit artifact lines must not create products, got %+v", products.items)
	}
	if len(billStore.items) != 1 {
		t.Fatalf("expected the persisted bill with its lines, got %d bills", len(billStore.items))
	}
	for _, it := range billStore.items[0].Items {
		if it.ProductID != nil {
			t.Errorf("artifact line %q must stay unlinked, got product %d", it.Name, *it.ProductID)
		}
	}
}

// TestApplyMappingMemorySkipsDepositArtifacts: a receipt line naming a
// deposit artifact is never looked up in the naming memory and never
// recorded into it, even though it is not flagged as a return.
func TestApplyMappingMemorySkipsDepositArtifacts(t *testing.T) {
	ctx := context.Background()
	mappings := newFakeProductMappingStore()
	mappings.seedMapping(domain.ProductNameMapping{
		RawName: "Gratis", StandardName: "Free Item", Source: domain.MappingSourceAI,
	})
	svc := &BillService{mappings: mappings, log: slog.Default()}
	draft := domain.BillDraft{Items: []domain.BillItemDraft{
		{Name: "GRATIS", StandardName: "Gratis"},
		{Name: "PFAND 0,25", StandardName: "Pfand 0,25"},
	}}
	svc.applyMappingMemory(ctx, &draft)

	mappings.mu.Lock()
	defer mappings.mu.Unlock()
	if len(mappings.lookups) != 0 {
		t.Errorf("artifact names must not be looked up, got %v", mappings.lookups)
	}
	if len(mappings.creates) != 0 {
		t.Errorf("artifact names must not be recorded, got %+v", mappings.creates)
	}
}

// TestLearnMappingOverridesSkipsDepositArtifacts: review edits of a deposit
// artifact line (a custom prompt may hand it standard/generic names) are
// never learned into the memory.
func TestLearnMappingOverridesSkipsDepositArtifacts(t *testing.T) {
	ctx := context.Background()
	mappings := newFakeProductMappingStore()
	svc := &BillService{mappings: mappings, log: slog.Default()}
	items := []domain.BillItemDraft{
		{Name: "PFAND 0,25", StandardName: "Bottle Deposit", GenericName: "Deposits", Quantity: 1, UnitPriceCents: 25, LineTotalCents: 25},
		{Name: "GRATIS", CategoryID: ptrInt64(7), Quantity: 1, UnitPriceCents: 0, LineTotalCents: 0},
	}
	svc.learnMappingOverrides(ctx, items, []priorStandardLine{})

	mappings.mu.Lock()
	defer mappings.mu.Unlock()
	if len(mappings.upserts) != 0 {
		t.Errorf("artifact lines must not be learned, got %+v", mappings.upserts)
	}
}

// TestConfirmResolvesProductsThroughMappings: a printed raw name the
// normalization memory links to a catalogue product joins that product's
// purchase history even when no catalogue row carries the raw name.
func TestConfirmResolvesProductsThroughMappings(t *testing.T) {
	ctx := context.Background()
	draft := domain.BillDraft{
		MarketName: "REWE", Currency: "EUR",
		Items: []domain.BillItemDraft{
			{Name: "Whole Milk", Unit: "l", Quantity: 1, UnitPriceCents: 149, LineTotalCents: 149},
		},
	}
	svc, scanStore, _, _, _, products := newTestBillServiceWithProducts(t,
		func(context.Context, []domain.ReceiptFile, domain.AIProvider, string) (domain.BillDraft, error) {
			return draft, nil
		})
	one := 1.0
	products.insert(domain.Product{Name: "Whole Milk 3.8%", Unit: "l", UnitValue: &one})
	milk, err := products.FindByName(ctx, "whole milk 3.8%")
	if err != nil {
		t.Fatalf("seed milk: %v", err)
	}
	mappings := newFakeProductMappingStore()
	svc.mappings = mappings
	mappings.seedMapping(domain.ProductNameMapping{
		RawName:      "Whole Milk",
		StandardName: "Whole Milk 3.8%",
		ProductID:    &milk.ID,
		Source:       domain.MappingSourceAI,
	})

	b := confirmDraft(t, svc, scanStore, []byte("receipt-mapped"),
		domain.BillConfirmInput{MarketName: "REWE", Currency: "EUR", Items: draft.Items})
	if len(products.items) != 1 {
		t.Fatalf("a duplicate product was find-or-created by raw name, got %d", len(products.items))
	}
	if len(b.Items) != 1 || b.Items[0].ProductID == nil || *b.Items[0].ProductID != milk.ID {
		t.Errorf("line not linked through the mapping: %+v", b.Items[0].ProductID)
	}
	if links := mappings.links; len(links) != 0 {
		t.Errorf("memory link rewritten: %v", links)
	}
}
