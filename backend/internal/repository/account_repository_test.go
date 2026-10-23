package repository

import (
	"context"
	"testing"

	"home-finance-planner/backend/internal/domain"
)

// TestDerivedAccountBalances verifies the migration-0040 balance math:
//
//	opening + Σ(income) − Σ(expense) − Σ(transfers out) + Σ(transfers in)
//
// A payment must move the balance, the counter leg of a transfer must credit
// the destination (its recorded amount, 1:1 when none), and the opening
// balance is the only writable money field — the reconcile shape.
func TestDerivedAccountBalances(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	r := NewAccountRepository(db)

	src, err := r.Create(ctx, domain.Account{Name: "Main", Type: domain.AccountChecking, Currency: "EUR", OpeningBalanceCents: 100000})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	dest, err := r.Create(ctx, domain.Account{Name: "Abroad", Type: domain.AccountSavings, Currency: "CHF"})
	if err != nil {
		t.Fatalf("create destination: %v", err)
	}

	seed := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("seed: %v (%s)", err, query)
		}
	}
	seed(`INSERT INTO transactions (account_id, kind, amount_cents, currency, date, created_at, updated_at)
		VALUES (?, 'income', 50000, 'EUR', '2026-10-02', 1, 1)`, src.ID)
	seed(`INSERT INTO transactions (account_id, kind, amount_cents, currency, date, created_at, updated_at)
		VALUES (?, 'expense', 20000, 'EUR', '2026-10-03', 1, 1)`, src.ID)

	got, err := r.GetByID(ctx, src.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.BalanceCents != 130000 {
		t.Fatalf("balance = %d; want 130000 (opening 100000 + income 50000 − expense 20000)", got.BalanceCents)
	}

	// A cross-currency transfer with a recorded receiving leg: 30000 EUR →
	// 45000 CHF (the FX snapshot rate folded at save time).
	seed(`INSERT INTO transactions (account_id, counter_account_id, kind, amount_cents, currency, counter_amount_cents, counter_currency, date, created_at, updated_at)
		VALUES (?, ?, 'transfer', 30000, 'EUR', 45000, 'CHF', '2026-10-04', 1, 1)`, src.ID, dest.ID)

	srcGot, err := r.GetByID(ctx, src.ID)
	if err != nil {
		t.Fatalf("get source after transfer: %v", err)
	}
	if srcGot.BalanceCents != 100000 {
		t.Fatalf("source balance = %d; want 100000 (−30000 transferred out)", srcGot.BalanceCents)
	}
	dstGot, err := r.GetByID(ctx, dest.ID)
	if err != nil {
		t.Fatalf("get destination: %v", err)
	}
	if dstGot.BalanceCents != 45000 {
		t.Fatalf("destination balance = %d; want 45000 (recorded receiving leg)", dstGot.BalanceCents)
	}

	// Reconcile: the bank says the source now holds 85000 — the opening
	// balance absorbs the difference; the ledger's facts stay untouched.
	out, err := r.Update(ctx, domain.Account{
		ID: src.ID, Name: "Main", Type: domain.AccountChecking, Currency: "EUR",
		OpeningBalanceCents: srcGot.OpeningBalanceCents + (85000 - srcGot.BalanceCents),
	})
	if err != nil {
		t.Fatalf("reconcile write: %v", err)
	}
	if out.BalanceCents != 85000 {
		t.Fatalf("after reconcile balance = %d; want 85000", out.BalanceCents)
	}
}

