package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"sync"
	"testing"

	"home-finance-planner/backend/internal/domain"
	"home-finance-planner/backend/internal/repository"
	"home-finance-planner/backend/migrations"

	_ "modernc.org/sqlite" // register the "sqlite" driver for the raw-migration tests
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
	// The NEW default from prompt_defaults.go — a fresh database runs the
	// guarded refreshes, which rewrite the old 0022 seed up to the current
	// default.
	if bill.Content != defaultPrompt(domain.PromptKeyBillExtraction) {
		t.Errorf("seeded bill_extraction drifted from the built-in default")
	}
	offers, err := repo.GetByKey(ctx, domain.PromptKeyOfferSearch)
	if err != nil {
		t.Fatalf("get seeded offer prompt: %v", err)
	}
	if offers.Content != defaultOffersPromptHead {
		t.Errorf("seeded offer_search drifted from the built-in default")
	}
	// Migration 0023 seeds the normalization prompt byte-identically to its
	// built-in fallback.
	normalization, err := repo.GetByKey(ctx, domain.PromptKeyProductNormalization)
	if err != nil {
		t.Fatalf("get seeded normalization prompt: %v", err)
	}
	if normalization.Content != defaultPrompt(domain.PromptKeyProductNormalization) {
		t.Errorf("seeded product_normalization drifted from the built-in default")
	}
	// Migration 0028 seeds the one-time unit-value backfill prompt the same
	// way (new row; there is no prior seed to refresh).
	backfill, err := repo.GetByKey(ctx, domain.PromptKeyUnitValueBackfill)
	if err != nil {
		t.Fatalf("get seeded unit-value backfill prompt: %v", err)
	}
	if backfill.Content != defaultPrompt(domain.PromptKeyUnitValueBackfill) {
		t.Errorf("seeded unit_value_backfill drifted from the built-in default")
	}
	// Migration 0031 seeds the product-insights prompt the same way (new row;
	// there is no prior seed to refresh).
	insights, err := repo.GetByKey(ctx, domain.PromptKeyProductInsights)
	if err != nil {
		t.Fatalf("get seeded product-insights prompt: %v", err)
	}
	if insights.Content != defaultPrompt(domain.PromptKeyProductInsights) {
		t.Errorf("seeded product_insights drifted from the built-in default")
	}
}

// openRawSQLite opens a plain SQLite database without applying migrations
// (the driver is registered by the blank import below, mirroring the
// repository package).
func openRawSQLite(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.TempDir()+"/migrations.db")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Ping(); err != nil {
		t.Fatalf("ping sqlite: %v", err)
	}
	return db
}

