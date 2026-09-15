package repository

import (
	"context"
	"database/sql"
	"testing"

	"home-finance-planner/backend/internal/domain"
)

// seedTransactionBase writes one account and returns its id.
func seedTransactionBase(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	res, err := db.Exec(`INSERT INTO accounts (name, type, currency, created_at, updated_at) VALUES ('Wallet', 'cash', 'EUR', 100, 100)`)
	if err != nil {
		t.Fatalf("seed account: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed account id: %v", err)
	}
	return id
}

func TestTransactionRepositoryCreateWithItems(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	repo := NewTransactionRepository(db)
	accountID := seedTransactionBase(t, db)

	storeID := int64(0)
	if res, err := db.Exec(`INSERT INTO stores (name, created_at, updated_at) VALUES ('REWE', 100, 100)`); err != nil {
		t.Fatalf("seed store: %v", err)
	} else if id, err := res.LastInsertId(); err != nil {
		t.Fatalf("seed store id: %v", err)
	} else {
		storeID = id
	}

	pid := int64(0)
	if res, err := db.Exec(`INSERT INTO products (name, created_at, updated_at) VALUES ('Milk', 100, 100)`); err != nil {
		t.Fatalf("seed product: %v", err)
	} else if id, err := res.LastInsertId(); err != nil {
		t.Fatalf("seed product id: %v", err)
	} else {
		pid = id
	}

	items := []domain.TransactionItem{
		{Name: "Milk", ProductID: &pid, Quantity: 2, UnitPriceCents: 139, LineTotalCents: 278},
		{Name: "Bread", Quantity: 1, UnitPriceCents: 250, LineTotalCents: 250},
	}
	created, err := repo.CreateWithItems(ctx, domain.Transaction{
		AccountID:   accountID,
		Kind:        domain.TransactionExpense,
		AmountCents: 528,
		Currency:    "EUR",
		Description: "Groceries",
		Date:        "2026-05-10",
		StoreID:     &storeID,
	}, items)
	if err != nil {
		t.Fatalf("create with items: %v", err)
	}

	// GetByID loads the lines and the derived totals.
	got, err := repo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.Items) != 2 || got.ItemsTotalCents != 528 {
		t.Fatalf("items = %d, total = %d; want 2, 528", len(got.Items), got.ItemsTotalCents)
	}
	if got.StoreID == nil || *got.StoreID != storeID || got.StoreName != "REWE" {
		t.Fatalf("store = %v %q; want %d REWE", got.StoreID, got.StoreName, storeID)
	}
	if got.Items[0].ProductName != "Milk" {
		t.Fatalf("product name = %q; want joined product name", got.Items[0].ProductName)
	}

	// List leaves items empty but counts them.
	list, err := repo.List(ctx, TransactionFilters{Month: "2026-05"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].ItemCount != 2 || list[0].Items != nil {
		t.Fatalf("list = %+v; want one row with item_count 2 and no items", list)
	}

	// UpdateWithItems replaces the lines.
	items = []domain.TransactionItem{{Name: "Milk", ProductID: &pid, Quantity: 1, UnitPriceCents: 139, LineTotalCents: 139}}
	updated, err := repo.UpdateWithItems(ctx, domain.Transaction{
		ID:          created.ID,
		AccountID:   accountID,
		Kind:        domain.TransactionExpense,
		AmountCents: 139,
		Currency:    "EUR",
		Description: "Groceries",
		Date:        "2026-05-10",
		StoreID:     &storeID,
	}, items)
	if err != nil {
		t.Fatalf("update with items: %v", err)
	}
	if len(updated.Items) != 1 || updated.ItemsTotalCents != 139 {
		t.Fatalf("updated items = %d, total %d; want 1, 139", len(updated.Items), updated.ItemsTotalCents)
	}

	// Delete cascades the lines.
	if err := repo.Delete(ctx, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM transaction_items WHERE transaction_id = ?`, created.ID).Scan(&count); err != nil {
		t.Fatalf("count leftovers: %v", err)
	}
	if count != 0 {
		t.Fatalf("%d item lines survived the transaction delete", count)
	}
}

func TestTransactionRepositoryBillIDRoundTrip(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	repo := NewTransactionRepository(db)
	accountID := seedTransactionBase(t, db)

	res, err := db.Exec(`INSERT INTO bills (market_name, date, currency, status, total_cents, created_at, updated_at)
		VALUES ('REWE', '2026-05-10', 'EUR', 'accepted', 500, 100, 100)`)
	if err != nil {
		t.Fatalf("seed bill: %v", err)
	}
	billID, _ := res.LastInsertId()

	tx, err := repo.Create(ctx, domain.Transaction{
		AccountID:   accountID,
		Kind:        domain.TransactionExpense,
		AmountCents: 500,
		Currency:    "EUR",
		Description: "Bill — REWE",
		Date:        "2026-05-10",
		BillID:      &billID,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if tx.BillID == nil || *tx.BillID != billID {
		t.Fatalf("bill id = %v; want %d", tx.BillID, billID)
	}

	// The bill sync path round-trips bill_id through a plain Update.
	updated, err := repo.Update(ctx, tx)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.BillID == nil || *updated.BillID != billID {
		t.Fatalf("bill id after update = %v; want preserved", updated.BillID)
	}
}
