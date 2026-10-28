package repository

import (
	"context"
	"testing"

	"home-finance-planner/backend/internal/domain"
)

// TestTaggedSpendForeignBillFold verifies the migration-0041 invariant: tagged
// spend drains the bag at the SAME conversion that debited the paying account
// (the rate recorded on the bill's expense transaction), never today's
// reference snapshot. With no bill-level discount the invariant is exact —
// one bag gets the whole debit.
func TestTaggedSpendForeignBillFold(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	r := NewBagRepository(db)

	card, err := NewAccountRepository(db).Create(ctx, domain.Account{
		Name: "EUR Card", Type: domain.AccountChecking, Currency: "EUR",
	})
	if err != nil {
		t.Fatalf("create card: %v", err)
	}
	bag, err := r.CreateBag(ctx, domain.Bag{
		Name: "Groceries", AccountID: card.ID, Status: domain.BagOpen,
	})
	if err != nil {
		t.Fatalf("create bag: %v", err)
	}

	seed := func(query string, args ...any) int64 {
		t.Helper()
		res, err := db.ExecContext(ctx, query, args...)
		if err != nil {
			t.Fatalf("seed: %v (%s)", err, query)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatalf("seed last id: %v", err)
		}
		return id
	}

	// A USD bill paid by the EUR card: two lines of 7500 USD each, no
	// discount, total 15000 — the recorded debit is 13500 EUR (rate 0.9).
	// The old query grouped the lines in USD and let the service convert at
	// the snapshot rate; the fold puts the group in EUR at the recorded rate.
	billID := seed(`INSERT INTO bills (market_name, date, currency, items_subtotal_cents, discount_cents,
			total_cents, status, bag_id, created_at, updated_at)
		VALUES ('Costco', '2026-10-10', 'USD', 15000, 0, 15000, 'accepted', ?, 1, 1)`, bag.ID)
	seed(`INSERT INTO bill_items (bill_id, name, quantity, unit_price_cents, line_total_cents)
		VALUES (?, 'Coffee', 1, 7500, 7500), (?, 'Bread', 1, 7500, 7500)`, billID, billID)
	txID := seed(`INSERT INTO transactions (account_id, kind, amount_cents, currency, account_amount_cents, fx_rate, date, created_at, updated_at)
		VALUES (?, 'expense', 15000, 'USD', 13500, 0.9, '2026-10-10', 1, 1)`, card.ID)
	seed(`UPDATE bills SET transaction_id = ? WHERE id = ?`, txID, billID)

	spend, err := r.TaggedSpend(ctx, "")
	if err != nil {
		t.Fatalf("tagged spend: %v", err)
	}
	if len(spend) != 1 || spend[0].BagID != bag.ID || spend[0].Currency != "EUR" || spend[0].Cents != 13500 {
		t.Fatalf("foreign bill spend = %+v; want one group {bag %d, EUR 13500}", spend, bag.ID)
	}

	// The exact invariant the test exists for: the bag drained what the
	// account lost — COALESCE(account_amount_cents) from accountBalanceExpr.
	billID2 := seed(`INSERT INTO bills (market_name, date, currency, items_subtotal_cents, discount_cents,
			total_cents, status, bag_id, created_at, updated_at)
		VALUES ('Aldi', '2026-10-11', 'USD', 10000, 0, 10000, 'accepted', ?, 1, 1)`, bag.ID)
	seed(`INSERT INTO bill_items (bill_id, name, quantity, unit_price_cents, line_total_cents)
		VALUES (?, 'Eggs', 1, 10000, 10000)`, billID2)
	txID2 := seed(`INSERT INTO transactions (account_id, kind, amount_cents, currency, account_amount_cents, fx_rate, date, created_at, updated_at)
		VALUES (?, 'expense', 10000, 'USD', 9200, 0.92, '2026-10-11', 1, 1)`, card.ID)
	seed(`UPDATE bills SET transaction_id = ? WHERE id = ?`, txID2, billID2)

	spend, err = r.TaggedSpend(ctx, "")
	if err != nil {
		t.Fatalf("tagged spend: %v", err)
	}
	if len(spend) != 1 || spend[0].Currency != "EUR" || spend[0].Cents != 22700 {
		t.Fatalf("spend after second bill = %+v; want EUR 22700 (13500 + 9200)", spend)
	}

	accGot, err := NewAccountRepository(db).GetByID(ctx, card.ID)
	if err != nil {
		t.Fatalf("load card: %v", err)
	}
	// Opening 0 − two recorded debits.
	if accGot.BalanceCents != -22700 {
		t.Fatalf("card balance = %d; want -22700 — bag drain must equal the ledger debit", accGot.BalanceCents)
	}
}

