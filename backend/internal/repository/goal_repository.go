package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"home-finance-planner/backend/internal/domain"
)

// goalColumns is the goal column list for SELECTs, in scan order. Progress is
// never stored on the goal row — the service reads the linked account's live
// balance (see GoalService.withProgress), so no account join lives here.
const goalColumns = `id, name, target_cents, currency, account_id, target_date, created_at, updated_at`

// GoalRepository is the SQLite-backed implementation of the goal store.
type GoalRepository struct{ db *sql.DB }

func NewGoalRepository(db *sql.DB) *GoalRepository { return &GoalRepository{db: db} }

// List returns every goal, oldest first.
func (r *GoalRepository) List(ctx context.Context) ([]domain.Goal, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+goalColumns+` FROM goals ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list goals: %w", err)
	}
	defer rows.Close()

	out := []domain.Goal{}
	for rows.Next() {
		g, err := scanGoal(rows)
		if err != nil {
			return nil, fmt.Errorf("scan goal: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (r *GoalRepository) GetByID(ctx context.Context, id int64) (domain.Goal, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+goalColumns+` FROM goals WHERE id = ?`, id)
	g, err := scanGoal(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Goal{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Goal{}, fmt.Errorf("get goal %d: %w", id, err)
	}
	return g, nil
}

func (r *GoalRepository) Create(ctx context.Context, g domain.Goal) (domain.Goal, error) {
	now := time.Now().Unix()
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO goals (name, target_cents, currency, account_id, target_date, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		g.Name, g.TargetCents, g.Currency, g.AccountID, nullableString(g.TargetDate), now, now)
	if err != nil {
		return domain.Goal{}, mapWriteError("create goal", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return domain.Goal{}, fmt.Errorf("goal insert id: %w", err)
	}
	return r.GetByID(ctx, id)
}

func (r *GoalRepository) Update(ctx context.Context, g domain.Goal) (domain.Goal, error) {
	res, err := r.db.ExecContext(ctx, `
		UPDATE goals
		SET name = ?, target_cents = ?, currency = ?, account_id = ?, target_date = ?, updated_at = ?
		WHERE id = ?`,
		g.Name, g.TargetCents, g.Currency, g.AccountID, nullableString(g.TargetDate), time.Now().Unix(), g.ID)
	if err != nil {
		return domain.Goal{}, mapWriteError("update goal", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.Goal{}, domain.ErrNotFound
	}
	return r.GetByID(ctx, g.ID)
}

func (r *GoalRepository) Delete(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM goals WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete goal %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// scanGoal scans one row from either *sql.Rows or *sql.Row.
func scanGoal(row interface{ Scan(dest ...any) error }) (domain.Goal, error) {
	var (
		g              domain.Goal
		targetDate     sql.NullString
		createdAt, upd int64
	)
	if err := row.Scan(&g.ID, &g.Name, &g.TargetCents, &g.Currency, &g.AccountID, &targetDate,
		&createdAt, &upd); err != nil {
		return domain.Goal{}, err
	}
	if targetDate.Valid {
		v := targetDate.String
		g.TargetDate = &v
	}
	g.CreatedAt = time.Unix(createdAt, 0).UTC()
	g.UpdatedAt = time.Unix(upd, 0).UTC()
	return g, nil
}

// nullableString converts a *string into a driver value (nil for SQL NULL).
// Empty strings are kept: goal target_date normalizes "" to nil in the
// service before it arrives here.
func nullableString(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}
