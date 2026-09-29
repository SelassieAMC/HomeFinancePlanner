package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"home-finance-planner/backend/internal/domain"
)

// productNameMappingColumns reads one mapping row with its category join.
const productNameMappingColumns = `
	m.id, m.raw_name, m.standard_name, m.generic_name, m.category_id, COALESCE(c.name, ''), m.source,
	m.created_at, m.updated_at`

const productNameMappingFrom = `
	FROM product_name_mappings m
	LEFT JOIN categories c ON c.id = m.category_id`

// ProductNameMappingRepository is the SQLite-backed implementation of the
// normalization-memory contract. RawName is unique case-insensitively
// (idx_product_name_mappings_raw COLLATE NOCASE) — one remembered decision
// per raw text. Create never overwrites an existing decision; Upsert does
// (explicit user overrides only).
type ProductNameMappingRepository struct{ db *sql.DB }

func NewProductNameMappingRepository(db *sql.DB) *ProductNameMappingRepository {
	return &ProductNameMappingRepository{db: db}
}

// FindByRawName returns the mapping for a raw text, matching case-insensitively.
func (r *ProductNameMappingRepository) FindByRawName(ctx context.Context, raw string) (domain.ProductNameMapping, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+productNameMappingColumns+productNameMappingFrom+`
		WHERE m.raw_name = ? COLLATE NOCASE`, raw)
	m, err := scanProductNameMapping(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ProductNameMapping{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.ProductNameMapping{}, fmt.Errorf("get mapping for %q: %w", raw, err)
	}
	return m, nil
}

// Create inserts a new mapping and returns it with id and timestamps filled
// in. A raw text that is already mapped reports domain.ErrConflict — the
// AI and manual paths never overwrite an existing decision.
func (r *ProductNameMappingRepository) Create(ctx context.Context, m domain.ProductNameMapping) (domain.ProductNameMapping, error) {
	now := time.Now().Unix()
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO product_name_mappings (raw_name, standard_name, generic_name, category_id, source, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		m.RawName, m.StandardName, m.GenericName, m.CategoryID, string(m.Source), now, now)
	if err != nil {
		return domain.ProductNameMapping{}, mapWriteError("create product mapping", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return domain.ProductNameMapping{}, fmt.Errorf("product mapping insert id: %w", err)
	}
	return r.GetByID(ctx, id)
}

// Upsert overwrites the decision for a raw text (explicit user overrides
// only) and returns the stored row.
func (r *ProductNameMappingRepository) Upsert(ctx context.Context, m domain.ProductNameMapping) (domain.ProductNameMapping, error) {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO product_name_mappings (raw_name, standard_name, generic_name, category_id, source, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(raw_name) DO UPDATE SET
			standard_name = excluded.standard_name,
			generic_name   = excluded.generic_name,
			category_id    = excluded.category_id,
			source         = excluded.source,
			updated_at     = excluded.updated_at`,
		m.RawName, m.StandardName, m.GenericName, m.CategoryID, string(m.Source), time.Now().Unix(), time.Now().Unix())
	if err != nil {
		return domain.ProductNameMapping{}, mapWriteError("upsert product mapping", err)
	}
	return r.FindByRawName(ctx, m.RawName)
}

// GetByID returns one mapping, or domain.ErrNotFound for unknown ids.
func (r *ProductNameMappingRepository) GetByID(ctx context.Context, id int64) (domain.ProductNameMapping, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+productNameMappingColumns+productNameMappingFrom+` WHERE m.id = ?`, id)
	m, err := scanProductNameMapping(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ProductNameMapping{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.ProductNameMapping{}, fmt.Errorf("get product mapping %d: %w", id, err)
	}
	return m, nil
}

// UnmappedProductNames lists products whose raw name has no mapping yet, or
// whose mapping has no generic name (the input of the "analyze existing
// products" job, which creates the former and fills the generic gap on the
// latter), oldest products first. The category is carried along so the job
// can stamp it onto a new mapping.
func (r *ProductNameMappingRepository) UnmappedProductNames(ctx context.Context, limit int) ([]domain.Product, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT p.id, p.name, p.category_id
		FROM products p
		LEFT JOIN product_name_mappings m ON p.name = m.raw_name COLLATE NOCASE
		WHERE m.id IS NULL OR m.generic_name = ''
		ORDER BY p.id
		LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list unmapped product names: %w", err)
	}
	defer rows.Close()

	products := []domain.Product{}
	for rows.Next() {
		var p domain.Product
		if err := rows.Scan(&p.ID, &p.Name, &p.CategoryID); err != nil {
			return nil, fmt.Errorf("scan unmapped product row: %w", err)
		}
		products = append(products, p)
	}
	return products, rows.Err()
}

// CountUnmappedProductNames totals the products without a mapping or without
// a generic name on their mapping — the denominator of the backfill job's
// progress.
func (r *ProductNameMappingRepository) CountUnmappedProductNames(ctx context.Context) (int64, error) {
	var n int64
	err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM products p
		LEFT JOIN product_name_mappings m ON p.name = m.raw_name COLLATE NOCASE
		WHERE m.id IS NULL OR m.generic_name = ''`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count unmapped product names: %w", err)
	}
	return n, nil
}

// GetJob returns the single product-normalization job row (id 1).
func (r *ProductNameMappingRepository) GetJob(ctx context.Context) (domain.ProductNormalizationJob, error) {
	var (
		j         domain.ProductNormalizationJob
		createdAt int64
		updatedAt int64
	)
	err := r.db.QueryRowContext(ctx, `
		SELECT status, total_names, processed_names, mapped_names, error, created_at, updated_at
		FROM product_normalization_jobs WHERE id = 1`).
		Scan(&j.Status, &j.TotalNames, &j.ProcessedNames, &j.MappedNames, &j.Error, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ProductNormalizationJob{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.ProductNormalizationJob{}, fmt.Errorf("get normalization job: %w", err)
	}
	j.CreatedAt = time.Unix(createdAt, 0).UTC()
	j.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return j, nil
}

// UpdateJob rewrites the single job row. Only the mutable fields are written.
func (r *ProductNameMappingRepository) UpdateJob(ctx context.Context, j domain.ProductNormalizationJob) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE product_normalization_jobs
		SET status = ?, total_names = ?, processed_names = ?, mapped_names = ?, error = ?, updated_at = ?
		WHERE id = 1`,
		string(j.Status), j.TotalNames, j.ProcessedNames, j.MappedNames, j.Error, time.Now().Unix())
	if err != nil {
		return mapWriteError("update normalization job", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func scanProductNameMapping(row interface{ Scan(...any) error }) (domain.ProductNameMapping, error) {
	var (
		m         domain.ProductNameMapping
		source    string
		createdAt int64
		updatedAt int64
	)
	if err := row.Scan(&m.ID, &m.RawName, &m.StandardName, &m.GenericName, &m.CategoryID, &m.CategoryName,
		&source, &createdAt, &updatedAt); err != nil {
		return domain.ProductNameMapping{}, err
	}
	m.Source = domain.MappingSource(source)
	m.CreatedAt = time.Unix(createdAt, 0).UTC()
	m.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return m, nil
}
