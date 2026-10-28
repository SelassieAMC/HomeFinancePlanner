package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"home-finance-planner/backend/internal/domain"
)

// ScheduledActionService implements the unified scheduler: one engine for
// every planned money movement, in five kinds split by the auto-apply
// boundary. Virtual kinds (bag_funding, bag_rebalance) auto-apply on their
// due date — they write only into the bag allocation ledger and never touch
// a stored account balance, so the system may execute them itself. Real
// kinds (account_transfer, transaction) and purchases are confirm-first: a
// reminder until one user click applies them, because a balance the real
// bank has not confirmed would falsify the numbers the net-worth tile trusts.
//
// Runs are materialized lazily (EnsureUpcoming): a read creates the run rows
// history has arrived for (bounded lookback, up to +2 months ahead) and
// auto-applies virtual runs due today or overdue — history is created as
// facts, never virtual, the same decision the monthly budgets made.
type ScheduledActionService struct {
	actions ScheduledActionStore
	// bags executes the virtual kinds (BagService's closed-bag and balance
	// rules apply to a scheduled movement like to any manual one) and hosts
	// the bag lookups of payload validation.
	bags *BagService
	// transactions executes the confirm-first kinds on apply.
	transactions *TransactionService
	// accounts/categories re-validate payload references at schedule time —
	// a typo fails at create instead of silently at apply.
	accounts   AccountStore
	categories CategoryStore
	// base is the base-currency source for the payables projection
	// (purchase estimates price in the base currency).
	base BaseCurrencySource
	log  *slog.Logger
}

// lookbackDays bounds the lazy catch-up: runs are materialized for due dates
// no further than this many days in the past — overdue reminders nag for a
// month, then silently lapse (the user archived or dropped the plan's noise;
// materializing a two-year-old funding as "history that never happened" would
// fabricate facts).
const lookbackDays = 31

// aheadMonths bounds the forward materialization: the catch-up generates up
// to this many months of upcoming runs — enough for the dashboard "Coming up"
// card and the schedules list to show the next occurrences without a cron.
const aheadMonths = 2

// ScheduledActionInput is the user-facing payload for create/update. The
// payload is kind-switched; fields meaningless for the kind are refused so a
// silently-ignored field never hides a mistake.
type ScheduledActionInput struct {
	Name        string
	Kind        domain.ScheduleKind
	Payload     domain.SchedulePayload
	Recurrence  domain.ScheduleRecurrence
	StartDate   string // YYYY-MM-DD; the first occurrence for once…
	DayOfMonth  *int   // DERIVED from StartDate — a client-sent one is refused
	MonthOfYear *int   // 1..12; required for yearly
	// AutoApply is the execution mode (0046): nil on an update keeps the
	// stored value; a create without it means manual (the alert behavior).
	AutoApply *bool
}

// ScheduleFilters narrows run-list reads (re-exported from the domain).
type ScheduleRunListFilters = domain.ScheduleRunFilters

// List returns the active (non-archived) schedules after running the lazy
// catch-up — reading the list is what keeps it honest.
func (s *ScheduledActionService) List(ctx context.Context, today string) ([]domain.ScheduledAction, error) {
	if err := s.EnsureUpcoming(ctx, today); err != nil {
		return nil, err
	}
	return s.actions.ListActions(ctx, false)
}

// Runs returns materialized occurrences filtered for the to-do list and the
// dashboard "Coming up" card, due-date ascending (overdue pending runs first).
func (s *ScheduledActionService) Runs(ctx context.Context, today string, f ScheduleRunListFilters) ([]domain.ScheduleRunView, error) {
	if err := validateDate(today, "today"); err != nil {
		return nil, err
	}
	if f.DueFrom != "" {
		if err := validateDate(f.DueFrom, "due_from"); err != nil {
			return nil, err
		}
	}
	if f.DueTo != "" {
		if err := validateDate(f.DueTo, "due_to"); err != nil {
			return nil, err
		}
	}
	for _, st := range f.Statuses {
		if !st.Valid() {
			return nil, validationError("status %q must be one of pending, applied, dismissed, skipped", st)
		}
	}
	if err := s.EnsureUpcoming(ctx, today); err != nil {
		return nil, err
	}
	return s.actions.ListRuns(ctx, f)
}

