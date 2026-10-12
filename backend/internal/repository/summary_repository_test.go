package repository

import (
	"context"
	"testing"

	"home-finance-planner/backend/internal/domain"
)

// TestRawGlanceWindowsAndPendingBacklog exercises the period-independent
// aggregate: cash flow reads only the month window, the burn window is a
// separate wider range, and the receipt backlog counts only pending bills.
func TestRawGlanceWindowsAndPendingBacklog(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	mustExec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("seed: %v (%s)", err, query)
		}
	}

	mustExec(`INSERT INTO accounts (name, type, currency, balance_cents, created_at, updated_at)
		VALUES ('Checking', 'checking', 'EUR', 100000, 1, 1),
		       ('Savings', 'savings', 'USD', 11000, 1, 1)`)

	// Expense spread across the two windows: 2026-09-20 is inside the burn
	// range but not the month, 2026-09-01 is outside both.
	mustExec(`INSERT INTO transactions (account_id, kind, amount_cents, currency, date, created_at, updated_at)
		VALUES (1, 'income', 300000, 'EUR', '2026-10-01', 1, 1),
		       (1, 'expense', 1000, 'EUR', '2026-10-05', 1, 1),
		       (1, 'expense', 500, 'EUR', '2026-09-20', 1, 1),
		       (1, 'expense', 700, 'EUR', '2026-09-01', 1, 1)`)

	// Two pending receipts in different currencies; accepted and discarded
	// bills must not count.
	mustExec(`INSERT INTO bills (total_cents, currency, status, date, created_at, updated_at)
		VALUES (2500, 'EUR', 'pending', '2026-10-10', 1, 1),
		       (1500, 'USD', 'pending', '2026-10-11', 1, 1),
		       (9999, 'EUR', 'accepted', '2026-10-01', 1, 1),
		       (555, 'EUR', 'discarded', '2026-10-02', 1, 1)`)

	raw, err := NewSummaryRepository(db).RawGlance(ctx,
		"2026-10-01", "2026-10-31", "2026-09-13", "2026-10-12")
	if err != nil {
		t.Fatal(err)
	}

	if len(raw.MonthIncome) != 1 || raw.MonthIncome[0] != (domain.CurrencyAmount{Currency: "EUR", Cents: 300000}) {
		t.Errorf("MonthIncome = %+v, want EUR 300000", raw.MonthIncome)
	}
	if len(raw.MonthExpense) != 1 || raw.MonthExpense[0].Cents != 1000 {
		t.Errorf("MonthExpense = %+v, want EUR 1000 (month only)", raw.MonthExpense)
	}
	if len(raw.Expense30Days) != 1 || raw.Expense30Days[0].Cents != 1500 {
		t.Errorf("Expense30Days = %+v, want EUR 1500 (burn window)", raw.Expense30Days)
	}

	balances := map[string]int64{}
	for _, b := range raw.Balances {
		balances[b.Currency] += b.Cents
	}
	if balances["EUR"] != 100000 || balances["USD"] != 11000 {
		t.Errorf("Balances = %+v, want EUR 100000 and USD 11000", raw.Balances)
	}

	if raw.PendingReceipts != 2 {
		t.Errorf("PendingReceipts = %d, want 2", raw.PendingReceipts)
	}
	pending := map[string]int64{}
	for _, p := range raw.PendingTotals {
		pending[p.Currency] += p.Cents
	}
	if pending["EUR"] != 2500 || pending["USD"] != 1500 || len(pending) != 2 {
		t.Errorf("PendingTotals = %+v, want EUR 2500 + USD 1500", raw.PendingTotals)
	}
}
