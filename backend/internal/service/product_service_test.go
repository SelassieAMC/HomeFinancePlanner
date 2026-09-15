package service

import (
	"context"
	"errors"
	"testing"

	"home-finance-planner/backend/internal/domain"
)

// idp returns a pointer to v (store ids in purchase summary rows).
func idp(v int64) *int64 { return &v }

// newMergeTestService builds a service over an empty fake product store and
// returns the store so tests can seed products and purchase summaries.
func newMergeTestService(t *testing.T) (*ProductService, *fakeProductStore) {
	t.Helper()
	products := newFakeProductStore()
	cats := &fakeCategoryStore{cats: map[int64]domain.Category{
		1: {ID: 1, Name: "Fruits", Kind: "product"},
	}}
	return NewProductService(products, cats, t.TempDir()), products
}

func TestProductServiceCheckMerge(t *testing.T) {
	svc, products := newMergeTestService(t)
	ctx := context.Background()

	milk, err := products.Create(ctx, domain.Product{Name: "Milk", Brand: "Weihenstephan"})
	if err != nil {
		t.Fatalf("seed milk: %v", err)
	}
	cola, err := products.Create(ctx, domain.Product{Name: "Cola"})
	if err != nil {
		t.Fatalf("seed cola: %v", err)
	}

	// Unknown name and unchanged name → no match, nothing to merge.
	if check, err := svc.CheckMerge(ctx, milk.ID, "Yoghurt"); err != nil || check.Match != nil {
		t.Fatalf("unknown name: check = %+v, err = %v, want no match", check, err)
	}
	if check, err := svc.CheckMerge(ctx, milk.ID, "milk"); err != nil || check.Match != nil {
		t.Fatalf("own name (renamed casing): check = %+v, err = %v, want no match", check, err)
	}

	// Disjoint store sets → different_stores, mergeable.
	products.seedStoreRows(milk.ID,
		domain.ProductStorePrice{StoreID: idp(1), StoreName: "REWE", Currency: "EUR"},
	)
	products.seedStoreRows(cola.ID,
		domain.ProductStorePrice{StoreID: idp(2), StoreName: "Aldi", Currency: "EUR"},
	)
	check, err := svc.CheckMerge(ctx, milk.ID, "COLA") // case-insensitive match
	if err != nil {
		t.Fatalf("check merge: %v", err)
	}
	if check.Match == nil || check.Match.ID != cola.ID {
		t.Fatalf("match = %+v, want cola %d", check.Match, cola.ID)
	}
	if !check.Mergeable || check.Reason != MergeDifferentStores {
		t.Fatalf("plan = %v (mergeable %v), want different_stores", check.Reason, check.Mergeable)
	}
	if len(check.SourceStores) != 1 || check.SourceStores[0] != "REWE" ||
		len(check.TargetStores) != 1 || check.TargetStores[0] != "Aldi" {
		t.Fatalf("store names = %v / %v, want [REWE] / [Aldi]", check.SourceStores, check.TargetStores)
	}

	// Shared store → same_store, mergeable, with the comparison row. A
	// differing price is an updated price for the same product: still mergeable.
	products.seedStoreRows(cola.ID,
		domain.ProductStorePrice{StoreID: idp(1), StoreName: "REWE", LatestPriceCents: idp(199), Currency: "EUR"},
	)
	products.seedStoreRows(milk.ID,
		domain.ProductStorePrice{StoreID: idp(1), StoreName: "REWE", LatestPriceCents: idp(129), Currency: "EUR"},
	)
	check, err = svc.CheckMerge(ctx, milk.ID, "Cola")
	if err != nil {
		t.Fatalf("check merge (same store): %v", err)
	}
	if !check.Mergeable || check.Reason != MergeSameStore || check.StoreName != "REWE" {
		t.Fatalf("plan = %v (mergeable %v, store %q), want same_store/REWE", check.Reason, check.Mergeable, check.StoreName)
	}
	if check.SourcePrice == nil || check.TargetPrice == nil ||
		*check.SourcePrice.LatestPriceCents != 129 || *check.TargetPrice.LatestPriceCents != 199 {
		t.Fatalf("prices = %+v / %+v, want 129 / 199", check.SourcePrice, check.TargetPrice)
	}
}

