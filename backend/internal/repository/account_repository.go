package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"home-finance-planner/backend/internal/domain"
)

// accountBalanceExpr is the derived-balance expression (migrations 0040 +
// 0041): opening_balance_cents plus the signed transaction ledger — income +,
// expense −, transfer sent − (amount + fee), transfer received + (the counter
// leg: its own recorded amount/currency when stored, else the row's amount
// 1:1 — the accepted degradation when no rate is known). Foreign-currency
// income/expense rows (e.g. a bill in another currency paid by this card)
// fold their account_amount_cents — the row converted into the account's
// currency at save time — in place of the raw amount; NULL folds 1:1, the
// same accepted degradation. The alias must stay balance_cents — scans and
// cross-store reads (summary balances) key on it.
const accountBalanceExpr = `a.opening_balance_cents
	+ COALESCE((SELECT SUM(CASE t.kind WHEN 'income' THEN 1 ELSE -1 END * COALESCE(t.account_amount_cents, t.amount_cents))
		FROM transactions t WHERE t.account_id = a.id AND t.kind IN ('income', 'expense')), 0)
	- COALESCE((SELECT SUM(t.amount_cents + COALESCE(t.fee_cents, 0))
		FROM transactions t WHERE t.account_id = a.id AND t.kind = 'transfer'), 0)
	+ COALESCE((SELECT SUM(COALESCE(t.counter_amount_cents, t.amount_cents))
		FROM transactions t WHERE t.counter_account_id = a.id AND t.kind = 'transfer'), 0)`

// ledgerRowChange is the per-row amount this row contributes to the owning
// account's balance — the same terms `accountBalanceExpr` sums: income + and
// expense − their COALESCE(account_amount_cents, amount_cents) (the 0041
// converting fold; NULL folds 1:1), a transfer sent −(amount + fee) and a
// transfer received + its recorded counter amount (raw 1:1 when none).
const ledgerRowChange = `CASE
	WHEN t.kind = 'income' THEN COALESCE(t.account_amount_cents, t.amount_cents)
	WHEN t.kind = 'expense' THEN -COALESCE(t.account_amount_cents, t.amount_cents)
	WHEN t.counter_account_id = ? THEN COALESCE(t.counter_amount_cents, t.amount_cents)
	ELSE -(t.amount_cents + COALESCE(t.fee_cents, 0))
END`

// Ledger returns one account's transaction thread with per-row running
// balances — the register view (Phase 2): newest first, the running fold
// computed over ALL of the account's rows in (date, id) order regardless of
// the page window, so every row carries the true balance after it. The change
// column rides the row for display (day totals, signed amounts). A transfer
// belongs to both of its accounts, exactly like the activity filter.
func (r *AccountRepository) Ledger(ctx context.Context, accountID int64, limit, offset int) ([]domain.LedgerEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	q := `SELECT ` + transactionColumns + `,
		` + ledgerRowChange + ` AS change_cents,
		SUM(` + ledgerRowChange + `) OVER (ORDER BY t.date, t.id
			ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW
		) + COALESCE((SELECT a0.opening_balance_cents FROM accounts a0 WHERE a0.id = ?), 0) AS running_balance_cents
		` + transactionFrom + `
		WHERE t.account_id = ? OR (t.kind = 'transfer' AND t.counter_account_id = ?)
		ORDER BY t.date DESC, t.id DESC
		LIMIT ? OFFSET ?`
	// Parameter order follows the SQL text: the change column's transfer-in
	// CASE branch, the window SUM's same branch, the opening-balance subquery,
	// then the two WHERE folds (rows on the account, counter leg), then the
	// page window.
	rows, err := r.db.QueryContext(ctx, q,
		accountID, accountID, accountID, accountID, accountID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("account ledger %d: %w", accountID, err)
	}
	defer rows.Close()

	out := []domain.LedgerEntry{}
	for rows.Next() {
		var (
			t       domain.Transaction
			change  int64
			running int64
		)
		t, err = scanTransaction(rows, &change, &running)
		if err != nil {
			return nil, fmt.Errorf("scan ledger row: %w", err)
		}
		out = append(out, domain.LedgerEntry{Transaction: t, ChangeCents: change, RunningBalanceCents: running})
	}
	return out, rows.Err()
}

