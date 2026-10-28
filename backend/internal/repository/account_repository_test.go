package repository

import (
	"context"
	"errors"
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

// TestAccountLedgerRunningBalance covers the register read (Phase 2): rows
// newest first, every row's running balance folds opening + all rows up to
// and including it by (date, id) — foreign-currency expenses via their
// account_amount_cents (raw 1:1 when none recorded), fee'd transfers out,
// counter-leg transfers in — and the LAST row of the FULL ledger equals the
// account's derived balance (the invariant the spec mandates). Also verifies
// the pagination window: the running values are window-independent.
func TestAccountLedgerRunningBalance(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	r := NewAccountRepository(db)

	card, err := r.Create(ctx, domain.Account{
		Name: "EUR Card", Type: domain.AccountChecking, Currency: "EUR", OpeningBalanceCents: 100000,
	})
	if err != nil {
		t.Fatalf("create card: %v", err)
	}
	dest, err := r.Create(ctx, domain.Account{Name: "Other", Type: domain.AccountChecking, Currency: "EUR"})
	if err != nil {
		t.Fatalf("create dest: %v", err)
	}

	seed := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("seed: %v (%s)", err, query)
		}
	}
	// Chronological thread on the card:
	//   +50000 income
	//   −25000 expense (raw EUR, no conversion recorded)
	//   −20000 COP expense folded at its recorded 20000 EUR
	//   −32000 transfer out (30000 + 2000 fee)
	seed(`INSERT INTO transactions (account_id, kind, amount_cents, currency, date, created_at, updated_at)
		VALUES (?, 'income', 50000, 'EUR', '2026-10-01', 1, 1)`, card.ID)
	seed(`INSERT INTO transactions (account_id, kind, amount_cents, currency, date, created_at, updated_at)
		VALUES (?, 'expense', 25000, 'EUR', '2026-10-02', 1, 1)`, card.ID)
	seed(`INSERT INTO transactions (account_id, kind, amount_cents, currency, account_amount_cents, date, created_at, updated_at)
		VALUES (?, 'expense', 20000, 'COP', 20000, '2026-10-03', 1, 1)`, card.ID)
	seed(`INSERT INTO transactions (account_id, counter_account_id, kind, amount_cents, currency, fee_cents, date, created_at, updated_at)
		VALUES (?, ?, 'transfer', 30000, 'EUR', 2000, '2026-10-04', 1, 1)`, card.ID, dest.ID)

	entries, err := r.Ledger(ctx, card.ID, 0, 0)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	if len(entries) != 4 {
		t.Fatalf("ledger rows = %d; want 4", len(entries))
	}
	// Newest first: the transfer tops the page.
	if entries[0].Date != "2026-10-04" || entries[len(entries)-1].Date != "2026-10-01" {
		t.Fatalf("order = %s..%s; want newest first", entries[0].Date, entries[len(entries)-1].Date)
	}
	// Per-row changes exactly mirror the balance terms. Oldest first:
	// 100000 opening +50000 income − 25000 expense − 20000 folded COP →
	// − 32000 transfer (amount + fee).
	wantRunning := []int64{150000, 125000, 105000, 73000}
	for i, want := range wantRunning { // oldest first: index 3 → 0
		got := entries[len(entries)-1-i]
		if got.RunningBalanceCents != want {
			t.Errorf("row %s running = %d; want %d", got.Date, got.RunningBalanceCents, want)
		}
	}
	if entries[0].ChangeCents != -32000 || entries[1].ChangeCents != -20000 || entries[2].ChangeCents != -25000 || entries[3].ChangeCents != 50000 {
		t.Fatalf("changes = %d/%d/%d/%d; want -32000/-20000/-25000/+50000",
			entries[0].ChangeCents, entries[1].ChangeCents, entries[2].ChangeCents, entries[3].ChangeCents)
	}

	// THE invariant: the last row of the full ledger carries the current
	// derived balance.
	acc, err := r.GetByID(ctx, card.ID)
	if err != nil {
		t.Fatalf("load account: %v", err)
	}
	last := entries[0] // the newest of a complete listing IS the fold end
	if last.RunningBalanceCents != acc.BalanceCents {
		t.Fatalf("last full-ledger running = %d; want derived balance %d", last.RunningBalanceCents, acc.BalanceCents)
	}

	// The destination account sees the receiving leg (+32000 wait — no fee on
	// the receiver, no counter amount recorded: folds the raw 30000).
	dstEntries, err := r.Ledger(ctx, dest.ID, 0, 0)
	if err != nil {
		t.Fatalf("dest ledger: %v", err)
	}
	if len(dstEntries) != 1 || dstEntries[0].RunningBalanceCents != 30000 || dstEntries[0].ChangeCents != 30000 {
		t.Fatalf("dest ledger = %+v; want one row +30000 (raw counter fold)", dstEntries)
	}

	// Pagination: a windowed page still carries the FULL fold per row
	// (limit 1 = only the transfer row, running 100000 — window-independent).
	page, err := r.Ledger(ctx, card.ID, 1, 0)
	if err != nil {
		t.Fatalf("paged ledger: %v", err)
	}
	if len(page) != 1 || page[0].RunningBalanceCents != acc.BalanceCents {
		t.Fatalf("paged row = %+v; want the transfer with the full fold %d", page, acc.BalanceCents)
	}

	// An unknown account id yields an empty thread, not an error.
	empty, err := r.Ledger(ctx, 999999, 0, 0)
	if err != nil || len(empty) != 0 {
		t.Fatalf("unknown account ledger = %+v, err %v; want empty", empty, err)
	}
}

