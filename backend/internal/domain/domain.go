// Package domain holds the core business entities and errors.
// It must stay dependency-free: no net/http, no sql, no other internal imports.
package domain

import (
	"errors"
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

// MonthSummary is the dashboard aggregate for one month.
type MonthSummary struct {
	Month             string          `json:"month"`
	IncomeCents       int64           `json:"income_cents"`
	ExpenseCents      int64           `json:"expense_cents"`
	NetCents          int64           `json:"net_cents"`
	TotalBalanceCents int64           `json:"total_balance_cents"`
	Budgets           []BudgetStatus  `json:"budgets"`
	TopCategories     []CategoryTotal `json:"top_categories"`
	DailyExpenses     []DayTotal      `json:"daily_expenses"`
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