// TestTaggedSpendDegradedBillFoldsOneToOne covers the no-rate degradation:
// a foreign-currency bill whose transaction recorded no account amount folds
// 1:1 — the bag group lands in the account's currency at the raw amount,
// exactly what accountBalanceExpr debits.
func TestTaggedSpendDegradedBillFoldsOneToOne(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	r := NewBagRepository(db)

	card, err := NewAccountRepository(db).Create(ctx, domain.Account{
		Name: "EUR Card", Type: domain.AccountChecking, Currency: "EUR",
	})
	if err != nil {
		t.Fatalf("create card: %v", err)
	}
	bag, err := r.CreateBag(ctx, domain.Bag{
		Name: "Fun", AccountID: card.ID, Status: domain.BagOpen,
	})
	if err != nil {
		t.Fatalf("create bag: %v", err)
	}

	seed := func(query string, args ...any) int64 {
		t.Helper()
		res, err := db.ExecContext(ctx, query, args...)
		if err != nil {
			t.Fatalf("seed: %v (%s)", err, query)
		}
		id, _ := res.LastInsertId()
		return id
	}

	billID := seed(`INSERT INTO bills (market_name, date, currency, items_subtotal_cents, discount_cents,
			total_cents, status, bag_id, created_at, updated_at)
		VALUES ('Unrated store', '2026-10-12', 'COP', 5000, 0, 5000, 'accepted', ?, 1, 1)`, bag.ID)
	seed(`INSERT INTO bill_items (bill_id, name, quantity, unit_price_cents, line_total_cents)
		VALUES (?, 'Snack', 1, 5000, 5000)`, billID)
	txID := seed(`INSERT INTO transactions (account_id, kind, amount_cents, currency, date, created_at, updated_at)
		VALUES (?, 'expense', 5000, 'COP', '2026-10-12', 1, 1)`, card.ID) // no conversion recorded
	seed(`UPDATE bills SET transaction_id = ? WHERE id = ?`, txID, billID)

	spend, err := r.TaggedSpend(ctx, "")
	if err != nil {
		t.Fatalf("tagged spend: %v", err)
	}
	if len(spend) != 1 || spend[0].BagID != bag.ID || spend[0].Currency != "EUR" || spend[0].Cents != 5000 {
		t.Fatalf("degraded spend = %+v; want one group {bag %d, EUR 5000 (1:1, ledger parity)}", spend, bag.ID)
	}
}

