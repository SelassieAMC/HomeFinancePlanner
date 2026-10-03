package repository

import (
	"context"
	"database/sql"
	"testing"

	"home-finance-planner/backend/internal/domain"
)

// TestProductRepositoryUnitValueBackfill covers the catalogue half of the
// one-time job: candidate listing (missing magnitudes, deposit/Leergut
// excluded) and guarded writes (unit adopted only into empty units, IS NULL
// idempotency).
func TestProductRepositoryUnitValueBackfill(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	repo := NewProductRepository(db)
	deposit := seededDepositCategory(t, ctx, db)

	type seeded struct {
		id int64
		p  domain.Product
	}
	made := map[string]seeded{}
	for name, p := range map[string]domain.Product{
		"plain":   {Name: "Yoghurt Natural", Unit: "g"},
		"no unit": {Name: "Pasta Pennette"},
		"deposit": {Name: "Pfand Flasche", CategoryID: &deposit.ID},
		"leergut": {Name: "Leergut Kasten"},
	} {
		product, err := repo.Create(ctx, p)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		made[name] = seeded{id: product.ID, p: product}
	}

	rows, err := repo.ProductsMissingUnitValue(ctx, 100)
	if err != nil {
		t.Fatalf("candidates: %v", err)
	}
	candidates := map[int64]string{}
	for _, r := range rows {
		candidates[r.ID] = r.Name
	}
	if len(rows) != 2 {
		t.Fatalf("candidates = %v; want exactly the plain and the unit-less product", candidates)
	}
	for _, name := range []string{"deposit", "leergut"} {
		if _, ok := candidates[made[name].id]; ok {
			t.Errorf("%s product must be excluded (deposit/Leergut never get a size)", name)
		}
	}
	if few, err := repo.ProductsMissingUnitValue(ctx, 1); err != nil || len(few) != 1 {
		t.Errorf("limit 1 = %v (%v)", few, err)
	}

	unit := "g"
	if err := repo.BackfillProductUnitValues(ctx, []domain.UnitValueFix{
		{ID: made["plain"].id, UnitValue: 500},                // decided unit kept
		{ID: made["no unit"].id, UnitValue: 500, Unit: &unit}, // unit adopted
	}); err != nil {
		t.Fatalf("backfill write: %v", err)
	}

	plain, err := repo.GetByID(ctx, made["plain"].id)
	if err != nil {
		t.Fatalf("read plain: %v", err)
	}
	if plain.UnitValue == nil || *plain.UnitValue != 500 || plain.Unit != "g" {
		t.Errorf("plain product = %v %q; want 500 decided-g", plain.UnitValue, plain.Unit)
	}
	less, err := repo.GetByID(ctx, made["no unit"].id)
	if err != nil {
		t.Fatalf("read unit-less: %v", err)
	}
	if less.UnitValue == nil || *less.UnitValue != 500 || less.Unit != "g" {
		t.Errorf("unit-less product = %v %q; want adopted 500 g", less.UnitValue, less.Unit)
	}

	// idempotency: a repeat write on a resolved row changes nothing, and the
	// candidate list drains
	if err := repo.BackfillProductUnitValues(ctx, []domain.UnitValueFix{
		{ID: made["plain"].id, UnitValue: 999, Unit: &unit},
	}); err != nil {
		t.Fatalf("second write: %v", err)
	}
	again, err := repo.GetByID(ctx, made["plain"].id)
	if err != nil || again.UnitValue == nil || *again.UnitValue != 500 {
		t.Fatalf("IS NULL guard failed: %v (%v)", again.UnitValue, err)
	}
	if left, err := repo.ProductsMissingUnitValue(ctx, 100); err != nil || len(left) != 0 {
		t.Errorf("candidates after write = %v (%v); want none", left, err)
	}
}

