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

// RawRangeSummary aggregates income, expenses, balances, budget progress and
// spending categories for an inclusive date range ("YYYY-MM-DD"), each row
// labeled with the native currency it was recorded in.
func (r *SummaryRepository) RawRangeSummary(ctx context.Context, from, to string) (domain.RawSummary, error) {
	raw := domain.RawSummary{From: from, To: to}

	kinds, err := r.amountsByKind(ctx, from, to)
	if err != nil {
		return raw, err
	}
	raw.Income = kinds["income"]
	raw.Expense = kinds["expense"]

	if raw.Balances, err = r.balances(ctx); err != nil {
		return raw, err
	}
	if raw.DailyExpenses, err = r.dailyExpenses(ctx, from, to); err != nil {
		return raw, err
	}
	if raw.CategorySpend, err = r.spendByCategory(ctx, from, to); err != nil {
		return raw, err
	}
	if raw.BillBudgetSpend, err = r.billSpendByBudget(ctx, from, to); err != nil {
		return raw, err
	}
	if raw.LifetimeCategorySpend, err = r.spendByCategoryAllTime(ctx); err != nil {
		return raw, err
	}
	if raw.LifetimeBillBudgetSpend, err = r.billSpendByBudgetAllTime(ctx); err != nil {
		return raw, err
	}
	if raw.Budgets, err = r.budgets(ctx); err != nil {
		return raw, err
	}
	return raw, nil
}

// amountsByKind sums transaction amounts per kind and native currency.
func (r *SummaryRepository) amountsByKind(ctx context.Context, from, to string) (map[string][]domain.CurrencyAmount, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT kind, currency, SUM(amount_cents)
		FROM transactions
		WHERE date >= ? AND date <= ?
		GROUP BY kind, currency`, from, to)
	if err != nil {
		return nil, fmt.Errorf("summary totals %s..%s: %w", from, to, err)
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

// dailyExpenses lists each day in the range that had expenses, grouped per
// native currency, in date order.
func (r *SummaryRepository) dailyExpenses(ctx context.Context, from, to string) ([]domain.DayCurrencyTotal, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT date, currency, SUM(amount_cents)
		FROM transactions
		WHERE kind = 'expense' AND date >= ? AND date <= ?
		GROUP BY date, currency ORDER BY date`, from, to)
	if err != nil {
		return nil, fmt.Errorf("summary daily expenses %s..%s: %w", from, to, err)
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
func (r *SummaryRepository) spendByCategory(ctx context.Context, from, to string) ([]domain.CategoryCurrencyTotal, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT category_id, currency, SUM(amount_cents)
		FROM transactions
		WHERE kind = 'expense' AND category_id IS NOT NULL AND date >= ? AND date <= ?
		GROUP BY category_id, currency`, from, to)
	if err != nil {
		return nil, fmt.Errorf("summary spend by category %s..%s: %w", from, to, err)
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

// spendByCategoryAllTime sums expense transactions per category and currency
// with no date filter — the lifetime view for envelope progress.
func (r *SummaryRepository) spendByCategoryAllTime(ctx context.Context) ([]domain.CategoryCurrencyTotal, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT category_id, currency, SUM(amount_cents)
		FROM transactions
		WHERE kind = 'expense' AND category_id IS NOT NULL
		GROUP BY category_id, currency`)
	if err != nil {
		return nil, fmt.Errorf("summary lifetime spend by category: %w", err)
	}
	defer rows.Close()

	out := []domain.CategoryCurrencyTotal{}
	for rows.Next() {
		var c domain.CategoryCurrencyTotal
		if err := rows.Scan(&c.CategoryID, &c.Currency, &c.TotalCents); err != nil {
			return nil, fmt.Errorf("scan lifetime category spend: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// budgets reads every budget envelope (amounts are in the base currency;
// the service compares them against converted spend and decides which ones
// are relevant for the requested range — open ones always, closed ones when
// they had attributed spend inside the range).
func (r *SummaryRepository) budgets(ctx context.Context) ([]domain.Budget, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, category_id, amount_cents, status, closed_at, created_at, updated_at
		FROM budgets ORDER BY category_id`)
	if err != nil {
		return nil, fmt.Errorf("summary budgets: %w", err)
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
// the range, per native currency. Per-line assignments fall back to the
// bill's budget (COALESCE); bill spending is NOT double counted here because
// the bill's transaction is created without a category.
func (r *SummaryRepository) billSpendByBudget(ctx context.Context, from, to string) ([]domain.BudgetCurrencySpend, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT COALESCE(bi.budget_id, b.budget_id), b.currency, SUM(bi.line_total_cents)
		FROM bill_items bi
		JOIN bills b ON b.id = bi.bill_id
		WHERE b.status = 'accepted' AND b.date >= ? AND b.date <= ?
		  AND COALESCE(bi.budget_id, b.budget_id) IS NOT NULL
		GROUP BY COALESCE(bi.budget_id, b.budget_id), b.currency`, from, to)
	if err != nil {
		return nil, fmt.Errorf("summary bill spend by budget %s..%s: %w", from, to, err)
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

// billSpendByBudgetAllTime sums accepted bill line amounts attributed to
// budgets with no date filter — the lifetime envelope view. Same attribution
// rule as billSpendByBudget.
func (r *SummaryRepository) billSpendByBudgetAllTime(ctx context.Context) ([]domain.BudgetCurrencySpend, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT COALESCE(bi.budget_id, b.budget_id), b.currency, SUM(bi.line_total_cents)
		FROM bill_items bi
		JOIN bills b ON b.id = bi.bill_id
		WHERE b.status = 'accepted'
		  AND COALESCE(bi.budget_id, b.budget_id) IS NOT NULL
		GROUP BY COALESCE(bi.budget_id, b.budget_id), b.currency`)
	if err != nil {
		return nil, fmt.Errorf("summary lifetime bill spend by budget: %w", err)
	}
	defer rows.Close()

	out := []domain.BudgetCurrencySpend{}
	for rows.Next() {
		var s domain.BudgetCurrencySpend
		if err := rows.Scan(&s.BudgetID, &s.Currency, &s.Cents); err != nil {
			return nil, fmt.Errorf("scan lifetime bill budget spend: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
