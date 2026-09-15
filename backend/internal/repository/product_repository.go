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

// productColumns + productFrom read a product together with its display-only
// purchase stats (computed from the linked bill lines of accepted bills;
// deposit returns are excluded — they are money back, not purchases). Prices
// are native-currency amounts from the most recent purchase; the average is
// taken only over lines in that same currency so unlike currencies never mix.
const productColumns = `
	p.id, p.name, p.brand, p.unit, p.category_id, c.name, p.description, p.image_path,
	p.created_at, p.updated_at,
	(SELECT COUNT(*)
	   FROM bill_items bi JOIN bills b ON b.id = bi.bill_id
	   WHERE bi.product_id = p.id AND b.status = 'accepted' AND bi.is_return = 0) AS times_bought,
	(SELECT MAX(b.date)
	   FROM bill_items bi JOIN bills b ON b.id = bi.bill_id
	   WHERE bi.product_id = p.id AND b.status = 'accepted' AND bi.is_return = 0 AND b.date != '') AS last_purchase_date,
	(SELECT bi2.unit_price_cents
	   FROM bill_items bi2 JOIN bills b2 ON b2.id = bi2.bill_id
	   WHERE bi2.product_id = p.id AND b2.status = 'accepted' AND bi2.is_return = 0
	   ORDER BY b2.date DESC, b2.id DESC, bi2.id DESC LIMIT 1) AS latest_price_cents,
	(SELECT b2.currency
	   FROM bill_items bi2 JOIN bills b2 ON b2.id = bi2.bill_id
	   WHERE bi2.product_id = p.id AND b2.status = 'accepted' AND bi2.is_return = 0
	   ORDER BY b2.date DESC, b2.id DESC, bi2.id DESC LIMIT 1) AS price_currency,
	(SELECT CAST(ROUND(AVG(bi3.unit_price_cents)) AS INTEGER)
	   FROM bill_items bi3 JOIN bills b3 ON b3.id = bi3.bill_id
	   WHERE bi3.product_id = p.id AND b3.status = 'accepted' AND bi3.is_return = 0
	     AND b3.currency = (SELECT b4.currency
	        FROM bill_items bi4 JOIN bills b4 ON b4.id = bi4.bill_id
	        WHERE bi4.product_id = p.id AND b4.status = 'accepted' AND bi4.is_return = 0
	        ORDER BY b4.date DESC, b4.id DESC, bi4.id DESC LIMIT 1)) AS avg_price_cents,
	(SELECT MIN(bi5.unit_price_cents)
	   FROM bill_items bi5 JOIN bills b5 ON b5.id = bi5.bill_id
	   WHERE bi5.product_id = p.id AND b5.status = 'accepted' AND bi5.is_return = 0
	     AND b5.currency = (SELECT b6.currency
	        FROM bill_items bi6 JOIN bills b6 ON b6.id = bi6.bill_id
	        WHERE bi6.product_id = p.id AND b6.status = 'accepted' AND bi6.is_return = 0
	        ORDER BY b6.date DESC, b6.id DESC, bi6.id DESC LIMIT 1)) AS best_price_cents`

const productFrom = `FROM products p LEFT JOIN categories c ON c.id = p.category_id`

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
		`SELECT `+productColumns+` `+productFrom+` WHERE `+whereSQL+`
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
func (r *ProductRepository) GetByID(ctx context.Context, id int64) (domain.Product, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+productColumns+` `+productFrom+` WHERE p.id = ?`, id)
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
		`SELECT `+productColumns+` `+productFrom+` WHERE p.name = ? COLLATE NOCASE`, name)
	p, err := scanProduct(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Product{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Product{}, fmt.Errorf("find product %q: %w", name, err)
	}
	return p, nil
}

// StorePrices returns the latest price a product was bought at, grouped by
// store. currency scopes the amounts to a single currency (the service passes
// the product's most recent purchase currency) so unlike currencies never mix.
// Bills without a store group under a NULL store id; the newest purchase per
// store wins, stores are ordered by their most recent purchase.
func (r *ProductRepository) StorePrices(ctx context.Context, id int64, currency string) ([]domain.ProductStorePrice, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT st.id, COALESCE(st.name, '—'),
		       (SELECT bi2.unit_price_cents
		          FROM bill_items bi2 JOIN bills b2 ON b2.id = bi2.bill_id
		          WHERE bi2.product_id = bi.product_id AND b2.status = 'accepted' AND bi2.is_return = 0
		            AND b2.store_id = b.store_id AND b2.currency = b.currency
		          ORDER BY b2.date DESC, b2.id DESC, bi2.id DESC LIMIT 1),
		       MAX(b.date)
		FROM bills b
		JOIN bill_items bi ON bi.bill_id = b.id
		LEFT JOIN stores st ON st.id = b.store_id
		WHERE bi.product_id = ? AND b.status = 'accepted' AND bi.is_return = 0 AND b.currency = ?
		GROUP BY b.store_id, st.id, st.name
		ORDER BY MAX(b.date) DESC, st.name ASC`, id, currency)
	if err != nil {
		return nil, fmt.Errorf("list product store prices: %w", err)
	}
	defer rows.Close()

	items := []domain.ProductStorePrice{}
	for rows.Next() {
		var (
			storeID      sql.NullInt64
			storeName    string
			latest       sql.NullInt64
			lastPurchase sql.NullString
		)
		if err := rows.Scan(&storeID, &storeName, &latest, &lastPurchase); err != nil {
			return nil, fmt.Errorf("scan product store price: %w", err)
		}
		row := domain.ProductStorePrice{StoreName: storeName, LastPurchaseDate: lastPurchase.String}
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
