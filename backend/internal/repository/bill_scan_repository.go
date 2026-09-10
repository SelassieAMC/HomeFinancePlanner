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

const billScanColumns = `
	id, token, status, image_path, mime_type, provider_id, draft_json, error, created_at, updated_at`

// Create inserts a scan row in the analyzing state.
func (r *BillScanRepository) Create(ctx context.Context, s domain.BillScan) (domain.BillScan, error) {
	now := time.Now().Unix()
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO bill_scans
			(token, status, image_path, mime_type, provider_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		s.ScanToken, string(domain.BillScanAnalyzing), s.ImagePath, s.MimeType, s.ProviderID, now, now)
	if err != nil {
		return domain.BillScan{}, mapWriteError("create bill scan", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return domain.BillScan{}, fmt.Errorf("bill scan insert id: %w", err)
	}
	s.ID = id
	s.Status = domain.BillScanAnalyzing
	s.CreatedAt = time.Unix(now, 0).UTC()
	return s, nil
}

// GetByToken returns the scan row, or domain.ErrNotFound for unknown/consumed
// tokens. The stored draft_json is decoded when present.
func (r *BillScanRepository) GetByToken(ctx context.Context, token string) (domain.BillScan, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+billScanColumns+` FROM bill_scans WHERE token = ?`, token)
	s, err := scanBillScan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.BillScan{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.BillScan{}, fmt.Errorf("get bill scan: %w", err)
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
		`SELECT `+billScanColumns+` FROM bill_scans WHERE `+where+` ORDER BY created_at DESC, id DESC LIMIT ?`,
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

// Delete removes the scan row and returns the removed one so the caller can
// delete its receipt file (kept on disk when confirming, as the bill's record).
func (r *BillScanRepository) Delete(ctx context.Context, token string) (domain.BillScan, error) {
	s, err := r.GetByToken(ctx, token)
	if err != nil {
		return domain.BillScan{}, err
	}
	if _, err := r.db.ExecContext(ctx, `DELETE FROM bill_scans WHERE token = ?`, token); err != nil {
		return domain.BillScan{}, fmt.Errorf("delete bill scan: %w", err)
	}
	return s, nil
}

// DeleteStale removes done/failed scans older than the cutoff (their drafts
// were never confirmed) and returns the image paths whose files should be
// removed from disk.
func (r *BillScanRepository) DeleteStale(ctx context.Context, olderThan time.Time) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, image_path FROM bill_scans
		WHERE status IN (?, ?) AND updated_at < ?`,
		string(domain.BillScanDone), string(domain.BillScanFailed), olderThan.Unix())
	if err != nil {
		return nil, fmt.Errorf("find stale bill scans: %w", err)
	}
	defer rows.Close()

	var ids []int64
	var paths []string
	for rows.Next() {
		var id int64
		var path string
		if err := rows.Scan(&id, &path); err != nil {
			return nil, fmt.Errorf("scan stale bill scan row: %w", err)
		}
		ids, paths = append(ids, id), append(paths, path)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, id := range ids {
		if _, err := r.db.ExecContext(ctx, `DELETE FROM bill_scans WHERE id = ?`, id); err != nil {
			return paths, fmt.Errorf("delete stale bill scan %d: %w", id, err)
		}
	}
	return paths, nil
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
		&s.ProviderID, &draftJSON, &s.Error, &createdAt, &updatedAt); err != nil {
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
