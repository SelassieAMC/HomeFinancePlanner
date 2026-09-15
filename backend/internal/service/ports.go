// Package service implements business rules on top of the repository layer.
// Stores are declared here as consumer-side interfaces so services can be
// unit-tested against fakes without SQLite.
package service

import (
	"context"
	"log/slog"
	"time"

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

// TransactionStore is the persistence contract for transactions. The plain
// Create/Update never touch item lines (the bill sync path uses them); the
// WithItems variants write the row and its item lines atomically.
type TransactionStore interface {
	List(ctx context.Context, f domain.TransactionFilters) ([]domain.Transaction, error)
	// GetByID loads the item lines (Items and ItemsTotalCents filled); List
	// leaves both empty.
	GetByID(ctx context.Context, id int64) (domain.Transaction, error)
	Create(ctx context.Context, t domain.Transaction) (domain.Transaction, error)
	CreateWithItems(ctx context.Context, t domain.Transaction, items []domain.TransactionItem) (domain.Transaction, error)
	Update(ctx context.Context, t domain.Transaction) (domain.Transaction, error)
	UpdateWithItems(ctx context.Context, t domain.Transaction, items []domain.TransactionItem) (domain.Transaction, error)
	Delete(ctx context.Context, id int64) error
}

// BudgetStore is the persistence contract for budgets.
type BudgetStore interface {
	List(ctx context.Context) ([]domain.Budget, error)
	GetByID(ctx context.Context, id int64) (domain.Budget, error)
	Create(ctx context.Context, b domain.Budget) (domain.Budget, error)
	Update(ctx context.Context, b domain.Budget) (domain.Budget, error)
	Delete(ctx context.Context, id int64) error
}

// SummaryStore is the persistence contract for dashboard aggregates. It
// returns native-currency groups for an inclusive date range; conversion into
// the base currency is the service's job.
type SummaryStore interface {
	RawRangeSummary(ctx context.Context, from, to string) (domain.RawSummary, error)
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

// ProductStore is the persistence contract for the product catalogue. Rows
// are find-or-created by BillService from bill item names; the UI never
// creates or deletes them. Update rewrites the linked bill_items
// (name/unit/category) in the same transaction.
type ProductStore interface {
	List(ctx context.Context, f domain.ProductFilters) (domain.ProductPage, error)
	GetByID(ctx context.Context, id int64) (domain.Product, error)
	FindByName(ctx context.Context, name string) (domain.Product, error)
	Create(ctx context.Context, p domain.Product) (domain.Product, error)
	Update(ctx context.Context, p domain.Product) (domain.Product, error)
	SetPhoto(ctx context.Context, id int64, photoPath string) (domain.Product, error)
	// StorePrices lists the latest per-store price of one product, scoped to
	// the given currency (the product's latest purchase currency).
	StorePrices(ctx context.Context, id int64, currency string) ([]domain.ProductStorePrice, error)
	// StorePurchaseSummary is StorePrices without the currency scope: the
	// latest price and its currency per store. The merge check compares two
	// products' pricing at shared stores through it.
	StorePurchaseSummary(ctx context.Context, id int64) ([]domain.ProductStorePrice, error)
	// Merge redirects every bill item of the drop id to the keep id, deletes
	// the drop row, and rewrites the keep row's editable fields to final —
	// one transaction. Photo files are owned by the service.
	Merge(ctx context.Context, keepID, dropID int64, final domain.Product) (domain.Product, error)
}

// OfferSearchStore is the persistence contract for offer searches (the
// purchase-cart pipeline). Rows are created in 'searching' state and written
// by the background worker; done rows are the kept record.
type OfferSearchStore interface {
	Create(ctx context.Context, s domain.OfferSearch) (domain.OfferSearch, error)
	GetByToken(ctx context.Context, token string) (domain.OfferSearch, error)
	List(ctx context.Context, statuses []domain.OfferSearchStatus, limit int) ([]domain.OfferSearch, error)
	// MarkDone stores the normalized result, guarded on status='searching'.
	MarkDone(ctx context.Context, token string, res *domain.OfferResult) error
	MarkFailed(ctx context.Context, token, msg string) error
	// ClaimRetry atomically moves a finished search back to 'searching';
	// returns false when the row is missing or still searching.
	ClaimRetry(ctx context.Context, token, providerID string) (bool, error)
	Delete(ctx context.Context, token string) error
	// DeleteStale removes failed searches older than the cutoff (done rows are
	// the kept record) and returns the removed tokens.
	DeleteStale(ctx context.Context, olderThan time.Time) ([]string, error)
}

// Services bundles the business services for handler wiring.
type Services struct {
	Accounts      *AccountService
	Categories    *CategoryService
	Stores        *StoreService
	Products      *ProductService
	Transactions  *TransactionService
	Budgets       *BudgetService
	Summary       *SummaryService
	Settings      *SettingsService
	Bills         *BillService
	OfferSearches *OfferSearchService
	Analytics     *AnalyticsService
}

// New wires services onto their stores. storeStore/productStore are the raw
// repositories (find-or-create targets for manual purchases), alongside the
// service wrappers the API handlers use.
func New(
	accounts AccountStore,
	categories CategoryStore,
	storeSvc *StoreService,
	productSvc *ProductService,
	storeStore StoreStore,
	productStore ProductStore,
	transactions TransactionStore,
	budgets BudgetStore,
	summary SummaryStore,
	settings *SettingsService,
	bills *BillService,
	offerSearches *OfferSearchService,
	fx *FXService,
	analytics *AnalyticsService,
) *Services {
	return &Services{
		Accounts:      &AccountService{accounts: accounts},
		Categories:    &CategoryService{categories: categories},
		Stores:        storeSvc,
		Products:      productSvc,
		Transactions:  &TransactionService{transactions: transactions, accounts: accounts, categories: categories, stores: storeStore, products: productStore, log: slog.Default()},
		Budgets:       &BudgetService{budgets: budgets, categories: categories},
		Summary:       &SummaryService{summary: summary, settings: settings, rates: fx, categories: categories},
		Settings:      settings,
		Bills:         bills,
		OfferSearches: offerSearches,
		Analytics:     analytics,
	}
}
