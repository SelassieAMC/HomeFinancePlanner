package service

import (
	"context"
	"errors"
	"testing"

	"home-finance-planner/backend/internal/domain"
)

// newTestTransactionService wires a TransactionService with fakes; the item
// store records the writes Create/Update made.
func newTestTransactionService(t *testing.T) (*TransactionService, *fakeTxStore, *fakeProductStore, *fakeStoreStore) {
	t.Helper()
	accounts := newFakeAccountStore()
	accounts.Create(context.Background(), domain.Account{ID: 1, Name: "Wallet", Type: domain.AccountCash, Currency: "EUR"})
	svc := &TransactionService{
		transactions: newFakeTxStore(),
		accounts:     accounts,
		categories: &fakeCategoryStore{cats: map[int64]domain.Category{
			7: {ID: 7, Name: "Deposit & Returns", AllowsNegative: true},
			8: {ID: 8, Name: "Food"},
		}},
		stores:   newFakeStoreStore(),
		products: newFakeProductStore(),
	}
	txs, _ := svc.transactions.(*fakeTxStore)
	products, _ := svc.products.(*fakeProductStore)
	stores, _ := svc.stores.(*fakeStoreStore)
	return svc, txs, products, stores
}

func validTransactionInput() TransactionInput {
	return TransactionInput{
		AccountID:   1,
		Kind:        domain.TransactionExpense,
		AmountCents: 1000,
		Description: "Groceries",
		Date:        "2026-05-10",
	}
}

func TestTransactionCreateWithItems(t *testing.T) {
	ctx := context.Background()
	svc, txs, products, stores := newTestTransactionService(t)

	in := validTransactionInput()
	in.StoreName = "rewe" // find-or-created, case-insensitively
	in.Items = []TransactionItemInput{
		{Name: "Milk", Quantity: 2, UnitPriceCents: 139}, // new product
		{Name: "  Bread  ", Brand: "Bakery", Unit: " PCS ", CategoryID: nil, Quantity: 1, UnitPriceCents: 250, DiscountCents: 10}, // trimmed
	}
	created, err := svc.Create(ctx, in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Unknown product names auto-created; both lines link to their product.
	if len(products.items) != 2 {
		t.Fatalf("products = %d; want milk and bread created", len(products.items))
	}
	milk, err := products.FindByName(ctx, "Milk")
	if err != nil {
		t.Fatalf("find created product: %v", err)
	}
	if created.Items[0].ProductID == nil || *created.Items[0].ProductID != milk.ID {
		t.Fatalf("milk product link = %v; want %d", created.Items[0].ProductID, milk.ID)
	}
	if created.Items[1].Name != "Bread" || created.Items[1].Unit != "pcs" {
		t.Fatalf("bread line = %+v; want trimmed name and lower-cased unit", created.Items[1])
	}
	line := created.Items[1].LineTotalCents
	if line != 240 { // 1 × 250 − 10 discount
		t.Fatalf("bread line total = %d; want 240", line)
	}

	// Store find-or-created from the typed name (store_name itself is the
	// repository's display-only join).
	if created.StoreID == nil || *created.StoreID == 0 {
		t.Fatalf("store = %v; want the rewe row linked", created.StoreID)
	}
	if len(stores.items) != 1 {
		t.Fatalf("stores = %d; want 1", len(stores.items))
	}

	// The amount stays independent of the items total (278 + 240).
	if created.AmountCents != 1000 || created.ItemsTotalCents != 518 {
		t.Fatalf("amount = %d, items total = %d; want 1000, 518", created.AmountCents, created.ItemsTotalCents)
	}

	// The lines persisted through the store.
	got, err := txs.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(got.Items) != 2 || got.ItemsTotalCents != 518 {
		t.Fatalf("reloaded items = %d, total %d; want 2, 518", len(got.Items), got.ItemsTotalCents)
	}
}

func TestTransactionCreateValidation(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _ := newTestTransactionService(t)

	// Items on income are rejected.
	in := validTransactionInput()
	in.Kind = domain.TransactionIncome
	in.Items = []TransactionItemInput{{Name: "Milk", Quantity: 1, UnitPriceCents: 100}}
	if _, err := svc.Create(ctx, in); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("items on income = %v; want ErrValidation", err)
	}

	// An empty item name, a non-positive quantity and a negative price without
	// a negative-allowed category are rejected.
	for name, mutate := range map[string]func(*TransactionItemInput){
		"empty name":    func(i *TransactionItemInput) { i.Name = "   " },
		"zero quantity": func(i *TransactionItemInput) { i.Quantity = 0 },
		"negative price": func(i *TransactionItemInput) {
			i.Quantity = 1
			i.UnitPriceCents = -5
		},
	} {
		in := validTransactionInput()
		item := TransactionItemInput{Name: "Milk", Quantity: 1, UnitPriceCents: 100}
		mutate(&item)
		in.Items = []TransactionItemInput{item}
		if _, err := svc.Create(ctx, in); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("%s = %v; want ErrValidation", name, err)
		}
	}
}

