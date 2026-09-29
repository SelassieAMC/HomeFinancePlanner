package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

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
	return NewProductService(products, cats, t.TempDir(), nil, nil, nil, nil, 0, nil), products
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
	svc := NewProductService(products, cats, t.TempDir(), nil, nil, nil, nil, 0, nil)
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

// strp boxes a string, for the optional standard_name input.
func strp(v string) *string { return &v }

// TestProductServiceUpdateLearnsMapping covers the product-edit twin of the
// bill workflow's learnMappingOverrides: a submitted standard_name records
// the mapping decision (source user), untouched saves write nothing,
// renames carry a mapped decision to the new raw key, and a cleared field
// records identity.
func TestProductServiceUpdateLearnsMapping(t *testing.T) {
	ctx := context.Background()
	newSvc := func() (*ProductService, *fakeProductStore, *fakeProductMappingStore) {
		products := newFakeProductStore()
		cats := &fakeCategoryStore{cats: map[int64]domain.Category{
			1: {ID: 1, Name: "Dairy", Kind: "product"},
		}}
		mappings := newFakeProductMappingStore()
		svc := NewProductService(products, cats, t.TempDir(), mappings, nil, nil, nil, time.Second, nil)
		return svc, products, mappings
	}
	seed := func(products *fakeProductStore, p domain.Product) domain.Product {
		t.Helper()
		created, err := products.Create(ctx, p)
		if err != nil {
			t.Fatalf("seed product: %v", err)
		}
		return created
	}

	t.Run("new standard on unmapped product", func(t *testing.T) {
		svc, products, mappings := newSvc()
		p := seed(products, domain.Product{Name: "WHL MLK"})
		if _, err := svc.Update(ctx, p.ID, ProductInput{
			Name: "WHL MLK", CategoryID: idp(1), StandardName: strp("Whole Milk 1L"),
		}); err != nil {
			t.Fatalf("update: %v", err)
		}
		if len(mappings.upserts) != 1 {
			t.Fatalf("upserts = %+v, want one decision", mappings.upserts)
		}
		m := mappings.upserts[0]
		if m.RawName != "WHL MLK" || m.StandardName != "Whole Milk 1L" ||
			m.Source != domain.MappingSourceUser || m.CategoryID == nil || *m.CategoryID != 1 {
			t.Fatalf("upsert = %+v, want WHL MLK → Whole Milk 1L (user, category 1)", m)
		}
	})

	t.Run("untouched prefill on mapped product writes nothing", func(t *testing.T) {
		svc, products, mappings := newSvc()
		p := seed(products, domain.Product{Name: "WHL MLK", StandardName: "Whole Milk 1L", CategoryID: idp(1)})
		if _, err := svc.Update(ctx, p.ID, ProductInput{
			Name: "WHL MLK", CategoryID: idp(1), StandardName: strp("Whole Milk 1L"),
		}); err != nil {
			t.Fatalf("update: %v", err)
		}
		if len(mappings.upserts) != 0 {
			t.Fatalf("upserts = %+v, want none on an untouched save", mappings.upserts)
		}
	})

	t.Run("untouched prefill on unmapped product writes nothing", func(t *testing.T) {
		svc, products, mappings := newSvc()
		p := seed(products, domain.Product{Name: "MLK"})
		if _, err := svc.Update(ctx, p.ID, ProductInput{
			Name: "MLK", StandardName: strp("MLK"), // prefill of an unmapped raw name
		}); err != nil {
			t.Fatalf("update: %v", err)
		}
		if len(mappings.upserts) != 0 {
			t.Fatalf("upserts = %+v, want none — the name stays open for AI suggestions", mappings.upserts)
		}
	})

	t.Run("rename carries a mapped decision to the new raw key", func(t *testing.T) {
		svc, products, mappings := newSvc()
		p := seed(products, domain.Product{Name: "WHL MLK", StandardName: "Whole Milk 1L", CategoryID: idp(1)})
		if _, err := svc.Update(ctx, p.ID, ProductInput{
			Name: "WHL MLK 1L", CategoryID: idp(1), StandardName: strp("Whole Milk 1L"),
		}); err != nil {
			t.Fatalf("update: %v", err)
		}
		if len(mappings.upserts) != 1 {
			t.Fatalf("upserts = %+v, want the decision carried to the new name", mappings.upserts)
		}
		m := mappings.upserts[0]
		if m.RawName != "WHL MLK 1L" || m.StandardName != "Whole Milk 1L" {
			t.Fatalf("upsert = %+v, want WHL MLK 1L → Whole Milk 1L", m)
		}
	})

	t.Run("rename of an unmapped product writes nothing", func(t *testing.T) {
		svc, products, mappings := newSvc()
		p := seed(products, domain.Product{Name: "MLK"})
		if _, err := svc.Update(ctx, p.ID, ProductInput{
			Name: "Milk", StandardName: strp("MLK"), // untouched prefill
		}); err != nil {
			t.Fatalf("update: %v", err)
		}
		if len(mappings.upserts) != 0 {
			t.Fatalf("upserts = %+v, want none — no opinion was expressed", mappings.upserts)
		}
	})

	t.Run("cleared field records identity", func(t *testing.T) {
		svc, products, mappings := newSvc()
		p := seed(products, domain.Product{Name: "WHL MLK", StandardName: "Whole Milk 1L"})
		if _, err := svc.Update(ctx, p.ID, ProductInput{
			Name: "WHL MLK", StandardName: strp(""),
		}); err != nil {
			t.Fatalf("update: %v", err)
		}
		if len(mappings.upserts) != 1 || mappings.upserts[0].StandardName != "WHL MLK" {
			t.Fatalf("upserts = %+v, want identity WHL MLK → WHL MLK", mappings.upserts)
		}
	})

	t.Run("category change syncs the mapping", func(t *testing.T) {
		svc, products, mappings := newSvc()
		// Mapped decision, but the row had no category yet.
		p := seed(products, domain.Product{Name: "WHL MLK", StandardName: "Whole Milk 1L"})
		if _, err := svc.Update(ctx, p.ID, ProductInput{
			Name: "WHL MLK", CategoryID: idp(1), StandardName: strp("Whole Milk 1L"),
		}); err != nil {
			t.Fatalf("update: %v", err)
		}
		if len(mappings.upserts) != 1 {
			t.Fatalf("upserts = %+v, want the category synced into the mapping", mappings.upserts)
		}
		m := mappings.upserts[0]
		if m.StandardName != "Whole Milk 1L" || m.CategoryID == nil || *m.CategoryID != 1 {
			t.Fatalf("upsert = %+v, want Whole Milk 1L with category 1", m)
		}
	})

	t.Run("nil mapping store leaves the save working", func(t *testing.T) {
		products := newFakeProductStore()
		svc := NewProductService(products, &fakeCategoryStore{cats: map[int64]domain.Category{}},
			t.TempDir(), nil, nil, nil, nil, time.Second, nil)
		p := seed(products, domain.Product{Name: "MLK"})
		updated, err := svc.Update(ctx, p.ID, ProductInput{
			Name: "Milk", StandardName: strp("Whole Milk 1L"),
		})
		if err != nil {
			t.Fatalf("update without mappings: %v", err)
		}
		if updated.Name != "Milk" {
			t.Fatalf("updated = %+v, want the rename applied", updated)
		}
	})

	t.Run("oversized standard fails the save", func(t *testing.T) {
		svc, products, mappings := newSvc()
		p := seed(products, domain.Product{Name: "MLK"})
		if _, err := svc.Update(ctx, p.ID, ProductInput{
			Name: "MLK", StandardName: strp(longString(201)),
		}); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("oversized standard = %v, want ErrValidation", err)
		}
		if len(mappings.upserts) != 0 {
			t.Fatalf("upserts = %+v, want none — the save failed", mappings.upserts)
		}
	})

	t.Run("mapping failure is non-fatal", func(t *testing.T) {
		svc, products, mappings := newSvc()
		mappings.upsertErr = errors.New("boom")
		p := seed(products, domain.Product{Name: "MLK"})
		if _, err := svc.Update(ctx, p.ID, ProductInput{
			Name: "MLK", StandardName: strp("Whole Milk 1L"),
		}); err != nil {
			t.Fatalf("update with failing upsert: %v", err)
		}
	})
}

