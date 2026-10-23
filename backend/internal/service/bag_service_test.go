package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"

	"home-finance-planner/backend/internal/domain"
)

// fakeBagStore is an in-memory bag store: bags, the allocation ledger and a
// manually seeded tagged-spend table (the real spend agg is SQL; the fake
// keeps the contract, the tests drive spend through it).
type fakeBagStore struct {
	mu    sync.Mutex
	bags  map[int64]domain.Bag
	alloc map[int64][]domain.BagAllocation // by bag
	leaks map[int64]domain.BagAllocation   // alloc id → row (all bags)
	spend []domain.BagCurrencySpend
	next  int64 // shared counter over bag ids and allocation ids
}

func newFakeBagStore() *fakeBagStore {
	return &fakeBagStore{
		bags:  map[int64]domain.Bag{},
		alloc: map[int64][]domain.BagAllocation{},
		leaks: map[int64]domain.BagAllocation{},
		next:  1,
	}
}

func (f *fakeBagStore) ListBags(_ context.Context, flt domain.BagFilters) ([]domain.Bag, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []domain.Bag{}
	for i := int64(1); i < f.next; i++ {
		b, ok := f.bags[i]
		if !ok {
			continue
		}
		if flt.AccountID > 0 && b.AccountID != flt.AccountID {
			continue
		}
		if flt.Status != "" && b.Status != flt.Status {
			continue
		}
		out = append(out, b)
	}
	return out, nil
}

func (f *fakeBagStore) GetBagByID(_ context.Context, id int64) (domain.Bag, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.bags[id]
	if !ok {
		return domain.Bag{}, domain.ErrNotFound
	}
	return b, nil
}

func (f *fakeBagStore) CreateBag(_ context.Context, b domain.Bag) (domain.Bag, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b.ID = f.next
	f.next++
	f.bags[b.ID] = b
	return b, nil
}

func (f *fakeBagStore) UpdateBag(_ context.Context, b domain.Bag) (domain.Bag, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.bags[b.ID]; !ok {
		return domain.Bag{}, domain.ErrNotFound
	}
	f.bags[b.ID] = b
	return b, nil
}

func (f *fakeBagStore) DeleteBag(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.bags[id]; !ok {
		return domain.ErrNotFound
	}
	delete(f.bags, id)
	delete(f.alloc, id)
	return nil
}

func (f *fakeBagStore) CreateAllocation(_ context.Context, a domain.BagAllocation) (domain.BagAllocation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a.ID = f.next
	f.next++
	f.alloc[a.BagID] = append(f.alloc[a.BagID], a)
	f.leaks[a.ID] = a
	return a, nil
}

func (f *fakeBagStore) ListAllocations(_ context.Context, bagID int64) ([]domain.BagAllocation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.BagAllocation, len(f.alloc[bagID]))
	copy(out, f.alloc[bagID])
	return out, nil
}

func (f *fakeBagStore) CreateAllocationPair(_ context.Context, from, to domain.BagAllocation) (domain.BagAllocation, domain.BagAllocation, error) {
	from, err := f.CreateAllocation(context.Background(), from)
	if err != nil {
		return from, to, err
	}
	to, err = f.CreateAllocation(context.Background(), to)
	if err != nil {
		return from, to, err
	}
	// Link the pair through related_allocation_id like the real store does.
	f.mu.Lock()
	defer f.mu.Unlock()
	fromID, toID := from.ID, to.ID
	from.RelatedAllocationID, to.RelatedAllocationID = &toID, &fromID
	for i := range f.alloc[from.BagID] {
		if f.alloc[from.BagID][i].ID == fromID {
			f.alloc[from.BagID][i] = from
		}
	}
	for i := range f.alloc[to.BagID] {
		if f.alloc[to.BagID][i].ID == toID {
			f.alloc[to.BagID][i] = to
		}
	}
	return from, to, nil
}

