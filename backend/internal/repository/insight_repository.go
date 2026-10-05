package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"home-finance-planner/backend/internal/domain"
)

// InsightRepository is the SQLite-backed store for product insights (the
// deferred-intelligence nudges) plus the price-per-unit history reads the
// analysis job feeds itself with. A purchase line contributes a history row
// when it is analyzable: linked product, real positive spend and a known size
// printed in a canonical unit — deposit/return lines (never product-linked)
// and sizeless lines are excluded in SQL.
type InsightRepository struct{ db *sql.DB }

func NewInsightRepository(db *sql.DB) *InsightRepository {
	return &InsightRepository{db: db}
}

const insightColumns = `
	i.id, i.kind, i.product_id, i.product_name, COALESCE(i.generic_name, ''), i.currency,
	i.message, i.source, i.data_json, i.acknowledged, i.created_at, i.updated_at`

// historyTemplate selects one analyzable purchase row. The two %s filters
// differ per branch (the table's own alias), so the product/family scope is
// injected literally and its placeholders bind once per branch — the same
// injectable-scope pattern productStatsCTE uses. The subquery carries the
// newest-scoped LIMIT; the outer select restores row order deterministically
// (the same ranking key productStatsCTE ranks by: date, source, purchase id,
// line id).
const historyTemplate = `
SELECT x.product_id, x.raw_name, x.date, x.currency, x.unit, x.unit_price_cents,
       x.unit_value, x.store_name, x.src, x.src_id, x.line_id
FROM (
	SELECT bi.product_id, bi.name AS raw_name, b.date AS date, b.currency,
	       bi.unit, bi.unit_price_cents, bi.unit_value, COALESCE(st.name, '—') AS store_name,
	       'b' AS src, b.id AS src_id, bi.id AS line_id
	FROM bill_items bi
	JOIN bills b ON b.id = bi.bill_id
	LEFT JOIN stores st ON st.id = b.store_id
	WHERE b.status = 'accepted' AND bi.is_return = 0%s
	  AND bi.quantity > 0 AND bi.unit_price_cents > 0 AND COALESCE(bi.unit_value, 0) > 0
	  AND bi.unit IN ('kg', 'g', 'l', 'ml', 'pcs')
	UNION ALL
	SELECT ti.product_id, ti.name, t.date, t.currency,
	       ti.unit, ti.unit_price_cents, ti.unit_value, '' AS store_name,
	       't', t.id, ti.id
	FROM transaction_items ti
	JOIN transactions t ON t.id = ti.transaction_id
	WHERE ti.product_id IS NOT NULL%s
	  AND ti.quantity > 0 AND ti.unit_price_cents > 0 AND COALESCE(ti.unit_value, 0) > 0
	  AND ti.unit IN ('kg', 'g', 'l', 'ml', 'pcs')
	ORDER BY date DESC LIMIT ?
) x
ORDER BY x.date DESC, x.src, x.src_id DESC, x.line_id DESC`

// History scope filters, per branch. The family scope resolves members
// through the normalization mappings, as everywhere else.
const (
	historyScopeProduct = " AND %s.product_id = ?"
	historyScopeFamily  = " AND %s.product_id IN (SELECT p.id FROM products p" +
		" JOIN product_name_mappings pnm ON pnm.raw_name = p.name COLLATE NOCASE" +
		" WHERE pnm.generic_name = ? COLLATE NOCASE)"
)

func historyQuery(scope string) (string, string, string) {
	return historyTemplate, fmt.Sprintf(scope, "bi"), fmt.Sprintf(scope, "ti")
}