// --- normalization memory & backfill job -------------------------------------

// fakeTextNormalizer is a TextNormalizer stub delegating to a function; every
// prompt handed over is recorded for assertions.
type fakeTextNormalizer struct {
	fn      func(ctx context.Context, p domain.AIProvider, prompt string) ([]domain.ProductNameMapping, error)
	prompts []string
}

func (f *fakeTextNormalizer) NormalizeNames(ctx context.Context, p domain.AIProvider, prompt string) ([]domain.ProductNameMapping, error) {
	f.prompts = append(f.prompts, prompt)
	return f.fn(ctx, p, prompt)
}

// newNormalizationJobService wires a ProductService whose backfill job can
// actually run: a mapping memory, a text normalizer and a settings service
// that resolves the configured connector (the same default_for_bills policy
// as bill scans).
func newNormalizationJobService(t *testing.T, mappings *fakeProductMappingStore, normalizer TextNormalizer) *ProductService {
	t.Helper()
	settings := NewSettingsService(
		&fakeSettingsStore{data: map[string]string{settingsKeyAIProviders: testProviderJSON()}},
		passthroughBox{}, nil)
	return NewProductService(newFakeProductStore(), &fakeCategoryStore{cats: map[int64]domain.Category{}},
		t.TempDir(), mappings, normalizer, settings, nil, 5*time.Second, nil)
}