func TestProductServiceMergeKeepsSource(t *testing.T) {
	svc, products := newMergeTestService(t)
	ctx := context.Background()

	source, err := products.Create(ctx, domain.Product{Name: "cola zero", Brand: "Coke"})
	if err != nil {
		t.Fatalf("seed source: %v", err)
	}
	target, err := products.Create(ctx, domain.Product{
		Name: "Coca Cola", Brand: "", Unit: "l", CategoryID: idp(1), Description: "sugary",
	})
	if err != nil {
		t.Fatalf("seed target: %v", err)
	}
	// Same store, so the plan holds.
	products.seedStoreRows(source.ID, domain.ProductStorePrice{StoreID: idp(1), StoreName: "REWE"})
	products.seedStoreRows(target.ID, domain.ProductStorePrice{StoreID: idp(1), StoreName: "REWE"})

	merged, err := svc.Merge(ctx, source.ID, ProductMergeInput{
		MergeWith: target.ID,
		Product:   &ProductInput{Name: "Coca Cola", Brand: "Pepsi", Description: "edited"},
	})
	if err != nil {
		t.Fatalf("merge keep source: %v", err)
	}
	if merged.ID != source.ID {
		t.Fatalf("kept id = %d, want source %d", merged.ID, source.ID)
	}
	if merged.Name != "Coca Cola" || merged.Brand != "Pepsi" || merged.Unit != "l" ||
		merged.CategoryID == nil || *merged.CategoryID != 1 || merged.Description != "edited" {
		t.Fatalf("merged fields = %+v, want renamed source with gaps filled from target", merged)
	}
	if _, err := products.GetByID(ctx, target.ID); err == nil {
		t.Fatalf("target %d still exists after merge", target.ID)
	}
}

func TestProductServiceMergeKeepsTarget(t *testing.T) {
	svc, products := newMergeTestService(t)
	ctx := context.Background()

	source, err := products.Create(ctx, domain.Product{Name: "pepsi light"})
	if err != nil {
		t.Fatalf("seed source: %v", err)
	}
	target, err := products.Create(ctx, domain.Product{Name: "Cola Light", Brand: "Coke", Unit: "l"})
	if err != nil {
		t.Fatalf("seed target: %v", err)
	}
	products.seedStoreRows(source.ID, domain.ProductStorePrice{StoreID: idp(1), StoreName: "REWE"})
	products.seedStoreRows(target.ID, domain.ProductStorePrice{StoreID: idp(1), StoreName: "REWE"})

	merged, err := svc.Merge(ctx, source.ID, ProductMergeInput{
		MergeWith:  target.ID,
		KeepTarget: true,
		Product:    &ProductInput{Name: "Cola Light", Brand: "edited away"},
	})
	if err != nil {
		t.Fatalf("merge keep target: %v", err)
	}
	if merged.ID != target.ID {
		t.Fatalf("kept id = %d, want target %d", merged.ID, target.ID)
	}
	// The pending edit is discarded; the source's stored brand fills the gap
	// — the target had none.
	if merged.Brand != "Coke" || merged.Unit != "l" {
		t.Fatalf("merged fields = %+v, want target's own values", merged)
	}
	if _, err := products.GetByID(ctx, source.ID); err == nil {
		t.Fatalf("source %d still exists after merge", source.ID)
	}
}

func TestProductServiceMergeValidates(t *testing.T) {
	svc, products := newMergeTestService(t)
	ctx := context.Background()

	source, err := products.Create(ctx, domain.Product{Name: "Cola"})
	if err != nil {
		t.Fatalf("seed source: %v", err)
	}
	target, err := products.Create(ctx, domain.Product{Name: "Cola Light"})
	if err != nil {
		t.Fatalf("seed target: %v", err)
	}
	products.seedStoreRows(source.ID, domain.ProductStorePrice{StoreID: idp(1), StoreName: "REWE"})
	products.seedStoreRows(target.ID, domain.ProductStorePrice{StoreID: idp(1), StoreName: "REWE"})

	if _, err := svc.Merge(ctx, source.ID, ProductMergeInput{MergeWith: source.ID}); err == nil {
		t.Fatalf("self-merge accepted")
	}
	// The pending edit's name drifted away from the confirmed target.
	if _, err := svc.Merge(ctx, source.ID, ProductMergeInput{
		MergeWith: target.ID,
		Product:   &ProductInput{Name: "Fanta"},
	}); err == nil {
		t.Fatalf("stale rename accepted")
	}
	// Invalid input (empty name) is rejected through the same build path.
	if _, err := svc.Merge(ctx, source.ID, ProductMergeInput{
		MergeWith: target.ID,
		Product:   &ProductInput{Name: "  "},
	}); err == nil {
		t.Fatalf("empty-name merge accepted")
	}
}

