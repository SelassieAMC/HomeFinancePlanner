package jobs

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/riverqueue/river"

	"home-finance-planner/backend/internal/domain"
	"home-finance-planner/backend/internal/repository"
)

// TestManagerUnitValueBackfillEndToEnd runs the real River client over its
// dedicated SQLite pool with the real repositories: enqueue the one-time job,
// wait for it to run, and verify the magnitudes land and the completion marker
// is set. The AI is deliberately configured to error — with every name
// parseable the deterministic passes must complete the run without any AI
// consumption (the same guarantee boot relies on for machines with no
// connector).
func TestManagerUnitValueBackfillEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test (real queue client)")
	}
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "planner.db")

	db, err := repository.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open application db: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`INSERT INTO accounts (name, type, currency, created_at, updated_at) VALUES ('Wallet', 'cash', 'EUR', 100, 100)`); err != nil {
		t.Fatalf("seed account: %v", err)
	}

	productRepo := repository.NewProductRepository(db)
	billRepo := repository.NewBillRepository(db)
	txRepo := repository.NewTransactionRepository(db)
	settingsRepo := repository.NewSettingsRepository(db)

	product, err := productRepo.Create(ctx, domain.Product{Name: "Spring Water 500 ml", Unit: "ml"})
	if err != nil {
		t.Fatalf("create product: %v", err)
	}
	if _, err := billRepo.Create(ctx, domain.Bill{
		MarketName: "REWE", Date: "2026-05-01", Currency: "EUR", Status: domain.BillStatusAccepted,
		Items: []domain.BillItem{{Name: "Spring Water 500 ml", ProductID: &product.ID,
			Quantity: 1, UnitPriceCents: 99, LineTotalCents: 99}},
	}); err != nil {
		t.Fatalf("create bill: %v", err)
	}
	if _, err := txRepo.CreateWithItems(ctx, domain.Transaction{
		AccountID: 1, Kind: domain.TransactionExpense, AmountCents: 99, Currency: "EUR", Date: "2026-05-02",
	}, []domain.TransactionItem{{Name: "Spring Water 500 ml", ProductID: &product.ID,
		Quantity: 1, UnitPriceCents: 99, LineTotalCents: 99}}); err != nil {
		t.Fatalf("create transaction: %v", err)
	}

	worker := NewUnitValueBackfillWorker(productRepo, billRepo, txRepo, settingsRepo,
		&fakeProviders{err: errors.New("AI must not be needed when all names parse")},
		&fakePrompts{}, &fakeAI{}, 50*time.Millisecond,
		slog.New(slog.DiscardHandler))
	queueWorkers := river.NewWorkers()
	river.AddWorker(queueWorkers, worker)
	mgr, err := NewManager(ctx, dbPath, 2*time.Second, queueWorkers, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()
	if err := mgr.Start(ctx); err != nil {
		t.Fatalf("start queue: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = mgr.StopAndCancel(stopCtx)
		cancel()
	})

	if err := mgr.Insert(ctx, UnitValueBackfillArgs{}, &river.InsertOpts{
		UniqueOpts: river.UniqueOpts{ByQueue: true},
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	// a second enqueue while the job is already queued must be a no-op, not an
	// error (the unique strategy reports it as a skipped duplicate)
	if err := mgr.Insert(ctx, UnitValueBackfillArgs{}, &river.InsertOpts{
		UniqueOpts: river.UniqueOpts{ByQueue: true},
	}); err != nil {
		t.Fatalf("duplicate enqueue must be ignored, got %v", err)
	}

	// wait for the one-time job to run and mark completion
	deadline := time.Now().Add(15 * time.Second)
	for {
		_, err := settingsRepo.Get(ctx, UnitValueCompletionKey)
		if err == nil {
			break
		}
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("read completion marker: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatal("completion marker never appeared")
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}

	// every table drained and the catalogue magnitude was applied
	for _, check := range []struct {
		name string
		fn   func(ctx context.Context, limit int) ([]domain.UnitValueRow, error)
	}{
		{"products", productRepo.ProductsMissingUnitValue},
		{"bill_items", billRepo.BillItemsMissingUnitValue},
		{"transaction_items", txRepo.TransactionItemsMissingUnitValue},
	} {
		rows, err := check.fn(ctx, 10)
		if err != nil {
			t.Fatalf("%s candidates: %v", check.name, err)
		}
		if len(rows) != 0 {
			t.Errorf("%s still missing unit_value: %v", check.name, rows)
		}
	}
	read, err := productRepo.GetByID(ctx, product.ID)
	if err != nil {
		t.Fatalf("read product: %v", err)
	}
	if read.UnitValue == nil || *read.UnitValue != 500 || read.Unit != "ml" {
		t.Errorf("product = %v %q; want 500 ml", read.UnitValue, read.Unit)
	}
}

// TestRiverDSN verifies the pool DSN carries the pragmas the river sqlite
// driver depends on (busy timeout, WAL, foreign keys, immediate transactions).
func TestRiverDSN(t *testing.T) {
	got := riverDSN("/data/finance.db")
	for _, want := range []string{"file:/data/finance.db", "busy_timeout(5000)", "journal_mode(WAL)", "foreign_keys(1)", "_txlock=immediate"} {
		if !strings.Contains(got, want) {
			t.Errorf("DSN missing %q: %s", want, got)
		}
	}
}