// Create validates and stores a new schedule.
// Create validates and stores a schedule. today comes from the handler
// (server clock): the once-recurrence's lookback bound is tested against it,
// keeping the service deterministic like every other read.
func (s *ScheduledActionService) Create(ctx context.Context, today string, in ScheduledActionInput) (domain.ScheduledAction, error) {
	if err := validateDate(today, "today"); err != nil {
		return domain.ScheduledAction{}, err
	}
	a, err := s.build(ctx, domain.ScheduledAction{}, in, today)
	if err != nil {
		return domain.ScheduledAction{}, err
	}
	return s.actions.CreateAction(ctx, a)
}

// Update reshapes a schedule. Future occurrences only: still-pending runs are
// deleted and regenerated from the new definition; applied runs and their
// materialized effects (allocations, transactions) are never rewritten, and
// dismissed/skipped acknowledgements stay on their dates (their (action,
// due_date) uniqueness blocks a regenerated pending run from overwriting
// them). Payloads a run already consumed re-validate at apply time, so a
// deleted bag or account surfaces there with an actionable message.
// Update reshapes a schedule. Future occurrences only: still-pending runs are
// deleted and regenerated from the new definition; applied runs and their
// materialized effects (allocations, transactions) are never rewritten, and
// dismissed/skipped acknowledgements stay on their dates (their (action,
// due_date) uniqueness blocks a regenerated pending run from overwriting
// them). Payloads a run already consumed re-validate at apply time, so a
// deleted bag or account surfaces there with an actionable message. today
// comes from the handler (server clock like List) — the regenerated window is
// pinned to the same clock the reads use.
func (s *ScheduledActionService) Update(ctx context.Context, id int64, today string, in ScheduledActionInput) (domain.ScheduledAction, error) {
	if err := validateDate(today, "today"); err != nil {
		return domain.ScheduledAction{}, err
	}
	existing, err := s.actions.GetActionByID(ctx, id)
	if err != nil {
		return domain.ScheduledAction{}, err
	}
	if existing.Archived {
		return domain.ScheduledAction{}, conflictError(
			"schedule %q is archived — archived schedules keep their history and take no edits", existing.Name)
	}
	a, err := s.build(ctx, existing, in, today)
	if err != nil {
		return domain.ScheduledAction{}, err
	}
	a.ID = id
	updated, err := s.actions.UpdateAction(ctx, a)
	if err != nil {
		return domain.ScheduledAction{}, err
	}
	// Regenerate future occurrences: drop the pending reminders of the old
	// shape, then materialize the new one up to the same horizon.
	if err := s.actions.DeletePendingRuns(ctx, id); err != nil {
		return domain.ScheduledAction{}, err
	}
	dates := dueDates(updated, today, s.horizon(today))
	if _, err := s.actions.MaterializeRuns(ctx, id, dates); err != nil {
		return domain.ScheduledAction{}, err
	}
	return updated, nil
}

// Archive is the soft stop: the schedule disappears everywhere but keeps its
// history (applied runs and their materialized effects). Its still-pending
// reminders go — an archived plan has no future occurrences.
func (s *ScheduledActionService) Archive(ctx context.Context, id int64) (domain.ScheduledAction, error) {
	existing, err := s.actions.GetActionByID(ctx, id)
	if err != nil {
		return domain.ScheduledAction{}, err
	}
	if existing.Archived {
		return existing, nil
	}
	existing.Archived = true
	updated, err := s.actions.UpdateAction(ctx, existing)
	if err != nil {
		return domain.ScheduledAction{}, err
	}
	if err := s.actions.DeletePendingRuns(ctx, id); err != nil {
		return domain.ScheduledAction{}, err
	}
	return updated, nil
}

// Delete removes a schedule outright, applied history or not (user decision
// 2026-10-28: a deleted schedule keeps no history). The run rows cascade and
// the movements already written stay as ordinary transactions — that is where
// the money lives, and the ledger never loses facts. Archive remains the soft
// alternative when the plan's history should stay viewable.
func (s *ScheduledActionService) Delete(ctx context.Context, id int64) error {
	return s.actions.DeleteAction(ctx, id)
}