func TestProductServiceMergeAdoptsPhoto(t *testing.T) {
	ctx := context.Background()

	// Keeper has no photo, loser has one: the file is adopted.
	svc, products := newMergeTestService(t)
	source, err := products.Create(ctx, domain.Product{Name: "Cola"})
	if err != nil {
		t.Fatalf("seed source: %v", err)
	}
	target, err := products.Create(ctx, domain.Product{Name: "Cola Light", ImagePath: "/tmp/drop.jpg"})
	if err != nil {
		t.Fatalf("seed target: %v", err)
	}
	products.seedStoreRows(source.ID, domain.ProductStorePrice{StoreID: idp(1), StoreName: "REWE"})
	products.seedStoreRows(target.ID, domain.ProductStorePrice{StoreID: idp(1), StoreName: "REWE"})

	merged, err := svc.Merge(ctx, source.ID, ProductMergeInput{
		MergeWith: target.ID,
		Product:   &ProductInput{Name: "Cola Light"},
	})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if merged.ImagePath != "/tmp/drop.jpg" {
		t.Fatalf("image path = %q, want the dropped product's photo adopted", merged.ImagePath)
	}

	// Keeper already has a photo: it is kept (the loser's file is removed).
	svc, products = newMergeTestService(t)
	source, err = products.Create(ctx, domain.Product{Name: "Cola", ImagePath: "/tmp/keep.jpg"})
	if err != nil {
		t.Fatalf("reseed source: %v", err)
	}
	target, err = products.Create(ctx, domain.Product{Name: "Cola Light", ImagePath: "/tmp/drop2.jpg"})
	if err != nil {
		t.Fatalf("reseed target: %v", err)
	}
	products.seedStoreRows(source.ID, domain.ProductStorePrice{StoreID: idp(1), StoreName: "REWE"})
	products.seedStoreRows(target.ID, domain.ProductStorePrice{StoreID: idp(1), StoreName: "REWE"})

	merged, err = svc.Merge(ctx, source.ID, ProductMergeInput{
		MergeWith: target.ID,
		Product:   &ProductInput{Name: "Cola Light"},
	})
	if err != nil {
		t.Fatalf("second merge: %v", err)
	}
	if merged.ImagePath != "/tmp/keep.jpg" {
		t.Fatalf("image path = %q, want the keeper's photo kept", merged.ImagePath)
	}
}

func TestProductServiceValidatesInput(t *testing.T) {
	products := newFakeProductStore()
	cats := &fakeCategoryStore{cats: map[int64]domain.Category{
		1: {ID: 1, Name: "Fruits", Kind: "product"},
		2: {ID: 2, Name: "Rent", Kind: "expense"},
	}}
	svc := NewProductService(products, cats, t.TempDir())
	ctx := context.Background()
	seeded, err := products.Create(ctx, domain.Product{Name: "Milk"})
	if err != nil {
		t.Fatalf("seed product: %v", err)
	}
	id := seeded.ID

	if _, err := svc.Update(ctx, id, ProductInput{Name: "  "}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("empty name = %v, want ErrValidation", err)
	}
	badCat := int64(2)
	if _, err := svc.Update(ctx, id, ProductInput{Name: "Milk", CategoryID: &badCat}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("expense category = %v, want ErrValidation (not a product category)", err)
	}
	missingCat := int64(99)
	if _, err := svc.Update(ctx, id, ProductInput{Name: "Milk", CategoryID: &missingCat}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown category = %v, want ErrNotFound", err)
	}
	if _, err := svc.Update(ctx, id, ProductInput{Name: "Milk", Description: longString(501)}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("long description = %v, want ErrValidation", err)
	}

	// A valid product-kind category passes and reaches the store.
	goodCat := int64(1)
	if _, err := svc.Update(ctx, id, ProductInput{Name: "Milk", Unit: " L ", CategoryID: &goodCat}); err != nil {
		t.Fatalf("valid update: %v", err)
	}
	stored, err := products.GetByID(ctx, id)
	if err != nil {
		t.Fatalf("stored product: %v", err)
	}
	if stored.Unit != "l" || stored.CategoryID == nil || *stored.CategoryID != 1 {
		t.Fatalf("update not normalized: %+v", stored)
	}
}

// longString returns a string of n 'x' characters.
func longString(n int) string {
	out := make([]rune, n)
	for i := range out {
		out[i] = 'x'
	}
	return string(out)
}
