package domain

import "time"

// ProductInsightKind is the trigger a saved insight came from. The analysis
// worker detects them deterministically from price-per-unit history; the AI
// only phrases the message.
type ProductInsightKind string

const (
	// ProductInsightShrinkflation marks the "pack got smaller, price stayed" finding.
	ProductInsightShrinkflation ProductInsightKind = "shrinkflation"
	// ProductInsightBulkBuy marks the "bigger pack costs notably less per unit" finding.
	ProductInsightBulkBuy ProductInsightKind = "bulk_buy"
	// ProductInsightPriceCreep marks the "price per unit keeps rising" finding.
	ProductInsightPriceCreep ProductInsightKind = "price_creep"
)

func (k ProductInsightKind) Valid() bool {
	switch k {
	case ProductInsightShrinkflation, ProductInsightBulkBuy, ProductInsightPriceCreep:
		return true
	}
	return false
}

// ProductInsight is one persisted purchase nudge: a deterministic finding
// about a product's price-per-unit history, phrased by the AI (source "ai")
// or, when no connector is configured or the call fails, kept in its plain
// auto-generated wording (source "auto"). Rows are the kept record — they are
// never swept, only acknowledged (the dashboard lists the unread ones).
type ProductInsight struct {
	ID           int64              `json:"id"`
	Kind         ProductInsightKind `json:"kind"`
	ProductID    *int64             `json:"product_id,omitempty"` // nil after the product row was deleted (FK SET NULL)
	ProductName  string             `json:"product_name"`
	GenericName  string             `json:"generic_name,omitempty"`
	Currency     string             `json:"currency"` // ISO 4217 code of the compared purchases
	Message      string             `json:"message"`
	Source       string             `json:"source"` // "ai" | "auto"
	Data         ProductInsightData `json:"data"`   // decoded facts snapshot (data_json)
	Acknowledged bool               `json:"acknowledged"`
	CreatedAt    time.Time          `json:"created_at"`
	UpdatedAt    time.Time          `json:"updated_at"`
}

// ProductInsightPurchase is one row of the facts data_json: a purchase the
// finding was computed from (date, store, price, size and the resulting
// price-per-unit normalized to the canonical unit).
type ProductInsightPurchase struct {
	Date            string  `json:"date"`
	StoreName       string  `json:"store_name,omitempty"`
	UnitPriceCents  int64   `json:"unit_price_cents"`
	UnitValue       float64 `json:"unit_value"`
	Unit            string  `json:"unit"`
	PPUCentsPerUnit float64 `json:"ppu_cents_per_unit"`
}

// ProductInsightData is the JSON facts snapshot persisted alongside the
// message (data_json): the compared purchases and the headline numbers the
// evaluator computed (shrinkflation/price-creep rows carry old vs new,
// bulk-buy rows the small vs large size averages).
type ProductInsightData struct {
	Unit          string                   `json:"unit"` // canonical comparison unit
	Threshold     float64                  `json:"threshold_pct"`
	Purchases     []ProductInsightPurchase `json:"purchases,omitempty"`
	OldPriceCents *int64                   `json:"old_unit_price_cents,omitempty"`
	NewPriceCents *int64                   `json:"new_unit_price_cents,omitempty"`
	OldUnitValue  *float64                 `json:"old_unit_value,omitempty"`
	NewUnitValue  *float64                 `json:"new_unit_value,omitempty"`
	OldPPU        *float64                 `json:"old_ppu_cents_per_unit,omitempty"`
	NewPPU        *float64                 `json:"new_ppu_cents_per_unit,omitempty"`
	ChangePct     float64                  `json:"change_pct,omitempty"`
}

// InsightLine is one purchase line the analysis worker evaluates — an
// analyzable line (linked product, real spend, known size) freshly saved on
// an accepted bill or a manual transaction.
type InsightLine struct {
	ProductID      int64    `json:"product_id"`
	Name           string   `json:"name"`
	Unit           string   `json:"unit"`
	UnitValue      *float64 `json:"unit_value"`
	UnitPriceCents int64    `json:"unit_price_cents"`
	Currency       string   `json:"currency"`
	Date           string   `json:"date"` // YYYY-MM-DD of the owning purchase
}

// ProductHistoryEntry is one stored, analyzable purchase of a product (or of
// a product family, for the bulk-buy read) — the raw input of the PPU trigger
// analysis, newest first per query. Source/SourceID locate the owning
// purchase ("b"=bills, "t"=transactions) so the analysis can exclude the
// just-saved lines; they are not API surface.
type ProductHistoryEntry struct {
	ProductID      int64    `json:"product_id"`
	Name           string   `json:"name"`
	Unit           string   `json:"unit"`
	UnitValue      *float64 `json:"unit_value"`
	UnitPriceCents int64    `json:"unit_price_cents"`
	Currency       string   `json:"currency"`
	Date           string   `json:"date"` // YYYY-MM-DD of the owning purchase
	StoreName      string   `json:"store_name,omitempty"`
	Source         string   `json:"-"`
	SourceID       int64    `json:"-"`
}

// IsSource reports whether this entry was stored by the given purchase
// ("b"/"t" + its row id).
func (e ProductHistoryEntry) IsSource(source string, id int64) bool {
	return e.Source == source && e.SourceID == id
}

// InsightFilters narrow the listed insights. Unseen lists only rows the user
// has not acknowledged yet.
type InsightFilters struct {
	Unseen bool
	Limit  int
}
