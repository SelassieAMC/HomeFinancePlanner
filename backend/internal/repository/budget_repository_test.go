package repository

import (
	"context"
	"testing"
	"time"

	"home-finance-planner/backend/internal/domain"
)

// TestRolloverMonthly covers the lazy month rollover: a stale open monthly
// row is closed and succeeded by a fresh row for the target month with the
// same amount; envelopes and already-closed monthly rows are untouched, and
// a second run is a no-op (idempotent).
func TestRolloverMonthly(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	repo := NewBudgetRepository(db)

	if _, err := db.ExecContext(ctx,
		`INSERT INTO categories (name, created_at) VALUES ('Rollover Monthly Cat', 1), ('Rollover Envelope Cat', 1)`); err != nil {
		t.Fatalf("seed categories: %v", err)
	}
	var monthlyCat, envelopeCat int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM categories WHERE name = 'Rollover Monthly Cat'`).Scan(&monthlyCat); err != nil {
		t.Fatalf("monthly category id: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT id FROM categories WHERE name = 'Rollover Envelope Cat'`).Scan(&envelopeCat); err != nil {
		t.Fatalf("envelope category id: %v", err)
	}
	mustBudget := func(b domain.Budget) domain.Budget {
		t.Helper()
		out, err := repo.Create(ctx, b)
		if err != nil {
			t.Fatalf("seed budget %+v: %v", b, err)
		}
		return out
	}

	stale := mustBudget(domain.Budget{CategoryID: monthlyCat, AmountCents: 50_000, Cadence: domain.BudgetMonthly, Month: "2026-08"})
	envelope := mustBudget(domain.Budget{CategoryID: envelopeCat, AmountCents: 20_000, Cadence: domain.BudgetEnvelope})

	// Rollover into October: stale August row closes, successor opens.
	if err := repo.RolloverMonthly(ctx, "2026-10", nil); err != nil {
		t.Fatal(err)
	}

	old, err := repo.GetByID(ctx, stale.ID)
	if err != nil {
		t.Fatal(err)
	}
	if old.Status != domain.BudgetClosed || old.ClosedAt == nil {
		t.Errorf("stale monthly = %+v, want closed with closed_at", old)
	}
	if old.Month != "2026-08" {
		t.Errorf("stale monthly month = %q, want 2026-08", old.Month)
	}

	all, err := repo.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var succ *domain.Budget
	for i := range all {
		if all[i].Cadence == domain.BudgetMonthly && all[i].Status == domain.BudgetOpen {
			succ = &all[i]
		}
	}
	if succ == nil {
		t.Fatal("no open monthly successor created")
	}
	if succ.CategoryID != monthlyCat || succ.AmountCents != 50_000 || succ.Month != "2026-10" {
		t.Errorf("successor = %+v, want monthly category, amount 50000, month 2026-10", succ)
	}

	env, err := repo.GetByID(ctx, envelope.ID)
	if err != nil {
		t.Fatal(err)
	}
	if env.Status != domain.BudgetOpen || env.Month != "" {
		t.Errorf("envelope = %+v, want open with no month", env)
	}

	// Idempotent: a second rollover must not close the fresh successor or
	// stack another row on top.
	if err := repo.RolloverMonthly(ctx, "2026-10", nil); err != nil {
		t.Fatal(err)
	}
	all, err = repo.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	openMonthly := 0
	for _, b := range all {
		if b.Cadence == domain.BudgetMonthly && b.Status == domain.BudgetOpen {
			openMonthly++
		}
	}
	if openMonthly != 1 {
		t.Errorf("open monthly rows = %d, want 1 after repeat rollover", openMonthly)
	}
	stillOpen, err := repo.GetByID(ctx, succ.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stillOpen.Status != domain.BudgetOpen || stillOpen.Month != "2026-10" {
		t.Errorf("successor after repeat rollover = %+v, want open 2026-10", stillOpen)
	}
}

