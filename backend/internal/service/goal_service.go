package service

import (
	"context"
	"fmt"
	"strings"

	"home-finance-planner/backend/internal/domain"
)

// GoalService implements financial-goal business rules. A goal tracks one
// account's balance — or one bag's — against a target: progress is read live
// from the tracked balance (account contributions are transfers into it; a
// bag's balance is Σ allocations − tagged spend), so the balance stays the
// single source of truth and the achieved state is derived per read, never
// stored.
type GoalService struct {
	goals    GoalStore
	accounts AccountStore
	// bags resolves a bag-linked goal's account and live balance (implemented
	// by *BagService). nil = bag tracking unavailable (legacy-only tests).
	bags bagReader
}

// bagReader is the bag side GoalService needs: one bag's resolved view (its
// account and live balance). *BagService implements it.
type bagReader interface {
	Get(ctx context.Context, id int64) (domain.BagStatus, error)
}

// GoalInput is the user-facing payload for create/update. Currency is not
// part of it: a goal's currency is decided by the account it tracks. When
// BagID is set, AccountID is derived from the bag (any value is ignored);
// otherwise AccountID is required.
type GoalInput struct {
	Name        string
	TargetCents int64
	AccountID   int64
	// BagID tracks the goal against a bag instead of an account (nil = account).
	BagID *int64
	// TargetDate is the optional "by when" milestone (YYYY-MM-DD; nil = none).
	TargetDate *string
}

// List returns goals with their live progress attached, in creation order.
func (s *GoalService) List(ctx context.Context) ([]domain.GoalStatus, error) {
	rows, err := s.goals.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.GoalStatus, 0, len(rows))
	for _, g := range rows {
		view, err := s.withProgress(ctx, g)
		if err != nil {
			return nil, err
		}
		out = append(out, view)
	}
	return out, nil
}

func (s *GoalService) Get(ctx context.Context, id int64) (domain.GoalStatus, error) {
	g, err := s.goals.GetByID(ctx, id)
	if err != nil {
		return domain.GoalStatus{}, err
	}
	return s.withProgress(ctx, g)
}

func (s *GoalService) Create(ctx context.Context, in GoalInput) (domain.GoalStatus, error) {
	g, err := s.build(ctx, domain.Goal{}, in)
	if err != nil {
		return domain.GoalStatus{}, err
	}
	created, err := s.goals.Create(ctx, g)
	if err != nil {
		return domain.GoalStatus{}, err
	}
	return s.withProgress(ctx, created)
}

func (s *GoalService) Update(ctx context.Context, id int64, in GoalInput) (domain.GoalStatus, error) {
	existing, err := s.goals.GetByID(ctx, id)
	if err != nil {
		return domain.GoalStatus{}, err
	}
	g, err := s.build(ctx, existing, in)
	if err != nil {
		return domain.GoalStatus{}, err
	}
	g.ID = id
	updated, err := s.goals.Update(ctx, g)
	if err != nil {
		return domain.GoalStatus{}, err
	}
	return s.withProgress(ctx, updated)
}

func (s *GoalService) Delete(ctx context.Context, id int64) error {
	return s.goals.Delete(ctx, id)
}

// build validates the payload over the existing row (zero for create) and
// resolves the goal's currency from the linked account. Re-pointing a goal at
// an account with a different currency is refused: the target would silently
// change meaning. When BagID is set the account is the bag's own — single
// source of truth, never user-chosen independently.
func (s *GoalService) build(ctx context.Context, existing domain.Goal, in GoalInput) (domain.Goal, error) {
	if err := validateRequiredString(strings.TrimSpace(in.Name), "name", 200); err != nil {
		return domain.Goal{}, err
	}
	if err := validatePositiveCents(in.TargetCents, "target_cents"); err != nil {
		return domain.Goal{}, err
	}
	if in.TargetDate != nil && *in.TargetDate != "" {
		if err := validateDate(*in.TargetDate, "target_date"); err != nil {
			return domain.Goal{}, err
		}
	}

	var bagID *int64
	accountID := in.AccountID
	if in.BagID != nil {
		if s.bags == nil {
			return domain.Goal{}, validationError("bag tracking is unavailable")
		}
		if *in.BagID <= 0 {
			return domain.Goal{}, validationError("bag_id must be a positive id — a goal tracks one bag")
		}
		bag, err := s.bags.Get(ctx, *in.BagID)
		if err != nil {
			return domain.Goal{}, fmt.Errorf("validate bag_id: %w", err)
		}
		// The account is the bag's — the currency anchor follows it.
		accountID = bag.AccountID
		bagID = in.BagID
	} else if accountID <= 0 {
		return domain.Goal{}, validationError("account_id must be a positive id — a goal tracks one account")
	}

	acc, err := s.accounts.GetByID(ctx, accountID)
	if err != nil {
		return domain.Goal{}, fmt.Errorf("validate account_id: %w", err)
	}
	if existing.ID > 0 && existing.Currency != acc.Currency {
		return domain.Goal{}, validationError(
			"goal %q is tracked in %s — the target would silently change meaning on a %s account",
			existing.Name, existing.Currency, acc.Currency)
	}
	targetDate := in.TargetDate
	if targetDate != nil && *targetDate == "" {
		targetDate = nil
	}
	return domain.Goal{
		Name:        strings.TrimSpace(in.Name),
		TargetCents: in.TargetCents,
		Currency:    acc.Currency,
		AccountID:   acc.ID,
		BagID:       bagID,
		TargetDate:  targetDate,
	}, nil
}

// withProgress attaches the tracked balance's live name, balance and derived
// achieved state to a goal. A bag-linked goal reads the bag's balance (money
// actually reserved); an account-linked goal reads the account's. Progress is
// clamped at zero — a negative balance (an overspent bag, an overdrawn
// account) is "nothing saved yet", never a negative progress.
func (s *GoalService) withProgress(ctx context.Context, g domain.Goal) (domain.GoalStatus, error) {
	if g.BagID != nil {
		bag, err := s.bags.Get(ctx, *g.BagID)
		if err != nil {
			return domain.GoalStatus{}, fmt.Errorf("load goal bag %d: %w", *g.BagID, err)
		}
		progress := bag.BalanceCents
		if progress < 0 {
			progress = 0
		}
		return domain.GoalStatus{
			Goal:          g,
			AccountName:   bag.AccountName,
			BagName:       bag.Name,
			ProgressCents: progress,
			Achieved:      bag.BalanceCents >= g.TargetCents,
		}, nil
	}

	acc, err := s.accounts.GetByID(ctx, g.AccountID)
	if err != nil {
		return domain.GoalStatus{}, fmt.Errorf("load goal account %d: %w", g.AccountID, err)
	}
	progress := acc.BalanceCents
	if progress < 0 {
		progress = 0
	}
	return domain.GoalStatus{
		Goal:          g,
		AccountName:   acc.Name,
		ProgressCents: progress,
		Achieved:      acc.BalanceCents >= g.TargetCents,
	}, nil
}
