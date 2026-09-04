package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"home-finance-planner/backend/internal/domain"
)

// SettingsRepository is the SQLite-backed key/value store for app settings
// (AI provider configurations, with secrets already encrypted by the caller).
type SettingsRepository struct{ db *sql.DB }

func NewSettingsRepository(db *sql.DB) *SettingsRepository { return &SettingsRepository{db: db} }

// Get returns the stored value for key, or domain.ErrNotFound.
func (r *SettingsRepository) Get(ctx context.Context, key string) (string, error) {
	var value string
	err := r.db.QueryRowContext(ctx,
		`SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", domain.ErrNotFound
	case err != nil:
		return "", fmt.Errorf("get setting %s: %w", key, err)
	}
	return value, nil
}

// Put stores (or replaces) the value for key.
func (r *SettingsRepository) Put(ctx context.Context, key, value string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, time.Now().Unix())
	if err != nil {
		return fmt.Errorf("put setting %s: %w", key, err)
	}
	return nil
}
