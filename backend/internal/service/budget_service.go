package service

// BudgetService rules, split out of account_service.go (2026-10-28) — the
// file had grown into a grab-bag while its test file already lived apart.

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"home-finance-planner/backend/internal/domain"
)

// BudgetService implements budget business rules. Budgets are either
// open-ended envelopes (no period, open until manually closed) or monthly
// caps that reset each calendar month through the lazy rollover: reading
// budgets on/after the first of a new month closes the stale open monthly
// row — its month stays the historical label — and opens a successor for the
// current month carrying the same amount. When the series carries
// (carry_over, migration 0043) the successor also inherits the running
// carried balance: predecessor.carried + (cap − month spend), deficits
// negative, MoneyWiz-style. Manually closing a monthly row ends its series
// (no successor) — the carry dies with it.
type BudgetService struct {
	budgets    BudgetStore
	categories CategoryStore
	// base + rates supply the carry chain's conversion. A leftover is a
	// base-currency number — the budget amount is never converted, so the
	// month spend folded into the carry must use the same fold the dashboard
	// evaluation applies. nil (tests, degraded installs) disables the carry:
	// rows roll over with carried_cents 0, byte-equal legacy behavior.
	base  BaseCurrencySource
	rates RateSource
	log   *slog.Logger
}

// BaseCurrencySource supplies the reference currency the carry chain
// denominates in (*SettingsService satisfies it).
type BaseCurrencySource interface {
	BaseCurrency(ctx context.Context) (string, error)
}

// BudgetInput is the user-facing payload for create/update.
type BudgetInput struct {
	CategoryID  int64
	AmountCents int64
	Cadence     domain.BudgetCadence // empty = envelope (the default)
	// CarryOver, monthly rows only: whether the series' successors inherit
	// the running carried balance. nil defaults ON for monthly (decision
	// 2026-10-28); existing rows created before 0043 stay off. Envelopes
	// never carry.
	CarryOver *bool
}

// currentMonth returns the calendar month ("YYYY-MM") the server is in.
func currentMonth() string { return time.Now().Format("2006-01") }

// monthEnd converts "YYYY-MM" to the month's inclusive last day
// ("YYYY-MM-DD"); malformed input degrades to a conservative upper bound.
func monthEnd(month string) string {
	first, err := timeParseDate(month + "-01")
	if err != nil {
		return month + "-31"
	}
	last := time.Date(first.Year(), first.Month()+1, 0, 0, 0, 0, 0, time.UTC)
	return last.Format("2006-01-02")
}

// EnsureMonthlyCurrent runs the lazy month rollover to the given month
// ("YYYY-MM"): every open monthly budget stale against it is closed and
// succeeded for that month, carrying its running balance when the series
// carries. The spend half is read and converted BEFORE the transactional
// close, so the carried number mirrors the dashboard evaluation exactly.
func (s *BudgetService) EnsureMonthlyCurrent(ctx context.Context, month string) error {
	if err := validateMonth(month, "month"); err != nil {
		return err
	}
	stales, err := s.budgets.ListStaleMonthly(ctx, month)
	if err != nil {
		return fmt.Errorf("list stale monthly budgets: %w", err)
	}
	carries, err := s.carryChain(ctx, stales)
	if err != nil {
		return fmt.Errorf("budget carry chain: %w", err)
	}
	return s.budgets.RolloverMonthly(ctx, month, carries)
}

// carryChain computes each stale row's successor carry:
//
//	carried(successor) = carried(pred) + (cap − spendOf(pred.month))
//
// Spend is the month's category-bucketed transaction spend plus all-time
// bill line links, converted into the base currency — the same fold the
// dashboard's evaluation uses, so the carried number equals what the UI
// reports for that row. Rollover-off rows are skipped (absent → 0).
// Without conversion sources (nil base/rates — tests, degraded installs)
// the chain is skipped entirely rather than computed half-converted.
func (s *BudgetService) carryChain(ctx context.Context, stales []domain.Budget) (map[int64]int64, error) {
	need := false
	for _, b := range stales {
		if b.CarryOver {
			need = true
			break
		}
	}
	if !need || s.base == nil || s.rates == nil {
		return nil, nil
	}
	base, err := s.base.BaseCurrency(ctx)
	if err != nil {
		return nil, fmt.Errorf("base currency: %w", err)
	}
	snap, err := s.rates.Snapshot(ctx)
	if err != nil {
		return nil, fmt.Errorf("rate snapshot: %w", err)
	}
	conv := newConverter(base, snap)

	categorySpend, err := s.budgets.CarryCategoryMonthSpend(ctx)
	if err != nil {
		return nil, err
	}
	catMonth := map[int64]map[string]int64{}
	for _, c := range categorySpend {
		m, ok := catMonth[c.CategoryID]
		if !ok {
			m = map[string]int64{}
			catMonth[c.CategoryID] = m
		}
		m[c.Month] += conv.add(c.Currency, c.TotalCents)
	}
	billRows, err := s.budgets.CarryBillSpend(ctx)
	if err != nil {
		return nil, err
	}
	billSpend := map[int64]int64{}
	for _, b := range billRows {
		billSpend[b.BudgetID] += conv.add(b.Currency, b.Cents)
	}
	if ws := conv.warnings(); len(ws) > 0 && s.log != nil {
		s.log.WarnContext(ctx, "budget carry spend converted 1:1 (no rate)",
			"currencies", fmt.Sprintf("%v", ws))
	}

	out := map[int64]int64{}
	for _, b := range stales {
		if !b.CarryOver {
			continue
		}
		spend := catMonth[b.CategoryID][b.Month] + billSpend[b.ID]
		out[b.ID] = b.CarriedCents + (b.AmountCents - spend)
	}
	return out, nil
}

