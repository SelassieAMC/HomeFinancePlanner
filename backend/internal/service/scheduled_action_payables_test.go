package service

import (
	"context"
	"testing"

	"home-finance-planner/backend/internal/domain"
)

// The occurrences-register tests (Phase 6, month paging): projection over
// the recurrence rules, on-demand materialization of far months, status
// merge, direction filtering, currency grouping, owed/paid totals, and the
// early-payment semantics. `today` rides the harness (2026-10-13) — the
// same clock the materializer keys on.

// fakeBaseSource is a constant BaseCurrencySource for the purchase-estimate
// currency.
type fakeBaseSource struct{ currency string }

func (f fakeBaseSource) BaseCurrency(_ context.Context) (string, error) { return f.currency, nil }

// fixedBase wires a constant base currency into a fresh harness.
func payablesHarness(t *testing.T, base string) *scheduleHarness {
	t.Helper()
	h := newScheduleHarness(t)
	h.svc.base = fakeBaseSource{currency: base}
	return h
}

func seedPayable(t *testing.T, h *scheduleHarness, in ScheduledActionInput) domain.ScheduledAction {
	t.Helper()
	a, err := h.svc.Create(h.ctx, h.today, in)
	if err != nil {
		t.Fatalf("seed schedule %q: %v", in.Name, err)
	}
	return a
}

func findPayable(items []domain.Payable, actionID int64, due string) *domain.Payable {
	for i := range items {
		if items[i].ActionID == actionID && items[i].DueDate == due {
			return &items[i]
		}
	}
	return nil
}

func findPayableByName(items []domain.Payable, name string) *domain.Payable {
	for i := range items {
		if items[i].Name == name {
			return &items[i]
		}
	}
	return nil
}

func totalOf(t *testing.T, totals []domain.PayablesTotal, month, currency string) domain.PayablesTotal {
	t.Helper()
	for _, tot := range totals {
		if tot.Month == month && tot.Currency == currency {
			return tot
		}
	}
	t.Fatalf("no total for %s/%s in %+v", month, currency, totals)
	return domain.PayablesTotal{}
}

// All payables tests price transactions in account 1 (EUR); the harness
// seeds a second USD account (id 2).
func TestPayablesMonthViewMergesRunStatus(t *testing.T) {
	h := payablesHarness(t, "EUR")
	id := int64(9)
	acc := int64(1)
	// Monthly electricity on account 1 (EUR), due the 5th, started last month.
	a := seedPayable(t, h, ScheduledActionInput{
		Name:       "Electricity",
		Kind:       domain.ScheduleTransaction,
		Payload:    domain.SchedulePayload{AccountID: &acc, TransactionKind: domain.TransactionExpense, AmountCents: 8000, CategoryID: &id},
		Recurrence: domain.ScheduleMonthly,
		StartDate:  "2026-09-05",
	})

	// Default (blank) month = today's month.
	report, err := h.svc.Payables(h.ctx, h.today, "")
	if err != nil {
		t.Fatalf("Payables: %v", err)
	}
	if report.Month != "2026-10" {
		t.Fatalf("blank month must default to today's month, got %s", report.Month)
	}
	it := findPayable(report.Items, a.ID, "2026-10-05")
	if it == nil {
		t.Fatalf("missing October occurrence: %+v", report.Items)
	}
	if it.Status != domain.SchedulePending || it.RunID == nil {
		t.Fatalf("occurrence must merge the materialized pending run, got %+v", it)
	}
	if it.Currency != "EUR" || it.AmountCents != 8000 || !it.IsPayable {
		t.Fatalf("occurrence fields wrong: %+v", it)
	}
	if len(report.Items) != 1 {
		t.Fatalf("a month view shows exactly that month: %+v", report.Items)
	}
	if oct := totalOf(t, report.Totals, "2026-10", "EUR"); oct.OwedCents != 8000 || oct.PaidCents != 0 {
		t.Fatalf("october total: %+v", oct)
	}

	// A far-future month the lazy catch-up never reaches: navigating there
	// materializes its runs on demand — the occurrence shows up MERGED, so
	// it is actionable (apply early / dismiss / skip).
	far, err := h.svc.Payables(h.ctx, h.today, "2027-05")
	if err != nil {
		t.Fatalf("Payables far month: %v", err)
	}
	fit := findPayable(far.Items, a.ID, "2027-05-05")
	if fit == nil || fit.RunID == nil || fit.Status != domain.SchedulePending {
		t.Fatalf("far month must be materialized and actionable on demand: %+v", fit)
	}

	// A month before the schedule started is simply empty.
	early, err := h.svc.Payables(h.ctx, h.today, "2026-08")
	if err != nil {
		t.Fatalf("Payables early month: %v", err)
	}
	if len(early.Items) != 0 || len(early.Totals) != 0 {
		t.Fatalf("empty month must be empty: %+v", early.Items)
	}
}