func TestProductServiceNormalizeName(t *testing.T) {
	ctx := context.Background()
	mappings := newFakeProductMappingStore()
	mappings.seedMapping(domain.ProductNameMapping{
		RawName: "WHL MLK 1L", StandardName: "Milk 1L",
		CategoryID: idp(3), CategoryName: "Dairy & Eggs", Source: domain.MappingSourceUser,
	})
	svc := NewProductService(newFakeProductStore(), &fakeCategoryStore{}, t.TempDir(),
		mappings, nil, nil, nil, time.Second, nil)

	// Empty input is a validation error, not a lookup.
	if _, err := svc.NormalizeName(ctx, "   "); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("empty name = %v, want ErrValidation", err)
	}

	// Unmapped name: no match, no error, the raw name is echoed back.
	res, err := svc.NormalizeName(ctx, "  TOMATOS ")
	if err != nil {
		t.Fatalf("unmapped lookup: %v", err)
	}
	if res.Matched || res.RawName != "TOMATOS" || res.StandardName != "" {
		t.Fatalf("unmapped result = %+v, want unmatched TOMATOS", res)
	}

	// Mapped name: the remembered standard name, category and source.
	res, err = svc.NormalizeName(ctx, " whl mlk 1l ")
	if err != nil {
		t.Fatalf("mapped lookup: %v", err)
	}
	if !res.Matched {
		t.Fatalf("mapped result = %+v, want matched", res)
	}
	if res.StandardName != "Milk 1L" || res.CategoryID == nil || *res.CategoryID != 3 ||
		res.CategoryName != "Dairy & Eggs" || res.Source != domain.MappingSourceUser {
		t.Fatalf("mapped result = %+v, want Milk 1L / cat 3 / Dairy & Eggs / user", res)
	}

	// A store failure surfaces (this lookup path is allowed to fail loudly).
	mappings.findErr = errors.New("database is locked")
	if _, err := svc.NormalizeName(ctx, "TOMATOS"); err == nil {
		t.Fatal("store failure was swallowed by NormalizeName")
	}
}

