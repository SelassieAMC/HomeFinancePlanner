package repository

import (
	"context"
	"testing"
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
