package service

import (
	"context"
	"testing"
	"time"

	"home-finance-planner/backend/internal/domain"
)

// fakeRateFetcher counts fetches and returns a canned snapshot or error.
type fakeRateFetcher struct {
	snapshots []domain.RateSnapshot // returned in order
	err       error                 // when non-nil, always fails
	calls     int
}

func (f *fakeRateFetcher) FetchRates(context.Context) (domain.RateSnapshot, error) {
	f.calls++
	if f.err != nil {
		return domain.RateSnapshot{}, f.err
	}
	if len(f.snapshots) == 0 {
		return domain.RateSnapshot{}, domain.ErrNotFound
	}
	snap := f.snapshots[0]
	f.snapshots = f.snapshots[1:]
	return snap, nil
}

func testSnapshot(fetchedAt time.Time) domain.RateSnapshot {
	return domain.RateSnapshot{
		Pivot:     "EUR",
		Date:      "2026-09-10",
		FetchedAt: fetchedAt,
		Rates:     map[string]float64{"USD": 1.1, "GBP": 0.85},
	}
}

func TestFXServiceServesFreshCacheWithoutFetching(t *testing.T) {
	store := &fakeSettingsStore{data: map[string]string{}}
	fetcher := &fakeRateFetcher{snapshots: []domain.RateSnapshot{testSnapshot(time.Now().UTC())}}
	svc := NewFXService(store, fetcher, nil)
	ctx := context.Background()

	if _, err := svc.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}
	if fetcher.calls != 1 {
		t.Fatalf("fetches = %d, want 1", fetcher.calls)
	}
	// Second call within the TTL must reuse the cache.
	if _, err := svc.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}
	if fetcher.calls != 1 {
		t.Fatalf("fetches after second Snapshot = %d, want 1 (cache reuse)", fetcher.calls)
	}
}

func TestFXServiceRefetchesAfterTTL(t *testing.T) {
	store := &fakeSettingsStore{data: map[string]string{}}
	stale := testSnapshot(time.Now().UTC().Add(-2 * fxCacheTTL))
	mustCacheSnapshot(t, store, stale)
	fresh := testSnapshot(time.Now().UTC())
	fresh.Rates = map[string]float64{"USD": 1.2}
	fetcher := &fakeRateFetcher{snapshots: []domain.RateSnapshot{fresh}}
	svc := NewFXService(store, fetcher, nil)
	ctx := context.Background()

	snap, err := svc.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if fetcher.calls != 1 {
		t.Fatalf("fetches = %d, want 1 (expired cache must refresh)", fetcher.calls)
	}
	if snap.Rates["USD"] != 1.2 {
		t.Errorf("refreshed snapshot not served: USD rate = %v, want 1.2", snap.Rates["USD"])
	}
}

func TestFXServiceFallsBackToStaleCacheOnFetchError(t *testing.T) {
	stale := testSnapshot(time.Now().UTC().Add(-2 * fxCacheTTL))
	store := &fakeSettingsStore{data: map[string]string{}}
	mustCacheSnapshot(t, store, stale)
	fetcher := &fakeRateFetcher{err: domain.ErrNotFound}
	svc := NewFXService(store, fetcher, nil)

	snap, err := svc.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snap.Rates["USD"] != 1.1 {
		t.Errorf("stale cache not served: snapshot = %+v", snap)
	}
}

func TestFXServiceEmptyWhenNoCacheAndFetchFails(t *testing.T) {
	store := &fakeSettingsStore{data: map[string]string{}}
	fetcher := &fakeRateFetcher{err: domain.ErrNotFound}
	svc := NewFXService(store, fetcher, nil)

	snap, err := svc.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Empty snapshot: callers convert 1:1 with warnings, never hard-fail.
	if snap.Pivot != "" || snap.Rates != nil {
		t.Errorf("expected empty snapshot, got %+v", snap)
	}
	if rate, ok := snap.Rate("USD", "EUR"); ok || rate != 0 {
		t.Errorf("Rate(USD,EUR) on empty snapshot = %v ok=%v, want 0 false", rate, ok)
	}
}

func mustCacheSnapshot(t *testing.T, store *fakeSettingsStore, snap domain.RateSnapshot) {
	t.Helper()
	svc := NewFXService(store, &fakeRateFetcher{snapshots: []domain.RateSnapshot{snap}}, nil)
	if _, err := svc.Snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
}
