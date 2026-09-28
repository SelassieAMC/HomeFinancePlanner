package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"home-finance-planner/backend/internal/domain"
)

// aiPromptColumns reads one managed prompt row.
const aiPromptColumns = `
	p.id, p.key, p.name, p.description, p.content, p.created_at, p.updated_at`

// AIPromptRepository is the SQLite-backed implementation of the prompt
// contract. Keys are unique case-insensitively (idx_ai_prompts_key COLLATE
// NOCASE) — the processes resolve prompts by key.
type AIPromptRepository struct{ db *sql.DB }

func NewAIPromptRepository(db *sql.DB) *AIPromptRepository { return &AIPromptRepository{db: db} }

// List returns every prompt ordered by key (case-insensitively).
func (r *AIPromptRepository) List(ctx context.Context) ([]domain.AIPrompt, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+aiPromptColumns+` FROM ai_prompts p ORDER BY p.key COLLATE NOCASE`)
	if err != nil {
		return nil, fmt.Errorf("list prompts: %w", err)
	}
	defer rows.Close()

	prompts := []domain.AIPrompt{}
	for rows.Next() {
		p, err := scanAIPrompt(rows)
		if err != nil {
			return nil, fmt.Errorf("scan prompt row: %w", err)
		}
		prompts = append(prompts, p)
	}
	return prompts, rows.Err()
}

// GetByID returns one prompt, or domain.ErrNotFound for unknown ids.
func (r *AIPromptRepository) GetByID(ctx context.Context, id int64) (domain.AIPrompt, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+aiPromptColumns+` FROM ai_prompts p WHERE p.id = ?`, id)
	p, err := scanAIPrompt(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.AIPrompt{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.AIPrompt{}, fmt.Errorf("get prompt %d: %w", id, err)
	}
	return p, nil
}

// GetByKey returns the prompt whose key matches case-insensitively.
func (r *AIPromptRepository) GetByKey(ctx context.Context, key string) (domain.AIPrompt, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+aiPromptColumns+` FROM ai_prompts p WHERE p.key = ? COLLATE NOCASE`, key)
	p, err := scanAIPrompt(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.AIPrompt{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.AIPrompt{}, fmt.Errorf("get prompt %q: %w", key, err)
	}
	return p, nil
}

// Create inserts a prompt and returns it with its id and timestamps filled in.
func (r *AIPromptRepository) Create(ctx context.Context, p domain.AIPrompt) (domain.AIPrompt, error) {
	now := time.Now().Unix()
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO ai_prompts (key, name, description, content, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		p.Key, p.Name, p.Description, p.Content, now, now)
	if err != nil {
		return domain.AIPrompt{}, mapWriteError("create prompt", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return domain.AIPrompt{}, fmt.Errorf("prompt insert id: %w", err)
	}
	return r.GetByID(ctx, id)
}

// Update rewrites the user-editable fields (key is immutable — processes
// resolve by it, so a changed key would orphan their lookup).
func (r *AIPromptRepository) Update(ctx context.Context, p domain.AIPrompt) (domain.AIPrompt, error) {
	res, err := r.db.ExecContext(ctx, `
		UPDATE ai_prompts SET name = ?, description = ?, content = ?, updated_at = ?
		WHERE id = ?`,
		p.Name, p.Description, p.Content, time.Now().Unix(), p.ID)
	if err != nil {
		return domain.AIPrompt{}, mapWriteError("update prompt", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.AIPrompt{}, domain.ErrNotFound
	}
	return r.GetByID(ctx, p.ID)
}

// Delete removes a prompt. Deleting a seeded row is safe: resolve falls back
// to the built-in default.
func (r *AIPromptRepository) Delete(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM ai_prompts WHERE id = ?`, id)
	if err != nil {
		return mapWriteError("delete prompt", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func scanAIPrompt(row interface{ Scan(...any) error }) (domain.AIPrompt, error) {
	var (
		p         domain.AIPrompt
		createdAt int64
		updatedAt int64
	)
	if err := row.Scan(&p.ID, &p.Key, &p.Name, &p.Description, &p.Content,
		&createdAt, &updatedAt); err != nil {
		return domain.AIPrompt{}, err
	}
	p.CreatedAt = time.Unix(createdAt, 0).UTC()
	p.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return p, nil
}
