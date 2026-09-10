package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"home-finance-planner/backend/internal/domain"
)

// fakeStoreStore is an in-memory StoreStore mirroring the SQLite semantics:
// names are unique case-insensitively (Create reports a unique-violation
// conflict, exactly the text mapWriteError's isUniqueViolation matches).
type fakeStoreStore struct {
	mu    sync.Mutex
	items map[int64]domain.Store
	next  int64
	// conflictOnce makes the next Create report a unique violation as if a
	// concurrent caller had just won the race (and stores the winner itself).
	conflictOnce bool
}

func newFakeStoreStore() *fakeStoreStore {
	return &fakeStoreStore{items: map[int64]domain.Store{}, next: 1}
}

func (f *fakeStoreStore) List(_ context.Context) ([]domain.Store, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []domain.Store{}
	for _, s := range f.items {
		out = append(out, s)
	}
	return out, nil
}

func (f *fakeStoreStore) GetByID(_ context.Context, id int64) (domain.Store, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.items[id]
	if !ok {
		return domain.Store{}, domain.ErrNotFound
	}
	return s, nil
}

func (f *fakeStoreStore) FindByName(_ context.Context, name string) (domain.Store, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.items {
		if strings.EqualFold(s.Name, name) {
			return s, nil
		}
	}
	return domain.Store{}, domain.ErrNotFound
}

func (f *fakeStoreStore) Create(_ context.Context, s domain.Store) (domain.Store, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.conflictOnce {
		f.conflictOnce = false
		// The "winner" stored the canonical spelling.
		s.ID = f.next
		f.next++
		s.Name = strings.ToUpper(s.Name)
		s.CreatedAt = time.Now().UTC()
		s.UpdatedAt = s.CreatedAt
		f.items[s.ID] = s
		return domain.Store{}, fmt.Errorf("create store: unique constraint failed: %w", domain.ErrConflict)
	}
	for _, existing := range f.items {
		if strings.EqualFold(existing.Name, s.Name) {
			return domain.Store{}, fmt.Errorf("create store: unique constraint failed: %w", domain.ErrConflict)
		}
	}
	s.ID = f.next
	f.next++
	s.CreatedAt = time.Now().UTC()
	s.UpdatedAt = s.CreatedAt
	f.items[s.ID] = s
	return s, nil
}

func (f *fakeStoreStore) Update(_ context.Context, s domain.Store) (domain.Store, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	existing, ok := f.items[s.ID]
	if !ok {
		return domain.Store{}, domain.ErrNotFound
	}
	for _, other := range f.items {
		if other.ID != s.ID && strings.EqualFold(other.Name, s.Name) {
			return domain.Store{}, fmt.Errorf("update store: unique constraint failed: %w", domain.ErrConflict)
		}
	}
	// Mirrors the real Update: logo_path is never touched here.
	existing.Name = s.Name
	existing.Description = s.Description
	existing.Location = s.Location
	existing.UpdatedAt = time.Now().UTC()
	f.items[s.ID] = existing
	return existing, nil
}

func (f *fakeStoreStore) SetLogo(_ context.Context, id int64, logoPath string) (domain.Store, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.items[id]
	if !ok {
		return domain.Store{}, domain.ErrNotFound
	}
	s.LogoPath = logoPath
	s.HasLogo = logoPath != ""
	s.UpdatedAt = time.Now().UTC()
	f.items[id] = s
	return s, nil
}

func (f *fakeStoreStore) Delete(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.items[id]; !ok {
		return domain.ErrNotFound
	}
	delete(f.items, id)
	return nil
}

func newTestStoreService(t *testing.T) (*StoreService, *fakeStoreStore) {
	t.Helper()
	storeStore := newFakeStoreStore()
	svc := NewStoreService(storeStore, t.TempDir())
	return svc, storeStore
}

// --- tests -------------------------------------------------------------------

func TestStoreCreateValidatesName(t *testing.T) {
	svc, _ := newTestStoreService(t)
	ctx := context.Background()

	if _, err := svc.Create(ctx, StoreInput{Name: "   "}); err == nil {
		t.Fatal("expected blank name to fail")
	}

	store, err := svc.Create(ctx, StoreInput{Name: "  REWE  ", Description: " groceries "})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if store.Name != "REWE" || store.Description != "groceries" {
		t.Fatalf("expected trimmed fields, got %+v", store)
	}
}