// Create stores one insight (already phrase-final — the AI wording or the
// auto fallback).
func (r *InsightRepository) Create(ctx context.Context, ins domain.ProductInsight) (domain.ProductInsight, error) {
	now := time.Now().Unix()
	raw, err := json.Marshal(ins.Data)
	if err != nil {
		return domain.ProductInsight{}, fmt.Errorf("encode insight data: %w", err)
	}
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO product_insights
			(kind, product_id, product_name, generic_name, currency, message, source,
			 data_json, acknowledged, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?)`,
		string(ins.Kind), ins.ProductID, ins.ProductName, ins.GenericName, ins.Currency,
		ins.Message, ins.Source, raw, now, now)
	if err != nil {
		return domain.ProductInsight{}, mapWriteError("create insight", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return domain.ProductInsight{}, fmt.Errorf("insight insert id: %w", err)
	}
	ins.ID = id
	ins.Acknowledged = false
	ins.CreatedAt = time.Unix(now, 0).UTC()
	ins.UpdatedAt = ins.CreatedAt
	return ins, nil
}

// GetByID loads one insight, or domain.ErrNotFound.
func (r *InsightRepository) GetByID(ctx context.Context, id int64) (domain.ProductInsight, error) {
	row := r.db.QueryRowContext(ctx, `SELECT`+insightColumns+` FROM product_insights i WHERE i.id = ?`, id)
	ins, err := scanInsight(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ProductInsight{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.ProductInsight{}, fmt.Errorf("get insight: %w", err)
	}
	return ins, nil
}

// List returns insights newest first; Unseen lists only unacknowledged rows.
func (r *InsightRepository) List(ctx context.Context, f domain.InsightFilters) ([]domain.ProductInsight, error) {
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 30
	}
	where := "acknowledged = 0"
	if !f.Unseen {
		where = "1 = 1"
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT`+insightColumns+` FROM product_insights i WHERE `+where+` ORDER BY created_at DESC, id DESC LIMIT ?`,
		f.Limit)
	if err != nil {
		return nil, fmt.Errorf("list insights: %w", err)
	}
	defer rows.Close()

	out := []domain.ProductInsight{}
	for rows.Next() {
		ins, err := scanInsight(rows)
		if err != nil {
			return nil, fmt.Errorf("scan insight row: %w", err)
		}
		out = append(out, ins)
	}
	return out, rows.Err()
}

// Dismiss acknowledges an unread insight (idempotently — a read is not an
// error; an unknown id is).
func (r *InsightRepository) Dismiss(ctx context.Context, id int64) error {
	w, err := r.db.ExecContext(ctx,
		`UPDATE product_insights SET acknowledged = 1, updated_at = ? WHERE id = ?`,
		time.Now().Unix(), id)
	if err != nil {
		return mapWriteError("dismiss insight", err)
	}
	if n, _ := w.RowsAffected(); n == 0 {
		return fmt.Errorf("dismiss insight %d: %w", id, domain.ErrNotFound)
	}
	return nil
}

