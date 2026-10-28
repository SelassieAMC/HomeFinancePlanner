package repository

import (
	"context"
	"testing"

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
