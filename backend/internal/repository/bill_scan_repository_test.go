package repository

import (
	"context"
	"testing"
	"time"

	"home-finance-planner/backend/internal/domain"
)

// threeScanFiles is a stored 3-part receipt: consecutive photos of one long
// receipt, with their content hashes.
func threeScanFiles() []domain.BillScanFile {
	return []domain.BillScanFile{
		{Position: 1, Path: "/data/bills/part-1.jpg", MimeType: "image/jpeg", FileHash: "hash-top"},
		{Position: 2, Path: "/data/bills/part-2.jpg", MimeType: "image/jpeg", FileHash: "hash-middle"},
		{Position: 3, Path: "/data/bills/part-3.jpg", MimeType: "image/jpeg", FileHash: "hash-bottom"},
	}
}

// Create persists every receipt part in bill_scan_files; the row's legacy
// single-file columns mirror part 1.
func TestBillScanRepository_CreateStoresEveryPart(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	repo := NewBillScanRepository(db)

	created, err := repo.Create(ctx, domain.BillScan{
		ScanToken: "tok-multi", ImagePath: "/data/bills/part-1.jpg",
		MimeType: "image/jpeg", ProviderID: "p1", FileHash: "hash-top",
		Files: threeScanFiles(),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.FileCount != 3 {
		t.Fatalf("created file_count = %d, want 3", created.FileCount)
	}

	got, err := repo.GetByToken(ctx, "tok-multi")
	if err != nil {
		t.Fatalf("GetByToken: %v", err)
	}
	if len(got.Files) != 3 || got.FileCount != 3 {
		t.Fatalf("files/count = %d/%d, want 3/3", len(got.Files), got.FileCount)
	}
	for i, f := range got.Files {
		if f.Position != i+1 {
			t.Errorf("file %d position = %d", i+1, f.Position)
		}
		if f.FileHash == "" || f.Path == "" {
			t.Errorf("file %d missing path/hash: %+v", i+1, f)
		}
	}
	if got.ImagePath != got.Files[0].Path || got.FileHash != got.Files[0].FileHash {
		t.Error("legacy columns must mirror part 1")
	}

	// Any stored part matches the dedup lookup — not just part 1's mirror.
	for _, hash := range []string{"hash-top", "hash-middle", "hash-bottom"} {
		s, err := repo.GetByFileHash(ctx, hash)
		if err != nil || s.ScanToken != "tok-multi" {
			t.Errorf("GetByFileHash(%q) = %v, %v; want the grouped scan", hash, s.ScanToken, err)
		}
	}
	if _, err := repo.GetByFileHash(ctx, "hash-unknown"); err != domain.ErrNotFound {
		t.Errorf("unknown hash: err = %v, want ErrNotFound", err)
	}
}

// Delete returns the removed scan with all its parts so the caller can remove
// every file from disk, and leaves no orphan child rows.
func TestBillScanRepository_DeleteReturnsAllParts(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	repo := NewBillScanRepository(db)
	if _, err := repo.Create(ctx, domain.BillScan{
		ScanToken: "tok-multi", ImagePath: "/data/bills/part-1.jpg",
		FileHash: "hash-top", Files: threeScanFiles(),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	removed, err := repo.Delete(ctx, "tok-multi")
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(removed.Files) != 3 {
		t.Fatalf("removed scan carried %d files, want 3", len(removed.Files))
	}

	var children int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bill_scan_files`).Scan(&children); err != nil {
		t.Fatal(err)
	}
	if children != 0 {
		t.Fatalf("orphan bill_scan_files rows: %d", children)
	}
}

// The TTL sweeper gets every stored part's path, not just part 1's mirror.
func TestBillScanRepository_DeleteStaleReturnsAllParts(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	repo := NewBillScanRepository(db)
	if _, err := repo.Create(ctx, domain.BillScan{
		ScanToken: "tok-stale", ImagePath: "/data/bills/part-1.jpg",
		FileHash: "hash-top", Files: threeScanFiles(),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.MarkDone(ctx, "tok-stale", &domain.BillDraft{MarketName: "M"}); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}
	// Age the row past the cutoff (the repo stamps updated_at on writes).
	if _, err := db.ExecContext(ctx, `UPDATE bill_scans SET updated_at = 1 WHERE token = ?`, "tok-stale"); err != nil {
		t.Fatal(err)
	}

	paths, err := repo.DeleteStale(ctx, time.Now())
	if err != nil {
		t.Fatalf("DeleteStale: %v", err)
	}
	if len(paths) != 3 {
		t.Fatalf("sweeper returned %d paths, want all 3 parts: %q", len(paths), paths)
	}
	for _, want := range []string{"/data/bills/part-1.jpg", "/data/bills/part-2.jpg", "/data/bills/part-3.jpg"} {
		found := false
		for _, p := range paths {
			if p == want {
				found = true
			}
		}
		if !found {
			t.Errorf("sweeper paths missing %s: %q", want, paths)
		}
	}

	var children int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bill_scan_files`).Scan(&children); err != nil {
		t.Fatal(err)
	}
	if children != 0 {
		t.Fatalf("orphan bill_scan_files rows: %d", children)
	}
}

// A pre-feature row (single file in the legacy columns, no children) is still
// swept, falling back to the legacy path.
func TestBillScanRepository_DeleteStaleLegacyRow(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	repo := NewBillScanRepository(db)
	if _, err := repo.Create(ctx, domain.BillScan{
		ScanToken: "tok-legacy", ImagePath: "/data/bills/legacy.jpg", FileHash: "hash-legacy",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.MarkDone(ctx, "tok-legacy", &domain.BillDraft{MarketName: "M"}); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE bill_scans SET updated_at = 1 WHERE token = ?`, "tok-legacy"); err != nil {
		t.Fatal(err)
	}

	paths, err := repo.DeleteStale(ctx, time.Now())
	if err != nil {
		t.Fatalf("DeleteStale: %v", err)
	}
	if len(paths) != 1 || paths[0] != "/data/bills/legacy.jpg" {
		t.Fatalf("legacy sweeper paths = %q, want the single legacy path", paths)
	}
}
