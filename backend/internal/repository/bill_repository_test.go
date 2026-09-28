package repository

import (
	"context"
	"testing"

	"home-finance-planner/backend/internal/domain"
)

func TestBillRepositoryItemsCarryStandardName(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	repo := NewBillRepository(db)
	mappings := NewProductNameMappingRepository(db)

	// One line's raw text has a remembered normalization, the other's does not.
	if _, err := mappings.Create(ctx, domain.ProductNameMapping{
		RawName:      "WHL MLK 1L",
		StandardName: "Whole Milk 1L",
		Source:       domain.MappingSourceAI,
	}); err != nil {
		t.Fatalf("seed mapping: %v", err)
	}

	created, err := repo.Create(ctx, domain.Bill{
		MarketName: "REWE",
		Date:       "2026-05-03",
		Currency:   "EUR",
		Status:     domain.BillStatusAccepted,
		Items: []domain.BillItem{
			{Name: "WHL MLK 1L", Quantity: 1, UnitPriceCents: 120, LineTotalCents: 120},
			{Name: "Bread", Quantity: 1, UnitPriceCents: 250, LineTotalCents: 250},
		},
	})
	if err != nil {
		t.Fatalf("create bill: %v", err)
	}

	got, err := repo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("get bill: %v", err)
	}
	if len(got.Items) != 2 {
		t.Fatalf("items: %d (want 2)", len(got.Items))
	}
	// The mapped line carries the standardized name alongside the raw text.
	if got.Items[0].Name != "WHL MLK 1L" {
		t.Fatalf("raw name = %q; want the printed text verbatim", got.Items[0].Name)
	}
	if got.Items[0].StandardName != "Whole Milk 1L" {
		t.Fatalf("standard name = %q; want the mapping's %q", got.Items[0].StandardName, "Whole Milk 1L")
	}
	// The unmapped line has no standard name to join.
	if got.Items[1].StandardName != "" {
		t.Fatalf("unmapped standard name = %q; want empty", got.Items[1].StandardName)
	}

	// The join is case-insensitive on the raw text, like the unique index.
	if _, err := mappings.Create(ctx, domain.ProductNameMapping{
		RawName:      "BREAD",
		StandardName: "Bread Loaf",
		Source:       domain.MappingSourceUser,
	}); err != nil {
		t.Fatalf("seed bread mapping: %v", err)
	}
	got, err = repo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("re-get bill: %v", err)
	}
	if got.Items[1].StandardName != "Bread Loaf" {
		t.Fatalf("case-insensitive join = %q; want %q", got.Items[1].StandardName, "Bread Loaf")
	}
}