// TestAccountGroupCRUDAndSetNull (Phase 4): the presentation groups — create
// with a unique name, rename, membership rides the account row, and deleting
// a group UNGROUPS its accounts (ON DELETE SET NULL) without touching them.
func TestAccountGroupCRUDAndSetNull(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	r := NewAccountRepository(db)
	g := NewAccountGroupRepository(db)

	group, err := g.Create(ctx, domain.AccountGroup{Name: "German banks"})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	if _, err := g.Create(ctx, domain.AccountGroup{Name: "German banks"}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate group name: expected ErrConflict, got %v", err)
	}

	a, err := r.Create(ctx, domain.Account{Name: "Giro", Type: domain.AccountChecking, Currency: "EUR", GroupID: &group.ID})
	if err != nil {
		t.Fatalf("create account in group: %v", err)
	}
	if a.GroupID == nil || *a.GroupID != group.ID {
		t.Fatalf("account group_id = %v, want %d", a.GroupID, group.ID)
	}

	if _, err := g.Update(ctx, domain.AccountGroup{ID: group.ID, Name: "DE banks"}); err != nil {
		t.Fatalf("rename group: %v", err)
	}

	// A create/update against a vanished group id must surface a real FK
	// error (not silently store NULL) — the service validates first, this is
	// the DB's backstop.
	missing := int64(9999)
	_, err = r.Create(ctx, domain.Account{Name: "Bad", Type: domain.AccountCash, Currency: "EUR", GroupID: &missing})
	if err == nil {
		t.Fatalf("account with unknown group_id accepted; want FK error")
	}

	// Deleting the group ungroups the account; the account row survives.
	if err := g.Delete(ctx, group.ID); err != nil {
		t.Fatalf("delete group: %v", err)
	}
	after, err := r.GetByID(ctx, a.ID)
	if err != nil {
		t.Fatalf("get account after group delete: %v", err)
	}
	if after.GroupID != nil {
		t.Fatalf("account still carries group_id %d after group delete; want NULL", *after.GroupID)
	}
	if g2, err := g.GetByID(ctx, group.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("get deleted group = %v, %v; want ErrNotFound", g2, err)
	}
}