// TestTaggedSpendSameCurrencyLegacyRows verifies the unchanged behaviors: a
// same-currency bill keeps its raw line totals, and an accepted bill without
// a transaction link (legacy rows before 0008) still reports in the bill's
// own currency.
func TestTaggedSpendSameCurrencyLegacyRows(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	r := NewBagRepository(db)

	acc, err := NewAccountRepository(db).Create(ctx, domain.Account{
		Name: "Main", Type: domain.AccountChecking, Currency: "EUR",
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	bag, err := r.CreateBag(ctx, domain.Bag{
		Name: "Fuel", AccountID: acc.ID, Status: domain.BagOpen,
	})
	if err != nil {
		t.Fatalf("create bag: %v", err)
	}

	seed := func(query string, args ...any) int64 {
		t.Helper()
		res, err := db.ExecContext(ctx, query, args...)
		if err != nil {
			t.Fatalf("seed: %v (%s)", err, query)
		}
		id, _ := res.LastInsertId()
		return id
	}

	// Same-currency bill: ratio 1, raw line totals in EUR.
	billID := seed(`INSERT INTO bills (market_name, date, currency, items_subtotal_cents, discount_cents,
			total_cents, status, bag_id, created_at, updated_at)
		VALUES ('Shell', '2026-10-13', 'EUR', 8000, 0, 8000, 'accepted', ?, 1, 1)`, bag.ID)
	seed(`INSERT INTO bill_items (bill_id, name, quantity, unit_price_cents, line_total_cents)
		VALUES (?, 'Diesel', 1, 8000, 8000)`, billID)
	txID := seed(`INSERT INTO transactions (account_id, kind, amount_cents, currency, date, created_at, updated_at)
		VALUES (?, 'expense', 8000, 'EUR', '2026-10-13', 1, 1)`, acc.ID)
	seed(`UPDATE bills SET transaction_id = ? WHERE id = ?`, txID, billID)

	// Manual expense tagged to the bag — the other leg of TaggedSpend.
	seed(`INSERT INTO transactions (account_id, kind, amount_cents, currency, bag_id, date, created_at, updated_at)
		VALUES (?, 'expense', 1200, 'EUR', ?, '2026-10-14', 1, 1)`, acc.ID, bag.ID)

	spend, err := r.TaggedSpend(ctx, "")
	if err != nil {
		t.Fatalf("tagged spend: %v", err)
	}
	// Both sources land in one EUR group: 8000 (bill lines) + 1200 (manual).
	if len(spend) != 1 || spend[0].Currency != "EUR" || spend[0].Cents != 9200 {
		t.Fatalf("spend = %+v; want one EUR group of 9200", spend)
	}

	// Legacy bill with no transaction link: keeps the bill's currency.
	legacyID := seed(`INSERT INTO bills (market_name, date, currency, items_subtotal_cents, discount_cents,
			total_cents, status, bag_id, created_at, updated_at)
		VALUES ('Old store', '2026-09-01', 'USD', 3000, 0, 3000, 'accepted', ?, 1, 1)`, bag.ID)
	seed(`INSERT INTO bill_items (bill_id, name, quantity, unit_price_cents, line_total_cents)
		VALUES (?, 'Old item', 1, 3000, 3000)`, legacyID)

	spend, err = r.TaggedSpend(ctx, "")
	if err != nil {
		t.Fatalf("tagged spend: %v", err)
	}
	if len(spend) != 2 {
		t.Fatalf("spend groups = %+v; want 2 (EUR 9200, USD 3000)", spend)
	}
	for _, g := range spend {
		switch {
		case g.Currency == "EUR" && g.Cents == 9200:
		case g.Currency == "USD" && g.Cents == 3000:
		default:
			t.Fatalf("unexpected group %+v; want EUR 9200 + USD 3000 (legacy bills keep their currency)", g)
		}
	}
}

// TestTaggedSpendDateBound checks the until bound still applies to both legs
// after the fold rewrite.
func TestTaggedSpendDateBound(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	r := NewBagRepository(db)

	acc, err := NewAccountRepository(db).Create(ctx, domain.Account{
		Name: "Main", Type: domain.AccountChecking, Currency: "EUR",
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	bag, err := r.CreateBag(ctx, domain.Bag{
		Name: "All", AccountID: acc.ID, Status: domain.BagOpen,
	})
	if err != nil {
		t.Fatalf("create bag: %v", err)
	}

	seed := func(query string, args ...any) int64 {
		t.Helper()
		res, err := db.ExecContext(ctx, query, args...)
		if err != nil {
			t.Fatalf("seed: %v (%s)", err, query)
		}
		id, _ := res.LastInsertId()
		return id
	}

	seed(`INSERT INTO transactions (account_id, kind, amount_cents, currency, bag_id, date, created_at, updated_at)
		VALUES (?, 'expense', 1000, 'EUR', ?, '2026-10-01', 1, 1)`, acc.ID, bag.ID)
	billID := seed(`INSERT INTO bills (market_name, date, currency, items_subtotal_cents, discount_cents,
			total_cents, status, bag_id, created_at, updated_at)
		VALUES ('X', '2026-10-20', 'EUR', 2000, 0, 2000, 'accepted', ?, 1, 1)`, bag.ID)
	seed(`INSERT INTO bill_items (bill_id, name, quantity, unit_price_cents, line_total_cents)
		VALUES (?, 'Y', 1, 2000, 2000)`, billID)
	txID := seed(`INSERT INTO transactions (account_id, kind, amount_cents, currency, date, created_at, updated_at)
		VALUES (?, 'expense', 2000, 'EUR', '2026-10-20', 1, 1)`, acc.ID)
	seed(`UPDATE bills SET transaction_id = ? WHERE id = ?`, txID, billID)

	spend, err := r.TaggedSpend(ctx, "2026-10-15")
	if err != nil {
		t.Fatalf("tagged spend: %v", err)
	}
	if len(spend) != 1 || spend[0].Cents != 1000 {
		t.Fatalf("bounded spend = %+v; want only the 2026-10-01 manual expense (1000)", spend)
	}
}
