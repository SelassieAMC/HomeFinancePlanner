package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"home-finance-planner/backend/internal/domain"
)

// BillScanRepository is the SQLite-backed store for the scan pipeline: one row
// per uploaded receipt from upload until confirm (row deleted, receipt kept)
// or discard (row and receipt deleted).
type BillScanRepository struct{ db *sql.DB }

func NewBillScanRepository(db *sql.DB) *BillScanRepository { return &BillScanRepository{db: db} }

// billScanColumns reads the scan row (aliased s) with its receipt-part count.
// The single-file columns mirror part 1; bill_scan_files holds every part.
const billScanColumns = `
	s.id, s.token, s.status, s.image_path, s.mime_type, s.provider_id, s.draft_json, s.error, s.file_hash,
	(SELECT COUNT(*) FROM bill_scan_files f WHERE f.scan_id = s.id) AS file_count,
	s.created_at, s.updated_at`

// Create inserts a scan row in the analyzing state together with its receipt
// parts (position 1..n). The row's own single-file columns mirror part 1.
func (r *BillScanRepository) Create(ctx context.Context, s domain.BillScan) (domain.BillScan, error) {
	now := time.Now().Unix()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.BillScan{}, fmt.Errorf("begin bill scan create: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		INSERT INTO bill_scans
			(token, status, image_path, mime_type, provider_id, file_hash, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ScanToken, string(domain.BillScanAnalyzing), s.ImagePath, s.MimeType, s.ProviderID, s.FileHash, now, now)
	if err != nil {
		return domain.BillScan{}, mapWriteError("create bill scan", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return domain.BillScan{}, fmt.Errorf("bill scan insert id: %w", err)
	}

	for i, f := range s.Files {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO bill_scan_files
				(scan_id, position, file_path, mime_type, file_hash, created_at)
			VALUES (?, ?, ?, ?, ?, ?)`,
			id, i+1, f.Path, f.MimeType, f.FileHash, now); err != nil {
			return domain.BillScan{}, mapWriteError("create bill scan file", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return domain.BillScan{}, fmt.Errorf("commit bill scan create: %w", err)
	}
	s.ID = id
	s.Status = domain.BillScanAnalyzing
	s.CreatedAt = time.Unix(now, 0).UTC()
	s.FileCount = len(s.Files)
	return s, nil
}

// GetByToken returns the scan row, or domain.ErrNotFound for unknown/consumed
// tokens. The stored draft_json is decoded when present; Files carries every
// stored receipt part.
func (r *BillScanRepository) GetByToken(ctx context.Context, token string) (domain.BillScan, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+billScanColumns+` FROM bill_scans s WHERE s.token = ?`, token)
	s, err := scanBillScan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.BillScan{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.BillScan{}, fmt.Errorf("get bill scan: %w", err)
	}
	files, err := r.scanFilesFor(ctx, s.ID)
	if err != nil {
		return domain.BillScan{}, err
	}
	s.Files = files
	return s, nil
}

// GetByFileHash returns the (single) scan row carrying this receipt-part hash,
// or domain.ErrNotFound. Used to reject re-uploads of a receipt that is
// already in the pipeline — any part matching is a conflict. Failed scans
// are dead ends the user can only abandon, so they do NOT match: a photo
// whose read failed must be re-uploadable (with a retake, a rotation or a
// different connector). Done scans still match — their receipt is awaiting
// review and may or may not become a bill (the bill dedup covers the saved
// ones separately).
func (r *BillScanRepository) GetByFileHash(ctx context.Context, hash string) (domain.BillScan, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+billScanColumns+`
		 FROM bill_scans s JOIN bill_scan_files f ON f.scan_id = s.id
		 WHERE f.file_hash = ? AND s.status != ? LIMIT 1`, hash, domain.BillScanFailed)
	s, err := scanBillScan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.BillScan{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.BillScan{}, fmt.Errorf("get bill scan by hash: %w", err)
	}
	return s, nil
}

// List returns recent scans in the given states (all states when empty),
// newest first.
func (r *BillScanRepository) List(ctx context.Context, statuses []domain.BillScanStatus, limit int) ([]domain.BillScan, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	where, args := "1 = 1", []any{}
	if len(statuses) > 0 {
		ph := make([]string, 0, len(statuses))
		for _, st := range statuses {
			ph = append(ph, "?")
			args = append(args, string(st))
		}
		where = "status IN (" + strings.Join(ph, ",") + ")"
	}
	args = append(args, limit)

	rows, err := r.db.QueryContext(ctx,
		`SELECT `+billScanColumns+` FROM bill_scans s WHERE `+where+` ORDER BY s.created_at DESC, s.id DESC LIMIT ?`,
		args...)
	if err != nil {
		return nil, fmt.Errorf("list bill scans: %w", err)
	}
	defer rows.Close()

	scans := []domain.BillScan{}
	for rows.Next() {
		s, err := scanBillScan(rows)
		if err != nil {
			return nil, fmt.Errorf("scan bill scan row: %w", err)
		}
		scans = append(scans, s)
	}
	return scans, rows.Err()
}

// MarkDone stores the extraction result. The write is guarded on
// status='analyzing' so a result is never written for a scan the user already
// confirmed or discarded (those delete the row, so this matches 0 rows).
func (r *BillScanRepository) MarkDone(ctx context.Context, token string, draft *domain.BillDraft) error {
	raw, err := json.Marshal(draft)
	if err != nil {
		return fmt.Errorf("encode scan draft: %w", err)
	}
	res, err := r.db.ExecContext(ctx, `
		UPDATE bill_scans
		SET status = ?, draft_json = ?, error = '', updated_at = ?
		WHERE token = ? AND status = ?`,
		string(domain.BillScanDone), raw, time.Now().Unix(), token, string(domain.BillScanAnalyzing))
	if err != nil {
		return mapWriteError("mark scan done", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("mark scan done: %w", domain.ErrNotFound)
	}
	return nil
}

// MarkFailed records the extraction error, clearing any stored draft.
func (r *BillScanRepository) MarkFailed(ctx context.Context, token, msg string) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE bill_scans
		SET status = ?, draft_json = '', error = ?, updated_at = ?
		WHERE token = ? AND status = ?`,
		string(domain.BillScanFailed), msg, time.Now().Unix(), token, string(domain.BillScanAnalyzing))
	if err != nil {
		return mapWriteError("mark scan failed", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("mark scan failed: %w", domain.ErrNotFound)
	}
	return nil
}

// ClaimRetry atomically moves a finished (done or failed) scan back to
// analyzing for a re-extraction. It returns false when the row is missing or
// still analyzing, which makes double-enqueues impossible.
func (r *BillScanRepository) ClaimRetry(ctx context.Context, token, providerID string) (bool, error) {
	res, err := r.db.ExecContext(ctx, `
		UPDATE bill_scans
		SET status = ?, provider_id = ?, draft_json = '', error = '', updated_at = ?
		WHERE token = ? AND status IN (?, ?)`,
		string(domain.BillScanAnalyzing), providerID, time.Now().Unix(), token,
		string(domain.BillScanDone), string(domain.BillScanFailed))
	if err != nil {
		return false, mapWriteError("claim scan retry", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// Delete removes the scan row (and its receipt-part rows) and returns the
// removed one so the caller can delete the files (kept on disk when
// confirming, as the bill's record).
func (r *BillScanRepository) Delete(ctx context.Context, token string) (domain.BillScan, error) {
	s, err := r.GetByToken(ctx, token)
	if err != nil {
		return domain.BillScan{}, err
	}
	if _, err := r.db.ExecContext(ctx, `DELETE FROM bill_scan_files WHERE scan_id = ?`, s.ID); err != nil {
		return domain.BillScan{}, fmt.Errorf("delete bill scan files: %w", err)
	}
	if _, err := r.db.ExecContext(ctx, `DELETE FROM bill_scans WHERE token = ?`, token); err != nil {
		return domain.BillScan{}, fmt.Errorf("delete bill scan: %w", err)
	}
	return s, nil
}

// DeleteStale removes done/failed scans older than the cutoff (their drafts
// were never confirmed) and returns the file paths whose receipt parts should
// be removed from disk — every stored part, not just part 1's mirror.
func (r *BillScanRepository) DeleteStale(ctx context.Context, olderThan time.Time) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT s.id, s.image_path, f.file_path FROM bill_scans s
		LEFT JOIN bill_scan_files f ON f.scan_id = s.id
		WHERE s.status IN (?, ?) AND s.updated_at < ?`,
		string(domain.BillScanDone), string(domain.BillScanFailed), olderThan.Unix())
	if err != nil {
		return nil, fmt.Errorf("find stale bill scans: %w", err)
	}
	defer rows.Close()

	type staleScan struct {
		id     int64
		legacy string
		paths  []string
	}
	var order []int64
	byID := map[int64]*staleScan{}
	for rows.Next() {
		var id int64
		var legacy, child sql.NullString
		if err := rows.Scan(&id, &legacy, &child); err != nil {
			return nil, fmt.Errorf("scan stale bill scan row: %w", err)
		}
		e, ok := byID[id]
		if !ok {
			e = &staleScan{id: id, legacy: legacy.String}
			byID[id] = e
			order = append(order, id)
		}
		if child.Valid && child.String != "" {
			e.paths = append(e.paths, child.String)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var paths []string
	for _, id := range order {
		e := byID[id]
		if len(e.paths) == 0 && e.legacy != "" {
			e.paths = []string{e.legacy} // pre-feature row without children
		}
		paths = append(paths, e.paths...)
	}
	for _, id := range order {
		if _, err := r.db.ExecContext(ctx, `DELETE FROM bill_scan_files WHERE scan_id = ?`, id); err != nil {
			return paths, fmt.Errorf("delete stale bill scan files %d: %w", id, err)
		}
		if _, err := r.db.ExecContext(ctx, `DELETE FROM bill_scans WHERE id = ?`, id); err != nil {
			return paths, fmt.Errorf("delete stale bill scan %d: %w", id, err)
		}
	}
	return paths, nil
}

// scanFilesFor loads one scan's receipt parts in position order.
func (r *BillScanRepository) scanFilesFor(ctx context.Context, scanID int64) ([]domain.BillScanFile, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, scan_id, position, file_path, mime_type, file_hash, created_at
		FROM bill_scan_files WHERE scan_id = ? ORDER BY position`, scanID)
	if err != nil {
		return nil, fmt.Errorf("list bill scan files: %w", err)
	}
	defer rows.Close()

	out := []domain.BillScanFile{}
	for rows.Next() {
		var f domain.BillScanFile
		var createdAt int64
		if err := rows.Scan(&f.ID, &f.ScanID, &f.Position, &f.Path, &f.MimeType, &f.FileHash, &createdAt); err != nil {
			return nil, fmt.Errorf("scan bill scan file row: %w", err)
		}
		f.CreatedAt = time.Unix(createdAt, 0).UTC()
		out = append(out, f)
	}
	return out, rows.Err()
}

func scanBillScan(row interface{ Scan(...any) error }) (domain.BillScan, error) {
	var (
		s         domain.BillScan
		status    string
		draftJSON string
		createdAt int64
		updatedAt int64
	)
	if err := row.Scan(&s.ID, &s.ScanToken, &status, &s.ImagePath, &s.MimeType,
		&s.ProviderID, &draftJSON, &s.Error, &s.FileHash, &s.FileCount, &createdAt, &updatedAt); err != nil {
		return domain.BillScan{}, err
	}
	s.Status = domain.BillScanStatus(status)
	if draftJSON != "" {
		s.Draft = &domain.BillDraft{}
		if err := json.Unmarshal([]byte(draftJSON), s.Draft); err != nil {
			return domain.BillScan{}, fmt.Errorf("decode scan draft: %w", err)
		}
	}
	s.CreatedAt = time.Unix(createdAt, 0).UTC()
	s.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return s, nil
}
