package service

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"home-finance-planner/backend/internal/domain"
)

// fakeBudgetStore is an in-memory BudgetStore; RolloverMonthly mirrors the
// real store — it closes the stale monthly rows and opens successors that
// inherit the carry map's carried_cents (carry_over rows only), so the fake
// keeps the running-balance chain the tests read.
type fakeBudgetStore struct {
	items      []domain.Budget
	rolloverTo []string
	carries    []map[int64]int64 // carries of each RolloverMonthly call
	catSpend   []domain.CategoryMonthCurrencyTotal
	billSpend  []domain.BudgetCurrencySpend
}

func (f *fakeBudgetStore) List(context.Context) ([]domain.Budget, error) {
	return f.items, nil
}

func (f *fakeBudgetStore) GetByID(_ context.Context, id int64) (domain.Budget, error) {
	for _, b := range f.items {
		if b.ID == id {
			return b, nil
		}
	}
	return domain.Budget{}, domain.ErrNotFound
}

func (f *fakeBudgetStore) Create(_ context.Context, b domain.Budget) (domain.Budget, error) {
	b.ID = int64(len(f.items) + 1)
	f.items = append(f.items, b)
	return b, nil
}

func (f *fakeBudgetStore) Update(_ context.Context, b domain.Budget) (domain.Budget, error) {
	for i := range f.items {
		if f.items[i].ID == b.ID {
			f.items[i] = b
			return b, nil
		}
	}
	return b, nil
}

func (f *fakeBudgetStore) Delete(context.Context, int64) error { return nil }

func (f *fakeBudgetStore) ListStaleMonthly(_ context.Context, month string) ([]domain.Budget, error) {
	var out []domain.Budget
	for _, b := range f.items {
		if b.Cadence == domain.BudgetMonthly && b.Status == domain.BudgetOpen && b.Month != "" && b.Month < month {
			out = append(out, b)
		}
	}
	return out, nil
}

// CarryCategoryMonthSpend returns the seeded spend halves (what the SQL
// reads would find for the tagged transactions/bills); nil fields = no spend.
func (f *fakeBudgetStore) CarryCategoryMonthSpend(context.Context) ([]domain.CategoryMonthCurrencyTotal, error) {
	return f.catSpend, nil
}

func (f *fakeBudgetStore) CarryBillSpend(context.Context) ([]domain.BudgetCurrencySpend, error) {
	return f.billSpend, nil
}

func (f *fakeBudgetStore) RolloverMonthly(_ context.Context, month string, carries map[int64]int64) error {
	f.rolloverTo = append(f.rolloverTo, month)
	copied := map[int64]int64{}
	for k, v := range carries {
		copied[k] = v
	}
	f.carries = append(f.carries, copied)
	// ids continue from the highest existing row id
	nextID := int64(0)
	for _, b := range f.items {
		if b.ID > nextID {
			nextID = b.ID
		}
	}
	next := make([]domain.Budget, 0, len(f.items)+len(copied))
	for _, b := range f.items {
		if b.Cadence == domain.BudgetMonthly && b.Status == domain.BudgetOpen && b.Month != "" && b.Month < month {
			b.Status = domain.BudgetClosed
			succ := b
			nextID++
			succ.ID = nextID
			succ.Month = month
			succ.Status = domain.BudgetOpen
			succ.ClosedAt = nil
			succ.CarriedCents = carries[b.ID]
			next = append(next, b, succ)
			continue
		}
		next = append(next, b)
	}
	f.items = next
	return nil
}

func newTestBudgetService() (*BudgetService, *fakeBudgetStore) {
	store := &fakeBudgetStore{}
	return &BudgetService{
		budgets:    store,
		categories: fakeCategoryNames{cats: []domain.Category{{ID: 1, Name: "Groceries"}}},
	}, store
}

func TestBudgetCreateDefaultsToEnvelope(t *testing.T) {
	svc, _ := newTestBudgetService()
	b, err := svc.Create(context.Background(), BudgetInput{CategoryID: 1, AmountCents: 50_000})
	if err != nil {
		t.Fatal(err)
	}
	if b.Cadence != domain.BudgetEnvelope {
		t.Errorf("Cadence = %q, want open", b.Cadence)
	}
	if b.Month != "" {
		t.Errorf("Month = %q, want empty for envelope", b.Month)
	}
}

