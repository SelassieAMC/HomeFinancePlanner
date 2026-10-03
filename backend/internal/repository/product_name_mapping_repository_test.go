package repository

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"testing"

	"home-finance-planner/backend/internal/domain"
	"home-finance-planner/backend/migrations"
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

// TestProductNameMappingRepository_LinkProduct covers the opportunistic
// convenience link: nil → filled, stale → re-pointed, matching → left alone
// (no error, no touch), unmapped raw names ignored, and an Upsert without a
// product (nil payload link) never erases a stored one (COALESCE).
func TestProductNameMappingRepository_LinkProduct(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	repo := NewProductNameMappingRepository(db)
	prods := NewProductRepository(db)

	created, err := repo.Create(ctx, domain.ProductNameMapping{
		RawName: "WHL MLK 1L", StandardName: "Whole Milk 1L", Source: domain.MappingSourceAI,
	})
	if err != nil {
		t.Fatalf("create mapping: %v", err)
	}
	if created.ProductID != nil {
		t.Fatalf("fresh mapping carries product_id %v; want nil", created.ProductID)
	}
	product, err := prods.Create(ctx, domain.Product{Name: "Whole Milk 1L"})
	if err != nil {
		t.Fatalf("create product: %v", err)
	}

	// Fill (nil → id).
	if err := repo.LinkProduct(ctx, "whl mlk 1l", product.ID); err != nil {
		t.Fatalf("link product: %v", err)
	}
	linked, err := repo.FindByRawName(ctx, "WHL MLK 1L")
	if err != nil {
		t.Fatalf("re-read mapping: %v", err)
	}
	if linked.ProductID == nil || *linked.ProductID != product.ID {
		t.Fatalf("linked product_id = %v; want %d", linked.ProductID, product.ID)
	}

	// Idempotent: linking the same id again must be a silent no-op.
	if err := repo.LinkProduct(ctx, "WHL MLK 1L", product.ID); err != nil {
		t.Fatalf("re-link same id: %v", err)
	}

	// Re-point (stale link → the new product).
	other, err := prods.Create(ctx, domain.Product{Name: "Whole Milk 1L Carton"})
	if err != nil {
		t.Fatalf("create second product: %v", err)
	}
	if err := repo.LinkProduct(ctx, "whl mlk 1l", other.ID); err != nil {
		t.Fatalf("re-link to new product: %v", err)
	}
	relinked, _ := repo.FindByRawName(ctx, "whl mlk 1l")
	if relinked.ProductID == nil || *relinked.ProductID != other.ID {
		t.Fatalf("re-linked product_id = %v; want %d", relinked.ProductID, other.ID)
	}

	// An Upsert without a product (nil) keeps the stored link; one with a
	// non-nil payload link rewrites it (explicit decisions win).
	upserted, err := repo.Upsert(ctx, domain.ProductNameMapping{
		RawName: "WHL MLK 1L", StandardName: "Whole Milk 1L Carton", Source: domain.MappingSourceUser,
	})
	if err != nil {
		t.Fatalf("upsert without product link: %v", err)
	}
	if upserted.ProductID == nil || *upserted.ProductID != other.ID {
		t.Fatalf("upsert nil payload link wiped the stored one: got %v; want %d", upserted.ProductID, other.ID)
	}
	if err := repo.LinkProduct(ctx, "WHL MLK 1L", product.ID); err != nil {
		t.Fatalf("re-point back: %v", err)
	}
	_, err = repo.Upsert(ctx, domain.ProductNameMapping{
		RawName: "WHL MLK 1L", StandardName: "Whole Milk 1L", Source: domain.MappingSourceUser,
		ProductID: &product.ID,
	})
	if err != nil {
		t.Fatalf("upsert with product link: %v", err)
	}
	final, _ := repo.FindByRawName(ctx, "WHL MLK 1L")
	if final.ProductID == nil || *final.ProductID != product.ID {
		t.Fatalf("upsert payload link ignored: got %v; want %d", final.ProductID, product.ID)
	}

	// Unmapped raw names are silently ignored (best-effort link).
	if err := repo.LinkProduct(ctx, "NO SUCH MAPPING", product.ID); err != nil {
		t.Fatalf("link on unmapped raw name = %v; want nil", err)
	}
}