// List returns budgets, optionally filtered by lifecycle status
// ("" = all, "open", "closed"). Triggers the month rollover first so a
// monthly row never lingers past its own month.
func (s *BudgetService) List(ctx context.Context, status string) ([]domain.Budget, error) {
	if err := s.EnsureMonthlyCurrent(ctx, currentMonth()); err != nil {
		return nil, fmt.Errorf("budget month rollover: %w", err)
	}
	budgets, err := s.budgets.List(ctx)
	if err != nil {
		return nil, err
	}
	if status == "" {
		return budgets, nil
	}
	lc := domain.BudgetLifecycle(status)
	if !lc.Valid() {
		return nil, validationError("status %q must be open or closed", status)
	}
	out := make([]domain.Budget, 0, len(budgets))
	for _, b := range budgets {
		if b.Status == lc {
			out = append(out, b)
		}
	}
	return out, nil
}

func (s *BudgetService) Create(ctx context.Context, in BudgetInput) (domain.Budget, error) {
	b, err := s.build(ctx, in)
	if err != nil {
		return domain.Budget{}, err
	}
	out, err := s.budgets.Create(ctx, b)
	if err != nil {
		return domain.Budget{}, err
	}
	return out, nil
}

// Update changes a budget's amount and (monthly open rows) the series'
// carry_over flag (category is immutable). A closed row refuses: a closed
// envelope's amount is its final account, and a closed monthly row's cap is
// the historical fact the Jan-vs-Feb comparison reads — both would be
// falsified by a late edit (reopen first, then edit). A nil carryOver leaves
// the flag untouched. Editing the cap mid-series changes only the transition
// month's leftover — the carry chain keeps the closed months' history intact.
func (s *BudgetService) Update(ctx context.Context, id int64, amountCents int64, carryOver *bool) (domain.Budget, error) {
	if err := validatePositiveCents(amountCents, "amount_cents"); err != nil {
		return domain.Budget{}, err
	}
	existing, err := s.budgets.GetByID(ctx, id)
	if err != nil {
		return domain.Budget{}, err
	}
	if existing.Status == domain.BudgetClosed {
		return domain.Budget{}, validationError(
			"closed budgets keep their amount — reopen the budget before editing it")
	}
	existing.AmountCents = amountCents
	if carryOver != nil && existing.Cadence == domain.BudgetMonthly {
		existing.CarryOver = *carryOver
	}
	return s.budgets.Update(ctx, existing)
}

// SetStatus marks an envelope finished (closed) or reopens it. Closing
// stamps closed_at; reopening clears it.
func (s *BudgetService) SetStatus(ctx context.Context, id int64, status domain.BudgetLifecycle) (domain.Budget, error) {
	if !status.Valid() {
		return domain.Budget{}, validationError("status %q must be open or closed", status)
	}
	existing, err := s.budgets.GetByID(ctx, id)
	if err != nil {
		return domain.Budget{}, err
	}
	existing.Status = status
	if status == domain.BudgetClosed {
		now := time.Now().UTC()
		existing.ClosedAt = &now
	} else {
		existing.ClosedAt = nil
	}
	return s.budgets.Update(ctx, existing)
}

func (s *BudgetService) Delete(ctx context.Context, id int64) error {
	return s.budgets.Delete(ctx, id)
}

// BudgetTransferInput is the payload of a budget→budget transfer. A nil
// FromBudgetID is the carried-balance seed: the amount (signed) rides onto
// the target row's carried_cents — a first-month manual carry is a transfer
// from nothing.
type BudgetTransferInput struct {
	FromBudgetID *int64
	ToBudgetID   int64
	// AmountCents is a positive allowance magnitude for transfers; for seeds
	// it may carry either sign (a negative seed models "already overspent").
	AmountCents int64
	Note        string
}