func TestBudgetCreateMonthlyStampsCurrentMonth(t *testing.T) {
	svc, _ := newTestBudgetService()
	b, err := svc.Create(context.Background(), BudgetInput{
		CategoryID: 1, AmountCents: 30_000, Cadence: domain.BudgetMonthly,
	})
	if err != nil {
		t.Fatal(err)
	}
	if b.Month != currentMonth() {
		t.Errorf("Month = %q, want %q", b.Month, currentMonth())
	}
}

func TestBudgetCreateRejectsBadCadence(t *testing.T) {
	svc, _ := newTestBudgetService()
	if _, err := svc.Create(context.Background(), BudgetInput{
		CategoryID: 1, AmountCents: 10_000, Cadence: "weekly",
	}); err == nil {
		t.Fatal("expected validation error for cadence weekly")
	}
}

func TestBudgetUpdateChangesAmountOfOpenRow(t *testing.T) {
	svc, _ := newTestBudgetService()
	b, err := svc.Create(context.Background(), BudgetInput{CategoryID: 1, AmountCents: 50_000})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := svc.Update(context.Background(), b.ID, 75_000, nil)
	if err != nil {
		t.Fatalf("update open budget: %v", err)
	}
	if updated.AmountCents != 75_000 {
		t.Errorf("AmountCents = %d, want 75000", updated.AmountCents)
	}
}

func TestBudgetUpdateRefusesClosedRow(t *testing.T) {
	svc, _ := newTestBudgetService()
	b, err := svc.Create(context.Background(), BudgetInput{CategoryID: 1, AmountCents: 50_000})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetStatus(context.Background(), b.ID, domain.BudgetClosed); err != nil {
		t.Fatalf("close: %v", err)
	}
	// A closed envelope's amount is its final account; a closed monthly row's
	// cap is history. Reopen, then edit.
	if _, err := svc.Update(context.Background(), b.ID, 75_000, nil); err == nil {
		t.Fatal("expected refusal to edit a closed budget")
	} else if !contains(err.Error(), "reopen") {
		t.Errorf("unexpected error: %v", err)
	}
	if _, err := svc.Update(context.Background(), b.ID, 0, nil); err == nil {
		t.Fatal("expected refusal of zero amount")
	}
}

func TestEnsureMonthlyCurrentValidatesAndForwards(t *testing.T) {
	svc, store := newTestBudgetService()
	if err := svc.EnsureMonthlyCurrent(context.Background(), "bogus"); err == nil {
		t.Fatal("expected validation error for month bogus")
	}
	if err := svc.EnsureMonthlyCurrent(context.Background(), "2026-11"); err != nil {
		t.Fatal(err)
	}
	if len(store.rolloverTo) != 1 || store.rolloverTo[0] != "2026-11" {
		t.Errorf("rolloverTo = %v, want [2026-11]", store.rolloverTo)
	}
}

