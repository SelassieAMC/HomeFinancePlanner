package service

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"sync"
	"testing"
	"time"

	"home-finance-planner/backend/internal/domain"
)

// fakeScheduleStore is an in-memory ScheduledActionStore mirroring the
// SQLite semantics the scheduler relies on: run creation is idempotent per
// (action, due_date), archived actions are excluded where the service
// promises (ListActions default, ListRuns join), and applied history exists
// to guard deletes.
type fakeScheduleStore struct {
	mu      sync.Mutex
	actions map[int64]domain.ScheduledAction
	runs    map[int64]domain.ScheduledActionRun
	next    int64
}

func newFakeScheduleStore() *fakeScheduleStore {
	return &fakeScheduleStore{
		actions: map[int64]domain.ScheduledAction{},
		runs:    map[int64]domain.ScheduledActionRun{},
		next:    1,
	}
}

func (f *fakeScheduleStore) ListActions(_ context.Context, includeArchived bool) ([]domain.ScheduledAction, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []domain.ScheduledAction{}
	for i := int64(1); i < f.next; i++ {
		a, ok := f.actions[i]
		if !ok || (a.Archived && !includeArchived) {
			continue
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (f *fakeScheduleStore) GetActionByID(_ context.Context, id int64) (domain.ScheduledAction, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.actions[id]
	if !ok {
		return domain.ScheduledAction{}, domain.ErrNotFound
	}
	return a, nil
}

func (f *fakeScheduleStore) CreateAction(_ context.Context, a domain.ScheduledAction) (domain.ScheduledAction, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a.ID = f.next
	f.next++
	a.CreatedAt = time.Unix(0, 0)
	a.UpdatedAt = time.Unix(0, 0)
	f.actions[a.ID] = a
	return a, nil
}

func (f *fakeScheduleStore) UpdateAction(_ context.Context, a domain.ScheduledAction) (domain.ScheduledAction, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.actions[a.ID]; !ok {
		return domain.ScheduledAction{}, domain.ErrNotFound
	}
	a.UpdatedAt = time.Unix(0, 0)
	f.actions[a.ID] = a
	return a, nil
}

func (f *fakeScheduleStore) DeleteAction(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.actions[id]; !ok {
		return domain.ErrNotFound
	}
	delete(f.actions, id)
	for rid, run := range f.runs {
		if run.ActionID == id {
			delete(f.runs, rid)
		}
	}
	return nil
}

func (f *fakeScheduleStore) ActiveActionsTargetingBag(_ context.Context, bagID int64) ([]domain.ScheduledAction, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []domain.ScheduledAction{}
	for _, a := range f.actions {
		if a.Archived {
			continue
		}
		if !a.Kind.Virtual() {
			continue
		}
		p := a.Payload
		hits := (p.BagID != nil && *p.BagID == bagID) ||
			(p.FromBagID != nil && *p.FromBagID == bagID) ||
			(p.ToBagID != nil && *p.ToBagID == bagID)
		if hits {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (f *fakeScheduleStore) HasAppliedRuns(_ context.Context, actionID int64) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, run := range f.runs {
		if run.ActionID == actionID && run.Status == domain.ScheduleApplied {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeScheduleStore) MaterializeRuns(_ context.Context, actionID int64, dueDates []string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var created int64
	for _, d := range dueDates {
		exists := false
		for _, run := range f.runs {
			if run.ActionID == actionID && run.DueDate == d {
				exists = true
				break
			}
		}
		if exists {
			continue
		}
		id := f.next
		f.next++
		f.runs[id] = domain.ScheduledActionRun{
			ID:       id,
			ActionID: actionID,
			DueDate:  d,
			Status:   domain.SchedulePending,
		}
		created++
	}
	return created, nil
}

func (f *fakeScheduleStore) DeletePendingRuns(_ context.Context, actionID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for rid, run := range f.runs {
		if run.ActionID == actionID && run.Status == domain.SchedulePending {
			delete(f.runs, rid)
		}
	}
	return nil
}

func (f *fakeScheduleStore) ListRuns(_ context.Context, flt domain.ScheduleRunFilters) ([]domain.ScheduleRunView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []domain.ScheduleRunView{}
	for _, run := range f.runs {
		a, ok := f.actions[run.ActionID]
		if !ok || a.Archived {
			continue // archived schedules are hidden everywhere
		}
		if flt.ActionID > 0 && run.ActionID != flt.ActionID {
			continue
		}
		if len(flt.Statuses) > 0 {
			match := false
			for _, st := range flt.Statuses {
				if run.Status == st {
					match = true
					break
				}
			}
			if !match {
				continue
			}
		}
		if flt.DueFrom != "" && run.DueDate < flt.DueFrom {
			continue
		}
		if flt.DueTo != "" && run.DueDate > flt.DueTo {
			continue
		}
		out = append(out, f.view(run, a))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DueDate < out[j].DueDate })
	return out, nil
}

func (f *fakeScheduleStore) GetRunByID(_ context.Context, id int64) (domain.ScheduleRunView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	run, ok := f.runs[id]
	if !ok {
		return domain.ScheduleRunView{}, domain.ErrNotFound
	}
	a, ok := f.actions[run.ActionID]
	if !ok {
		return domain.ScheduleRunView{}, domain.ErrNotFound
	}
	return f.view(run, a), nil
}

func (f *fakeScheduleStore) view(run domain.ScheduledActionRun, a domain.ScheduledAction) domain.ScheduleRunView {
	run.CreatedAt = time.Unix(0, 0)
	run.UpdatedAt = time.Unix(0, 0)
	return domain.ScheduleRunView{
		ScheduledActionRun: run,
		ActionName:         a.Name,
		Kind:               a.Kind,
		Schedule:           a.Payload,
	}
}

func (f *fakeScheduleStore) UpdateRunStatus(_ context.Context, runID int64, status domain.ScheduleRunStatus, resultType *string, resultID *int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	run, ok := f.runs[runID]
	if !ok {
		return domain.ErrNotFound
	}
	run.Status = status
	run.ResultType = resultType
	run.ResultID = resultID
	run.UpdatedAt = time.Unix(0, 0)
	f.runs[runID] = run
	return nil
}

func (f *fakeScheduleStore) ListRunsByStatus(_ context.Context, actionID int64, status domain.ScheduleRunStatus) ([]domain.ScheduledActionRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []domain.ScheduledActionRun{}
	for _, run := range f.runs {
		if run.ActionID != actionID || run.Status != status {
			continue
		}
		out = append(out, run)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DueDate < out[j].DueDate })
	return out, nil
}

// seed helpers -----------------------------------------------------------

func (f *fakeScheduleStore) countPending(actionID int64) int {
	count := 0
	for _, run := range f.runs {
		if run.ActionID == actionID && run.Status == domain.SchedulePending {
			count++
		}
	}
	return count
}

// scheduleHarness builds a ScheduledActionService over in-memory fakes:
// two accounts (id 1 Main EUR with a second USD account), one open bag on
// account 1, and a seeded category.
type scheduleHarness struct {
	svc       *ScheduledActionService
	schedules *fakeScheduleStore
	bagSvc    *BagService
	bagStore  *fakeBagStore
	bags      ScheduledActionStore
	txStore   *fakeTxStore
	accounts  *fakeAccountStore
	cats      *fakeCategoryStore
	ctx       context.Context
	today     string
}

func newScheduleHarness(t *testing.T) *scheduleHarness {
	t.Helper()
	accounts := newFakeAccountStore()
	ctx := context.Background()
	if _, err := accounts.Create(ctx, domain.Account{Name: "Main", Type: domain.AccountChecking, Currency: "EUR", OpeningBalanceCents: 100000}); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := accounts.Create(ctx, domain.Account{Name: "Cash", Type: domain.AccountCash, Currency: "USD", OpeningBalanceCents: 50000}); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	bagStore := newFakeBagStore()
	bagSvc := &BagService{bags: bagStore, accounts: accounts, log: discardLogger()}
	cats := &fakeCategoryStore{cats: map[int64]domain.Category{
		9: {ID: 9, Name: "Rent", Kind: "fixed"},
	}}
	txSvc := &TransactionService{
		transactions: newFakeTxStore(),
		accounts:     accounts,
		categories:   cats,
		log:          discardLogger(),
	}
	store := newFakeScheduleStore()
	bagSvc.schedules = store
	svc := &ScheduledActionService{
		actions:      store,
		bags:         bagSvc,
		transactions: txSvc,
		accounts:     accounts,
		categories:   cats,
		log:          discardLogger(),
	}
	return &scheduleHarness{
		svc:       svc,
		schedules: store,
		bagSvc:    bagSvc,
		bagStore:  bagStore,
		txStore:   txSvc.transactions.(*fakeTxStore),
		accounts:  accounts,
		cats:      cats,
		ctx:       ctx,
		today:     "2026-10-13",
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func (h *scheduleHarness) bag(t *testing.T, name string) domain.BagStatus {
	t.Helper()
	b, err := h.bagSvc.Create(h.ctx, BagInput{Name: name, AccountID: 1})
	if err != nil {
		t.Fatalf("seed bag %q: %v", name, err)
	}
	return b
}

// scheduleInput builds a valid bag_funding once input, the tests' baseline.
func scheduleFundingOnce(bagID, amount int64, date string) ScheduledActionInput {
	return ScheduledActionInput{
		Name:       "Warm expenses",
		Kind:       domain.ScheduleBagFunding,
		Payload:    domain.SchedulePayload{BagID: &bagID, AmountCents: amount},
		Recurrence: domain.ScheduleOnce,
		StartDate:  date,
	}
}

func TestScheduleCreateValidation(t *testing.T) {
	h := newScheduleHarness(t)
	h.bag(t, "Warm expenses")

	cases := []struct {
		name string
		in   func() ScheduledActionInput
		want string // substring of the expected error
	}{
		{"empty name", func() ScheduledActionInput {
			in := scheduleFundingOnce(1, 10000, "2026-10-20")
			in.Name = "  "
			return in
		}, "name is required"},
		{"bad kind", func() ScheduledActionInput {
			in := scheduleFundingOnce(1, 10000, "2026-10-20")
			in.Kind = "bag_robbery"
			return in
		}, "kind"},
		{"bad recurrence", func() ScheduledActionInput {
			in := scheduleFundingOnce(1, 10000, "2026-10-20")
			in.Recurrence = "weekly"
			return in
		}, "recurrence"},
		{"bad date", func() ScheduledActionInput { in := scheduleFundingOnce(1, 10000, "2026-02-31"); return in }, "not a real date"},
		{"once with day_of_month", func() ScheduledActionInput {
			in := scheduleFundingOnce(1, 10000, "2026-10-20")
			day := 5
			in.DayOfMonth = &day
			return in
		}, "day_of_month/month_of_year belong"},
		{"once too old", func() ScheduledActionInput { return scheduleFundingOnce(1, 10000, "2025-01-01") }, "never materialize"},
		{"monthly without day", func() ScheduledActionInput {
			in := scheduleFundingOnce(1, 10000, "2026-10-05")
			in.Recurrence = domain.ScheduleMonthly
			return in
		}, "monthly and yearly recurrences need day_of_month"},
		{"monthly day 31", func() ScheduledActionInput {
			in := scheduleFundingOnce(1, 10000, "2026-10-05")
			in.Recurrence = domain.ScheduleMonthly
			day := 31
			in.DayOfMonth = &day
			return in
		}, "day_of_month must be 1..28"},
		{"yearly without month", func() ScheduledActionInput {
			in := scheduleFundingOnce(1, 10000, "2026-10-05")
			in.Recurrence = domain.ScheduleYearly
			day := 5
			in.DayOfMonth = &day
			return in
		}, "month_of_year"},
		{"funding without bag", func() ScheduledActionInput {
			in := scheduleFundingOnce(1, 10000, "2026-10-20")
			in.Payload.BagID = nil
			return in
		}, "bag_id"},
		{"funding carries junk fields", func() ScheduledActionInput {
			in := scheduleFundingOnce(1, 10000, "2026-10-20")
			acc := int64(1)
			in.Payload.AccountID = &acc
			return in
		}, "takes exactly bag_id and amount_cents"},
		{"funding missing bag row", func() ScheduledActionInput { return scheduleFundingOnce(999, 10000, "2026-10-20") }, "not found"},
		{"zero amount", func() ScheduledActionInput { return scheduleFundingOnce(1, 0, "2026-10-20") }, "positive amount"},
		{"transfer same account", func() ScheduledActionInput {
			return ScheduledActionInput{
				Name:       "Rent",
				Kind:       domain.ScheduleAccountTransfer,
				Payload:    domain.SchedulePayload{AccountID: int64Ptr(1), CounterID: int64Ptr(1), AmountCents: 10000},
				Recurrence: domain.ScheduleMonthly,
				StartDate:  "2026-10-05",
				DayOfMonth: intPtr(5),
			}
		}, "source and destination accounts must differ"},
		{"transfer unknown account", func() ScheduledActionInput {
			return ScheduledActionInput{
				Name:       "Rent",
				Kind:       domain.ScheduleAccountTransfer,
				Payload:    domain.SchedulePayload{AccountID: int64Ptr(1), CounterID: int64Ptr(77), AmountCents: 10000},
				Recurrence: domain.ScheduleOnce,
				StartDate:  "2026-10-20",
			}
		}, "not found"},
		{"transaction kind transfer refused", func() ScheduledActionInput {
			return ScheduledActionInput{
				Name:       "Rent",
				Kind:       domain.ScheduleTransaction,
				Payload:    domain.SchedulePayload{AccountID: int64Ptr(1), TransactionKind: domain.TransactionTransfer, AmountCents: 10000},
				Recurrence: domain.ScheduleOnce,
				StartDate:  "2026-10-20",
			}
		}, "expense or income"},
		{"transaction unknown category", func() ScheduledActionInput {
			return ScheduledActionInput{
				Name:       "Rent",
				Kind:       domain.ScheduleTransaction,
				Payload:    domain.SchedulePayload{AccountID: int64Ptr(1), TransactionKind: domain.TransactionExpense, CategoryID: int64Ptr(55), AmountCents: 10000},
				Recurrence: domain.ScheduleOnce,
				StartDate:  "2026-10-20",
			}
		}, "not found"},
		{"purchase junk fields", func() ScheduledActionInput {
			return ScheduledActionInput{
				Name:       "New bike helmet",
				Kind:       domain.SchedulePurchase,
				Payload:    domain.SchedulePayload{Note: "check sizes", BagID: int64Ptr(1)},
				Recurrence: domain.ScheduleOnce,
				StartDate:  "2026-10-20",
			}
		}, "purchase payload takes exactly"},
	}

	for _, tc := range cases {
		_, err := h.svc.Create(h.ctx, h.today, tc.in())
		if err == nil {
			t.Errorf("%s: expected error %q, got none", tc.name, tc.want)
			continue
		}
		if !contains(err.Error(), tc.want) {
			t.Errorf("%s: error %q does not contain %q", tc.name, err.Error(), tc.want)
		}
	}

	// The happy path: a clean bag_funding once.
	created, err := h.svc.Create(h.ctx, h.today, scheduleFundingOnce(1, 10000, "2026-10-20"))
	if err != nil {
		t.Fatalf("create valid schedule: %v", err)
	}
	if created.ID == 0 || created.Archived {
		t.Fatalf("unexpected created row: %+v", created)
	}
}

func TestScheduleRebalanceValidation(t *testing.T) {
	h := newScheduleHarness(t)
	a := h.bag(t, "Alpha")
	b := h.bag(t, "Beta")

	valid := ScheduledActionInput{
		Name:       "Top up Beta",
		Kind:       domain.ScheduleBagRebalance,
		Payload:    domain.SchedulePayload{FromBagID: &a.ID, ToBagID: &b.ID, AmountCents: 5000},
		Recurrence: domain.ScheduleMonthly,
		StartDate:  "2026-10-05",
		DayOfMonth: intPtr(5),
	}
	if _, err := h.svc.Create(h.ctx, h.today, valid); err != nil {
		t.Fatalf("create valid rebalance: %v", err)
	}

	// Same-bag rebalance refused.
	same := valid
	same.Payload.ToBagID = &a.ID
	if _, err := h.svc.Create(h.ctx, h.today, same); err == nil || !contains(err.Error(), "onto itself") {
		t.Errorf("same-bag rebalance: expected self-refusal, got %v", err)
	}
}

func intPtr(v int) *int       { return &v }
func int64Ptr(v int64) *int64 { return &v }

func contains(hay, needle string) bool {
	return len(hay) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(hay); i++ {
			if hay[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}

func TestEnsureUpcomingMaterializesMonthly(t *testing.T) {
	h := newScheduleHarness(t)
	bag := h.bag(t, "Warm expenses")
	day := 25
	created, err := h.svc.Create(h.ctx, h.today, ScheduledActionInput{
		Name:       "Warm expenses",
		Kind:       domain.ScheduleBagFunding,
		Payload:    domain.SchedulePayload{BagID: &bag.ID, AmountCents: 15000},
		Recurrence: domain.ScheduleMonthly,
		StartDate:  "2026-10-25",
		DayOfMonth: &day,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := h.svc.EnsureUpcoming(h.ctx, h.today); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	// Window: start 2026-10-25 through today+2 months = 2026-12-13 → runs on
	// Oct 25 and Nov 25 (Dec 25 is past the horizon).
	runs, err := h.schedules.ListRuns(h.ctx, domain.ScheduleRunFilters{ActionID: created.ID})
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("expected 2 materialized runs, got %d: %+v", len(runs), runs)
	}
	if runs[0].DueDate != "2026-10-25" || runs[1].DueDate != "2026-11-25" {
		t.Errorf("unexpected due dates: %s, %s", runs[0].DueDate, runs[1].DueDate)
	}

	// The catch-up is idempotent.
	if err := h.svc.EnsureUpcoming(h.ctx, h.today); err != nil {
		t.Fatalf("ensure again: %v", err)
	}
	runs, _ = h.schedules.ListRuns(h.ctx, domain.ScheduleRunFilters{ActionID: created.ID})
	if len(runs) != 2 {
		t.Errorf("expected still 2 runs after re-run, got %d", len(runs))
	}
}

func TestEnsureUpcomingYearly(t *testing.T) {
	h := newScheduleHarness(t)
	day, month := 1, 11
	created, err := h.svc.Create(h.ctx, h.today, ScheduledActionInput{
		Name:        "Nebenkosten",
		Kind:        domain.ScheduleTransaction,
		Payload:     domain.SchedulePayload{AccountID: int64Ptr(1), TransactionKind: domain.TransactionExpense, AmountCents: 90000},
		Recurrence:  domain.ScheduleYearly,
		StartDate:   "2026-11-01",
		DayOfMonth:  &day,
		MonthOfYear: &month,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := h.svc.EnsureUpcoming(h.ctx, h.today); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	runs, _ := h.schedules.ListRuns(h.ctx, domain.ScheduleRunFilters{ActionID: created.ID})
	if len(runs) != 1 || runs[0].DueDate != "2026-11-01" {
		t.Fatalf("expected one yearly run on 2026-11-01, got %+v", runs)
	}
}

func TestEnsureUpcomingAutoAppliesFunding(t *testing.T) {
	h := newScheduleHarness(t)
	bag := h.bag(t, "Warm expenses")
	// Once, due 3 days ago: the catch-up writes the missed occurrence itself.
	created, err := h.svc.Create(h.ctx, h.today, scheduleFundingOnce(bag.ID, 15000, "2026-10-10"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := h.svc.EnsureUpcoming(h.ctx, h.today); err != nil {
		t.Fatalf("ensure: %v", err)
	}

	runs, _ := h.schedules.ListRuns(h.ctx, domain.ScheduleRunFilters{ActionID: created.ID})
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	run := runs[0]
	if run.Status != domain.ScheduleApplied {
		t.Fatalf("expected auto-applied run, got %s", run.Status)
	}
	if run.ResultType == nil || *run.ResultType != "allocation" || run.ResultID == nil {
		t.Fatalf("expected allocation result, got %+v", run)
	}

	// The allocation landed in the bag ledger dated at the due date.
	allocs, err := h.bagSvc.AllocationHistory(h.ctx, bag.ID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(allocs) != 1 || allocs[0].Date != "2026-10-10" || allocs[0].AmountCents != 15000 {
		t.Fatalf("unexpected ledger: %+v", allocs)
	}
	if allocs[0].Note != "Scheduled: Warm expenses" {
		t.Errorf("expected run-linked note, got %q", allocs[0].Note)
	}

	// And the balance reflects it.
	status, err := h.bagSvc.Get(h.ctx, bag.ID)
	if err != nil {
		t.Fatalf("bag status: %v", err)
	}
	if status.BalanceCents != 15000 {
		t.Errorf("expected balance 15000, got %d", status.BalanceCents)
	}
}

func TestEnsureUpcomingClosedBagStaysPending(t *testing.T) {
	h := newScheduleHarness(t)
	bag := h.bag(t, "Warm expenses")
	created, err := h.svc.Create(h.ctx, h.today, scheduleFundingOnce(bag.ID, 15000, "2026-10-10"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// The bag closes after the schedule exists (the create refuses a closed
	// bag): the catch-up must not fabricate a funding into a sealed reserve.
	if _, err := h.bagSvc.SetStatus(h.ctx, bag.ID, domain.BagClosed); err != nil {
		t.Fatalf("close bag: %v", err)
	}
	if err := h.svc.EnsureUpcoming(h.ctx, h.today); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	runs, _ := h.schedules.ListRuns(h.ctx, domain.ScheduleRunFilters{ActionID: created.ID})
	if len(runs) != 1 || runs[0].Status != domain.SchedulePending {
		t.Fatalf("expected the overdue run to stay pending, got %+v", runs)
	}
}

func TestUpdateRegeneratesPendingOnly(t *testing.T) {
	h := newScheduleHarness(t)
	bag := h.bag(t, "Warm expenses")
	day := 25
	created, err := h.svc.Create(h.ctx, h.today, ScheduledActionInput{
		Name:       "Warm expenses",
		Kind:       domain.ScheduleBagFunding,
		Payload:    domain.SchedulePayload{BagID: &bag.ID, AmountCents: 15000},
		Recurrence: domain.ScheduleMonthly,
		StartDate:  "2026-10-25",
		DayOfMonth: &day,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := h.svc.EnsureUpcoming(h.ctx, h.today); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	runs, _ := h.scheduledRuns(t, created.ID)
	if len(runs) != 2 {
		t.Fatalf("expected 2 pending runs, got %d", len(runs))
	}

	// The funding schedule above stays untouched. The real scenario: a
	// confirm-first schedule with pending runs — dismiss one, edit the day,
	// and watch the pending set regenerate while the dismissed row stays.
	txBag := ScheduledActionInput{
		Name:       "Rent",
		Kind:       domain.ScheduleAccountTransfer,
		Payload:    domain.SchedulePayload{AccountID: int64Ptr(1), CounterID: int64Ptr(2), AmountCents: 100000},
		Recurrence: domain.ScheduleMonthly,
		StartDate:  "2026-10-25",
		DayOfMonth: &day,
	}
	rent, err := h.svc.Create(h.ctx, h.today, txBag)
	if err != nil {
		t.Fatalf("create rent: %v", err)
	}
	if err := h.svc.EnsureUpcoming(h.ctx, h.today); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	rentRuns, _ := h.scheduledRuns(t, rent.ID)
	if len(rentRuns) != 2 {
		t.Fatalf("expected 2 pending rent runs, got %d", len(rentRuns))
	}
	if _, err := h.svc.DismissRun(h.ctx, rentRuns[0].ID); err != nil {
		t.Fatalf("dismiss: %v", err)
	}

	// Edit: new day (25 → 20) and a bigger amount.
	day20 := 20
	updated, err := h.svc.Update(h.ctx, rent.ID, h.today, ScheduledActionInput{
		Name:       "Rent",
		Kind:       domain.ScheduleAccountTransfer,
		Payload:    domain.SchedulePayload{AccountID: int64Ptr(1), CounterID: int64Ptr(2), AmountCents: 120000},
		Recurrence: domain.ScheduleMonthly,
		StartDate:  "2026-10-20",
		DayOfMonth: &day20,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Payload.AmountCents != 120000 {
		t.Fatalf("payload not updated: %+v", updated)
	}
	after, _ := h.scheduledRuns(t, rent.ID)
	pendingAfter := 0
	var dismissed domain.ScheduleRunStatus
	dueDates := map[string]domain.ScheduleRunStatus{}
	for _, run := range after {
		dueDates[run.DueDate] = run.Status
		if run.Status == domain.SchedulePending {
			pendingAfter++
		}
		if run.Status == domain.ScheduleDismissed {
			dismissed = run.Status
		}
	}
	// The old dismissed Oct-25 row stays; the new pending rows land on the
	// 20ths (Oct 20 is today → due now; Nov 20 ahead).
	if dueDates["2026-10-25"] != domain.ScheduleDismissed {
		t.Errorf("dismissed run did not survive the edit: %+v", dueDates)
	}
	if dueDates["2026-10-20"] != domain.SchedulePending || dueDates["2026-11-20"] != domain.SchedulePending {
		t.Errorf("pending runs not regenerated on the new day: %+v", dueDates)
	}
	if pendingAfter != 2 {
		t.Errorf("expected 2 pending runs after edit, got %d: %s", pendingAfter, dumpRuns(after))
	}
	if dismissed != domain.ScheduleDismissed {
		t.Errorf("dismissed row missing")
	}
}

// dumpRuns renders "due=status" facts per run for failure messages.
func dumpRuns(runs []domain.ScheduleRunView) string {
	parts := make([]string, 0, len(runs))
	for _, r := range runs {
		parts = append(parts, fmt.Sprintf("%s=%s", r.DueDate, r.Status))
	}
	return fmt.Sprint(parts)
}

func TestDeleteRefusesHistoryAndArchiveKeepsIt(t *testing.T) {
	h := newScheduleHarness(t)
	bag := h.bag(t, "Warm expenses")
	created, err := h.svc.Create(h.ctx, h.today, scheduleFundingOnce(bag.ID, 15000, "2026-10-10"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := h.svc.EnsureUpcoming(h.ctx, h.today); err != nil {
		t.Fatalf("ensure: %v", err)
	}

	// Applied history exists → plain delete refuses.
	if err := h.svc.Delete(h.ctx, created.ID); err == nil || !contains(err.Error(), "archive") {
		t.Errorf("delete with applied runs: expected archive refusal, got %v", err)
	}

	// Archive is the soft path: keeps the action + its applied run, drops
	// pending reminders, hides both from the reads.
	archived, err := h.svc.Archive(h.ctx, created.ID)
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if !archived.Archived {
		t.Fatalf("archive did not stick: %+v", archived)
	}
	list, err := h.svc.List(h.ctx, h.today)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("archived schedule must be hidden from the list, got %+v", list)
	}
	runs, err := h.svc.Runs(h.ctx, h.today, domain.ScheduleRunFilters{})
	if err != nil {
		t.Fatalf("runs: %v", err)
	}
	if len(runs) != 0 {
		t.Errorf("archived schedule's runs must be hidden, got %+v", runs)
	}
	// History stays in the store (the ledger row was the whole point).
	if len(h.schedules.runs) != 1 {
		t.Errorf("archive must keep the run history, got %d rows", len(h.schedules.runs))
	}

	// A never-applied schedule deletes cleanly.
	fresh, err := h.svc.Create(h.ctx, h.today, scheduleFundingOnce(bag.ID, 2000, "2026-10-20"))
	if err != nil {
		t.Fatalf("create fresh: %v", err)
	}
	if err := h.svc.Delete(h.ctx, fresh.ID); err != nil {
		t.Errorf("delete never-applied: %v", err)
	}
}

func TestApplyRunTransfer(t *testing.T) {
	h := newScheduleHarness(t)
	created, err := h.svc.Create(h.ctx, h.today, ScheduledActionInput{
		Name:       "Rent to cash",
		Kind:       domain.ScheduleAccountTransfer,
		Payload:    domain.SchedulePayload{AccountID: int64Ptr(1), CounterID: int64Ptr(2), AmountCents: 100000, Description: "monthly rent"},
		Recurrence: domain.ScheduleOnce,
		StartDate:  "2026-10-10",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := h.svc.EnsureUpcoming(h.ctx, h.today); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	runs, _ := h.scheduledRuns(t, created.ID)
	if len(runs) != 1 {
		t.Fatalf("expected 1 pending run, got %d", len(runs))
	}

	applied, err := h.svc.ApplyRun(h.ctx, runs[0].ID, h.today)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if applied.Status != domain.ScheduleApplied || applied.ResultType == nil || *applied.ResultType != "transaction" {
		t.Fatalf("unexpected applied view: %+v", applied)
	}

	// The transaction exists, transfer-shaped, dated at the due date.
	txs := allTxs(h.txStore)
	if len(txs) != 1 {
		t.Fatalf("expected 1 transaction, got %d", len(txs))
	}
	tx := txs[0]
	if tx.Kind != domain.TransactionTransfer || tx.AccountID != 1 || tx.CounterAccountID == nil || *tx.CounterAccountID != 2 {
		t.Fatalf("unexpected transfer shape: %+v", tx)
	}
	if tx.Date != "2026-10-10" || tx.AmountCents != 100000 || tx.Currency != "EUR" {
		t.Fatalf("unexpected transfer fields: %+v", tx)
	}
	if tx.Description != "monthly rent" {
		t.Errorf("expected payload description, got %q", tx.Description)
	}

	// Double-apply conflicts.
	if _, err := h.svc.ApplyRun(h.ctx, runs[0].ID, h.today); err == nil || !contains(err.Error(), "pending") {
		t.Errorf("re-apply: expected conflict, got %v", err)
	}
}

func TestApplyRunRefusesFuturePending(t *testing.T) {
	h := newScheduleHarness(t)
	created, err := h.svc.Create(h.ctx, h.today, ScheduledActionInput{
		Name:       "Rent to cash",
		Kind:       domain.ScheduleAccountTransfer,
		Payload:    domain.SchedulePayload{AccountID: int64Ptr(1), CounterID: int64Ptr(2), AmountCents: 100000},
		Recurrence: domain.ScheduleOnce,
		StartDate:  "2026-12-01",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// The catch-up materializes +2 months ahead → Dec 1 exists as pending.
	if err := h.svc.EnsureUpcoming(h.ctx, h.today); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	runs, _ := h.scheduledRuns(t, created.ID)
	if len(runs) != 1 || runs[0].Status != domain.SchedulePending {
		t.Fatalf("expected 1 pending future run, got %+v", runs)
	}
	if _, err := h.svc.ApplyRun(h.ctx, runs[0].ID, h.today); err == nil || !contains(err.Error(), "future") {
		t.Errorf("future apply: expected refusal, got %v", err)
	}
	// Skip is the legitimate answer to a not-yet-real occurrence — but only
	// once it is due; before it is due it stays nagging by design? No: skip
	// needs no date gate (the reminder list owns it). It just transitions.
	if _, err := h.svc.SkipRun(h.ctx, runs[0].ID); err != nil {
		t.Errorf("skip pending future run: %v", err)
	}
}

func TestApplyRunTransactionWithCategory(t *testing.T) {
	h := newScheduleHarness(t)
	created, err := h.svc.Create(h.ctx, h.today, ScheduledActionInput{
		Name: "Electricity bill",
		Kind: domain.ScheduleTransaction,
		Payload: domain.SchedulePayload{
			AccountID: int64Ptr(1), TransactionKind: domain.TransactionExpense,
			CategoryID: int64Ptr(9), AmountCents: 4500,
		},
		Recurrence: domain.ScheduleOnce,
		StartDate:  "2026-10-12",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := h.svc.EnsureUpcoming(h.ctx, h.today); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	// Confirm-first kind: the catch-up left it pending — a real balance
	// movement never fires without the user's click.
	runs, _ := h.scheduledRuns(t, created.ID)
	if len(runs) != 1 || runs[0].Status != domain.SchedulePending {
		t.Fatalf("expected pending run, got %+v", runs)
	}
	applied, err := h.svc.ApplyRun(h.ctx, runs[0].ID, h.today)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if applied.ResultType == nil || *applied.ResultType != "transaction" {
		t.Fatalf("unexpected applied view: %+v", applied)
	}
	txs := allTxs(h.txStore)
	if len(txs) != 1 {
		t.Fatalf("expected 1 transaction, got %d", len(txs))
	}
	tx := txs[0]
	if tx.Kind != domain.TransactionExpense || tx.CategoryID == nil || *tx.CategoryID != 9 {
		t.Fatalf("unexpected transaction: %+v", tx)
	}
	// Blank payload descriptions default to the schedule name.
	if tx.Description != "Electricity bill" {
		t.Errorf("description should default to the schedule name, got %q", tx.Description)
	}
}

func TestApplyRunPurchaseIsChecklistOnly(t *testing.T) {
	h := newScheduleHarness(t)
	est := int64(5000)
	created, err := h.svc.Create(h.ctx, h.today, ScheduledActionInput{
		Name:       "New bike helmet",
		Kind:       domain.SchedulePurchase,
		Payload:    domain.SchedulePayload{EstimatedCents: &est, Note: "check sizes first"},
		Recurrence: domain.ScheduleOnce,
		StartDate:  "2026-10-12",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := h.svc.EnsureUpcoming(h.ctx, h.today); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	runs, _ := h.scheduledRuns(t, created.ID)
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	applied, err := h.svc.ApplyRun(h.ctx, runs[0].ID, h.today)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	// Applied, but with no financial result: a purchase rides the checklist.
	if applied.ResultType != nil || applied.ResultID != nil {
		t.Errorf("purchase apply must write no result, got %+v", applied)
	}
	txs := allTxs(h.txStore)
	if len(txs) != 0 {
		t.Errorf("purchase apply must write no transaction, got %d", len(txs))
	}
}

func TestBagDeleteGuard(t *testing.T) {
	h := newScheduleHarness(t)
	bag := h.bag(t, "Warm expenses")
	created, err := h.svc.Create(h.ctx, h.today, scheduleFundingOnce(bag.ID, 15000, "2026-10-10"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := h.svc.EnsureUpcoming(h.ctx, h.today); err != nil {
		t.Fatalf("ensure: %v", err)
	}

	// The active schedule blocks the delete.
	if err := h.bagSvc.Delete(h.ctx, bag.ID); err == nil || !contains(err.Error(), "active schedule") {
		t.Errorf("delete targeted bag: expected schedule refusal, got %v", err)
	}
	// After archiving the schedule, the bag delete goes through.
	if _, err := h.svc.Archive(h.ctx, created.ID); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if err := h.bagSvc.Delete(h.ctx, bag.ID); err != nil {
		t.Errorf("delete untargeted bag: %v", err)
	}
}

func TestRunsFiltersAndOrdering(t *testing.T) {
	h := newScheduleHarness(t)
	bag := h.bag(t, "Warm expenses")
	_, err := h.svc.Create(h.ctx, h.today, scheduleFundingOnce(bag.ID, 15000, "2026-10-10"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	day := 25
	if _, err := h.svc.Create(h.ctx, h.today, ScheduledActionInput{
		Name:       "Rent",
		Kind:       domain.ScheduleAccountTransfer,
		Payload:    domain.SchedulePayload{AccountID: int64Ptr(1), CounterID: int64Ptr(2), AmountCents: 100000},
		Recurrence: domain.ScheduleMonthly,
		StartDate:  "2026-10-25",
		DayOfMonth: &day,
	}); err != nil {
		t.Fatalf("create rent: %v", err)
	}
	if err := h.svc.EnsureUpcoming(h.ctx, h.today); err != nil {
		t.Fatalf("ensure: %v", err)
	}

	// Due-ascending: the overdue funding run (Oct 10) sorts first.
	all, err := h.svc.Runs(h.ctx, h.today, domain.ScheduleRunFilters{Statuses: []domain.ScheduleRunStatus{domain.SchedulePending, domain.ScheduleApplied}})
	if err != nil {
		t.Fatalf("runs: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 runs (1 applied funding + 2 pending rent), got %d", len(all))
	}
	foundOverdue := false
	for i, run := range all {
		if i > 0 && run.DueDate < all[i-1].DueDate {
			t.Errorf("runs not due-ascending: %+v", all)
		}
		if run.DueDate == "2026-10-10" && run.Status == domain.ScheduleApplied {
			foundOverdue = true
		}
	}
	if !foundOverdue {
		t.Errorf("funding run missing from the feed")
	}

	// The to-do list: pending only, overdue first.
	pending, err := h.svc.Runs(h.ctx, h.today, domain.ScheduleRunFilters{Statuses: []domain.ScheduleRunStatus{domain.SchedulePending}})
	if err != nil {
		t.Fatalf("pending runs: %v", err)
	}
	if len(pending) != 2 || pending[0].DueDate != "2026-10-25" {
		t.Errorf("unexpected pending feed: %+v", pending)
	}

	// The dashboard window: next 7 days only.
	window, err := h.svc.Runs(h.ctx, h.today, domain.ScheduleRunFilters{
		Statuses: []domain.ScheduleRunStatus{domain.SchedulePending},
		DueFrom:  h.today,
		DueTo:    "2026-10-20",
	})
	if err != nil {
		t.Fatalf("window runs: %v", err)
	}
	if len(window) != 0 {
		t.Errorf("expected empty 7-day window (rent due Oct 25), got %+v", window)
	}
}

// scheduledRuns fetches one action's runs through the fake store.
func (h *scheduleHarness) scheduledRuns(t *testing.T, actionID int64) ([]domain.ScheduleRunView, error) {
	t.Helper()
	return h.schedules.ListRuns(h.ctx, domain.ScheduleRunFilters{ActionID: actionID})
}

// allTxs reads the fake transaction store's written rows (its List is a
// shared-package stub that returns nothing).
func allTxs(f *fakeTxStore) []domain.Transaction {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.Transaction, 0, len(f.items))
	for _, tx := range f.items {
		out = append(out, tx)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

var _ = fmt.Sprintf // keep fmt linked for future harness use