func TestPayablesRefusesBadMonth(t *testing.T) {
	h := payablesHarness(t, "EUR")
	for _, bad := range []string{"2026-13", "2026-0", "october", "2026-3", "202603"} {
		if _, err := h.svc.Payables(h.ctx, h.today, bad); err == nil {
			t.Errorf("month %q must be refused", bad)
		}
	}
}

func TestPayablesOwedPaidAndAcknowledged(t *testing.T) {
	h := payablesHarness(t, "EUR")
	acc := int64(1)
	a := seedPayable(t, h, ScheduledActionInput{
		Name:       "Rent",
		Kind:       domain.ScheduleTransaction,
		Payload:    domain.SchedulePayload{AccountID: &acc, TransactionKind: domain.TransactionExpense, AmountCents: 98000},
		Recurrence: domain.ScheduleMonthly,
		StartDate:  "2026-09-01",
	})

	report, err := h.svc.Payables(h.ctx, h.today, "2026-10")
	if err != nil {
		t.Fatalf("Payables: %v", err)
	}
	// The 2026-10-05 occurrence is overdue-pending; apply it (writes the
	// expense transaction through the real path).
	octRun := findPayable(report.Items, a.ID, "2026-10-01")
	if octRun == nil || octRun.RunID == nil {
		t.Fatalf("overdue october occurrence must carry its run: %+v", octRun)
	}
	if _, err := h.svc.ApplyRun(h.ctx, *octRun.RunID, h.today); err != nil {
		t.Fatalf("apply October run: %v", err)
	}
	// Dismiss November's.
	after, err := h.svc.Payables(h.ctx, h.today, "2026-11")
	if err != nil {
		t.Fatalf("Payables November: %v", err)
	}
	novRun := findPayable(after.Items, a.ID, "2026-11-01")
	if novRun == nil || novRun.RunID == nil {
		t.Fatalf("november occurrence must be materialized: %+v", novRun)
	}
	if _, err := h.svc.DismissRun(h.ctx, *novRun.RunID); err != nil {
		t.Fatalf("dismiss November run: %v", err)
	}

	final, err := h.svc.Payables(h.ctx, h.today, "2026-10")
	if err != nil {
		t.Fatalf("Payables final: %v", err)
	}
	oct := findPayable(final.Items, a.ID, "2026-10-01")
	if oct.Status != domain.ScheduleApplied {
		t.Fatalf("october must read applied after apply, got %s", oct.Status)
	}
	tt := totalOf(t, final.Totals, "2026-10", "EUR")
	if tt.PaidCents != 98000 || tt.OwedCents != 0 {
		t.Fatalf("october total: paid 98000, got %+v", tt)
	}
	// The dismissed November occurrence enters neither column.
	nov, err := h.svc.Payables(h.ctx, h.today, "2026-11")
	if err != nil {
		t.Fatalf("Payables November after dismiss: %v", err)
	}
	if novItems := findPayable(nov.Items, a.ID, "2026-11-01"); novItems.Status != domain.ScheduleDismissed {
		t.Fatalf("november must read dismissed, got %s", novItems.Status)
	}
	if novT := totalOf(t, nov.Totals, "2026-11", "EUR"); novT.OwedCents != 0 || novT.PaidCents != 0 {
		t.Fatalf("a dismissed occurrence enters neither column: %+v", novT)
	}
}

