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
	id, account_id, category_id, kind, amount_cents, currency, description, date,
	created_at, updated_at`

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
		where = append(where, "account_id = ?")
		args = append(args, f.AccountID)
	}
	if f.Category != nil {
		where = append(where, "category_id = ?")
		args = append(args, *f.Category)
	}
	if f.Month != "" {
		where = append(where, "substr(date, 1, 7) = ?")
		args = append(args, f.Month)
	}
	if f.Kind != "" {
		where = append(where, "kind = ?")
		args = append(args, string(f.Kind))
	}

	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	if f.Offset < 0 {
		f.Offset = 0
	}

	q := `SELECT ` + transactionColumns + ` FROM transactions
		WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY date DESC, id DESC
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

func (r *TransactionRepository) GetByID(ctx context.Context, id int64) (domain.Transaction, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+transactionColumns+` FROM transactions WHERE id = ?`, id)

	t, err := scanTransaction(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Transaction{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Transaction{}, fmt.Errorf("get transaction %d: %w", id, err)
	}
	return t, nil
}

func (r *TransactionRepository) Create(ctx context.Context, t domain.Transaction) (domain.Transaction, error) {
	now := time.Now().Unix()
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO transactions
			(account_id, category_id, kind, amount_cents, currency, description, date, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.AccountID, nullableID(t.CategoryID), string(t.Kind), t.AmountCents,
		t.Currency, t.Description, t.Date, now, now)
	if err != nil {
		return domain.Transaction{}, mapWriteError("create transaction", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return domain.Transaction{}, fmt.Errorf("transaction insert id: %w", err)
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
	)
	if err := row.Scan(&t.ID, &t.AccountID, &category, &kind, &t.AmountCents,
		&t.Currency, &t.Description, &t.Date, &createdAt, &upd); err != nil {
		return domain.Transaction{}, err
	}
	if category.Valid {
		v := category.Int64
		t.CategoryID = &v
	}
	t.Kind = domain.TransactionKind(kind)
	t.CreatedAt = time.Unix(createdAt, 0).UTC()
	t.UpdatedAt = time.Unix(upd, 0).UTC()
	return t, nil
}