func TestProductServiceRunNormalizationJobCompletes(t *testing.T) {
	mappings := newFakeProductMappingStore()
	mappings.products = []domain.Product{
		{ID: 1, Name: "WHL MLK 1L", CategoryID: idp(7)}, // already mapped below → excluded
		{ID: 2, Name: "TOMATOS"},
		{ID: 3, Name: "JOGHURT 500G", CategoryID: idp(4)},
	}
	mappings.seedMapping(domain.ProductNameMapping{
		RawName: "WHL MLK 1L", StandardName: "Milk 1L", Source: domain.MappingSourceUser,
	})
	normalizer := &fakeTextNormalizer{fn: func(context.Context, domain.AIProvider, string) ([]domain.ProductNameMapping, error) {
		return []domain.ProductNameMapping{
			{RawName: "TOMATOS", StandardName: "Tomatoes"},
			{RawName: "JOGHURT 500G", StandardName: "Yoghurt 500g"},
		}, nil
	}}
	svc := newNormalizationJobService(t, mappings, normalizer)
	ctx := context.Background()

	job, err := svc.RunNormalization(ctx)
	if err != nil {
		t.Fatalf("RunNormalization: %v", err)
	}
	if job.Status != domain.ProductNormalizationRunning || job.TotalNames != 2 {
		t.Fatalf("started job = %+v, want running with 2 unmapped names", job)
	}

	waitFor(t, 2*time.Second, func() bool {
		st, err := svc.NormalizationStatus(ctx)
		return err == nil && st.Status == domain.ProductNormalizationDone
	})
	st, err := svc.NormalizationStatus(ctx)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if st.TotalNames != 2 || st.ProcessedNames != 2 || st.MappedNames != 2 || st.Error != "" {
		t.Fatalf("finished job = %+v, want 2/2/2 without error", st)
	}

	// Every unmapped name was recorded with source 'ai', keeping its product
	// category; the already-mapped name was never re-asked.
	mappings.mu.Lock()
	defer mappings.mu.Unlock()
	if len(mappings.creates) != 2 {
		t.Fatalf("creates = %+v, want one per unmapped name", mappings.creates)
	}
	tomatoes := mappings.creates[0]
	if tomatoes.RawName != "TOMATOS" || tomatoes.StandardName != "Tomatoes" ||
		tomatoes.Source != domain.MappingSourceAI || tomatoes.CategoryID != nil {
		t.Fatalf("tomatoes mapping = %+v, want TOMATOS → Tomatoes (ai, no category)", tomatoes)
	}
	yoghurt := mappings.creates[1]
	if yoghurt.RawName != "JOGHURT 500G" || yoghurt.StandardName != "Yoghurt 500g" ||
		yoghurt.Source != domain.MappingSourceAI || yoghurt.CategoryID == nil || *yoghurt.CategoryID != 4 {
		t.Fatalf("yoghurt mapping = %+v, want JOGHURT 500G → Yoghurt 500g (ai, cat 4)", yoghurt)
	}
	if m := mappings.items["whl mlk 1l"]; m.Source != domain.MappingSourceUser || m.StandardName != "Milk 1L" {
		t.Errorf("the user's existing decision was overwritten: %+v", m)
	}

	// The prompt is the built-in head with the raw-name JSON array appended
	// (unmapped products only, id order).
	if len(normalizer.prompts) != 1 {
		t.Fatalf("normalizer calls = %d, want exactly one batch", len(normalizer.prompts))
	}
	prompt := normalizer.prompts[0]
	if !strings.HasPrefix(prompt, defaultProductNormalizationPrompt) {
		t.Errorf("prompt does not start with the product-normalization head: %.60q", prompt)
	}
	if !strings.HasSuffix(prompt, `["TOMATOS","JOGHURT 500G"]`) {
		t.Errorf("prompt does not end with the raw-name array: ...%q", prompt[min(len(prompt), 200):])
	}
}

// promptNames extracts the raw-name JSON array the job appends to the
// normalization prompt (the batch the call was asked to standardize).
func promptNames(t *testing.T, prompt string) []string {
	t.Helper()
	i := strings.LastIndex(prompt, "[")
	if i < 0 {
		t.Fatalf("normalization prompt has no name array: %.80q", prompt)
	}
	var names []string
	if err := json.Unmarshal([]byte(prompt[i:]), &names); err != nil {
		t.Fatalf("decode prompt name array: %v", err)
	}
	return names
}

func TestProductServiceNormalizationHalvesSlowBatches(t *testing.T) {
	mappings := newFakeProductMappingStore()
	for i := 1; i <= 8; i++ {
		mappings.products = append(mappings.products,
			domain.Product{ID: int64(i), Name: fmt.Sprintf("PRD %d", i)})
	}
	// A model slower than the per-call deadline: batches over 3 names time
	// out, smaller ones answer (each name maps to itself + " Std").
	normalizer := &fakeTextNormalizer{fn: func(_ context.Context, _ domain.AIProvider, prompt string) ([]domain.ProductNameMapping, error) {
		names := promptNames(t, prompt)
		if len(names) > 3 {
			return nil, context.DeadlineExceeded
		}
		out := make([]domain.ProductNameMapping, len(names))
		for i, name := range names {
			out[i] = domain.ProductNameMapping{RawName: name, StandardName: name + " Std"}
		}
		return out, nil
	}}
	svc := newNormalizationJobService(t, mappings, normalizer)
	ctx := context.Background()

	job, err := svc.RunNormalization(ctx)
	if err != nil {
		t.Fatalf("RunNormalization: %v", err)
	}
	if job.Status != domain.ProductNormalizationRunning || job.TotalNames != 8 {
		t.Fatalf("started job = %+v, want running with 8 unmapped names", job)
	}
	waitFor(t, 2*time.Second, func() bool {
		st, err := svc.NormalizationStatus(ctx)
		return err == nil && st.Status == domain.ProductNormalizationDone
	})
	st, err := svc.NormalizationStatus(ctx)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if st.ProcessedNames != 8 || st.MappedNames != 8 || st.Error != "" {
		t.Fatalf("finished job = %+v, want 8/8 without error", st)
	}
	// The over-deadline batches (8, 4) were retried on halves; the working
	// size is remembered for the rest of the process.
	if svc.batchSize != 2 {
		t.Fatalf("learned batch size = %d, want 2", svc.batchSize)
	}
	if len(normalizer.prompts) != 6 {
		t.Fatalf("normalizer calls = %d, want 8-timeout, 4-timeout, then four 2-name batches", len(normalizer.prompts))
	}
	if got := len(promptNames(t, normalizer.prompts[2])); got != 2 {
		t.Fatalf("first successful call held %d names, want the halved batch", got)
	}
}

