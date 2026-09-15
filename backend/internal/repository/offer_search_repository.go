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

// OfferSearchRepository is the SQLite-backed store for the offer-search
// pipeline: one row per confirmed cart from creation until it is deleted
// manually (done rows are the kept record) or TTL-swept (failed rows only).
type OfferSearchRepository struct{ db *sql.DB }

func NewOfferSearchRepository(db *sql.DB) *OfferSearchRepository {
	return &OfferSearchRepository{db: db}
}

const offerSearchColumns = `
	id, token, status, provider_id, request_json, result_json, error, created_at, updated_at`

// Create inserts a search row in the searching state.
func (r *OfferSearchRepository) Create(ctx context.Context, s domain.OfferSearch) (domain.OfferSearch, error) {
	now := time.Now().Unix()
	reqJSON, err := json.Marshal(s.Products)
	if err != nil {
		return domain.OfferSearch{}, fmt.Errorf("encode offer search request: %w", err)
	}
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO offer_searches
			(token, status, provider_id, request_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		s.SearchToken, string(domain.OfferSearchSearching), s.ProviderID, reqJSON, now, now)
	if err != nil {
		return domain.OfferSearch{}, mapWriteError("create offer search", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return domain.OfferSearch{}, fmt.Errorf("offer search insert id: %w", err)
	}
	s.ID = id
	s.Status = domain.OfferSearchSearching
	s.CreatedAt = time.Unix(now, 0).UTC()
	return s, nil
}

// GetByToken returns the search row, or domain.ErrNotFound for unknown tokens.
// The stored request_json and result_json are decoded when present.
func (r *OfferSearchRepository) GetByToken(ctx context.Context, token string) (domain.OfferSearch, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+offerSearchColumns+` FROM offer_searches WHERE token = ?`, token)
	s, err := scanOfferSearch(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.OfferSearch{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.OfferSearch{}, fmt.Errorf("get offer search: %w", err)
	}
	return s, nil
}

// List returns recent searches in the given states (all states when empty),
// newest first.
func (r *OfferSearchRepository) List(ctx context.Context, statuses []domain.OfferSearchStatus, limit int) ([]domain.OfferSearch, error) {
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
		`SELECT `+offerSearchColumns+` FROM offer_searches WHERE `+where+` ORDER BY created_at DESC, id DESC LIMIT ?`,
		args...)
	if err != nil {
		return nil, fmt.Errorf("list offer searches: %w", err)
	}
	defer rows.Close()

	searches := []domain.OfferSearch{}
	for rows.Next() {
		s, err := scanOfferSearch(rows)
		if err != nil {
			return nil, fmt.Errorf("scan offer search row: %w", err)
		}
		searches = append(searches, s)
	}
	return searches, rows.Err()
}

// MarkDone stores the normalized result. The write is guarded on
// status='searching' so a result is never written for a row the user already
// deleted (this matches 0 rows there).
func (r *OfferSearchRepository) MarkDone(ctx context.Context, token string, res *domain.OfferResult) error {
	raw, err := json.Marshal(res)
	if err != nil {
		return fmt.Errorf("encode offer result: %w", err)
	}
	w, err := r.db.ExecContext(ctx, `
		UPDATE offer_searches
		SET status = ?, result_json = ?, error = '', updated_at = ?
		WHERE token = ? AND status = ?`,
		string(domain.OfferSearchDone), raw, time.Now().Unix(), token, string(domain.OfferSearchSearching))
	if err != nil {
		return mapWriteError("mark offer search done", err)
	}
	if n, _ := w.RowsAffected(); n == 0 {
		return fmt.Errorf("mark offer search done: %w", domain.ErrNotFound)
	}
	return nil
}

