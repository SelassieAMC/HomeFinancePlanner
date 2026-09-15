package domain

import "time"

// Product is a thing the user has bought, find-or-created from bill item and
// manual transaction item names. Lines link to their product via
// bill_items.product_id and transaction_items.product_id; editing
// name/unit/category propagates to those lines (line names are not snapshots,
// unlike bills' market_name). ImagePath is the server-side file path and is
// never exposed; HasImage tells the UI whether GET /products/{id}/photo
// returns an image. The purchase stats are display-only, computed from linked
// accepted bill lines and manual transaction item lines.
type Product struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Brand        string `json:"brand,omitempty"`
	Unit         string `json:"unit,omitempty"` // measure: kg, g, l, ml, pcs, …
	CategoryID   *int64 `json:"category_id"`
	CategoryName string `json:"category_name,omitempty"` // display-only, joined from categories
	Description  string `json:"description,omitempty"`
	ImagePath    string `json:"-"`
	HasImage     bool   `json:"has_image"`

	// Display-only purchase stats, computed from linked accepted bill lines
	// and manual transaction item lines.
	TimesBought      int64  `json:"times_bought"`
	LastPurchaseDate string `json:"last_purchase_date,omitempty"` // bills.date text, YYYY-MM-DD
	// Latest/Avg/Best price are native-currency amounts; Latest is from the
	// most recent purchase, Best is the lowest ever paid. All are quoted in
	// PriceCurrency (the most recent purchase's currency) so currencies never
	// mix.
	LatestPriceCents *int64 `json:"latest_price_cents,omitempty"`
	AvgPriceCents    *int64 `json:"avg_price_cents,omitempty"`
	BestPriceCents   *int64 `json:"best_price_cents,omitempty"`
	PriceCurrency    string `json:"price_currency,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ProductFilters narrows the paged product list. Sort must be one of the
// repository's whitelisted keys; Order is "asc" | "desc" (default: name asc).
type ProductFilters struct {
	Name       string // substring match, case-insensitive
	CategoryID *int64
	Sort       string
	Order      string
	Limit      int
	Offset     int
}

// ProductPage is the paged list result.
type ProductPage struct {
	Items  []Product `json:"items"`
	Total  int64     `json:"total"`
	Limit  int       `json:"limit"`
	Offset int       `json:"offset"`
}

// ProductStorePrice is the latest price a product was bought at, per store.
// The latest price and last purchase date come from the product's most recent
// purchases; the list is currency-scoped by the service so amounts never mix.
type ProductStorePrice struct {
	StoreID          *int64 `json:"store_id,omitempty"`
	StoreName        string `json:"store_name"` // "—" when the bill had no store
	LatestPriceCents *int64 `json:"latest_price_cents,omitempty"`
	// Currency of LatestPriceCents. The /prices endpoint is currency-scoped by
	// the service, so it is redundant there; the merge check uses unscoped
	// per-store rows, where two products can be priced in different currencies.
	Currency         string `json:"currency,omitempty"`
	LastPurchaseDate string `json:"last_purchase_date,omitempty"`
}
