package service

import (
	"context"
	"errors"
	"fmt"
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

func (f *fakeBillScanStore) ClaimRetry(_ context.Context, token, providerID string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.items[token]
	if !ok || (s.Status != domain.BillScanDone && s.Status != domain.BillScanFailed) {
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
		if (s.Status == domain.BillScanDone || s.Status == domain.BillScanFailed) &&
			s.UpdatedAt.Before(olderThan) {
			paths = append(paths, s.ImagePath)
			delete(f.items, token)
		}
	}
	return paths, nil
}

// fakeBillStore is a BillStore stub; only Create is exercised by Confirm.
type fakeBillStore struct {
	mu    sync.Mutex
	items []domain.Bill
}

func (f *fakeBillStore) Create(_ context.Context, b domain.Bill) (domain.Bill, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b.ID = int64(len(f.items) + 1)
	b.Items = nil
	f.items = append(f.items, b)
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
func (f *fakeBillStore) SetTransaction(context.Context, int64, int64) error { return nil }
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
func (f *fakeBillStore) List(context.Context, BillFilters) ([]domain.Bill, error) {
	return nil, nil
}
func (f *fakeBillStore) Stats(context.Context, string, string) ([]domain.BillStatsRow, error) {
	return nil, nil
}
func (f *fakeBillStore) ListBrands(context.Context) ([]string, error) { return nil, nil }

// fakeBillExtractor delegates to a function so tests can stage results.
type fakeBillExtractor struct {
	fn func(context.Context, []byte, string, domain.AIProvider) (domain.BillDraft, error)
}

func (f *fakeBillExtractor) Extract(ctx context.Context, image []byte, mimeType string, p domain.AIProvider) (domain.BillDraft, error) {
	return f.fn(ctx, image, mimeType, p)
}

// TestConnection satisfies the ProviderTester interface SettingsService needs.
func (f *fakeBillExtractor) TestConnection(context.Context, domain.AIProvider) error {
	return nil
}

// fakeCategoryStore is a CategoryStore stub; the worker's category resolution
// only needs List (returning an empty taxonomy).
type fakeCategoryStore struct{}

func (fakeCategoryStore) List(context.Context) ([]domain.Category, error) { return nil, nil }
func (fakeCategoryStore) GetByID(context.Context, int64) (domain.Category, error) {
	return domain.Category{}, domain.ErrNotFound
}
func (fakeCategoryStore) Create(_ context.Context, c domain.Category) (domain.Category, error) {
	return c, nil
}
func (fakeCategoryStore) Delete(context.Context, int64) error { return nil }

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

func newTestBillService(t *testing.T, extractFn func(context.Context, []byte, string, domain.AIProvider) (domain.BillDraft, error)) (*BillService, *fakeBillScanStore, *fakeBillStore, *fakeStoreStore) {
	t.Helper()
	scanStore := newFakeBillScanStore()
	billStore := &fakeBillStore{}
	storeStore := newFakeStoreStore()
	extractor := &fakeBillExtractor{fn: extractFn}
	settings := NewSettingsService(
		&fakeSettingsStore{data: map[string]string{settingsKeyAIProviders: testProviderJSON()}},
		passthroughBox{}, extractor)
	svc := NewBillService(billStore, scanStore, extractor, settings,
		nil, fakeCategoryStore{}, storeStore, nil, nil, t.TempDir(), 5*time.Second, nil)
	t.Cleanup(svc.Close)
	return svc, scanStore, billStore, storeStore
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

// --- tests -------------------------------------------------------------------

func TestScanReturnsAnalyzingImmediately(t *testing.T) {
	blocked := make(chan struct{}) // extractor never returns
	svc, scanStore, _, _ := newTestBillService(t, func(context.Context, []byte, string, domain.AIProvider) (domain.BillDraft, error) {
		<-blocked
		return domain.BillDraft{}, errors.New("unreachable")
	})

	res, err := svc.Scan(context.Background(), "image/jpeg", testImage(), "")
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
	svc, scanStore, _, _ := newTestBillService(t, func(context.Context, []byte, string, domain.AIProvider) (domain.BillDraft, error) {
		return domain.BillDraft{
			MarketName: "Test Market",
			Items: []domain.BillItemDraft{
				{Name: "Milk", Quantity: 1, UnitPriceCents: 200, LineTotalCents: 200},
				{Name: "Bread", Quantity: 1, UnitPriceCents: 150, LineTotalCents: 150},
			},
			TotalCents: 350, Currency: "USD",
		}, nil
	})

	res, err := svc.Scan(context.Background(), "image/jpeg", testImage(), "")
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
	svc, scanStore, _, _ := newTestBillService(t, func(context.Context, []byte, string, domain.AIProvider) (domain.BillDraft, error) {
		return domain.BillDraft{}, errors.New("ollama is down")
	})

	res, err := svc.Scan(context.Background(), "image/jpeg", testImage(), "")
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

func TestConfirmGuardsScanState(t *testing.T) {
	svc, scanStore, billStore, _ := newTestBillService(t, func(context.Context, []byte, string, domain.AIProvider) (domain.BillDraft, error) {
		return domain.BillDraft{MarketName: "M", TotalCents: 100, Currency: "USD"}, nil
	})
	ctx := context.Background()

	// Confirm before the analysis finishes → validation error.
	early, err := svc.Scan(ctx, "image/jpeg", testImage(), "")
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
	svc, scanStore, _, _ := newTestBillService(t, func(context.Context, []byte, string, domain.AIProvider) (domain.BillDraft, error) {
		calls.Add(1)
		<-release
		return domain.BillDraft{MarketName: "M"}, nil
	})
	ctx := context.Background()

	res, err := svc.Scan(ctx, "image/jpeg", testImage(), "")
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
	svc, scanStore, _, _ := newTestBillService(t, func(context.Context, []byte, string, domain.AIProvider) (domain.BillDraft, error) {
		return domain.BillDraft{MarketName: "M"}, nil
	})
	ctx := context.Background()

	res, err := svc.Scan(ctx, "image/jpeg", testImage(), "")
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

func TestCloseReturnsWhileExtractionBlocked(t *testing.T) {
	blocked := make(chan struct{})
	svc, _, _, _ := newTestBillService(t, func(context.Context, []byte, string, domain.AIProvider) (domain.BillDraft, error) {
		<-blocked
		return domain.BillDraft{}, errors.New("unreachable")
	})
	if _, err := svc.Scan(context.Background(), "image/jpeg", testImage(), ""); err != nil {
		t.Fatalf("Scan: %v", err)
	}

	start := time.Now()
	svc.Close()
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("Close waited %v; it must not wait for the extraction", elapsed)
	}
}

func TestGetScanUnknownTokenIsNotFound(t *testing.T) {
	svc, _, _, _ := newTestBillService(t, func(context.Context, []byte, string, domain.AIProvider) (domain.BillDraft, error) {
		return domain.BillDraft{}, errors.New("not called")
	})
	if _, err := svc.GetScan(context.Background(), "deadbeef"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// --- store find-or-create on confirm/update ----------------------------------

func TestConfirmFindOrCreatesStore(t *testing.T) {
	svc, scanStore, _, storeStore := newTestBillService(t, func(context.Context, []byte, string, domain.AIProvider) (domain.BillDraft, error) {
		return domain.BillDraft{MarketName: "M"}, nil
	})
	ctx := context.Background()

	res, err := svc.Scan(ctx, "image/jpeg", testImage(), "")
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
	svc, scanStore, _, storeStore := newTestBillService(t, func(context.Context, []byte, string, domain.AIProvider) (domain.BillDraft, error) {
		return domain.BillDraft{MarketName: "M"}, nil
	})
	ctx := context.Background()

	first, err := svc.Scan(ctx, "image/jpeg", testImage(), "")
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

	// A second bill naming the same store in another casing reuses it.
	second, err := svc.Scan(ctx, "image/jpeg", testImage(), "")
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
	svc, scanStore, _, storeStore := newTestBillService(t, func(context.Context, []byte, string, domain.AIProvider) (domain.BillDraft, error) {
		return domain.BillDraft{MarketName: "M"}, nil
	})
	ctx := context.Background()

	res, err := svc.Scan(ctx, "image/jpeg", testImage(), "")
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
	svc, scanStore, _, storeStore := newTestBillService(t, func(context.Context, []byte, string, domain.AIProvider) (domain.BillDraft, error) {
		return domain.BillDraft{MarketName: "M"}, nil
	})
	ctx := context.Background()

	res, err := svc.Scan(ctx, "image/jpeg", testImage(), "")
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
	svc, _, _, storeStore := newTestBillService(t, func(context.Context, []byte, string, domain.AIProvider) (domain.BillDraft, error) {
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