// TestRolloverMonthlyCarriesBalance covers migration 0043: a carrying row's
// successor inherits the service-computed carried_cents AND the carry_over
// flag; a rollover-off row rolls over with carried 0 even when a map entry
// exists (the flag gates the carry, not the map); caps are copied, never
// rebased.
func TestRolloverMonthlyCarriesBalance(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	repo := NewBudgetRepository(db)

	if _, err := db.ExecContext(ctx,
		`INSERT INTO categories (name, created_at) VALUES ('Carry Cat', 1), ('NoCarry Cat', 1)`); err != nil {
		t.Fatalf("seed categories: %v", err)
	}
	var carryCat, noCarryCat int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM categories WHERE name = 'Carry Cat'`).Scan(&carryCat); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT id FROM categories WHERE name = 'NoCarry Cat'`).Scan(&noCarryCat); err != nil {
		t.Fatal(err)
	}
	carryOn, err := repo.Create(ctx, domain.Budget{
		CategoryID: carryCat, AmountCents: 10_000, Cadence: domain.BudgetMonthly,
		Month: "2026-01", CarryOver: true, CarriedCents: 2_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	carryOff, err := repo.Create(ctx, domain.Budget{
		CategoryID: noCarryCat, AmountCents: 4_000, Cadence: domain.BudgetMonthly, Month: "2026-01",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Service numbers: carry row's chain says 9200 (its own 2000 + 10000 cap −
	// spend); the off row's entry must be ignored.
	carries := map[int64]int64{carryOn.ID: 9_200, carryOff.ID: 99_999}
	if err := repo.RolloverMonthly(ctx, "2026-02", carries); err != nil {
		t.Fatal(err)
	}

	succ, err := repo.GetByID(ctx, carryOn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if succ.Status != domain.BudgetClosed || succ.Month != "2026-01" {
		t.Errorf("predecessor = %+v; want closed January (never rewritten)", succ)
	}
	var succOn *domain.Budget
	all, _ := repo.List(ctx)
	for i := range all {
		if all[i].CategoryID == carryCat && all[i].Month == "2026-02" {
			succOn = &all[i]
		}
	}
	if succOn == nil {
		t.Fatal("carry successor missing")
	}
	if !succOn.CarryOver || succOn.CarriedCents != 9_200 || succOn.AmountCents != 10_000 {
		t.Errorf("carry successor = %+v; want carry_over on, carried 9200, cap untouched 10000", succOn)
	}

	var succOff *domain.Budget
	for i := range all {
		if all[i].CategoryID == noCarryCat && all[i].Month == "2026-02" {
			succOff = &all[i]
		}
	}
	if succOff == nil {
		t.Fatal("no-carry successor missing")
	}
	if succOff.CarryOver || succOff.CarriedCents != 0 {
		t.Errorf("no-carry successor = %+v; want carry_over off, carried 0 (map ignored)", succOff)
	}
}

// TestBudgetTransfersAndSeeds covers migration 0044: a transfer moves the
// caps both ways and records one auditable row; a closed source refuses the
// WHOLE move (both caps unchanged — the transaction guards); a seed rides
// carried_cents with from_budget NULL; the history joins read back with
// amounts and NULL-from intact.
func TestBudgetTransfersAndSeeds(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	repo := NewBudgetRepository(db)

	if _, err := db.ExecContext(ctx,
		`INSERT INTO categories (name, created_at) VALUES ('T Cat A', 1), ('T Cat B', 1)`); err != nil {
		t.Fatalf("seed categories: %v", err)
	}
	var catA, catB int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM categories WHERE name = 'T Cat A'`).Scan(&catA); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT id FROM categories WHERE name = 'T Cat B'`).Scan(&catB); err != nil {
		t.Fatal(err)
	}
	src, err := repo.Create(ctx, domain.Budget{CategoryID: catA, AmountCents: 10_000, Cadence: domain.BudgetMonthly, Month: "2026-09", CarryOver: true})
	if err != nil {
		t.Fatal(err)
	}
	dst, err := repo.Create(ctx, domain.Budget{CategoryID: catB, AmountCents: 4_000, Cadence: domain.BudgetMonthly, Month: "2026-09"})
	if err != nil {
		t.Fatal(err)
	}

	from, to, err := repo.ApplyTransfer(ctx, src.ID, dst.ID, 1_500, "leftover", "2026-09")
	if err != nil {
		t.Fatalf("transfer: %v", err)
	}
	if from.AmountCents != 8_500 || to.AmountCents != 5_500 {
		t.Fatalf("caps = %d/%d; want 8500/5500", from.AmountCents, to.AmountCents)
	}

	// A closed source refuses atomically: no cap moves, no audit row.
	if _, err := repo.Update(ctx, domain.Budget{ID: dst.ID, CategoryID: catB, AmountCents: 5_500,
		Status: domain.BudgetClosed, Cadence: domain.BudgetMonthly, Month: "2026-09", ClosedAt: &time.Time{}}); err != nil {
		t.Fatalf("close dst: %v", err)
	}
	if _, _, err := repo.ApplyTransfer(ctx, src.ID, dst.ID, 1_500, "", "2026-09"); err == nil {
		t.Fatal("closed-target transfer accepted")
	}
	hist, err := repo.ListTransfers(ctx)
	if err != nil {
		t.Fatalf("transfers: %v", err)
	}
	if len(hist) != 1 {
		t.Fatalf("audit rows = %d; want 1 (the refused move recorded nothing)", len(hist))
	}
	if hist[0].FromCategoryName != "T Cat A" || hist[0].ToCategoryName != "T Cat B" || hist[0].AmountCents != 1_500 {
		t.Fatalf("audit row = %+v; want the joined move", hist[0])
	}

	// Reopen and seed: carried rides signed, from NULL in the audit.
	if _, err := repo.Update(ctx, domain.Budget{ID: dst.ID, CategoryID: catB, AmountCents: 5_500,
		Status: domain.BudgetOpen, Cadence: domain.BudgetMonthly, Month: "2026-09"}); err != nil {
		t.Fatalf("reopen dst: %v", err)
	}
	seeded, err := repo.ApplySeed(ctx, dst.ID, -2_000, "overspent seed", "2026-09")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if seeded.CarriedCents != -2_000 {
		t.Fatalf("seeded carry = %d; want -2000", seeded.CarriedCents)
	}
	hist, err = repo.ListTransfers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 2 || hist[0].FromBudgetID != nil || hist[0].AmountCents != -2_000 || hist[1].FromBudgetID == nil {
		t.Fatalf("audit rows = %+v; want seed (from NULL) first, move second", hist)
	}
}
