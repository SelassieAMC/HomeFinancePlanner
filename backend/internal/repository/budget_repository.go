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

func (r *BudgetRepository) ListByMonth(ctx context.Context, month string) ([]domain.Budget, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, category_id, month, amount_cents, created_at, updated_at
		FROM budgets WHERE month = ? ORDER BY category_id`, month)
	if err != nil {
		return nil, fmt.Errorf("list budgets %s: %w", month, err)
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
		SELECT id, category_id, month, amount_cents, created_at, updated_at
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
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO budgets (category_id, month, amount_cents, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)`,
		b.CategoryID, b.Month, b.AmountCents, now, now)
	if err != nil {
		return domain.Budget{}, mapWriteError("create budget", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return domain.Budget{}, fmt.Errorf("budget insert id: %w", err)
	}
	return r.GetByID(ctx, id)
}

func (r *BudgetRepository) Update(ctx context.Context, b domain.Budget) (domain.Budget, error) {
	res, err := r.db.ExecContext(ctx, `
		UPDATE budgets
		SET amount_cents = ?, updated_at = ?
		WHERE id = ?`,
		b.AmountCents, time.Now().Unix(), b.ID)
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

func scanBudget(row interface{ Scan(dest ...any) error }) (domain.Budget, error) {
	var (
		b              domain.Budget
		createdAt, upd int64
	)
	if err := row.Scan(&b.ID, &b.CategoryID, &b.Month, &b.AmountCents, &createdAt, &upd); err != nil {
		return domain.Budget{}, err
	}
	b.CreatedAt = time.Unix(createdAt, 0).UTC()
	b.UpdatedAt = time.Unix(upd, 0).UTC()
	return b, nil
}
