package repository

import (
	"context"
	"testing"

	"home-finance-planner/backend/internal/domain"
)

// TestAnalyticsItemPricesUnitValue covers both branches of the per-item
// price math: when bill_items.unit_value is present the preferred formula
// (unit_price ÷ size, g/ml scaled ×1000 to kg/l) wins; when it is NULL or 0
// the legacy formula (unit_price × per-unit factor) applies. Deposit returns
// and non-positive prices stay excluded either way.
func TestAnalyticsItemPricesUnitValue(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	repo := NewAnalyticsRepository(db)

	seed := func(query string, args ...any) int64 {
		t.Helper()
		res, err := db.ExecContext(ctx, query, args...)
		if err != nil {
			t.Fatalf("seed %q: %v", query, err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatalf("seed id: %v", err)
		}
		return id
	}
	store := seed(`INSERT INTO stores (name, created_at, updated_at) VALUES ('REWE', 100, 100)`)
	bill := seed(`
		INSERT INTO bills (store_id, market_name, date, currency, status, created_at, updated_at)
		VALUES (?, 'REWE', '2026-05-10', 'EUR', 'accepted', 100, 100)`, store)
	item := func(name, unit string, unitValue any, unitPrice int64) int64 {
		t.Helper()
		return seed(`
			INSERT INTO bill_items (bill_id, name, unit, unit_value, unit_price_cents, line_total_cents, quantity, is_return)
			VALUES (?, ?, ?, ?, ?, ?, 1, 0)`, bill, name, unit, unitValue, unitPrice, unitPrice)
	}
	item("cola 1.5l", "l", 1.5, 300)      // preferred: 300 ÷ 1.5 = 200 ¢/l
	item("water 500ml", "ml", 500.0, 100) // preferred + scaled: 100 ÷ 500 × 1000 = 200 ¢/l
	item("bread", "pcs", 2.0, 500)        // preferred, no scaling: 500 ÷ 2 = 250 ¢/pcs
	item("flour 1kg", "g", nil, 200)      // legacy: 200 × 1000 = 200000 ¢/kg
	item("oil 1l", "ml", 0.0, 100)        // 0 = unknown → legacy: 100 × 1000 = 100000 ¢/l
	item("leergut", "pcs", 2.0, -25)      // deposit return: excluded

	rows, err := repo.ItemPrices(ctx, "2026-05-01", "2026-05-31")
	if err != nil {
		t.Fatalf("item prices: %v", err)
	}
	if len(rows) != 5 {
		t.Fatalf("rows = %d; want 5 (the return line excluded)", len(rows))
	}
	byName := map[string]float64{}
	for _, r := range rows {
		byName[r.Key] = r.PricePerUnit
	}
	want := map[string]float64{
		"cola 1.5l":   200,
		"water 500ml": 200,
		"bread":       250,
		"flour 1kg":   200000,
		"oil 1l":      100000,
	}
	for name, expected := range want {
		if byName[name] != expected {
			t.Errorf("%s: price per unit = %v; want %v", name, byName[name], expected)
		}
	}
}

// TestAnalyticsTopProductsAndStores covers the two consumption aggregates,
// which read the same union of sources the stats CTE uses: accepted
// non-return bill lines plus manual-transaction item lines. Excluded: items
// of discarded bills, return lines, item lines of income transactions, and —
// for the store view — bills/transactions without a store.
func TestAnalyticsTopProductsAndStores(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	repo := NewAnalyticsRepository(db)

	seed := func(query string, args ...any) int64 {
		t.Helper()
		res, err := db.ExecContext(ctx, query, args...)
		if err != nil {
			t.Fatalf("seed %q: %v", query, err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatalf("seed id: %v", err)
		}
		return id
	}
	rewe := seed(`INSERT INTO stores (name, created_at, updated_at) VALUES ('REWE', 100, 100)`)
	lidl := seed(`INSERT INTO stores (name, created_at, updated_at) VALUES ('Lidl', 100, 100)`)
	milk := seed(`INSERT INTO products (name, created_at, updated_at) VALUES ('Milk', 100, 100)`)
	eggs := seed(`INSERT INTO products (name, created_at, updated_at) VALUES ('Eggs', 100, 100)`)
	account := seed(`INSERT INTO accounts (name, type, currency, created_at, updated_at) VALUES ('Wallet', 'cash', 'EUR', 100, 100)`)

	okBill := seed(`
		INSERT INTO bills (store_id, market_name, date, currency, status, created_at, updated_at)
		VALUES (?, 'REWE', '2026-05-10', 'EUR', 'accepted', 100, 100)`, rewe)
	seed(`
		INSERT INTO bill_items (bill_id, product_id, name, quantity, unit_price_cents, line_total_cents)
		VALUES (?, ?, 'Milk', 2, 150, 300)`, okBill, milk)
	seed(`
		INSERT INTO bill_items (bill_id, product_id, name, quantity, unit_price_cents, line_total_cents)
		VALUES (?, ?, 'Eggs', 1, 200, 200)`, okBill, eggs)
	seed(`
		INSERT INTO bill_items (bill_id, product_id, name, quantity, unit_price_cents, line_total_cents)
		VALUES (?, ?, 'Milk', 1, 150, 150)`, okBill, milk) // second line of the same product merges

	staleBill := seed(`
		INSERT INTO bills (store_id, market_name, date, currency, status, created_at, updated_at)
		VALUES (?, 'Lidl', '2026-05-11', 'EUR', 'discarded', 100, 100)`, lidl)
	seed(`
		INSERT INTO bill_items (bill_id, product_id, name, quantity, unit_price_cents, line_total_cents)
		VALUES (?, ?, 'Milk', 9, 150, 1350)`, staleBill, milk) // discarded bill: excluded

	storelessBill := seed(`
		INSERT INTO bills (market_name, date, currency, status, created_at, updated_at)
		VALUES ('Market', '2026-05-12', 'EUR', 'accepted', 100, 100)`)
	seed(`
		INSERT INTO bill_items (bill_id, product_id, name, quantity, unit_price_cents, line_total_cents)
		VALUES (?, ?, 'Milk', 1, 150, 150)`, storelessBill, milk) // no store: product still counts, store not

	returnBill := seed(`
		INSERT INTO bills (store_id, market_name, date, currency, status, created_at, updated_at)
		VALUES (?, 'REWE', '2026-05-12', 'EUR', 'accepted', 100, 100)`, rewe)
	seed(`
		INSERT INTO bill_items (bill_id, product_id, name, quantity, unit_price_cents, line_total_cents, is_return)
		VALUES (?, ?, 'Leergut', 1, -25, -25, 1)`, returnBill, milk) // return line: excluded even with a product

	tx := seed(`
		INSERT INTO transactions (account_id, kind, amount_cents, currency, description, date, store_id, created_at, updated_at)
		VALUES (?, 'expense', 250, 'EUR', 'Manual purchase', '2026-05-14', ?, 100, 100)`, account, rewe)
	seed(`
		INSERT INTO transaction_items (transaction_id, product_id, name, quantity, unit_price_cents, line_total_cents, created_at, updated_at)
		VALUES (?, ?, 'Milk', 1, 150, 150, 100, 100)`, tx, milk)

	seed(`
		INSERT INTO transactions (account_id, kind, amount_cents, currency, description, date, store_id, created_at, updated_at)
		VALUES (?, 'income', 900, 'EUR', 'Salary', '2026-05-15', ?, 100, 100)`, account, rewe) // unlinked visit: excluded

	// --- top products -----------------------------------------------------------
	// One row per (product, source branch): the bill branch merges every
	// accepted EUR bill line (including the storeless bill), the transaction
	// branch the manual line. The service folds them into per-product totals.
	prodRows, err := repo.TopProducts(ctx, "2026-05-01", "2026-05-31")
	if err != nil {
		t.Fatalf("top products: %v", err)
	}
	if len(prodRows) != 3 {
		t.Fatalf("productId rows = %d; want 3 (milk×2 branches, eggs): %+v", len(prodRows), prodRows)
	}
	var milkRows []domain.ProductSpendRow
	var eggsRow *domain.ProductSpendRow
	for _, r := range prodRows {
		if r.ProductID == milk {
			milkRows = append(milkRows, r)
		} else if r.ProductID == eggs {
			eggsRow = &r
		}
	}
	if len(milkRows) != 2 {
		t.Fatalf("milk rows = %d; want 2 (bill branch + manual branch): %+v", len(milkRows), milkRows)
	}
	if (milkRows[0].Quantity == 4 && milkRows[1].Quantity != 1) || (milkRows[0].Quantity == 1 && milkRows[1].Quantity != 4) {
		t.Fatalf("milk branch rows: %+v; want q=4 total=600 and q=1 total=150", milkRows)
	}
	if eggsRow == nil || eggsRow.Quantity != 1 || eggsRow.TotalCents != 200 {
		if eggsRow != nil {
			t.Fatalf("eggs row: %+v; want q=1 total=200", *eggsRow)
		}
		t.Fatal("eggs row missing")
	}

	// --- top stores -------------------------------------------------------------
	// Same per-branch shape (bills branch, transactions branch); the Lidl
	// discarded bill, the storeless bill and the income transaction drop out,
	// so only REWE appears in both branches.
	storeRows, err := repo.TopStores(ctx, "2026-05-01", "2026-05-31")
	if err != nil {
		t.Fatalf("top stores: %v", err)
	}
	if len(storeRows) != 2 {
		t.Fatalf("store rows = %d; want 2 (REWE bill branch + tx branch): %+v", len(storeRows), storeRows)
	}
	var visits, spend int64
	for _, r := range storeRows {
		if r.StoreID != rewe || r.Name != "REWE" {
			t.Fatalf("unexpected store row: %+v", r)
		}
		visits += r.VisitCount
		spend += r.TotalCents
	}
	if visits != 3 || spend != 875 {
		t.Errorf("rewe totals: visits=%d spend=%d; want 3 / 875 (625 bill + 250 manual)", visits, spend)
	}
	_ = lidl // Lidl's only bill is discarded, so its row never appears
}
