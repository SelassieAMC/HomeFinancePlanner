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

// A confirmed multi-part bill keeps every receipt part in bill_files; the
// row's legacy columns mirror part 1.
func TestBillRepository_MultiPartFiles(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	repo := NewBillRepository(db)

	created, err := repo.Create(ctx, domain.Bill{
		MarketName: "REWE", Date: "2026-05-03", Currency: "EUR",
		Status:    domain.BillStatusAccepted,
		ImagePath: "/data/bills/part-1.jpg", FileHash: "hash-top",
		Files: []domain.BillFile{
			{Position: 1, Path: "/data/bills/part-1.jpg", MimeType: "image/jpeg", FileHash: "hash-top"},
			{Position: 2, Path: "/data/bills/part-2.jpg", MimeType: "image/jpeg", FileHash: "hash-middle"},
			{Position: 3, Path: "/data/bills/part-3.jpg", MimeType: "image/jpeg", FileHash: "hash-bottom"},
		},
	})
	if err != nil {
		t.Fatalf("create bill: %v", err)
	}

	got, err := repo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("get bill: %v", err)
	}
	if len(got.Files) != 3 || got.FileCount != 3 {
		t.Fatalf("files/count = %d/%d, want 3/3", len(got.Files), got.FileCount)
	}
	for i, f := range got.Files {
		if f.Position != i+1 {
			t.Errorf("bill file %d position = %d", i+1, f.Position)
		}
	}
	if got.ImagePath != got.Files[0].Path {
		t.Error("legacy image_path must mirror part 1")
	}

	// Any stored part blocks re-uploads, not just part 1's mirror.
	for _, hash := range []string{"hash-top", "hash-middle", "hash-bottom"} {
		b, err := repo.GetByFileHash(ctx, hash)
		if err != nil || b.ID != created.ID {
			t.Errorf("GetByFileHash(%q) = %v, %v; want bill %d", hash, b.ID, err, created.ID)
		}
	}

	// Deleting the bill leaves no orphan bill_files rows.
	if err := repo.Delete(ctx, created.ID); err != nil {
		t.Fatalf("delete bill: %v", err)
	}
	var children int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bill_files`).Scan(&children); err != nil {
		t.Fatal(err)
	}
	if children != 0 {
		t.Fatalf("orphan bill_files rows: %d", children)
	}
}
