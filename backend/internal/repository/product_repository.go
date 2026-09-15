package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"home-finance-planner/backend/internal/domain"
)

// Purchase stats are derived from the linked bill lines of accepted bills
// (deposit returns are excluded — they are money back, not purchases). One
// CTE computes every product's stats in a single pass over bill_items ⋈ bills,
// instead of the per-row correlated subqueries this replaced:
//
//	ranked — each line, numbered per product by recency (bill date, bill id,
//	         item id; the same top-1 rule the old subqueries used)
//	latest — the most recent line's price and currency, per product
//	stats  — the aggregates; avg/best are scoped to that latest currency so
//	         unlike currencies never mix
//
// productColumns joins products against it (LEFT JOIN keeps never-bought
// products, hence the COALESCE on the count). Scope is injectable so single-
// product reads stay index-driven instead of aggregating the whole table:
// the scope's placeholder(s) bind first (the CTE is evaluated first).
const productStatsCTE = `
WITH ranked AS (
	SELECT bi.product_id, b.date AS bill_date, bi.unit_price_cents, b.currency,
	       ROW_NUMBER() OVER (PARTITION BY bi.product_id
	         ORDER BY b.date DESC, b.id DESC, bi.id DESC) AS rn
	FROM bill_items bi JOIN bills b ON b.id = bi.bill_id
	WHERE b.status = 'accepted' AND bi.is_return = 0 [scope]
),
latest AS (
	SELECT product_id,
	       MAX(CASE WHEN rn = 1 THEN unit_price_cents END) AS latest_price_cents,
	       MAX(CASE WHEN rn = 1 THEN currency END) AS price_currency
	FROM ranked
	GROUP BY product_id
),
stats AS (
	SELECT r.product_id,
	       COUNT(*) AS times_bought,
	       MAX(r.bill_date) AS last_purchase_date,
	       l.latest_price_cents,
	       l.price_currency,
	       CAST(ROUND(AVG(CASE WHEN r.currency = l.price_currency
	                       THEN r.unit_price_cents END)) AS INTEGER) AS avg_price_cents,
	       MIN(CASE WHEN r.currency = l.price_currency
	                THEN r.unit_price_cents END) AS best_price_cents
	FROM ranked r JOIN latest l ON l.product_id = r.product_id
	GROUP BY r.product_id
)`

// CTE scopes: all products (List), one by id (GetByID), one by name
// (FindByName — the subselect keeps the lookup index-driven without knowing
// the id up front).
const (
	productScopeNone   = ""
	productScopeByID   = " AND bi.product_id = ?"
	productScopeByName = " AND bi.product_id = (SELECT id FROM products WHERE name = ? COLLATE NOCASE)"
)

// productColumns + productFrom read a product together with its derived
// purchase stats (see productStatsCTE). The column order matches scanProduct.
const productColumns = `
	p.id, p.name, p.brand, p.unit, p.category_id, c.name, p.description, p.image_path,
	p.created_at, p.updated_at,
	COALESCE(s.times_bought, 0) AS times_bought,
	s.last_purchase_date, s.latest_price_cents, s.price_currency,
	s.avg_price_cents, s.best_price_cents`

const productFrom = `
	FROM products p
	LEFT JOIN categories c ON c.id = p.category_id
	LEFT JOIN stats s ON s.product_id = p.id`

// productQuery assembles the stats CTE for a scope with the product columns
// after it.
func productQuery(scope string) string {
	return strings.Replace(productStatsCTE, "[scope]", scope, 1) + `
SELECT` + productColumns + productFrom
}

// productSortColumns whitelists the sortable ORDER BY expressions. Anything
// else falls back to name — a raw query string never reaches the ORDER BY
// clause.
var productSortColumns = map[string]string{
	"name":          "p.name COLLATE NOCASE",
	"updated_at":    "p.updated_at",
	"created_at":    "p.created_at",
	"times_bought":  "times_bought",
	"last_purchase": "last_purchase_date",
	"avg_price":     "avg_price_cents",
	"best_price":    "best_price_cents",
	"latest_price":  "latest_price_cents",
}

// ProductRepository is the SQLite-backed implementation of the product
// contract. Names are unique case-insensitively (idx_products_name COLLATE
// NOCASE), which is what BillService's find-or-create resolves through on a
// race. There is no Delete: products are never removed.
type ProductRepository struct{ db *sql.DB }