// Transfer moves allowance between two OPEN monthly budgets of the SAME
// month (MoneyWiz rule 5): the source cap shrinks (staying positive), the
// target cap grows, and one auditable reallocation row records the move.
// With FromBudgetID nil the amount seeds the target row's carried_cents —
// only rows whose series carries (carry_over) may be seeded, a non-carry
// row would hold an unreadable number. The carry chains stay honest by
// construction: a mid-month cap change feeds the next rollover's leftover
// exactly like a direct cap edit.
func (s *BudgetService) Transfer(ctx context.Context, in BudgetTransferInput) (domain.Budget, domain.Budget, error) {
	if in.FromBudgetID == nil {
		if in.AmountCents == 0 {
			return domain.Budget{}, domain.Budget{}, validationError("a carried-balance seed must not be zero")
		}
	} else if in.AmountCents <= 0 {
		return domain.Budget{}, domain.Budget{}, validationError("amount_cents must be a positive allowance magnitude")
	}
	if len(in.Note) > 200 {
		return domain.Budget{}, domain.Budget{}, validationError("note must be at most 200 characters")
	}
	if in.ToBudgetID <= 0 {
		return domain.Budget{}, domain.Budget{}, validationError("to_budget_id must be a positive budget id")
	}
	seed := in.FromBudgetID == nil
	if !seed && in.FromBudgetID != nil && *in.FromBudgetID == in.ToBudgetID {
		return domain.Budget{}, domain.Budget{}, validationError("source and target budgets must differ")
	}

	target, err := s.budgets.GetByID(ctx, in.ToBudgetID)
	if err != nil {
		return domain.Budget{}, domain.Budget{}, fmt.Errorf("validate to_budget_id: %w", err)
	}
	if target.Cadence != domain.BudgetMonthly || target.Status != domain.BudgetOpen {
		return domain.Budget{}, domain.Budget{}, validationError(
			"budget #%d is not an open monthly row — transfers ride open rows of one month", target.ID)
	}
	if seed {
		// A carried number is unread unless the series rolls it forward.
		if !target.CarryOver {
			return domain.Budget{}, domain.Budget{}, validationError(
				"budget #%d does not roll over — carried-balance seeds only affect carrying series", target.ID)
		}
		to, err := s.budgets.ApplySeed(ctx, target.ID, in.AmountCents, strings.TrimSpace(in.Note), target.Month)
		if err != nil {
			return domain.Budget{}, domain.Budget{}, err
		}
		return domain.Budget{}, to, nil
	}

	source, err := s.budgets.GetByID(ctx, *in.FromBudgetID)
	if err != nil {
		return domain.Budget{}, domain.Budget{}, fmt.Errorf("validate from_budget_id: %w", err)
	}
	if source.Cadence != domain.BudgetMonthly || source.Status != domain.BudgetOpen {
		return domain.Budget{}, domain.Budget{}, validationError(
			"budget #%d is not an open monthly row — closed months are history", source.ID)
	}
	if source.Month != target.Month {
		return domain.Budget{}, domain.Budget{}, validationError(
			"budgets #%d and #%d cover different months (%s vs %s) — transfers stay inside one month",
			source.ID, target.ID, source.Month, target.Month)
	}
	from, to, err := s.budgets.ApplyTransfer(
		ctx, source.ID, target.ID, in.AmountCents, strings.TrimSpace(in.Note), source.Month)
	if err != nil {
		return domain.Budget{}, domain.Budget{}, err
	}
	return from, to, nil
}

// Transfers lists the auditable transfer history, newest first.
func (s *BudgetService) Transfers(ctx context.Context) ([]domain.BudgetTransfer, error) {
	return s.budgets.ListTransfers(ctx)
}

func (s *BudgetService) build(ctx context.Context, in BudgetInput) (domain.Budget, error) {
	if in.CategoryID <= 0 {
		return domain.Budget{}, validationError("category_id must be a positive id")
	}
	if err := validatePositiveCents(in.AmountCents, "amount_cents"); err != nil {
		return domain.Budget{}, err
	}
	cadence := in.Cadence
	if cadence == "" {
		cadence = domain.BudgetEnvelope
	}
	if !cadence.Valid() {
		return domain.Budget{}, validationError("cadence %q must be open or monthly", cadence)
	}
	if _, err := s.categories.GetByID(ctx, in.CategoryID); err != nil {
		return domain.Budget{}, fmt.Errorf("validate category_id: %w", err)
	}
	b := domain.Budget{
		CategoryID:  in.CategoryID,
		AmountCents: in.AmountCents,
		Status:      domain.BudgetOpen,
		Cadence:     cadence,
	}
	if cadence == domain.BudgetMonthly {
		// A new monthly row covers the month it is created in; the rollover
		// takes it from there. Rollover defaults ON (decision 2026-10-28);
		// a payload that says otherwise opts out. The first month never
		// inherits a carried balance (MoneyWiz's "carried balance" seed is a
		// later slice).
		b.Month = currentMonth()
		b.CarryOver = in.CarryOver == nil || *in.CarryOver
	}
	return b, nil
}
