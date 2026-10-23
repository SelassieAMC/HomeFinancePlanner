package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"home-finance-planner/backend/internal/domain"
	"home-finance-planner/backend/internal/fx"
)

// RateFetcher abstracts the outbound exchange-rate source (implemented by
// internal/fx against the Frankfurter API).
type RateFetcher interface {
	FetchRates(ctx context.Context) (domain.RateSnapshot, error)
}

// settingsKeyFXRates caches the last fetched rate snapshot as JSON.
const settingsKeyFXRates = "fx_rates"

// fxCacheTTL bounds how often a refresh hits the rates provider: at most one
// fetch per TTL window, lazily on read.
const fxCacheTTL = 24 * time.Hour

// fxCacheVersion invalidates caches that predate a fetcher upgrade. The cache
// lives in the settings table and survives restarts, but the TTL alone cannot
// know that the fetcher started covering more currencies (version 2 added the
// fallback source that fills COP — the ECB table stopped publishing it, and a
// pre-2 cached snapshot kept every peso converting 1:1 until the 24h TTL ran
// out). Any cached entry below the current version is treated as stale and
// refetched on the next read. Bump with every fetcher behavior change.
const fxCacheVersion = 2

// FXService supplies exchange-rate snapshots with caching and offline
// tolerance. It never hard-fails on network errors — callers keep converting
// with whatever data exists and report missing currencies as warnings.
type FXService struct {
	settings SettingsStore
	fetcher  RateFetcher
	log      *slog.Logger
	mu       sync.Mutex // collapses concurrent refreshes into one fetch
}

func NewFXService(settings SettingsStore, fetcher RateFetcher, log *slog.Logger) *FXService {
	if log == nil {
		log = slog.Default()
	}
	return &FXService{settings: settings, fetcher: fetcher, log: log}
}

// Snapshot returns the freshest rate table. Fresh cache → cached; fetch
// fails → stale cache; nothing cached and fetch fails → empty snapshot
// (callers convert 1:1 with warnings). Only settings-store failures error.
func (s *FXService) Snapshot(ctx context.Context) (domain.RateSnapshot, error) {
	cached, err := s.cachedSnapshot(ctx)
	if err != nil {
		return domain.RateSnapshot{}, err
	}
	if freshSnapshot(cached) {
		return cached, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	// Double-check: another request may have refreshed while we waited.
	cached, err = s.cachedSnapshot(ctx)
	if err != nil {
		return domain.RateSnapshot{}, err
	}
	if freshSnapshot(cached) {
		return cached, nil
	}

	snap, fetchErr := s.fetcher.FetchRates(ctx)
	if fetchErr != nil {
		if cached.Pivot != "" {
			s.log.WarnContext(ctx, "fx refresh failed; using stale rates", "error", fetchErr)
			return cached, nil
		}
		s.log.WarnContext(ctx, "fx refresh failed; no cached rates", "error", fetchErr)
		return domain.RateSnapshot{}, nil
	}
	if err := s.persistSnapshot(ctx, snap); err != nil {
		// Cache write failing is not fatal — serve the fresh snapshot.
		s.log.WarnContext(ctx, "fx cache write failed", "error", err)
	}
	return snap, nil
}

// freshSnapshot reports whether the snapshot carries usable, non-stale rates.
func freshSnapshot(s domain.RateSnapshot) bool {
	return s.Pivot != "" && len(s.Rates) > 0 && time.Since(s.FetchedAt) < fxCacheTTL
}

// cachedFX is the cache format: the snapshot plus the fetcher-behavior
// version that produced it. Legacy entries (pre-versioning) carry no version
// field and read as 0 — always stale.
type cachedFX struct {
	domain.RateSnapshot
	Version int `json:"version"`
}

func (s *FXService) cachedSnapshot(ctx context.Context) (domain.RateSnapshot, error) {
	raw, err := s.settings.Get(ctx, settingsKeyFXRates)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.RateSnapshot{}, nil
		}
		return domain.RateSnapshot{}, fmt.Errorf("read fx cache: %w", err)
	}
	var cached cachedFX
	if err := json.Unmarshal([]byte(raw), &cached); err != nil {
		// Corrupt cache behaves like no cache: refresh overwrites it.
		return domain.RateSnapshot{}, nil
	}
	if cached.Version < fxCacheVersion {
		// A snapshot from an older fetcher generation (e.g. missing the
		// fallback-filled currencies) is not "fresh" — force a refetch.
		return domain.RateSnapshot{}, nil
	}
	return cached.RateSnapshot, nil
}

func (s *FXService) persistSnapshot(ctx context.Context, snap domain.RateSnapshot) error {
	raw, err := json.Marshal(cachedFX{RateSnapshot: snap, Version: fxCacheVersion})
	if err != nil {
		return fmt.Errorf("encode fx cache: %w", err)
	}
	return s.settings.Put(ctx, settingsKeyFXRates, string(raw))
}

// converter turns native-currency amounts into one base currency using a
// single snapshot. Currencies with no available rate convert 1:1 and are
// recorded as warnings — data stays visible instead of being dropped.
type converter struct {
	base    string
	snap    domain.RateSnapshot
	missing map[string]bool
}

func newConverter(base string, snap domain.RateSnapshot) *converter {
	return &converter{base: base, snap: snap, missing: map[string]bool{}}
}

// add converts one native-currency amount into the base currency.
func (c *converter) add(currency string, cents int64) int64 {
	rate, ok := c.snap.Rate(currency, c.base)
	if !ok {
		rate = 1
		c.missing[currency] = true
	}
	return fx.ConvertCents(cents, rate)
}

// addFloat converts a non-integer amount (e.g. a per-base-unit price) into
// the base currency, same 1:1-with-warning convention as add.
func (c *converter) addFloat(currency string, v float64) float64 {
	rate, ok := c.snap.Rate(currency, c.base)
	if !ok {
		rate = 1
		c.missing[currency] = true
	}
	return v * rate
}

// warnings returns the sorted currencies that were shown 1:1.
func (c *converter) warnings() []string {
	out := make([]string, 0, len(c.missing))
	for cur := range c.missing {
		out = append(out, cur)
	}
	sort.Strings(out)
	return out
}
