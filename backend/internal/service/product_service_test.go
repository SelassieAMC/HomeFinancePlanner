package service

import (
	"context"
	"errors"
	"testing"

	"home-finance-planner/backend/internal/domain"
)

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
