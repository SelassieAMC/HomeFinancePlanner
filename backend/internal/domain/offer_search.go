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

// OfferRow is one price found for one product in one market. BestPrice and
// WorstPrice are computed by the service at parse time (cheapest/most
// expensive offer of the product within one currency) — the UI only applies
// classes to them.
type OfferRow struct {
	Market     string `json:"market"`
	Brand      string `json:"brand,omitempty"`
	PriceCents int64  `json:"price_cents"`
	Currency   string `json:"currency"`
	IsOffer    bool   `json:"is_offer"` // true = explicit promotion
	Note       string `json:"note,omitempty"`
	BestPrice  bool   `json:"best_price,omitempty"`  // computed: cheapest of its product+currency
	WorstPrice bool   `json:"worst_price,omitempty"` // computed: most expensive
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

// OfferSearch is the wire shape of one search (mirrors BillScan): created with
// status searching, polled by token until done (Result) or failed (Error).
type OfferSearch struct {
	ID          int64                `json:"-"`
	SearchToken string               `json:"search_token"`
	Status      OfferSearchStatus    `json:"status"`
	ProviderID  string               `json:"provider_id,omitempty"`
	Products    []OfferSearchProduct `json:"products,omitempty"` // from request_json
	Result      *OfferResult         `json:"result,omitempty"`   // null unless done
	Error       string               `json:"error,omitempty"`    // set when failed
	CreatedAt   time.Time            `json:"created_at"`
	UpdatedAt   time.Time            `json:"-"`
}

// OfferSearchInput is the POST body when confirming a cart: cart lines by
// product id.
type OfferSearchInput struct {
	Items []OfferSearchInputItem `json:"items"`
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
