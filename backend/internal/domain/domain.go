// Package domain holds the core business entities and errors.
// It must stay dependency-free: no net/http, no sql, no other internal imports.
package domain

import (
	"errors"
	"strings"
	"time"
)

// Domain errors. Handlers map these to HTTP status codes in one place.
var (
	// ErrNotFound is returned when an entity does not exist.
	ErrNotFound = errors.New("not found")
	// ErrValidation is returned when input fails business validation.
	ErrValidation = errors.New("validation failed")
	// ErrConflict is returned when a write violates a uniqueness or
	// consistency rule (e.g. budget for month+category already exists).
	ErrConflict = errors.New("conflict")
)

// AccountType enumerates the kinds of accounts a user can hold.
type AccountType string

const (
	AccountChecking AccountType = "checking"
	AccountSavings  AccountType = "savings"
	AccountCredit   AccountType = "credit"
	AccountCash     AccountType = "cash"
	AccountOther    AccountType = "other"
)

func (t AccountType) Valid() bool {
	switch t {
	case AccountChecking, AccountSavings, AccountCredit, AccountCash, AccountOther:
		return true
	}
	return false
}

// Account is a user-owned financial account.
type Account struct {
	ID             int64       `json:"id"`
	Name           string      `json:"name"`
	Type           AccountType `json:"type"`
	Currency       string      `json:"currency"`
	BalanceCents   int64       `json:"balance_cents"`
	CardLastDigits string      `json:"card_last_digits,omitempty"` // card identifier for bill matching
	CreatedAt      time.Time   `json:"created_at"`
	UpdatedAt      time.Time   `json:"updated_at"`
}

// TransactionKind distinguishes money in vs money out.
type TransactionKind string

const (
	TransactionIncome  TransactionKind = "income"
	TransactionExpense TransactionKind = "expense"
)

func (k TransactionKind) Valid() bool {
	return k == TransactionIncome || k == TransactionExpense
}

// TransactionFilters narrows transaction list queries. Zero values mean "no
// filter"; Limit <= 0 uses the store's default limit.
type TransactionFilters struct {
	AccountID int64
	Category  *int64
	Month     string // YYYY-MM
	Kind      TransactionKind
	Limit     int
	Offset    int
}

