package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"home-finance-planner/backend/internal/domain"
)

// CategoryRepository is the SQLite-backed implementation of the category store.
type CategoryRepository struct{ db *sql.DB }

func NewCategoryRepository(db *sql.DB) *CategoryRepository { return &CategoryRepository{db: db} }

func (r *CategoryRepository) List(ctx context.Context) ([]domain.Category, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, name, section, icon, description, kind, is_system, allows_negative, created_at
		 FROM categories ORDER BY kind, is_system DESC, section, name`)
	if err != nil {
		return nil, fmt.Errorf("list categories: %w", err)
	}
	defer rows.Close()

	out := []domain.Category{}
	for rows.Next() {
		var (
			c         domain.Category
			createdAt int64
		)
		if err := rows.Scan(&c.ID, &c.Name, &c.Section, &c.Icon, &c.Description, &c.Kind, &c.IsSystem, &c.AllowsNegative, &createdAt); err != nil {
			return nil, fmt.Errorf("scan category: %w", err)
		}
		c.CreatedAt = time.Unix(createdAt, 0).UTC()
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *CategoryRepository) GetByID(ctx context.Context, id int64) (domain.Category, error) {
	var (
		c         domain.Category
		createdAt int64
	)
	err := r.db.QueryRowContext(ctx,
		`SELECT id, name, section, icon, description, kind, is_system, allows_negative, created_at
		 FROM categories WHERE id = ?`, id).
		Scan(&c.ID, &c.Name, &c.Section, &c.Icon, &c.Description, &c.Kind, &c.IsSystem, &c.AllowsNegative, &createdAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return domain.Category{}, domain.ErrNotFound
	case err != nil:
		return domain.Category{}, fmt.Errorf("get category %d: %w", id, err)
	}
	c.CreatedAt = time.Unix(createdAt, 0).UTC()
	return c, nil
}

func (r *CategoryRepository) Create(ctx context.Context, c domain.Category) (domain.Category, error) {
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO categories (name, section, icon, description, kind, is_system, allows_negative, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		c.Name, c.Section, c.Icon, c.Description, c.Kind, c.IsSystem, c.AllowsNegative, time.Now().Unix())
	if err != nil {
		return domain.Category{}, mapWriteError("create category", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return domain.Category{}, fmt.Errorf("category insert id: %w", err)
	}
	return r.GetByID(ctx, id)
}

func (r *CategoryRepository) Delete(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM categories WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete category %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}
