package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"home-finance-planner/backend/internal/domain"
)

// newTestDB opens a migrated SQLite database in a temp directory.
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := Open(context.Background(), t.TempDir()+"/test.db")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// seedProductStats builds the scenario the stats queries are tested against:
//
//	Milk   — REWE twice in EUR (129 on 01-10, 139 on 02-10), then Aldi twice
//	         in USD (150, 170 on 03-10, higher item id last → latest) →
//	         times_bought 4, latest 170 USD, avg 160, best 150 (EUR lines
//	         must not leak into avg/best), plus a EUR return line (excluded)
//	         and a draft REWE bill (excluded).
//	Cola   — one purchase at a storeless bill (200 EUR on 04-10).
//	Nothing — never bought: zero stats, nil prices.
//
// Returns the REWE store id, the two bought product ids and the never-bought
// one.
func seedProductStats(t *testing.T, db *sql.DB) (reweID, milkID, colaID, nothingID int64) {
	t.Helper()
	seed := func(query string, args ...any) int64 {
		t.Helper()
		res, err := db.Exec(query, args...)
		if err != nil {
			t.Fatalf("seed %q: %v", query, err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatalf("seed id: %v", err)
		}
		return id
	}

	reweID = seed(`INSERT INTO stores (name, created_at, updated_at) VALUES ('REWE', 100, 100)`)
	aldiID := seed(`INSERT INTO stores (name, created_at, updated_at) VALUES ('Aldi', 100, 100)`)
	milkID = seed(`INSERT INTO products (name, created_at, updated_at) VALUES ('Milk', 100, 100)`)
	colaID = seed(`INSERT INTO products (name, created_at, updated_at) VALUES ('Cola', 100, 100)`)
	nothingID = seed(`INSERT INTO products (name, created_at, updated_at) VALUES ('Nothing', 100, 100)`)

	bill := func(store any, date, currency, status string) int64 {
		t.Helper()
		return seed(`
			INSERT INTO bills (store_id, market_name, date, currency, status, created_at, updated_at)
			VALUES (?, 'Market', ?, ?, ?, 100, 100)`, store, date, currency, status)
	}
	item := func(billID, productID int64, name string, price int64, isReturn int) {
		t.Helper()
		if _, err := db.Exec(`
			INSERT INTO bill_items (bill_id, product_id, name, unit_price_cents, line_total_cents, is_return)
			VALUES (?, ?, ?, ?, ?, ?)`,
			billID, productID, name, price, price, isReturn); err != nil {
			t.Fatalf("seed bill item: %v", err)
		}
	}

	reweJan := bill(reweID, "2026-01-10", "EUR", "accepted")
	reweFeb := bill(reweID, "2026-02-10", "EUR", "accepted")
	aldiMar := bill(aldiID, "2026-03-10", "USD", "accepted")
	reweDraft := bill(reweID, "2026-02-20", "EUR", "draft")
	storeless := bill(nil, "2026-04-10", "EUR", "accepted")

	item(reweJan, milkID, "Milk", 129, 0)
	item(reweFeb, milkID, "Milk", 139, 0)
	item(reweFeb, milkID, "Milk Return", 0, 1) // deposit return: never counted
	item(aldiMar, milkID, "Milk", 150, 0)
	item(aldiMar, milkID, "Milk", 170, 0)
	item(reweDraft, milkID, "Milk", 999, 0) // draft: excluded
	item(storeless, colaID, "Cola", 200, 0)
	return reweID, milkID, colaID, nothingID
}

func TestProductRepositoryStats(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	repo := NewProductRepository(db)
	_, milkID, _, nothingID := seedProductStats(t, db)

	milk, err := repo.GetByID(ctx, milkID)
	if err != nil {
		t.Fatalf("get milk: %v", err)
	}
	if milk.TimesBought != 4 || milk.LastPurchaseDate != "2026-03-10" {
		t.Fatalf("milk stats = %d bought, last %q; want 4, 2026-03-10", milk.TimesBought, milk.LastPurchaseDate)
	}
	// Latest is the 170 USD line (same date/bill as 150, higher item id);
	// avg/best are scoped to USD — the EUR lines must not mix in.
	if milk.LatestPriceCents == nil || *milk.LatestPriceCents != 170 || milk.PriceCurrency != "USD" {
		t.Fatalf("milk latest = %v %q; want 170 USD", milk.LatestPriceCents, milk.PriceCurrency)
	}
	if milk.AvgPriceCents == nil || *milk.AvgPriceCents != 160 {
		t.Fatalf("milk avg = %v; want 160 (USD lines only)", milk.AvgPriceCents)
	}
	if milk.BestPriceCents == nil || *milk.BestPriceCents != 150 {
		t.Fatalf("milk best = %v; want 150 (EUR 129 excluded by currency scope)", milk.BestPriceCents)
	}

	// A product never bought: zero count, everything else nil.
	nothing, err := repo.GetByID(ctx, nothingID)
	if err != nil {
		t.Fatalf("get nothing: %v", err)
	}
	if nothing.TimesBought != 0 || nothing.LastPurchaseDate != "" ||
		nothing.LatestPriceCents != nil || nothing.AvgPriceCents != nil || nothing.BestPriceCents != nil {
		t.Fatalf("nothing stats = %+v; want zero/nil", nothing)
	}

	// FindByName matches case-insensitively and returns the same stats.
	found, err := repo.FindByName(ctx, "milk")
	if err != nil {
		t.Fatalf("find milk by name: %v", err)
	}
	if found.ID != milkID || found.TimesBought != 4 || found.LatestPriceCents == nil || *found.LatestPriceCents != 170 {
		t.Fatalf("found milk = %+v; want id %d with milk's stats", found, milkID)
	}
	if _, err := repo.FindByName(ctx, "Yoghurt"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("find unknown = %v; want ErrNotFound", err)
	}
	if _, err := repo.GetByID(ctx, 424242); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("get unknown = %v; want ErrNotFound", err)
	}
}

