package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"home-finance-planner/backend/internal/domain"
)

// BillFilters re-exports the shared domain filter type for callers of this
// package.
type BillFilters = domain.BillFilters

// billColumns + billFrom read a bill together with its budget's display name
// (joined through the budget's category; NULL when no budget is linked).
const billColumns = `
	b.id, b.market_name, b.date, b.payment_method, b.card_last_digits, b.currency,
	b.items_subtotal_cents, b.discount_cents, b.vat_cents, b.total_cents, b.printed_total_cents,
	b.status, b.image_path, b.extracted_by, b.created_at, b.updated_at, b.budget_id, b.transaction_id, bg.name`

const billFrom = `
	FROM bills b
	LEFT JOIN budgets g ON g.id = b.budget_id
	LEFT JOIN categories bg ON bg.id = g.category_id`

// BillRepository is the SQLite-backed implementation of the bill store.
type BillRepository struct{ db *sql.DB }

func NewBillRepository(db *sql.DB) *BillRepository { return &BillRepository{db: db} }

// Create inserts a bill and its items in one transaction.
func (r *BillRepository) Create(ctx context.Context, b domain.Bill) (domain.Bill, error) {
	now := time.Now().Unix()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Bill{}, fmt.Errorf("begin bill create: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		INSERT INTO bills
			(market_name, date, payment_method, card_last_digits, currency,
			 items_subtotal_cents, discount_cents, vat_cents, total_cents, printed_total_cents,
			 status, image_path, extracted_by, created_at, updated_at, budget_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		b.MarketName, b.Date, b.PaymentMethod, b.CardLastDigits, b.Currency,
		b.ItemsSubtotalCents, b.DiscountCents, b.VATCents, b.TotalCents, b.PrintedTotalCents,
		string(b.Status), b.ImagePath, b.ExtractedBy, now, now, b.BudgetID)
	if err != nil {
		tx.Rollback()
		return domain.Bill{}, mapWriteError("create bill", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		tx.Rollback()
		return domain.Bill{}, fmt.Errorf("bill insert id: %w", err)
	}

	for _, item := range b.Items {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO bill_items
				(bill_id, name, brand, unit, category_id, quantity, unit_price_cents, discount_cents, line_total_cents, is_return, budget_id)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, item.Name, item.Brand, item.Unit, item.CategoryID, item.Quantity, item.UnitPriceCents,
			item.DiscountCents, item.LineTotalCents, item.IsReturn, item.BudgetID); err != nil {
			tx.Rollback()
			return domain.Bill{}, mapWriteError("create bill item", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return domain.Bill{}, fmt.Errorf("commit bill create: %w", err)
	}
	return r.GetByID(ctx, id)
}

// Update replaces a bill's editable fields and item lines in one transaction.
func (r *BillRepository) Update(ctx context.Context, b domain.Bill) (domain.Bill, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Bill{}, fmt.Errorf("begin bill update: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		UPDATE bills SET
			market_name = ?, date = ?, payment_method = ?, card_last_digits = ?, currency = ?,
			items_subtotal_cents = ?, discount_cents = ?, vat_cents = ?, total_cents = ?,
			printed_total_cents = ?, status = ?, budget_id = ?, updated_at = ?
		WHERE id = ?`,
		b.MarketName, b.Date, b.PaymentMethod, b.CardLastDigits, b.Currency,
		b.ItemsSubtotalCents, b.DiscountCents, b.VATCents, b.TotalCents, b.PrintedTotalCents,
		string(b.Status), b.BudgetID, time.Now().Unix(), b.ID)
	if err != nil {
		tx.Rollback()
		return domain.Bill{}, mapWriteError("update bill", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		tx.Rollback()
		return domain.Bill{}, domain.ErrNotFound
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM bill_items WHERE bill_id = ?`, b.ID); err != nil {
		tx.Rollback()
		return domain.Bill{}, fmt.Errorf("clear bill items: %w", err)
	}
	for _, item := range b.Items {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO bill_items
				(bill_id, name, brand, unit, category_id, quantity, unit_price_cents, discount_cents, line_total_cents, is_return, budget_id)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			b.ID, item.Name, item.Brand, item.Unit, item.CategoryID, item.Quantity, item.UnitPriceCents,
			item.DiscountCents, item.LineTotalCents, item.IsReturn, item.BudgetID); err != nil {
			tx.Rollback()
			return domain.Bill{}, mapWriteError("update bill item", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return domain.Bill{}, fmt.Errorf("commit bill update: %w", err)
	}
	return r.GetByID(ctx, b.ID)
}

// SetTransaction links a bill to the expense transaction recorded for it.
func (r *BillRepository) SetTransaction(ctx context.Context, billID, txID int64) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE bills SET transaction_id = ?, updated_at = ? WHERE id = ?`,
		txID, time.Now().Unix(), billID)
	if err != nil {
		return fmt.Errorf("link bill %d to transaction: %w", billID, err)
	}
	return nil
}

// GetByID returns the bill with its items, or domain.ErrNotFound.
func (r *BillRepository) GetByID(ctx context.Context, id int64) (domain.Bill, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+billColumns+billFrom+` WHERE b.id = ?`, id)

	b, err := scanBill(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Bill{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Bill{}, fmt.Errorf("get bill %d: %w", id, err)
	}

	items, err := r.itemsForBill(ctx, id)
	if err != nil {
		return domain.Bill{}, err
	}
	b.Items = items
	return b, nil
}

// List returns bills matching the filters (no items; use GetByID for detail).
func (r *BillRepository) List(ctx context.Context, f BillFilters) ([]domain.Bill, error) {
	where := []string{"1 = 1"}
	args := []any{}

	if f.Status != "" {
		where = append(where, "b.status = ?")
		args = append(args, string(f.Status))
	}
	if f.Month != "" {
		where = append(where, "substr(b.date, 1, 7) = ?")
		args = append(args, f.Month)
	}
	if f.Market != "" {
		where = append(where, "b.market_name LIKE '%' || ? || '%'")
		args = append(args, f.Market)
	}

	rows, err := r.db.QueryContext(ctx,
		`SELECT `+billColumns+billFrom+` WHERE `+joinAND(where)+`
		 ORDER BY b.date DESC, b.id DESC LIMIT 200`, args...)
	if err != nil {
		return nil, fmt.Errorf("list bills: %w", err)
	}
	defer rows.Close()

	out := []domain.Bill{}
	for rows.Next() {
		b, err := scanBill(rows)
		if err != nil {
			return nil, fmt.Errorf("scan bill: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// Stats aggregates accepted bills. groupBy is one of
// "market" | "month" | "week" | "item" | "category"; month optionally narrows
// the range.
func (r *BillRepository) Stats(ctx context.Context, groupBy, month string) ([]domain.BillStatsRow, error) {
	where := []string{"b.status = 'accepted'"}
	args := []any{}
	if month != "" {
		where = append(where, "substr(b.date, 1, 7) = ?")
		args = append(args, month)
	}
	whereSQL := joinAND(where)

	var q string
	switch groupBy {
	case "market":
		q = `SELECT b.market_name AS label, COUNT(*), 0, SUM(b.total_cents)
			FROM bills b WHERE ` + whereSQL + ` AND b.market_name != ''
			GROUP BY lower(b.market_name) ORDER BY SUM(b.total_cents) DESC`
	case "month":
		q = `SELECT substr(b.date, 1, 7) AS label, COUNT(*), 0, SUM(b.total_cents)
			FROM bills b WHERE ` + whereSQL + ` AND b.date != ''
			GROUP BY label ORDER BY label DESC`
	case "week":
		q = `SELECT strftime('%Y-W%W', b.date) AS label, COUNT(*), 0, SUM(b.total_cents)
			FROM bills b WHERE ` + whereSQL + ` AND b.date != ''
			GROUP BY label ORDER BY label DESC`
	case "item":
		q = `SELECT MAX(bi.name) AS label, COUNT(DISTINCT b.id), SUM(bi.quantity), SUM(bi.line_total_cents)
			FROM bill_items bi JOIN bills b ON b.id = bi.bill_id
			WHERE ` + whereSQL + `
			GROUP BY lower(bi.name) ORDER BY SUM(bi.line_total_cents) DESC LIMIT 50`
	case "category":
		q = `SELECT MAX(c.name) AS label, COUNT(DISTINCT b.id), SUM(bi.quantity), SUM(bi.line_total_cents)
			FROM bill_items bi
			JOIN bills b ON b.id = bi.bill_id
			JOIN categories c ON c.id = bi.category_id
			WHERE ` + whereSQL + `
			GROUP BY lower(c.name) ORDER BY SUM(bi.line_total_cents) DESC LIMIT 50`
	default:
		return nil, domain.ErrValidation
	}

	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("bill stats %s: %w", groupBy, err)
	}
	defer rows.Close()

	out := []domain.BillStatsRow{}
	for rows.Next() {
		var row domain.BillStatsRow
		if err := rows.Scan(&row.Label, &row.BillCount, &row.Quantity, &row.TotalCents); err != nil {
			return nil, fmt.Errorf("scan bill stats: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (r *BillRepository) itemsForBill(ctx context.Context, billID int64) ([]domain.BillItem, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT bi.id, bi.bill_id, bi.name, bi.brand, bi.unit, bi.category_id, COALESCE(c.name, ''),
		       bi.quantity, bi.unit_price_cents, bi.discount_cents, bi.line_total_cents, bi.is_return, bi.budget_id
		FROM bill_items bi
		LEFT JOIN categories c ON c.id = bi.category_id
		WHERE bi.bill_id = ? ORDER BY bi.id`, billID)
	if err != nil {
		return nil, fmt.Errorf("list bill items: %w", err)
	}
	defer rows.Close()

	out := []domain.BillItem{}
	for rows.Next() {
		var it domain.BillItem
		if err := rows.Scan(&it.ID, &it.BillID, &it.Name, &it.Brand, &it.Unit, &it.CategoryID, &it.CategoryName,
			&it.Quantity, &it.UnitPriceCents, &it.DiscountCents, &it.LineTotalCents, &it.IsReturn, &it.BudgetID); err != nil {
			return nil, fmt.Errorf("scan bill item: %w", err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// ListBrands returns the distinct brands already recorded on bill items, so
// the review UI can offer a brand dropdown. Custom brands become available
// here automatically once a bill is confirmed.
func (r *BillRepository) ListBrands(ctx context.Context) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT DISTINCT TRIM(brand) FROM bill_items
		WHERE TRIM(brand) != ''
		ORDER BY TRIM(brand) COLLATE NOCASE`)
	if err != nil {
		return nil, fmt.Errorf("list bill brands: %w", err)
	}
	defer rows.Close()

	out := []string{}
	for rows.Next() {
		var brand string
		if err := rows.Scan(&brand); err != nil {
			return nil, fmt.Errorf("scan bill brand: %w", err)
		}
		out = append(out, brand)
	}
	return out, rows.Err()
}

func joinAND(parts []string) string {
	for i, p := range parts {
		parts[i] = "(" + p + ")"
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += " AND " + p
	}
	return out
}

func scanBill(row interface{ Scan(dest ...any) error }) (domain.Bill, error) {
	var (
		b              domain.Bill
		status         string
		createdAt, upd int64
	)
	var budgetName sql.NullString
	if err := row.Scan(&b.ID, &b.MarketName, &b.Date, &b.PaymentMethod, &b.CardLastDigits, &b.Currency,
		&b.ItemsSubtotalCents, &b.DiscountCents, &b.VATCents, &b.TotalCents, &b.PrintedTotalCents,
		&status, &b.ImagePath, &b.ExtractedBy, &createdAt, &upd, &b.BudgetID, &b.TransactionID, &budgetName); err != nil {
		return domain.Bill{}, err
	}
	b.BudgetName = budgetName.String
	b.Status = domain.BillStatus(status)
	b.CreatedAt = time.Unix(createdAt, 0).UTC()
	b.UpdatedAt = time.Unix(upd, 0).UTC()
	return b, nil
}