// accountColumns is the column list used for SELECTs, in scan order. The
// balance is computed, not stored.
const accountColumns = `a.id, a.name, a.type, a.currency, ` + accountBalanceExpr + ` AS balance_cents,
	a.opening_balance_cents, a.card_last_digits, a.group_id, a.created_at, a.updated_at`

// AccountRepository is the SQLite-backed implementation of the account store.
type AccountRepository struct{ db *sql.DB }

func NewAccountRepository(db *sql.DB) *AccountRepository { return &AccountRepository{db: db} }

func (r *AccountRepository) List(ctx context.Context) ([]domain.Account, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+accountColumns+` FROM accounts a ORDER BY a.name`)
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	defer rows.Close()

	out := []domain.Account{}
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, fmt.Errorf("scan account: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r *AccountRepository) GetByID(ctx context.Context, id int64) (domain.Account, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+accountColumns+` FROM accounts a WHERE a.id = ?`, id)

	a, err := scanAccount(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Account{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Account{}, fmt.Errorf("get account %d: %w", id, err)
	}
	return a, nil
}

func (r *AccountRepository) Create(ctx context.Context, a domain.Account) (domain.Account, error) {
	now := time.Now().Unix()
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO accounts (name, type, currency, opening_balance_cents, card_last_digits, group_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		a.Name, string(a.Type), a.Currency, a.OpeningBalanceCents, a.CardLastDigits, a.GroupID, now, now)
	if err != nil {
		return domain.Account{}, mapWriteError("create account", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return domain.Account{}, fmt.Errorf("account insert id: %w", err)
	}
	return r.GetByID(ctx, id)
}

// Update replaces the user-editable fields. The opening balance is the only
// money field (the balance itself is derived); reconciliation adjusts it —
// never a direct balance write, which would silently fork the ledger.
func (r *AccountRepository) Update(ctx context.Context, a domain.Account) (domain.Account, error) {
	res, err := r.db.ExecContext(ctx, `
		UPDATE accounts
		SET name = ?, type = ?, currency = ?, opening_balance_cents = ?, card_last_digits = ?, group_id = ?, updated_at = ?
		WHERE id = ?`,
		a.Name, string(a.Type), a.Currency, a.OpeningBalanceCents, a.CardLastDigits, a.GroupID, time.Now().Unix(), a.ID)
	if err != nil {
		return domain.Account{}, mapWriteError("update account", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.Account{}, domain.ErrNotFound
	}
	return r.GetByID(ctx, a.ID)
}

func (r *AccountRepository) Delete(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM accounts WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete account %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// scanAccount scans one row from either *sql.Rows or *sql.Row.
func scanAccount(row interface{ Scan(dest ...any) error }) (domain.Account, error) {
	var (
		a              domain.Account
		typ            string
		createdAt, upd int64
	)
	if err := row.Scan(&a.ID, &a.Name, &typ, &a.Currency, &a.BalanceCents, &a.OpeningBalanceCents, &a.CardLastDigits, &a.GroupID, &createdAt, &upd); err != nil {
		return domain.Account{}, err
	}
	a.Type = domain.AccountType(typ)
	a.CreatedAt = time.Unix(createdAt, 0).UTC()
	a.UpdatedAt = time.Unix(upd, 0).UTC()
	return a, nil
}

// mapWriteError translates driver write errors into domain errors.
func mapWriteError(op string, err error) error {
	if isUniqueViolation(err) {
		return fmt.Errorf("%s: %w", op, domain.ErrConflict)
	}
	if err != nil && strings.Contains(err.Error(), "CHECK constraint failed") {
		return fmt.Errorf("%s: %w", op, domain.ErrValidation)
	}
	return fmt.Errorf("%s: %w", op, err)
}
