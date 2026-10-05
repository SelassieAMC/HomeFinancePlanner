package service

import (
	"context"
	"errors"
	"strings"
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

// TestTransactionCreateAppliesMappingMemory covers the normalization memory
// on manual purchases: a typed name that is already mapped fills its category
// only when the input left it open (an explicit input category wins), an
// unmapped name records an identity mapping with source 'manual', and return
// lines never touch the memory at all.
func TestTransactionCreateAppliesMappingMemory(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _ := newTestTransactionService(t)
	mappings := newFakeProductMappingStore()
	mappings.seedMapping(domain.ProductNameMapping{
		RawName: "Milk", StandardName: "Milk 1L", CategoryID: ptrInt64(8), Source: domain.MappingSourceUser,
	})
	mappings.seedMapping(domain.ProductNameMapping{
		RawName: "Oats", StandardName: "Oats", CategoryID: ptrInt64(8), Source: domain.MappingSourceManual,
	})
	svc.mappings = mappings

	in := validTransactionInput()
	in.Items = []TransactionItemInput{
		{Name: "Milk", Quantity: 1, UnitPriceCents: 139},                         // category filled from the memory (8)
		{Name: "Oats", CategoryID: ptrInt64(7), Quantity: 1, UnitPriceCents: 99}, // input category wins (7, not the memory's 8)
		{Name: "Bread", Quantity: 1, UnitPriceCents: 250},                        // unmapped → identity mapping recorded
		{Name: "Leergut", Quantity: 8, UnitPriceCents: -25},                      // return line: skipped
	}
	created, err := svc.Create(ctx, in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// The stored lines carry the resolved categories.
	if c := created.Items[0].CategoryID; c == nil || *c != 8 {
		t.Errorf("milk category = %v, want the mapping's 8", created.Items[0].CategoryID)
	}
	if c := created.Items[1].CategoryID; c == nil || *c != 7 {
		t.Errorf("oats category = %v, want the input's 7 to win over the mapping's 8", created.Items[1].CategoryID)
	}
	if c := created.Items[2].CategoryID; c != nil {
		t.Errorf("bread category = %v, want nil (no mapping, no input)", c)
	}

	mappings.mu.Lock()
	defer mappings.mu.Unlock()
	// The identity-mapping Creates: every non-return purchase line, including
	// the already-mapped ones (their Create conflicts and is tolerated).
	if len(mappings.creates) != 3 {
		t.Fatalf("creates = %+v, want one per non-return line (Milk, Oats, Bread)", mappings.creates)
	}
	bread := mappings.creates[2]
	if bread.RawName != "Bread" || bread.StandardName != "Bread" || bread.Source != domain.MappingSourceManual {
		t.Fatalf("bread identity mapping = %+v, want raw=standard=Bread (manual)", bread)
	}
	for _, looked := range mappings.lookups {
		if strings.Contains(strings.ToLower(looked), "leergut") {
			t.Errorf("return line %q must not be looked up in the memory", looked)
		}
	}
	// The existing decisions were never overwritten by the identity Creates.
	if m := mappings.items["milk"]; m.StandardName != "Milk 1L" || m.Source != domain.MappingSourceUser {
		t.Errorf("existing Milk decision was overwritten: %+v", m)
	}
}

// TestTransactionCreateRecordsIdentityMappings pins the identity-mapping
// shape of a fresh typed name: raw = standard, the line's resolved category,
// source 'manual'.
func TestTransactionCreateRecordsIdentityMappings(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _ := newTestTransactionService(t)
	mappings := newFakeProductMappingStore()
	svc.mappings = mappings

	in := validTransactionInput()
	in.Items = []TransactionItemInput{
		{Name: "Yoghurt", CategoryID: ptrInt64(8), Quantity: 1, UnitPriceCents: 159},
		{Name: "Leergut 0.25", Quantity: 4, UnitPriceCents: -25},
	}
	if _, err := svc.Create(ctx, in); err != nil {
		t.Fatalf("create: %v", err)
	}

	mappings.mu.Lock()
	defer mappings.mu.Unlock()
	if len(mappings.creates) != 1 {
		t.Fatalf("creates = %+v, want only the Yoghurt identity mapping (return lines are skipped)", mappings.creates)
	}
	m := mappings.creates[0]
	if m.RawName != "Yoghurt" || m.StandardName != "Yoghurt" ||
		m.CategoryID == nil || *m.CategoryID != 8 || m.Source != domain.MappingSourceManual {
		t.Fatalf("identity mapping = %+v, want Yoghurt→Yoghurt cat 8 (manual)", m)
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

// TestTransactionCreateUnitValue covers the printed size magnitude on manual
// purchases: lines carry it (sanitized), new products are seeded with it, an
// existing product's decided value is never overwritten and a NULL one is
// learned — the same rules as the bill flow.
func TestTransactionCreateUnitValue(t *testing.T) {
	ctx := context.Background()
	svc, _, products, _ := newTestTransactionService(t)

	// A decided magnitude (0.5) and a NULL one on the catalogue.
	half := 0.5
	products.insert(domain.Product{Name: "Water 500ml", Unit: "ml", UnitValue: &half})
	products.insert(domain.Product{Name: "Bread", Unit: "pcs"})

	in := validTransactionInput()
	in.Items = []TransactionItemInput{
		{Name: "Water 500ml", Unit: "ml", UnitValue: floatPtr(500), Quantity: 1, UnitPriceCents: 45},    // decided stays
		{Name: "Bread", Unit: "pcs", UnitValue: nil, Quantity: 1, UnitPriceCents: 150},                  // NULL → learned
		{Name: "Cola Zero 1.5L", Unit: "l", UnitValue: floatPtr(1.5), Quantity: 1, UnitPriceCents: 210}, // new: seeded
	}
	created, err := svc.Create(ctx, in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Items[0].UnitValue == nil || *created.Items[0].UnitValue != 500 {
		t.Fatalf("water line unit_value = %v; want 500", created.Items[0].UnitValue)
	}
	water, err := products.FindByName(ctx, "water 500ml")
	if err != nil {
		t.Fatalf("water product: %v", err)
	}
	if water.UnitValue == nil || *water.UnitValue != 0.5 {
		t.Errorf("decided magnitude overwritten: %v; want 0.5", water.UnitValue)
	}
	bread, err := products.FindByName(ctx, "bread")
	if err != nil {
		t.Fatalf("bread product: %v", err)
	}
	if bread.UnitValue != nil {
		t.Errorf("NULL magnitude must stay NULL when the line has none: %v", bread.UnitValue)
	}
	cola, err := products.FindByName(ctx, "cola zero 1.5l")
	if err != nil {
		t.Fatalf("cola product: %v", err)
	}
	if cola.UnitValue == nil || *cola.UnitValue != 1.5 {
		t.Errorf("new product not seeded with magnitude: %v; want 1.5", cola.UnitValue)
	}

	// A 0 magnitude on a line is sanitized to "unknown" on the line too —
	// it must never reach storage as a real value.
	in.Items = []TransactionItemInput{
		{Name: "Cola Zero 1.5L", Unit: "l", UnitValue: floatPtr(0), Quantity: 1, UnitPriceCents: 210},
	}
	in.Description = "retry"
	retried, err := svc.Create(ctx, in)
	if err != nil {
		t.Fatalf("retry create: %v", err)
	}
	if retried.Items[0].UnitValue != nil {
		t.Errorf("0 magnitude not sanitized: %v; want NULL", retried.Items[0].UnitValue)
	}
	bread2, _ := products.FindByName(ctx, "bread")
	if bread2.UnitValue != nil {
		t.Errorf("magnitude leaked onto bread through the 0 line: %v", bread2.UnitValue)
	}
}

// TestTransactionCreateSkipsDepositArtifacts: a positive Pfand charge typed as
// a manual line stays an unlinked financial line — no product, no memory
// lookup, no identity mapping — while the real purchase next to it links
// normally.
func TestTransactionCreateSkipsDepositArtifacts(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _ := newTestTransactionService(t)
	mappings := newFakeProductMappingStore()
	svc.mappings = mappings

	in := validTransactionInput()
	in.Items = []TransactionItemInput{
		{Name: "PFAND 0,25", Quantity: 2, UnitPriceCents: 25},
		{Name: "Milk", Quantity: 1, UnitPriceCents: 139},
	}
	created, err := svc.Create(ctx, in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Items[0].ProductID != nil {
		t.Errorf("pfand line must stay unlinked, got product %d", *created.Items[0].ProductID)
	}
	if created.Items[1].ProductID == nil {
		t.Errorf("the real purchase must still link to its product")
	}

	mappings.mu.Lock()
	defer mappings.mu.Unlock()
	for _, looked := range mappings.lookups {
		if strings.Contains(strings.ToLower(looked), "pfand") {
			t.Errorf("artifact name %q must not be looked up in the memory", looked)
		}
	}
	for _, c := range mappings.creates {
		if strings.Contains(strings.ToLower(c.RawName), "pfand") {
			t.Errorf("artifact name %q must not be recorded into the memory (creates=%+v)", c.RawName, mappings.creates)
		}
	}
	if len(mappings.creates) != 1 || mappings.creates[0].RawName != "Milk" {
		t.Errorf("creates = %+v, want only the Milk identity mapping", mappings.creates)
	}
}

// TestTransactionCreateMappingFirstAndFillDown: a typed name that the
// normalization memory links to a catalogue product joins that product's
// purchase history (no near-duplicate by raw name), and blank unit/unit_value
// fields inherit the linked product's descriptors. Garbage the user actually
// typed is still sanitized to unknown, never replaced.
func TestTransactionCreateMappingFirstAndFillDown(t *testing.T) {
	ctx := context.Background()
	svc, _, products, _ := newTestTransactionService(t)
	mappings := newFakeProductMappingStore()
	svc.mappings = mappings

	one := 1.0
	milk := domain.Product{Name: "Whole Milk 3.8%", Unit: "l", UnitValue: &one}
	products.insert(milk)
	milk, err := products.FindByName(ctx, "Whole Milk 3.8%")
	if err != nil {
		t.Fatalf("seed milk: %v", err)
	}
	mappings.seedMapping(domain.ProductNameMapping{
		RawName:      "Whole Milk",
		StandardName: "Whole Milk 3.8%",
		ProductID:    &milk.ID,
		Source:       domain.MappingSourceAI,
	})

	in := validTransactionInput()
	in.Items = []TransactionItemInput{
		// Typed name differs from the catalogue row, unit/unit_value blank.
		{Name: "Whole Milk", Quantity: 2, UnitPriceCents: 149},
		// Garbage magnitude actually typed: sanitized to unknown, not replaced.
		{Name: "Whole Milk", Quantity: 1, UnitPriceCents: 99, UnitValue: floatPtr(0)},
	}
	created, err := svc.Create(ctx, in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	for i, want := range []struct {
		unit  string
		value *float64
	}{
		{"l", &one},
		{"l", nil},
	} {
		line := created.Items[i]
		if line.ProductID == nil || *line.ProductID != milk.ID {
			t.Errorf("line %d product_id = %v; want mapped product %d", i, line.ProductID, milk.ID)
		}
		if line.Unit != want.unit {
			t.Errorf("line %d unit = %q; want %q", i, line.Unit, want.unit)
		}
		got, wantV := line.UnitValue, want.value
		if wantV == nil {
			if got != nil {
				t.Errorf("line %d unit_value = %v; want NULL (garbage stays unknown)", i, *got)
			}
		} else if got == nil || *got != *wantV {
			t.Errorf("line %d unit_value = %v; want %v (filled from the product)", i, got, *wantV)
		}
	}
	// Mapping-first means no find-or-create: the catalogue still has the one
	// milk row under its standard name, and the memory's link was never
	// repointed at a duplicate.
	if len(mappings.links) != 0 {
		t.Errorf("memory link rewritten: %v", mappings.links)
	}
	if _, err := products.FindByName(ctx, "Whole Milk"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("a duplicate product was created under the typed name")
	}
	// The create attempt hits the unique constraint; the AI-reviewed mapping
	// decision must still be what the memory holds.
	mappings.mu.Lock()
	still := mappings.items["whole milk"]
	mappings.mu.Unlock()
	if still.Source != domain.MappingSourceAI || still.ProductID == nil || *still.ProductID != milk.ID {
		t.Errorf("mapping decision overwritten: %+v", still)
	}
}

// TestTransactionCreateDefaultsFromTransaction: an open account defaults to
// the primary (first listed) account, and an uncategorized item line files
// under the transaction's main category — the display default never learning
// into the normalization memory.
func TestTransactionCreateDefaultsFromTransaction(t *testing.T) {
	ctx := context.Background()
	svc, txs, _, _ := newTestTransactionService(t)
	mappings := newFakeProductMappingStore()
	svc.mappings = mappings

	in := validTransactionInput()
	in.AccountID = 0 // open → the primary account (the harness's EUR wallet)
	cat8 := int64(8)
	in.CategoryID = &cat8
	in.Items = []TransactionItemInput{{Name: "Dish Soap", Quantity: 1, UnitPriceCents: 199}}
	created, err := svc.Create(ctx, in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.AccountID != 1 || created.Currency != "EUR" {
		t.Fatalf("account = %d %s; want 1 EUR", created.AccountID, created.Currency)
	}
	if created.Items[0].CategoryID == nil || *created.Items[0].CategoryID != cat8 {
		t.Fatalf("line category = %v; want the transaction's main category", created.Items[0].CategoryID)
	}
	// But the transaction-level default is not a memory decision: the identity
	// mapping records no category, and the seeded product is not created with
	// a reviewed-looking category either.
	mappings.mu.Lock()
	recorded := mappings.items["dish soap"]
	mappings.mu.Unlock()
	if recorded.RawName == "" {
		t.Fatalf("identity mapping missing")
	}
	if recorded.CategoryID != nil {
		t.Errorf("transaction-level category learned into the memory: %+v", recorded)
	}
	saved := txs.items[created.ID]
	if len(saved.Items) != 1 || saved.Items[0].CategoryID == nil || *saved.Items[0].CategoryID != cat8 {
		t.Fatalf("line category not persisted: %+v", saved.Items)
	}

	// Explicit and mapped categories still outrank the transaction default and
	// are recorded: the mapped name fills first, the explicit last.
	mappings.seedMapping(domain.ProductNameMapping{
		RawName:      "Bread",
		StandardName: "Bread",
		CategoryID:   &cat8,
		Source:       domain.MappingSourceUser,
	})
	cat7 := int64(7)
	in2 := validTransactionInput()
	in2.AccountID = 0
	in2.CategoryID = nil
	in2.Items = []TransactionItemInput{
		{Name: "Bread", Quantity: 1, UnitPriceCents: 150},                     // mapped → 8
		{Name: "Butter", CategoryID: &cat7, Quantity: 1, UnitPriceCents: 200}, // explicit → 7
	}
	created2, err := svc.Create(ctx, in2)
	if err != nil {
		t.Fatalf("create 2: %v", err)
	}
	if created2.Items[0].CategoryID == nil || *created2.Items[0].CategoryID != cat8 {
		t.Errorf("mapped category lost: %v", created2.Items[0].CategoryID)
	}
	if created2.Items[1].CategoryID == nil || *created2.Items[1].CategoryID != cat7 {
		t.Errorf("explicit category lost: %v", created2.Items[1].CategoryID)
	}
}
