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

// accountColumns is the column list used for SELECTs, in scan order.
const accountColumns = `id, name, type, currency, balance_cents, card_last_digits, created_at, updated_at`

// AccountRepository is the SQLite-backed implementation of the account store.
type AccountRepository struct{ db *sql.DB }

func NewAccountRepository(db *sql.DB) *AccountRepository { return &AccountRepository{db: db} }

func (r *AccountRepository) List(ctx context.Context) ([]domain.Account, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+accountColumns+` FROM accounts ORDER BY name`)
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
		`SELECT `+accountColumns+` FROM accounts WHERE id = ?`, id)

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
		INSERT INTO accounts (name, type, currency, balance_cents, card_last_digits, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		a.Name, string(a.Type), a.Currency, a.BalanceCents, a.CardLastDigits, now, now)
	if err != nil {
		return domain.Account{}, mapWriteError("create account", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return domain.Account{}, fmt.Errorf("account insert id: %w", err)
	}
	return r.GetByID(ctx, id)
}

func (r *AccountRepository) Update(ctx context.Context, a domain.Account) (domain.Account, error) {
	res, err := r.db.ExecContext(ctx, `
		UPDATE accounts
		SET name = ?, type = ?, currency = ?, balance_cents = ?, card_last_digits = ?, updated_at = ?
		WHERE id = ?`,
		a.Name, string(a.Type), a.Currency, a.BalanceCents, a.CardLastDigits, time.Now().Unix(), a.ID)
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
	if err := row.Scan(&a.ID, &a.Name, &typ, &a.Currency, &a.BalanceCents, &a.CardLastDigits, &createdAt, &upd); err != nil {
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
