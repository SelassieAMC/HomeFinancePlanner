package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"home-finance-planner/backend/internal/domain"
)

// AccountGroupRepository stores the folder-style presentation groups (0045).
// A group is a name only — membership lives on accounts.group_id and every
// aggregate folds identically with or without it.
type AccountGroupRepository struct{ db *sql.DB }

func NewAccountGroupRepository(db *sql.DB) *AccountGroupRepository {
	return &AccountGroupRepository{db: db}
}

const groupColumns = `id, name`

func (r *AccountGroupRepository) List(ctx context.Context) ([]domain.AccountGroup, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+groupColumns+` FROM account_groups ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list account groups: %w", err)
	}
	defer rows.Close()

	out := []domain.AccountGroup{}
	for rows.Next() {
		var g domain.AccountGroup
		if err := rows.Scan(&g.ID, &g.Name); err != nil {
			return nil, fmt.Errorf("scan account group: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (r *AccountGroupRepository) GetByID(ctx context.Context, id int64) (domain.AccountGroup, error) {
	var g domain.AccountGroup
	err := r.db.QueryRowContext(ctx,
		`SELECT `+groupColumns+` FROM account_groups WHERE id = ?`, id).
		Scan(&g.ID, &g.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.AccountGroup{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.AccountGroup{}, fmt.Errorf("get account group %d: %w", id, err)
	}
	return g, nil
}

func (r *AccountGroupRepository) Create(ctx context.Context, g domain.AccountGroup) (domain.AccountGroup, error) {
	now := time.Now().Unix()
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO account_groups (name, created_at, updated_at) VALUES (?, ?, ?)`,
		g.Name, now, now)
	if err != nil {
		return domain.AccountGroup{}, mapWriteError("create account group", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return domain.AccountGroup{}, fmt.Errorf("account group insert id: %w", err)
	}
	return r.GetByID(ctx, id)
}

func (r *AccountGroupRepository) Update(ctx context.Context, g domain.AccountGroup) (domain.AccountGroup, error) {
	res, err := r.db.ExecContext(ctx, `
		UPDATE account_groups SET name = ?, updated_at = ? WHERE id = ?`,
		g.Name, time.Now().Unix(), g.ID)
	if err != nil {
		return domain.AccountGroup{}, mapWriteError("update account group", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.AccountGroup{}, domain.ErrNotFound
	}
	return r.GetByID(ctx, g.ID)
}

func (r *AccountGroupRepository) Delete(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM account_groups WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete account group %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}