func TestPayablesFiltersDirectionsAndKinds(t *testing.T) {
	h := payablesHarness(t, "EUR")
	acc := int64(1)

	// Income transaction — lists as an occurrence (the register shows every
	// movement) but is never payable: it enters no totals column.
	income := seedPayable(t, h, ScheduledActionInput{
		Name:       "Salary",
		Kind:       domain.ScheduleTransaction,
		Payload:    domain.SchedulePayload{AccountID: &acc, TransactionKind: domain.TransactionIncome, AmountCents: 500000},
		Recurrence: domain.ScheduleMonthly,
		StartDate:  "2026-10-10",
	})

	report, err := h.svc.Payables(h.ctx, h.today, "2026-10")
	if err != nil {
		t.Fatalf("Payables: %v", err)
	}
	it := findPayable(report.Items, income.ID, "2026-10-10")
	if it == nil {
		t.Fatalf("income occurrence must list in the register: %+v", report.Items)
	}
	if it.IsPayable {
		t.Fatalf("income must not be payable: %+v", it)
	}
	if len(report.Totals) != 0 {
		t.Fatalf("income must enter no totals column: %+v", report.Totals)
	}
}

func TestPayablesPurchaseEstimateAndVirtualExcluded(t *testing.T) {
	h := payablesHarness(t, "EUR")
	bag := h.bag(t, "Vacation")
	amount := int64(25000)
	// Virtual funding — lists, never payable, never summed.
	seedPayable(t, h, ScheduledActionInput{
		Name:       "Vacation fund",
		Kind:       domain.ScheduleBagFunding,
		Payload:    domain.SchedulePayload{BagID: &bag.ID, AmountCents: amount},
		Recurrence: domain.ScheduleOnce,
		StartDate:  h.today,
	})
	// Purchases: one with an estimate (priced in base currency), one without.
	est := int64(15000)
	seedPayable(t, h, ScheduledActionInput{
		Name:       "New bike",
		Kind:       domain.SchedulePurchase,
		Payload:    domain.SchedulePayload{EstimatedCents: &est, Note: "city bike"},
		Recurrence: domain.ScheduleOnce,
		StartDate:  "2026-10-20",
	})
	seedPayable(t, h, ScheduledActionInput{
		Name:       "Decide couch",
		Kind:       domain.SchedulePurchase,
		Payload:    domain.SchedulePayload{Note: "no estimate yet"},
		Recurrence: domain.ScheduleOnce,
		StartDate:  "2026-10-20",
	})

	report, err := h.svc.Payables(h.ctx, h.today, "2026-10")
	if err != nil {
		t.Fatalf("Payables: %v", err)
	}
	// The virtual funding lists as a register row (the system applies it
	// itself on the due date) but is never payable.
	fund := findPayableByName(report.Items, "Vacation fund")
	if fund == nil {
		t.Fatalf("virtual funding must list as a register row: %+v", report.Items)
	}
	if fund.IsPayable {
		t.Fatalf("virtual funding must not be payable: %+v", fund)
	}
	bike := findPayableByName(report.Items, "New bike")
	if bike == nil {
		t.Fatalf("purchase with estimate must list: %+v", report.Items)
	}
	// The occurrence is inside the materialization window, so its pending
	// run merges in (it is owed until the user ticks it done).
	if bike.Currency != "EUR" || bike.AmountCents != est || bike.Status != domain.SchedulePending {
		t.Fatalf("bike occurrence wrong: %+v", bike)
	}
	if !bike.IsPayable {
		t.Fatalf("an estimated purchase is payable: %+v", bike)
	}
	couch := findPayableByName(report.Items, "Decide couch")
	if couch == nil || couch.AmountCents != 0 || couch.RunID == nil {
		t.Fatalf("estimate-less purchase must still list as a row: %+v", couch)
	}
	if len(report.Totals) != 1 {
		t.Fatalf("totals must carry only the estimated purchase: %+v", report.Totals)
	}
	if totalOf(t, report.Totals, "2026-10", "EUR").OwedCents != est {
		t.Fatalf("totals must carry only the estimated purchase: %+v", report.Totals)
	}
}