// TestDerivedBalanceLegacyTransferMissingLeg covers rows written before 0040:
// counter fields NULL fold the transfer 1:1 into the destination.
func TestDerivedBalanceLegacyTransferMissingLeg(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	r := NewAccountRepository(db)

	src, err := r.Create(ctx, domain.Account{Name: "A", Type: domain.AccountChecking, Currency: "EUR", OpeningBalanceCents: 100000})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	dest, err := r.Create(ctx, domain.Account{Name: "B", Type: domain.AccountChecking, Currency: "EUR"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO transactions (account_id, counter_account_id, kind, amount_cents, currency, date, created_at, updated_at)
		VALUES (?, ?, 'transfer', 25000, 'EUR', '2026-10-05', 1, 1)`, src.ID, dest.ID); err != nil {
		t.Fatalf("seed transfer: %v", err)
	}

	srcGot, err := r.GetByID(ctx, src.ID)
	if err != nil {
		t.Fatalf("get source: %v", err)
	}
	if srcGot.BalanceCents != 75000 {
		t.Fatalf("source balance = %d; want 75000", srcGot.BalanceCents)
	}
	dstGot, err := r.GetByID(ctx, dest.ID)
	if err != nil {
		t.Fatalf("get destination: %v", err)
	}
	if dstGot.BalanceCents != 25000 {
		t.Fatalf("destination balance = %d; want 25000 (NULL counter leg folds 1:1)", dstGot.BalanceCents)
	}
}

// TestDerivedBalancesForeignExpenseAndFee verifies the migration-0041 legs:
// an income/expense row in a currency other than the account's folds its
// account_amount_cents (the conversion recorded at save time) instead of the
// raw amount, and a transfer's fee is deducted from the sending side on top
// of the amount.
func TestDerivedBalancesForeignExpenseAndFee(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	r := NewAccountRepository(db)

	card, err := r.Create(ctx, domain.Account{
		Name: "EUR Card", Type: domain.AccountCredit, Currency: "EUR", OpeningBalanceCents: 100000,
	})
	if err != nil {
		t.Fatalf("create card: %v", err)
	}
	local, err := r.Create(ctx, domain.Account{Name: "COP Wallet", Type: domain.AccountCash, Currency: "COP"})
	if err != nil {
		t.Fatalf("create wallet: %v", err)
	}

	// A COP bill paid by the EUR card: the raw amount is 100000 COP, the
	// converted account amount 20000 EUR (rate 0.2). Before 0041 this row
	// folded 1:1 and corrupted the balance by a factor of five.
	seedForeign := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("seed: %v (%s)", err, query)
		}
	}
	seedForeign(`INSERT INTO transactions (account_id, kind, amount_cents, currency, account_amount_cents, fx_rate, date, created_at, updated_at)
		VALUES (?, 'expense', 100000, 'COP', 20000, 0.2, '2026-10-06', 1, 1)`, card.ID)

	cardGot, err := r.GetByID(ctx, card.ID)
	if err != nil {
		t.Fatalf("get card: %v", err)
	}
	if cardGot.BalanceCents != 80000 {
		t.Fatalf("card balance = %d; want 80000 (−20000 converted, not −100000 raw)", cardGot.BalanceCents)
	}

	// A transfer with a bank fee: 30000 EUR sent + 2000 EUR fee taken; the
	// COP wallet receives the recorded counter leg.
	seedForeign(`INSERT INTO transactions (account_id, counter_account_id, kind, amount_cents, currency, fee_cents, counter_amount_cents, counter_currency, date, created_at, updated_at)
		VALUES (?, ?, 'transfer', 30000, 'EUR', 2000, 150000, 'COP', '2026-10-07', 1, 1)`, card.ID, local.ID)

	cardGot, err = r.GetByID(ctx, card.ID)
	if err != nil {
		t.Fatalf("get card after transfer: %v", err)
	}
	if cardGot.BalanceCents != 48000 {
		t.Fatalf("card balance = %d; want 48000 (−32000 amount + fee)", cardGot.BalanceCents)
	}
	localGot, err := r.GetByID(ctx, local.ID)
	if err != nil {
		t.Fatalf("get wallet: %v", err)
	}
	if localGot.BalanceCents != 150000 {
		t.Fatalf("wallet balance = %d; want 150000 (fee never touches the receiver)", localGot.BalanceCents)
	}
}
