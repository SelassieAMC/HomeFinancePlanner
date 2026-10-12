package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"home-finance-planner/backend/internal/domain"
)

// BudgetRepository is the SQLite-backed implementation of the budget store.
type BudgetRepository struct{ db *sql.DB }

func NewBudgetRepository(db *sql.DB) *BudgetRepository { return &BudgetRepository{db: db} }

// budgetColumns is the shared projection for every budget read; scanBudget
// consumes it in this exact order.
const budgetColumns = `id, category_id, amount_cents, status, cadence, month, closed_at, created_at, updated_at`

func (r *BudgetRepository) List(ctx context.Context) ([]domain.Budget, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+budgetColumns+`
		FROM budgets ORDER BY status DESC, category_id`)
	if err != nil {
		return nil, fmt.Errorf("list budgets: %w", err)
	}
	defer rows.Close()

	out := []domain.Budget{}
	for rows.Next() {
		b, err := scanBudget(rows)
		if err != nil {
			return nil, fmt.Errorf("scan budget: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (r *BudgetRepository) GetByID(ctx context.Context, id int64) (domain.Budget, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT `+budgetColumns+`
		FROM budgets WHERE id = ?`, id)

	b, err := scanBudget(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Budget{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Budget{}, fmt.Errorf("get budget %d: %w", id, err)
	}
	return b, nil
}

func (r *BudgetRepository) Create(ctx context.Context, b domain.Budget) (domain.Budget, error) {
	now := time.Now().Unix()
	var month any
	if b.Month != "" {
		month = b.Month
	}
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO budgets (category_id, amount_cents, status, cadence, month, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		b.CategoryID, b.AmountCents, domain.BudgetOpen, b.Cadence, month, now, now)
	if err != nil {
		return domain.Budget{}, mapWriteError("create budget", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return domain.Budget{}, fmt.Errorf("budget insert id: %w", err)
	}
	return r.GetByID(ctx, id)
}

// Update persists amount and lifecycle state. closed_at is owned by the
// service: it is set when a budget closes and cleared when it reopens.
func (r *BudgetRepository) Update(ctx context.Context, b domain.Budget) (domain.Budget, error) {
	var closedAt any
	if b.ClosedAt != nil {
		closedAt = b.ClosedAt.Unix()
	}
	res, err := r.db.ExecContext(ctx, `
		UPDATE budgets
		SET amount_cents = ?, status = ?, closed_at = ?, updated_at = ?
		WHERE id = ?`,
		b.AmountCents, b.Status, closedAt, time.Now().Unix(), b.ID)
	if err != nil {
		return domain.Budget{}, mapWriteError("update budget", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.Budget{}, domain.ErrNotFound
	}
	return r.GetByID(ctx, b.ID)
}

func (r *BudgetRepository) Delete(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM budgets WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete budget %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// RolloverMonthly closes open monthly budgets whose month is older than the
// given month ("YYYY-MM") and creates each one's successor for the given
// month with the same amount — the lazy month rollover, one transaction so a
// budget is never both closed and successor-less. The NOT EXISTS guard keeps
// it idempotent against a concurrent manual create.
func (r *BudgetRepository) RolloverMonthly(ctx context.Context, month string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("rollover budgets begin: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `
		SELECT id, category_id, amount_cents
		FROM budgets
		WHERE cadence = 'monthly' AND status = 'open' AND month < ?`, month)
	if err != nil {
		return fmt.Errorf("rollover budgets stale list: %w", err)
	}
	type stale struct{ id, categoryID, amountCents int64 }
	var stales []stale
	for rows.Next() {
		var s stale
		if err := rows.Scan(&s.id, &s.categoryID, &s.amountCents); err != nil {
			rows.Close()
			return fmt.Errorf("rollover budgets scan stale: %w", err)
		}
		stales = append(stales, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rollover budgets stale rows: %w", err)
	}

	now := time.Now().Unix()
	for _, s := range stales {
		if _, err := tx.ExecContext(ctx, `
			UPDATE budgets SET status = 'closed', closed_at = ?, updated_at = ?
			WHERE id = ? AND status = 'open'`, now, now, s.id); err != nil {
			return fmt.Errorf("rollover budgets close %d: %w", s.id, err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO budgets (category_id, amount_cents, status, cadence, month, created_at, updated_at)
			SELECT ?, ?, 'open', 'monthly', ?, ?, ?
			WHERE NOT EXISTS (SELECT 1 FROM budgets WHERE category_id = ? AND status = 'open')`,
			s.categoryID, s.amountCents, month, now, now, s.categoryID); err != nil {
			return fmt.Errorf("rollover budgets successor for category %d: %w", s.categoryID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("rollover budgets commit: %w", err)
	}
	return nil
}

func scanBudget(row interface{ Scan(dest ...any) error }) (domain.Budget, error) {
	var (
		b              domain.Budget
		status         string
		month          sql.NullString
		closedAt       sql.NullInt64
		createdAt, upd int64
	)
	if err := row.Scan(&b.ID, &b.CategoryID, &b.AmountCents, &status, &b.Cadence, &month, &closedAt, &createdAt, &upd); err != nil {
		return domain.Budget{}, err
	}
	b.Status = domain.BudgetLifecycle(status)
	if month.Valid {
		b.Month = month.String
	}
	if closedAt.Valid {
		t := time.Unix(closedAt.Int64, 0).UTC()
		b.ClosedAt = &t
	}
	b.CreatedAt = time.Unix(createdAt, 0).UTC()
	b.UpdatedAt = time.Unix(upd, 0).UTC()
	return b, nil
}
