package repository

import (
	"context"
	"testing"
	"time"

	"home-finance-planner/backend/internal/domain"
)

// TestInsightRepositoryCRUD covers the insight reads/writes the API and the
// analysis job depend on: create-with-facts, unseen listing (newest first),
// guarded dismiss and the kind+product cooldown check.
func TestInsightRepositoryCRUD(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	repo := NewInsightRepository(db)
	products := NewProductRepository(db)

	made, err := products.Create(ctx, domain.Product{Name: "Chips", Unit: "g"})
	if err != nil {
		t.Fatalf("create product: %v", err)
	}
	pid := made.ID
	ins := domain.ProductInsight{
		Kind:        domain.ProductInsightShrinkflation,
		ProductID:   &pid,
		ProductName: made.Name,
		GenericName: "Potato Chips",
		Currency:    "EUR",
		Message:     "auto wording",
		Source:      "auto",
		Data: domain.ProductInsightData{
			Unit:      "kg",
			ChangePct: 33,
			Purchases: []domain.ProductInsightPurchase{{Date: "2026-10-05", Unit: "g", UnitValue: 150, UnitPriceCents: 200}},
		},
	}
	saved, err := repo.Create(ctx, ins)
	if err != nil {
		t.Fatalf("create insight: %v", err)
	}
	if saved.ID == 0 || saved.CreatedAt.IsZero() || saved.UpdatedAt.IsZero() || saved.Acknowledged {
		t.Fatalf("created row incomplete: %+v", saved)
	}
	if len(saved.Data.Purchases) != 1 || saved.Data.Purchases[0].UnitValue != 150 {
		t.Fatalf("facts not round-tripped: %+v", saved.Data)
	}

	got, err := repo.GetByID(ctx, saved.ID)
	if err != nil || got.ProductName != made.Name {
		t.Fatalf("get: %v %+v", err, got)
	}

	// newest first, unseen only
	older, err := repo.Create(ctx, domain.ProductInsight{
		Kind: domain.ProductInsightPriceCreep, ProductName: "Milk", Currency: "EUR",
		Message: "creep", Source: "ai", Data: domain.ProductInsightData{Unit: "l"},
	})
	if err != nil {
		t.Fatalf("create second: %v", err)
	}
	unseen, err := repo.List(ctx, domain.InsightFilters{Unseen: true})
	if err != nil || len(unseen) != 2 {
		t.Fatalf("unseen list: %v %d", err, len(unseen))
	}
	if unseen[0].ID != older.ID {
		t.Fatalf("list must be newest first: %d then %d", unseen[0].ID, unseen[1].ID)
	}
	if all, err := repo.List(ctx, domain.InsightFilters{}); err != nil || len(all) != 2 {
		t.Fatalf("unfiltered list: %v %d", err, len(all))
	}
	if limited, err := repo.List(ctx, domain.InsightFilters{Unseen: true, Limit: 1}); err != nil || len(limited) != 1 {
		t.Fatalf("limited list: %v %d", err, len(limited))
	}

	if err := repo.Dismiss(ctx, saved.ID); err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	after, err := repo.List(ctx, domain.InsightFilters{Unseen: true})
	if err != nil || len(after) != 1 || after[0].ID != older.ID {
		t.Fatalf("dismiss must remove from the unseen list: %v %+v", err, after)
	}
	if err := repo.Dismiss(ctx, saved.ID); err != nil {
		t.Fatalf("re-dismiss must be a guarded not-found, got %v", err)
	}
	if dismissed, err := repo.GetByID(ctx, saved.ID); err != nil || !dismissed.Acknowledged {
		t.Fatalf("row kept after dismiss: %v %+v", err, dismissed)
	}

	// Cooldown: only the same kind+product pair inside the window counts.
	cutoff := time.Now().Add(-24 * time.Hour)
	if hit, err := repo.RecentlyInsighted(ctx, domain.ProductInsightShrinkflation, made.ID, cutoff); err != nil || !hit {
		t.Fatalf("shrinkflation must be cooling down (%v, %v)", hit, err)
	}
	if hit, err := repo.RecentlyInsighted(ctx, domain.ProductInsightBulkBuy, made.ID, cutoff); err != nil || hit {
		t.Fatalf("other kind must not cool down (%v, %v)", hit, err)
	}
	oldHit, err := repo.RecentlyInsighted(ctx, domain.ProductInsightShrinkflation, made.ID, time.Now().Add(24*time.Hour))
	if err != nil || oldHit {
		t.Fatalf("a cutoff after the row must not be in cooldown (%v, %v)", oldHit, err)
	}
}