func TestStoreCreateRejectsDuplicateNameCaseInsensitive(t *testing.T) {
	svc, _ := newTestStoreService(t)
	ctx := context.Background()

	if _, err := svc.Create(ctx, StoreInput{Name: "REWE"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := svc.Create(ctx, StoreInput{Name: "rewe"}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("expected ErrConflict for case-insensitive duplicate, got %v", err)
	}
}

func TestStoreUpdateKeepsLogoPath(t *testing.T) {
	svc, storeStore := newTestStoreService(t)
	ctx := context.Background()

	store, err := svc.Create(ctx, StoreInput{Name: "REWE"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := storeStore.SetLogo(ctx, store.ID, "/tmp/logo.png"); err != nil {
		t.Fatalf("SetLogo: %v", err)
	}

	updated, err := svc.Update(ctx, store.ID, StoreInput{Name: "Rewe Market", Location: "Main St"})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Name != "Rewe Market" || updated.Location != "Main St" {
		t.Fatalf("unexpected update result: %+v", updated)
	}
	if updated.LogoPath != "/tmp/logo.png" {
		t.Fatalf("Update must not touch logo_path, got %q", updated.LogoPath)
	}
}

func TestStoreSetLogoLifecycle(t *testing.T) {
	svc, _ := newTestStoreService(t)
	ctx := context.Background()
	dir := svc.logosDir

	store, err := svc.Create(ctx, StoreInput{Name: "REWE"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// PDFs are receipts, not logos — rejected even though sniffMime knows them.
	if _, err := svc.SetLogo(ctx, store.ID, "application/pdf", []byte("%PDF-1.4")); err == nil {
		t.Fatal("expected pdf logo to be rejected")
	}

	// The declared type never overrides the sniff: gif bytes in a part that
	// claims to be a png are rejected.
	if _, err := svc.SetLogo(ctx, store.ID, "image/png", []byte("GIF89a-not-a-logo")); err == nil {
		t.Fatal("expected mislabeled gif logo to be rejected")
	}

	first, err := svc.SetLogo(ctx, store.ID, "image/png", []byte("png-bytes"))
	if err != nil {
		t.Fatalf("SetLogo: %v", err)
	}
	if !first.HasLogo || first.LogoPath == "" {
		t.Fatalf("expected logo set, got %+v", first)
	}
	if _, err := os.Stat(first.LogoPath); err != nil {
		t.Fatalf("logo file missing: %v", err)
	}
	if !strings.HasPrefix(first.LogoPath, dir) || !strings.HasSuffix(first.LogoPath, ".png") {
		t.Fatalf("logo written to wrong place: %q", first.LogoPath)
	}

	// Re-upload replaces the file on disk.
	second, err := svc.SetLogo(ctx, store.ID, "image/jpeg", []byte("jpg-bytes"))
	if err != nil {
		t.Fatalf("SetLogo 2: %v", err)
	}
	if second.LogoPath == first.LogoPath {
		t.Fatal("re-upload must write a new file")
	}
	if _, err := os.Stat(first.LogoPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old logo file not removed: %v", err)
	}

	// RemoveLogo clears the row and deletes the file.
	cleared, err := svc.RemoveLogo(ctx, store.ID)
	if err != nil {
		t.Fatalf("RemoveLogo: %v", err)
	}
	if cleared.HasLogo || cleared.LogoPath != "" {
		t.Fatalf("expected logo cleared, got %+v", cleared)
	}
	if _, err := os.Stat(second.LogoPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("logo file not removed: %v", err)
	}
}

func TestStoreDeleteRemovesRowAndLogo(t *testing.T) {
	svc, storeStore := newTestStoreService(t)
	ctx := context.Background()

	store, err := svc.Create(ctx, StoreInput{Name: "REWE"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := svc.SetLogo(ctx, store.ID, "image/png", []byte("png-bytes")); err != nil {
		t.Fatalf("SetLogo: %v", err)
	}
	full, err := svc.Get(ctx, store.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	path := full.LogoPath

	if err := svc.Delete(ctx, store.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := storeStore.GetByID(ctx, store.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected row deleted, got %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("logo file not removed: %v", err)
	}
}