// applyEmbeddedMigrations executes the embedded migration files in order,
// starting strictly after afterName ("" = from the first file) up to and
// including lastName, mirroring repository.Open's sequential application
// without the schema_migrations bookkeeping.
func applyEmbeddedMigrations(t *testing.T, db *sql.DB, afterName, lastName string) {
	t.Helper()
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	started := afterName == ""
	found := false
	for _, name := range names {
		if !started {
			if name == afterName {
				started = true
			}
			continue
		}
		sqlBytes, err := migrations.FS.ReadFile(name)
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		if _, err := db.ExecContext(context.Background(), string(sqlBytes)); err != nil {
			t.Fatalf("apply migration %s: %v", name, err)
		}
		if name == lastName {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("migration %s not found among %v", lastName, names)
	}
}

// promptContent reads the raw ai_prompts content of one key straight from the
// database (no repository involved).
func promptContent(t *testing.T, db *sql.DB, key string) string {
	t.Helper()
	var content string
	if err := db.QueryRow(`SELECT content FROM ai_prompts WHERE key = ?`, key).Scan(&content); err != nil {
		t.Fatalf("read prompt %s: %v", key, err)
	}
	return content
}

// TestMigration0023RefreshesOnlyUnmodifiedBillExtractionSeed executes
// migration 0023's guarded UPDATE against SQLite databases in the two states
// it can meet: (i) a row still carrying the OLD default seeded by migration
// 0022 → rewritten to the then-new default, (ii) a user-customized row →
// left untouched. The final-state assertions run through the latest prompt
// refresh migration (0030), so they compare against the CURRENT built-in
// consts — what a fresh database ends up with.
func TestMigration0023RefreshesOnlyUnmodifiedBillExtractionSeed(t *testing.T) {
	// (i) The 0022-seeded default (read from 0022_ai_prompts.sql through the
	// migrations themselves) is refreshed to the current default.
	db := openRawSQLite(t)
	applyEmbeddedMigrations(t, db, "", "0022_ai_prompts.sql")
	oldContent := promptContent(t, db, domain.PromptKeyBillExtraction)
	if oldContent == "" {
		t.Fatal("0022 did not seed bill_extraction")
	}
	if oldContent == defaultBillExtractionPrompt {
		t.Fatal("0022 seed already equals the new default; the guarded UPDATE under test would be a no-op")
	}
	applyEmbeddedMigrations(t, db, "0022_ai_prompts.sql", "0023_product_name_mappings.sql")
	if got := promptContent(t, db, domain.PromptKeyBillExtraction); got == oldContent {
		t.Fatal("0023 did not rewrite the 0022-seeded default")
	}
	applyEmbeddedMigrations(t, db, "0023_product_name_mappings.sql", "0030_deposit_aware_prompts.sql")
	if got := promptContent(t, db, domain.PromptKeyBillExtraction); got != defaultBillExtractionPrompt {
		t.Fatalf("old default not refreshed: got %q, want the new default byte-for-byte", got[:min(len(got), 120)])
	}
	// 0023 also seeds the normalization prompt with its built-in default.
	if got := promptContent(t, db, domain.PromptKeyProductNormalization); got != defaultPrompt(domain.PromptKeyProductNormalization) {
		t.Errorf("product_normalization seed drifted from the built-in default")
	}

	// (ii) A customized prompt survives the guarded refreshes byte-for-byte.
	db = openRawSQLite(t)
	applyEmbeddedMigrations(t, db, "", "0022_ai_prompts.sql")
	const custom = "my own receipt prompt"
	if _, err := db.Exec(`UPDATE ai_prompts SET content = ? WHERE key = 'bill_extraction'`, custom); err != nil {
		t.Fatalf("customize bill_extraction: %v", err)
	}
	applyEmbeddedMigrations(t, db, "0022_ai_prompts.sql", "0026_global_discount_total.sql")
	if got := promptContent(t, db, domain.PromptKeyBillExtraction); got != custom {
		t.Fatalf("custom prompt must survive the guarded refresh: got %q, want %q", got, custom)
	}
}

// TestMigration0024RefreshesOnlyUnmodifiedSeeds executes migration 0024's
// guarded UPDATEs against SQLite databases in the two states they can meet:
// (i) rows still carrying the 0023-era defaults → rewritten to the new
// defaults (adds the generic_name field + rules), (ii) user-customized rows
// → left untouched. The final-state assertions run through migration 0026
// (which refreshes the bill seed again), so they compare against the
// CURRENT built-in consts — what a fresh database ends up with.
func TestMigration0024RefreshesOnlyUnmodifiedSeeds(t *testing.T) {
	// (i) The 0023-seeded defaults are refreshed to the new defaults.
	db := openRawSQLite(t)
	applyEmbeddedMigrations(t, db, "", "0023_product_name_mappings.sql")
	if got := promptContent(t, db, domain.PromptKeyBillExtraction); got == defaultBillExtractionPrompt {
		t.Fatal("0023 seed already equals the new bill default; the guarded UPDATE under test would be a no-op")
	}
	if got := promptContent(t, db, domain.PromptKeyProductNormalization); got == defaultPrompt(domain.PromptKeyProductNormalization) {
		t.Fatal("0023 seed already equals the new normalization default; the guarded UPDATE under test would be a no-op")
	}
	applyEmbeddedMigrations(t, db, "0023_product_name_mappings.sql", "0030_deposit_aware_prompts.sql")
	if got := promptContent(t, db, domain.PromptKeyBillExtraction); got != defaultBillExtractionPrompt {
		t.Fatalf("old bill default not refreshed: got %q, want the new default byte-for-byte", got[:min(len(got), 120)])
	}
	if got := promptContent(t, db, domain.PromptKeyProductNormalization); got != defaultPrompt(domain.PromptKeyProductNormalization) {
		t.Fatalf("old normalization default not refreshed: got %q, want the new default byte-for-byte", got[:min(len(got), 120)])
	}

	// (ii) Customized prompts survive the guarded refresh byte-for-byte.
	db = openRawSQLite(t)
	applyEmbeddedMigrations(t, db, "", "0023_product_name_mappings.sql")
	const (
		customBill = "my own receipt prompt"
		customNorm = "my own normalizer prompt"
	)
	if _, err := db.Exec(`UPDATE ai_prompts SET content = ? WHERE key = 'bill_extraction'`, customBill); err != nil {
		t.Fatalf("customize bill_extraction: %v", err)
	}
	if _, err := db.Exec(`UPDATE ai_prompts SET content = ? WHERE key = 'product_normalization'`, customNorm); err != nil {
		t.Fatalf("customize product_normalization: %v", err)
	}
	applyEmbeddedMigrations(t, db, "0023_product_name_mappings.sql", "0030_deposit_aware_prompts.sql")
	if got := promptContent(t, db, domain.PromptKeyBillExtraction); got != customBill {
		t.Fatalf("custom bill prompt must survive the guarded refresh: got %q, want %q", got, customBill)
	}
	if got := promptContent(t, db, domain.PromptKeyProductNormalization); got != customNorm {
		t.Fatalf("custom normalization prompt must survive the guarded refresh: got %q, want %q", got, customNorm)
	}
}

// TestMigration0025RefreshesOnlyUnmodifiedBillExtractionSeed executes
// migration 0025's guarded UPDATE against SQLite databases in the two states
// it can meet: (i) a row still carrying the 0024-era default → rewritten to
// the multi-part default (new head + merge rules), (ii) a user-customized
// row → left untouched. The final-state assertions run through migration
// 0026 (which refreshes the seed again), so they compare against the
// CURRENT built-in consts — what a fresh database ends up with.
func TestMigration0025RefreshesOnlyUnmodifiedBillExtractionSeed(t *testing.T) {
	// (i) The 0024-seeded default is refreshed to the multi-part default.
	db := openRawSQLite(t)
	applyEmbeddedMigrations(t, db, "", "0024_generic_product_names.sql")
	if got := promptContent(t, db, domain.PromptKeyBillExtraction); got == defaultBillExtractionPrompt {
		t.Fatal("0024 seed already equals the new bill default; the guarded UPDATE under test would be a no-op")
	}
	applyEmbeddedMigrations(t, db, "0024_generic_product_names.sql", "0030_deposit_aware_prompts.sql")
	if got := promptContent(t, db, domain.PromptKeyBillExtraction); got != defaultBillExtractionPrompt {
		t.Fatalf("old bill default not refreshed: got %q, want the new default byte-for-byte", got[:min(len(got), 120)])
	}

	// (ii) A customized prompt survives the guarded refresh byte-for-byte.
	db = openRawSQLite(t)
	applyEmbeddedMigrations(t, db, "", "0024_generic_product_names.sql")
	const custom = "my own receipt prompt"
	if _, err := db.Exec(`UPDATE ai_prompts SET content = ? WHERE key = 'bill_extraction'`, custom); err != nil {
		t.Fatalf("customize bill_extraction: %v", err)
	}
	applyEmbeddedMigrations(t, db, "0024_generic_product_names.sql", "0030_deposit_aware_prompts.sql")
	if got := promptContent(t, db, domain.PromptKeyBillExtraction); got != custom {
		t.Fatalf("custom prompt must survive the guarded refresh: got %q, want %q", got, custom)
	}
}

// TestMigration0026RefreshesOnlyUnmodifiedBillExtractionSeed executes
// migration 0026's guarded UPDATE against SQLite databases in the two states
// it can meet: (i) a row still carrying the 0025-era default → rewritten to
// the global-discount default (the discount_total rule changes from
// informational to reconciling), (ii) a user-customized row → left
// untouched.
func TestMigration0026RefreshesOnlyUnmodifiedBillExtractionSeed(t *testing.T) {
	// (i) The 0025-seeded default is refreshed to the global-discount default.
	db := openRawSQLite(t)
	applyEmbeddedMigrations(t, db, "", "0025_bill_receipt_parts.sql")
	oldContent := promptContent(t, db, domain.PromptKeyBillExtraction)
	if oldContent == "" {
		t.Fatal("0025 did not seed bill_extraction")
	}
	if oldContent == defaultBillExtractionPrompt {
		t.Fatal("0025 seed already equals the new default; the guarded UPDATE under test would be a no-op")
	}
	// The guarded refresh must actually rewrite the 0025-era seed; the
	// final-state equality check runs through the latest refresh (0030), so
	// it compares against the CURRENT built-in const.
	applyEmbeddedMigrations(t, db, "0025_bill_receipt_parts.sql", "0026_global_discount_total.sql")
	rewritten := promptContent(t, db, domain.PromptKeyBillExtraction)
	if rewritten == oldContent {
		t.Fatal("0026 did not rewrite the 0025-seeded default")
	}
	applyEmbeddedMigrations(t, db, "0026_global_discount_total.sql", "0030_deposit_aware_prompts.sql")
	if got := promptContent(t, db, domain.PromptKeyBillExtraction); got != defaultBillExtractionPrompt {
		t.Fatalf("old default not refreshed to the current one: got %q, want byte-for-byte", got[:min(len(got), 120)])
	}

	// (ii) A customized prompt survives the guarded refresh byte-for-byte.
	db = openRawSQLite(t)
	applyEmbeddedMigrations(t, db, "", "0025_bill_receipt_parts.sql")
	const custom = "my own receipt prompt"
	if _, err := db.Exec(`UPDATE ai_prompts SET content = ? WHERE key = 'bill_extraction'`, custom); err != nil {
		t.Fatalf("customize bill_extraction: %v", err)
	}
	applyEmbeddedMigrations(t, db, "0025_bill_receipt_parts.sql", "0027_unit_value_and_mapping_links.sql")
	if got := promptContent(t, db, domain.PromptKeyBillExtraction); got != custom {
		t.Fatalf("custom prompt must survive the guarded refresh: got %q, want %q", got, custom)
	}
}

// TestMigration0027RefreshesOnlyUnmodifiedBillExtractionSeed executes
// migration 0027's guarded UPDATE against SQLite databases in the two states
// it can meet: (i) a row still carrying the 0026-era default → rewritten to
// the unit_value default (the schema and rule list gain the printed size
// magnitude), (ii) a user-customized row → left untouched.
func TestMigration0027RefreshesOnlyUnmodifiedBillExtractionSeed(t *testing.T) {
	// (i) The 0026-seeded default is refreshed to the unit_value default.
	db := openRawSQLite(t)
	applyEmbeddedMigrations(t, db, "", "0026_global_discount_total.sql")
	oldContent := promptContent(t, db, domain.PromptKeyBillExtraction)
	if oldContent == "" {
		t.Fatal("0026 did not seed bill_extraction")
	}
	if oldContent == defaultBillExtractionPrompt {
		t.Fatal("0026 seed already equals the new default; the guarded UPDATE under test would be a no-op")
	}
	applyEmbeddedMigrations(t, db, "0026_global_discount_total.sql", "0030_deposit_aware_prompts.sql")
	if got := promptContent(t, db, domain.PromptKeyBillExtraction); got != defaultBillExtractionPrompt {
		t.Fatalf("old default not refreshed: got %q, want the new default byte-for-byte", got[:min(len(got), 120)])
	}

	// (ii) A customized prompt survives the guarded refresh byte-for-byte.
	db = openRawSQLite(t)
	applyEmbeddedMigrations(t, db, "", "0026_global_discount_total.sql")
	const custom = "my own receipt prompt"
	if _, err := db.Exec(`UPDATE ai_prompts SET content = ? WHERE key = 'bill_extraction'`, custom); err != nil {
		t.Fatalf("customize bill_extraction: %v", err)
	}
	applyEmbeddedMigrations(t, db, "0026_global_discount_total.sql", "0030_deposit_aware_prompts.sql")
	if got := promptContent(t, db, domain.PromptKeyBillExtraction); got != custom {
		t.Fatalf("custom prompt must survive the guarded refresh: got %q, want %q", got, custom)
	}
}