func TestBudgetListTriggersRollover(t *testing.T) {
	svc, store := newTestBudgetService()
	if _, err := svc.List(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if len(store.rolloverTo) != 1 || store.rolloverTo[0] != currentMonth() {
		t.Errorf("rolloverTo = %v, want [%s]", store.rolloverTo, currentMonth())
	}
}

// fakeBaseCurrency / fakeRater supply the carry chain's conversion sources.
type fakeBaseCurrency struct{ currency string }

func (f fakeBaseCurrency) BaseCurrency(context.Context) (string, error) { return f.currency, nil }

type fakeRater struct{ rates map[string]float64 }

func (f fakeRater) Snapshot(context.Context) (domain.RateSnapshot, error) {
	return domain.RateSnapshot{Pivot: "EUR", Rates: f.rates}, nil
}

func newCarryBudgetService() (*BudgetService, *fakeBudgetStore) {
	svc, store := newTestBudgetService()
	svc.base = fakeBaseCurrency{currency: "EUR"}
	svc.rates = fakeRater{rates: map[string]float64{"EUR": 1, "USD": 1.1}}
	svc.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	return svc, store
}

// TestBudgetCarryChainRollsLeftoverForward: a carrying monthly series rolls
// its running balance into successors — amount − month spend (converted like
// the dashboard fold) PLUS all-time bill line links, compounding across
// months (spec §3a: running balance, not single-month carry). Successors
// inherit the flag; rollover-off rows stay byte-equal legacy (carried 0).
func TestBudgetCarryChainRollsLeftoverForward(t *testing.T) {
	svc, store := newCarryBudgetService()
	// Seeded directly (Create stamps the current month on monthly rows): a
	// July carrying row and a September rollover-off row.
	legacyOn := domain.Budget{ID: 1, CategoryID: 1, AmountCents: 10_000, Status: domain.BudgetOpen,
		Cadence: domain.BudgetMonthly, Month: "2026-07", CarryOver: true}
	legacyOff := domain.Budget{ID: 2, CategoryID: 2, AmountCents: 5_000, Status: domain.BudgetOpen,
		Cadence: domain.BudgetMonthly, Month: "2026-09"}
	store.items = append(store.items, legacyOn, legacyOff)
	// July spend: 3000 in the category (EUR) + a 2000-EUR bill line linked to
	// the July row → leftover 5000.
	store.catSpend = []domain.CategoryMonthCurrencyTotal{
		{CategoryID: 1, Month: "2026-07", Currency: "EUR", TotalCents: 3_000},
	}
	store.billSpend = []domain.BudgetCurrencySpend{{BudgetID: legacyOn.ID, Currency: "EUR", Cents: 2_000}}

	// Rollover July → August on a stale service clock, then August → September.
	if err := svc.EnsureMonthlyCurrent(context.Background(), "2026-08"); err != nil {
		t.Fatalf("rollover july: %v", err)
	}
	// The July row closed unchanged — closed rows are never rewritten.
	closed, err := svc.budgets.GetByID(context.Background(), legacyOn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if closed.Status != domain.BudgetClosed || closed.CarriedCents != 0 {
		t.Errorf("closed predecessor = %+v; want closed, carried 0 (never rewritten)", closed)
	}
	var succRow *domain.Budget
	for i := range store.items {
		b := &store.items[i]
		if b.CategoryID == 1 && b.Month == "2026-08" && b.Status == domain.BudgetOpen {
			succRow = b
		}
	}
	if succRow == nil {
		t.Fatal("no carry successor created")
	}
	if !succRow.CarryOver || succRow.CarriedCents != 5_000 {
		t.Errorf("successor carry = %+v; want carry_over on, carried 5000", succRow)
	}

	// August month: only USD 1100 in the snapshot (folds to EUR 1000) —
	// the whole rest rolls on: the July leftover (5000) + the full August
	// cap (10000) − 1000.
	store.catSpend = append(store.catSpend,
		domain.CategoryMonthCurrencyTotal{CategoryID: 1, Month: "2026-08", Currency: "USD", TotalCents: 1_100})
	// USD 1.1 in the snapshot folds to EUR 1000 → September successor carry.
	if err := svc.EnsureMonthlyCurrent(context.Background(), "2026-09"); err != nil {
		t.Fatalf("rollover august: %v", err)
	}
	var sep *domain.Budget
	for i := range store.items {
		b := &store.items[i]
		if b.CategoryID == 1 && b.Month == "2026-09" && b.Status == domain.BudgetOpen {
			sep = b
		}
	}
	if sep == nil {
		t.Fatal("no September successor created")
	}
	if sep.CarriedCents != 14_000 {
		t.Errorf("September carry = %d; want 14000 (5000 + 10000 − 1000 folded USD spend)", sep.CarriedCents)
	}

	// Legacy off row: rolled with carried 0, flag stays off.
	var offSucc *domain.Budget
	for i := range store.items {
		b := &store.items[i]
		if b.CategoryID == 2 && b.Month == "2026-09" && b.Status == domain.BudgetOpen {
			offSucc = b
		}
	}
	if offSucc == nil || offSucc.CarriedCents != 0 || offSucc.CarryOver {
		t.Errorf("legacy successor = %+v; want carry_over off, carried 0", offSucc)
	}
}

// TestBudgetCarryChainDeficitCarriesNegative: spend beyond the cap carries
// the deficit into the next month (decision 2026-10-28 — no floor).
func TestBudgetCarryChainDeficitCarriesNegative(t *testing.T) {
	svc, store := newCarryBudgetService()
	stale := domain.Budget{ID: 1, CategoryID: 1, AmountCents: 10_000, Status: domain.BudgetOpen,
		Cadence: domain.BudgetMonthly, Month: "2026-09", CarryOver: true}
	store.items = []domain.Budget{stale}
	store.catSpend = []domain.CategoryMonthCurrencyTotal{
		{CategoryID: 1, Month: "2026-09", Currency: "EUR", TotalCents: 12_500},
	}
	if err := svc.EnsureMonthlyCurrent(context.Background(), "2026-10"); err != nil {
		t.Fatalf("rollover: %v", err)
	}
	var succ *domain.Budget
	for i := range store.items {
		b := &store.items[i]
		if b.Month == "2026-10" && b.Status == domain.BudgetOpen {
			succ = b
		}
	}
	if succ == nil {
		t.Fatal("no successor created")
	}
	if succ.CarriedCents != -2_500 {
		t.Errorf("deficit carry = %d; want -2500 (cumulative negative)", succ.CarriedCents)
	}
}

// TestBudgetCarrySkippedWithoutConversionSources: no base/rates (bare unit
// test service) → rows roll over with carried 0 rather than a half-converted
// chain; the legacy byte-equal behavior is kept for that pass.
func TestBudgetCarrySkippedWithoutConversionSources(t *testing.T) {
	svc, store := newTestBudgetService()
	store.items = []domain.Budget{{
		ID: 1, CategoryID: 1, AmountCents: 10_000, Status: domain.BudgetOpen,
		Cadence: domain.BudgetMonthly, Month: "2026-09", CarryOver: true,
	}}
	if err := svc.EnsureMonthlyCurrent(context.Background(), "2026-10"); err != nil {
		t.Fatalf("rollover: %v", err)
	}
	var succ *domain.Budget
	for i := range store.items {
		b := &store.items[i]
		if b.Month == "2026-10" && b.Status == domain.BudgetOpen {
			succ = b
		}
	}
	if succ == nil || succ.CarriedCents != 0 {
		t.Errorf("successor = %+v; want carried 0 without conversion sources", succ)
	}
}

// TestBudgetCreateCarryOverDefaultsAndOptOut: new monthly rows default to
// carry ON (decision 3), an explicit false opts out, envelopes never carry.
func TestBudgetCreateCarryOverDefaultsAndOptOut(t *testing.T) {
	svc, _ := newTestBudgetService()
	monthly, err := svc.Create(context.Background(), BudgetInput{CategoryID: 1, AmountCents: 10_000, Cadence: domain.BudgetMonthly})
	if err != nil {
		t.Fatal(err)
	}
	if !monthly.CarryOver {
		t.Errorf("new monthly carry_over = %+v; want default ON", monthly)
	}
	off, err := svc.Create(context.Background(), BudgetInput{CategoryID: 1, AmountCents: 10_000, Cadence: domain.BudgetMonthly, CarryOver: boolPtr(false)})
	if err != nil {
		t.Fatal(err)
	}
	if off.CarryOver {
		t.Errorf("opted-out monthly carry_over = %+v; want false", off)
	}
	env, err := svc.Create(context.Background(), BudgetInput{CategoryID: 1, AmountCents: 10_000, CarryOver: boolPtr(true)})
	if err != nil {
		t.Fatal(err)
	}
	if env.CarryOver {
		t.Errorf("envelope carry_over = %+v; want false (envelopes never carry)", env)
	}
}

// TestBudgetUpdateTogglesCarryOver: an open monthly row's flag is editable,
// nil leaves it untouched, envelopes ignore it.
func TestBudgetUpdateTogglesCarryOver(t *testing.T) {
	svc, _ := newTestBudgetService()
	b, err := svc.Create(context.Background(), BudgetInput{CategoryID: 1, AmountCents: 10_000, Cadence: domain.BudgetMonthly, CarryOver: boolPtr(false)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Update(context.Background(), b.ID, 11_000, boolPtr(true)); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ := svc.budgets.GetByID(context.Background(), b.ID)
	if !got.CarryOver || got.AmountCents != 11_000 {
		t.Errorf("after update = %+v; want carry on, amount 11000", got)
	}
	if _, err := svc.Update(context.Background(), b.ID, 11_100, nil); err != nil {
		t.Fatalf("nil update: %v", err)
	}
	got, _ = svc.budgets.GetByID(context.Background(), b.ID)
	if !got.CarryOver {
		t.Errorf("nil carry_over update changed the flag; want untouched")
	}
}

func boolPtr(v bool) *bool { return &v }