// ApplyRun executes a confirm-first occurrence after one user click. Virtual
// kinds don't route through here from the API (they auto-applied), but the
// service uses the same path internally so the validation stays one code
// path. The run must be pending. A future occurrence (MoneyWiz semantics:
// the due date is a deadline, not a gate) applies EARLY and the movement
// records at TODAY's date — the paid-before-due money leaves now, and
// budgets/analytics count the spend in the month it actually happened; the
// run keeps its due date (occurrence identity and audit stay on the plan).
// A virtual kind cannot apply early — it auto-applies itself exactly on its
// due date, and bag-fund ledger rows dated ahead would read as reserved
// money that is not there yet.
func (s *ScheduledActionService) ApplyRun(ctx context.Context, runID int64, today string) (domain.ScheduleRunView, error) {
	run, action, err := s.loadRun(ctx, runID)
	if err != nil {
		return domain.ScheduleRunView{}, err
	}
	if err := validateDate(today, "today"); err != nil {
		return domain.ScheduleRunView{}, err
	}
	if run.Status != domain.SchedulePending {
		return domain.ScheduleRunView{}, conflictError(
			"run %d of %q is %s — only a pending occurrence can be applied", run.ID, action.Name, run.Status)
	}
	if run.DueDate > today && action.Kind.Virtual() {
		return domain.ScheduleRunView{}, validationError(
			"run %d is due %s — a virtual occurrence applies itself on its due date and cannot be applied early", run.ID, run.DueDate)
	}
	view, err := s.executeRun(ctx, run, action, today)
	if err != nil {
		return domain.ScheduleRunView{}, err
	}
	return view, nil
}

// DismissRun acknowledges a pending occurrence: seen, nothing to do.
func (s *ScheduledActionService) DismissRun(ctx context.Context, runID int64) (domain.ScheduleRunView, error) {
	return s.transitionRun(ctx, runID, domain.ScheduleDismissed)
}

// SkipRun marks a pending occurrence not applicable this time (e.g. skipped
// a rent month). The run row stays — silence is not history.
func (s *ScheduledActionService) SkipRun(ctx context.Context, runID int64) (domain.ScheduleRunView, error) {
	return s.transitionRun(ctx, runID, domain.ScheduleSkipped)
}

// transitionRun moves a pending run to dismissed/skipped and returns its view.
func (s *ScheduledActionService) transitionRun(ctx context.Context, runID int64, status domain.ScheduleRunStatus) (domain.ScheduleRunView, error) {
	run, action, err := s.loadRun(ctx, runID)
	if err != nil {
		return domain.ScheduleRunView{}, err
	}
	if run.Status != domain.SchedulePending {
		return domain.ScheduleRunView{}, conflictError(
			"run %d of %q is %s — only a pending occurrence can be %s", run.ID, action.Name, run.Status, status)
	}
	if err := s.actions.UpdateRunStatus(ctx, runID, status, nil, nil); err != nil {
		return domain.ScheduleRunView{}, err
	}
	view, _, err := s.loadRun(ctx, runID)
	return view, err
}

// loadRun fetches one run with its action.
func (s *ScheduledActionService) loadRun(ctx context.Context, runID int64) (domain.ScheduleRunView, domain.ScheduledAction, error) {
	run, err := s.actions.GetRunByID(ctx, runID)
	if err != nil {
		return domain.ScheduleRunView{}, domain.ScheduledAction{}, err
	}
	action, err := s.actions.GetActionByID(ctx, run.ActionID)
	if err != nil {
		return domain.ScheduleRunView{}, domain.ScheduledAction{}, fmt.Errorf("load action of run %d: %w", runID, err)
	}
	return run, action, nil
}