func TestProductRepositoryStorePrices(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	repo := NewProductRepository(db)
	reweID, milkID, colaID, _ := seedProductStats(t, db)

	// Currency-scoped: EUR sees only the REWE lines' most recent price.
	eur, err := repo.StorePrices(ctx, milkID, "EUR")
	if err != nil {
		t.Fatalf("store prices EUR: %v", err)
	}
	if len(eur) != 1 || eur[0].StoreID == nil || *eur[0].StoreID != reweID ||
		eur[0].LatestPriceCents == nil || *eur[0].LatestPriceCents != 139 ||
		eur[0].LastPurchaseDate != "2026-02-10" {
		t.Fatalf("EUR prices = %+v; want REWE 139 on 2026-02-10", eur)
	}

	// Unscoped summary: every store, newest first; returns excluded.
	summary, err := repo.StorePurchaseSummary(ctx, milkID)
	if err != nil {
		t.Fatalf("store summary: %v", err)
	}
	if len(summary) != 2 {
		t.Fatalf("summary rows = %d; want 2 (Aldi, REWE)", len(summary))
	}
	if summary[0].StoreName != "Aldi" || summary[0].LatestPriceCents == nil ||
		*summary[0].LatestPriceCents != 170 || summary[0].Currency != "USD" {
		t.Fatalf("summary[0] = %+v; want Aldi 170 USD (most recent)", summary[0])
	}
	if summary[1].StoreName != "REWE" || summary[1].LatestPriceCents == nil ||
		*summary[1].LatestPriceCents != 139 || summary[1].Currency != "EUR" {
		t.Fatalf("summary[1] = %+v; want REWE 139 EUR", summary[1])
	}

	// A storeless bill is its own group, displayed as "—" with a nil store id.
	colaSummary, err := repo.StorePurchaseSummary(ctx, colaID)
	if err != nil {
		t.Fatalf("cola summary: %v", err)
	}
	if len(colaSummary) != 1 || colaSummary[0].StoreID != nil ||
		colaSummary[0].StoreName != "—" || colaSummary[0].LatestPriceCents == nil ||
		*colaSummary[0].LatestPriceCents != 200 {
		t.Fatalf("cola summary = %+v; want one storeless group at 200", colaSummary)
	}
}

