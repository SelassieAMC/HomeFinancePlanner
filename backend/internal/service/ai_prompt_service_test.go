package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"home-finance-planner/backend/internal/domain"
	"home-finance-planner/backend/internal/repository"
)

// fakeAIPromptStore is an in-memory AIPromptStore mirroring the SQLite
// semantics: keys are unique case-insensitively (Create reports the
// unique-violation conflict text mapWriteError's isUniqueViolation matches).
type fakeAIPromptStore struct {
	mu    sync.Mutex
	items map[int64]domain.AIPrompt
	next  int64
}

func newFakeAIPromptStore() *fakeAIPromptStore {
	return &fakeAIPromptStore{items: map[int64]domain.AIPrompt{}, next: 1}
}

func (f *fakeAIPromptStore) List(_ context.Context) ([]domain.AIPrompt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []domain.AIPrompt{}
	for _, p := range f.items {
		out = append(out, p)
	}
	return out, nil
}

func (f *fakeAIPromptStore) GetByID(_ context.Context, id int64) (domain.AIPrompt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.items[id]
	if !ok {
		return domain.AIPrompt{}, domain.ErrNotFound
	}
	return p, nil
}

func (f *fakeAIPromptStore) GetByKey(_ context.Context, key string) (domain.AIPrompt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.items {
		if strings.EqualFold(p.Key, key) {
			return p, nil
		}
	}
	return domain.AIPrompt{}, domain.ErrNotFound
}

func (f *fakeAIPromptStore) Create(_ context.Context, p domain.AIPrompt) (domain.AIPrompt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, existing := range f.items {
		if strings.EqualFold(existing.Key, p.Key) {
			return domain.AIPrompt{}, fmt.Errorf("create prompt: unique constraint failed: %w", domain.ErrConflict)
		}
	}
	p.ID = f.next
	f.next++
	f.items[p.ID] = p
	return p, nil
}

func (f *fakeAIPromptStore) Update(_ context.Context, p domain.AIPrompt) (domain.AIPrompt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	existing, ok := f.items[p.ID]
	if !ok {
		return domain.AIPrompt{}, domain.ErrNotFound
	}
	// Key is immutable, mirroring the SQL UPDATE.
	p.Key = existing.Key
	f.items[p.ID] = p
	return p, nil
}

func (f *fakeAIPromptStore) Delete(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.items[id]; !ok {
		return domain.ErrNotFound
	}
	delete(f.items, id)
	return nil
}

// fakePromptCategoryStore is an in-memory CategoryStore (only List is needed).
type fakePromptCategoryStore struct {
	categories []domain.Category
}

func (f *fakePromptCategoryStore) List(_ context.Context) ([]domain.Category, error) {
	return f.categories, nil
}
func (f *fakePromptCategoryStore) GetByID(_ context.Context, id int64) (domain.Category, error) {
	return domain.Category{}, domain.ErrNotFound
}
func (f *fakePromptCategoryStore) Create(_ context.Context, c domain.Category) (domain.Category, error) {
	return c, nil
}
func (f *fakePromptCategoryStore) Delete(_ context.Context, id int64) error {
	return domain.ErrNotFound
}

func newPromptTestService() *AIPromptService {
	return NewAIPromptService(newFakeAIPromptStore(), &fakePromptCategoryStore{})
}

func TestAIPromptService_CreateValidation(t *testing.T) {
	ctx := context.Background()
	s := newPromptTestService()

	cases := []struct {
		name string
		in   AIPromptCreateInput
	}{
		{"missing key", AIPromptCreateInput{Name: "n"}},
		{"uppercase key", AIPromptCreateInput{Key: "Bad-Key", Name: "n"}},
		{"leading digit", AIPromptCreateInput{Key: "1x", Name: "n"}},
		{"too long", AIPromptCreateInput{Key: strings.Repeat("a", 65), Name: "n"}},
		{"missing name", AIPromptCreateInput{Key: "good_key"}},
	}
	for _, tc := range cases {
		if _, err := s.Create(ctx, tc.in); !errors.Is(err, domain.ErrValidation) {
			t.Errorf("%s: expected ErrValidation, got %v", tc.name, err)
		}
	}

	// Content is optional (empty = use built-in default at resolve time).
	if _, err := s.Create(ctx, AIPromptCreateInput{Key: "custom_key", Name: "Custom", Content: ""}); err != nil {
		t.Fatalf("create with empty content: %v", err)
	}
}