func TestPayablesCurrencyGrouping(t *testing.T) {
	h := payablesHarness(t, "EUR")
	eur := int64(1)
	usd := int64(2)
	seedPayable(t, h, ScheduledActionInput{
		Name:       "Rent EUR",
		Kind:       domain.ScheduleTransaction,
		Payload:    domain.SchedulePayload{AccountID: &eur, TransactionKind: domain.TransactionExpense, AmountCents: 98000},
		Recurrence: domain.ScheduleMonthly,
		StartDate:  "2026-10-15",
	})
	seedPayable(t, h, ScheduledActionInput{
		Name:       "Netflix USD",
		Kind:       domain.ScheduleTransaction,
		Payload:    domain.SchedulePayload{AccountID: &usd, TransactionKind: domain.TransactionExpense, AmountCents: 1500},
		Recurrence: domain.ScheduleMonthly,
		StartDate:  "2026-10-15",
	})

	report, err := h.svc.Payables(h.ctx, h.today, "2026-10")
	if err != nil {
		t.Fatalf("Payables: %v", err)
	}
	tt := totalOf(t, report.Totals, "2026-10", "USD")
	if tt.OwedCents != 1500 {
		t.Fatalf("usd total: %+v", tt)
	}
	tt = totalOf(t, report.Totals, "2026-10", "EUR")
	if tt.OwedCents != 98000 {
		t.Fatalf("eur total: %+v", tt)
	}
}

func TestApplyRunEarlyPaymentRecordsToday(t *testing.T) {
	h := payablesHarness(t, "EUR")
	acc := int64(1)
	// Monthly expense whose first occurrence lands next month; viewing the
	// November register materializes its run on demand.
	a := seedPayable(t, h, ScheduledActionInput{
		Name:       "Rent",
		Kind:       domain.ScheduleTransaction,
		Payload:    domain.SchedulePayload{AccountID: &acc, TransactionKind: domain.TransactionExpense, AmountCents: 98000},
		Recurrence: domain.ScheduleMonthly,
		StartDate:  "2026-11-01",
	})
	report, err := h.svc.Payables(h.ctx, h.today, "2026-11")
	if err != nil {
		t.Fatalf("Payables November: %v", err)
	}
	future := findPayable(report.Items, a.ID, "2026-11-01")
	if future == nil || future.RunID == nil {
		t.Fatalf("november occurrence must be materialized: %+v", future)
	}

	// Paying before the due date records the movement at TODAY's date —
	// money that genuinely left now is never written dated ahead — while
	// the run keeps its plan due date.
	view, err := h.svc.ApplyRun(h.ctx, *future.RunID, h.today)
	if err != nil {
		t.Fatalf("early apply: %v", err)
	}
	if view.Status != domain.ScheduleApplied {
		t.Fatalf("early-applied run must be applied: %+v", view)
	}
	if len(h.txStore.items) != 1 {
		t.Fatalf("early apply must write exactly one transaction: %+v", h.txStore.items)
	}
	var written domain.Transaction
	for _, tx := range h.txStore.items {
		written = tx
	}
	if written.Date != h.today {
		t.Fatalf("early payment must date at today %s, got %s", h.today, written.Date)
	}
	if written.AccountID != acc || written.AmountCents != 98000 {
		t.Fatalf("wrong transaction written: %+v", written)
	}

	// The register shows the occurrence paid — in its plan month, so the
	// committed-month totals stay aligned with the schedule.
	after, err := h.svc.Payables(h.ctx, h.today, "2026-11")
	if err != nil {
		t.Fatalf("Payables after early apply: %v", err)
	}
	paid := findPayable(after.Items, a.ID, "2026-11-01")
	if paid.Status != domain.ScheduleApplied {
		t.Fatalf("occurrence must read applied: %+v", paid)
	}
	if nov := totalOf(t, after.Totals, "2026-11", "EUR"); nov.PaidCents != 98000 || nov.OwedCents != 0 {
		t.Fatalf("november totals: %+v", nov)
	}
}

func TestApplyRunRefusesEarlyOnVirtual(t *testing.T) {
	h := payablesHarness(t, "EUR")
	bag := h.bag(t, "Warm expenses")
	// Virtual funding due in the future — the system applies it itself on
	// the due date; an early manual apply is refused (bag ledger rows dated
	// ahead would read as reserved money that is not there yet).
	seedPayable(t, h, ScheduledActionInput{
		Name:       "Vacation funding",
		Kind:       domain.ScheduleBagFunding,
		Payload:    domain.SchedulePayload{BagID: &bag.ID, AmountCents: 30000},
		Recurrence: domain.ScheduleMonthly,
		StartDate:  "2026-11-20",
	})
	report, err := h.svc.Payables(h.ctx, h.today, "2026-11")
	if err != nil {
		t.Fatalf("Payables: %v", err)
	}
	future := findPayableByName(report.Items, "Vacation funding")
	if future == nil || future.RunID == nil {
		t.Fatalf("virtual occurrence must be materialized: %+v", future)
	}
	if _, err := h.svc.ApplyRun(h.ctx, *future.RunID, h.today); err == nil {
		t.Fatalf("virtual early apply must be refused")
	}
}