// TestLineUnitValueBackfill covers bill_items and transaction_items: the
// product-copy join through product_id, the deposit/Leergut exclusion, and the
// same guarded-write semantics as the catalogue query.
func TestLineUnitValueBackfill(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	productRepo := NewProductRepository(db)
	deposit := seededDepositCategory(t, ctx, db)

	resolved, err := productRepo.Create(ctx, domain.Product{Name: "Water Still", Unit: "ml", UnitValue: fp64(500)})
	if err != nil {
		t.Fatalf("create resolved product: %v", err)
	}
	sized, err := productRepo.Create(ctx, domain.Product{Name: "Cola 1,5l", Unit: "l", UnitValue: fp64(1.5)})
	if err != nil {
		t.Fatalf("create sized product: %v", err)
	}
	unsized, err := productRepo.Create(ctx, domain.Product{Name: "Store Brand Muesli", Unit: "g"})
	if err != nil {
		t.Fatalf("create unsized product: %v", err)
	}

	if _, err := db.Exec(`INSERT INTO bills (market_name, date, currency, status, created_at, updated_at)
		VALUES ('REWE', '2026-05-01', 'EUR', 'accepted', 100, 100)`); err != nil {
		t.Fatalf("seed bill: %v", err)
	}
	billID := int64(1) // first bill row of the fresh database

	billItems := []struct {
		name, unit string
		productID  *int64
		categoryID *int64
	}{
		{name: "Water Still", unit: "", productID: &resolved.ID},       // copy from product, adopt unit
		{name: "Cola 1,5l", unit: "l", productID: &sized.ID},           // copy, same-dimension unit kept
		{name: "Store Brand Muesli", unit: "", productID: &unsized.ID}, // no product magnitude
		{name: "Leergut Kasten", unit: "", categoryID: &deposit.ID},    // excluded: deposit category
		{name: "Leergut PET", unit: ""},                                // excluded: name
	}
	for _, item := range billItems {
		if _, err := db.Exec(`INSERT INTO bill_items (bill_id, name, unit, product_id, category_id, unit_price_cents, line_total_cents)
			VALUES (?, ?, ?, ?, ?, 99, 99)`, billID, item.name, item.unit, item.productID, item.categoryID); err != nil {
			t.Fatalf("seed bill item %s: %v", item.name, err)
		}
	}

	accountID := seedTransactionBase(t, db)
	if _, err := NewTransactionRepository(db).CreateWithItems(ctx, domain.Transaction{
		AccountID: accountID, Kind: domain.TransactionExpense, AmountCents: 500, Currency: "EUR", Date: "2026-05-02",
	}, []domain.TransactionItem{
		{Name: "Water Still", Unit: "", ProductID: &resolved.ID, Quantity: 1, UnitPriceCents: 99, LineTotalCents: 99},
		{Name: "Bread", Unit: "pcs", Quantity: 1, UnitPriceCents: 250, LineTotalCents: 250},
	}); err != nil {
		t.Fatalf("seed transaction: %v", err)
	}
	billRepo := NewBillRepository(db)
	txRepo := NewTransactionRepository(db)

	billRows, err := billRepo.BillItemsMissingUnitValue(ctx, 100)
	if err != nil {
		t.Fatalf("bill candidates: %v", err)
	}
	byName := map[string]domain.UnitValueRow{}
	for _, r := range billRows {
		byName[r.Name] = r
	}
	if len(billRows) != 3 {
		t.Fatalf("bill candidates = %v; want 3 (deposit and Leergut lines excluded)", billRows)
	}
	if water := byName["Water Still"]; water.ProductUnitValue == nil || *water.ProductUnitValue != 500 || water.ProductUnit != "ml" {
		t.Errorf("copy candidate = %+v; want product magnitude 500 ml", water)
	}
	if cola := byName["Cola 1,5l"]; cola.ProductUnitValue == nil || *cola.ProductUnitValue != 1.5 {
		t.Errorf("sized candidate = %+v; want product magnitude 1.5", cola)
	}
	if muesli := byName["Store Brand Muesli"]; muesli.ProductUnitValue != nil {
		t.Errorf("unsized candidate = %+v; want no product magnitude", muesli)
	}

	// The copy write adopts the product unit only into an empty one; a line
	// with a decided unit keeps it (Cola's product is l, the line is l).
	ml := "ml"
	if err := billRepo.BackfillBillItemUnitValues(ctx, []domain.UnitValueFix{
		{ID: byName["Water Still"].ID, UnitValue: 500, Unit: &ml}, // adopt into empty
		{ID: byName["Store Brand Muesli"].ID, UnitValue: 400},     // value only, unit untouched
	}); err != nil {
		t.Fatalf("bill write: %v", err)
	}
	var gotUnit string
	var gotValue float64
	if err := db.QueryRow(`SELECT unit, unit_value FROM bill_items WHERE name = 'Store Brand Muesli'`).
		Scan(&gotUnit, &gotValue); err != nil {
		t.Fatal(err)
	}
	if gotUnit != "" || gotValue != 400 {
		t.Errorf("unit-only fix = %q %v; want \"\" 400 (a value-only fix never touches the unit)", gotUnit, gotValue)
	}

	// idempotency: repeating the same fix changes nothing
	if err := billRepo.BackfillBillItemUnitValues(ctx, []domain.UnitValueFix{
		{ID: byName["Water Still"].ID, UnitValue: 999, Unit: &ml},
	}); err != nil {
		t.Fatalf("second bill write: %v", err)
	}
	if err := db.QueryRow(`SELECT unit_value FROM bill_items WHERE name = 'Water Still'`).Scan(&gotValue); err != nil || gotValue != 500 {
		t.Errorf("IS NULL guard failed: %v (%v)", gotValue, err)
	}

	txRows, err := txRepo.TransactionItemsMissingUnitValue(ctx, 100)
	if err != nil {
		t.Fatalf("tx candidates: %v", err)
	}
	if len(txRows) != 2 {
		t.Fatalf("tx candidates = %d; want 2", len(txRows))
	}
	var txWater domain.UnitValueRow
	for _, r := range txRows {
		if r.Name == "Water Still" {
			txWater = r
		}
	}
	if txWater.ID == 0 || txWater.ProductUnitValue == nil || *txWater.ProductUnitValue != 500 {
		t.Errorf("tx copy candidate = %+v; want product magnitude 500", txWater)
	}
	if err := txRepo.BackfillTransactionItemUnitValues(ctx, []domain.UnitValueFix{
		{ID: txWater.ID, UnitValue: 500, Unit: &ml},
	}); err != nil {
		t.Fatalf("tx write: %v", err)
	}
	if err := db.QueryRow(`SELECT unit, unit_value FROM transaction_items WHERE id = ?`, txWater.ID).
		Scan(&gotUnit, &gotValue); err != nil {
		t.Fatal(err)
	}
	if gotUnit != "ml" || gotValue != 500 {
		t.Errorf("tx line after write = %q %v; want ml 500", gotUnit, gotValue)
	}
	if left, err := txRepo.TransactionItemsMissingUnitValue(ctx, 100); err != nil || len(left) != 1 {
		t.Errorf("tx candidates after write = %v (%v); want 1 (Bread)", left, err)
	}
}

func fp64(v float64) *float64 { return &v }

// seededDepositCategory returns the migration-seeded Deposit & Returns product
// category (allows_negative; created by the seeds in migrations 0001).
func seededDepositCategory(t *testing.T, ctx context.Context, db *sql.DB) domain.Category {
	t.Helper()
	cats, err := NewCategoryRepository(db).List(ctx)
	if err != nil {
		t.Fatalf("list categories: %v", err)
	}
	for _, c := range cats {
		if c.Kind == "product" && c.AllowsNegative {
			return c
		}
	}
	t.Fatal("no seeded allows_negative product category found")
	return domain.Category{}
}
