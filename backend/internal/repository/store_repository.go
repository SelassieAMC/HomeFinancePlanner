package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"home-finance-planner/backend/internal/domain"
)

// storeColumns + storeFrom read a store together with a display-only bill
// count (how many bills link to it).
const storeColumns = `
	s.id, s.name, s.description, s.location, s.logo_path, s.created_at, s.updated_at,
	(SELECT COUNT(*) FROM bills b WHERE b.store_id = s.id) AS bill_count`

const storeFrom = `FROM stores s`

// StoreRepository is the SQLite-backed implementation of the store contract.
// Names are unique case-insensitively (idx_stores_name COLLATE NOCASE), which
// is what BillService's find-or-create resolves through on a race.
type StoreRepository struct{ db *sql.DB }

func NewStoreRepository(db *sql.DB) *StoreRepository { return &StoreRepository{db: db} }

// List returns every store ordered by name (case-insensitively).
func (r *StoreRepository) List(ctx context.Context) ([]domain.Store, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+storeColumns+` `+storeFrom+` ORDER BY s.name COLLATE NOCASE`)
	if err != nil {
		return nil, fmt.Errorf("list stores: %w", err)
	}
	defer rows.Close()

	stores := []domain.Store{}
	for rows.Next() {
		s, err := scanStore(rows)
		if err != nil {
			return nil, fmt.Errorf("scan store row: %w", err)
		}
		stores = append(stores, s)
	}
	return stores, rows.Err()
}

// GetByID returns one store, or domain.ErrNotFound for unknown ids.
func (r *StoreRepository) GetByID(ctx context.Context, id int64) (domain.Store, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+storeColumns+` `+storeFrom+` WHERE s.id = ?`, id)
	s, err := scanStore(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Store{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Store{}, fmt.Errorf("get store %d: %w", id, err)
	}
	return s, nil
}

// FindByName returns the store whose name matches case-insensitively.
func (r *StoreRepository) FindByName(ctx context.Context, name string) (domain.Store, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+storeColumns+` `+storeFrom+` WHERE s.name = ? COLLATE NOCASE`, name)
	s, err := scanStore(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Store{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Store{}, fmt.Errorf("find store %q: %w", name, err)
	}
	return s, nil
}

// Create inserts a store and returns it with its id and timestamps filled in.
func (r *StoreRepository) Create(ctx context.Context, s domain.Store) (domain.Store, error) {
	now := time.Now().Unix()
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO stores (name, description, location, logo_path, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		s.Name, s.Description, s.Location, s.LogoPath, now, now)
	if err != nil {
		return domain.Store{}, mapWriteError("create store", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return domain.Store{}, fmt.Errorf("store insert id: %w", err)
	}
	return r.GetByID(ctx, id)
}

// Update rewrites the user-editable fields. It deliberately never touches
// logo_path — logo files are owned by SetLogo only.
func (r *StoreRepository) Update(ctx context.Context, s domain.Store) (domain.Store, error) {
	res, err := r.db.ExecContext(ctx, `
		UPDATE stores SET name = ?, description = ?, location = ?, updated_at = ?
		WHERE id = ?`,
		s.Name, s.Description, s.Location, time.Now().Unix(), s.ID)
	if err != nil {
		return domain.Store{}, mapWriteError("update store", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.Store{}, domain.ErrNotFound
	}
	return r.GetByID(ctx, s.ID)
}

// SetLogo stores (or clears, with an empty path) the logo file path.
func (r *StoreRepository) SetLogo(ctx context.Context, id int64, logoPath string) (domain.Store, error) {
	res, err := r.db.ExecContext(ctx, `
		UPDATE stores SET logo_path = ?, updated_at = ? WHERE id = ?`,
		logoPath, time.Now().Unix(), id)
	if err != nil {
		return domain.Store{}, mapWriteError("set store logo", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.Store{}, domain.ErrNotFound
	}
	return r.GetByID(ctx, id)
}

// Delete removes a store (bills keep their market_name; the FK sets their
// store_id to NULL).
func (r *StoreRepository) Delete(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM stores WHERE id = ?`, id)
	if err != nil {
		return mapWriteError("delete store", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func scanStore(row interface{ Scan(...any) error }) (domain.Store, error) {
	var (
		s         domain.Store
		logoPath  string
		createdAt int64
		updatedAt int64
	)
	if err := row.Scan(&s.ID, &s.Name, &s.Description, &s.Location, &logoPath,
		&createdAt, &updatedAt, &s.BillCount); err != nil {
		return domain.Store{}, err
	}
	s.LogoPath = logoPath
	s.HasLogo = logoPath != ""
	s.CreatedAt = time.Unix(createdAt, 0).UTC()
	s.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return s, nil
}
