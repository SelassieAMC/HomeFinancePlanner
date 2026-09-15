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

// TransactionFilters re-exports the shared domain filter type for callers of
// this package.
type TransactionFilters = domain.TransactionFilters

const transactionColumns = `
	t.id, t.account_id, t.category_id, t.kind, t.amount_cents, t.currency, t.description, t.date,
	t.created_at, t.updated_at,
	t.bill_id, t.store_id, st.name AS store_name,
	(SELECT COUNT(*) FROM transaction_items ti WHERE ti.transaction_id = t.id) AS item_count`

const transactionFrom = `
	FROM transactions t
	LEFT JOIN stores st ON st.id = t.store_id`

const transactionItemColumns = `
	ti.id, ti.transaction_id, ti.product_id, p.name AS product_name, ti.name, ti.brand, ti.unit, ti.category_id,
	ti.quantity, ti.unit_price_cents, ti.discount_cents, ti.line_total_cents, ti.created_at, ti.updated_at`

const transactionItemFrom = `
	FROM transaction_items ti
	LEFT JOIN products p ON p.id = ti.product_id`

// TransactionRepository is the SQLite-backed implementation of the
// transaction store.
type TransactionRepository struct{ db *sql.DB }

func NewTransactionRepository(db *sql.DB) *TransactionRepository {
	return &TransactionRepository{db: db}
}

