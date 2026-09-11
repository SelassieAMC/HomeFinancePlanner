package repository

import (
	"context"
	"database/sql"
	"fmt"

	"home-finance-planner/backend/internal/domain"
)

// SummaryRepository computes read-only dashboard aggregates, grouped by
// native currency. Conversion into the user's base currency happens in the
// service layer.
type SummaryRepository struct{ db *sql.DB }

func NewSummaryRepository(db *sql.DB) *SummaryRepository { return &SummaryRepository{db: db} }

// RawMonthSummary aggregates income, expenses, balances, budget progress and
// spending categories for a month ("YYYY-MM"), each row labeled with the
// native currency it was recorded in.
func (r *SummaryRepository) RawMonthSummary(ctx context.Context, month string) (domain.RawMonthSummary, error) {
	raw := domain.RawMonthSummary{Month: month}

	kinds, err := r.amountsByKind(ctx, month)
	if err != nil {
		return raw, err
	}
	raw.Income = kinds["income"]
	raw.Expense = kinds["expense"]

	if raw.Balances, err = r.balances(ctx); err != nil {
		return raw, err
	}
	if raw.DailyExpenses, err = r.dailyExpenses(ctx, month); err != nil {
		return raw, err
	}
	if raw.CategorySpend, err = r.spendByCategory(ctx, month); err != nil {
		return raw, err
	}
	if raw.BillBudgetSpend, err = r.billSpendByBudget(ctx, month); err != nil {
		return raw, err
	}
	if raw.Budgets, err = r.monthBudgets(ctx, month); err != nil {
		return raw, err
	}
	return raw, nil
}

// amountsByKind sums transaction amounts per kind and native currency.
func (r *SummaryRepository) amountsByKind(ctx context.Context, month string) (map[string][]domain.CurrencyAmount, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT kind, currency, SUM(amount_cents)
		FROM transactions
		WHERE substr(date, 1, 7) = ?
		GROUP BY kind, currency`, month)
	if err != nil {
		return nil, fmt.Errorf("summary totals %s: %w", month, err)
	}
	defer rows.Close()

	out := map[string][]domain.CurrencyAmount{}
	for rows.Next() {
		var kind, currency string
		var cents int64
		if err := rows.Scan(&kind, &currency, &cents); err != nil {
			return nil, fmt.Errorf("scan totals: %w", err)
		}
		out[kind] = append(out[kind], domain.CurrencyAmount{Currency: currency, Cents: cents})
	}
	return out, rows.Err()
}

// balances sums account balances per native currency.
func (r *SummaryRepository) balances(ctx context.Context) ([]domain.CurrencyAmount, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT currency, SUM(balance_cents)
		FROM accounts
		GROUP BY currency`)
	if err != nil {
		return nil, fmt.Errorf("summary balances: %w", err)
	}
	defer rows.Close()

	out := []domain.CurrencyAmount{}
	for rows.Next() {
		var a domain.CurrencyAmount
		if err := rows.Scan(&a.Currency, &a.Cents); err != nil {
			return nil, fmt.Errorf("scan balance: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// dailyExpenses lists each day of the month that had expenses, grouped per
// native currency, in date order.
func (r *SummaryRepository) dailyExpenses(ctx context.Context, month string) ([]domain.DayCurrencyTotal, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT date, currency, SUM(amount_cents)
		FROM transactions
		WHERE kind = 'expense' AND substr(date, 1, 7) = ?
		GROUP BY date, currency ORDER BY date`, month)
	if err != nil {
		return nil, fmt.Errorf("summary daily expenses %s: %w", month, err)
	}
	defer rows.Close()

	out := []domain.DayCurrencyTotal{}
	for rows.Next() {
		var d domain.DayCurrencyTotal
		if err := rows.Scan(&d.Date, &d.Currency, &d.ExpenseCents); err != nil {
			return nil, fmt.Errorf("scan daily expense: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// spendByCategory sums expense transactions per category and currency.
func (r *SummaryRepository) spendByCategory(ctx context.Context, month string) ([]domain.CategoryCurrencyTotal, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT category_id, currency, SUM(amount_cents)
		FROM transactions
		WHERE kind = 'expense' AND category_id IS NOT NULL AND substr(date, 1, 7) = ?
		GROUP BY category_id, currency`, month)
	if err != nil {
		return nil, fmt.Errorf("summary spend by category %s: %w", month, err)
	}
	defer rows.Close()

	out := []domain.CategoryCurrencyTotal{}
	for rows.Next() {
		var c domain.CategoryCurrencyTotal
		if err := rows.Scan(&c.CategoryID, &c.Currency, &c.TotalCents); err != nil {
			return nil, fmt.Errorf("scan category spend: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// monthBudgets reads the month's budget rows (amounts are in the base
// currency; the service compares them against converted spend).
func (r *SummaryRepository) monthBudgets(ctx context.Context, month string) ([]domain.Budget, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, category_id, month, amount_cents, created_at, updated_at
		FROM budgets WHERE month = ? ORDER BY category_id`, month)
	if err != nil {
		return nil, fmt.Errorf("summary budgets %s: %w", month, err)
	}
	defer rows.Close()

	out := []domain.Budget{}
	for rows.Next() {
		b, err := scanBudget(rows)
		if err != nil {
			return nil, fmt.Errorf("scan budget: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// billSpendByBudget sums accepted bill line amounts attributed to budgets in
// the month, per native currency. Per-line assignments fall back to the
// bill's budget (COALESCE); bill spending is NOT double counted here because
// the bill's transaction is created without a category.
func (r *SummaryRepository) billSpendByBudget(ctx context.Context, month string) ([]domain.BudgetCurrencySpend, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT COALESCE(bi.budget_id, b.budget_id), b.currency, SUM(bi.line_total_cents)
		FROM bill_items bi
		JOIN bills b ON b.id = bi.bill_id
		WHERE b.status = 'accepted' AND substr(b.date, 1, 7) = ?
		  AND COALESCE(bi.budget_id, b.budget_id) IS NOT NULL
		GROUP BY COALESCE(bi.budget_id, b.budget_id), b.currency`, month)
	if err != nil {
		return nil, fmt.Errorf("summary bill spend by budget %s: %w", month, err)
	}
	defer rows.Close()

	out := []domain.BudgetCurrencySpend{}
	for rows.Next() {
		var s domain.BudgetCurrencySpend
		if err := rows.Scan(&s.BudgetID, &s.Currency, &s.Cents); err != nil {
			return nil, fmt.Errorf("scan bill budget spend: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