// Transaction is a single movement of money on an account.
type Transaction struct {
	ID          int64           `json:"id"`
	AccountID   int64           `json:"account_id"`
	CategoryID  *int64          `json:"category_id"` // nullable
	Kind        TransactionKind `json:"kind"`
	AmountCents int64           `json:"amount_cents"` // always positive; Kind gives direction
	Currency    string          `json:"currency"`     // ISO 4217 code; account's currency for manual rows, bill's for confirmations
	Description string          `json:"description"`
	Date        string          `json:"date"` // YYYY-MM-DD
	// BillID is set when this row was recorded for a scanned bill: the
	// transaction is then readonly — edits happen in the bills view.
	BillID *int64 `json:"bill_id,omitempty"`
	// StoreID is the market a manual purchase was made at (NULL for bill
	// transactions, which keep their store on the bill row).
	StoreID   *int64 `json:"store_id,omitempty"`
	StoreName string `json:"store_name,omitempty"` // display-only, joined from stores

	// Display-only. ItemCount is the number of item lines (correlated count,
	// always present); Items are loaded by Get only, never by List.
	ItemCount       int               `json:"item_count"`
	ItemsTotalCents int64             `json:"items_total_cents"` // Σ line totals; 0 without items
	Items           []TransactionItem `json:"items,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TransactionItem is one article line of a manually entered transaction.
// Bill lines live in bill_items and carry budget attribution, returns and
// receipt analysis; these are the purchase-relevant subset, in the
// transaction's currency. Money-back lines (Leergut / a category with
// allows_negative) may carry negative prices and reduce the items total —
// they never link to the catalogue, like bill returns. LineTotalCents is
// computed server-side (quantity × unit price − discount, clamped to ≥ 0
// unless the line allows negatives).
type TransactionItem struct {
	ID             int64   `json:"id"`
	TransactionID  int64   `json:"transaction_id"`
	ProductID      *int64  `json:"product_id"`
	ProductName    string  `json:"product_name,omitempty"` // display-only join
	Name           string  `json:"name"`
	Brand          string  `json:"brand,omitempty"`
	Unit           string  `json:"unit,omitempty"` // measure: kg, g, l, ml, pcs, …
	CategoryID     *int64  `json:"category_id"`    // seeds a newly created product only
	Quantity       float64 `json:"quantity"`
	UnitPriceCents int64   `json:"unit_price_cents"`
	DiscountCents  int64   `json:"discount_cents"`
	LineTotalCents int64   `json:"line_total_cents"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Category groups transactions (groceries, rent, …). The migration-seeded
// storage taxonomy (is_system) adds section/icon/description for bill items.
type Category struct {
	ID             int64     `json:"id"`
	Name           string    `json:"name"`
	Section        string    `json:"section,omitempty"`     // storage group (Fridge, Freezer, …)
	Icon           string    `json:"icon,omitempty"`        // emoji representing the category
	Description    string    `json:"description,omitempty"` // what belongs in it
	Kind           string    `json:"kind"`                  // 'product' (bill items) vs 'expense' (budgets)
	IsSystem       bool      `json:"is_system"`             // seeded taxonomy row
	AllowsNegative bool      `json:"allows_negative"`       // deposit/refund lines (Pfand, Leergut) may carry negative prices
	IsFixed        bool      `json:"is_fixed"`              // recurring commitment (rent, utilities, insurance) vs discretionary
	CreatedAt      time.Time `json:"created_at"`
}

// BudgetLifecycle is the state of an open-ended budget envelope.
type BudgetLifecycle string

const (
	BudgetOpen   BudgetLifecycle = "open"
	BudgetClosed BudgetLifecycle = "closed"
)

func (s BudgetLifecycle) Valid() bool {
	return s == BudgetOpen || s == BudgetClosed
}

// Budget is an open-ended spending envelope for one category. It has no
// period: it stays open — accumulating attributed spend — until it is marked
// finished and closed, so total spend, savings and overspend can be evaluated
// over its whole lifetime (e.g. a vacations budget).
type Budget struct {
	ID          int64           `json:"id"`
	CategoryID  int64           `json:"category_id"`
	AmountCents int64           `json:"amount_cents"`
	Status      BudgetLifecycle `json:"status"`
	ClosedAt    *time.Time      `json:"closed_at,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// Summary is the dashboard aggregate for an inclusive date range, with every
// amount converted into the user's base Currency. Budgets lists every open
// envelope plus any closed one that had attributed spend inside the range.
type Summary struct {
	From               string          `json:"from"`                // YYYY-MM-DD
	To                 string          `json:"to"`                  // YYYY-MM-DD
	Currency           string          `json:"currency"`            // base currency of all amounts
	ConversionWarnings []string        `json:"conversion_warnings"` // currencies shown 1:1 (no rate available)
	IncomeCents        int64           `json:"income_cents"`
	ExpenseCents       int64           `json:"expense_cents"`
	NetCents           int64           `json:"net_cents"`
	TotalBalanceCents  int64           `json:"total_balance_cents"`
	Budgets            []BudgetStatus  `json:"budgets"`
	TopCategories      []CategoryTotal `json:"top_categories"`
	DailyExpenses      []DayTotal      `json:"daily_expenses"`
}

// DayTotal aggregates one day of expenses for the dashboard chart.
type DayTotal struct {
	Date         string `json:"date"` // YYYY-MM-DD
	ExpenseCents int64  `json:"expense_cents"`
}

// BudgetStatus compares a budget against actual spending. SpentCents is the
// spend attributed inside the reported range (the monthly view);
// LifetimeSpentCents is every-accepted-spend attributed to the envelope since
// it was created. RemainingCents is the envelope balance: amount minus
// lifetime spend — negative means overspent.
type BudgetStatus struct {
	Budget
	SpentCents         int64 `json:"spent_cents"`
	LifetimeSpentCents int64 `json:"lifetime_spent_cents"`
	RemainingCents     int64 `json:"remaining_cents"`
}

// CategoryTotal aggregates spending per category for a month.
type CategoryTotal struct {
	CategoryID   int64  `json:"category_id"`
	CategoryName string `json:"category_name"`
	TotalCents   int64  `json:"total_cents"`
}

// CurrencyAmount is one native-currency subtotal inside an aggregate.
type CurrencyAmount struct {
	Currency string `json:"currency"`
	Cents    int64  `json:"cents"`
}

// DayCurrencyTotal is one day's expenses in one native currency.
type DayCurrencyTotal struct {
	Date         string `json:"date"`
	Currency     string `json:"currency"`
	ExpenseCents int64  `json:"expense_cents"`
}

// CategoryCurrencyTotal is one category's month spend in one native currency.
type CategoryCurrencyTotal struct {
	CategoryID int64  `json:"category_id"`
	Currency   string `json:"currency"`
	TotalCents int64  `json:"total_cents"`
}

// BudgetCurrencySpend is the bill-line spend attributed to one budget in one
// native currency.
type BudgetCurrencySpend struct {
	BudgetID int64  `json:"budget_id"`
	Currency string `json:"currency"`
	Cents    int64  `json:"cents"`
}

// RawSummary aggregates an inclusive date range in the NATIVE currency of
// each row, before conversion into the user's base currency. It is internal
// to the repository→service seam and never leaves the backend.
type RawSummary struct {
	From            string
	To              string
	Income          []CurrencyAmount
	Expense         []CurrencyAmount
	Balances        []CurrencyAmount
	DailyExpenses   []DayCurrencyTotal
	CategorySpend   []CategoryCurrencyTotal
	BillBudgetSpend []BudgetCurrencySpend
	// Lifetime counterparts without any date filter: all-time spend per
	// category (transactions) and per budget (accepted bill lines).
	LifetimeCategorySpend   []CategoryCurrencyTotal
	LifetimeBillBudgetSpend []BudgetCurrencySpend
	Budgets                 []Budget // base-currency amounts; compared against converted spend
}

// RateSnapshot is a set of reference exchange rates quoted against a pivot
// currency (EUR for the ECB/Frankfurter table). Rates are units of the key
// currency per 1 unit of the pivot.
type RateSnapshot struct {
	Pivot     string             `json:"pivot"`
	Date      string             `json:"date"` // YYYY-MM-DD of the reference data
	FetchedAt time.Time          `json:"fetched_at"`
	Rates     map[string]float64 `json:"rates"`
}

// Rate returns the conversion rate from → to (units of `to` per 1 `from`).
// ok=false when either currency is absent from the snapshot. from == to is 1.
func (s RateSnapshot) Rate(from, to string) (float64, bool) {
	from = strings.ToUpper(strings.TrimSpace(from))
	to = strings.ToUpper(strings.TrimSpace(to))
	if from == "" || to == "" {
		return 0, false
	}
	if from == to {
		return 1, true
	}
	if s.Pivot == "" || s.Rates == nil {
		return 0, false
	}
	fromRate, fromOK := s.rateFromPivot(from)
	toRate, toOK := s.rateFromPivot(to)
	if !fromOK || !toOK {
		return 0, false
	}
	return toRate / fromRate, true
}

// rateFromPivot gives the units of `c` per 1 pivot (pivot itself is 1).
func (s RateSnapshot) rateFromPivot(c string) (float64, bool) {
	if c == s.Pivot {
		return 1, true
	}
	r, ok := s.Rates[c]
	return r, ok
}