func TestProductRepositoryListSortsByStats(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	repo := NewProductRepository(db)
	seedProductStats(t, db)

	page, err := repo.List(ctx, domain.ProductFilters{Sort: "best_price", Order: "desc"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	// Cola (200) before Milk (150); the never-bought product (NULL best)
	// sorts last on DESC.
	if page.Total != 3 || len(page.Items) != 3 {
		t.Fatalf("page = %d/%d; want 3/3", page.Total, len(page.Items))
	}
	if page.Items[0].Name != "Cola" || page.Items[1].Name != "Milk" || page.Items[2].Name != "Nothing" {
		t.Fatalf("order = %s, %s, %s; want Cola, Milk, Nothing",
			page.Items[0].Name, page.Items[1].Name, page.Items[2].Name)
	}
	if page.Items[2].TimesBought != 0 {
		t.Fatalf("nothing times_bought = %d; want 0", page.Items[2].TimesBought)
	}
}

func TestProductRepositoryMergeRedirectsItems(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	repo := NewProductRepository(db)
	_, milkID, colaID, _ := seedProductStats(t, db)

	merged, err := repo.Merge(ctx, milkID, colaID, domain.Product{Name: "Milk", Brand: "Alpen"})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if merged.ID != milkID || merged.Brand != "Alpen" {
		t.Fatalf("merged = %+v; want milk with the final fields", merged)
	}

	// The loser is gone; its history now belongs to the keeper.
	if _, err := repo.GetByID(ctx, colaID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("get merged-away product = %v; want ErrNotFound", err)
	}
	milk, err := repo.GetByID(ctx, milkID)
	if err != nil {
		t.Fatalf("get merged product: %v", err)
	}
	// 4 milk lines + cola's line: history combined, latest is now cola's
	// 200 EUR purchase (2026-04-10 beats 2026-03-10).
	if milk.TimesBought != 5 || milk.LatestPriceCents == nil ||
		*milk.LatestPriceCents != 200 || milk.PriceCurrency != "EUR" {
		t.Fatalf("merged stats = %+v; want 5 bought, latest 200 EUR", milk)
	}
	var orphaned int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM bill_items WHERE product_id = ?`, colaID).Scan(&orphaned); err != nil {
		t.Fatalf("count orphans: %v", err)
	}
	if orphaned != 0 {
		t.Fatalf("%d bill items still reference the deleted product", orphaned)
	}
}

// seedManualPurchase writes an account, a manual transaction and one item
// line linked to a product; it feeds the stats CTE's manual branch. Returns
// the transaction id.
func seedManualPurchase(t *testing.T, db *sql.DB, accountID, productID int64, date, currency string, price int64) int64 {
	t.Helper()
	res, err := db.Exec(`
		INSERT INTO transactions (account_id, kind, amount_cents, currency, description, date, created_at, updated_at)
		VALUES (?, 'expense', ?, ?, 'Manual purchase', ?, 100, 100)`,
		accountID, price, currency, date)
	if err != nil {
		t.Fatalf("seed transaction: %v", err)
	}
	txID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed transaction id: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO transaction_items (transaction_id, product_id, name, quantity, unit_price_cents, line_total_cents, created_at, updated_at)
		VALUES (?, ?, 'Milk', 1, ?, ?, 100, 100)`,
		txID, productID, price, price); err != nil {
		t.Fatalf("seed transaction item: %v", err)
	}
	return txID
}

func TestProductRepositoryStatsIncludeManualItems(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	repo := NewProductRepository(db)
	reweID, milkID, _, _ := seedProductStats(t, db)

	accountID, err := db.Exec(`INSERT INTO accounts (name, type, currency, created_at, updated_at) VALUES ('Wallet', 'cash', 'EUR', 100, 100)`)
	if err != nil {
		t.Fatalf("seed account: %v", err)
	}
	accountIDv, _ := accountID.LastInsertId()

	// A manual purchase of milk at REWE is the most recent line overall.
	seedManualPurchase(t, db, accountIDv, milkID, "2026-05-10", "EUR", 189)

	milk, err := repo.GetByID(ctx, milkID)
	if err != nil {
		t.Fatalf("get milk: %v", err)
	}
	if milk.TimesBought != 5 || milk.LastPurchaseDate != "2026-05-10" {
		t.Fatalf("milk stats = %d bought, last %q; want 5, 2026-05-10", milk.TimesBought, milk.LastPurchaseDate)
	}
	if milk.LatestPriceCents == nil || *milk.LatestPriceCents != 189 || milk.PriceCurrency != "EUR" {
		t.Fatalf("milk latest = %v %q; want 189 EUR (manual line wins recency)", milk.LatestPriceCents, milk.PriceCurrency)
	}

	// The manual line participates in the per-store comparison too.
	prices, err := repo.StorePrices(ctx, milkID, "EUR")
	if err != nil {
		t.Fatalf("store prices: %v", err)
	}
	// REWE bill history + the manual purchase (storeless, its own group).
	foundManual := false
	for _, p := range prices {
		if p.StoreID == nil && p.LatestPriceCents != nil && *p.LatestPriceCents == 189 {
			foundManual = true
		}
	}
	if !foundManual {
		t.Fatalf("store prices = %+v; want a storeless manual group at 189", prices)
	}
	_ = reweID
}

func TestProductRepositoryUpdateLeavesLinesUntouched(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	repo := NewProductRepository(db)
	_, milkID, _, _ := seedProductStats(t, db)

	seedCat := func(name string) int64 {
		t.Helper()
		res, err := db.Exec(`INSERT INTO categories (name, created_at) VALUES (?, 100)`, name)
		if err != nil {
			t.Fatalf("seed category %q: %v", name, err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatalf("seed category id: %v", err)
		}
		return id
	}
	drinksCat, groceryCat := seedCat("Drinks"), seedCat("Grocery")

	// A bill line carrying its own unit/category — the snapshot Update must
	// not touch, unlike the seed lines (empty unit, NULL category).
	res, err := db.Exec(`
		INSERT INTO bills (store_id, market_name, date, currency, status, created_at, updated_at)
		VALUES (NULL, 'Market', '2026-06-10', 'EUR', 'accepted', 100, 100)`)
	if err != nil {
		t.Fatalf("seed bill: %v", err)
	}
	billID, _ := res.LastInsertId()
	res, err = db.Exec(`
		INSERT INTO bill_items (bill_id, product_id, name, unit, category_id, unit_price_cents, line_total_cents)
		VALUES (?, ?, 'Mlk 1L', 'ml', ?, 111, 111)`,
		billID, milkID, drinksCat)
	if err != nil {
		t.Fatalf("seed snapshot bill item: %v", err)
	}
	snapshotItemID, _ := res.LastInsertId()

	// A manual purchase line for the same product.
	res, err = db.Exec(`INSERT INTO accounts (name, type, currency, created_at, updated_at) VALUES ('Wallet', 'cash', 'EUR', 100, 100)`)
	if err != nil {
		t.Fatalf("seed account: %v", err)
	}
	accountID, _ := res.LastInsertId()
	seedManualPurchase(t, db, accountID, milkID, "2026-05-10", "EUR", 189)

	updated, err := repo.Update(ctx, domain.Product{
		ID: milkID, Name: "Milk Fresh", Unit: "l", CategoryID: &groceryCat,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Name != "Milk Fresh" || updated.Unit != "l" ||
		updated.CategoryID == nil || *updated.CategoryID != groceryCat {
		t.Fatalf("updated product = %+v; want Milk Fresh / l / grocery", updated)
	}

	// The products row changed, but every linked line kept its snapshot.
	var bName, bUnit string
	var bCat sql.NullInt64
	if err := db.QueryRow(`
		SELECT name, unit, category_id FROM bill_items WHERE id = ?`, snapshotItemID).
		Scan(&bName, &bUnit, &bCat); err != nil {
		t.Fatalf("scan bill item: %v", err)
	}
	if bName != "Mlk 1L" || bUnit != "ml" || !bCat.Valid || bCat.Int64 != drinksCat {
		t.Fatalf("bill item = %q/%q/%v; want Mlk 1L / ml / drinks", bName, bUnit, bCat)
	}
	var rewrites int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM bill_items WHERE product_id = ? AND name = 'Milk Fresh'`,
		milkID).Scan(&rewrites); err != nil {
		t.Fatalf("count rewritten bill items: %v", err)
	}
	if rewrites != 0 {
		t.Fatalf("%d bill items were rewritten to the product's new name", rewrites)
	}
	var tName string
	if err := db.QueryRow(`
		SELECT name FROM transaction_items WHERE product_id = ?`, milkID).Scan(&tName); err != nil {
		t.Fatalf("scan transaction item: %v", err)
	}
	if tName != "Milk" {
		t.Fatalf("transaction item name = %q; want the seeded snapshot %q", tName, "Milk")
	}

	// Unknown id: no row, no side effects.
	if _, err := repo.Update(ctx, domain.Product{ID: 424242, Name: "Ghost"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("update unknown = %v; want ErrNotFound", err)
	}
}

// TestProductRepositoryListGrouped covers the grouped catalogue view: family
// grouping by the mapping's generic name (raw-name fallback for unmapped
// products), the name filter working on the group key, the category filter
// keeping whole groups, the family price aggregate and group-level sorting.
func TestProductRepositoryListGrouped(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	repo := NewProductRepository(db)
	_, milkID, _, _ := seedProductStats(t, db)

	// A second milk that shares Milk's family: "Oat Milk" maps to the generic
	// "Milk" — a family of two. Cola and Nothing stay unmapped (single-member
	// groups under their raw names).
	seed := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatalf("seed %q: %v", query, err)
		}
	}
	oatID := int64(0)
	res, err := db.Exec(`INSERT INTO products (name, created_at, updated_at) VALUES ('Oat Milk', 100, 100)`)
	if err != nil {
		t.Fatalf("seed oat milk: %v", err)
	}
	oatID, _ = res.LastInsertId()
	seed(`INSERT INTO product_name_mappings (raw_name, standard_name, generic_name, source, created_at, updated_at)
	      VALUES ('Milk', 'Milk', 'Milk', 'user', 100, 100)`)
	seed(`INSERT INTO product_name_mappings (raw_name, standard_name, generic_name, source, created_at, updated_at)
	      VALUES ('Oat Milk', 'Oat Milk', 'Milk', 'ai', 100, 100)`)

	page, err := repo.ListGrouped(ctx, domain.ProductFilters{Sort: "name"})
	if err != nil {
		t.Fatalf("list grouped: %v", err)
	}
	// Groups name-ascending: Cola, Milk (Milk + Oat Milk), Nothing.
	if page.Total != 3 || len(page.Items) != 3 {
		t.Fatalf("groups = %d/%d; want 3/3", page.Total, len(page.Items))
	}
	milk := page.Items[1]
	if milk.GenericName != "Milk" || milk.ProductCount != 2 || milk.TimesBought != 4 {
		t.Fatalf("milk group = %+v; want family Milk, 2 products, 4 bought", milk)
	}
	if len(milk.Items) != 2 || milk.Items[0].Name != "Milk" || milk.Items[1].Name != "Oat Milk" {
		t.Fatalf("milk members = %+v; want Milk + Oat Milk", milk.Items)
	}
	// Family price: the members' own aggregates averaged in the group's
	// currency — USD, Oat Milk's most recent purchase's currency is none (it
	// was never bought), so the group's only priced member decides. Milk's
	// avg is 160 → the family average is 160, best 150.
	if milk.PriceCurrency != "USD" || milk.AvgPriceCents == nil || *milk.AvgPriceCents != 160 {
		t.Fatalf("milk price = %v %q; want 160 USD", milk.AvgPriceCents, milk.PriceCurrency)
	}
	if milk.BestPriceCents == nil || *milk.BestPriceCents != 150 {
		t.Fatalf("milk best = %v; want 150", milk.BestPriceCents)
	}
	// The mini table's store column: Milk's latest purchase is the Aldi line.
	if milk.Items[0].LastStoreName != "Aldi" {
		t.Fatalf("milk last store = %q; want Aldi", milk.Items[0].LastStoreName)
	}
	if page.Items[0].GenericName != "Cola" || page.Items[2].GenericName != "Nothing" {
		t.Fatalf("group order = %s, %s; want Cola first, Nothing last",
			page.Items[0].GenericName, page.Items[2].GenericName)
	}
	// A never-bought single member: no currency, no prices, no store.
	if page.Items[2].Items[0].LastStoreName != "" || page.Items[2].PriceCurrency != "" {
		t.Fatalf("nothing group = %+v; want empty price/store", page.Items[2])
	}

	// Name filter matches the group key, not the member names.
	page, err = repo.ListGrouped(ctx, domain.ProductFilters{Name: "ilk"})
	if err != nil {
		t.Fatalf("list grouped filtered: %v", err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].GenericName != "Milk" {
		t.Fatalf("filtered groups = %+v; want only the Milk family", page.Items)
	}

	// Group-level sorting: times_bought descending puts the Milk family (4)
	// first; the never-bought group (0) last.
	page, err = repo.ListGrouped(ctx, domain.ProductFilters{Sort: "times_bought", Order: "desc"})
	if err != nil {
		t.Fatalf("list grouped sorted: %v", err)
	}
	if page.Items[0].GenericName != "Milk" || page.Items[len(page.Items)-1].GenericName != "Nothing" {
		t.Fatalf("times_bought order = %s…%s; want Milk first, Nothing last",
			page.Items[0].GenericName, page.Items[len(page.Items)-1].GenericName)
	}

	// Paging works on groups: one group per page, name-ascending → the Milk
	// family is page 2.
	page, err = repo.ListGrouped(ctx, domain.ProductFilters{Limit: 1, Offset: 1})
	if err != nil {
		t.Fatalf("list grouped paged: %v", err)
	}
	if page.Total != 3 || len(page.Items) != 1 || page.Items[0].GenericName != "Milk" {
		t.Fatalf("paged groups = %+v; want total 3, the Milk family on page 2", page.Items)
	}

	// The embedded members are the catalogue rows themselves.
	if page.Items[0].Items[0].ID != milkID || page.Items[0].Items[1].ID != oatID {
		t.Fatalf("milk member ids = %d, %d; want %d, %d",
			page.Items[0].Items[0].ID, page.Items[0].Items[1].ID, milkID, oatID)
	}
}
