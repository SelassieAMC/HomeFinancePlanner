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

// Account is a user-owned financial account. BalanceCents is DERIVED per
// read (migration 0040): opening_balance_cents plus the signed transaction
// ledger (income +, expense −, transfers both legs). Only the opening balance
// is editable — the reconciliation adjusts it so the derived balance matches
// what the bank says.
type Account struct {
	ID                  int64       `json:"id"`
	Name                string      `json:"name"`
	Type                AccountType `json:"type"`
	Currency            string      `json:"currency"`
	OpeningBalanceCents int64       `json:"opening_balance_cents"`
	BalanceCents        int64       `json:"balance_cents"`              // derived: opening + Σ transactions
	CardLastDigits      string      `json:"card_last_digits,omitempty"` // card identifier for bill matching
	GroupID             *int64      `json:"group_id,omitempty"`         // presentation-only group (0045); nil = ungrouped
	CreatedAt           time.Time   `json:"created_at"`
	UpdatedAt           time.Time   `json:"updated_at"`
}

// AccountGroup is a folder-style presentation grouping of accounts (0045).
// Zero money semantics: membership changes no fold anywhere. Deleting a group
// ungroups its accounts (ON DELETE SET NULL).
type AccountGroup struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// AccountCurrencyAmount is one account's derived balance in its own
// currency — the per-account grain behind the assets/liabilities net worth
// split (sign-partitioned before conversion; the per-currency sums stay the
// net-worth fold).
type AccountCurrencyAmount struct {
	AccountID int64
	Currency  string
	Cents     int64
}

// TransactionKind distinguishes money in vs money out.
type TransactionKind string

const (
	TransactionIncome   TransactionKind = "income"
	TransactionExpense  TransactionKind = "expense"
	TransactionTransfer TransactionKind = "transfer"
)

func (k TransactionKind) Valid() bool {
	return k == TransactionIncome || k == TransactionExpense || k == TransactionTransfer
}