func TestPayablesMonthlyClampsShortMonth(t *testing.T) {
	h := payablesHarness(t, "EUR")
	acc := int64(1)
	// A plan started on the 31st: short months clamp to their LAST day
	// (2027-02-28), long months keep the 31st. The start date IS the
	// pattern — no explicit day selector exists anymore.
	a := seedPayable(t, h, ScheduledActionInput{
		Name:       "Rent",
		Kind:       domain.ScheduleTransaction,
		Payload:    domain.SchedulePayload{AccountID: &acc, TransactionKind: domain.TransactionExpense, AmountCents: 98000},
		Recurrence: domain.ScheduleMonthly,
		StartDate:  "2027-01-31",
	})
	// Day 31 exceeds the DB CHECK's storeable bound: NULL is stored and the
	// materializer derives the day from the start date.
	if a.DayOfMonth != nil {
		t.Fatalf("day 31 must store NULL (the CHECK bound), got %d", *a.DayOfMonth)
	}

	feb, err := h.svc.Payables(h.ctx, h.today, "2027-02")
	if err != nil {
		t.Fatalf("Payables February: %v", err)
	}
	if f := findPayable(feb.Items, a.ID, "2027-02-28"); f == nil {
		t.Fatalf("february must clamp to its last day: %+v", feb.Items)
	}
	mar, err := h.svc.Payables(h.ctx, h.today, "2027-03")
	if err != nil {
		t.Fatalf("Payables March: %v", err)
	}
	if m := findPayable(mar.Items, a.ID, "2027-03-31"); m == nil {
		t.Fatalf("march keeps the 31st: %+v", mar.Items)
	}
}

func TestScheduledExpenseWithOptionalBag(t *testing.T) {
	h := payablesHarness(t, "EUR")
	bag := h.bag(t, "Insurance reserve")
	acc := int64(1)
	// The manual expense form's "From bag", scheduled: expense on the
	// account, bag tag optional but validated (own account, open).
	in := ScheduledActionInput{
		Name:       "Insurance",
		Kind:       domain.ScheduleTransaction,
		Payload:    domain.SchedulePayload{AccountID: &acc, TransactionKind: domain.TransactionExpense, AmountCents: 50000, BagID: &bag.ID},
		Recurrence: domain.ScheduleOnce,
		StartDate:  "2026-10-25",
	}
	if _, err := h.svc.Create(h.ctx, h.today, in); err != nil {
		t.Fatalf("create with bag: %v", err)
	}
	report, err := h.svc.Payables(h.ctx, h.today, "2026-10")
	if err != nil {
		t.Fatalf("Payables: %v", err)
	}
	row := findPayableByName(report.Items, "Insurance")
	if row == nil || row.RunID == nil {
		t.Fatalf("occurrence must be materialized: %+v", row)
	}
	if _, err := h.svc.ApplyRun(h.ctx, *row.RunID, h.today); err != nil {
		t.Fatalf("apply: %v", err)
	}
	var written domain.Transaction
	for _, tx := range h.txStore.items {
		written = tx
	}
	if written.BagID == nil || *written.BagID != bag.ID {
		t.Fatalf("the applied expense must carry the bag tag: %+v", written)
	}

	// A bag of ANOTHER account is refused (the harness's second USD account
	// is id 2 — a bag can only be created there, so build one via direct
	// input).
	inBad := in
	otherBag := int64(0)
	inBad.Payload.BagID = &otherBag
	if _, err := h.svc.Create(h.ctx, h.today, inBad); err == nil {
		t.Fatalf("a nonexistent bag must be refused")
	}
	// An income never tags a bag.
	inIncome := in
	inIncome.Payload.TransactionKind = domain.TransactionIncome
	if _, err := h.svc.Create(h.ctx, h.today, inIncome); err == nil {
		t.Fatalf("income with bag_id must be refused")
	}
}