// executeRun applies a pending run by kind and stamps the result. The kind
// switch is where the auto-apply boundary lives in code: bag kinds write
// allocations through BagService (its closed-bag and balance rules apply like
// any manual movement), real kinds write transactions through
// TransactionService, purchases only tick the checklist off. today bounds the
// movement date: an occurrence applies at min(its due date, today) — on or
// after the due date the movement dates at the due date (the plan's date,
// which history honors), before it the movement dates at today (an early
// payment left today, never ahead of it).
func (s *ScheduledActionService) executeRun(ctx context.Context, run domain.ScheduleRunView, action domain.ScheduledAction, today string) (domain.ScheduleRunView, error) {
	p := action.Payload
	note := "Scheduled: " + action.Name
	effectiveDate := run.DueDate
	if run.DueDate > today {
		effectiveDate = today
	}

	var (
		resultType string
		resultID   int64
	)
	switch action.Kind {
	case domain.ScheduleBagFunding:
		alloc, err := s.bags.Allocate(ctx, *p.BagID, domain.BagAllocationInput{
			Kind:        domain.BagFund,
			AmountCents: p.AmountCents,
			Note:        note,
			Date:        effectiveDate,
		})
		if err != nil {
			return domain.ScheduleRunView{}, fmt.Errorf("apply scheduling funding for %q: %w", action.Name, err)
		}
		resultType, resultID = "allocation", alloc.ID

	case domain.ScheduleBagRebalance:
		_, dst, err := s.bags.Rebalance(ctx, RebalanceInput{
			FromBagID:   *p.FromBagID,
			ToBagID:     *p.ToBagID,
			AmountCents: p.AmountCents,
			Note:        note,
			Date:        effectiveDate,
		})
		if err != nil {
			return domain.ScheduleRunView{}, fmt.Errorf("apply scheduled rebalance for %q: %w", action.Name, err)
		}
		resultType, resultID = "allocation", dst.ID

	case domain.ScheduleAccountTransfer:
		t, err := s.transactions.Create(ctx, TransactionInput{
			AccountID:        *p.AccountID,
			Kind:             domain.TransactionTransfer,
			AmountCents:      p.AmountCents,
			Description:      descriptionOrDefault(p.Description, action.Name),
			Date:             effectiveDate,
			CounterAccountID: p.CounterID,
		})
		if err != nil {
			// A deleted account, a stale bag, a currency change: the payload
			// re-validates at apply time and fails with an actionable message
			// instead of corrupting anything.
			return domain.ScheduleRunView{}, fmt.Errorf("apply scheduled transfer %q: %w", action.Name, err)
		}
		resultType, resultID = "transaction", t.ID

	case domain.ScheduleTransaction:
		t, err := s.transactions.Create(ctx, TransactionInput{
			AccountID:   *p.AccountID,
			Kind:        p.TransactionKind,
			CategoryID:  p.CategoryID,
			AmountCents: p.AmountCents,
			Description: descriptionOrDefault(p.Description, action.Name),
			Date:        effectiveDate,
			BagID:       p.BagID, // optional bag tag (expense), validated at apply
		})
		if err != nil {
			return domain.ScheduleRunView{}, fmt.Errorf("apply scheduled transaction %q: %w", action.Name, err)
		}
		resultType, resultID = "transaction", t.ID

	case domain.SchedulePurchase:
		// A purchase is a checklist item: applying it records only that it is
		// done — no financial effect until the purchase is actually tagged.
		// The UI guides the user into the purchase flow for the real record.

	default:
		return domain.ScheduleRunView{}, validationError("unknown schedule kind %q", action.Kind)
	}

	var rType *string
	var rid *int64
	if resultType != "" {
		v := resultType
		rType = &v
		id := resultID
		rid = &id
	}
	if err := s.actions.UpdateRunStatus(ctx, run.ID, domain.ScheduleApplied, rType, rid); err != nil {
		return domain.ScheduleRunView{}, err
	}
	view, _, err := s.loadRun(ctx, run.ID)
	return view, err
}

// descriptionOrDefault fills a run's transaction description from the
// schedule name when the payload left it blank.
func descriptionOrDefault(desc, name string) string {
	if strings.TrimSpace(desc) == "" {
		return name
	}
	return desc
}

