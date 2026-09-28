package repository

import (
	"context"
	"errors"
	"testing"

	"home-finance-planner/backend/internal/domain"
)

func TestAIPromptRepository_CRUD(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	repo := NewAIPromptRepository(db)

	// The migration seeds the three known prompts.
	seeded, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(seeded) != 3 {
		t.Fatalf("expected 3 seeded prompts, got %d", len(seeded))
	}

	created, err := repo.Create(ctx, domain.AIPrompt{
		Key: "custom_prompt", Name: "Custom", Description: "d", Content: "c",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID == 0 || created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatalf("create did not fill id/timestamps: %+v", created)
	}

	if _, err := repo.GetByKey(ctx, "CUSTOM_PROMPT"); err != nil {
		t.Fatalf("get by key must be case-insensitive: %v", err)
	}
	if _, err := repo.GetByKey(ctx, "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for unknown key, got %v", err)
	}

	created.Name = "Renamed"
	created.Content = "rewritten"
	updated, err := repo.Update(ctx, created)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Name != "Renamed" || updated.Content != "rewritten" {
		t.Fatalf("update did not persist: %+v", updated)
	}
	// Update may run in the same second as create, so only require that
	// updated_at never went backwards.
	if updated.UpdatedAt.Before(updated.CreatedAt) {
		t.Fatalf("update must not rewind updated_at")
	}

	// Key is immutable at the SQL level too: a rewrite via Update never
	// touches it.
	if updated.Key != "custom_prompt" {
		t.Fatalf("key must stay %q, got %q", "custom_prompt", updated.Key)
	}

	if err := repo.Delete(ctx, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := repo.Delete(ctx, created.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound deleting twice, got %v", err)
	}
	if _, err := repo.GetByID(ctx, created.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestAIPromptRepository_DuplicateKeyConflict(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	repo := NewAIPromptRepository(db)

	if _, err := repo.Create(ctx, domain.AIPrompt{Key: "bill_extraction", Name: "Dupe"}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("expected ErrConflict on duplicate key, got %v", err)
	}
}