// TransactionFilters narrows transaction list queries. Zero values mean "no
// filter"; Limit <= 0 uses the store's default limit. BagID matches explicit
// tagging (the transaction's own bag, and for bill-linked rows the bill's
// bag mirrored onto the row).
type TransactionFilters struct {
	AccountID int64
	Category  *int64
	Month     string // YYYY-MM
	Kind      TransactionKind
	BagID     *int64
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

	// CounterAccountID is the destination account of a transfer: the row's
	// AccountID is where the money left from. Always NULL for income and
	// expense rows (a NULL on a transfer violates validation).
	CounterAccountID   *int64 `json:"counter_account_id,omitempty"`
	CounterAccountName string `json:"counter_account_name,omitempty"` // display-only, joined from accounts
	// The receiving leg of the transfer (migration 0040): what the destination
	// account gains, recorded at transfer time from the FX snapshot when the
	// two accounts differ in currency. NULL = same-currency (or no rate known):
	// balance reads fold the row's amount 1:1 — the accepted degradation.
	CounterAmountCents *int64 `json:"counter_amount_cents,omitempty"`
	CounterCurrency    string `json:"counter_currency,omitempty"`
	// FeeCents is a transfer's bank/intermediary fee, in the SOURCE account's
	// currency: the sending side loses amount + fee, the receiving side gains
	// only the converted amount. NULL = no fee. Refused on income/expense.
	FeeCents *int64 `json:"fee_cents,omitempty"`
	// FxRate is the exchange rate actually applied when the row was converted
	// (units of `to` per 1 of `from`) — the public reference rate unless the
	// user overrode it (banks don't always follow the reference). NULL = no
	// conversion happened for this row.
	FxRate *float64 `json:"fx_rate,omitempty"`
	// AccountAmountCents appears on income/expense rows whose currency differs
	// from the owning account's currency (e.g. a COP bill paid from a EUR
	// card): the amount in the ACCOUNT's currency, converted at save time from
	// the FX snapshot (or a user-overridden rate). Balance reads fold this in
	// place of AmountCents; NULL folds 1:1 — the accepted degradation when no
	// rate was known.
	AccountAmountCents *int64 `json:"account_amount_cents,omitempty"`

	// BagID tags an expense to a bag: the money was spent out of that
	// reserve. Never set on income or transfer rows (validated); NULL for
	// untagged spend. Bag spend that arrives through a scanned bill stays on
	// the bill row itself (per-line override on bill_items) — the bill's
	// mirrored transaction carries the bill-level bag for display only.
	BagID   *int64 `json:"bag_id,omitempty"`
	BagName string `json:"bag_name,omitempty"` // display-only, joined from bags

	// Display-only. ItemCount is the number of item lines (correlated count,
	// always present); Items are loaded by Get only, never by List.
	ItemCount       int               `json:"item_count"`
	ItemsTotalCents int64             `json:"items_total_cents"` // Σ line totals; 0 without items
	Items           []TransactionItem `json:"items,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// LedgerEntry is one register row of an account (GET /accounts/{id}/ledger,
// Phase 2 of the MoneyWiz roadmap): the transaction plus its per-row running
// balance. ChangeCents is the row's own effect on the balance in the
// ACCOUNT's currency — the same fold `accountBalanceExpr` debits, so a
// foreign-currency expense contributes its account_amount_cents (raw amount
// when none), a fee'd transfer out −(amount + fee) and a transfer in its
// recorded counter amount. RunningBalanceCents folds opening + every row up
// to and including this one in (date, id) order; the LAST row of the FULL
// ledger equals the account's derived balance (repository invariant test).
type LedgerEntry struct {
	Transaction
	ChangeCents         int64 `json:"change_cents"`
	RunningBalanceCents int64 `json:"running_balance_cents"`
}

// AccountLedger bundles the register read: the account (its derived balance
// rides along so the register header shows the live total) and the requested
// window of its transaction thread, newest first.
type AccountLedger struct {
	Account Account       `json:"account"`
	Entries []LedgerEntry `json:"entries"`
}

// BudgetTransfer is one auditable envelope transfer (migration 0044, the
// roadmap's Phase 3): allowance moved between two OPEN monthly rows of the
// SAME month — the source cap shrank, the target cap grew. FromBudgetID NULL
// is the carried-balance SEED: the amount applied to the target row's
// carried_cents (signed; a negative seed models "already overspent") — a
// first-month manual carry is a transfer from nothing. The transfer row is
// plan-level bookkeeping: no money moves anywhere, it is deliberately not a
// bag allocation.
type BudgetTransfer struct {
	ID           int64     `json:"id"`
	FromBudgetID *int64    `json:"from_budget_id,omitempty"` // NULL = carried-balance seed
	ToBudgetID   int64     `json:"to_budget_id"`
	Month        string    `json:"month"` // YYYY-MM, shared by both rows
	AmountCents  int64     `json:"amount_cents"`
	Note         string    `json:"note,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	// Display-only joins: the from category is empty for seeds.
	FromCategoryID   int64  `json:"from_category_id,omitempty"`
	FromCategoryName string `json:"from_category_name,omitempty"`
	ToCategoryID     int64  `json:"to_category_id"`
	ToCategoryName   string `json:"to_category_name"`
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
	ID             int64    `json:"id"`
	TransactionID  int64    `json:"transaction_id"`
	ProductID      *int64   `json:"product_id"`
	ProductName    string   `json:"product_name,omitempty"` // display-only join
	Name           string   `json:"name"`
	StandardName   string   `json:"standard_name,omitempty"` // display-only, joined from product_name_mappings (raw name stays authoritative)
	GenericName    string   `json:"generic_name,omitempty"`  // display-only, joined from product_name_mappings (product family; empty = none known)
	Brand          string   `json:"brand,omitempty"`
	Unit           string   `json:"unit,omitempty"`       // measure: kg, g, l, ml, pcs, …
	UnitValue      *float64 `json:"unit_value,omitempty"` // entered size magnitude ("500" for "500ml"); nil = unknown
	CategoryID     *int64   `json:"category_id"`          // seeds a newly created product only
	Quantity       float64  `json:"quantity"`
	UnitPriceCents int64    `json:"unit_price_cents"`
	DiscountCents  int64    `json:"discount_cents"`
	LineTotalCents int64    `json:"line_total_cents"`

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

// BudgetCadence distinguishes open-ended envelopes from monthly budgets.
// Monthly budgets materialize one row per calendar month through a lazy
// rollover: a stale open monthly row is closed (its month stays the
// historical label; leftovers never roll over) and a successor opens for the
// current month with the same amount.
type BudgetCadence string

const (
	BudgetEnvelope BudgetCadence = "open"
	BudgetMonthly  BudgetCadence = "monthly"
)

func (c BudgetCadence) Valid() bool {
	return c == BudgetEnvelope || c == BudgetMonthly
}

// Budget is a spending pool for one category. Envelopes (cadence 'open')
// have no period: they stay open — accumulating attributed spend — until
// marked finished and closed (e.g. a vacations budget). Monthly budgets
// (cadence 'monthly') cover exactly the calendar Month they carry; the lazy
// rollover closes a stale open monthly row and opens the successor for the
// current month with the same amount.
type Budget struct {
	ID          int64           `json:"id"`
	CategoryID  int64           `json:"category_id"`
	AmountCents int64           `json:"amount_cents"`
	Status      BudgetLifecycle `json:"status"`
	Cadence     BudgetCadence   `json:"cadence"`
	Month       string          `json:"month,omitempty"` // YYYY-MM, monthly rows only
	// CarryOver is the monthly row's series attribute: when set, the lazy
	// rollover computes the predecessor's leftover (amount − its month spend,
	// deficits negative) into the successor's CarriedCents. Envelopes ignore
	// it. New monthly rows default true (service layer); existing rows stayed
	// off through migration 0043.
	CarryOver    bool       `json:"carry_over,omitempty"`
	CarriedCents int64      `json:"carried_cents,omitempty"` // running carried balance, monthly rows
	ClosedAt     *time.Time `json:"closed_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// Goal is a named savings target tracked against one account. It holds no
// money of its own: progress is the live balance of the linked account
// (contributions are transfers into it), and the target is achieved once
// that balance reaches TargetCents. TargetDate is an optional "by when"
// milestone.
type Goal struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	TargetCents int64     `json:"target_cents"`
	Currency    string    `json:"currency"` // the linked account's currency (decided at save time)
	AccountID   int64     `json:"account_id"`
	TargetDate  *string   `json:"target_date,omitempty"` // YYYY-MM-DD, nullable
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// GoalStatus is a goal with its live progress attached. ProgressCents is the
// linked account's current balance; Achieved is derived per read, never
// stored.
type GoalStatus struct {
	Goal
	AccountName   string `json:"account_name"` // display-only, joined
	ProgressCents int64  `json:"progress_cents"`
	Achieved      bool   `json:"achieved"`
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

// Glance is the period-independent "at a glance" dashboard row: current
// standing regardless of the period the user navigated to. Every amount is
// converted into the user's base Currency. Cash flow covers the current
// calendar month; BurnRateCents is the average daily expense over the
// trailing 30 days (today included). PendingReceipts is the review backlog
// of scanned bills not yet confirmed — money already spent, not unpaid
// obligations.
type Glance struct {
	Currency            string   `json:"currency"`            // base currency of all amounts
	ConversionWarnings  []string `json:"conversion_warnings"` // currencies shown 1:1 (no rate available)
	Month               string   `json:"month"`               // YYYY-MM the cash-flow window covers
	IncomeCents         int64    `json:"income_cents"`
	ExpenseCents        int64    `json:"expense_cents"`
	NetCents            int64    `json:"net_cents"` // income minus expense for Month
	NetWorthCents       int64    `json:"net_worth_cents"`
	AssetsCents         int64    `json:"assets_cents"`      // positive derived balances, converted (Phase 4 split)
	LiabilitiesCents    int64    `json:"liabilities_cents"` // |negative derived balances|, converted — a POSITIVE magnitude
	BurnRateCents       int64    `json:"burn_rate_cents"`   // average daily expense, trailing 30 days
	PendingReceipts     int64    `json:"pending_receipts"`
	PendingReceiptCents int64    `json:"pending_receipt_cents"`
}

// RawGlance carries the native-currency subtotals behind Glance; conversion
// into the base currency happens in the service layer.
type RawGlance struct {
	MonthIncome     []CurrencyAmount
	MonthExpense    []CurrencyAmount
	Balances        []CurrencyAmount
	AccountBalances []AccountCurrencyAmount // per-account grain: the assets/liabilities sign split
	Expense30Days   []CurrencyAmount
	PendingReceipts int64
	PendingTotals   []CurrencyAmount
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
	// CategoryMonthSpend buckets transaction expenses per category AND
	// calendar month, all time: the evaluation base for monthly budgets,
	// whose spend is bounded to their own month row.
	CategoryMonthSpend []CategoryMonthCurrencyTotal
	Budgets            []Budget // base-currency amounts; compared against converted spend
}

// CategoryMonthCurrencyTotal is one category's transaction expenses for one
// calendar month in one native currency.
type CategoryMonthCurrencyTotal struct {
	CategoryID int64
	Month      string // YYYY-MM (derived from the transaction date)
	Currency   string
	TotalCents int64
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