// build validates an action payload over the existing row (zero for create).
// Kind-field mismatches are refused so an ignored field never hides a mistake.
func (s *ScheduledActionService) build(ctx context.Context, existing domain.ScheduledAction, in ScheduledActionInput, today string) (domain.ScheduledAction, error) {
	if err := validateRequiredString(strings.TrimSpace(in.Name), "name", 100); err != nil {
		return domain.ScheduledAction{}, err
	}
	if !in.Kind.Valid() {
		return domain.ScheduledAction{}, validationError(
			"kind %q must be one of bag_funding, bag_rebalance, account_transfer, transaction, purchase", in.Kind)
	}
	if !in.Recurrence.Valid() {
		return domain.ScheduledAction{}, validationError("recurrence %q must be one of once, monthly, yearly", in.Recurrence)
	}
	if err := validateDate(in.StartDate, "start_date"); err != nil {
		return domain.ScheduledAction{}, err
	}

	// Recurrence field discipline. The day-of-month is DERIVED from the
	// start date (user decision 2026-10-28 — the start date IS the pattern:
	// it repeats on that day, clamped to the month's last day when a month
	// is shorter), never taken from the client — a silently-ignored field
	// would hide mistakes, so an explicit one is refused.
	startT, err := timeParseDate(in.StartDate)
	if err != nil {
		return domain.ScheduledAction{}, err
	}
	day, month := in.DayOfMonth, in.MonthOfYear
	switch in.Recurrence {
	case domain.ScheduleOnce:
		if day != nil || month != nil {
			return domain.ScheduledAction{}, validationError("day_of_month/month_of_year belong to monthly and yearly recurrences, not once")
		}
		// A once-schedule more than the lookback window in the past would
		// never materialize a run (the catch-up never touches it) — refuse the
		// input instead of storing a plan that silently does nothing.
		bound := timeParseDateOr(today).AddDate(0, 0, -lookbackDays).Format("2006-01-02")
		if in.StartDate < bound {
			return domain.ScheduledAction{}, validationError(
				"start_date %s is more than %d days in the past — a one-off that old would never materialize", in.StartDate, lookbackDays)
		}
	case domain.ScheduleMonthly:
		if day != nil {
			return domain.ScheduledAction{}, validationError(
				"day_of_month is derived from start_date (day %d) — send only the start_date", startT.Day())
		}
		if month != nil {
			return domain.ScheduledAction{}, validationError("month_of_year belongs to yearly recurrences, not monthly")
		}
	case domain.ScheduleYearly:
		if month == nil || *month < 1 || *month > 12 {
			return domain.ScheduledAction{}, validationError("yearly recurrence needs month_of_year 1..12")
		}
		if day != nil {
			return domain.ScheduledAction{}, validationError(
				"day_of_month is derived from start_date (day %d) — send only the start_date", startT.Day())
		}
	}
	// The derivation: a repeating schedule rides the start date's day. Days
	// beyond 28 store NULL — migration 0037's DB CHECK keeps the 1..28 bound
	// (never edit an applied migration), and `dueDatesRange` derives the day
	// from the start date whenever the column is NULL, so a day-31 plan
	// clamps to each month's last day and still round-trips: the stored
	// field is a cache, the start date is the pattern.
	if in.Recurrence != domain.ScheduleOnce {
		if d := startT.Day(); d <= 28 {
			day = &d
		} else {
			day = nil
		}
	}

	// Kind-payload validation — positive amounts, real referenced rows.
	p := in.Payload
	switch in.Kind {
	case domain.ScheduleBagFunding:
		if p.FromBagID != nil || p.ToBagID != nil || p.AccountID != nil || p.CounterID != nil ||
			p.TransactionKind != "" || p.CategoryID != nil || p.EstimatedCents != nil || p.Description != "" || p.Note != "" {
			return domain.ScheduledAction{}, validationError("bag_funding payload takes exactly bag_id and amount_cents")
		}
		if p.BagID == nil {
			return domain.ScheduledAction{}, validationError("bag_funding needs a bag_id")
		}
		bag, err := s.bags.Get(ctx, *p.BagID)
		if err != nil {
			return domain.ScheduledAction{}, fmt.Errorf("validate bag_id: %w", err)
		}
		if existing.ID == 0 && bag.Status == domain.BagClosed {
			return domain.ScheduledAction{}, validationError("bag %q is closed — reopen it before scheduling funding", bag.Name)
		}
		if err := validatePositiveCents(p.AmountCents, "amount_cents"); err != nil {
			return domain.ScheduledAction{}, err
		}

	case domain.ScheduleBagRebalance:
		if p.BagID != nil || p.AccountID != nil || p.CounterID != nil ||
			p.TransactionKind != "" || p.CategoryID != nil || p.EstimatedCents != nil || p.Description != "" || p.Note != "" {
			return domain.ScheduledAction{}, validationError("bag_rebalance payload takes exactly from_bag_id, to_bag_id and amount_cents")
		}
		if p.FromBagID == nil || p.ToBagID == nil {
			return domain.ScheduledAction{}, validationError("bag_rebalance needs from_bag_id and to_bag_id")
		}
		from, err := s.bags.Get(ctx, *p.FromBagID)
		if err != nil {
			return domain.ScheduledAction{}, fmt.Errorf("validate from_bag_id: %w", err)
		}
		to, err := s.bags.Get(ctx, *p.ToBagID)
		if err != nil {
			return domain.ScheduledAction{}, fmt.Errorf("validate to_bag_id: %w", err)
		}
		if from.ID == to.ID {
			return domain.ScheduledAction{}, validationError("a bag cannot rebalance onto itself")
		}
		if from.AccountID != to.AccountID {
			return domain.ScheduledAction{}, validationError(
				"bags %q and %q live on different accounts — schedule a transfer for cross-account moves", from.Name, to.Name)
		}
		if err := validatePositiveCents(p.AmountCents, "amount_cents"); err != nil {
			return domain.ScheduledAction{}, err
		}

	case domain.ScheduleAccountTransfer:
		if p.BagID != nil || p.FromBagID != nil || p.ToBagID != nil ||
			p.TransactionKind != "" || p.CategoryID != nil || p.EstimatedCents != nil || p.Note != "" {
			return domain.ScheduledAction{}, validationError("account_transfer payload takes exactly account_id, counter_account_id, amount_cents and an optional description")
		}
		if p.AccountID == nil || p.CounterID == nil {
			return domain.ScheduledAction{}, validationError("account_transfer needs account_id (source) and counter_account_id (destination)")
		}
		if err := s.validateTransactionAccount(ctx, *p.AccountID); err != nil {
			return domain.ScheduledAction{}, err
		}
		if err := s.validateTransactionAccount(ctx, *p.CounterID); err != nil {
			return domain.ScheduledAction{}, err
		}
		if *p.AccountID == *p.CounterID {
			return domain.ScheduledAction{}, validationError("source and destination accounts must differ")
		}
		if err := validatePositiveCents(p.AmountCents, "amount_cents"); err != nil {
			return domain.ScheduledAction{}, err
		}
		if len(p.Description) > 500 {
			return domain.ScheduledAction{}, validationError("description must be at most 500 characters")
		}

	case domain.ScheduleTransaction:
		if p.FromBagID != nil || p.ToBagID != nil || p.CounterID != nil || p.EstimatedCents != nil || p.Note != "" {
			return domain.ScheduledAction{}, validationError("transaction payload takes exactly account_id, transaction_kind, amount_cents, optional category_id, optional bag_id and description")
		}
		if p.AccountID == nil {
			return domain.ScheduledAction{}, validationError("transaction needs an account_id")
		}
		if p.TransactionKind != domain.TransactionExpense && p.TransactionKind != domain.TransactionIncome {
			return domain.ScheduledAction{}, validationError(
				"transaction_kind %q must be expense or income — cross-account moves schedule as account_transfer", p.TransactionKind)
		}
		if err := s.validateTransactionAccount(ctx, *p.AccountID); err != nil {
			return domain.ScheduledAction{}, err
		}
		if p.BagID != nil {
			// Optional bag tag, the manual expense form's "From bag": expenses
			// only, and the bag must be the account's own open bag — each
			// applied occurrence then drains the reserve like any tagged
			// spend.
			if p.TransactionKind != domain.TransactionExpense {
				return domain.ScheduledAction{}, validationError("bag_id belongs to an expense — an income never tags a bag")
			}
			bag, err := s.bags.Get(ctx, *p.BagID)
			if err != nil {
				return domain.ScheduledAction{}, fmt.Errorf("validate bag_id: %w", err)
			}
			if bag.AccountID != *p.AccountID {
				return domain.ScheduledAction{}, validationError(
					"bag %q lives on a different account — a scheduled expense tags a bag of its own account", bag.Name)
			}
		}
		if p.CategoryID != nil {
			if _, err := s.categories.GetByID(ctx, *p.CategoryID); err != nil {
				return domain.ScheduledAction{}, fmt.Errorf("validate category_id: %w", err)
			}
		}
		if err := validatePositiveCents(p.AmountCents, "amount_cents"); err != nil {
			return domain.ScheduledAction{}, err
		}
		if len(p.Description) > 500 {
			return domain.ScheduledAction{}, validationError("description must be at most 500 characters")
		}

	case domain.SchedulePurchase:
		if p.BagID != nil || p.FromBagID != nil || p.ToBagID != nil || p.AccountID != nil ||
			p.CounterID != nil || p.CategoryID != nil || p.TransactionKind != "" {
			return domain.ScheduledAction{}, validationError("purchase payload takes exactly an optional estimated_cents and note")
		}
		if p.EstimatedCents != nil {
			if err := validatePositiveCents(*p.EstimatedCents, "estimated_cents"); err != nil {
				return domain.ScheduledAction{}, err
			}
		}
		if len(p.Note) > 200 {
			return domain.ScheduledAction{}, validationError("note must be at most 200 characters")
		}
	}

	// The execution mode: nil on an edit keeps the stored flag untouched
	// (the carry_over pattern); a create without it means manual — the red
	// alert until confirmed, the legacy behavior of every 0037 row.
	autoApply := existing.AutoApply
	if in.AutoApply != nil {
		autoApply = *in.AutoApply
	}

	return domain.ScheduledAction{
		Name:        strings.TrimSpace(in.Name),
		Kind:        in.Kind,
		Payload:     p,
		Recurrence:  in.Recurrence,
		StartDate:   in.StartDate,
		DayOfMonth:  day,
		MonthOfYear: month,
		AutoApply:   autoApply,
		Archived:    false,
	}, nil
}

