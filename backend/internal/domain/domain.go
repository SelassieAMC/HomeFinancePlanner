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
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
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
	CreatedAt      time.Time `json:"created_at"`
}

// Budget is a monthly spending cap for one category. Month is "YYYY-MM".
type Budget struct {
	ID          int64     `json:"id"`
	CategoryID  int64     `json:"category_id"`
	Month       string    `json:"month"`
	AmountCents int64     `json:"amount_cents"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// MonthSummary is the dashboard aggregate for one month, with every amount
// converted into the user's base Currency.
type MonthSummary struct {
	Month              string          `json:"month"`
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

// BudgetStatus compares a budget against actual spending.
type BudgetStatus struct {
	Budget
	SpentCents     int64 `json:"spent_cents"`
	RemainingCents int64 `json:"remaining_cents"`
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

// RawMonthSummary aggregates one month in the NATIVE currency of each row,
// before conversion into the user's base currency. It is internal to the
// repository→service seam and never leaves the backend.
type RawMonthSummary struct {
	Month           string
	Income          []CurrencyAmount
	Expense         []CurrencyAmount
	Balances        []CurrencyAmount
	DailyExpenses   []DayCurrencyTotal
	CategorySpend   []CategoryCurrencyTotal
	BillBudgetSpend []BudgetCurrencySpend
	Budgets         []Budget // base-currency amounts; compared against converted spend
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
