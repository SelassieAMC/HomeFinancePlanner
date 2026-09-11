// Package service implements business rules on top of the repository layer.
// Stores are declared here as consumer-side interfaces so services can be
// unit-tested against fakes without SQLite.
package service

import (
	"context"

	"home-finance-planner/backend/internal/domain"
)

// AccountStore is the persistence contract for accounts.
type AccountStore interface {
	List(ctx context.Context) ([]domain.Account, error)
	GetByID(ctx context.Context, id int64) (domain.Account, error)
	Create(ctx context.Context, a domain.Account) (domain.Account, error)
	Update(ctx context.Context, a domain.Account) (domain.Account, error)
	Delete(ctx context.Context, id int64) error
}

// CategoryStore is the persistence contract for categories.
type CategoryStore interface {
	List(ctx context.Context) ([]domain.Category, error)
	GetByID(ctx context.Context, id int64) (domain.Category, error)
	Create(ctx context.Context, c domain.Category) (domain.Category, error)
	Delete(ctx context.Context, id int64) error
}

// TransactionFilters re-exports the shared domain filter type.
type TransactionFilters = domain.TransactionFilters

// TransactionStore is the persistence contract for transactions.
type TransactionStore interface {
	List(ctx context.Context, f domain.TransactionFilters) ([]domain.Transaction, error)
	GetByID(ctx context.Context, id int64) (domain.Transaction, error)
	Create(ctx context.Context, t domain.Transaction) (domain.Transaction, error)
	Update(ctx context.Context, t domain.Transaction) (domain.Transaction, error)
	Delete(ctx context.Context, id int64) error
}

// BudgetStore is the persistence contract for budgets.
type BudgetStore interface {
	ListByMonth(ctx context.Context, month string) ([]domain.Budget, error)
	GetByID(ctx context.Context, id int64) (domain.Budget, error)
	Create(ctx context.Context, b domain.Budget) (domain.Budget, error)
	Update(ctx context.Context, b domain.Budget) (domain.Budget, error)
	Delete(ctx context.Context, id int64) error
}

// SummaryStore is the persistence contract for dashboard aggregates. It
// returns native-currency groups; conversion into the base currency is the
// service's job.
type SummaryStore interface {
	RawMonthSummary(ctx context.Context, month string) (domain.RawMonthSummary, error)
}

// RateSource supplies the freshest cached exchange-rate snapshot
// (implemented by FXService).
type RateSource interface {
	Snapshot(ctx context.Context) (domain.RateSnapshot, error)
}

// StoreStore is the persistence contract for stores (recurring markets).
// Names are unique case-insensitively, which BillService's find-or-create
// resolves through on a race.
type StoreStore interface {
	List(ctx context.Context) ([]domain.Store, error)
	GetByID(ctx context.Context, id int64) (domain.Store, error)
	FindByName(ctx context.Context, name string) (domain.Store, error)
	Create(ctx context.Context, s domain.Store) (domain.Store, error)
	Update(ctx context.Context, s domain.Store) (domain.Store, error)
	SetLogo(ctx context.Context, id int64, logoPath string) (domain.Store, error)
	Delete(ctx context.Context, id int64) error
}

// Services bundles the business services for handler wiring.
type Services struct {
	Accounts     *AccountService
	Categories   *CategoryService
	Stores       *StoreService
	Transactions *TransactionService
	Budgets      *BudgetService
	Summary      *SummaryService
	Settings     *SettingsService
	Bills        *BillService
}

// New wires services onto their stores.
func New(
	accounts AccountStore,
	categories CategoryStore,
	stores *StoreService,
	transactions TransactionStore,
	budgets BudgetStore,
	summary SummaryStore,
	settings *SettingsService,
	bills *BillService,
	fx *FXService,
) *Services {
	return &Services{
		Accounts:     &AccountService{accounts: accounts},
		Categories:   &CategoryService{categories: categories},
		Stores:       stores,
		Transactions: &TransactionService{transactions: transactions, accounts: accounts, categories: categories},
		Budgets:      &BudgetService{budgets: budgets, categories: categories},
		Summary:      &SummaryService{summary: summary, settings: settings, rates: fx, categories: categories},
		Settings:     settings,
		Bills:        bills,
	}
}