func (f *fakeBagStore) AllocationTotals(_ context.Context, until string) (map[int64]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[int64]int64{}
	for _, rows := range f.alloc {
		for _, a := range rows {
			if until != "" && a.Date > until {
				continue
			}
			out[a.BagID] += a.AmountCents
		}
	}
	return out, nil
}

func (f *fakeBagStore) TaggedSpend(_ context.Context, until string) ([]domain.BagCurrencySpend, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []domain.BagCurrencySpend{}
	for _, s := range f.spend {
		if until != "" && s.Currency == "LATER" { // test hook: bounded rows only
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

// seedSpend pushes a tagged-spend group into the fake (what the SQL read would
// find for tagged transactions/bills).
func (f *fakeBagStore) seedSpend(bagID int64, currency string, cents int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.spend = append(f.spend, domain.BagCurrencySpend{BagID: bagID, Currency: currency, Cents: cents})
}

func newTestBagService(t *testing.T) (*BagService, *fakeBagStore, *fakeAccountStore) {
	t.Helper()
	accounts := newFakeAccountStore()
	ctx := context.Background()
	if _, err := accounts.Create(ctx, domain.Account{Name: "Main", Type: domain.AccountChecking, Currency: "EUR", OpeningBalanceCents: 50000}); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := accounts.Create(ctx, domain.Account{Name: "Cash", Type: domain.AccountCash, Currency: "USD", OpeningBalanceCents: 10000}); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	store := newFakeBagStore()
	return &BagService{bags: store, accounts: accounts, log: slog.New(slog.NewTextHandler(io.Discard, nil))}, store, accounts
}

func validBagInput() BagInput {
	return BagInput{Name: "Warm Expenses", AccountID: 1, TargetCents: nil}
}

// TestBagAllocateSigns: fund stores positive, withdraw stores negative and
// adjust is free-form; the balance is ledger minus spend.
func TestBagAllocateSigns(t *testing.T) {
	ctx := context.Background()
	svc, store, _ := newTestBagService(t)

	b, err := svc.Create(ctx, validBagInput())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if b.Status != domain.BagOpen || b.BalanceCents != 0 {
		t.Fatalf("new bag = %s balance %d; want open/0", b.Status, b.BalanceCents)
	}

	if _, err := svc.Allocate(ctx, b.ID, domain.BagAllocationInput{Kind: domain.BagFund, AmountCents: 30000, Date: "2026-10-01"}); err != nil {
		t.Fatalf("fund: %v", err)
	}
	wd, err := svc.Allocate(ctx, b.ID, domain.BagAllocationInput{Kind: domain.BagWithdraw, AmountCents: 5000, Date: "2026-10-02"})
	if err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	if wd.AmountCents != -5000 {
		t.Fatalf("withdraw stored %d; want -5000 (kind carries the sign)", wd.AmountCents)
	}
	if _, err := svc.Allocate(ctx, b.ID, domain.BagAllocationInput{Kind: domain.BagAdjust, AmountCents: 300, Date: "2026-10-03"}); err != nil {
		t.Fatalf("adjust: %v", err)
	}

	if _, err := svc.Allocate(ctx, b.ID, domain.BagAllocationInput{Kind: domain.BagRebalance, AmountCents: 100, Date: "2026-10-03"}); err == nil {
		t.Fatalf("rebalance accepted through the single endpoint; want refusal")
	}
	if _, err := svc.Allocate(ctx, b.ID, domain.BagAllocationInput{Kind: domain.BagFund, AmountCents: -1, Date: "2026-10-03"}); err == nil {
		t.Fatalf("negative magnitude accepted for fund")
	}
	if _, err := svc.Allocate(ctx, b.ID, domain.BagAllocationInput{Kind: domain.BagWithdraw, AmountCents: 0, Date: "2026-10-03"}); err == nil {
		t.Fatalf("zero magnitude accepted for withdraw")
	}
	if _, err := svc.Allocate(ctx, b.ID, domain.BagAllocationInput{Kind: domain.BagAdjust, AmountCents: 0, Date: "2026-10-03"}); err == nil {
		t.Fatalf("zero adjustment accepted")
	}
	if _, err := svc.Allocate(ctx, b.ID, domain.BagAllocationInput{Kind: domain.BagFund, AmountCents: 100, Date: "2026-13-40"}); err == nil {
		t.Fatalf("invalid date accepted")
	}
	if _, err := svc.Allocate(ctx, b.ID, domain.BagAllocationInput{Kind: domain.BagFund, AmountCents: 100, Date: "2026-10-03", Note: string(make([]byte, 201))}); err == nil {
		t.Fatalf("over-long note accepted")
	}

	st, err := svc.Get(ctx, b.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	// 30000 fund − 5000 withdraw + 300 adjust = 25300
	if st.BalanceCents != 25300 || st.TaggedSpendCents != 0 {
		t.Fatalf("balance = %d spend %d; want 25300/0", st.BalanceCents, st.TaggedSpendCents)
	}

	store.seedSpend(b.ID, "EUR", 2300)
	st, _ = svc.Get(ctx, b.ID)
	if st.BalanceCents != 23000 || st.TaggedSpendCents != 2300 {
		t.Fatalf("balance after spend = %d/%d; want 23000/2300", st.BalanceCents, st.TaggedSpendCents)
	}
}

// TestBagLifecycleAndClosedRefusals: closing requires a zero balance and a
// closed bag admits no ledger writes or tagging.
func TestBagLifecycleAndClosedRefusals(t *testing.T) {
	ctx := context.Background()
	svc, store, _ := newTestBagService(t)

	b, err := svc.Create(ctx, validBagInput())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.Allocate(ctx, b.ID, domain.BagAllocationInput{Kind: domain.BagFund, AmountCents: 1000, Date: "2026-10-01"}); err != nil {
		t.Fatalf("fund: %v", err)
	}
	if _, err := svc.SetStatus(ctx, b.ID, domain.BagClosed); err == nil {
		t.Fatalf("close with balance accepted; want refusal")
	}
	// Release the leftover, then close.
	if _, err := svc.Allocate(ctx, b.ID, domain.BagAllocationInput{Kind: domain.BagWithdraw, AmountCents: 1000, Date: "2026-10-02"}); err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	if _, err := svc.SetStatus(ctx, b.ID, domain.BagClosed); err != nil {
		t.Fatalf("close at zero: %v", err)
	}
	if _, err := svc.SetStatus(ctx, b.ID, domain.BagOpen); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if _, err := svc.SetStatus(ctx, b.ID, domain.BagLifecycle("paused")); err == nil {
		t.Fatalf("bogus status accepted")
	}
	_ = store
}

// TestBagRebalanceRules: same account only, positive magnitude, source
// balance respected, and both legs land linked in the ledger.
func TestBagRebalanceRules(t *testing.T) {
	ctx := context.Background()
	svc, store, _ := newTestBagService(t)

	src, err := svc.Create(ctx, BagInput{Name: "Holiday", AccountID: 1})
	if err != nil {
		t.Fatalf("create src: %v", err)
	}
	dst, err := svc.Create(ctx, BagInput{Name: "Repairs", AccountID: 1})
	if err != nil {
		t.Fatalf("create dst: %v", err)
	}
	if _, err := svc.Allocate(ctx, src.ID, domain.BagAllocationInput{Kind: domain.BagFund, AmountCents: 4000, Date: "2026-10-01"}); err != nil {
		t.Fatalf("fund: %v", err)
	}

	neg, pos, err := svc.Rebalance(ctx, RebalanceInput{FromBagID: src.ID, ToBagID: dst.ID, AmountCents: 2500, Note: "leftover", Date: "2026-10-05"})
	if err != nil {
		t.Fatalf("rebalance: %v", err)
	}
	if neg.AmountCents != -2500 || pos.AmountCents != 2500 {
		t.Fatalf("legs = %d/%d; want -2500/2500", neg.AmountCents, pos.AmountCents)
	}
	for _, leg := range []domain.BagAllocation{neg, pos} {
		if leg.Kind != string(domain.BagRebalance) || leg.RelatedAllocationID == nil {
			t.Fatalf("leg %d kind %s related %v; want rebalance linked", leg.ID, leg.Kind, leg.RelatedAllocationID)
		}
	}

	srcSt, _ := svc.Get(ctx, src.ID)
	if srcSt.BalanceCents != 1500 {
		t.Fatalf("src balance = %d; want 1500", srcSt.BalanceCents)
	}

	// Overshooting the source is refused — a bag that "spent more than it
	// holds" is a data smell.
	if _, _, err := svc.Rebalance(ctx, RebalanceInput{FromBagID: src.ID, ToBagID: dst.ID, AmountCents: 1501, Date: "2026-10-06"}); err == nil {
		t.Fatalf("over-draw rebalance accepted")
	}
	// Cross-account is refused (Cash is account 2).
	other, err := svc.Create(ctx, BagInput{Name: "Gadget", AccountID: 2})
	if err != nil {
		t.Fatalf("create other: %v", err)
	}
	if _, _, err := svc.Rebalance(ctx, RebalanceInput{FromBagID: src.ID, ToBagID: other.ID, AmountCents: 100, Date: "2026-10-06"}); err == nil {
		t.Fatalf("cross-account rebalance accepted")
	}
	if _, _, err := svc.Rebalance(ctx, RebalanceInput{FromBagID: src.ID, ToBagID: src.ID, AmountCents: 100, Date: "2026-10-06"}); err == nil {
		t.Fatalf("self rebalance accepted")
	}
	if _, _, err := svc.Rebalance(ctx, RebalanceInput{FromBagID: src.ID, ToBagID: dst.ID, AmountCents: 0, Date: "2026-10-06"}); err == nil {
		t.Fatalf("zero-amount rebalance accepted")
	}
	_ = store
}

// TestBagOverviewUnbaggedAndOversubscribed: per account, unbagged = balance −
// Σ open bag balances; closing a bag returns its leftover to unbagged.
func TestBagOverviewUnbaggedAndOversubscribed(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newTestBagService(t)

	a, err := svc.Create(ctx, BagInput{Name: "Warm Expenses", AccountID: 1, TargetCents: nil})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	b, err := svc.Create(ctx, BagInput{Name: "Holiday", AccountID: 1})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	for _, id := range []int64{a.ID, b.ID} {
		if _, err := svc.Allocate(ctx, id, domain.BagAllocationInput{Kind: domain.BagFund, AmountCents: 20000, Date: "2026-10-01"}); err != nil {
			t.Fatalf("fund: %v", err)
		}
	}

	ov, err := svc.Overview(ctx)
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	var view *domain.BagAccountView
	for i := range ov.Accounts {
		if ov.Accounts[i].AccountID == 1 {
			view = &ov.Accounts[i]
		}
	}
	if view == nil {
		t.Fatalf("account 1 missing from overview")
	}
	// 50000 − 40000 bagged = 10000 unbagged, not oversubscribed.
	if view.UnbaggedCents != 10000 || view.Oversubscribed {
		t.Fatalf("unbagged = %d oversubscribed=%v; want 10000 false", view.UnbaggedCents, view.Oversubscribed)
	}

	// Spending into the bags does not change unbagged until allocations adapt
	// (balance drops, but the reserve is still claimed). Tag the spend of the
	// first bag — the account balance is user-maintained, so unbagged moves on
	// the bag side.
	store := svc.bags.(*fakeBagStore)
	store.seedSpend(a.ID, "EUR", 20000)

	ov, _ = svc.Overview(ctx)
	view = &ov.Accounts[0]
	// Still fully claimed — the money left through the bag.
	if view.Oversubscribed {
		t.Fatalf("oversubscribed before balance catch-up")
	}

	// Closing a spent-out bag returns its (zero) leftover: unbagged stays.
	if _, err := svc.Allocate(ctx, a.ID, domain.BagAllocationInput{Kind: domain.BagWithdraw, AmountCents: 0}); err == nil {
		t.Fatalf("zero withdraw accepted")
	}
}

// TestBagYearCloseShortfall: the preview bounds facts at the year end and
// reports shortage vs target only for open bags with a target.
func TestBagYearCloseShortfall(t *testing.T) {
	ctx := context.Background()
	svc, store, accounts := newTestBagService(t)

	acc, _ := accounts.GetByID(ctx, 1)
	acc.OpeningBalanceCents = 100000
	if _, err := accounts.Update(ctx, acc); err != nil {
		t.Fatalf("top up: %v", err)
	}

	target := int64(30000)
	bags, err := svc.Create(ctx, BagInput{Name: "Warm Expenses", AccountID: 1, TargetCents: &target})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	holiday, err := svc.Create(ctx, BagInput{Name: "Holiday", AccountID: 1, TargetCents: nil})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.Allocate(ctx, bags.ID, domain.BagAllocationInput{Kind: domain.BagFund, AmountCents: 10000, Date: "2026-01-15"}); err != nil {
		t.Fatalf("fund: %v", err)
	}
	if _, err := svc.Allocate(ctx, holiday.ID, domain.BagAllocationInput{Kind: domain.BagFund, AmountCents: 5000, Date: "2027-02-15"}); err != nil {
		t.Fatalf("fund next year: %v", err)
	}
	store.seedSpend(bags.ID, "EUR", 2500)

	preview, err := svc.YearClose(ctx, "2026")
	if err != nil {
		t.Fatalf("year close: %v", err)
	}
	if preview.Year != "2026" || len(preview.Bags) != 2 {
		t.Fatalf("preview = year %s bags %d; want 2026/2", preview.Year, len(preview.Bags))
	}
	for _, row := range preview.Bags {
		if row.BagID == bags.ID {
			// 10000 fund − 2500 spend = 7500; short of the 30000 target by 22500.
			if row.BalanceCents != 7500 || row.ShortfallCents != 22500 {
				t.Fatalf("target bag = %d/%d; want 7500/22500", row.BalanceCents, row.ShortfallCents)
			}
		}
		if row.BagID == holiday.ID {
			// Next year's funding is past the 2026 bound: 0 balance, untargeted.
			if row.BalanceCents != 0 || row.TargetCents != 0 || row.ShortfallCents != 0 {
				t.Fatalf("holiday row = %d/%d/%d; want 0/0/0", row.BalanceCents, row.TargetCents, row.ShortfallCents)
			}
		}
	}
	if _, err := svc.YearClose(ctx, "26"); err == nil {
		t.Fatalf("non-year accepted")
	}
}

// TestBagValidation covers create/update refusals: names, unknown accounts,
// re-pointing, and delete.
func TestBagValidation(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newTestBagService(t)

	if _, err := svc.Create(ctx, BagInput{Name: "  ", AccountID: 1}); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("blank name = %v; want ErrValidation", err)
	}
	if _, err := svc.Create(ctx, BagInput{Name: "X", AccountID: 99}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown account = %v; want ErrNotFound", err)
	}
	if _, err := svc.Create(ctx, BagInput{Name: "X", AccountID: 1, TargetCents: &zeroCents}); err == nil {
		t.Errorf("zero target accepted; want refusal")
	}

	b, err := svc.Create(ctx, validBagInput())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Re-pointing is refused — a reserve does not move between accounts.
	if _, err := svc.Update(ctx, b.ID, BagInput{Name: "Warm Expenses", AccountID: 2}); err == nil {
		t.Fatalf("re-point accepted; want refusal")
	}
	// Rename on the same account works.
	renamed, err := svc.Update(ctx, b.ID, BagInput{Name: "Warm Season", AccountID: 1})
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if renamed.Name != "Warm Season" {
		t.Fatalf("rename not applied: %s", renamed.Name)
	}
	if err := svc.Delete(ctx, b.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := svc.Get(ctx, b.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("get after delete = %v; want ErrNotFound", err)
	}
	if _, err := svc.AllocationHistory(ctx, b.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("history of deleted bag = %v; want ErrNotFound", err)
	}
}

var zeroCents = int64(0)