func TestAIPromptService_DuplicateKeyConflict(t *testing.T) {
	ctx := context.Background()
	s := newPromptTestService()

	if _, err := s.Create(ctx, AIPromptCreateInput{Key: "custom_key", Name: "A"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Case-insensitive uniqueness is the index's job (COLLATE NOCASE, covered
	// by the repository test); the service surfaces the exact-key conflict.
	if _, err := s.Create(ctx, AIPromptCreateInput{Key: "custom_key", Name: "B"}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("expected ErrConflict on duplicate key, got %v", err)
	}
}

func TestAIPromptService_UpdateKeepsKeyAndReturnsView(t *testing.T) {
	ctx := context.Background()
	store := newFakeAIPromptStore()
	seeded, err := store.Create(ctx, domain.AIPrompt{Key: domain.PromptKeyBillExtraction, Name: "Bill extraction", Content: "custom text"})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	s := NewAIPromptService(store, &fakePromptCategoryStore{})

	updated, err := s.Update(ctx, seeded.ID, AIPromptInput{Name: "Renamed", Content: ""})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Key != domain.PromptKeyBillExtraction {
		t.Fatalf("key must be immutable, got %q", updated.Key)
	}
	if updated.Content != "" {
		t.Fatalf("content should be empty, got %q", updated.Content)
	}
	// A known key with empty content resolves to the built-in default, and
	// the view must say so.
	if !updated.UsesDefault {
		t.Fatalf("expected uses_default for empty content with a built-in default")
	}
	if updated.DefaultContent != defaultBillExtractionPrompt {
		t.Fatalf("default_content must be the raw built-in template")
	}
}

func TestAIPromptService_ResolvePrompt(t *testing.T) {
	ctx := context.Background()
	categories := []domain.Category{
		{Name: "Vegetables", Kind: "product"},
		{Name: "Pasta, Rice & Grains", Kind: "product"},
		{Name: "Rent", Kind: "expense"},
	}
	store := newFakeAIPromptStore()
	s := NewAIPromptService(store, &fakePromptCategoryStore{categories})

	// Missing row → built-in default, categories expanded.
	got, err := s.ResolvePrompt(ctx, domain.PromptKeyBillExtraction)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !strings.Contains(got, `"Pasta, Rice & Grains"`) {
		t.Errorf("expected comma-containing category quoted in: %s", got[:min(len(got), 300)])
	}
	if strings.Contains(got, "Rent") {
		t.Errorf("expense categories must not be injected")
	}
	if strings.Contains(got, "{{categories}}") {
		t.Errorf("placeholder must be expanded")
	}
	if !strings.Contains(got, "Vegetables") {
		t.Errorf("product categories must be injected")
	}

	// Row content wins, and the placeholder expands there too.
	if _, err := store.Create(ctx, domain.AIPrompt{Key: domain.PromptKeyBillExtraction, Name: "x", Content: "Categories: {{categories}}"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	got, err = s.ResolvePrompt(ctx, domain.PromptKeyBillExtraction)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != `Categories: Vegetables, "Pasta, Rice & Grains"` {
		t.Fatalf("unexpected resolved content: %q", got)
	}

	// Whitespace-only content falls back to the default (rendered: the
	// placeholder expands against the fake taxonomy).
	if _, err := store.Update(ctx, domain.AIPrompt{ID: 1, Key: domain.PromptKeyBillExtraction, Name: "x", Content: "   "}); err != nil {
		t.Fatalf("blank content: %v", err)
	}
	got, err = s.ResolvePrompt(ctx, domain.PromptKeyBillExtraction)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want, wantErr := renderPrompt(ctx, &fakePromptCategoryStore{categories}, defaultBillExtractionPrompt)
	if wantErr != nil {
		t.Fatalf("render default: %v", wantErr)
	}
	if got != want {
		t.Fatalf("whitespace content must fall back to the built-in default")
	}

	// A custom key without a row resolves to "" (no built-in, no row).
	got, err = s.ResolvePrompt(ctx, "custom_key")
	if err != nil {
		t.Fatalf("resolve custom: %v", err)
	}
	if got != "" {
		t.Fatalf("expected empty resolution for missing custom key, got %q", got)
	}
}

func TestAIPromptService_DeleteThenResolveFallsBackToDefault(t *testing.T) {
	ctx := context.Background()
	store := newFakeAIPromptStore()
	seeded, err := store.Create(ctx, domain.AIPrompt{Key: domain.PromptKeyOfferSearch, Name: "x", Content: "edited"})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	s := NewAIPromptService(store, &fakePromptCategoryStore{})

	if err := s.Delete(ctx, seeded.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got, err := s.ResolvePrompt(ctx, domain.PromptKeyOfferSearch)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != defaultOffersPromptHead {
		t.Fatalf("resolve must fall back to the built-in head after delete")
	}
}

// TestAIPromptMigrationSeedMatchesDefaults guards the drift between the
// migration 0022 seed and the built-in Go fallbacks: the seed must stay
// byte-identical to the consts, so resetting a prompt in the UI always
// restores exactly what a fresh database would contain.
func TestAIPromptMigrationSeedMatchesDefaults(t *testing.T) {
	ctx := context.Background()
	db, err := repository.Open(ctx, t.TempDir()+"/test.db")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	repo := repository.NewAIPromptRepository(db)

	bill, err := repo.GetByKey(ctx, domain.PromptKeyBillExtraction)
	if err != nil {
		t.Fatalf("get seeded bill prompt: %v", err)
	}
	if bill.Content != defaultBillExtractionPrompt {
		t.Errorf("seeded bill_extraction drifted from the built-in default")
	}
	offers, err := repo.GetByKey(ctx, domain.PromptKeyOfferSearch)
	if err != nil {
		t.Fatalf("get seeded offer prompt: %v", err)
	}
	if offers.Content != defaultOffersPromptHead {
		t.Errorf("seeded offer_search drifted from the built-in default")
	}
}