// TestProductNameMappingRepository_MigrationBackfillProductLinks runs the
// 0027 backfill UPDATE against a 0026-schema database: mappings whose raw
// text (NOCASE) is exactly a product's name gain the product link; renamed
// or unmatched mappings stay NULL. Also proves the new unit_value columns
// exist after 0027.
func TestProductNameMappingRepository_MigrationBackfillProductLinks(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file:"+t.TempDir()+"/backfill.db")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Ping(); err != nil {
		t.Fatalf("ping sqlite: %v", err)
	}

	apply := func(afterName, lastName string) {
		t.Helper()
		entries, err := migrations.FS.ReadDir(".")
		if err != nil {
			t.Fatalf("read embedded migrations: %v", err)
		}
		var names []string
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".sql") {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		started := afterName == ""
		for _, name := range names {
			if !started {
				if name == afterName {
					started = true
				}
				continue
			}
			sqlBytes, err := migrations.FS.ReadFile(name)
			if err != nil {
				t.Fatalf("read migration %s: %v", name, err)
			}
			if _, err := db.ExecContext(ctx, string(sqlBytes)); err != nil {
				t.Fatalf("apply migration %s: %v", name, err)
			}
			if name == lastName {
				return
			}
		}
	}

	// Up to 0026: the pre-0027 schema. Seed products and mappings as an
	// upgrade would leave them, then run 0027.
	apply("", "0026_global_discount_total.sql")
	seed := func(query string, args ...any) int64 {
		t.Helper()
		res, err := db.ExecContext(ctx, query, args...)
		if err != nil {
			t.Fatalf("seed %q: %v", query, err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	milk := seed(`INSERT INTO products (name, created_at, updated_at) VALUES ('Milk', 100, 100)`)
	seed(`INSERT INTO products (name, created_at, updated_at) VALUES ('Milk 1L', 100, 100)`) // renamed away
	seed(`INSERT INTO product_name_mappings (raw_name, standard_name, source, created_at, updated_at)
	      VALUES ('MILK', 'Milk', 'ai', 100, 100)`) // exact NOCASE match → linked
	seed(`INSERT INTO product_name_mappings (raw_name, standard_name, source, created_at, updated_at)
	      VALUES ('Milk 500G', 'Milk 500g', 'ai', 100, 100)`) // no product matches → NULL
	seed(`INSERT INTO product_name_mappings (raw_name, standard_name, source, created_at, updated_at)
	      VALUES ('Milk 1L', 'Milk 1L', 'ai', 100, 100)`) // mapping is older than the products' name; raw text matches a real product → linked
	apply("0026_global_discount_total.sql", "0027_unit_value_and_mapping_links.sql")

	repo := NewProductNameMappingRepository(db)
	linked, err := repo.FindByRawName(ctx, "MILK")
	if err != nil {
		t.Fatalf("find backfilled mapping: %v", err)
	}
	if linked.ProductID == nil || *linked.ProductID != milk {
		t.Fatalf("backfilled link = %v; want product %d", linked.ProductID, milk)
	}
	unmatched, err := repo.FindByRawName(ctx, "Milk 500G")
	if err != nil {
		t.Fatalf("find unmatched mapping: %v", err)
	}
	if unmatched.ProductID != nil {
		t.Fatalf("unmatched link = %v; want NULL", unmatched.ProductID)
	}
	renamed, err := repo.FindByRawName(ctx, "Milk 1L")
	if err != nil {
		t.Fatalf("find renamed mapping: %v", err)
	}
	if renamed.ProductID == nil || *renamed.ProductID != milk+1 {
		t.Fatalf("renamed-mapping link = %v; want product %d", renamed.ProductID, milk+1)
	}

	// unit_value columns exist and round-trip as SQL NULL before any write.
	var uv any
	if err := db.QueryRow(`SELECT unit_value FROM products WHERE id = ?`, milk).Scan(&uv); err != nil || uv != nil {
		t.Fatalf("migrated products.unit_value = %v (err %v); want NULL", uv, err)
	}
}