func TestAutoApplyExecutesRealMovement(t *testing.T) {
	h := payablesHarness(t, "EUR")
	acc := int64(1)
	counter := int64(2)
	auto := true
	// A REAL movement (transfer) flagged automatic: the catch-up applies its
	// due occurrences itself — the manual=off red alert never appears.
	a := seedPayable(t, h, ScheduledActionInput{
		Name:       "Rent to cash",
		Kind:       domain.ScheduleAccountTransfer,
		Payload:    domain.SchedulePayload{AccountID: &acc, CounterID: &counter, AmountCents: 100000},
		Recurrence: domain.ScheduleMonthly,
		StartDate:  "2026-09-25",
		AutoApply:  &auto,
	})
	if !a.AutoApply {
		t.Fatalf("auto flag must persist: %+v", a)
	}

	report, err := h.svc.Payables(h.ctx, h.today, "2026-10")
	if err != nil {
		t.Fatalf("Payables: %v", err)
	}
	if !report.Items[0].AutoApply {
		t.Fatalf("register rows must carry the plan's auto flag: %+v", report.Items[0])
	}
	// The overdue September run auto-applied: exactly one transaction,
	// dated at the Due DATE (the historical record), and the run applied.
	if len(h.txStore.items) != 1 {
		t.Fatalf("the auto flag must execute the overdue occurrence itself: %+v", h.txStore.items)
	}
	for _, tx := range h.txStore.items {
		if tx.Date != "2026-09-25" {
			t.Fatalf("auto-apply dates at the due date, got %s", tx.Date)
		}
	}
	if runs, _ := h.schedules.ListRuns(h.ctx, domain.ScheduleRunFilters{ActionID: a.ID}); runs[0].Status != domain.ScheduleApplied {
		t.Fatalf("september run must be applied: %+v", runs)
	}
	// Idempotent: re-reading does not re-execute.
	if _, err := h.svc.Payables(h.ctx, h.today, "2026-10"); err != nil {
		t.Fatalf("Payables re-read: %v", err)
	}
	if len(h.txStore.items) != 1 {
		t.Fatalf("a re-read must not double-execute: %+v", h.txStore.items)
	}
	// October's run is still pending (not yet due) — the reminder only
	// flips the moment its date arrives.
	octRuns, _ := h.schedules.ListRuns(h.ctx, domain.ScheduleRunFilters{ActionID: a.ID})
	for _, r := range octRuns {
		if r.DueDate == "2026-10-25" && r.Status != domain.SchedulePending {
			t.Fatalf("october must stay pending until due: %+v", octRuns)
		}
	}
}

func TestAutoApplyFlagSurvivesNilEdit(t *testing.T) {
	h := payablesHarness(t, "EUR")
	acc := int64(1)
	auto := true
	a := seedPayable(t, h, ScheduledActionInput{
		Name:       "Rent",
		Kind:       domain.ScheduleTransaction,
		Payload:    domain.SchedulePayload{AccountID: &acc, TransactionKind: domain.TransactionExpense, AmountCents: 98000},
		Recurrence: domain.ScheduleMonthly,
		StartDate:  "2026-10-05",
		AutoApply:  &auto,
	})
	// An edit that does not touch the flag keeps it (carry_over pattern).
	updated, err := h.svc.Update(h.ctx, a.ID, h.today, ScheduledActionInput{
		Name:       "Rent",
		Kind:       domain.ScheduleTransaction,
		Payload:    domain.SchedulePayload{AccountID: &acc, TransactionKind: domain.TransactionExpense, AmountCents: 99000},
		Recurrence: domain.ScheduleMonthly,
		StartDate:  "2026-10-05",
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !updated.AutoApply {
		t.Fatalf("a nil-flag edit must keep the stored mode: %+v", updated)
	}
	// And an explicit false turns it back to manual.
	manual := false
	updated, err = h.svc.Update(h.ctx, a.ID, h.today, ScheduledActionInput{
		Name:       "Rent",
		Kind:       domain.ScheduleTransaction,
		Payload:    domain.SchedulePayload{AccountID: &acc, TransactionKind: domain.TransactionExpense, AmountCents: 99000},
		Recurrence: domain.ScheduleMonthly,
		StartDate:  "2026-10-05",
		AutoApply:  &manual,
	})
	if err != nil {
		t.Fatalf("update manual: %v", err)
	}
	if updated.AutoApply {
		t.Fatalf("an explicit false must switch back to manual: %+v", updated)
	}
}
