package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"home-finance-planner/backend/internal/domain"
)

// seedMappingCategory inserts one category and returns its id, so mapping
// rows can be created with a real category join.
func seedMappingCategory(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	res, err := db.Exec(`INSERT INTO categories (name, created_at) VALUES ('Dairy & Eggs (test)', 100)`)
	if err != nil {
		t.Fatalf("seed category: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed category id: %v", err)
	}
	return id
}

func TestProductNameMappingRepository_CreateAndFindByRawName(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	repo := NewProductNameMappingRepository(db)
	categoryID := seedMappingCategory(t, db)

	created, err := repo.Create(ctx, domain.ProductNameMapping{
		RawName:      "WHL MLK 1L",
		StandardName: "Whole Milk 1L",
		GenericName:  "Fresh Milk",
		CategoryID:   &categoryID,
		Source:       domain.MappingSourceAI,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID == 0 || created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatalf("create did not fill id/timestamps: %+v", created)
	}
	if created.CategoryID == nil || *created.CategoryID != categoryID {
		t.Fatalf("category id = %v; want %d", created.CategoryID, categoryID)
	}
	if created.CategoryName != "Dairy & Eggs (test)" {
		t.Fatalf("category name = %q; want the joined category name", created.CategoryName)
	}

	// The raw-name lookup is case-insensitive (NOCASE unique index).
	found, err := repo.FindByRawName(ctx, "whl mlk 1l")
	if err != nil {
		t.Fatalf("find by raw name (lowercase): %v", err)
	}
	if found.ID != created.ID || found.StandardName != "Whole Milk 1L" || found.GenericName != "Fresh Milk" ||
		found.CategoryID == nil || *found.CategoryID != categoryID ||
		found.CategoryName != "Dairy & Eggs (test)" || found.Source != domain.MappingSourceAI {
		t.Fatalf("found = %+v; want the created row with joins", found)
	}
	if found.UpdatedAt.Before(found.CreatedAt) {
		t.Fatalf("updated_at must not rewind created_at: %+v", found)
	}

	if _, err := repo.FindByRawName(ctx, "NO SUCH NAME"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("find unknown raw name = %v; want ErrNotFound", err)
	}
	if _, err := repo.GetByID(ctx, 424242); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("get unknown id = %v; want ErrNotFound", err)
	}

	// A mapping without a category joins an empty name, not NULL; a missing
	// generic family round-trips as the empty string (no broader family known).
	bare, err := repo.Create(ctx, domain.ProductNameMapping{
		RawName:      "TOMATOS",
		StandardName: "Tomatoes",
		Source:       domain.MappingSourceManual,
	})
	if err != nil {
		t.Fatalf("create bare mapping: %v", err)
	}
	if bare.CategoryID != nil || bare.CategoryName != "" {
		t.Fatalf("bare mapping category = %v %q; want nil/empty", bare.CategoryID, bare.CategoryName)
	}
	if bare.GenericName != "" {
		t.Fatalf("bare mapping generic name = %q; want empty", bare.GenericName)
	}
	if found2, err := repo.FindByRawName(ctx, "TOMATOS"); err != nil || found2.GenericName != "" {
		t.Fatalf("re-read bare mapping = %v %q; want empty generic", err, found2.GenericName)
	}
}

