package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"home-finance-planner/backend/internal/domain"
)

// AccountService implements account business rules.
type AccountService struct{ accounts AccountStore }

// AccountInput is the user-facing payload for create/update.
type AccountInput struct {
	Name           string
	Type           domain.AccountType
	Currency       string
	BalanceCents   int64
	CardLastDigits string // optional; last digits for bill matching
}

func (s *AccountService) List(ctx context.Context) ([]domain.Account, error) {
	return s.accounts.List(ctx)
}

func (s *AccountService) Get(ctx context.Context, id int64) (domain.Account, error) {
	return s.accounts.GetByID(ctx, id)
}

func (s *AccountService) Create(ctx context.Context, in AccountInput) (domain.Account, error) {
	a, err := s.build(in)
	if err != nil {
		return domain.Account{}, err
	}
	out, err := s.accounts.Create(ctx, a)
	if err != nil {
		return domain.Account{}, err
	}
	return out, nil
}

func (s *AccountService) Update(ctx context.Context, id int64, in AccountInput) (domain.Account, error) {
	a, err := s.build(in)
	if err != nil {
		return domain.Account{}, err
	}
	a.ID = id
	out, err := s.accounts.Update(ctx, a)
	if err != nil {
		return domain.Account{}, err
	}
	return out, nil
}

func (s *AccountService) Delete(ctx context.Context, id int64) error {
	return s.accounts.Delete(ctx, id)
}

func (s *AccountService) build(in AccountInput) (domain.Account, error) {
	if err := validateRequiredString(in.Name, "name", 100); err != nil {
		return domain.Account{}, err
	}
	if !in.Type.Valid() {
		return domain.Account{}, validationError("type %q must be one of checking, savings, credit, cash, other", in.Type)
	}
	currency := strings.ToUpper(strings.TrimSpace(in.Currency))
	if currency == "" {
		currency = "USD"
	}
	if len(currency) != 3 {
		return domain.Account{}, validationError("currency %q must be a 3-letter ISO code", in.Currency)
	}
	if err := validateNonNegativeCents(in.BalanceCents, "balance_cents"); err != nil {
		return domain.Account{}, err
	}
	cardDigits := nonDigitsAccount.ReplaceAllString(strings.TrimSpace(in.CardLastDigits), "")
	if cardDigits != "" && len(cardDigits) != 4 {
		return domain.Account{}, validationError("card_last_digits %q must be exactly 4 digits", in.CardLastDigits)
	}
	return domain.Account{
		Name:           strings.TrimSpace(in.Name),
		Type:           in.Type,
		Currency:       currency,
		BalanceCents:   in.BalanceCents,
		CardLastDigits: cardDigits,
	}, nil
}

// nonDigitsAccount strips anything that is not a digit from card numbers.
var nonDigitsAccount = regexp.MustCompile(`\D`)

// CategoryService implements category business rules.
type CategoryService struct{ categories CategoryStore }

func (s *CategoryService) List(ctx context.Context) ([]domain.Category, error) {
	return s.categories.List(ctx)
}

// CategoryInput is the user-facing payload for create.
type CategoryInput struct {
	Name string
}

func (s *CategoryService) Create(ctx context.Context, in CategoryInput) (domain.Category, error) {
	if err := validateRequiredString(in.Name, "name", 60); err != nil {
		return domain.Category{}, err
	}
	c, err := s.categories.Create(ctx, domain.Category{Name: strings.TrimSpace(in.Name)})
	if err != nil {
		return domain.Category{}, err
	}
	return c, nil
}

func (s *CategoryService) Delete(ctx context.Context, id int64) error {
	return s.categories.Delete(ctx, id)
}

// TransactionService implements transaction business rules.
type TransactionService struct {
	transactions TransactionStore
	accounts     AccountStore
	categories   CategoryStore
}

// TransactionInput is the user-facing payload for create/update.
type TransactionInput struct {
	AccountID   int64
	CategoryID  *int64
	Kind        domain.TransactionKind
	AmountCents int64
	Description string
	Date        string
}

func (s *TransactionService) List(ctx context.Context, f TransactionFilters) ([]domain.Transaction, error) {
	if f.Month != "" {
		if err := validateMonth(f.Month, "month"); err != nil {
			return nil, err
		}
	}
	return s.transactions.List(ctx, f)
}

func (s *TransactionService) Get(ctx context.Context, id int64) (domain.Transaction, error) {
	return s.transactions.GetByID(ctx, id)
}

