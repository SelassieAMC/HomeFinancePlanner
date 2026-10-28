package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"home-finance-planner/backend/internal/domain"
)

// bagColumns is the column list for SELECTs, in scan order.
const bagColumns = `id, name, account_id, target_cents, status, closed_at, created_at, updated_at`

const bagAllocationColumns = `id, bag_id, kind, amount_cents, note, date, related_allocation_id, created_at, updated_at`

// BagRepository is the SQLite-backed implementation of the bag stores: the
// bag rows, the allocation ledger, and the live tagged-spend aggregation.
// Spend is never copied into the ledger — every balance read computes
// Σ allocations − Σ tagged spend, exactly like budgets attribute live.
type BagRepository struct{ db *sql.DB }

func NewBagRepository(db *sql.DB) *BagRepository { return &BagRepository{db: db} }

// ListBags returns bags matching the filters, oldest first. An empty filter
// returns every bag, open ones first within an account.
func (r *BagRepository) ListBags(ctx context.Context, f domain.BagFilters) ([]domain.Bag, error) {
	where := []string{"1 = 1"}
	args := []any{}
	if f.AccountID > 0 {
		where = append(where, "account_id = ?")
		args = append(args, f.AccountID)
	}
	if f.Status != "" {
		where = append(where, "status = ?")
		args = append(args, string(f.Status))
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+bagColumns+` FROM bags WHERE `+joinAND(where)+` ORDER BY account_id, id ASC`, args...)
	if err != nil {
		return nil, fmt.Errorf("list bags: %w", err)
	}
	defer rows.Close()

	out := []domain.Bag{}
	for rows.Next() {
		b, err := scanBag(rows)
		if err != nil {
			return nil, fmt.Errorf("scan bag: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (r *BagRepository) GetBagByID(ctx context.Context, id int64) (domain.Bag, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+bagColumns+` FROM bags WHERE id = ?`, id)
	b, err := scanBag(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Bag{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Bag{}, fmt.Errorf("get bag %d: %w", id, err)
	}
	return b, nil
}

func (r *BagRepository) CreateBag(ctx context.Context, b domain.Bag) (domain.Bag, error) {
	now := time.Now().Unix()
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO bags (name, account_id, target_cents, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		b.Name, b.AccountID, nullableID(b.TargetCents), string(b.Status), now, now)
	if err != nil {
		return domain.Bag{}, mapWriteError("create bag", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return domain.Bag{}, fmt.Errorf("bag insert id: %w", err)
	}
	return r.GetBagByID(ctx, id)
}

// UpdateBag replaces the editable fields of a bag. Status transitions live
// here too (the service validates open⇄closed) — closed_at is stamped on the
// close transition the service performs.
func (r *BagRepository) UpdateBag(ctx context.Context, b domain.Bag) (domain.Bag, error) {
	res, err := r.db.ExecContext(ctx, `
		UPDATE bags
		SET name = ?, account_id = ?, target_cents = ?, status = ?, closed_at = ?, updated_at = ?
		WHERE id = ?`,
		b.Name, b.AccountID, nullableID(b.TargetCents), string(b.Status), nullableTime(b.ClosedAt),
		time.Now().Unix(), b.ID)
	if err != nil {
		return domain.Bag{}, mapWriteError("update bag", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.Bag{}, domain.ErrNotFound
	}
	return r.GetBagByID(ctx, b.ID)
}

// DeleteBag removes a bag: allocations cascade, tagged transactions/bills
// fall back to untagged spend (ON DELETE SET NULL) — plain delete, no
// history kept (decision 2026-10-13).
func (r *BagRepository) DeleteBag(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM bags WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete bag %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// CreateAllocation writes one ledger row.
func (r *BagRepository) CreateAllocation(ctx context.Context, a domain.BagAllocation) (domain.BagAllocation, error) {
	now := time.Now().Unix()
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO bag_allocations (bag_id, kind, amount_cents, note, date, related_allocation_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		a.BagID, a.Kind, a.AmountCents, a.Note, a.Date, nullableID(a.RelatedAllocationID), now, now)
	if err != nil {
		return domain.BagAllocation{}, mapWriteError("create bag allocation", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return domain.BagAllocation{}, fmt.Errorf("bag allocation insert id: %d", id)
	}
	return r.GetAllocationByID(ctx, id)
}

// CreateAllocationPair writes both legs of one rebalance atomically and links
// them through related_allocation_id. Either both rows exist or neither does.
func (r *BagRepository) CreateAllocationPair(ctx context.Context, from, to domain.BagAllocation) (domain.BagAllocation, domain.BagAllocation, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.BagAllocation{}, domain.BagAllocation{}, fmt.Errorf("begin rebalance: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().Unix()
	fromID, err := insertAllocation(ctx, tx, from, now)
	if err != nil {
		return domain.BagAllocation{}, domain.BagAllocation{}, err
	}
	toID, err := insertAllocation(ctx, tx, to, now)
	if err != nil {
		return domain.BagAllocation{}, domain.BagAllocation{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE bag_allocations SET related_allocation_id = ?, updated_at = ? WHERE id = ?`,
		toID, now, fromID); err != nil {
		return domain.BagAllocation{}, domain.BagAllocation{}, fmt.Errorf("link rebalance pair: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE bag_allocations SET related_allocation_id = ?, updated_at = ? WHERE id = ?`,
		fromID, now, toID); err != nil {
		return domain.BagAllocation{}, domain.BagAllocation{}, fmt.Errorf("link rebalance pair: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return domain.BagAllocation{}, domain.BagAllocation{}, fmt.Errorf("commit rebalance: %w", err)
	}
	src, err := r.GetAllocationByID(ctx, fromID)
	if err != nil {
		return domain.BagAllocation{}, domain.BagAllocation{}, err
	}
	dst, err := r.GetAllocationByID(ctx, toID)
	if err != nil {
		return domain.BagAllocation{}, domain.BagAllocation{}, err
	}
	return src, dst, nil
}

func insertAllocation(ctx context.Context, tx *sql.Tx, a domain.BagAllocation, now int64) (int64, error) {
	res, err := tx.ExecContext(ctx, `
		INSERT INTO bag_allocations (bag_id, kind, amount_cents, note, date, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		a.BagID, a.Kind, a.AmountCents, a.Note, a.Date, now, now)
	if err != nil {
		return 0, mapWriteError("create bag allocation", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("bag allocation insert id: %w", err)
	}
	return id, nil
}

func (r *BagRepository) GetAllocationByID(ctx context.Context, id int64) (domain.BagAllocation, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+bagAllocationColumns+` FROM bag_allocations WHERE id = ?`, id)
	a, err := scanBagAllocation(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.BagAllocation{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.BagAllocation{}, fmt.Errorf("get bag allocation %d: %w", id, err)
	}
	return a, nil
}

// ListAllocations returns one bag's ledger, newest entries first.
func (r *BagRepository) ListAllocations(ctx context.Context, bagID int64) ([]domain.BagAllocation, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+bagAllocationColumns+` FROM bag_allocations WHERE bag_id = ? ORDER BY date DESC, id DESC`, bagID)
	if err != nil {
		return nil, fmt.Errorf("list bag allocations: %w", err)
	}
	defer rows.Close()

	out := []domain.BagAllocation{}
	for rows.Next() {
		a, err := scanBagAllocation(rows)
		if err != nil {
			return nil, fmt.Errorf("scan bag allocation: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// AllocationTotals sums the signed allocation ledger per bag (all bags,
// open and closed), bounded by the given date ("YYYY-MM-DD"; empty = no
// bound). Amounts are all in the bag's account currency — allocations carry
// no currency of their own.
func (r *BagRepository) AllocationTotals(ctx context.Context, until string) (map[int64]int64, error) {
	q := `SELECT bag_id, SUM(amount_cents) FROM bag_allocations `
	args := []any{}
	if until != "" {
		q += `WHERE date <= ? `
		args = append(args, until)
	}
	q += `GROUP BY bag_id`
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("sum bag allocations: %w", err)
	}
	defer rows.Close()

	out := map[int64]int64{}
	for rows.Next() {
		var bagID, cents int64
		if err := rows.Scan(&bagID, &cents); err != nil {
			return nil, fmt.Errorf("scan bag allocation total: %w", err)
		}
		out[bagID] = cents
	}
	return out, rows.Err()
}

// TaggedSpend sums the live spend attributed to each bag, bounded by the
// given date ("YYYY-MM-DD"; empty = all-time), grouped by bag AND native
// currency — converted into the bag's currency by the service. Two sources,
// never double counted: manual expense transactions (the bill's own expense
// transaction is created without a bag and stays bagless) and accepted bill
// lines through the per-line override COALESCE.
//
// Both legs fold the migration-0041 conversion (`COALESCE(
// account_amount_cents, amount_cents)`) the same way `accountBalanceExpr`
// debits the account, so a foreign-currency card spend drains the bag at the
// rate recorded on the row — never at today's reference snapshot. For bill
// lines the per-line amounts live in the bill's currency, so the recorded
// conversion is applied as the bill's debit-to-total ratio (one rounding per
// bag × bill group); the group's currency is the paying account's currency.
// A NULL account amount (same-currency bill, or the accepted no-rate
// degradation) folds ratio 1:1 into the account's currency — exactly what the
// account ledger did; a bill whose link or account vanished (LEFT JOIN miss)
// keeps the raw bill currency with the old snapshot conversion. Manual expense
// rows are always recorded in their account's
// currency, so the transaction leg folds 1:1 today; the COALESCE keeps the
// two ledgers aligned if that ever changes.
func (r *BagRepository) TaggedSpend(ctx context.Context, until string) ([]domain.BagCurrencySpend, error) {
	txBound, billBound := "", ""
	args := []any{}
	if until != "" {
		txBound = " AND t.date <= ?"
		billBound = " AND b.date <= ?"
		args = append(args, until, until)
	}
	q := `
		SELECT bag_id, currency, SUM(cents) FROM (
			SELECT t.bag_id AS bag_id, t.currency AS currency,
				SUM(COALESCE(t.account_amount_cents, t.amount_cents)) AS cents
			FROM transactions t
			WHERE t.kind = 'expense' AND t.bag_id IS NOT NULL AND t.bill_id IS NULL` + txBound + `
			GROUP BY t.bag_id, t.currency
			UNION ALL
			SELECT bag_id, currency, SUM(cents) FROM (
				SELECT COALESCE(bi.bag_id, b.bag_id) AS bag_id,
					COALESCE(acc.currency, b.currency) AS currency,
					CAST(ROUND(SUM(bi.line_total_cents) * 1.0 * CASE
							WHEN tx.account_amount_cents IS NOT NULL AND tx.amount_cents <> 0
							THEN CAST(tx.account_amount_cents AS REAL) / tx.amount_cents
							ELSE 1.0 END) AS INTEGER) AS cents
				FROM bill_items bi
				JOIN bills b ON b.id = bi.bill_id
				LEFT JOIN transactions tx ON tx.id = b.transaction_id
				LEFT JOIN accounts acc ON acc.id = tx.account_id
				WHERE b.status = 'accepted' AND COALESCE(bi.bag_id, b.bag_id) IS NOT NULL` + billBound + `
				GROUP BY 1, 2, b.id
			)
			GROUP BY bag_id, currency
		)
		GROUP BY bag_id, currency ORDER BY bag_id`
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("sum bag tagged spend: %w", err)
	}
	defer rows.Close()

	out := []domain.BagCurrencySpend{}
	for rows.Next() {
		var s domain.BagCurrencySpend
		if err := rows.Scan(&s.BagID, &s.Currency, &s.Cents); err != nil {
			return nil, fmt.Errorf("scan bag tagged spend: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// scanBag scans one bag row from either *sql.Rows or *sql.Row.
func scanBag(row interface{ Scan(dest ...any) error }) (domain.Bag, error) {
	var (
		b              domain.Bag
		target         sql.NullInt64
		status         string
		closedAt       sql.NullInt64
		createdAt, upd int64
	)
	if err := row.Scan(&b.ID, &b.Name, &b.AccountID, &target, &status, &closedAt, &createdAt, &upd); err != nil {
		return domain.Bag{}, err
	}
	if target.Valid {
		v := target.Int64
		b.TargetCents = &v
	}
	b.Status = domain.BagLifecycle(status)
	if closedAt.Valid {
		t := time.Unix(closedAt.Int64, 0).UTC()
		b.ClosedAt = &t
	}
	b.CreatedAt = time.Unix(createdAt, 0).UTC()
	b.UpdatedAt = time.Unix(upd, 0).UTC()
	return b, nil
}

func scanBagAllocation(row interface{ Scan(dest ...any) error }) (domain.BagAllocation, error) {
	var (
		a            domain.BagAllocation
		related      sql.NullInt64
		createdAt, u int64
	)
	if err := row.Scan(&a.ID, &a.BagID, &a.Kind, &a.AmountCents, &a.Note, &a.Date, &related, &createdAt, &u); err != nil {
		return domain.BagAllocation{}, err
	}
	if related.Valid {
		v := related.Int64
		a.RelatedAllocationID = &v
	}
	a.CreatedAt = time.Unix(createdAt, 0).UTC()
	a.UpdatedAt = time.Unix(u, 0).UTC()
	return a, nil
}

// nullableTime converts a *time.Time into a driver value (Unix seconds).
func nullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Unix()
}