func NewProductRepository(db *sql.DB) *ProductRepository { return &ProductRepository{db: db} }

// List returns the matching product page. Sorting keys are whitelisted; the
// trailing p.id tie-breaker keeps paging stable.
func (r *ProductRepository) List(ctx context.Context, f domain.ProductFilters) (domain.ProductPage, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	sortCol, ok := productSortColumns[f.Sort]
	if !ok {
		sortCol = productSortColumns["name"]
	}
	order := "ASC"
	if strings.EqualFold(f.Order, "desc") {
		order = "DESC"
	}

	where := []string{"1 = 1"}
	args := []any{}
	if f.Name != "" {
		where = append(where, "p.name LIKE '%' || ? || '%'")
		args = append(args, f.Name)
	}
	if f.CategoryID != nil {
		where = append(where, "p.category_id = ?")
		args = append(args, *f.CategoryID)
	}
	whereSQL := joinAND(where)

	var total int64
	if err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM products p WHERE `+whereSQL, args...).Scan(&total); err != nil {
		return domain.ProductPage{}, fmt.Errorf("count products: %w", err)
	}

	rows, err := r.db.QueryContext(ctx,
		productQuery(productScopeNone)+` WHERE `+whereSQL+`
		 ORDER BY `+sortCol+` `+order+`, p.id ASC
		 LIMIT ? OFFSET ?`, append(args, limit, f.Offset)...)
	if err != nil {
		return domain.ProductPage{}, fmt.Errorf("list products: %w", err)
	}
	defer rows.Close()

	items := []domain.Product{}
	for rows.Next() {
		p, err := scanProduct(rows)
		if err != nil {
			return domain.ProductPage{}, fmt.Errorf("scan product row: %w", err)
		}
		items = append(items, p)
	}
	if err := rows.Err(); err != nil {
		return domain.ProductPage{}, err
	}
	return domain.ProductPage{Items: items, Total: total, Limit: limit, Offset: f.Offset}, nil
}

// GetByID returns one product with its purchase stats, or domain.ErrNotFound.
// The id scopes the stats CTE (bound first) so the read stays index-driven.
func (r *ProductRepository) GetByID(ctx context.Context, id int64) (domain.Product, error) {
	row := r.db.QueryRowContext(ctx,
		productQuery(productScopeByID)+` WHERE p.id = ?`, id, id)
	p, err := scanProduct(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Product{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Product{}, fmt.Errorf("get product %d: %w", id, err)
	}
	return p, nil
}

// FindByName returns the product whose name matches case-insensitively.
func (r *ProductRepository) FindByName(ctx context.Context, name string) (domain.Product, error) {
	row := r.db.QueryRowContext(ctx,
		productQuery(productScopeByName)+` WHERE p.name = ? COLLATE NOCASE`, name, name)
	p, err := scanProduct(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Product{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Product{}, fmt.Errorf("find product %q: %w", name, err)
	}
	return p, nil
}

// storePricesCTE lists the latest price a product was bought at, grouped by
// store, in one pass: ranked numbers each accepted non-return line per store
// (partitioning by store id treats NULL-store bills as their own group — the
// `store_id IS store_id` workaround the correlated version needed is gone),
// then the group select takes the top-1 price/currency and the last date.
// [currencyFilter] is either empty (all currencies) or an extra `b.currency
// = ?` filter, whose placeholder binds after the product id.
const storePricesCTE = `
WITH ranked AS (
	SELECT b.store_id, COALESCE(st.name, '—') AS store_name, b.date,
	       bi.unit_price_cents, b.currency,
	       ROW_NUMBER() OVER (PARTITION BY b.store_id
	         ORDER BY b.date DESC, b.id DESC, bi.id DESC) AS rn
	FROM bills b
	JOIN bill_items bi ON bi.bill_id = b.id
	LEFT JOIN stores st ON st.id = b.store_id
	WHERE bi.product_id = ? AND b.status = 'accepted' AND bi.is_return = 0 [currencyFilter]
)
SELECT store_id, store_name,
       MAX(CASE WHEN rn = 1 THEN unit_price_cents END) AS latest_price_cents,
       MAX(CASE WHEN rn = 1 THEN currency END) AS currency,
       MAX(date) AS last_purchase_date
FROM ranked
GROUP BY store_id, store_name
ORDER BY last_purchase_date DESC, store_name ASC`

const storeCurrencyFilter = " AND b.currency = ?"

// StorePrices returns the latest price a product was bought at, grouped by
// store. currency scopes the amounts to a single currency (the service passes
// the product's most recent purchase currency) so unlike currencies never mix.
func (r *ProductRepository) StorePrices(ctx context.Context, id int64, currency string) ([]domain.ProductStorePrice, error) {
	return r.storePrices(ctx,
		strings.Replace(storePricesCTE, "[currencyFilter]", storeCurrencyFilter, 1),
		id, currency, "list product store prices")
}

// StorePurchaseSummary is StorePrices without the currency scope: the latest
// price and its currency per store across all currencies. The merge check
// needs it to compare two products' pricing at their shared stores, where the
// two products may have been priced in different currencies.
func (r *ProductRepository) StorePurchaseSummary(ctx context.Context, id int64) ([]domain.ProductStorePrice, error) {
	return r.storePrices(ctx,
		strings.Replace(storePricesCTE, "[currencyFilter]", "", 1),
		id, "", "list product store purchases")
}

// storePrices runs the shared CTE and scans its rows.
func (r *ProductRepository) storePrices(ctx context.Context, query string, id int64, currency, op string) ([]domain.ProductStorePrice, error) {
	args := []any{id}
	if currency != "" {
		args = append(args, currency)
	}
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	defer rows.Close()

	items := []domain.ProductStorePrice{}
	for rows.Next() {
		var (
			storeID      sql.NullInt64
			storeName    string
			latest       sql.NullInt64
			currencyCode sql.NullString
			lastPurchase sql.NullString
		)
		if err := rows.Scan(&storeID, &storeName, &latest, &currencyCode, &lastPurchase); err != nil {
			return nil, fmt.Errorf("scan product store price: %w", err)
		}
		row := domain.ProductStorePrice{
			StoreName:        storeName,
			Currency:         currencyCode.String,
			LastPurchaseDate: lastPurchase.String,
		}
		if storeID.Valid {
			v := storeID.Int64
			row.StoreID = &v
		}
		if latest.Valid {
			v := latest.Int64
			row.LatestPriceCents = &v
		}
		items = append(items, row)
	}
	return items, rows.Err()
}

// Merge redirects every bill item of drop to keep, deletes drop, and rewrites
// keep's editable fields to final — one transaction, so the name index never
// sees both rows under the same NOCASE name (drop is deleted before keep is
// renamed). Redirected items get the kept product's final name/unit/category
// straight away (unit keeps its historical line value when final.unit is
// empty), mirroring Update's propagation semantics. image_path is untouched:
// photo files are owned by the service, which transfers or removes them after
// the transaction commits.
func (r *ProductRepository) Merge(ctx context.Context, keepID, dropID int64, final domain.Product) (domain.Product, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Product{}, fmt.Errorf("begin product merge: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		UPDATE bill_items
		SET product_id = ?,
		    name = ?,
		    unit = CASE WHEN TRIM(?) = '' THEN unit ELSE ? END,
		    category_id = ?
		WHERE product_id = ?`,
		keepID, final.Name, final.Unit, final.Unit, final.CategoryID, dropID); err != nil {
		tx.Rollback()
		return domain.Product{}, fmt.Errorf("redirect merged product items: %w", err)
	}

	res, err := tx.ExecContext(ctx, `DELETE FROM products WHERE id = ?`, dropID)
	if err != nil {
		tx.Rollback()
		return domain.Product{}, mapWriteError("delete merged product", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		tx.Rollback()
		return domain.Product{}, domain.ErrNotFound
	}

	res, err = tx.ExecContext(ctx, `
		UPDATE products SET name = ?, brand = ?, unit = ?, category_id = ?, description = ?, updated_at = ?
		WHERE id = ?`,
		final.Name, final.Brand, final.Unit, final.CategoryID, final.Description, time.Now().Unix(), keepID)
	if err != nil {
		tx.Rollback()
		return domain.Product{}, mapWriteError("update merged product", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		tx.Rollback()
		return domain.Product{}, domain.ErrNotFound
	}

	if err := tx.Commit(); err != nil {
		return domain.Product{}, fmt.Errorf("commit product merge: %w", err)
	}
	return r.GetByID(ctx, keepID)
}

// Create inserts a product and returns it with its id and timestamps filled
// in. A name already in use (case-insensitively) surfaces as ErrConflict —
// the find-or-create race resolves through it.
func (r *ProductRepository) Create(ctx context.Context, p domain.Product) (domain.Product, error) {
	now := time.Now().Unix()
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO products (name, brand, unit, category_id, description, image_path, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, '', ?, ?)`,
		p.Name, p.Brand, p.Unit, p.CategoryID, p.Description, now, now)
	if err != nil {
		return domain.Product{}, mapWriteError("create product", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return domain.Product{}, fmt.Errorf("product insert id: %w", err)
	}
	return r.GetByID(ctx, id)
}

// Update rewrites the user-editable fields and propagates name/unit/category
// to the linked bill items in one transaction (their names are not snapshots,
// unlike bills' market_name). A cleared unit keeps historical line units.
// It deliberately never touches image_path — photo files are owned by
// SetPhoto only.
func (r *ProductRepository) Update(ctx context.Context, p domain.Product) (domain.Product, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Product{}, fmt.Errorf("begin product update: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		UPDATE products SET name = ?, brand = ?, unit = ?, category_id = ?, description = ?, updated_at = ?
		WHERE id = ?`,
		p.Name, p.Brand, p.Unit, p.CategoryID, p.Description, time.Now().Unix(), p.ID)
	if err != nil {
		tx.Rollback()
		return domain.Product{}, mapWriteError("update product", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		tx.Rollback()
		return domain.Product{}, domain.ErrNotFound
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE bill_items
		SET name = ?,
		    unit = CASE WHEN TRIM(?) = '' THEN unit ELSE ? END,
		    category_id = ?
		WHERE product_id = ?`,
		p.Name, p.Unit, p.Unit, p.CategoryID, p.ID); err != nil {
		tx.Rollback()
		return domain.Product{}, fmt.Errorf("propagate product to bill items: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return domain.Product{}, fmt.Errorf("commit product update: %w", err)
	}
	return r.GetByID(ctx, p.ID)
}

// SetPhoto stores (or clears, with an empty path) the photo file path.
func (r *ProductRepository) SetPhoto(ctx context.Context, id int64, imagePath string) (domain.Product, error) {
	res, err := r.db.ExecContext(ctx, `
		UPDATE products SET image_path = ?, updated_at = ? WHERE id = ?`,
		imagePath, time.Now().Unix(), id)
	if err != nil {
		return domain.Product{}, mapWriteError("set product photo", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.Product{}, domain.ErrNotFound
	}
	return r.GetByID(ctx, id)
}

func scanProduct(row interface{ Scan(dest ...any) error }) (domain.Product, error) {
	var (
		p             domain.Product
		imagePath     string
		createdAt     int64
		updatedAt     int64
		categoryName  sql.NullString
		lastPurchase  sql.NullString
		latestPrice   sql.NullInt64
		avgPrice      sql.NullInt64
		bestPrice     sql.NullInt64
		priceCurrency sql.NullString
	)
	if err := row.Scan(&p.ID, &p.Name, &p.Brand, &p.Unit, &p.CategoryID, &categoryName, &p.Description, &imagePath,
		&createdAt, &updatedAt, &p.TimesBought, &lastPurchase, &latestPrice, &priceCurrency, &avgPrice, &bestPrice); err != nil {
		return domain.Product{}, err
	}
	p.CategoryName = categoryName.String
	p.ImagePath = imagePath
	p.HasImage = imagePath != ""
	if lastPurchase.Valid {
		p.LastPurchaseDate = lastPurchase.String
	}
	if latestPrice.Valid {
		v := latestPrice.Int64
		p.LatestPriceCents = &v
	}
	if avgPrice.Valid {
		v := avgPrice.Int64
		p.AvgPriceCents = &v
	}
	if bestPrice.Valid {
		v := bestPrice.Int64
		p.BestPriceCents = &v
	}
	p.PriceCurrency = priceCurrency.String
	p.CreatedAt = time.Unix(createdAt, 0).UTC()
	p.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return p, nil
}
