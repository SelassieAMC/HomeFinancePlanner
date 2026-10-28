package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"home-finance-planner/backend/internal/domain"
)

// BudgetRepository is the SQLite-backed implementation of the budget store.
type BudgetRepository struct{ db *sql.DB }

func NewBudgetRepository(db *sql.DB) *BudgetRepository { return &BudgetRepository{db: db} }

// budgetColumns is the shared projection for every budget read; scanBudget
// consumes it in this exact order.
const budgetColumns = `id, category_id, amount_cents, status, cadence, month, carry_over, carried_cents, closed_at, created_at, updated_at`

func (r *BudgetRepository) List(ctx context.Context) ([]domain.Budget, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+budgetColumns+`
		FROM budgets ORDER BY status DESC, category_id`)
	if err != nil {
		return nil, fmt.Errorf("list budgets: %w", err)
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

func (r *BudgetRepository) GetByID(ctx context.Context, id int64) (domain.Budget, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT `+budgetColumns+`
		FROM budgets WHERE id = ?`, id)

	b, err := scanBudget(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Budget{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Budget{}, fmt.Errorf("get budget %d: %w", id, err)
	}
	return b, nil
}

func (r *BudgetRepository) Create(ctx context.Context, b domain.Budget) (domain.Budget, error) {
	now := time.Now().Unix()
	var month any
	if b.Month != "" {
		month = b.Month
	}
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO budgets (category_id, amount_cents, status, cadence, month, carry_over, carried_cents, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		b.CategoryID, b.AmountCents, domain.BudgetOpen, b.Cadence, month, boolInt(b.CarryOver), b.CarriedCents, now, now)
	if err != nil {
		return domain.Budget{}, mapWriteError("create budget", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return domain.Budget{}, fmt.Errorf("budget insert id: %w", err)
	}
	return r.GetByID(ctx, id)
}

// Update persists amount, lifecycle state and the monthly series' carry_over
// flag. closed_at is owned by the service: it is set when a budget closes and
// cleared when it reopens.
func (r *BudgetRepository) Update(ctx context.Context, b domain.Budget) (domain.Budget, error) {
	var closedAt any
	if b.ClosedAt != nil {
		closedAt = b.ClosedAt.Unix()
	}
	res, err := r.db.ExecContext(ctx, `
		UPDATE budgets
		SET amount_cents = ?, status = ?, carry_over = ?, closed_at = ?, updated_at = ?
		WHERE id = ?`,
		b.AmountCents, b.Status, boolInt(b.CarryOver), closedAt, time.Now().Unix(), b.ID)
	if err != nil {
		return domain.Budget{}, mapWriteError("update budget", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.Budget{}, domain.ErrNotFound
	}
	return r.GetByID(ctx, b.ID)
}

func (r *BudgetRepository) Delete(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM budgets WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete budget %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ListStaleMonthly returns the open monthly rows whose month is older than
// the given month ("YYYY-MM") — the rows a rollover would close and succeed.
// The read-only preview half of the lazy rollover: the BudgetService computes
// each row's carried_cents from this list, then calls RolloverMonthly with
// the carry map.
func (r *BudgetRepository) ListStaleMonthly(ctx context.Context, month string) ([]domain.Budget, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+budgetColumns+`
		FROM budgets
		WHERE cadence = 'monthly' AND status = 'open' AND month < ?
		ORDER BY month`, month)
	if err != nil {
		return nil, fmt.Errorf("list stale monthly budgets: %w", err)
	}
	defer rows.Close()

	out := []domain.Budget{}
	for rows.Next() {
		b, err := scanBudget(rows)
		if err != nil {
			return nil, fmt.Errorf("scan stale monthly budget: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// CarryCategoryMonthSpend buckets expense transactions per category AND
// calendar month (date prefix, "YYYY-MM"), per native currency — the same
// bucketing SummaryRepository.spendByCategoryMonth feeds the dashboard's
// monthly-budget evaluation. The rollover's carry chain reads it so a row's
// leftover (cap − month spend) matches EXACTLY what the UI shows as that
// row's spent number; keep the two queries in sync.
func (r *BudgetRepository) CarryCategoryMonthSpend(ctx context.Context) ([]domain.CategoryMonthCurrencyTotal, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT category_id, substr(date, 1, 7), currency, SUM(amount_cents)
		FROM transactions
		WHERE kind = 'expense' AND category_id IS NOT NULL
		GROUP BY category_id, substr(date, 1, 7), currency`)
	if err != nil {
		return nil, fmt.Errorf("carry category month spend: %w", err)
	}
	defer rows.Close()

	out := []domain.CategoryMonthCurrencyTotal{}
	for rows.Next() {
		var c domain.CategoryMonthCurrencyTotal
		if err := rows.Scan(&c.CategoryID, &c.Month, &c.Currency, &c.TotalCents); err != nil {
			return nil, fmt.Errorf("scan carry category month spend: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CarryBillSpend sums accepted bill line amounts attributed to budgets, per
// native currency, with NO date filter — the budget row is the attribution
// (per-line override COALESCE to the bill's budget), the same all-time basis
// SummaryRepository.billSpendByBudgetAllTime feeds the lifetime field, so
// the carry chain's spend half matches the row the UI shows.
func (r *BudgetRepository) CarryBillSpend(ctx context.Context) ([]domain.BudgetCurrencySpend, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT COALESCE(bi.budget_id, b.budget_id), b.currency, SUM(bi.line_total_cents)
		FROM bill_items bi
		JOIN bills b ON b.id = bi.bill_id
		WHERE b.status = 'accepted'
		  AND COALESCE(bi.budget_id, b.budget_id) IS NOT NULL
		GROUP BY COALESCE(bi.budget_id, b.budget_id), b.currency`)
	if err != nil {
		return nil, fmt.Errorf("carry bill spend by budget: %w", err)
	}
	defer rows.Close()

	out := []domain.BudgetCurrencySpend{}
	for rows.Next() {
		var s domain.BudgetCurrencySpend
		if err := rows.Scan(&s.BudgetID, &s.Currency, &s.Cents); err != nil {
			return nil, fmt.Errorf("scan carry bill spend: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// RolloverMonthly closes open monthly budgets whose month is older than the
// given month ("YYYY-MM") and creates each one's successor for the given
// month with the same amount — the lazy month rollover, one transaction so a
// budget is never both closed and successor-less. The NOT EXISTS guard keeps
// it idempotent against a concurrent manual create.
//
// The carry is the service's number: carries maps a stale row's id to the
// carried_cents its successor inherits (predecessor.carried + amount − month
// spend, base-currency converted by the BudgetService — the spending basis
// mirrors the dashboard evaluation fold). Rows absent from the map (rollover
// off, or conversion sources unavailable) roll over with carried_cents 0 —
// byte-equal legacy behavior. carry_over and the cap are copied as-is; the
// cap is never rebased.
func (r *BudgetRepository) RolloverMonthly(ctx context.Context, month string, carries map[int64]int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("rollover budgets begin: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `
		SELECT id, category_id, amount_cents, carry_over
		FROM budgets
		WHERE cadence = 'monthly' AND status = 'open' AND month < ?`, month)
	if err != nil {
		return fmt.Errorf("rollover budgets stale list: %w", err)
	}
	type stale struct {
		id, categoryID, amountCents int64
		carryOver                   int64
	}
	var stales []stale
	for rows.Next() {
		var s stale
		if err := rows.Scan(&s.id, &s.categoryID, &s.amountCents, &s.carryOver); err != nil {
			rows.Close()
			return fmt.Errorf("rollover budgets scan stale: %w", err)
		}
		stales = append(stales, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rollover budgets stale rows: %w", err)
	}

	now := time.Now().Unix()
	for _, s := range stales {
		if _, err := tx.ExecContext(ctx, `
			UPDATE budgets SET status = 'closed', closed_at = ?, updated_at = ?
			WHERE id = ? AND status = 'open'`, now, now, s.id); err != nil {
			return fmt.Errorf("rollover budgets close %d: %w", s.id, err)
		}
		var carried int64
		if s.carryOver != 0 {
			carried = carries[s.id]
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO budgets (category_id, amount_cents, status, cadence, month, carry_over, carried_cents, created_at, updated_at)
			SELECT ?, ?, 'open', 'monthly', ?, ?, ?, ?, ?
			WHERE NOT EXISTS (SELECT 1 FROM budgets WHERE category_id = ? AND status = 'open')`,
			s.categoryID, s.amountCents, month, s.carryOver, carried, now, now, s.categoryID); err != nil {
			return fmt.Errorf("rollover budgets successor for category %d: %w", s.categoryID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("rollover budgets commit: %w", err)
	}
	return nil
}

// ApplyTransfer moves allowance between two OPEN monthly rows of the same
// month in ONE transaction: the source cap shrinks, the target cap grows, and
// the auditable reallocation row is written. The SQL re-guards what the
// service validated (both rows open, source cap stays positive) — a concurrent
// close or edit fails the move with ErrNotFound/ErrValidation instead of
// half-applying it.
func (r *BudgetRepository) ApplyTransfer(ctx context.Context, fromID, toID int64, amountCents int64, note string, month string) (domain.Budget, domain.Budget, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Budget{}, domain.Budget{}, fmt.Errorf("budget transfer begin: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().Unix()
	res, err := tx.ExecContext(ctx, `
		UPDATE budgets SET amount_cents = amount_cents - ?, updated_at = ?
		WHERE id = ? AND status = 'open' AND cadence = 'monthly' AND amount_cents > ?`,
		amountCents, now, fromID, amountCents)
	if err != nil {
		return domain.Budget{}, domain.Budget{}, fmt.Errorf("budget transfer source: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.Budget{}, domain.Budget{}, fmt.Errorf(
			"source budget #%d is not an open monthly row or its cap is not above the amount: %w", fromID, domain.ErrValidation)
	}
	res, err = tx.ExecContext(ctx, `
		UPDATE budgets SET amount_cents = amount_cents + ?, updated_at = ?
		WHERE id = ? AND status = 'open' AND cadence = 'monthly'`,
		amountCents, now, toID)
	if err != nil {
		return domain.Budget{}, domain.Budget{}, fmt.Errorf("budget transfer target: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.Budget{}, domain.Budget{}, fmt.Errorf(
			"target budget #%d is not an open monthly row: %w", toID, domain.ErrValidation)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO budget_transfers (from_budget_id, to_budget_id, month, amount_cents, note, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		fromID, toID, month, amountCents, note, now); err != nil {
		return domain.Budget{}, domain.Budget{}, fmt.Errorf("budget transfer record: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return domain.Budget{}, domain.Budget{}, fmt.Errorf("budget transfer commit: %w", err)
	}
	from, err := r.GetByID(ctx, fromID)
	if err != nil {
		return domain.Budget{}, domain.Budget{}, err
	}
	to, err := r.GetByID(ctx, toID)
	if err != nil {
		return domain.Budget{}, domain.Budget{}, err
	}
	return from, to, nil
}

// ApplySeed applies a carried-balance seed ("a transfer from nothing"): the
// signed amount rides onto the target row's carried_cents and the audit row
// records from_budget_id NULL. Same open-monthly guard as a transfer.
func (r *BudgetRepository) ApplySeed(ctx context.Context, toID int64, amountCents int64, note string, month string) (domain.Budget, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Budget{}, fmt.Errorf("budget seed begin: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().Unix()
	res, err := tx.ExecContext(ctx, `
		UPDATE budgets SET carried_cents = carried_cents + ?, updated_at = ?
		WHERE id = ? AND status = 'open' AND cadence = 'monthly'`,
		amountCents, now, toID)
	if err != nil {
		return domain.Budget{}, fmt.Errorf("budget seed target: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.Budget{}, fmt.Errorf(
			"target budget #%d is not an open monthly row: %w", toID, domain.ErrValidation)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO budget_transfers (from_budget_id, to_budget_id, month, amount_cents, note, created_at)
		VALUES (NULL, ?, ?, ?, ?, ?)`,
		toID, month, amountCents, note, now); err != nil {
		return domain.Budget{}, fmt.Errorf("budget seed record: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return domain.Budget{}, fmt.Errorf("budget seed commit: %w", err)
	}
	return r.GetByID(ctx, toID)
}

// ListTransfers returns the transfer history joined with the categories both
// rows live under, newest first — the audit line under the budgets table.
func (r *BudgetRepository) ListTransfers(ctx context.Context) ([]domain.BudgetTransfer, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT bt.id, bt.from_budget_id, bt.to_budget_id, bt.month, bt.amount_cents, bt.note, bt.created_at,
			COALESCE(fb.category_id, 0), COALESCE(fc.name, ''),
			tb.category_id, tc.name
		FROM budget_transfers bt
		JOIN budgets tb ON tb.id = bt.to_budget_id
		JOIN categories tc ON tc.id = tb.category_id
		LEFT JOIN budgets fb ON fb.id = bt.from_budget_id
		LEFT JOIN categories fc ON fc.id = fb.category_id
		ORDER BY bt.created_at DESC, bt.id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list budget transfers: %w", err)
	}
	defer rows.Close()

	out := []domain.BudgetTransfer{}
	for rows.Next() {
		var (
			t            domain.BudgetTransfer
			fromBudgetID sql.NullInt64
			createdAt    int64
		)
		if err := rows.Scan(&t.ID, &fromBudgetID, &t.ToBudgetID, &t.Month, &t.AmountCents, &t.Note, &createdAt,
			&t.FromCategoryID, &t.FromCategoryName, &t.ToCategoryID, &t.ToCategoryName); err != nil {
			return nil, fmt.Errorf("scan budget transfer: %w", err)
		}
		if fromBudgetID.Valid {
			v := fromBudgetID.Int64
			t.FromBudgetID = &v
		}
		t.CreatedAt = time.Unix(createdAt, 0).UTC()
		out = append(out, t)
	}
	return out, rows.Err()
}

func scanBudget(row interface{ Scan(dest ...any) error }) (domain.Budget, error) {
	var (
		b              domain.Budget
		status         string
		month          sql.NullString
		carryOver      int64
		closedAt       sql.NullInt64
		createdAt, upd int64
	)
	if err := row.Scan(&b.ID, &b.CategoryID, &b.AmountCents, &status, &b.Cadence, &month, &carryOver, &b.CarriedCents, &closedAt, &createdAt, &upd); err != nil {
		return domain.Budget{}, err
	}
	b.Status = domain.BudgetLifecycle(status)
	if month.Valid {
		b.Month = month.String
	}
	b.CarryOver = carryOver != 0
	if closedAt.Valid {
		t := time.Unix(closedAt.Int64, 0).UTC()
		b.ClosedAt = &t
	}
	b.CreatedAt = time.Unix(createdAt, 0).UTC()
	b.UpdatedAt = time.Unix(upd, 0).UTC()
	return b, nil
}