// MarkFailed records the search error, clearing any stored result.
func (r *OfferSearchRepository) MarkFailed(ctx context.Context, token, msg string) error {
	w, err := r.db.ExecContext(ctx, `
		UPDATE offer_searches
		SET status = ?, result_json = '', error = ?, updated_at = ?
		WHERE token = ? AND status = ?`,
		string(domain.OfferSearchFailed), msg, time.Now().Unix(), token, string(domain.OfferSearchSearching))
	if err != nil {
		return mapWriteError("mark offer search failed", err)
	}
	if n, _ := w.RowsAffected(); n == 0 {
		return fmt.Errorf("mark offer search failed: %w", domain.ErrNotFound)
	}
	return nil
}

// ClaimRetry atomically moves a finished (done or failed) search back to
// searching. It returns false when the row is missing or still searching,
// which makes double-enqueues impossible.
func (r *OfferSearchRepository) ClaimRetry(ctx context.Context, token, providerID string) (bool, error) {
	w, err := r.db.ExecContext(ctx, `
		UPDATE offer_searches
		SET status = ?, provider_id = ?, result_json = '', error = '', updated_at = ?
		WHERE token = ? AND status IN (?, ?)`,
		string(domain.OfferSearchSearching), providerID, time.Now().Unix(), token,
		string(domain.OfferSearchDone), string(domain.OfferSearchFailed))
	if err != nil {
		return false, mapWriteError("claim offer search retry", err)
	}
	n, _ := w.RowsAffected()
	return n > 0, nil
}

// Delete removes the search row (the persisted result goes with it — done
// rows are only discarded on purpose).
func (r *OfferSearchRepository) Delete(ctx context.Context, token string) error {
	s, err := r.GetByToken(ctx, token)
	if err != nil {
		return err
	}
	if _, err := r.db.ExecContext(ctx, `DELETE FROM offer_searches WHERE token = ?`, s.SearchToken); err != nil {
		return fmt.Errorf("delete offer search: %w", err)
	}
	return nil
}

// DeleteStale removes failed searches older than the cutoff (done rows are the
// kept record and are never swept) and returns the removed tokens.
func (r *OfferSearchRepository) DeleteStale(ctx context.Context, olderThan time.Time) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id FROM offer_searches
		WHERE status = ? AND updated_at < ?`,
		string(domain.OfferSearchFailed), olderThan.Unix())
	if err != nil {
		return nil, fmt.Errorf("find stale offer searches: %w", err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan stale offer search row: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	tokens := []string{}
	for _, id := range ids {
		var token string
		if err := r.db.QueryRowContext(ctx,
			`SELECT token FROM offer_searches WHERE id = ?`, id).Scan(&token); err != nil {
			return tokens, fmt.Errorf("delete stale offer search %d: %w", id, err)
		}
		if _, err := r.db.ExecContext(ctx, `DELETE FROM offer_searches WHERE id = ?`, id); err != nil {
			return tokens, fmt.Errorf("delete stale offer search %d: %w", id, err)
		}
		tokens = append(tokens, token)
	}
	return tokens, nil
}

func scanOfferSearch(row interface{ Scan(...any) error }) (domain.OfferSearch, error) {
	var (
		s         domain.OfferSearch
		status    string
		reqJSON   string
		resJSON   string
		createdAt int64
		updatedAt int64
	)
	if err := row.Scan(&s.ID, &s.SearchToken, &status, &s.ProviderID, &reqJSON, &resJSON, &s.Error, &createdAt, &updatedAt); err != nil {
		return domain.OfferSearch{}, err
	}
	s.Status = domain.OfferSearchStatus(status)
	if reqJSON != "" {
		if err := json.Unmarshal([]byte(reqJSON), &s.Products); err != nil {
			return domain.OfferSearch{}, fmt.Errorf("decode offer search request: %w", err)
		}
	}
	if resJSON != "" {
		s.Result = &domain.OfferResult{}
		if err := json.Unmarshal([]byte(resJSON), s.Result); err != nil {
			return domain.OfferSearch{}, fmt.Errorf("decode offer result: %w", err)
		}
	}
	s.CreatedAt = time.Unix(createdAt, 0).UTC()
	s.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return s, nil
}
