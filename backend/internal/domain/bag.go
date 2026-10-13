package domain

import (
	"time"
)

// BagLifecycle is the state of an allocation envelope. Closed bags stop
// accepting spend attribution and funding (reopen by setting lifecycle back
// to open); they keep their ledger and tagged-spend history like closed
// budgets do.
type BagLifecycle string

const (
	BagOpen   BagLifecycle = "open"
	BagClosed BagLifecycle = "closed"
)

func (s BagLifecycle) Valid() bool {
	return s == BagOpen || s == BagClosed
}

// Bag is a named reserve of one account's balance — money set aside for a
// purpose (e.g. warm expenses, a holiday). It holds no money of its own: the
// bank application is untouched, and the balance is derived from the
// allocation ledger minus tagged spend. TargetCents is an optional "should
// contain at least this much" goal used by the dashboard card and the
// year-end close preview.
type Bag struct {
	ID          int64        `json:"id"`
	Name        string       `json:"name"`
	AccountID   int64        `json:"account_id"`
	TargetCents *int64       `json:"target_cents,omitempty"` // optional, positive
	Status      BagLifecycle `json:"status"`
	ClosedAt    *time.Time   `json:"closed_at,omitempty"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
}

// BagAllocation is one signed ledger row of a bag. Allocations are NOT
// transactions: they enter no aggregate by construction — they partition an
// already-existing balance. AmountCents is positive for fund (money reserved),
// negative for withdraw (released back to unbagged) and either sign inside a
// rebalance pair (each bag records its own leg).
type BagAllocation struct {
	ID        int64  `json:"id"`
	BagID     int64  `json:"bag_id"`
	Kind      string `json:"kind"` // fund | rebalance | withdraw | adjust
	// AmountCents is signed by nature: fund > 0, withdraw < 0, rebalance and
	// adjust carry their balance effect directly.
	AmountCents int64  `json:"amount_cents"`
	Note        string `json:"note,omitempty"`
	Date        string `json:"date"` // YYYY-MM-DD
	// RelatedAllocationID links the two legs of a rebalance (the counterpart
	// row in the receiving bag).
	RelatedAllocationID *int64 `json:"related_allocation_id,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// BagAllocationKind enumerates the ledger row kinds the service accepts.
type BagAllocationKind string

const (
	BagFund      BagAllocationKind = "fund"
	BagRebalance BagAllocationKind = "rebalance"
	BagWithdraw  BagAllocationKind = "withdraw"
	BagAdjust    BagAllocationKind = "adjust"
)

// Valid reports whether the kind is one of the ledger row kinds. Rebalance
// rows are only ever written by the service's paired path, never by the
// single-allocation endpoint.
func (k BagAllocationKind) Valid() bool {
	return k == BagFund || k == BagWithdraw || k == BagAdjust
}

// BagStatus is a bag with its computed balance attached, expressed in the
// linked account's currency. TaggedSpendCents sums every live expense tagged
// to the bag (transactions level bag_id + accepted bill lines through the
// COALESCE per-line override); BalanceCents is Σ allocations − tagged spend.
type BagStatus struct {
	Bag
	AccountName      string `json:"account_name"` // display-only, joined
	TaggedSpendCents int64  `json:"tagged_spend_cents"`
	BalanceCents     int64  `json:"balance_cents"` // Σ allocations − tagged spend; negative = overspent its reserve
}

// BagAccountView is one account's bag section: the account's live balance,
// the money not claimed by any open bag, and its bags.
type BagAccountView struct {
	AccountID           int64       `json:"account_id"`
	AccountName         string      `json:"account_name"`
	Currency            string      `json:"currency"`
	AccountBalanceCents int64       `json:"account_balance_cents"` // the account's stored balance
	UnbaggedCents       int64       `json:"unbagged_cents"`        // balance − Σ open bag balances; negative = oversubscribed
	Oversubscribed      bool        `json:"oversubscribed"`
	Bags                []BagStatus `json:"bags"`
}

// BagOverview is the whole GET /bags read: every account that holds bags (plus
// every account a bag could be named for), with per-account unbagged numbers.
// ConversionWarnings lists currencies shown 1:1 when no rate was available.
type BagOverview struct {
	ConversionWarnings []string         `json:"conversion_warnings"`
	Accounts           []BagAccountView `json:"accounts"`
}

// BagCurrencySpend is one bag's tagged spend in one native currency — the
// repository → service seam type before conversion into the bag's currency.
type BagCurrencySpend struct {
	BagID    int64  `json:"bag_id"`
	Currency string `json:"currency"`
	Cents    int64  `json:"cents"`
}

// BagAllocationInput is the manual ledger entry payload. Kind carries the
// sign: fund and withdraw are validated sign-pure (fund > 0, withdraw < 0);
// adjust carries any non-zero amount.
type BagAllocationInput struct {
	Kind        BagAllocationKind
	AmountCents int64
	Note        string
	Date        string
}

// YearCloseBagRow is one open bag's year-end close preview: where the
// balance would stand after every allocation and tagged spend dated through
// December 31 of the year, and what a target bag would still be short of.
// The assignment itself (rebalances / withdrawals dated in close-out) is
// posted by the client through the regular actions — no bespoke state.
type YearCloseBagRow struct {
	BagID          int64  `json:"bag_id"`
	BagName        string `json:"bag_name"`
	AccountID      int64  `json:"account_id"`
	AccountName    string `json:"account_name"`
	Currency       string `json:"currency"`
	BalanceCents   int64  `json:"balance_cents"`   // Σ allocations − tagged spend through Dec 31
	TargetCents    int64  `json:"target_cents"`    // 0 = no target
	ShortfallCents int64  `json:"shortfall_cents"` // target − balance, 0 when met or untargeted
}

// YearClosePreview answers GET /bags/year-close?year=YYYY.
type YearClosePreview struct {
	Year                string                `json:"year"`
	ConversionWarnings  []string              `json:"conversion_warnings"`
	Bags                []YearCloseBagRow     `json:"bags"`
}

// BagFilters narrows bag-list reads. Zero values mean "no filter".
type BagFilters struct {
	AccountID int64
	Status    BagLifecycle // empty = all
}