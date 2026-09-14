package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"home-finance-planner/backend/internal/domain"
)

// AnalyticsRepository provides the raw read-only aggregates behind the
// dashboard charts, grouped by native currency. Conversion into the user's
// base currency (and all index/median math) happens in the service layer.
type AnalyticsRepository struct{ db *sql.DB }

func NewAnalyticsRepository(db *sql.DB) *AnalyticsRepository { return &AnalyticsRepository{db: db} }

// itemPriceLimit caps the normalized item-price scan: one row per accepted
// bill line, so a long history can grow large and every unit-price chart is
// better served by a bounded recent window than an unbounded query.
const itemPriceLimit = 20000

// ItemPrices returns one row per accepted, non-return, positively priced bill
// line in the range, with its unit price normalized to a base unit
// (g→kg and ml→l multiply the per-unit price by 1000; pcs stays per piece).
// Lines with a free-text or missing unit are excluded rather than compared
// across incompatible units. Serves the store price index and the personal
// price index.
func (r *AnalyticsRepository) ItemPrices(ctx context.Context, from, to string) ([]domain.ItemPriceRow, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT COALESCE(b.store_id, 0), b.date,
		       lower(trim(bi.name)),
		       CASE bi.unit WHEN 'g' THEN 'kg' WHEN 'ml' THEN 'l' ELSE bi.unit END,
		       b.currency,
		       CASE bi.unit WHEN 'g' THEN bi.unit_price_cents * 1000.0
		                    WHEN 'ml' THEN bi.unit_price_cents * 1000.0
		                    ELSE bi.unit_price_cents * 1.0 END,
		       COALESCE(bi.category_id, 0), COALESCE(c.name, ''), COALESCE(c.section, '')
		FROM bill_items bi
		JOIN bills b ON b.id = bi.bill_id
		LEFT JOIN categories c ON c.id = bi.category_id
		WHERE b.status = 'accepted' AND b.date >= ? AND b.date <= ?
		  AND COALESCE(bi.is_return, 0) = 0
		  AND bi.quantity > 0 AND bi.unit_price_cents > 0
		  AND bi.unit IN ('kg', 'g', 'l', 'ml', 'pcs')
		ORDER BY b.date
		LIMIT ?`, from, to, itemPriceLimit)
	if err != nil {
		return nil, fmt.Errorf("analytics item prices %s..%s: %w", from, to, err)
	}
	defer rows.Close()

	out := []domain.ItemPriceRow{}
	for rows.Next() {
		var p domain.ItemPriceRow
		if err := rows.Scan(&p.StoreID, &p.Date, &p.Key, &p.BaseUnit, &p.Currency, &p.PricePerUnit,
			&p.CategoryID, &p.CategoryName, &p.Section); err != nil {
			return nil, fmt.Errorf("scan item price: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// CategorySpend sums accepted bill line amounts per product category in the
// range, per native currency (sunburst input).
func (r *AnalyticsRepository) CategorySpend(ctx context.Context, from, to string) ([]domain.CategorySpendRow, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT c.id, c.name, COALESCE(c.section, ''), b.currency, SUM(bi.line_total_cents)
		FROM bill_items bi
		JOIN bills b ON b.id = bi.bill_id
		JOIN categories c ON c.id = bi.category_id
		WHERE b.status = 'accepted' AND b.date >= ? AND b.date <= ?
		GROUP BY c.id, c.name, c.section, b.currency`, from, to)
	if err != nil {
		return nil, fmt.Errorf("analytics category spend %s..%s: %w", from, to, err)
	}
	defer rows.Close()

	out := []domain.CategorySpendRow{}
	for rows.Next() {
		var s domain.CategorySpendRow
		if err := rows.Scan(&s.CategoryID, &s.Name, &s.Section, &s.Currency, &s.TotalCents); err != nil {
			return nil, fmt.Errorf("scan category spend: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// DailyExpenseByMonth sums expense transactions per day for the given months
// (YYYY-MM), per native currency (run-rate input).
func (r *AnalyticsRepository) DailyExpenseByMonth(ctx context.Context, months []string) ([]domain.DayExpenseRow, error) {
	rows, err := r.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT substr(date, 1, 7), CAST(substr(date, 9, 2) AS INTEGER), currency, SUM(amount_cents)
		FROM transactions
		WHERE kind = 'expense' AND substr(date, 1, 7) IN (%s)
		GROUP BY 1, 2, 3`, placeholders(len(months))), stringsToAny(months)...)
	if err != nil {
		return nil, fmt.Errorf("analytics daily expense by month: %w", err)
	}
	defer rows.Close()

	out := []domain.DayExpenseRow{}
	for rows.Next() {
		var d domain.DayExpenseRow
		if err := rows.Scan(&d.Month, &d.Day, &d.Currency, &d.AmountCents); err != nil {
			return nil, fmt.Errorf("scan daily expense: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Heatmap sums accepted bill line amounts per weekday and category section in
// the range, per native currency. strftime('%w') is Sunday-first, so the
// Monday-first index is (%w + 6) % 7.
func (r *AnalyticsRepository) Heatmap(ctx context.Context, from, to string) ([]domain.SectionDayRow, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT (CAST(strftime('%w', b.date) AS INTEGER) + 6) % 7 AS dow,
		       COALESCE(c.section, ''), b.currency, SUM(bi.line_total_cents)
		FROM bill_items bi
		JOIN bills b ON b.id = bi.bill_id
		LEFT JOIN categories c ON c.id = bi.category_id
		WHERE b.status = 'accepted' AND b.date >= ? AND b.date <= ?
		GROUP BY dow, c.section, b.currency`, from, to)
	if err != nil {
		return nil, fmt.Errorf("analytics heatmap %s..%s: %w", from, to, err)
	}
	defer rows.Close()

	out := []domain.SectionDayRow{}
	for rows.Next() {
		var h domain.SectionDayRow
		if err := rows.Scan(&h.Dow, &h.Section, &h.Currency, &h.TotalCents); err != nil {
			return nil, fmt.Errorf("scan heatmap row: %w", err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// SpendByMonthAndFixed sums expense transactions per month, split by whether
// the category is a fixed commitment, per native currency (fixed-split
// input). Bucket 2 marks unclassified transactions (no category — which is
// how bill transactions are created) so the service can report them apart.
func (r *AnalyticsRepository) SpendByMonthAndFixed(ctx context.Context, months []string) ([]domain.MonthFixedRow, error) {
	rows, err := r.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT substr(t.date, 1, 7),
		       CASE WHEN t.category_id IS NULL THEN 2 ELSE COALESCE(c.is_fixed, 0) END AS bucket,
		       t.currency, SUM(t.amount_cents)
		FROM transactions t
		LEFT JOIN categories c ON c.id = t.category_id
		WHERE t.kind = 'expense' AND substr(t.date, 1, 7) IN (%s)
		GROUP BY 1, 2, 3`, placeholders(len(months))), stringsToAny(months)...)
	if err != nil {
		return nil, fmt.Errorf("analytics spend by month and fixed: %w", err)
	}
	defer rows.Close()

	out := []domain.MonthFixedRow{}
	for rows.Next() {
		var m domain.MonthFixedRow
		var bucket int64
		if err := rows.Scan(&m.Month, &bucket, &m.Currency, &m.TotalCents); err != nil {
			return nil, fmt.Errorf("scan month fixed row: %w", err)
		}
		m.IsFixed = bucket == 1
		m.Unclassified = bucket == 2
		out = append(out, m)
	}
	return out, rows.Err()
}

// OpenBudgetTotal sums the amounts of all open budget envelopes. Budgets are
// lifetime envelopes (0015), so this powers a target pace line rather than a
// true monthly budget.
func (r *AnalyticsRepository) OpenBudgetTotal(ctx context.Context) (int64, error) {
	var total int64
	err := r.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(amount_cents), 0) FROM budgets WHERE status = 'open'`).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("analytics open budget total: %w", err)
	}
	return total, nil
}

// placeholders builds a comma-separated "?, ?, ?" list for IN clauses.
func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

// stringsToAny widens string slices for database/sql variadic args.
func stringsToAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}
