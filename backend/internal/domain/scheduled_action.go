package domain

import (
	"time"
)

// ScheduleKind enumerates the scheduler's action kinds. The split is the
// auto-apply boundary: virtual kinds (bag_funding, bag_rebalance) write only
// into the bag allocation ledger — they never touch a stored account balance,
// so the system applies them itself on the due date. Real kinds
// (account_transfer, transaction) and purchases are confirm-first: reminder
// until one user click applies.
type ScheduleKind string

const (
	ScheduleBagFunding      ScheduleKind = "bag_funding"
	ScheduleBagRebalance    ScheduleKind = "bag_rebalance"
	ScheduleAccountTransfer ScheduleKind = "account_transfer"
	ScheduleTransaction     ScheduleKind = "transaction"
	SchedulePurchase        ScheduleKind = "purchase"
)

func (k ScheduleKind) Valid() bool {
	switch k {
	case ScheduleBagFunding, ScheduleBagRebalance, ScheduleAccountTransfer,
		ScheduleTransaction, SchedulePurchase:
		return true
	}
	return false
}

// Virtual reports whether the kind auto-applies (movements into the bag
// ledger that touch no stored account balance).
func (k ScheduleKind) Virtual() bool {
	return k == ScheduleBagFunding || k == ScheduleBagRebalance
}

// ScheduleRecurrence mirrors the budget rollover semantics: once (a single
// start_date run), monthly (one run per month at day_of_month, capped at 28
// so a missing date never skips), yearly (monthly plus month_of_year —
// Nebenkosten, insurance renewals).
type ScheduleRecurrence string

const (
	ScheduleOnce    ScheduleRecurrence = "once"
	ScheduleMonthly ScheduleRecurrence = "monthly"
	ScheduleYearly  ScheduleRecurrence = "yearly"
)

func (r ScheduleRecurrence) Valid() bool {
	return r == ScheduleOnce || r == ScheduleMonthly || r == ScheduleYearly
}

// ScheduleRunStatus is a materialized occurrence's state. Both dismissed
// (acknowledged, nothing to do) and skipped (not applicable this occurrence —
// e.g. skipped a rent month) keep the run row: silence is not history.
type ScheduleRunStatus string

const (
	SchedulePending   ScheduleRunStatus = "pending"
	ScheduleApplied   ScheduleRunStatus = "applied"
	ScheduleDismissed ScheduleRunStatus = "dismissed"
	ScheduleSkipped   ScheduleRunStatus = "skipped"
)

func (s ScheduleRunStatus) Valid() bool {
	return s == SchedulePending || s == ScheduleApplied ||
		s == ScheduleDismissed || s == ScheduleSkipped
}

// SchedulePayload carries the kind-switched action details. Which fields are
// meaningful is decided by the action's kind — validated in the service, never
// in SQL. One struct keeps the JSON shape stable and the switch readable.
//
//	bag_funding       BagID, AmountCents
//	bag_rebalance     FromBagID, ToBagID, AmountCents
//	account_transfer  AccountID (source), CounterAccountID, AmountCents
//	transaction       AccountID, TransactionKind (expense|income), CategoryID?,
//	                  AmountCents, Description, BagID? (expense-only optional
//	                  bag tag — each applied occurrence drains that bag, the
//	                  manual expense form's "From bag")
//	purchase          EstimatedCents?, Note
type SchedulePayload struct {
	BagID       *int64 `json:"bag_id,omitempty"`
	FromBagID   *int64 `json:"from_bag_id,omitempty"`
	ToBagID     *int64 `json:"to_bag_id,omitempty"`
	AccountID   *int64 `json:"account_id,omitempty"`
	CounterID   *int64 `json:"counter_account_id,omitempty"`
	AmountCents int64  `json:"amount_cents,omitempty"`
	// TransactionKind is "expense" or "income" — a scheduled transfer is the
	// account_transfer kind with its own payload shape.
	TransactionKind TransactionKind `json:"transaction_kind,omitempty"`
	CategoryID      *int64          `json:"category_id,omitempty"`
	Description     string          `json:"description,omitempty"`
	EstimatedCents  *int64          `json:"estimated_cents,omitempty"`
	Note            string          `json:"note,omitempty"`
}

// ScheduledAction is one plan row: what should move, how often, from when.
// The payload lives validated in JSON; SchedulePayload decodes it.
//
// AutoApply is the execution mode (migration 0046): false (every legacy
// row) = manual — the pending occurrence is the red alert until the user
// confirms, dismisses or skips it; true = the movement executes itself on
// each due date (the virtual bag kinds always did, regardless of the
// flag). A failed auto-apply leaves the run pending — the alert survives.
type ScheduledAction struct {
	ID          int64              `json:"id"`
	Name        string             `json:"name"`
	Kind        ScheduleKind       `json:"kind"`
	Payload     SchedulePayload    `json:"payload"`
	Recurrence  ScheduleRecurrence `json:"recurrence"`
	StartDate   string             `json:"start_date"` // YYYY-MM-DD
	DayOfMonth  *int               `json:"day_of_month,omitempty"`
	MonthOfYear *int               `json:"month_of_year,omitempty"`
	AutoApply   bool               `json:"auto_apply"`
	Archived    bool               `json:"archived"`
	CreatedAt   time.Time          `json:"created_at"`
	UpdatedAt   time.Time          `json:"updated_at"`
}