// RecentlyInsighted reports whether the given kind+product pair was nudged
// since the cutoff (the cooldown that keeps repeat warnings quiet).
func (r *InsightRepository) RecentlyInsighted(ctx context.Context, kind domain.ProductInsightKind, productID int64, since time.Time) (bool, error) {
	var row int64
	err := r.db.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM product_insights WHERE kind = ? AND product_id = ? AND created_at >= ?)`,
		string(kind), productID, since.Unix()).Scan(&row)
	if err != nil {
		return false, fmt.Errorf("check recent insight: %w", err)
	}
	return row != 0, nil
}

// ProductPurchaseHistory lists the newest analyzable purchases of one
// product, newest first.
func (r *InsightRepository) ProductPurchaseHistory(ctx context.Context, productID int64, limit int) ([]domain.ProductHistoryEntry, error) {
	if limit <= 0 {
		limit = 10
	}
	tpl, fBill, fTx := historyQuery(historyScopeProduct)
	return r.history(ctx, tpl, fBill, fTx, []any{productID, productID, limit}, "product purchase history")
}

// FamilyPurchaseHistory lists the newest analyzable purchases of every
// product whose normalization mapping carries the given generic family —
// the input of the bulk-buy size comparison.
func (r *InsightRepository) FamilyPurchaseHistory(ctx context.Context, genericName string, limit int) ([]domain.ProductHistoryEntry, error) {
	if limit <= 0 {
		limit = 20
	}
	tpl, fBill, fTx := historyQuery(historyScopeFamily)
	return r.history(ctx, tpl, fBill, fTx, []any{genericName, genericName, limit}, "family purchase history")
}

func (r *InsightRepository) history(ctx context.Context, tpl, filterBill, filterTx string, args []any, op string) ([]domain.ProductHistoryEntry, error) {
	rows, err := r.db.QueryContext(ctx, fmt.Sprintf(tpl, filterBill, filterTx), args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	defer rows.Close()

	out := []domain.ProductHistoryEntry{}
	for rows.Next() {
		e, err := scanHistoryEntry(rows)
		if err != nil {
			return nil, fmt.Errorf("scan %s row: %w", op, err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func scanHistoryEntry(row interface{ Scan(...any) error }) (domain.ProductHistoryEntry, error) {
	var (
		e         domain.ProductHistoryEntry
		src       string
		lineID    int64
		unitValue sql.NullFloat64
	)
	if err := row.Scan(&e.ProductID, &e.Name, &e.Date, &e.Currency, &e.Unit, &e.UnitPriceCents, &unitValue, &e.StoreName, &src, &e.SourceID, &lineID); err != nil {
		return domain.ProductHistoryEntry{}, err
	}
	if unitValue.Valid {
		e.UnitValue = &unitValue.Float64
	}
	e.Source = src
	return e, nil
}

func scanInsight(row interface{ Scan(...any) error }) (domain.ProductInsight, error) {
	var (
		ins       domain.ProductInsight
		kind      string
		productID sql.NullInt64
		dataJSON  string
		acked     int64
		createdAt int64
		updatedAt int64
	)
	if err := row.Scan(&ins.ID, &kind, &productID, &ins.ProductName, &ins.GenericName,
		&ins.Currency, &ins.Message, &ins.Source, &dataJSON, &acked, &createdAt, &updatedAt); err != nil {
		return domain.ProductInsight{}, err
	}
	ins.Kind = domain.ProductInsightKind(kind)
	if productID.Valid {
		id := productID.Int64
		ins.ProductID = &id
	}
	ins.Acknowledged = acked != 0
	if dataJSON != "" {
		if err := json.Unmarshal([]byte(dataJSON), &ins.Data); err != nil {
			return domain.ProductInsight{}, fmt.Errorf("decode insight data: %w", err)
		}
	}
	ins.CreatedAt = time.Unix(createdAt, 0).UTC()
	ins.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return ins, nil
}

// billInsightColumns reads one accepted bill's analyzable raw lines: linked
// product, price and size (the analysis worker filters/deposits in Go).
const billInsightColumns = `
	bi.product_id, bi.name, COALESCE(bi.unit, ''), bi.unit_value, bi.unit_price_cents,
	b.date, b.currency`

// BillLines lists the saved raw lines of one bill with the purchase context
// the analysis needs. Lines without a linked product carry ProductID 0.
func (r *InsightRepository) BillLines(ctx context.Context, billID int64) ([]domain.InsightLine, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT`+billInsightColumns+`
		FROM bill_items bi JOIN bills b ON b.id = bi.bill_id
		WHERE bi.bill_id = ?`, billID)
	if err != nil {
		return nil, fmt.Errorf("bill insight lines: %w", err)
	}
	defer rows.Close()
	return scanInsightLines(rows)
}

// TransactionLines is BillLines for manual purchases.
func (r *InsightRepository) TransactionLines(ctx context.Context, txID int64) ([]domain.InsightLine, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT ti.product_id, ti.name, COALESCE(ti.unit, ''), ti.unit_value, ti.unit_price_cents,
		       t.date, t.currency
		FROM transaction_items ti JOIN transactions t ON t.id = ti.transaction_id
		WHERE ti.transaction_id = ?`, txID)
	if err != nil {
		return nil, fmt.Errorf("transaction insight lines: %w", err)
	}
	defer rows.Close()
	return scanInsightLines(rows)
}

func scanInsightLines(rows *sql.Rows) ([]domain.InsightLine, error) {
	out := []domain.InsightLine{}
	for rows.Next() {
		var (
			line      domain.InsightLine
			productID sql.NullInt64
			unitValue sql.NullFloat64
		)
		if err := rows.Scan(&productID, &line.Name, &line.Unit, &unitValue, &line.UnitPriceCents, &line.Date, &line.Currency); err != nil {
			return nil, err
		}
		if productID.Valid {
			line.ProductID = productID.Int64
		}
		if unitValue.Valid {
			v := unitValue.Float64
			line.UnitValue = &v
		}
		out = append(out, line)
	}
	return out, rows.Err()
}
