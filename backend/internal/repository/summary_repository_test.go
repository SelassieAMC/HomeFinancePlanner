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

	// Derived balances (migration 0040): the seeded ledger (300000 income −
	// 1000 − 500 − 700 = 297800) is folded into the opening balance once, so
	// the account derives to EUR 100000 exactly like the pre-0040 stored value.
	// Migrations seed a default wallet (USD) that takes the first ids — the
	// fixture pins explicit ids so the ledger lands on the right account.
	mustExec(`INSERT INTO accounts (id, name, type, currency, opening_balance_cents, created_at, updated_at)
		VALUES (10, 'Checking', 'checking', 'EUR', -197800, 1, 1),
		       (20, 'Savings', 'savings', 'USD', 11000, 1, 1)`)

	// Expense spread across the two windows: 2026-09-20 is inside the burn
	// range but not the month, 2026-09-01 is outside both.
	mustExec(`INSERT INTO transactions (account_id, kind, amount_cents, currency, date, created_at, updated_at)
		VALUES (10, 'income', 300000, 'EUR', '2026-10-01', 1, 1),
		       (10, 'expense', 1000, 'EUR', '2026-10-05', 1, 1),
		       (10, 'expense', 500, 'EUR', '2026-09-20', 1, 1),
		       (10, 'expense', 700, 'EUR', '2026-09-01', 1, 1)`)

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

// TestAccountBalancesPerAccountSplit (Phase 4): accountBalances returns one
// row per account with its FULL derived balance — opposite-signed accounts
// sharing one currency must stay separate rows, never cancel, so the
// service's sign partition (assets vs liabilities) sees both sides.
func TestAccountBalancesPerAccountSplit(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	mustExec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("seed: %v (%s)", err, query)
		}
	}
	// Explicit ids: migrations seed a default wallet that claims id 1.
	mustExec(`INSERT INTO accounts (id, name, type, currency, opening_balance_cents, created_at, updated_at)
		VALUES (10, 'Checking', 'checking', 'EUR', 100000, 1, 1),
		       (30, 'Card', 'credit', 'EUR', 0, 1, 1)`)

	mustExec(`INSERT INTO transactions (account_id, kind, amount_cents, currency, date, created_at, updated_at)
		VALUES (30, 'expense', 25000, 'EUR', '2026-10-05', 1, 1)`)

	rows, err := NewSummaryRepository(db).accountBalances(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[int64]domain.AccountCurrencyAmount{}
	for _, r := range rows {
		got[r.AccountID] = r
	}
	if g, ok := got[10]; !ok || g.Currency != "EUR" || g.Cents != 100_000 {
		t.Errorf("account 10 = %+v (present %v), want EUR 100000", g, ok)
	}
	if g, ok := got[30]; !ok || g.Currency != "EUR" || g.Cents != -25_000 {
		t.Errorf("account 30 = %+v (present %v), want EUR -25000 (debt, uncanceled)", g, ok)
	}
}