// ScheduledActionRun is one materialized occurrence. Created lazily by the
// catch-up (UNIQUE action_id+due_date keeps it idempotent); the status moves
// pending → applied (the effect was written), dismissed or skipped.
// Auto-applied virtual runs record the bag allocation they wrote; applied
// confirm-first runs record the transaction they wrote.
type ScheduledActionRun struct {
	ID         int64             `json:"id"`
	ActionID   int64             `json:"action_id"`
	DueDate    string            `json:"due_date"` // YYYY-MM-DD
	Status     ScheduleRunStatus `json:"status"`
	ResultType *string           `json:"result_type,omitempty"` // "allocation" | "transaction"
	ResultID   *int64            `json:"result_id,omitempty"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`
}

// ScheduleRunView is the list/read shape of a run with its action attached —
// a run alone is uninterpretable (which schedule, what payload).
type ScheduleRunView struct {
	ScheduledActionRun                 // embedded: the run's own fields flatten onto the view
	ActionName         string          `json:"action_name"`
	Kind               ScheduleKind    `json:"kind"`
	Schedule           SchedulePayload `json:"schedule"`
}

// ScheduleFilters narrows run-list reads. Zero values mean "no filter";
// Statuses matches every listed status (a run row answers the to-do list with
// one status, the dashboard card with more).
type ScheduleRunFilters struct {
	ActionID int64
	Statuses []ScheduleRunStatus
	DueFrom  string // YYYY-MM-DD, inclusive
	DueTo    string // YYYY-MM-DD, inclusive
	Limit    int
}

// PayableProjected marks a payables-view occurrence derived from the
// recurrence rules that has no materialized run row yet — the lazy catch-up
// only covers the 31-day-lookback / +2-months window, while the payables
// projection reaches further. An occurrence counts as owed until a run
// materializes and the user applies, dismisses or skips it.
const PayableProjected ScheduleRunStatus = "projected"

// Payable is one projected occurrence on the occurrences register (Phase 6).
// Pure projection — nothing here writes run rows; when the lazy catch-up
// materializes the month, the numbers converge (projection and runs never
// both list, a date is either merged with its run or projected, never
// duplicated).
type Payable struct {
	ActionID int64        `json:"action_id"`
	Name     string       `json:"name"`
	Kind     ScheduleKind `json:"kind"`
	DueDate  string       `json:"due_date"` // YYYY-MM-DD
	// AmountCents is the payload's amount (a purchase's estimate); zero means
	// "no estimate" — the row shows but never enters the totals.
	AmountCents int64  `json:"amount_cents"`
	Currency    string `json:"currency"`
	// Status is the materialized run's status, or PayableProjected. A
	// dismissed/skipped occurrence stays listed (with its own badge) but is
	// not owed — acknowledge ≠ paid ≠ owed.
	Status ScheduleRunStatus `json:"status"`
	RunID  *int64            `json:"run_id,omitempty"`
	// Schedule carries the kind's payload so the UI renders the same
	// payload sentence the schedules list shows.
	Schedule SchedulePayload `json:"schedule"`
	// AutoApply mirrors the plan's execution mode (0046): the register
	// marks these rows "runs itself each period" instead of a red alert.
	AutoApply bool `json:"auto_apply"`
	// IsPayable marks the confirm-first outflow directions (expense
	// transactions, transfers, estimated purchases) whose occurrences fold
	// into the owed/paid totals. Income transactions and virtual bag kinds
	// list with their own rows but enter neither column.
	IsPayable bool `json:"is_payable"`
	// Plan fields, so the UI can manage the schedule from the occurrence row
	// (MoneyWiz register style: edit/delete where the date is shown) without
	// a second fetch.
	Recurrence  ScheduleRecurrence `json:"recurrence"`
	StartDate   string             `json:"start_date"` // YYYY-MM-DD
	DayOfMonth  *int               `json:"day_of_month,omitempty"`
	MonthOfYear *int               `json:"month_of_year,omitempty"`
}

// PayablesTotal is one month-currency cell of the payables report: owed sums
// the pending + projected occurrences, paid the applied ones. Dismissed and
// skipped occurrences enter neither column. Cross-currency sums are never
// attempted — each currency keeps its own row (no recorded rate exists at
// projection time).
type PayablesTotal struct {
	Month     string `json:"month"` // YYYY-MM, first-of-month key
	Currency  string `json:"currency"`
	OwedCents int64  `json:"owed_cents"`
	PaidCents int64  `json:"paid_cents"`
}

// PayablesReport is the read model of GET /scheduled-actions/payables: one
// month of the occurrences register (MoneyWiz-style month paging — the user
// decision that replaced the rolling window).
type PayablesReport struct {
	Today  string          `json:"today"`
	Month  string          `json:"month"` // YYYY-MM, the rendered month
	Items  []Payable       `json:"items"`
	Totals []PayablesTotal `json:"totals"`
}