func TestProductServiceNormalizationFailsWhenEvenOneNameTimesOut(t *testing.T) {
	mappings := newFakeProductMappingStore()
	mappings.products = []domain.Product{{ID: 1, Name: "WHL MLK 1L"}}
	normalizer := &fakeTextNormalizer{fn: func(context.Context, domain.AIProvider, string) ([]domain.ProductNameMapping, error) {
		return nil, context.DeadlineExceeded
	}}
	svc := newNormalizationJobService(t, mappings, normalizer)
	ctx := context.Background()

	if _, err := svc.RunNormalization(ctx); err != nil {
		t.Fatalf("RunNormalization: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		st, err := svc.NormalizationStatus(ctx)
		return err == nil && st.Status == domain.ProductNormalizationFailed
	})
	st, err := svc.NormalizationStatus(ctx)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(st.Error, "LLM_TIMEOUT") || !strings.Contains(st.Error, "timed out") {
		t.Fatalf("job error = %q, want the actionable timeout advice", st.Error)
	}
}

func TestProductServiceRunNormalizationSkipsUnansweredNames(t *testing.T) {
	mappings := newFakeProductMappingStore()
	mappings.products = []domain.Product{
		{ID: 2, Name: "TOMATOS"},
		{ID: 3, Name: "JOGHURT 500G"},
	}
	// The model answers TOMATOS but never JOGHURT 500G: a raw text the AI
	// skips must be asked once, not re-queued forever (the old drain loop
	// spun on it — one AI call per round trip).
	normalizer := &fakeTextNormalizer{fn: func(_ context.Context, _ domain.AIProvider, _ string) ([]domain.ProductNameMapping, error) {
		return []domain.ProductNameMapping{{RawName: "TOMATOS", StandardName: "Tomatoes"}}, nil
	}}
	svc := newNormalizationJobService(t, mappings, normalizer)
	ctx := context.Background()

	if _, err := svc.RunNormalization(ctx); err != nil {
		t.Fatalf("RunNormalization: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		st, err := svc.NormalizationStatus(ctx)
		return err == nil && st.Status == domain.ProductNormalizationDone
	})
	st, err := svc.NormalizationStatus(ctx)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if st.MappedNames != 1 || st.ProcessedNames != 2 {
		t.Fatalf("finished job = %+v, want 2 processed / 1 mapped", st)
	}
	// The skipped name got exactly its one batch ask, then the job converged.
	if calls := len(normalizer.prompts); calls != 1 {
		t.Fatalf("normalizer calls = %d, want exactly 1 (skipped names are not re-asked)", calls)
	}
	// And it was not silently recorded: a later bill scan can still give
	// JOGHURT 500G a real mapping (memory would win over a bogus identity).
	mappings.mu.Lock()
	defer mappings.mu.Unlock()
	if m, ok := mappings.items["joghurt 500g"]; ok {
		t.Fatalf("unanswered name was recorded anyway: %+v", m)
	}
}

