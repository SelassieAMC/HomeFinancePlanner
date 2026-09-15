package domain

import (
	"errors"
	"time"
)

// OfferSearchStatus is the pipeline state of one persisted offer search, the
// offer-search counterpart of BillScanStatus.
type OfferSearchStatus string

const (
	OfferSearchSearching OfferSearchStatus = "searching"
	OfferSearchDone      OfferSearchStatus = "done"
	OfferSearchFailed    OfferSearchStatus = "failed"
)

func (s OfferSearchStatus) Valid() bool {
	switch s {
	case OfferSearchSearching, OfferSearchDone, OfferSearchFailed:
		return true
	}
	return false
}

// OfferNameMatch selects how strictly the model must match product names:
// strict = exactly the product as named; loose = include similar naming and
// varieties (avocado → Hass, XL, ready-to-eat). Empty means strict.
type OfferNameMatch string

const (
	OfferNameStrict OfferNameMatch = "strict"
	OfferNameLoose  OfferNameMatch = "loose"
)

func (m OfferNameMatch) Valid() bool {
	switch m {
	case OfferNameStrict, OfferNameLoose:
		return true
	}
	return false
}

// OfferAvailability is the per-row store availability reported by the model.
// Empty means available (legacy rows never carried the field).
type OfferAvailability string

const (
	OfferAvailable    OfferAvailability = "available"
	OfferNotAvailable OfferAvailability = "not_available" // store does not carry the product
	OfferNotPublished OfferAvailability = "not_published" // store exists but publishes no price online
)

// Unavailable reports whether the row is an explicit "no price here" entry —
// such rows carry no price and are excluded from best/worst flags.
func (a OfferAvailability) Unavailable() bool {
	return a == OfferNotAvailable || a == OfferNotPublished
}

// OfferSearchProduct is one cart line as sent to the model — a snapshot of the
// product at search time (name, brand, unit, last known price), so the result
// stays renderable even if the product is later edited or merged.
type OfferSearchProduct struct {
	ProductID      int64   `json:"product_id"`
	Name           string  `json:"name"`
	Brand          string  `json:"brand,omitempty"` // product brand hint; the AI returns a brand per offer
	Unit           string  `json:"unit,omitempty"`
	Quantity       float64 `json:"quantity"`
	LastPriceCents *int64  `json:"last_price_cents,omitempty"` // calibration hint for the model
	Currency       string  `json:"currency,omitempty"`
}

// OfferRow is one price found for one product in one market — or an explicit
// "no price here" entry when the search was scoped to pinned markets and the
// store does not carry the product (Availability) . Variety discloses which
// name/variety the market actually sells (loose name match). BestPrice and
// WorstPrice are computed by the service at parse time (cheapest/most
// expensive offer of the product within one currency) — the UI only applies
// classes to them.
type OfferRow struct {
	Market       string            `json:"market"`
	Brand        string            `json:"brand,omitempty"`
	Variety      string            `json:"variety,omitempty"` // variety/name actually sold, e.g. "Hass"
	PriceCents   int64             `json:"price_cents"`
	Currency     string            `json:"currency"`
	IsOffer      bool              `json:"is_offer"`               // true = explicit promotion
	Availability OfferAvailability `json:"availability,omitempty"` // omitted = available
	Note         string            `json:"note,omitempty"`
	BestPrice    bool              `json:"best_price,omitempty"`  // computed: cheapest of its product+currency
	WorstPrice   bool              `json:"worst_price,omitempty"` // computed: most expensive
}

// OfferProductResult is the set of offers found for one cart product.
type OfferProductResult struct {
	ProductID int64      `json:"product_id"`
	Name      string     `json:"name"`
	Brand     string     `json:"brand,omitempty"` // requested brand, echoed
	Offers    []OfferRow `json:"offers"`
	Note      string     `json:"note,omitempty"` // e.g. "nothing found for this product"
}

// OfferResult is the normalized model output persisted as result_json.
// CannotSearch/Reason are set when the model reports it cannot browse the web;
// the service rejects those with an actionable error instead of persisting.
type OfferResult struct {
	Products     []OfferProductResult `json:"products"`
	CannotSearch bool                 `json:"cannot_search,omitempty"`
	Reason       string               `json:"reason,omitempty"`
	SearchedAt   string               `json:"searched_at"` // RFC3339
}

// OfferSearchRequest is the search scope snapshot persisted in request_json:
// the cart lines plus the store pinning and name-match mode at search time
// (retry reuses them).
type OfferSearchRequest struct {
	Products  []OfferSearchProduct `json:"products"`
	Stores    []string             `json:"stores,omitempty"`     // pinned markets; empty = any local market
	NameMatch OfferNameMatch       `json:"name_match,omitempty"` // omitted = strict
}

// OfferSearch is the wire shape of one search (mirrors BillScan): created with
// status searching, polled by token until done (Result) or failed (Error).
type OfferSearch struct {
	ID          int64                `json:"-"`
	SearchToken string               `json:"search_token"`
	Status      OfferSearchStatus    `json:"status"`
	ProviderID  string               `json:"provider_id,omitempty"`
	Products    []OfferSearchProduct `json:"products,omitempty"` // from request_json
	Stores      []string             `json:"stores,omitempty"`   // pinned markets; empty = any
	NameMatch   OfferNameMatch       `json:"name_match,omitempty"`
	Result      *OfferResult         `json:"result,omitempty"` // null unless done
	Error       string               `json:"error,omitempty"`  // set when failed
	CreatedAt   time.Time            `json:"created_at"`
	UpdatedAt   time.Time            `json:"-"`
}

// OfferSearchInput is the POST body when confirming a cart: cart lines by
// product id, optionally scoped to pinned stores and a name-match mode.
type OfferSearchInput struct {
	Items     []OfferSearchInputItem `json:"items"`
	Stores    []string               `json:"stores,omitempty"`     // pinned markets; empty = any local market
	NameMatch OfferNameMatch         `json:"name_match,omitempty"` // omitted = strict
}

// OfferSearchInputItem carries the product id plus optional quantity and a
// brand preference overriding the product's own brand hint.
type OfferSearchInputItem struct {
	ProductID int64   `json:"product_id"`
	Quantity  float64 `json:"quantity,omitempty"` // default 1
	Brand     string  `json:"brand,omitempty"`
}

// ErrCannotSearch is returned by the extractor when the configured provider
// cannot browse the web (reports it itself, or answered from memory without a
// search tool). Services turn it into an actionable message naming families
// that can search.
var ErrCannotSearch = errors.New("provider cannot search the web")