func (r *TransactionRepository) List(ctx context.Context, f TransactionFilters) ([]domain.Transaction, error) {
	where := []string{"1 = 1"}
	args := []any{}

	if f.AccountID > 0 {
		where = append(where, "t.account_id = ?")
		args = append(args, f.AccountID)
	}
	if f.Category != nil {
		where = append(where, "t.category_id = ?")
		args = append(args, *f.Category)
	}
	if f.Month != "" {
		where = append(where, "substr(t.date, 1, 7) = ?")
		args = append(args, f.Month)
	}
	if f.Kind != "" {
		where = append(where, "t.kind = ?")
		args = append(args, string(f.Kind))
	}

	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	if f.Offset < 0 {
		f.Offset = 0
	}

	q := `SELECT ` + transactionColumns + transactionFrom + `
		WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY t.date DESC, t.id DESC
		LIMIT ? OFFSET ?`
	args = append(args, limit, f.Offset)

	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list transactions: %w", err)
	}
	defer rows.Close()

	out := []domain.Transaction{}
	for rows.Next() {
		t, err := scanTransaction(rows)
		if err != nil {
			return nil, fmt.Errorf("scan transaction: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetByID returns one transaction with its item lines loaded (Items and
// ItemsTotalCents filled; List leaves both empty).
func (r *TransactionRepository) GetByID(ctx context.Context, id int64) (domain.Transaction, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+transactionColumns+transactionFrom+` WHERE t.id = ?`, id)

	t, err := scanTransaction(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Transaction{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Transaction{}, fmt.Errorf("get transaction %d: %w", id, err)
	}
	items, err := r.itemsForTransaction(ctx, id)
	if err != nil {
		return domain.Transaction{}, err
	}
	t.Items = items
	for _, it := range items {
		t.ItemsTotalCents += it.LineTotalCents
	}
	return t, nil
}

func (r *TransactionRepository) Create(ctx context.Context, t domain.Transaction) (domain.Transaction, error) {
	now := time.Now().Unix()
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO transactions
			(account_id, category_id, kind, amount_cents, currency, description, date, created_at, updated_at, bill_id, store_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.AccountID, nullableID(t.CategoryID), string(t.Kind), t.AmountCents,
		t.Currency, t.Description, t.Date, now, now, nullableID(t.BillID), nullableID(t.StoreID))
	if err != nil {
		return domain.Transaction{}, mapWriteError("create transaction", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return domain.Transaction{}, fmt.Errorf("transaction insert id: %w", err)
	}
	return r.GetByID(ctx, id)
}

// CreateWithItems inserts the transaction row and its item lines atomically,
// with product_id already resolved by the service. A plain Create keeps its
// no-items behaviour for the bill sync path.
func (r *TransactionRepository) CreateWithItems(ctx context.Context, t domain.Transaction, items []domain.TransactionItem) (domain.Transaction, error) {
	now := time.Now().Unix()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Transaction{}, fmt.Errorf("begin transaction insert: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		INSERT INTO transactions
			(account_id, category_id, kind, amount_cents, currency, description, date, created_at, updated_at, store_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.AccountID, nullableID(t.CategoryID), string(t.Kind), t.AmountCents,
		t.Currency, t.Description, t.Date, now, now, nullableID(t.StoreID))
	if err != nil {
		return domain.Transaction{}, mapWriteError("create transaction", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return domain.Transaction{}, fmt.Errorf("transaction insert id: %w", err)
	}
	if err := insertTransactionItems(ctx, tx, id, items, now); err != nil {
		return domain.Transaction{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Transaction{}, fmt.Errorf("commit transaction insert: %w", err)
	}
	return r.GetByID(ctx, id)
}

func (r *TransactionRepository) Update(ctx context.Context, t domain.Transaction) (domain.Transaction, error) {
	res, err := r.db.ExecContext(ctx, `
		UPDATE transactions
		SET account_id = ?, category_id = ?, kind = ?, amount_cents = ?, currency = ?,
		    description = ?, date = ?, updated_at = ?
		WHERE id = ?`,
		t.AccountID, nullableID(t.CategoryID), string(t.Kind), t.AmountCents,
		t.Currency, t.Description, t.Date, time.Now().Unix(), t.ID)
	if err != nil {
		return domain.Transaction{}, mapWriteError("update transaction", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.Transaction{}, domain.ErrNotFound
	}
	return r.GetByID(ctx, t.ID)
}

// UpdateWithItems rewrites the transaction row (bill_id and store_id are
// round-tripped untouched — a bill-linked row is never demoted to a manual
// one) and replaces every item line, like BillRepository.Update replaces
// bill items.
func (r *TransactionRepository) UpdateWithItems(ctx context.Context, t domain.Transaction, items []domain.TransactionItem) (domain.Transaction, error) {
	now := time.Now().Unix()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Transaction{}, fmt.Errorf("begin transaction update: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		UPDATE transactions
		SET account_id = ?, category_id = ?, kind = ?, amount_cents = ?, currency = ?,
		    description = ?, date = ?, updated_at = ?, store_id = ?
		WHERE id = ?`,
		t.AccountID, nullableID(t.CategoryID), string(t.Kind), t.AmountCents,
		t.Currency, t.Description, t.Date, now, nullableID(t.StoreID), t.ID)
	if err != nil {
		return domain.Transaction{}, mapWriteError("update transaction", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.Transaction{}, domain.ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM transaction_items WHERE transaction_id = ?`, t.ID); err != nil {
		return domain.Transaction{}, fmt.Errorf("clear transaction items: %w", err)
	}
	if err := insertTransactionItems(ctx, tx, t.ID, items, now); err != nil {
		return domain.Transaction{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Transaction{}, fmt.Errorf("commit transaction update: %w", err)
	}
	return r.GetByID(ctx, t.ID)
}

func (r *TransactionRepository) Delete(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM transactions WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete transaction %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// itemsForTransaction loads the item lines of one transaction, oldest first.
func (r *TransactionRepository) itemsForTransaction(ctx context.Context, transactionID int64) ([]domain.TransactionItem, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+transactionItemColumns+transactionItemFrom+`
		WHERE ti.transaction_id = ?
		ORDER BY ti.id ASC`, transactionID)
	if err != nil {
		return nil, fmt.Errorf("list transaction items: %w", err)
	}
	defer rows.Close()

	items := []domain.TransactionItem{}
	for rows.Next() {
		it, err := scanTransactionItem(rows)
		if err != nil {
			return nil, fmt.Errorf("scan transaction item: %w", err)
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// insertTransactionItems writes every line inside the caller's transaction,
// carrying the resolved product links.
func insertTransactionItems(ctx context.Context, tx *sql.Tx, transactionID int64, items []domain.TransactionItem, now int64) error {
	for _, it := range items {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO transaction_items
				(transaction_id, product_id, name, brand, unit, category_id,
				 quantity, unit_price_cents, discount_cents, line_total_cents, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			transactionID, nullableID(it.ProductID), it.Name, it.Brand, it.Unit, nullableID(it.CategoryID),
			it.Quantity, it.UnitPriceCents, it.DiscountCents, it.LineTotalCents, now, now); err != nil {
			return mapWriteError("insert transaction item", err)
		}
	}
	return nil
}

// nullableID converts a *int64 into a driver value (nil for SQL NULL).
func nullableID(id *int64) any {
	if id == nil {
		return nil
	}
	return *id
}

// scanTransaction scans one row from either *sql.Rows or *sql.Row.
func scanTransaction(row interface{ Scan(dest ...any) error }) (domain.Transaction, error) {
	var (
		t              domain.Transaction
		category       sql.NullInt64
		kind           string
		createdAt, upd int64
		billID         sql.NullInt64
		storeID        sql.NullInt64
		storeName      sql.NullString
		itemCount      int
	)
	if err := row.Scan(&t.ID, &t.AccountID, &category, &kind, &t.AmountCents,
		&t.Currency, &t.Description, &t.Date, &createdAt, &upd,
		&billID, &storeID, &storeName, &itemCount); err != nil {
		return domain.Transaction{}, err
	}
	if category.Valid {
		v := category.Int64
		t.CategoryID = &v
	}
	if billID.Valid {
		v := billID.Int64
		t.BillID = &v
	}
	if storeID.Valid {
		v := storeID.Int64
		t.StoreID = &v
	}
	t.StoreName = storeName.String
	t.ItemCount = itemCount
	t.Kind = domain.TransactionKind(kind)
	t.CreatedAt = time.Unix(createdAt, 0).UTC()
	t.UpdatedAt = time.Unix(upd, 0).UTC()
	return t, nil
}

// scanTransactionItem scans one item row (product name joined).
func scanTransactionItem(row interface{ Scan(dest ...any) error }) (domain.TransactionItem, error) {
	var (
		it        domain.TransactionItem
		productID sql.NullInt64
		prodName  sql.NullString
		category  sql.NullInt64
		createdAt int64
		updatedAt int64
	)
	if err := row.Scan(&it.ID, &it.TransactionID, &productID, &prodName, &it.Name,
		&it.Brand, &it.Unit, &category, &it.Quantity, &it.UnitPriceCents,
		&it.DiscountCents, &it.LineTotalCents, &createdAt, &updatedAt); err != nil {
		return domain.TransactionItem{}, err
	}
	if productID.Valid {
		v := productID.Int64
		it.ProductID = &v
	}
	it.ProductName = prodName.String
	if category.Valid {
		v := category.Int64
		it.CategoryID = &v
	}
	it.CreatedAt = time.Unix(createdAt, 0).UTC()
	it.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return it, nil
}
