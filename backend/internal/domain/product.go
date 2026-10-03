package domain

import "time"

// Product is a thing the user has bought, find-or-created from bill item and
// manual transaction item names. Lines link to their product via
// bill_items.product_id and transaction_items.product_id and are snapshots:
// they keep the name/unit/category they were created with — product edits
// update the products row only (the merge flow is the one exception,
// rewriting the items it redirects). ImagePath is the server-side file path and is
// never exposed; HasImage tells the UI whether GET /products/{id}/photo
// returns an image. The purchase stats are display-only, computed from linked
// accepted bill lines and manual transaction item lines.
type Product struct {
	ID           int64    `json:"id"`
	Name         string   `json:"name"`
	StandardName string   `json:"standard_name,omitempty"` // display-only, joined from product_name_mappings (raw name stays authoritative)
	GenericName  string   `json:"generic_name,omitempty"`  // display-only, joined from product_name_mappings (product family; empty = no broader family known)
	Brand        string   `json:"brand,omitempty"`
	Unit         string   `json:"unit,omitempty"`       // measure: kg, g, l, ml, pcs, …
	UnitValue    *float64 `json:"unit_value,omitempty"` // printed size magnitude ("500" for "500ml"); nil = unknown
	CategoryID   *int64   `json:"category_id"`
	CategoryName string   `json:"category_name,omitempty"` // display-only, joined from categories
	Description  string   `json:"description,omitempty"`
	ImagePath    string   `json:"-"`
	HasImage     bool     `json:"has_image"`

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
	// LastStoreName is the store of the most recent purchase, display-only. It
	// is filled by the grouped catalogue listing alone (the flat list keeps
	// its column set); "—" when the newest bill had no store.
	LastStoreName string `json:"last_store_name,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// MappingSource records who decided a raw→standard name mapping.
type MappingSource string

const (
	// MappingSourceAI marks suggestions recorded by AI extraction or the
	// "analyze existing products" job.
	MappingSourceAI MappingSource = "ai"
	// MappingSourceUser marks explicit corrections from the bill draft review.
	MappingSourceUser MappingSource = "user"
	// MappingSourceManual marks names typed in the manual expense form.
	MappingSourceManual MappingSource = "manual"
)

// ProductNameMapping is one remembered normalization decision: the raw text
// as printed on a receipt (or typed by hand) resolves to StandardName (+ an
// optional generic product family + an optional category) until the user
// overrides it in review. RawName is unique case-insensitively; several raw
// texts may share one StandardName — product rows keep their raw names and
// only link to the standardized form through this table. GenericName is the
// product family ("POTATO MINIONS 450G" → "Frozen Shaped Potatoes"); empty
// means "no broader family known" (it never falls back to the raw name).
// ProductID is the convenience link to the product the raw text currently
// resolves to — maintained opportunistically; lookups stay name-based.
type ProductNameMapping struct {
	ID           int64         `json:"id"`
	RawName      string        `json:"raw_name"`
	StandardName string        `json:"standard_name"`
	GenericName  string        `json:"generic_name"`
	CategoryID   *int64        `json:"category_id,omitempty"`
	ProductID    *int64        `json:"product_id,omitempty"`
	CategoryName string        `json:"category_name,omitempty"` // display-only, joined from categories
	Source       MappingSource `json:"source"`
	CreatedAt    time.Time     `json:"created_at"`
	UpdatedAt    time.Time     `json:"updated_at"`
}

// ProductNormalizationJobStatus is the state of the user-triggered
// "analyze existing products" job (single row, id 1).
type ProductNormalizationJobStatus string

const (
	ProductNormalizationIdle    ProductNormalizationJobStatus = "idle"
	ProductNormalizationRunning ProductNormalizationJobStatus = "running"
	ProductNormalizationDone    ProductNormalizationJobStatus = "done"
	ProductNormalizationFailed  ProductNormalizationJobStatus = "failed"
)

// ProductNormalizationJob reports the progress of the backfill job that
// asks the AI to standardize the raw names of existing products and stores
// the mappings. Products themselves are never modified.
type ProductNormalizationJob struct {
	Status         ProductNormalizationJobStatus `json:"status"`
	TotalNames     int64                         `json:"total_names"`
	ProcessedNames int64                         `json:"processed_names"`
	MappedNames    int64                         `json:"mapped_names"`
	Error          string                        `json:"error,omitempty"`
	CreatedAt      time.Time                     `json:"created_at"`
	UpdatedAt      time.Time                     `json:"updated_at"`
}

// ProductGroup is one row of the grouped product catalogue: every product
// whose normalization mapping shares a GenericName collapses into one family
// row, with the member products embedded. A product with no generic family
// (empty GenericName) forms its own single-member group under its raw name,
// so every product is listed exactly once. AvgPriceCents averages the members'
// own average prices, quoted in PriceCurrency — the currency of the group's
// most recent purchase — so unlike currencies never mix.
type ProductGroup struct {
	GenericName      string    `json:"generic_name"`
	CategoryName     string    `json:"category_name,omitempty"` // category of the most-bought member
	ProductCount     int       `json:"product_count"`
	TimesBought      int64     `json:"times_bought"`
	LastPurchaseDate string    `json:"last_purchase_date,omitempty"` // YYYY-MM-DD
	AvgPriceCents    *int64    `json:"avg_price_cents,omitempty"`
	BestPriceCents   *int64    `json:"best_price_cents,omitempty"`
	PriceCurrency    string    `json:"price_currency,omitempty"`
	Items            []Product `json:"items"`
}

// ProductGroupPage is the paged grouped-list result (the same envelope as
// ProductPage; sorting and the name filter work on the group, not the member).
type ProductGroupPage struct {
	Items  []ProductGroup `json:"items"`
	Total  int64          `json:"total"`
	Limit  int            `json:"limit"`
	Offset int            `json:"offset"`
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