func TestTransactionCreateNegativeReturnLines(t *testing.T) {
	ctx := context.Background()
	svc, txs, products, _ := newTestTransactionService(t)

	// Leergut lines and lines under an allows_negative category ("Deposit &
	// Returns") are money back: negative unit price and line total, reducing
	// the items total; they never link to the catalogue.
	in := validTransactionInput()
	in.AmountCents = 600
	cat7 := int64(7)
	cat8 := int64(8)
	in.Items = []TransactionItemInput{
		{Name: "Milk", Quantity: 2, UnitPriceCents: 139},
		{Name: "Leergut", Quantity: 8, UnitPriceCents: -25},
		{Name: "Bottle deposit refund", CategoryID: &cat7, Quantity: 1, UnitPriceCents: -150},
	}
	created, err := svc.Create(ctx, in)
	if err != nil {
		t.Fatalf("create with return lines: %v", err)
	}

	want := int64(278 - 200 - 150)
	if created.ItemsTotalCents != want {
		t.Fatalf("items total = %d; want %d", created.ItemsTotalCents, want)
	}
	if created.Items[1].LineTotalCents != -200 || created.Items[2].LineTotalCents != -150 {
		t.Fatalf("return line totals = %d/%d; want -200/-150", created.Items[1].LineTotalCents, created.Items[2].LineTotalCents)
	}
	// Only the purchase line links to a product.
	if len(products.items) != 1 {
		t.Fatalf("products = %d; want only Milk created", len(products.items))
	}
	if created.Items[1].ProductID != nil || created.Items[2].ProductID != nil {
		t.Fatalf("return lines must stay unlinked (pfand=%v refund=%v)",
			created.Items[1].ProductID, created.Items[2].ProductID)
	}
	saved := txs.items[created.ID]
	if len(saved.Items) != 3 || saved.Items[1].LineTotalCents != -200 {
		t.Fatalf("negative line total not persisted: %+v", saved.Items)
	}

	// A line under a category that forbids negatives is still clamped to 0.
	in.Items = []TransactionItemInput{
		{Name: "Milk", Quantity: 1, UnitPriceCents: 100},
		{Name: "Odd refund", CategoryID: &cat8, Quantity: 1, UnitPriceCents: 50, DiscountCents: 80},
	}
	clamped, err := svc.Create(ctx, in)
	if err != nil {
		t.Fatalf("create clamped line: %v", err)
	}
	if clamped.Items[1].LineTotalCents != 0 {
		t.Fatalf("clamped line total = %d; want 0", clamped.Items[1].LineTotalCents)
	}
}

func TestTransactionBillGuard(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _ := newTestTransactionService(t)

	// Seed a bill-linked transaction directly through the store, the way
	// BillService.recordBillTransaction does.
	billID := int64(9)
	linked, err := svc.transactions.Create(ctx, domain.Transaction{
		AccountID:   1,
		Kind:        domain.TransactionExpense,
		AmountCents: 500,
		Currency:    "EUR",
		Description: "Bill — REWE",
		Date:        "2026-05-10",
		BillID:      &billID,
	})
	if err != nil {
		t.Fatalf("seed bill transaction: %v", err)
	}

	in := validTransactionInput()
	if _, err := svc.Update(ctx, linked.ID, in); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("update bill transaction = %v; want ErrConflict", err)
	}
	if err := svc.Delete(ctx, linked.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("delete bill transaction = %v; want ErrConflict", err)
	}

	// A manual transaction keeps its edit and delete.
	manual, err := svc.Create(ctx, in)
	if err != nil {
		t.Fatalf("create manual: %v", err)
	}
	if _, err := svc.Update(ctx, manual.ID, in); err != nil {
		t.Fatalf("update manual: %v", err)
	}
	if err := svc.Delete(ctx, manual.ID); err != nil {
		t.Fatalf("delete manual: %v", err)
	}
}

func TestTransactionProductRaceResolvesThroughReRead(t *testing.T) {
	ctx := context.Background()
	svc, _, products, _ := newTestTransactionService(t)

	// The next Create loses the find-or-create race (the concurrent winner
	// stored its row); the re-read must still return a link.
	products.failConflict = true

	in := validTransactionInput()
	in.Items = []TransactionItemInput{{Name: "Milk", Quantity: 1, UnitPriceCents: 100}}
	created, err := svc.Create(ctx, in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Items[0].ProductID == nil {
		t.Fatalf("milk product link = nil; want the raced product resolved")
	}
}