func TestProductNameMappingRepository_DuplicateRawConflict(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	repo := NewProductNameMappingRepository(db)

	if _, err := repo.Create(ctx, domain.ProductNameMapping{
		RawName: "WHL MLK 1L", StandardName: "Whole Milk 1L", Source: domain.MappingSourceAI,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	// The raw text is unique case-insensitively: the same text in different
	// case is the same mapping slot and must not be overwritten by Create.
	if _, err := repo.Create(ctx, domain.ProductNameMapping{
		RawName: "whl mlk 1l", StandardName: "Another Decision", Source: domain.MappingSourceManual,
	}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate raw name = %v; want ErrConflict", err)
	}
}

func TestProductNameMappingRepository_UpsertOverwrites(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	repo := NewProductNameMappingRepository(db)
	categoryID := seedMappingCategory(t, db)

	created, err := repo.Create(ctx, domain.ProductNameMapping{
		RawName: "WHL MLK 1L", StandardName: "Whole Milk 1L", Source: domain.MappingSourceAI,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Upsert is the explicit user-override path: same raw text, new decision.
	upserted, err := repo.Upsert(ctx, domain.ProductNameMapping{
		RawName:      "WHL MLK 1L",
		StandardName: "Whole Milk 1L Carton",
		GenericName:  "Fresh Milk",
		CategoryID:   &categoryID,
		Source:       domain.MappingSourceUser,
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if upserted.ID != created.ID {
		t.Fatalf("upsert id = %d; want the existing row %d", upserted.ID, created.ID)
	}
	if upserted.StandardName != "Whole Milk 1L Carton" {
		t.Fatalf("standard name = %q; want the upserted value", upserted.StandardName)
	}
	if upserted.GenericName != "Fresh Milk" {
		t.Fatalf("generic name = %q; want the upserted value", upserted.GenericName)
	}
	if upserted.CategoryID == nil || *upserted.CategoryID != categoryID || upserted.CategoryName == "" {
		t.Fatalf("category = %v %q; want the upserted category with join", upserted.CategoryID, upserted.CategoryName)
	}
	if upserted.Source != domain.MappingSourceUser {
		t.Fatalf("source = %q; want %q", upserted.Source, domain.MappingSourceUser)
	}
	// Upsert may run in the same second as create, so only require that
	// updated_at never went backwards.
	if upserted.UpdatedAt.Before(created.UpdatedAt) {
		t.Fatalf("upsert rewound updated_at: %v < %v", upserted.UpdatedAt, created.UpdatedAt)
	}

	// The re-read row reflects the update, not the original decision.
	reread, err := repo.FindByRawName(ctx, "WHL MLK 1L")
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if reread.StandardName != "Whole Milk 1L Carton" || reread.Source != domain.MappingSourceUser {
		t.Fatalf("re-read = %+v; want the upserted decision", reread)
	}
	if reread.GenericName != "Fresh Milk" {
		t.Fatalf("re-read generic name = %q; want the upserted value", reread.GenericName)
	}
}

func TestProductNameMappingRepository_UnmappedProductNames(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	repo := NewProductNameMappingRepository(db)
	products := NewProductRepository(db)
	categoryID := seedMappingCategory(t, db)

	mapped, err := products.Create(ctx, domain.Product{Name: "WHL MLK 1L", CategoryID: &categoryID})
	if err != nil {
		t.Fatalf("seed mapped product: %v", err)
	}
	unmapped, err := products.Create(ctx, domain.Product{Name: "Bread", CategoryID: &categoryID})
	if err != nil {
		t.Fatalf("seed unmapped product: %v", err)
	}

	// Nothing has a mapping yet: both products are pending.
	pending, err := repo.UnmappedProductNames(ctx, 100)
	if err != nil {
		t.Fatalf("list unmapped: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("pending = %d rows; want 2", len(pending))
	}
	count, err := repo.CountUnmappedProductNames(ctx)
	if err != nil {
		t.Fatalf("count unmapped: %v", err)
	}
	if count != 2 {
		t.Fatalf("count unmapped = %d; want 2", count)
	}

	if _, err := repo.Create(ctx, domain.ProductNameMapping{
		RawName:      "whl mlk 1l", // case-insensitive: matches the product row
		StandardName: "Whole Milk 1L",
		CategoryID:   &categoryID,
		Source:       domain.MappingSourceAI,
	}); err != nil {
		t.Fatalf("create mapping: %v", err)
	}

	// The mapping exists but carries no generic name yet: both products stay
	// pending — the job also fills the generic-family gap.
	pending, err = repo.UnmappedProductNames(ctx, 100)
	if err != nil {
		t.Fatalf("list unmapped after mapping: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("pending = %d rows; want 2 (mapping without a generic name)", len(pending))
	}
	count, err = repo.CountUnmappedProductNames(ctx)
	if err != nil {
		t.Fatalf("count unmapped after mapping: %v", err)
	}
	if count != 2 {
		t.Fatalf("count unmapped = %d; want 2 (mapping without a generic name)", count)
	}

	if _, err := repo.Upsert(ctx, domain.ProductNameMapping{
		RawName:      "WHL MLK 1L",
		StandardName: "Whole Milk 1L",
		GenericName:  "Fresh Milk",
		CategoryID:   &categoryID,
		Source:       domain.MappingSourceAI,
	}); err != nil {
		t.Fatalf("upsert generic name: %v", err)
	}

	// Only the unmapped product remains, with its category carried along.
	pending, err = repo.UnmappedProductNames(ctx, 100)
	if err != nil {
		t.Fatalf("list unmapped after mapping: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != unmapped.ID || pending[0].Name != "Bread" {
		t.Fatalf("pending = %+v; want only the unmapped Bread product", pending)
	}
	if pending[0].CategoryID == nil || *pending[0].CategoryID != categoryID {
		t.Fatalf("pending category = %v; want %d", pending[0].CategoryID, categoryID)
	}

	// The limit bounds the query (the job works in batches).
	limited, err := repo.UnmappedProductNames(ctx, 0)
	if err != nil {
		t.Fatalf("list unmapped with limit 0: %v", err)
	}
	if len(limited) != 0 {
		t.Fatalf("limit 0 returned %d rows; want none", len(limited))
	}

	count, err = repo.CountUnmappedProductNames(ctx)
	if err != nil {
		t.Fatalf("count unmapped after mapping: %v", err)
	}
	if count != 1 {
		t.Fatalf("count unmapped = %d; want 1 (mapped product %d excluded)", count, mapped.ID)
	}
}

func TestProductNameMappingRepository_Job(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	repo := NewProductNameMappingRepository(db)

	// The migration seeds the single idle job row.
	job, err := repo.GetJob(ctx)
	if err != nil {
		t.Fatalf("get seeded job: %v", err)
	}
	if job.Status != domain.ProductNormalizationIdle {
		t.Fatalf("seeded status = %q; want idle", job.Status)
	}
	if job.TotalNames != 0 || job.ProcessedNames != 0 || job.MappedNames != 0 || job.Error != "" {
		t.Fatalf("seeded job = %+v; want zero counters and no error", job)
	}
	if job.CreatedAt.IsZero() || job.UpdatedAt.Before(job.CreatedAt) {
		t.Fatalf("seeded timestamps = %v..%v", job.CreatedAt, job.UpdatedAt)
	}

	// A run stamps status and progress.
	job.Status = domain.ProductNormalizationRunning
	job.TotalNames = 12
	job.ProcessedNames = 5
	job.MappedNames = 4
	if err := repo.UpdateJob(ctx, job); err != nil {
		t.Fatalf("update job: %v", err)
	}
	running, err := repo.GetJob(ctx)
	if err != nil {
		t.Fatalf("get running job: %v", err)
	}
	if running.Status != domain.ProductNormalizationRunning ||
		running.TotalNames != 12 || running.ProcessedNames != 5 || running.MappedNames != 4 {
		t.Fatalf("running job = %+v; want the updated counters", running)
	}
	if running.UpdatedAt.Before(job.UpdatedAt) {
		t.Fatalf("update rewound updated_at: %v < %v", running.UpdatedAt, job.UpdatedAt)
	}

	// A failure records the reason; a completed run clears the error.
	job.Status = domain.ProductNormalizationFailed
	job.Error = "provider unreachable"
	if err := repo.UpdateJob(ctx, job); err != nil {
		t.Fatalf("update failed job: %v", err)
	}
	failed, err := repo.GetJob(ctx)
	if err != nil {
		t.Fatalf("get failed job: %v", err)
	}
	if failed.Status != domain.ProductNormalizationFailed || failed.Error != "provider unreachable" {
		t.Fatalf("failed job = %+v; want the failure recorded", failed)
	}
	job.Status = domain.ProductNormalizationDone
	job.ProcessedNames = 12
	job.MappedNames = 11
	job.Error = ""
	if err := repo.UpdateJob(ctx, job); err != nil {
		t.Fatalf("update done job: %v", err)
	}
	done, err := repo.GetJob(ctx)
	if err != nil {
		t.Fatalf("get done job: %v", err)
	}
	if done.Status != domain.ProductNormalizationDone || done.Error != "" ||
		done.ProcessedNames != 12 || done.MappedNames != 11 {
		t.Fatalf("done job = %+v; want final counters and no error", done)
	}
}