func (s *TransactionService) Create(ctx context.Context, in TransactionInput) (domain.Transaction, error) {
	t, err := s.build(ctx, in)
	if err != nil {
		return domain.Transaction{}, err
	}
	return s.transactions.Create(ctx, t)
}

func (s *TransactionService) Update(ctx context.Context, id int64, in TransactionInput) (domain.Transaction, error) {
	t, err := s.build(ctx, in)
	if err != nil {
		return domain.Transaction{}, err
	}
	t.ID = id
	return s.transactions.Update(ctx, t)
}

func (s *TransactionService) Delete(ctx context.Context, id int64) error {
	return s.transactions.Delete(ctx, id)
}

func (s *TransactionService) build(ctx context.Context, in TransactionInput) (domain.Transaction, error) {
	if in.AccountID <= 0 {
		return domain.Transaction{}, validationError("account_id must be a positive id")
	}
	if err := validatePositiveCents(in.AmountCents, "amount_cents"); err != nil {
		return domain.Transaction{}, err
	}
	if !in.Kind.Valid() {
		return domain.Transaction{}, validationError("kind %q must be income or expense", in.Kind)
	}
	if err := validateDate(in.Date, "date"); err != nil {
		return domain.Transaction{}, err
	}
	if len(in.Description) > 500 {
		return domain.Transaction{}, validationError("description must be at most 500 characters")
	}
	// Referential checks: a transaction must point at real rows.
	if _, err := s.accounts.GetByID(ctx, in.AccountID); err != nil {
		return domain.Transaction{}, fmt.Errorf("validate account_id: %w", err)
	}
	if in.CategoryID != nil {
		if _, err := s.categories.GetByID(ctx, *in.CategoryID); err != nil {
			return domain.Transaction{}, fmt.Errorf("validate category_id: %w", err)
		}
	}
	return domain.Transaction{
		AccountID:   in.AccountID,
		CategoryID:  in.CategoryID,
		Kind:        in.Kind,
		AmountCents: in.AmountCents,
		Description: strings.TrimSpace(in.Description),
		Date:        in.Date,
	}, nil
}

// BudgetService implements budget business rules.
type BudgetService struct {
	budgets    BudgetStore
	categories CategoryStore
}

// BudgetInput is the user-facing payload for create/update.
type BudgetInput struct {
	CategoryID  int64
	Month       string
	AmountCents int64
}

func (s *BudgetService) ListByMonth(ctx context.Context, month string) ([]domain.Budget, error) {
	if err := validateMonth(month, "month"); err != nil {
		return nil, err
	}
	return s.budgets.ListByMonth(ctx, month)
}

func (s *BudgetService) Create(ctx context.Context, in BudgetInput) (domain.Budget, error) {
	b, err := s.build(ctx, in)
	if err != nil {
		return domain.Budget{}, err
	}
	return s.budgets.Create(ctx, b)
}

func (s *BudgetService) Update(ctx context.Context, id int64, amountCents int64) (domain.Budget, error) {
	if err := validatePositiveCents(amountCents, "amount_cents"); err != nil {
		return domain.Budget{}, err
	}
	existing, err := s.budgets.GetByID(ctx, id)
	if err != nil {
		return domain.Budget{}, err
	}
	existing.AmountCents = amountCents
	return s.budgets.Update(ctx, existing)
}

func (s *BudgetService) Delete(ctx context.Context, id int64) error {
	return s.budgets.Delete(ctx, id)
}

func (s *BudgetService) build(ctx context.Context, in BudgetInput) (domain.Budget, error) {
	if in.CategoryID <= 0 {
		return domain.Budget{}, validationError("category_id must be a positive id")
	}
	if err := validateMonth(in.Month, "month"); err != nil {
		return domain.Budget{}, err
	}
	if err := validatePositiveCents(in.AmountCents, "amount_cents"); err != nil {
		return domain.Budget{}, err
	}
	if _, err := s.categories.GetByID(ctx, in.CategoryID); err != nil {
		return domain.Budget{}, fmt.Errorf("validate category_id: %w", err)
	}
	return domain.Budget{
		CategoryID:  in.CategoryID,
		Month:       in.Month,
		AmountCents: in.AmountCents,
	}, nil
}

// SummaryService implements dashboard aggregates.
type SummaryService struct{ summary SummaryStore }

func (s *SummaryService) MonthSummary(ctx context.Context, month string) (domain.MonthSummary, error) {
	if err := validateMonth(month, "month"); err != nil {
		return domain.MonthSummary{}, err
	}
	return s.summary.MonthSummaryFor(ctx, month)
}
