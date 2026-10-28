package service

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"home-finance-planner/backend/internal/domain"
)

// This file implements the payables view (Phase 6 of the MoneyWiz parity
// roadmap): the unified occurrences register — "what do we owe, and when",
// paged ONE month at a time (user decision 2026-10-28, replacing the rolling
// window). Spec: home-planner-context/Phase 6 Payables View Spec.md.
//
// Occurrence dates are derived from the recurrence rules with the same
// dueDatesRange helper the lazy materializer uses (one code path), over the
// viewed month. Run rows: the standard lazy catch-up covers
// 31-day-lookback / +2-months, and the viewed month WIDENS the window
// forward on demand (every shown pending occurrence is actionable); where a
// materialized run exists its real status merges in by
// (action_id, due_date) — a date is either merged or projected, never both
// listed.

// splitMonth validates a YYYY-MM string and returns (year, month). The
// register pages one calendar month at a time — anything else is refused
// input, not silently reshaped.
func splitMonth(m string) (int, int, error) {
	if len(m) != 7 || m[4] != '-' {
		return 0, 0, fmt.Errorf("month must be YYYY-MM")
	}
	y, errY := strconv.Atoi(m[:4])
	mo, errM := strconv.Atoi(m[5:])
	if errY != nil || errM != nil || mo < 1 || mo > 12 || y < 1 {
		return 0, 0, fmt.Errorf("month must be YYYY-MM with a real month 1..12")
	}
	return y, mo, nil
}

// payableKind reports whether a schedule enters the payables view. Only
// confirm-first outflow directions count: a scheduled expense and a transfer
// (whose source account must hold the money) are commitments; a scheduled
// income is an inflow; the virtual bag kinds auto-apply themselves (nothing
// "unpaid" can exist); a purchase lists as a planned spend (only with an
// estimate does it carry an amount).
func payableKind(kind domain.ScheduleKind, p domain.SchedulePayload) bool {
	switch kind {
	case domain.ScheduleTransaction:
		return p.TransactionKind == domain.TransactionExpense
	case domain.ScheduleAccountTransfer, domain.SchedulePurchase:
		return true
	default:
		return false // income transactions, bag_funding, bag_rebalance
	}
}

// payableAmount resolves the occurrence's amount: the payload's amount, or a
// purchase's estimate. A purchase without an estimate reads as 0 — the row
// still lists, but never enters a total.
func payableAmount(kind domain.ScheduleKind, p domain.SchedulePayload) int64 {
	if kind == domain.SchedulePurchase {
		if p.EstimatedCents != nil {
			return *p.EstimatedCents
		}
		return 0
	}
	return p.AmountCents
}

// Payables renders ONE month of the unified occurrences register
// (MoneyWiz-style month paging — user decision 2026-10-28, replacing the
// rolling window): every active schedule's occurrences due inside the month,
// payable or not (is_payable marks the rows that fold into the owed/paid
// totals). today comes from the handler (server clock, like every other
// scheduler read); an empty month defaults to today's month. The viewed
// month materializes on demand — navigating to a month the lazy catch-up
// never reached creates its run rows, so EVERY listed pending occurrence is
// actionable (apply early / dismiss / skip). A purchase's currency is the
// base currency (it carries no account); transactions and transfers price in
// their source account's currency, virtual bag movements in their bag's
// account currency — a deleted reference falls back to the base currency
// instead of dropping the row silently.
func (s *ScheduledActionService) Payables(ctx context.Context, today, month string) (domain.PayablesReport, error) {
	if err := validateDate(today, "today"); err != nil {
		return domain.PayablesReport{}, err
	}
	if month == "" {
		month = monthOf(today)
	}
	if _, _, err := splitMonth(month); err != nil {
		return domain.PayablesReport{}, validationError(
			"month %q must be YYYY-MM — the register shows one month at a time", month)
	}
	if err := s.EnsureUpcoming(ctx, today); err != nil {
		return domain.PayablesReport{}, err
	}
	monthStart := month + "-01"
	monthEnd := endOfNthMonth(monthStart, 0)
	if err := s.widenWindow(ctx, today, monthStart, monthEnd); err != nil {
		return domain.PayablesReport{}, err
	}

	base, err := s.baseCurrency(ctx)
	if err != nil {
		return domain.PayablesReport{}, err
	}

	report := domain.PayablesReport{
		Today:  today,
		Month:  month,
		Items:  []domain.Payable{},
		Totals: []domain.PayablesTotal{},
	}

	actions, err := s.actions.ListActions(ctx, false)
	if err != nil {
		return domain.PayablesReport{}, err
	}
	// The register lists EVERY active schedule's occurrences: income
	// transactions and virtual bag kinds ride their own rows with
	// is_payable=false (they act, or are inflows, but are never owed money
	// to a third party); only payable directions also fold totals.
	if len(actions) == 0 {
		return report, nil
	}

	// Currency lookup over the accounts the payloads reference.
	accountCurrency := map[int64]string{}
	if s.accounts != nil {
		accounts, err := s.accounts.List(ctx)
		if err != nil {
			return domain.PayablesReport{}, err
		}
		for _, a := range accounts {
			accountCurrency[a.ID] = a.Currency
		}
	}

	// Runs of the viewed month (every status — dismissed and skipped
	// occurrences stay listed with their badge, just not owed).
	runs, err := s.actions.ListRuns(ctx, domain.ScheduleRunFilters{DueFrom: monthStart, DueTo: monthEnd})
	if err != nil {
		return domain.PayablesReport{}, err
	}
	runStatus := map[string]domain.ScheduleRunView{}
	for _, r := range runs {
		runStatus[runKey(r.ActionID, r.DueDate)] = r
	}

	// Projection: one item per occurrence of the month, status merged.
	for _, a := range actions {
		for _, due := range dueDatesRange(a, monthStart, monthEnd) {
			item := domain.Payable{
				ActionID:    a.ID,
				Name:        a.Name,
				Kind:        a.Kind,
				DueDate:     due,
				Status:      domain.PayableProjected,
				Schedule:    a.Payload,
				IsPayable:   payableKind(a.Kind, a.Payload),
				AutoApply:   a.AutoApply || a.Kind.Virtual(),
				Recurrence:  a.Recurrence,
				StartDate:   a.StartDate,
				DayOfMonth:  a.DayOfMonth,
				MonthOfYear: a.MonthOfYear,
			}
			if run, ok := runStatus[runKey(a.ID, due)]; ok {
				item.Status = run.Status
				id := run.ID
				item.RunID = &id
			}
			item.AmountCents = payableAmount(a.Kind, a.Payload)
			item.Currency = base
			if a.Kind != domain.SchedulePurchase && a.Payload.AccountID != nil {
				if c, ok := accountCurrency[*a.Payload.AccountID]; ok {
					item.Currency = c
				}
			} else if a.Kind.Virtual() && s.bags != nil {
				// Virtual bag movements price in their bag's account currency.
				if bagID := bagIDOf(a.Payload); bagID != nil {
					if bag, err := s.bags.Get(ctx, *bagID); err == nil {
						if c, ok := accountCurrency[bag.AccountID]; ok {
							item.Currency = c
						}
					}
				}
			}
			report.Items = append(report.Items, item)
		}
	}

	// Totals fold: owed = pending + projected (a plan not yet acted on),
	// paid = applied; dismissed/skipped and non-payable directions enter
	// neither column.
	totals := map[string]*domain.PayablesTotal{}
	for _, item := range report.Items {
		if item.AmountCents <= 0 || !item.IsPayable {
			continue
		}
		month := monthOf(item.DueDate)
		key := totalsKey(month, item.Currency)
		t, ok := totals[key]
		if !ok {
			t = &domain.PayablesTotal{Month: month, Currency: item.Currency}
			totals[key] = t
		}
		switch item.Status {
		case domain.ScheduleApplied:
			t.PaidCents += item.AmountCents
		case domain.SchedulePending, domain.PayableProjected:
			t.OwedCents += item.AmountCents
		}
	}
	report.Totals = sortTotals(totals)
	sort.Slice(report.Items, func(i, j int) bool {
		if report.Items[i].DueDate != report.Items[j].DueDate {
			return report.Items[i].DueDate < report.Items[j].DueDate
		}
		return report.Items[i].ActionID < report.Items[j].ActionID
	})
	return report, nil
}

