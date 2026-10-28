package service

// BudgetService rules, split out of account_service.go (2026-10-28) — the
// file had grown into a grab-bag while its test file already lived apart.

import (
	"context"
	"fmt"
	"time"

	"home-finance-planner/backend/internal/domain"
)

// BudgetService implements budget business rules. Budgets are either
// open-ended envelopes (no period, open until manually closed) or monthly
// caps that reset each calendar month through the lazy rollover: reading
// budgets on/after the first of a new month closes the stale open monthly
// row — its month stays the historical label, leftovers never roll over —
// and opens a successor for the current month carrying the same amount.
// Manually closing a monthly row ends its series (no successor).
type BudgetService struct {
	budgets    BudgetStore
	categories CategoryStore
}

// BudgetInput is the user-facing payload for create/update.
type BudgetInput struct {
	CategoryID  int64
	AmountCents int64
	Cadence     domain.BudgetCadence // empty = envelope (the default)
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
// succeeded for that month.
func (s *BudgetService) EnsureMonthlyCurrent(ctx context.Context, month string) error {
	if err := validateMonth(month, "month"); err != nil {
		return err
	}
	return s.budgets.RolloverMonthly(ctx, month)
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

// Update changes only a budget's amount (category is immutable). A closed
// row refuses: a closed envelope's amount is its final account, and a closed
// monthly row's cap is the historical fact the Jan-vs-Feb comparison reads —
// both would be falsified by a late edit (reopen first, then edit).
func (s *BudgetService) Update(ctx context.Context, id int64, amountCents int64) (domain.Budget, error) {
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
		// takes it from there.
		b.Month = currentMonth()
	}
	return b, nil
}
