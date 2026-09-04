package repository

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

	"home-finance-planner/backend/internal/domain"
)

// SummaryRepository computes read-only dashboard aggregates.
type SummaryRepository struct{ db *sql.DB }

func NewSummaryRepository(db *sql.DB) *SummaryRepository { return &SummaryRepository{db: db} }

// MonthSummaryFor aggregates income, expenses, balances, budget progress and
// top spending categories for a month ("YYYY-MM").
func (r *SummaryRepository) MonthSummaryFor(ctx context.Context, month string) (domain.MonthSummary, error) {
	s := domain.MonthSummary{Month: month}

	if err := r.db.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN kind = 'income'  THEN amount_cents ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN kind = 'expense' THEN amount_cents ELSE 0 END), 0)
		FROM transactions WHERE substr(date, 1, 7) = ?`, month).
		Scan(&s.IncomeCents, &s.ExpenseCents); err != nil {
		return s, fmt.Errorf("summary totals %s: %w", month, err)
	}
	s.NetCents = s.IncomeCents - s.ExpenseCents

	if err := r.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(balance_cents), 0) FROM accounts`).
		Scan(&s.TotalBalanceCents); err != nil {
		return s, fmt.Errorf("summary balance: %w", err)
	}

	spentByCategory, err := r.spentByCategory(ctx, month)
	if err != nil {
		return s, err
	}

	billSpend, err := r.billSpendByBudget(ctx, month)
	if err != nil {
		return s, err
	}

	budgets, err := r.budgetStatuses(ctx, month, spentByCategory, billSpend)
	if err != nil {
		return s, err
	}
	s.Budgets = budgets

	top, err := r.topCategories(ctx, month, spentByCategory)
	if err != nil {
		return s, err
	}
	s.TopCategories = top

	daily, err := r.dailyExpenses(ctx, month)
	if err != nil {
		return s, err
	}
	s.DailyExpenses = daily

	return s, nil
}

// dailyExpenses lists each day of the month that had expenses, in date order.
func (r *SummaryRepository) dailyExpenses(ctx context.Context, month string) ([]domain.DayTotal, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT date, SUM(amount_cents)
		FROM transactions
		WHERE kind = 'expense' AND substr(date, 1, 7) = ?
		GROUP BY date ORDER BY date`, month)
	if err != nil {
		return nil, fmt.Errorf("summary daily expenses %s: %w", month, err)
	}
	defer rows.Close()

	out := []domain.DayTotal{}
	for rows.Next() {
		var d domain.DayTotal
		if err := rows.Scan(&d.Date, &d.ExpenseCents); err != nil {
			return nil, fmt.Errorf("scan daily expense: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (r *SummaryRepository) spentByCategory(ctx context.Context, month string) (map[int64]int64, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT category_id, SUM(amount_cents)
		FROM transactions
		WHERE kind = 'expense' AND category_id IS NOT NULL AND substr(date, 1, 7) = ?
		GROUP BY category_id`, month)
	if err != nil {
		return nil, fmt.Errorf("summary spend by category %s: %w", month, err)
	}
	defer rows.Close()

	out := map[int64]int64{}
	for rows.Next() {
		var cat, spent int64
		if err := rows.Scan(&cat, &spent); err != nil {
			return nil, fmt.Errorf("scan category spend: %w", err)
		}
		out[cat] = spent
	}
	return out, rows.Err()
}

func (r *SummaryRepository) budgetStatuses(ctx context.Context, month string, spent map[int64]int64, billSpend map[int64]int64) ([]domain.BudgetStatus, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, category_id, month, amount_cents, created_at, updated_at
		FROM budgets WHERE month = ? ORDER BY category_id`, month)
	if err != nil {
		return nil, fmt.Errorf("summary budgets %s: %w", month, err)
	}
	defer rows.Close()

	out := []domain.BudgetStatus{}
	for rows.Next() {
		b, err := scanBudget(rows)
		if err != nil {
			return nil, fmt.Errorf("scan budget status: %w", err)
		}
		st := domain.BudgetStatus{Budget: b}
		// Category spending from transactions plus the bill line amounts
		// correlated with this budget (per-line overrides included).
		st.SpentCents = spent[b.CategoryID] + billSpend[b.ID]
		st.RemainingCents = b.AmountCents - st.SpentCents
		out = append(out, st)
	}
	return out, rows.Err()
}

// billSpendByBudget sums accepted bill line amounts attributed to budgets in
// the month. Per-line assignments fall back to the bill's budget (COALESCE);
// bill spending is NOT double counted here because the bill's transaction is
// created without a category.
func (r *SummaryRepository) billSpendByBudget(ctx context.Context, month string) (map[int64]int64, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT COALESCE(bi.budget_id, b.budget_id), SUM(bi.line_total_cents)
		FROM bill_items bi
		JOIN bills b ON b.id = bi.bill_id
		WHERE b.status = 'accepted' AND substr(b.date, 1, 7) = ?
		  AND COALESCE(bi.budget_id, b.budget_id) IS NOT NULL
		GROUP BY COALESCE(bi.budget_id, b.budget_id)`, month)
	if err != nil {
		return nil, fmt.Errorf("summary bill spend by budget %s: %w", month, err)
	}
	defer rows.Close()

	out := map[int64]int64{}
	for rows.Next() {
		var budgetID, total int64
		if err := rows.Scan(&budgetID, &total); err != nil {
			return nil, fmt.Errorf("scan bill budget spend: %w", err)
		}
		out[budgetID] += total
	}
	return out, rows.Err()
}

func (r *SummaryRepository) topCategories(ctx context.Context, month string, spent map[int64]int64) ([]domain.CategoryTotal, error) {
	if len(spent) == 0 {
		return []domain.CategoryTotal{}, nil
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT id, name FROM categories`)
	if err != nil {
		return nil, fmt.Errorf("summary category names: %w", err)
	}
	defer rows.Close()

	names := map[int64]string{}
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, fmt.Errorf("scan category name: %w", err)
		}
		names[id] = name
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]domain.CategoryTotal, 0, len(spent))
	for cat, total := range spent {
		out = append(out, domain.CategoryTotal{
			CategoryID:   cat,
			CategoryName: names[cat],
			TotalCents:   total,
		})
	}
	// sort by total descending
	sort.Slice(out, func(i, j int) bool { return out[i].TotalCents > out[j].TotalCents })
	const topN = 5
	if len(out) > topN {
		out = out[:topN]
	}
	return out, nil
}