// timeParseDateOr parses or returns today zero-time (input is validated).
func timeParseDateOr(date string) time.Time {
	t, _ := timeParseDate(date)
	return t
}

// validateTransactionAccount checks the payload's account references exist
// (the apply path re-validates through TransactionService anyway — this
// catches a typo at schedule time instead of silently at apply time).
func (s *ScheduledActionService) validateTransactionAccount(ctx context.Context, id int64) error {
	if _, err := s.accounts.GetByID(ctx, id); err != nil {
		return fmt.Errorf("validate account_id %d: %w", id, err)
	}
	return nil
}

// today is the server-local date — the same reference the transaction
// future-date rule and the budget rollover use.
// horizon returns the forward materialization bound: today + aheadMonths.
func (s *ScheduledActionService) horizon(today string) string {
	return timeParseDateOr(today).AddDate(0, aheadMonths, 0).Format("2006-01-02")
}

// EnsureUpcoming runs the lazy catch-up for every active schedule: it
// materializes the run rows of due dates inside [max(start_date, lookback),
// today + 2 months] and auto-applies the virtual kinds due today or overdue —
// the system auto-executes only movements that touch no stored account
// balance. Called from the scheduler's own reads and the dashboard summary,
// like the budget rollover. A virtual apply that fails (closed bag, short
// source) leaves the run pending and logs — the reminder survives until the
// user fixes the bag or dismisses the occurrence.
func (s *ScheduledActionService) EnsureUpcoming(ctx context.Context, today string) error {
	if err := validateDate(today, "today"); err != nil {
		return err
	}
	actions, err := s.actions.ListActions(ctx, false)
	if err != nil {
		return err
	}
	from := timeParseDateOr(today).AddDate(0, 0, -lookbackDays).Format("2006-01-02")
	to := s.horizon(today)
	for _, a := range actions {
		dates := dueDatesRange(a, from, to)
		if len(dates) > 0 {
			if _, err := s.actions.MaterializeRuns(ctx, a.ID, dates); err != nil {
				return err
			}
		}
		// The auto-apply loop runs for the virtual kinds (they always apply
		// themselves) AND for real kinds flagged manual=off (0046): the user
		// opted the movement into execution on its due date — a failed apply
		// (deleted account, closed bag) leaves the run pending and logged.
		if a.Kind.Virtual() || a.AutoApply {
			if err := s.autoApplyDue(ctx, a, today); err != nil {
				return err
			}
		}
	}
	return nil
}