func TestProductServiceRunNormalizationSingleFlight(t *testing.T) {
	mappings := newFakeProductMappingStore()
	mappings.products = []domain.Product{
		{ID: 1, Name: "TOMATOS"},
		{ID: 2, Name: "BANANE"},
	}
	answers := []domain.ProductNameMapping{
		{RawName: "TOMATOS", StandardName: "Tomatoes"},
		{RawName: "BANANE", StandardName: "Bananas"},
	}
	block := make(chan struct{})
	normalizer := &fakeTextNormalizer{fn: func(context.Context, domain.AIProvider, string) ([]domain.ProductNameMapping, error) {
		<-block // hold the job in its first AI call
		return answers, nil
	}}
	svc := newNormalizationJobService(t, mappings, normalizer)
	ctx := context.Background()

	if _, err := svc.RunNormalization(ctx); err != nil {
		t.Fatalf("first RunNormalization: %v", err)
	}
	// While the job sits in its AI call, a second start must conflict.
	_, err := svc.RunNormalization(ctx)
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("second RunNormalization while running = %v, want ErrConflict", err)
	}

	close(block)
	waitFor(t, 2*time.Second, func() bool {
		st, err := svc.NormalizationStatus(ctx)
		return err == nil && st.Status == domain.ProductNormalizationDone
	})
	// After the job finished, the next run is allowed again.
	if _, err := svc.RunNormalization(ctx); err != nil {
		t.Fatalf("RunNormalization after done: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		st, err := svc.NormalizationStatus(ctx)
		return err == nil && st.Status == domain.ProductNormalizationDone
	})
}

func TestProductServiceRunNormalizationFailsOnNormalizerError(t *testing.T) {
	mappings := newFakeProductMappingStore()
	mappings.products = []domain.Product{{ID: 1, Name: "TOMATOS"}}
	normalizer := &fakeTextNormalizer{fn: func(context.Context, domain.AIProvider, string) ([]domain.ProductNameMapping, error) {
		return nil, errors.New("model exploded")
	}}
	svc := newNormalizationJobService(t, mappings, normalizer)
	ctx := context.Background()

	if _, err := svc.RunNormalization(ctx); err != nil {
		t.Fatalf("RunNormalization: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		st, err := svc.NormalizationStatus(ctx)
		return err == nil && st.Status == domain.ProductNormalizationFailed
	})
	st, err := svc.NormalizationStatus(ctx)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(st.Error, "model exploded") {
		t.Fatalf("job error = %q, want the normalizer failure recorded", st.Error)
	}
	if st.TotalNames != 1 || st.ProcessedNames != 0 || st.MappedNames != 0 {
		t.Fatalf("failed job counters = %+v, want total 1, processed/mapped 0", st)
	}
	// Nothing was recorded for the failed batch.
	mappings.mu.Lock()
	defer mappings.mu.Unlock()
	if len(mappings.creates) != 0 {
		t.Fatalf("creates = %+v, want none on a failed job", mappings.creates)
	}
}

func TestProductServiceRunNormalizationUnavailableWithoutDeps(t *testing.T) {
	ctx := context.Background()
	// No mapping memory wired → the job endpoints are unavailable.
	svc := NewProductService(newFakeProductStore(), &fakeCategoryStore{}, t.TempDir(),
		nil, nil, nil, nil, time.Second, nil)
	if _, err := svc.RunNormalization(ctx); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("RunNormalization without mappings = %v, want ErrValidation", err)
	}
	if _, err := svc.NormalizationStatus(ctx); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("NormalizationStatus without mappings = %v, want ErrValidation", err)
	}
	// Memory wired but no normalizer → still unavailable.
	svc = NewProductService(newFakeProductStore(), &fakeCategoryStore{}, t.TempDir(),
		newFakeProductMappingStore(), nil, nil, nil, time.Second, nil)
	if _, err := svc.RunNormalization(ctx); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("RunNormalization without normalizer = %v, want ErrValidation", err)
	}
}

func TestNewProductServiceResetsStaleRunningJob(t *testing.T) {
	ctx := context.Background()

	// A job left "running" by a previous process is marked failed at boot.
	stale := newFakeProductMappingStore()
	stale.job.Status = domain.ProductNormalizationRunning
	NewProductService(newFakeProductStore(), &fakeCategoryStore{}, t.TempDir(),
		stale, nil, nil, nil, time.Second, nil)
	st, err := stale.GetJob(ctx)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if st.Status != domain.ProductNormalizationFailed {
		t.Fatalf("stale job status = %q, want failed", st.Status)
	}
	if !strings.Contains(st.Error, "interrupted by a restart") {
		t.Fatalf("stale job error = %q, want the restart explanation", st.Error)
	}

	// A job in any other state (idle here) is left untouched.
	fresh := newFakeProductMappingStore()
	NewProductService(newFakeProductStore(), &fakeCategoryStore{}, t.TempDir(),
		fresh, nil, nil, nil, time.Second, nil)
	st, err = fresh.GetJob(ctx)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if st.Status != domain.ProductNormalizationIdle {
		t.Fatalf("idle job status = %q, want idle", st.Status)
	}
}