// baseCurrency reads the user's base currency (the purchase-estimate and the
// fallback currency). The interface is the same narrow BaseCurrencySource the
// BudgetService's carry chain uses.
func (s *ScheduledActionService) baseCurrency(ctx context.Context) (string, error) {
	if s.base == nil {
		return DefaultBaseCurrency, nil
	}
	return s.base.BaseCurrency(ctx)
}

// widenWindow materializes the viewed month when it sticks out FORWARD of
// the lazy catch-up window (31-day lookback / +2 months): navigating the
// register is what keeps it honest — the moment a month is shown, its runs
// exist and every pending occurrence is actionable. MaterializeRuns is
// idempotent (UNIQUE action_id+due_date), so re-covering the standard window
// is free. The BACKWARD side never widens past the lookback: months older
// than it show whatever runs exist — the lapse rule stays exact (a
// never-followed-up occurrence must not be fabricated as history just
// because the user scrolled there).
func (s *ScheduledActionService) widenWindow(ctx context.Context, today, from, to string) error {
	if to <= s.horizon(today) {
		return nil
	}
	actions, err := s.actions.ListActions(ctx, false)
	if err != nil {
		return err
	}
	for _, a := range actions {
		dates := dueDatesRange(a, from, to)
		if len(dates) > 0 {
			if _, err := s.actions.MaterializeRuns(ctx, a.ID, dates); err != nil {
				return err
			}
		}
	}
	return nil
}

// runKey builds the (action_id, due_date) map key the run-status merge uses.
func runKey(actionID int64, due string) string {
	return fmt.Sprintf("%d:%s", actionID, due)
}

// bagIDOf returns the bag a virtual payload moves on — the funding target, or
// the rebalance's source bag (where the money sits when it moves).
func bagIDOf(p domain.SchedulePayload) *int64 {
	if p.BagID != nil {
		return p.BagID
	}
	return p.FromBagID
}

// totalsKey builds the (month, currency) map key the totals fold uses.
func totalsKey(month, currency string) string {
	return fmt.Sprintf("%s:%s", month, currency)
}

func monthOf(due string) string {
	return due[:7]
}

func sortTotals(totals map[string]*domain.PayablesTotal) []domain.PayablesTotal {
	out := make([]domain.PayablesTotal, 0, len(totals))
	for _, t := range totals {
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Month != out[j].Month {
			return out[i].Month < out[j].Month
		}
		return out[i].Currency < out[j].Currency
	})
	return out
}

// endOfNthMonth returns the last day of the month that lies `months` months
// after today's month — the projection window's inclusive upper bound
// (months=1 on 2026-10-28 → 2026-11-30; the current month's remainder is
// covered by the window regardless).
func endOfNthMonth(today string, months int) string {
	first := firstOfMonth(timeParseDateOr(today)).AddDate(0, months, 0)
	return first.AddDate(0, 1, 0).AddDate(0, 0, -1).Format("2006-01-02")
}