// autoApplyDue applies every still-pending virtual run of the action due
// today or earlier, in due order. Failures leave the run pending (the catch-up
// retries on the next read); an error here must not stop the read that
// triggered the catch-up, so failures are logged, not returned.
func (s *ScheduledActionService) autoApplyDue(ctx context.Context, a domain.ScheduledAction, today string) error {
	runs, err := s.actions.ListRunsByStatus(ctx, a.ID, domain.SchedulePending)
	if err != nil {
		return err
	}
	for _, run := range runs {
		if run.DueDate > today {
			continue
		}
		view := domain.ScheduleRunView{ScheduledActionRun: run}
		if _, err := s.executeRun(ctx, view, a, today); err != nil {
			if s.log != nil {
				s.log.WarnContext(ctx, "auto-apply of scheduled movement left pending",
					"action_id", a.ID, "name", a.Name, "run_id", run.ID, "due", run.DueDate, "error", err)
			}
		}
	}
	return nil
}

// dueDates computes the materialization window for one action from today
// (lookback and horizon included) — the Update path's helper.
func dueDates(a domain.ScheduledAction, today, to string) []string {
	from := timeParseDateOr(today).AddDate(0, 0, -lookbackDays).Format("2006-01-02")
	return dueDatesRange(a, from, to)
}

// dueDatesRange enumerates the due dates of one schedule inside
// [from, to], aligned to its recurrence, never before start_date. The
// day-of-month is the stored (start-date-derived) day, CLAMPED to each
// month's last day: a plan started on the 31st lands on Feb 28 in short
// months instead of skipping or spilling into the next month (MoneyWiz
// end-of-month rule, user decision 2026-10-28 — the old 1..28 cap skipped
// nothing but forbade real month-end dates).
func dueDatesRange(a domain.ScheduledAction, from, to string) []string {
	start := a.StartDate
	startT, err := timeParseDate(start)
	if err != nil {
		return nil
	}
	toT, err := timeParseDate(to)
	if err != nil {
		return nil
	}
	day := startT.Day()
	if a.DayOfMonth != nil {
		day = *a.DayOfMonth // stored derivation (all saved rows carry it)
	}

	var out []string
	switch a.Recurrence {
	case domain.ScheduleOnce:
		if inWindow(start, from, to) {
			out = append(out, start)
		}
	case domain.ScheduleMonthly:
		for t := firstOfMonth(startT); !t.After(toT); t = t.AddDate(0, 1, 0) {
			due := clampedDate(t, day)
			if due.Before(startT) {
				continue
			}
			s := due.Format("2006-01-02")
			if s >= from && s <= to {
				out = append(out, s)
			}
		}
	case domain.ScheduleYearly:
		month := 1
		if a.MonthOfYear != nil {
			month = *a.MonthOfYear
		}
		for t := firstOfMonth(startT); !t.After(toT); t = t.AddDate(0, 1, 0) {
			if int(t.Month()) != month {
				continue
			}
			due := clampedDate(t, day)
			if due.Before(startT) {
				continue
			}
			s := due.Format("2006-01-02")
			if s >= from && s <= to {
				out = append(out, s)
			}
		}
	}
	return out
}

// inWindow reports whether a date lies inside [from, to] inclusive.
func inWindow(date, from, to string) bool {
	return date >= from && date <= to
}

// clampedDate builds the occurrence date of month(t) at `day`, clamped to
// the month's last day (a day-31 plan lands on Feb 28, not on Mar 3 —
// time.Date would normalize the overflow into the NEXT month).
func clampedDate(t time.Time, day int) time.Time {
	last := time.Date(t.Year(), t.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
	if day > last {
		day = last
	}
	return time.Date(t.Year(), t.Month(), day, 0, 0, 0, 0, time.UTC)
}

// firstOfMonth truncates a date to its month's first day.
func firstOfMonth(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}
